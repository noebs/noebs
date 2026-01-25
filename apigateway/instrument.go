package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/cenkalti/backoff/v4"
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
		routePath := c.Path()
		if r := c.Route(); r != nil && r.Path != "" {
			routePath = r.Path
		}
		method := c.Method()
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

// SyncLedger sends the user data to the server endpoint (dapi.noebs.sd) for backup
func SyncLedger(user ebs_fields.User) error {
	client := &http.Client{Timeout: 15 * time.Second}
	safeUser := sanitizeLedgerUser(user)
	body, err := json.Marshal(&safeUser)
	if err != nil {
		log.Printf("error in marshaling user data: %v", err)
		return err
	}
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.MaxElapsedTime = 5 * time.Minute
	op := func() error {

		req, err := http.NewRequest("POST", "https://dapi.nil.sd/updates", bytes.NewBuffer(body))
		if err != nil {
			log.Printf("error in creating request: %v", err)
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("error in sending request: %v", err)
			return err
		}
		defer resp.Body.Close()
		res, err := io.ReadAll(resp.Body)
		log.Printf("response from server: %v", string(res))
		return nil
	}
	err = backoff.Retry(op, expBackoff)
	return err
}

func sanitizeLedgerUser(user ebs_fields.User) ebs_fields.User {
	user.Password = ""
	user.Password2 = ""
	user.PublicKey = ""
	user.OTP = ""
	user.SignedOTP = ""
	user.MainCard = ""
	user.ExpDate = ""
	user.DeviceID = ""
	user.DeviceToken = ""
	user.NewPassword = ""
	user.KYC = nil
	user.Cards = nil
	user.Tokens = nil
	user.Beneficiaries = nil
	return user
}

const (
	BACKUP_TIME = 24 * time.Minute
)
