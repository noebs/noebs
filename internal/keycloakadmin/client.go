package keycloakadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxResponseBytes = 4 << 20

const BootstrapClientID = "noebs-keycloak-bootstrap"

var ErrUnexpectedResponse = errors.New("unexpected Keycloak response")

type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s: %s %s returned %d", ErrUnexpectedResponse, e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("%s: %s %s returned %d: %s", ErrUnexpectedResponse, e.Method, e.Path, e.StatusCode, e.Body)
}

func (e *HTTPError) Unwrap() error { return ErrUnexpectedResponse }

type Reconciler struct {
	config Config
	http   *http.Client
}

type Result struct {
	Created int
	Updated int
	Deleted int
}

func (r Result) Changed() bool {
	return r.Created+r.Updated+r.Deleted != 0
}

func New(config Config, httpClient *http.Client) (*Reconciler, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		return nil, fmt.Errorf("%w: http client is required", ErrInvalidConfig)
	}
	return &Reconciler{config: config, http: httpClient}, nil
}

// DeleteBootstrapClient removes the temporary master-realm client after the
// first reconcile has provisioned the realm-local reconciler service account.
func (r *Reconciler) DeleteBootstrapClient(ctx context.Context) error {
	if r.config.AdminRealm != "master" || r.config.ClientID != BootstrapClientID {
		return fmt.Errorf("%w: bootstrap deletion requires the %s client in the master realm", ErrInvalidConfig, BootstrapClientID)
	}
	session, err := r.session(ctx)
	if err != nil {
		return err
	}
	client, found, err := findClient(ctx, session, realmPath("master"), BootstrapClientID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: bootstrap client %s does not exist", ErrUnexpectedResponse, BootstrapClientID)
	}
	if err := session.delete(ctx, realmPath("master")+"/clients/"+url.PathEscape(client.ID), nil); err != nil {
		return fmt.Errorf("delete bootstrap client: %w", err)
	}
	return nil
}

type adminSession struct {
	baseURL string
	token   string
	http    *http.Client
}

func (r *Reconciler) session(ctx context.Context) (*adminSession, error) {
	tokenPath := "/realms/" + url.PathEscape(r.config.AdminRealm) + "/protocol/openid-connect/token"
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {r.config.ClientID},
		"client_secret": {r.config.ClientSecret},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.BaseURL+tokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create Keycloak token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := r.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request Keycloak admin token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError(request.Method, tokenPath, response)
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := decodeJSONResponse(response.Body, &payload); err != nil {
		return nil, fmt.Errorf("decode Keycloak admin token: %w", err)
	}
	if payload.AccessToken == "" || !strings.EqualFold(payload.TokenType, "Bearer") {
		return nil, fmt.Errorf("%w: token endpoint did not return a bearer access token", ErrUnexpectedResponse)
	}
	return &adminSession{baseURL: r.config.BaseURL, token: payload.AccessToken, http: r.http}, nil
}

func (s *adminSession) get(ctx context.Context, path string, target any) (bool, error) {
	response, err := s.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, responseError(http.MethodGet, path, response)
	}
	if err := decodeJSONResponse(response.Body, target); err != nil {
		return false, fmt.Errorf("decode GET %s: %w", path, err)
	}
	return true, nil
}

func (s *adminSession) post(ctx context.Context, path string, payload any) error {
	return s.mutate(ctx, http.MethodPost, path, payload, http.StatusCreated, http.StatusNoContent)
}

func (s *adminSession) put(ctx context.Context, path string, payload any) error {
	return s.mutate(ctx, http.MethodPut, path, payload, http.StatusOK, http.StatusNoContent)
}

func (s *adminSession) delete(ctx context.Context, path string, payload any) error {
	return s.mutate(ctx, http.MethodDelete, path, payload, http.StatusNoContent)
}

func (s *adminSession) mutate(ctx context.Context, method, path string, payload any, accepted ...int) error {
	response, err := s.request(ctx, method, path, payload)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	for _, status := range accepted {
		if response.StatusCode == status {
			return nil
		}
	}
	return responseError(method, path, response)
}

func (s *adminSession) request(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create %s %s: %w", method, path, err)
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return response, nil
}

func responseError(method, path string, response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return &HTTPError{
		Method:     method,
		Path:       path,
		StatusCode: response.StatusCode,
		Body:       strings.TrimSpace(string(payload)),
	}
}

func decodeJSONResponse(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxResponseBytes))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}
