package gateway

import (
	"testing"

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
