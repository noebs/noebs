package keycloakadmin

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These cases exercise Keycloak's hosted forms over HTTPS. They deliberately
// do not use direct grants or admin-created passwords to stand in for signup.
func assertRealLocalAccounts(t *testing.T, baseURL string, transport http.RoundTripper, reconciler *Reconciler, state DesiredState, mailbox <-chan string) {
	t.Helper()
	for _, account := range []struct {
		name, username, email string
		walletSignup          bool
	}{
		{"Email", "native-account", "native-account@example.invalid", true},
		{"Phone", "+971500001234", "", false},
	} {
		t.Run(account.name, func(t *testing.T) {
			login := account.username
			if account.email != "" {
				login = account.email
			}
			client := realLocalBrowser(t, transport)
			authorization := realLocalAuthorizationURL(t, baseURL, "native-signup", account.walletSignup)
			page, body := realLocalGet(t, client, authorization)
			assertRealLocalLoginPage(t, body)
			registration := realLocalMatch(t, body, `<a[^>]*href="([^"]*/login-actions/registration[^\"]*)"`, "registration link")
			page, body = realLocalGet(t, client, realLocalURL(t, page, registration))
			action := realLocalForm(t, page, body, "kc-register-form")
			const password = "Native-test-password-4729"
			values := url.Values{"username": {account.username}, "email": {account.email}, "firstName": {"Native"}, "lastName": {"Account"}}
			invalidUsername := "+000"
			if account.email != "" {
				invalidUsername = "unverified@example.invalid"
			}
			values.Set("username", invalidUsername)
			values.Set("email", "")
			page, body = realLocalPost(t, client, action, values)
			action = realLocalForm(t, page, body, "kc-register-form")
			values.Set("username", account.username)
			values.Set("email", account.email)
			page, body = realLocalPost(t, client, action, values)

			session := mustRealAdminSession(t, reconciler)
			var users []userRepresentation
			if _, err := session.get(context.Background(), realmPath(state.Realm.Name)+"/users?exact=true&username="+url.QueryEscape(account.username), &users); err != nil || len(users) != 1 {
				t.Fatal("native registration did not create exactly one user")
			}
			userPath := realmPath(state.Realm.Name) + "/users/" + url.PathEscape(users[0].ID)
			t.Cleanup(func() {
				if err := session.delete(context.Background(), userPath, nil); err != nil {
					t.Error("native test user cleanup failed")
				}
			})
			var secret string
			var lastOTP time.Time
			verifiedEmail := false
			passwordSet := false
			for attempts := 0; page.Host != "api.noebs.sd" && attempts < 5; attempts++ {
				switch {
				case strings.Contains(string(body), `id="kc-passwd-update-form"`):
					action = realLocalForm(t, page, body, "kc-passwd-update-form")
					page, body = realLocalPost(t, client, action, url.Values{"password-new": {"short"}, "password-confirm": {"short"}})
					if !strings.Contains(string(body), `id="kc-passwd-update-form"`) {
						t.Fatal("native password setup accepted a password shorter than the managed policy")
					}
					action = realLocalForm(t, page, body, "kc-passwd-update-form")
					page, body = realLocalPost(t, client, action, url.Values{"password-new": {password}, "password-confirm": {password}})
					passwordSet = true
				case strings.Contains(string(body), `id="kc-totp-settings-form"`):
					secret = realLocalMatch(t, body, `<input[^>]*name="totpSecret"[^>]*value="([^"]+)"`, "OTP setup secret")
					action = realLocalForm(t, page, body, "kc-totp-settings-form")
					lastOTP = time.Now()
					page, body = realLocalPost(t, client, action, url.Values{"totp": {realLocalTOTP(secret, lastOTP)}, "totpSecret": {secret}, "userLabel": {"native-integration"}})
				case strings.Contains(string(body), "verify-email") || strings.Contains(string(body), "verification link"):
					if account.email == "" {
						t.Fatal("phone-only registration unexpectedly requires an email")
					}
					var credentials []map[string]any
					if _, err := session.get(context.Background(), userPath+"/credentials", &credentials); err != nil || len(credentials) != 0 {
						t.Fatal("email registration installed credentials before proving mailbox control")
					}
					page, body = realLocalGet(t, client, realLocalEmailLink(t, mailbox, baseURL))
					verifiedEmail = true
				default:
					t.Fatalf("native registration stopped at unexpected page %s: %s", page.Path, realLocalPageSummary(body))
				}
			}
			assertRealLocalCallback(t, page, "native-signup")
			if account.walletSignup {
				exchangeRealAuthorizationCode(t, client, baseURL, page.Query().Get("code"), realLocalVerifier("native-signup"), reconciler.config.ClientCredentials[walletAuthorizerClientID].ClientSecret, "native-signup-nonce")
			}
			if secret == "" || !passwordSet || (account.email != "" && !verifiedEmail) {
				t.Fatal("native registration skipped password setup, OTP enrollment or email verification")
			}
			var user struct {
				Email           string   `json:"email"`
				EmailVerified   bool     `json:"emailVerified"`
				RequiredActions []string `json:"requiredActions"`
			}
			if _, err := session.get(context.Background(), userPath, &user); err != nil || user.Email != account.email || len(user.RequiredActions) != 0 || (account.email != "" && !user.EmailVerified) {
				t.Fatal("registered account has unexpected email verification or required actions")
			}

			// A new browser must reject the wrong password before issuing a code.
			client = realLocalBrowser(t, transport)
			page, body = realLocalGet(t, client, realLocalAuthorizationURL(t, baseURL, "native-login", false))
			action = realLocalForm(t, page, body, "kc-form-login")
			page, body = realLocalPost(t, client, action, url.Values{"username": {login}, "password": {"incorrect-password"}})
			if page.Host == "api.noebs.sd" || !strings.Contains(string(body), `name="password"`) {
				t.Fatal("incorrect native password was not rejected at login")
			}
			action = realLocalForm(t, page, body, "kc-form-login")
			page, _ = realLocalPost(t, client, action, url.Values{"username": {login}, "password": {password}})
			assertRealLocalCallback(t, page, "native-login")

			currentPassword := password
			if account.email != "" {
				currentPassword = "Reset-native-password-4829"
				lastOTP = assertRealLocalPasswordReset(t, baseURL, transport, login, password, currentPassword, secret, lastOTP, reconciler.config.ClientCredentials[walletAuthorizerClientID].ClientSecret, mailbox)
			}

			// max_age=0 must run both primary credentials and a new OTP even
			// with the previous browser session and a requested primary ACR.
			var previousAuthTime time.Time
			for index := 1; index <= 2; index++ {
				nextPeriod := time.Unix((lastOTP.Unix()/30+1)*30, 0)
				if delay := time.Until(nextPeriod); delay > 0 {
					time.Sleep(delay)
				}
				authState := fmt.Sprintf("native-step-up-%d", index)
				started := time.Now().Truncate(time.Second)
				page, body = realLocalGet(t, client, realLocalAuthorizationURL(t, baseURL, authState, true))
				action = realLocalForm(t, page, body, "kc-form-login")
				page, body = realLocalPost(t, client, action, url.Values{"username": {login}, "password": {currentPassword}})
				action = realLocalForm(t, page, body, "kc-otp-login-form")
				lastOTP = time.Now()
				page, _ = realLocalPost(t, client, action, url.Values{"otp": {realLocalTOTP(secret, lastOTP)}, "selectedCredentialId": {""}})
				assertRealLocalCallback(t, page, authState)
				authTime := exchangeRealAuthorizationCode(t, client, baseURL, page.Query().Get("code"), realLocalVerifier(authState), reconciler.config.ClientCredentials[walletAuthorizerClientID].ClientSecret, authState+"-nonce")
				if authTime.Before(started) || !authTime.After(previousAuthTime) {
					t.Fatal("native MFA authorization reused an earlier authentication time")
				}
				previousAuthTime = authTime
			}
			t.Log("native signup, OTP enrollment, wrong-password rejection, primary login and two fresh MFA authorizations passed")
		})
	}
}

