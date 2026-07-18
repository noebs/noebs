package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
	"github.com/gorilla/websocket"
	chat "github.com/tutipay/ws"
)

func TestChatSocketThroughGatewayTracksSessionState(t *testing.T) {
	for _, tt := range []struct {
		name       string
		invalidate func(*atomic.Int64, *atomic.Bool)
		wantClose  int
	}{
		{
			name: "revoked",
			invalidate: func(epoch *atomic.Int64, _ *atomic.Bool) {
				epoch.Add(1)
			},
			wantClose: websocket.ClosePolicyViolation,
		},
		{
			name: "validator unavailable",
			invalidate: func(_ *atomic.Int64, unavailable *atomic.Bool) {
				unavailable.Store(true)
			},
			wantClose: websocket.CloseInternalServerErr,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const (
				tenant       = "tenant_1"
				mobile       = "0990000000"
				victimMobile = "0980000000"
				userID       = int64(42)
				initialEpoch = int64(3)
			)
			var currentEpoch atomic.Int64
			var unavailable atomic.Bool
			currentEpoch.Store(initialEpoch)

			identityVerifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway), string(serviceRoleNotification))
			identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if unavailable.Load() {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				principal, err := identityVerifier.VerifyRequest(r)
				if err != nil || principal.Caller != string(serviceRoleAPIGateway) && principal.Caller != string(serviceRoleNotification) {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				var command consumer.SessionValidationCommand
				if r.Method != http.MethodPost ||
					r.URL.Path != "/internal/identity-auth/sessions/validate" ||
					r.Header.Get(gateway.GatewayTenantIDHeader) != tenant ||
					json.NewDecoder(r.Body).Decode(&command) != nil ||
					command.UserID != userID {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if command.SessionEpoch != currentEpoch.Load() {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer identityServer.Close()

			gatewaySigners := newTestWorkloadSigners(t, string(serviceRoleAPIGateway), string(serviceRoleIdentityAuth), string(serviceRoleNotification))
			notificationSigners := newTestWorkloadSigners(t, string(serviceRoleNotification), string(serviceRoleIdentityAuth))
			identityValidator, err := newIdentitySessionValidator(ebs_fields.NoebsConfig{
				ServiceDiscovery: map[string]string{string(serviceRoleIdentityAuth): identityServer.URL},
			}, gatewaySigners)
			if err != nil {
				t.Fatalf("newIdentitySessionValidator(): %v", err)
			}
			jwt := gateway.JWTAuth{Key: []byte("test-signing-key"), Sessions: identityValidator}
			token, err := jwt.GenerateJWTWithSessionEpoch(userID, mobile, tenant, initialEpoch)
			if err != nil {
				t.Fatalf("GenerateJWTWithSessionEpoch(): %v", err)
			}

			gatewayListener := newFiberTestListener(t)
			gatewayURL := "http://" + gatewayListener.Addr().String()
			chatValidator, err := newChatSessionValidator(ebs_fields.NoebsConfig{
				ServiceDiscovery: map[string]string{string(serviceRoleIdentityAuth): identityServer.URL},
			}, notificationSigners)
			if err != nil {
				t.Fatalf("newChatSessionValidator(): %v", err)
			}

			chatCfg := chat.DefaultHubConfig()
			chatCfg.PingPeriod = time.Hour
			chatCfg.ClientIDFromRequest = chatClientIDFromGatewayIdentity
			chatCfg.ValidateClientSession = chatSessionValidation(chatValidator)
			chatCfg.SessionValidationInterval = 10 * time.Millisecond
			chatHub := chat.NewHubWithConfig(nil, chatCfg)
			go chatHub.Run()
			defer chatHub.Stop()

			notificationApp := fiber.New(fiber.Config{DisableStartupMessage: true})
			notificationApp.Use(signedWorkloadBoundary(serviceRoleNotification, newTestWorkloadVerifier(t, string(serviceRoleNotification), string(serviceRoleAPIGateway))))
			notificationApp.Get("/ws", gateway.InternalUserIdentityMiddleware(), chatWebSocketHandler(chatHub))
			notificationURL := startFiberTestApp(t, notificationApp, newFiberTestListener(t))

			gatewayApp := fiber.New(fiber.Config{DisableStartupMessage: true})
			gatewayApp.Use(gateway.RequestID())
			discovery := map[string]string{}
			for _, spec := range gatewayProxyRouteSpecs() {
				discovery[string(spec.role)] = notificationURL
			}
			previousSigners := workloadSigners
			workloadSigners = gatewaySigners
			t.Cleanup(func() { workloadSigners = previousSigners })
			if err := registerAPIGatewayProxyRoutes(gatewayApp, ebs_fields.NoebsConfig{
				DefaultTenantID:  tenant,
				ServiceDiscovery: discovery,
			}, jwt, func(c *fiber.Ctx) error { return c.Next() }); err != nil {
				t.Fatalf("registerAPIGatewayProxyRoutes(): %v", err)
			}
			startFiberTestApp(t, gatewayApp, gatewayListener)

			spoofHeaders := http.Header{}
			spoofHeaders.Set(gateway.GatewayTenantIDHeader, tenant)
			spoofHeaders.Set(gateway.GatewayUserIDHeader, "42")
			spoofHeaders.Set(gateway.GatewayMobileHeader, victimMobile)
			spoofHeaders.Set(gateway.GatewaySessionEpochHeader, "3")
			spoofHeaders.Set(gateway.GatewaySessionTokenHeader, token)
			spoofURL := "ws" + strings.TrimPrefix(notificationURL, "http") + "/ws"
			spoofConn, spoofResponse, spoofErr := websocket.DefaultDialer.Dial(spoofURL, spoofHeaders)
			if spoofConn != nil {
				spoofConn.Close()
			}
			if spoofErr == nil || spoofResponse == nil || spoofResponse.StatusCode != http.StatusUnauthorized {
				t.Fatalf("spoofed mobile handshake = response:%v error:%v, want HTTP 401", spoofResponse, spoofErr)
			}

			gatewayWSURL := "ws" + strings.TrimPrefix(gatewayURL, "http") + "/ws"
			headers := http.Header{fiber.HeaderAuthorization: []string{"Bearer " + token}}
			conn, _, err := websocket.DefaultDialer.Dial(gatewayWSURL, headers)
			if err != nil {
				t.Fatalf("dial gateway websocket: %v", err)
			}
			defer conn.Close()

			typing := true
			if err := conn.WriteJSON(chat.Message{Type: "typing", To: mobile, IsTyping: &typing}); err != nil {
				t.Fatalf("write through gateway: %v", err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			var response chat.Response
			if err := conn.ReadJSON(&response); err != nil {
				t.Fatalf("read through gateway: %v", err)
			}
			if len(response.Messages) != 1 || response.Messages[0].From != mobile || response.Messages[0].Type != "typing" {
				t.Fatalf("typing response = %#v", response)
			}

			tt.invalidate(&currentEpoch, &unavailable)
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, _, err = conn.ReadMessage()
			if !websocket.IsCloseError(err, tt.wantClose) {
				t.Fatalf("expected close code %d after session change, got %v", tt.wantClose, err)
			}
		})
	}
}

func TestChatSessionValidatorCallsIdentityAuthDirectly(t *testing.T) {
	verifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleNotification))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := verifier.VerifyRequest(r)
		if err != nil || principal.Caller != string(serviceRoleNotification) || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var command consumer.SessionValidationCommand
		if json.NewDecoder(r.Body).Decode(&command) != nil || command.UserID != 42 || command.SessionEpoch != 3 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	validator, err := newChatSessionValidator(ebs_fields.NoebsConfig{
		ServiceDiscovery: map[string]string{string(serviceRoleIdentityAuth): server.URL},
	}, newTestWorkloadSigners(t, string(serviceRoleNotification), string(serviceRoleIdentityAuth)))
	if err != nil {
		t.Fatalf("newChatSessionValidator(): %v", err)
	}
	err = validator.ValidateSession(context.Background(), chatGatewayIdentity{
		UserIdentity: gateway.UserIdentity{
			TenantID:     "tenant_1",
			UserID:       42,
			Mobile:       "0990000000",
			SessionEpoch: 3,
		},
		Token: "gateway-validated-session-token",
	})
	if !errors.Is(err, gateway.ErrSessionRevoked) {
		t.Fatalf("ValidateSession() error = %v, want %v", err, gateway.ErrSessionRevoked)
	}
}

func newFiberTestListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return listener
}

func startFiberTestApp(t *testing.T, app *fiber.App, listener net.Listener) string {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- app.Listener(listener) }()
	t.Cleanup(func() {
		_ = app.Shutdown()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Errorf("Fiber server did not stop")
		}
	})
	return "http://" + listener.Addr().String()
}
