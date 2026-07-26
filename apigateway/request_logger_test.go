package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

func TestRequestLoggerUsesEffectiveErrorStatus(t *testing.T) {
	tests := []struct {
		name       string
		handlerErr error
		wantStatus int
		wantLevel  string
	}{
		{
			name:       "typed client error",
			handlerErr: fiber.NewError(http.StatusBadRequest, "bad request"),
			wantStatus: http.StatusBadRequest,
			wantLevel:  "warning",
		},
		{
			name:       "unhandled error",
			handlerErr: errors.New("handler failed"),
			wantStatus: http.StatusInternalServerError,
			wantLevel:  "error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := logrus.New()
			logger.SetOutput(&output)
			logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})

			app := fiber.New(fiber.Config{DisableStartupMessage: true})
			app.Use(RequestLogger(logger, LogSamplingConfig{}))
			app.Get("/result", func(*fiber.Ctx) error {
				return test.handlerErr
			})

			response, err := app.Test(httptest.NewRequest(http.MethodGet, "/result", nil))
			if err != nil {
				t.Fatalf("request error = %v", err)
			}
			t.Cleanup(func() { _ = response.Body.Close() })
			if response.StatusCode != test.wantStatus {
				t.Fatalf("response status = %d, want %d", response.StatusCode, test.wantStatus)
			}

			var entry map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
				t.Fatalf("decode log entry: %v; output = %q", err, output.String())
			}
			if got := int(entry["status"].(float64)); got != test.wantStatus {
				t.Fatalf("logged status = %d, want %d", got, test.wantStatus)
			}
			if got := entry["level"]; got != test.wantLevel {
				t.Fatalf("logged level = %v, want %s", got, test.wantLevel)
			}
		})
	}
}