func assertRealLocalPasswordReset(t *testing.T, baseURL string, transport http.RoundTripper, username, oldPassword, newPassword, otpSecret string, lastOTP time.Time, clientSecret string, mailbox <-chan string) time.Time {
	t.Helper()
	client := realLocalBrowser(t, transport)
	page, body := realLocalGet(t, client, realLocalAuthorizationURL(t, baseURL, "native-password-reset", true))
	link := realLocalMatch(t, body, `<a[^>]*href="([^"]*/login-actions/reset-credentials[^\"]*)"`, "password reset link")
	page, body = realLocalGet(t, client, realLocalURL(t, page, link))
	action := realLocalForm(t, page, body, "kc-reset-password-form")
	page, body = realLocalPost(t, client, action, url.Values{"username": {username}})
	page, body = realLocalGet(t, client, realLocalEmailLink(t, mailbox, baseURL))
	sawOTP, updatedPassword := false, false
	for attempts := 0; page.Host != "api.noebs.sd" && attempts < 3; attempts++ {
		switch {
		case strings.Contains(string(body), `id="kc-otp-login-form"`):
			action = realLocalForm(t, page, body, "kc-otp-login-form")
			if delay := time.Until(time.Unix((lastOTP.Unix()/30+1)*30, 0)); delay > 0 {
				time.Sleep(delay)
			}
			lastOTP = time.Now()
			page, body = realLocalPost(t, client, action, url.Values{"otp": {realLocalTOTP(otpSecret, lastOTP)}, "selectedCredentialId": {""}})
			sawOTP = true
		case strings.Contains(string(body), `id="kc-passwd-update-form"`):
			action = realLocalForm(t, page, body, "kc-passwd-update-form")
			page, body = realLocalPost(t, client, action, url.Values{"password-new": {newPassword}, "password-confirm": {newPassword}})
			updatedPassword = true
		default:
			t.Fatalf("wallet password recovery returned an unexpected page at %s: %s", page.Path, realLocalPageSummary(body))
		}
	}
	assertRealLocalCallback(t, page, "native-password-reset")
	if !sawOTP || !updatedPassword {
		t.Fatal("wallet password recovery skipped existing OTP verification or password update")
	}
	exchangeRealAuthorizationCode(t, client, baseURL, page.Query().Get("code"), realLocalVerifier("native-password-reset"), clientSecret, "native-password-reset-nonce")
	client = realLocalBrowser(t, transport)
	page, body = realLocalGet(t, client, realLocalAuthorizationURL(t, baseURL, "native-old-password", false))
	action = realLocalForm(t, page, body, "kc-form-login")
	page, body = realLocalPost(t, client, action, url.Values{"username": {username}, "password": {oldPassword}})
	if page.Host == "api.noebs.sd" || !strings.Contains(string(body), `name="password"`) {
		t.Fatal("password reset left the previous password usable")
	}
	// The next payment login proves the new password works and the existing
	// OTP credential survived recovery.
	return lastOTP
}

