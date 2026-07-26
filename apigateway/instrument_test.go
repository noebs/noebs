package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v2"
	fiberutils "github.com/gofiber/fiber/v2/utils"
	"github.com/prometheus/client_golang/prometheus"
)

func TestInstrumentationOwnsFiberLabelStrings(t *testing.T) {
	registry := prometheus.NewRegistry()
	methodBuffer := []byte("POST")
	pathBuffer := []byte("/first")
	method := ownMetricLabel(fiberutils.UnsafeString(methodBuffer))
	path := ownMetricLabel(fiberutils.UnsafeString(pathBuffer))

	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "request_total"}, []string{"method", "path"})
	registry.MustRegister(counter)
	counter.WithLabelValues(method, path).Inc()

	copy(methodBuffer, "GETT")
	copy(pathBuffer, "/other")
	counter.WithLabelValues("GETT", "/other").Inc()

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	if len(families) != 1 || len(families[0].Metric) != 2 {
		t.Fatalf("metric families = %#v, want two distinct owned label sets", families)
	}
}

func TestInstrumentationRecordsEffectiveErrorStatus(t *testing.T) {
	tests := []struct {
		path       string
		handlerErr error
		wantStatus int
	}{
		{
			path:       "/typed-error",
			handlerErr: fiber.NewError(http.StatusTeapot, "typed failure"),
			wantStatus: http.StatusTeapot,
		},
		{
			path:       "/unhandled-error",
			handlerErr: errors.New("unhandled failure"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(Instrumentation())
	for _, test := range tests {
		handlerErr := test.handlerErr
		app.Get(test.path, func(*fiber.Ctx) error {
			return handlerErr
		})
	}

	for _, test := range tests {
		response, err := app.Test(httptest.NewRequest(http.MethodGet, test.path, nil))
		if err != nil {
			t.Fatalf("%s request error = %v", test.path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != test.wantStatus {
			t.Fatalf("%s response status = %d, want %d", test.path, response.StatusCode, test.wantStatus)
		}
	}

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	recorded := map[string]bool{}
	for _, family := range families {
		if family.GetName() != "noebs_http_server_requests_total" {
			continue
		}
		for _, metric := range family.Metric {
			code := ""
			method := ""
			for _, label := range metric.Label {
				switch label.GetName() {
				case "code":
					code = label.GetValue()
				case "method":
					method = label.GetValue()
				}
			}
			if method == http.MethodGet && metric.GetCounter().GetValue() > 0 {
				recorded[code] = true
			}
		}
	}
	for _, test := range tests {
		code := http.StatusText(test.wantStatus)
		if !recorded[strconv.Itoa(test.wantStatus)] {
			t.Fatalf("%s status metric missing for %d (%s); recorded = %v", test.path, test.wantStatus, code, recorded)
		}
	}
}
