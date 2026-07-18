package gateway

import (
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
)

var registerOnce sync.Once
var (
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
	httpRequestSize     *prometheus.HistogramVec
	httpResponseSize    *prometheus.HistogramVec
	httpInFlight        *prometheus.GaugeVec
)

func registerCounterVec(c *prometheus.CounterVec) *prometheus.CounterVec {
	if err := prometheus.Register(c); err != nil {
		if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
		}
		log.Printf("prometheus counter register failed: %v", err)
	}
	return c
}

func registerHistogramVec(c *prometheus.HistogramVec) *prometheus.HistogramVec {
	if err := prometheus.Register(c); err != nil {
		if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := already.ExistingCollector.(*prometheus.HistogramVec); ok {
				return existing
			}
		}
		log.Printf("prometheus histogram register failed: %v", err)
	}
	return c
}

func registerGaugeVec(c *prometheus.GaugeVec) *prometheus.GaugeVec {
	if err := prometheus.Register(c); err != nil {
		if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := already.ExistingCollector.(*prometheus.GaugeVec); ok {
				return existing
			}
		}
		log.Printf("prometheus gauge register failed: %v", err)
	}
	return c
}

func initHTTPMetrics() {
	registerOnce.Do(func() {
		httpRequestsTotal = registerCounterVec(prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "noebs",
			Subsystem: "http_server",
			Name:      "requests_total",
			Help:      "Total number of HTTP requests.",
		}, []string{"code", "method", "route"}))

		httpRequestDuration = registerHistogramVec(prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "noebs",
			Subsystem: "http_server",
			Name:      "request_duration_seconds",
			Help:      "Duration of HTTP requests.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"code", "method", "route"}))

		sizeBuckets := []float64{100, 500, 1_000, 2_000, 5_000, 10_000, 25_000, 50_000, 100_000, 250_000, 500_000, 1_000_000, 2_000_000, 5_000_000, 10_000_000}
		httpRequestSize = registerHistogramVec(prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "noebs",
			Subsystem: "http_server",
			Name:      "request_size_bytes",
			Help:      "Size of HTTP requests.",
			Buckets:   sizeBuckets,
		}, []string{"method", "route"}))

		httpResponseSize = registerHistogramVec(prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "noebs",
			Subsystem: "http_server",
			Name:      "response_size_bytes",
			Help:      "Size of HTTP responses.",
			Buckets:   sizeBuckets,
		}, []string{"code", "method", "route"}))

		httpInFlight = registerGaugeVec(prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "noebs",
			Subsystem: "http_server",
			Name:      "in_flight_requests",
			Help:      "Number of HTTP requests currently being served.",
		}, []string{"method", "route"}))
	})
}

func Instrumentation() fiber.Handler {
	initHTTPMetrics()
	return func(c *fiber.Ctx) error {
		if c.Path() == "/metrics" {
			return c.Next()
		}
		routePath := ownMetricLabel(c.Path())
		if r := c.Route(); r != nil && r.Path != "" {
			routePath = ownMetricLabel(r.Path)
		}
		method := ownMetricLabel(c.Method())
		httpInFlight.WithLabelValues(method, routePath).Inc()
		defer httpInFlight.WithLabelValues(method, routePath).Dec()

		start := time.Now()
		err := c.Next()
		duration := time.Since(start).Seconds()

		status := strconv.Itoa(c.Response().StatusCode())
		httpRequestsTotal.WithLabelValues(status, method, routePath).Inc()
		httpRequestDuration.WithLabelValues(status, method, routePath).Observe(duration)
		httpRequestSize.WithLabelValues(method, routePath).Observe(float64(len(c.Body())))
		httpResponseSize.WithLabelValues(status, method, routePath).Observe(float64(len(c.Response().Body())))
		return err
	}
}

func ownMetricLabel(value string) string {
	return strings.Clone(value)
}
