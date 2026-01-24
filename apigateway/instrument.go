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

func Instrumentation() fiber.Handler {
	counterVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   "noebs",
		Subsystem:   "request",
		Name:        "requests_count",
		Help:        "Number of requests per each endpoint",
		ConstLabels: nil,
	}, []string{"code", "method", "handler", "host", "url"})

	resTime := prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace:   "noebs",
		Subsystem:   "response",
		Name:        "response_time_hist",
		Help:        "noebs response duration",
		ConstLabels: nil,
		Buckets:     nil,
	})

	resSize := prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace:   "noebs",
		Subsystem:   "response",
		Name:        "size_histogram",
		Help:        "noebs response size",
		ConstLabels: nil,
		Buckets:     nil,
	})

	reqSize := prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace:   "noebs",
		Subsystem:   "request",
		Name:        "size_hist",
		Help:        "Request size instrumenter",
		ConstLabels: nil,
		Buckets:     nil,
	})

	resTimeSum := prometheus.NewSummary(prometheus.SummaryOpts{
		Namespace:   "noebs",
		Subsystem:   "response",
		Name:        "latency_summary",
		Help:        "Computes responses latency",
		ConstLabels: nil,
		Objectives:  nil,
		MaxAge:      0,
		AgeBuckets:  0,
		BufCap:      0,
	})

	registerOnce.Do(func() {
		if err := prometheus.Register(counterVec); err != nil {
			if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
					counterVec = existing
				}
				log.Printf("prometheus counter already registered: %v", err)
			} else {
				log.Printf("prometheus counter register failed: %v", err)
			}
		}
		if err := prometheus.Register(resTime); err != nil {
			if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if existing, ok := already.ExistingCollector.(prometheus.Histogram); ok {
					resTime = existing
				}
				log.Printf("prometheus resTime already registered: %v", err)
			} else {
				log.Printf("prometheus resTime register failed: %v", err)
			}
		}
		if err := prometheus.Register(resSize); err != nil {
			if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if existing, ok := already.ExistingCollector.(prometheus.Histogram); ok {
					resSize = existing
				}
				log.Printf("prometheus resSize already registered: %v", err)
			} else {
				log.Printf("prometheus resSize register failed: %v", err)
			}
		}
		if err := prometheus.Register(reqSize); err != nil {
			if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if existing, ok := already.ExistingCollector.(prometheus.Histogram); ok {
					reqSize = existing
				}
				log.Printf("prometheus reqSize already registered: %v", err)
			} else {
				log.Printf("prometheus reqSize register failed: %v", err)
			}
		}
		if err := prometheus.Register(resTimeSum); err != nil {
			if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if existing, ok := already.ExistingCollector.(prometheus.Summary); ok {
					resTimeSum = existing
				}
				log.Printf("prometheus resTimeSum already registered: %v", err)
			} else {
				log.Printf("prometheus resTimeSum register failed: %v", err)
			}
		}
	})
	return func(c *fiber.Ctx) error {

		if c.Path() == "/metrics" {
			return c.Next()
		}
		start := time.Now()
		err := c.Next()
		duration := float64(time.Since(start)) * 1e-6 // to millisecond

		rSize := len(c.Response().Body())
		rqSize := len(c.Body())

		status := strconv.Itoa(c.Response().StatusCode())
		url := c.Path()
		routePath := ""
		if c.Route() != nil {
			routePath = c.Route().Path
		}

		counterVec.WithLabelValues(status, c.Method(), routePath, string(c.Context().Host()), url).Inc()
		resTime.Observe(duration)
		resSize.Observe(float64(rSize))
		reqSize.Observe(float64(rqSize))
		resTimeSum.Observe(duration)
		return err
	}
}

// SyncLedger sends the user data to the server endpoint (dapi.noebs.sd) for backup
func SyncLedger(user ebs_fields.User) error {
	client := &http.Client{}
	body, err := json.Marshal(&user)
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
	err = backoff.Retry(op, backoff.NewExponentialBackOff())
	return err
}

const (
	BACKUP_TIME = 24 * time.Minute
)