func realLocalBrowser(t *testing.T, transport http.RoundTripper) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func realLocalVerifier(state string) string {
	return "noebs-native-authorization-pkce-verifier-0000000000-" + state
}

func realLocalAuthorizationURL(t *testing.T, baseURL, state string, wallet bool) string {
	t.Helper()
	authorization, err := url.Parse(realWalletAuthorizationURL(t, baseURL, realLocalVerifier(state), state, state+"-nonce"))
	if err != nil {
		t.Fatal(err)
	}
	query := authorization.Query()
	query.Del("kc_idp_hint")
	query.Del("login_hint")
	query.Set("ui_locales", "en")
	if !wallet {
		query.Set("client_id", realMobileClientID)
		query.Set("redirect_uri", realMobileRedirect)
		query.Set("scope", "openid organization:*")
		query.Del("max_age")
	}
	authorization.RawQuery = query.Encode()
	return authorization.String()
}

func realLocalURL(t *testing.T, page *url.URL, link string) string {
	t.Helper()
	target, err := page.Parse(link)
	if err != nil || target.Scheme != page.Scheme || target.Host != page.Host {
		t.Fatal("native account link left the isolated issuer")
	}
	return target.String()
}

func realLocalGet(t *testing.T, client *http.Client, target string) (*url.URL, []byte) {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal("native browser request failed")
	}
	return realLocalFollow(t, client, response)
}

func realLocalPost(t *testing.T, client *http.Client, target string, values url.Values) (*url.URL, []byte) {
	t.Helper()
	response, err := client.PostForm(target, values)
	if err != nil {
		t.Fatal("native form submission failed")
	}
	return realLocalFollow(t, client, response)
}

func realLocalFollow(t *testing.T, client *http.Client, response *http.Response) (*url.URL, []byte) {
	t.Helper()
	issuer := response.Request.URL.Host
	for range 16 {
		body := readRealResponse(t, response)
		if response.StatusCode == http.StatusOK {
			return response.Request.URL, body
		}
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			t.Fatalf("native flow HTTP status %d at %s: %s", response.StatusCode, response.Request.URL.Path, realLocalPageSummary(body))
		}
		next, err := response.Request.URL.Parse(response.Header.Get("Location"))
		if err != nil || next.Scheme != "https" || next.User != nil {
			t.Fatal("native flow returned an invalid redirect")
		}
		if next.Host == "api.noebs.sd" {
			return next, nil
		}
		if next.Host != issuer {
			t.Fatal("native flow unexpectedly left the isolated issuer")
		}
		response, err = client.Get(next.String())
		if err != nil {
			t.Fatal("native redirect request failed")
		}
	}
	t.Fatal("native flow redirect limit exceeded")
	return nil, nil
}

func assertRealLocalCallback(t *testing.T, callback *url.URL, state string) {
	t.Helper()
	if callback.Host != "api.noebs.sd" || callback.Query().Get("state") != state || callback.Query().Get("code") == "" || callback.Query().Get("error") != "" {
		t.Fatalf("native authorization did not complete successfully at %s", callback.Path)
	}
}

