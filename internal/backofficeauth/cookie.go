package backofficeauth

import (
	"net/http"
	"strings"
	"time"
)

type CookiePolicyConfig struct {
	FlowName    string
	SessionName string
}

type CookiePolicy struct {
	flowName    string
	sessionName string
}

func NewCookiePolicy(config CookiePolicyConfig) (*CookiePolicy, error) {
	if !validHostCookieName(config.FlowName) || !validHostCookieName(config.SessionName) || config.FlowName == config.SessionName {
		return nil, ErrInvalidConfiguration
	}
	return &CookiePolicy{flowName: config.FlowName, sessionName: config.SessionName}, nil
}

func validHostCookieName(name string) bool {
	if !strings.HasPrefix(name, "__Host-") || len(name) == len("__Host-") {
		return false
	}
	return (&http.Cookie{Name: name, Value: "value"}).Valid() == nil
}

func (p *CookiePolicy) Flow(value string, now, expiresAt time.Time) (*http.Cookie, error) {
	if _, err := digestOpaque(value); err != nil {
		return nil, ErrInvalidFlow
	}
	return strictCookie(p.flowName, value, now, expiresAt)
}

func (p *CookiePolicy) Session(value string, now, expiresAt time.Time) (*http.Cookie, error) {
	if _, err := digestOpaque(value); err != nil {
		return nil, ErrSessionNotFound
	}
	return strictCookie(p.sessionName, value, now, expiresAt)
}

func strictCookie(name, value string, now, expiresAt time.Time) (*http.Cookie, error) {
	if !expiresAt.After(now) {
		return nil, ErrInvalidInput
	}
	maxAge := int(expiresAt.Sub(now) / time.Second)
	if maxAge < 1 {
		return nil, ErrInvalidInput
	}
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt.UTC(),
		MaxAge:   maxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}, nil
}

func (p *CookiePolicy) ClearFlow() *http.Cookie {
	return clearCookie(p.flowName)
}

func (p *CookiePolicy) ClearSession() *http.Cookie {
	return clearCookie(p.sessionName)
}

func clearCookie(name string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func (p *CookiePolicy) ReadFlow(request *http.Request) (string, error) {
	value, err := readExactCookie(request, p.flowName)
	if err != nil {
		return "", ErrInvalidFlow
	}
	return value, nil
}

func (p *CookiePolicy) ReadSession(request *http.Request) (string, error) {
	value, err := readExactCookie(request, p.sessionName)
	if err != nil {
		return "", ErrSessionNotFound
	}
	return value, nil
}

func readExactCookie(request *http.Request, name string) (string, error) {
	if request == nil {
		return "", ErrInvalidInput
	}
	var value string
	found := false
	for _, cookie := range request.Cookies() {
		if cookie.Name != name {
			continue
		}
		if found {
			return "", ErrInvalidInput
		}
		found = true
		value = cookie.Value
	}
	if !found {
		return "", ErrInvalidInput
	}
	if _, err := digestOpaque(value); err != nil {
		return "", err
	}
	return value, nil
}
