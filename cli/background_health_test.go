package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestStartBackgroundHealthServerServesHealthForBackgroundRoles(t *testing.T) {
	backgroundRoles := []serviceRole{
		serviceRoleEBSAdapterEvents,
		serviceRoleAdminReportingProjector,
		serviceRoleWalletWorker,
	}
	for _, role := range backgroundRoles {
		t.Run(string(role), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server, err := startBackgroundHealthServer(ctx, role, "127.0.0.1:0")
			if err != nil {
				t.Fatalf("startBackgroundHealthServer() error = %v", err)
			}
			if server == nil {
				t.Fatalf("startBackgroundHealthServer() server = nil")
			}
			resp, err := http.Get("http://" + server.Addr + "/test")
			if err != nil {
				t.Fatalf("GET /test error = %v", err)
			}
			t.Cleanup(func() {
				if err := resp.Body.Close(); err != nil {
					t.Errorf("close /test body: %v", err)
				}
			})
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read /test body: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d body = %s", resp.StatusCode, body)
			}
			var payload map[string]bool
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode /test body %s: %v", body, err)
			}
			if got, ok := payload["message"]; !ok || !got {
				t.Fatalf("message = %v present = %v, want true", got, ok)
			}
		})
	}
}

func TestStartBackgroundHealthServerRejectsNonBackgroundRoles(t *testing.T) {
	server, err := startBackgroundHealthServer(context.Background(), serviceRoleAPIGateway, "127.0.0.1:0")
	if !errors.Is(err, errHealthNotAllowed) {
		t.Fatalf("startBackgroundHealthServer() error = %v, want %v", err, errHealthNotAllowed)
	}
	if server != nil {
		t.Fatalf("startBackgroundHealthServer() server = %#v, want nil for api-gateway", server)
	}
}

func TestStartBackgroundHealthServerRequiresExplicitPort(t *testing.T) {
	_, err := startBackgroundHealthServer(context.Background(), serviceRoleWalletWorker, "")
	if !errors.Is(err, errMissingHealthPort) {
		t.Fatalf("startBackgroundHealthServer() error = %v, want %v", err, errMissingHealthPort)
	}
}
