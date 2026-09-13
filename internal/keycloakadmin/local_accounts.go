package keycloakadmin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// SMTPConfig is deployment configuration. Keycloak sends verification and
// recovery mail; application services never handle account credentials.
// External mail uses implicit TLS: Keycloak 26.7 STARTTLS is opportunistic.
type SMTPConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	From            string `yaml:"from"`
	FromDisplayName string `yaml:"from_display_name"`
	Username        string `yaml:"username"`
	Password        string `yaml:"password"`
	TLS             bool   `yaml:"tls"`
}

var smtpHostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*\.?$`)

func (s SMTPConfig) Validate() error {
	if strings.TrimSpace(s.Host) != s.Host || s.Host == "" || strings.ContainsAny(s.Host, "/@\r\n\t ") || s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("%w: smtp requires an explicit host and port", ErrInvalidConfig)
	}
	if net.ParseIP(s.Host) == nil && (len(s.Host) > 253 || !smtpHostnamePattern.MatchString(s.Host)) {
		return fmt.Errorf("%w: smtp.host must be a hostname or IP address without a port", ErrInvalidConfig)
	}
	address, err := mail.ParseAddress(s.From)
	if err != nil || address.Address != s.From || strings.ContainsAny(s.FromDisplayName, "\r\n") {
		return fmt.Errorf("%w: smtp.from must be an email address and display name must not contain line breaks", ErrInvalidConfig)
	}
	if (s.Username == "") != (s.Password == "") {
		return fmt.Errorf("%w: smtp username and password must be supplied together", ErrInvalidConfig)
	}
	loopback := s.Host == "localhost"
	if ip := net.ParseIP(s.Host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback && !s.TLS {
		return fmt.Errorf("%w: smtp requires implicit TLS outside loopback", ErrInvalidConfig)
	}
	return nil
}

func smtpRepresentation(config *SMTPConfig) map[string]string {
	if config == nil {
		return map[string]string{}
	}
	result := map[string]string{
		"host": config.Host, "port": strconv.Itoa(config.Port),
		"from": config.From, "fromDisplayName": config.FromDisplayName,
		"starttls": "false", "ssl": strconv.FormatBool(config.TLS),
		"auth": strconv.FormatBool(config.Username != ""),
	}
	if config.Username != "" {
		result["user"] = config.Username
		result["password"] = config.Password
	}
	return result
}

func reconcileSMTP(ctx context.Context, session *adminSession, realm string, config *SMTPConfig, result *Result) error {
	path := realmPath(realm)
	var current struct {
		SMTPServer map[string]string `json:"smtpServer"`
	}
	if _, err := session.get(ctx, path, &current); err != nil {
		return fmt.Errorf("read account email configuration: %w", err)
	}
	wanted := smtpRepresentation(config)
	comparison := cloneStringMap(current.SMTPServer)
	// Keycloak masks this secret on reads. Reassert it on each pass, as with
	// identity-provider secrets, so drift cannot hide behind the mask.
	masked := comparison["password"] == "**********"
	if masked {
		comparison["password"] = wanted["password"]
	}
	changed := !equalStringMap(comparison, wanted)
	if !changed && !masked {
		return nil
	}
	if err := session.put(ctx, path, map[string]any{"smtpServer": wanted}); err != nil {
		return fmt.Errorf("configure account email delivery: %w", err)
	}
	if changed {
		result.Updated++
	}
	return nil
}

// Use Keycloak's native user profile and registration form. Email is optional
// so a phone identifier does not require a mailbox. A username is never treated
// as proof of control of a phone or email, nor projected as a verified contact.
// Reserve @ for the verified email field: a username without an email must not
// be able to reserve another person's mailbox in Keycloak's login namespace.
func desiredLocalAccountProfile() map[string]any {
	permissions := map[string]any{"view": []string{"admin", "user"}, "edit": []string{"admin", "user"}}
	return map[string]any{
		// Keycloak represents disabled unmanaged attributes with a null policy.
		"unmanagedAttributePolicy": nil,
		"attributes": []map[string]any{
			{
				"name": "username", "displayName": "Username or phone number", "permissions": permissions, "multivalued": false,
				"validations": map[string]any{
					"length":                         map[string]any{"min": 1, "max": 255},
					"username-prohibited-characters": map[string]any{},
					"up-username-not-idn-homograph":  map[string]any{},
					"pattern":                        map[string]any{"pattern": `^(?:\+[1-9][0-9]{1,14}|[^+@\s][^@\s]*)$`, "error-message": "Enter a username without @ or an international phone number such as +249912345678. Use the email field for email sign-in."},
				},
			},
			{
				"name": "email", "displayName": "${email}", "permissions": permissions, "multivalued": false,
				"validations": map[string]any{"email": map[string]any{}, "length": map[string]any{"max": 255}},
			},
			{
				"name": "firstName", "displayName": "${firstName}", "permissions": permissions, "multivalued": false,
				"validations": map[string]any{"length": map[string]any{"max": 255}, "person-name-prohibited-characters": map[string]any{}},
			},
			{
				"name": "lastName", "displayName": "${lastName}", "permissions": permissions, "multivalued": false,
				"validations": map[string]any{"length": map[string]any{"max": 255}, "person-name-prohibited-characters": map[string]any{}},
			},
		},
		"groups": []any{},
	}
}

func reconcileLocalAccountProfile(ctx context.Context, session *adminSession, realm string, result *Result) error {
	path := realmPath(realm) + "/users/profile"
	var current map[string]any
	if _, err := session.get(ctx, path, &current); err != nil {
		return fmt.Errorf("read account user profile: %w", err)
	}
	wanted := desiredLocalAccountProfile()
	// Compare through JSON so numeric values and typed slices match API data.
	data, err := json.Marshal(wanted)
	if err != nil {
		return err
	}
	var comparison map[string]any
	if err := json.Unmarshal(data, &comparison); err != nil {
		return err
	}
	if _, present := current["unmanagedAttributePolicy"]; !present {
		current["unmanagedAttributePolicy"] = nil
	}
	if reflect.DeepEqual(current, comparison) {
		return nil
	}
	if err := session.put(ctx, path, wanted); err != nil {
		return fmt.Errorf("configure account user profile: %w", err)
	}
	result.Updated++
	return nil
}