func assertRealLocalLoginPage(t *testing.T, body []byte) {
	t.Helper()
	for _, marker := range []string{`name="username"`, `name="password"`, `/broker/google/login`} {
		if !strings.Contains(string(body), marker) {
			t.Fatalf("shared login page lacks %s", marker)
		}
	}
}

func realLocalForm(t *testing.T, page *url.URL, body []byte, id string) string {
	t.Helper()
	forms := regexp.MustCompile(`<form\b[^>]*>`).FindAllString(string(body), -1)
	for _, form := range forms {
		if !strings.Contains(form, `id="`+id+`"`) {
			continue
		}
		action := realLocalMatch(t, []byte(form), `action="([^"]+)"`, id+" action")
		target, err := page.Parse(action)
		if err != nil || target.Scheme != page.Scheme || target.Host != page.Host {
			t.Fatal("native form action left the isolated issuer")
		}
		return target.String()
	}
	t.Fatalf("missing native form %s at %s: %s", id, page.Path, realLocalPageSummary(body))
	return ""
}

func realLocalMatch(t *testing.T, body []byte, pattern, field string) string {
	t.Helper()
	matches := regexp.MustCompile(pattern).FindAllSubmatch(body, -1)
	if len(matches) != 1 || len(matches[0]) != 2 {
		t.Fatalf("missing or duplicate native %s: %s", field, realLocalPageSummary(body))
	}
	return html.UnescapeString(string(matches[0][1]))
}

func realLocalPageSummary(body []byte) string {
	// Report headings/errors without dumping form actions, OTP secrets or codes.
	matches := regexp.MustCompile(`<(?:h1|span|p)[^>]*(?:kc-page-title|error|instruction)[^>]*>([^<]*)`).FindAllSubmatch(body, -1)
	var messages []string
	for _, match := range matches {
		messages = append(messages, strings.TrimSpace(html.UnescapeString(string(match[1]))))
	}
	return strings.Join(messages, "; ")
}

func realLocalTOTP(secret string, now time.Time) string {
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write(counter)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(digest[offset:offset+4])&0x7fffffff)%1_000_000)
}

func startRealSMTP(t *testing.T) (<-chan string, *SMTPConfig) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 16)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
				reader := bufio.NewReader(connection)
				_, _ = fmt.Fprint(connection, "220 localhost native integration SMTP\r\n")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					switch command := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
						_, _ = fmt.Fprint(connection, "250 localhost\r\n")
					case command == "DATA":
						_, _ = fmt.Fprint(connection, "354 Send message\r\n")
						var body strings.Builder
						for {
							line, err = reader.ReadString('\n')
							if err != nil {
								return
							}
							if line == ".\r\n" {
								break
							}
							if strings.HasPrefix(line, "..") {
								line = line[1:]
							}
							body.WriteString(line)
						}
						messages <- body.String()
						_, _ = fmt.Fprint(connection, "250 Accepted\r\n")
					case command == "QUIT":
						_, _ = fmt.Fprint(connection, "221 Goodbye\r\n")
						return
					default:
						_, _ = fmt.Fprint(connection, "250 OK\r\n")
					}
				}
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	portNumber, _ := strconv.Atoi(port)
	return messages, &SMTPConfig{Host: "127.0.0.1", Port: portNumber, From: "noebs@example.invalid", FromDisplayName: "Native integration"}
}

func realLocalEmailLink(t *testing.T, mailbox <-chan string, baseURL string) string {
	t.Helper()
	var raw string
	select {
	case raw = <-mailbox:
	case <-time.After(10 * time.Second):
		t.Fatal("Keycloak did not send the required account email")
	}
	message, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal("Keycloak account email is malformed")
	}
	contentType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal("Keycloak email content type is malformed")
	}
	var bodies []string
	if strings.HasPrefix(contentType, "multipart/") {
		parts := multipart.NewReader(message.Body, params["boundary"])
		for {
			part, err := parts.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal("Keycloak account email MIME body is malformed")
			}
			bodies = append(bodies, realLocalEmailBody(t, part, part.Header.Get("Content-Transfer-Encoding")))
		}
	} else {
		bodies = append(bodies, realLocalEmailBody(t, message.Body, message.Header.Get("Content-Transfer-Encoding")))
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(baseURL) + `/realms/[^\s<>"']+/login-actions/action-token\?[^\s<>"']+`)
	for _, body := range bodies {
		if link := pattern.FindString(body); link != "" {
			return html.UnescapeString(link)
		}
	}
	t.Fatal("Keycloak account email lacks an action link for the isolated issuer")
	return ""
}

func realLocalEmailBody(t *testing.T, reader io.Reader, encoding string) string {
	t.Helper()
	switch strings.ToLower(encoding) {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	case "quoted-printable":
		reader = quotedprintable.NewReader(reader)
	}
	body, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil {
		t.Fatal("Keycloak email body cannot be decoded")
	}
	return string(body)
}
