package consumer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/golang-jwt/jwt/v5"
)

type refreshAuthStub struct {
	claims          *gateway.TokenClaims
	verifyErr       error
	generated       bool
	generatedTenant string
}

func (a *refreshAuthStub) VerifyJWT(string) (*gateway.TokenClaims, error) {
	return a.claims, a.verifyErr
}

func (a *refreshAuthStub) GenerateJWT(_ int64, _ string, tenantID string) (string, error) {
	a.generated = true
	a.generatedTenant = tenantID
	return "new-token", nil
}

type oauthRoundTripFunc func(*http.Request) (*http.Response, error)

func (f oauthRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func oauthHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestServiceGoogleAuthRequiresHTTPClient(t *testing.T) {
	service := &Service{
		Store:       &store.Store{},
		NoebsConfig: ebs_fields.NoebsConfig{GoogleClientID: "client-id"},
	}

	_, _, _, err := service.GoogleAuth(context.Background(), "tenant-a", "code", "", "")
	if !errors.Is(err, ErrMissingHTTPClient) {
		t.Fatalf("GoogleAuth() error = %v, want %v", err, ErrMissingHTTPClient)
	}
}

func TestServiceGoogleAuthUsesConfiguredHTTPClient(t *testing.T) {
	requests := []string{}
	client := &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		switch req.URL.String() {
		case googleTokenURL:
			return oauthHTTPResponse(http.StatusOK, `{"access_token":"token"}`), nil
		case googleUserURL:
			return oauthHTTPResponse(http.StatusOK, `{"email":"user@example.com"}`), nil
		default:
			return nil, fmt.Errorf("unexpected oauth request %s", req.URL.String())
		}
	})}
	service := &Service{
		Store:       &store.Store{},
		NoebsConfig: ebs_fields.NoebsConfig{GoogleClientID: "client-id"},
		HTTPClient:  client,
	}

	_, _, _, err := service.GoogleAuth(context.Background(), "tenant-a", "code", "verifier", "https://app.example/callback")
	if err == nil || err.Error() != "invalid_userinfo" {
		t.Fatalf("GoogleAuth() error = %v, want invalid_userinfo after configured client requests", err)
	}
	want := []string{googleTokenURL, googleUserURL}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("oauth requests = %v, want %v", requests, want)
	}
}

func TestServiceRefreshJWTRequiresTenantClaim(t *testing.T) {
	tests := []struct {
		name      string
		tenantID  string
		verifyErr error
		wantErr   error
	}{
		{name: "valid token missing tenant", wantErr: store.ErrMissingTenantID},
		{name: "expired token missing tenant", verifyErr: jwt.ErrTokenExpired, wantErr: store.ErrMissingTenantID},
		{name: "valid token reserved tenant", tenantID: "default", wantErr: store.ErrInvalidTenantID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &refreshAuthStub{
				claims:    &gateway.TokenClaims{UserID: 42, Mobile: "0990000000", TenantID: tt.tenantID},
				verifyErr: tt.verifyErr,
			}
			service := &Service{Store: &store.Store{}, Auth: auth}

			_, err := service.RefreshJWT(context.Background(), gateway.Token{JWT: "old-token"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RefreshJWT() error = %v, want %v", err, tt.wantErr)
			}
			if auth.generated {
				t.Fatalf("RefreshJWT() generated a token with tenant %q", auth.generatedTenant)
			}
		})
	}
}

func TestServiceRefreshJWTUsesClaimTenant(t *testing.T) {
	auth := &refreshAuthStub{
		claims: &gateway.TokenClaims{UserID: 42, Mobile: "0990000000", TenantID: "tenant-a"},
	}
	service := &Service{Store: &store.Store{}, Auth: auth}

	token, err := service.RefreshJWT(context.Background(), gateway.Token{JWT: "old-token"})
	if err != nil {
		t.Fatalf("RefreshJWT() error = %v", err)
	}
	if token != "new-token" {
		t.Fatalf("RefreshJWT() token = %q, want %q", token, "new-token")
	}
	if auth.generatedTenant != "tenant-a" {
		t.Fatalf("RefreshJWT() generated tenant = %q, want %q", auth.generatedTenant, "tenant-a")
	}
}
