package ebs_fields

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var ebsMetricsOnce sync.Once

var (
	ebsRequestsTotal   *prometheus.CounterVec
	ebsRequestDuration *prometheus.HistogramVec
	ebsRequestSize     *prometheus.HistogramVec
	ebsResponseSize    *prometheus.HistogramVec
)

func registerEBSHistogramVec(c *prometheus.HistogramVec) *prometheus.HistogramVec {
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

func registerEBSCountVec(c *prometheus.CounterVec) *prometheus.CounterVec {
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

func initEBSMetrics() {
	ebsMetricsOnce.Do(func() {
		ebsRequestsTotal = registerEBSCountVec(prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "noebs",
			Subsystem: "ebs_client",
			Name:      "requests_total",
			Help:      "Total number of EBS HTTP requests.",
		}, []string{"endpoint", "target", "method", "status", "result"}))

		ebsRequestDuration = registerEBSHistogramVec(prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "noebs",
			Subsystem: "ebs_client",
			Name:      "request_duration_seconds",
			Help:      "Duration of EBS HTTP requests.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"endpoint", "target", "method", "result"}))

		sizeBuckets := []float64{100, 500, 1_000, 2_000, 5_000, 10_000, 25_000, 50_000, 100_000, 250_000, 500_000, 1_000_000, 2_000_000, 5_000_000, 10_000_000}
		ebsRequestSize = registerEBSHistogramVec(prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "noebs",
			Subsystem: "ebs_client",
			Name:      "request_size_bytes",
			Help:      "Size of EBS HTTP requests.",
			Buckets:   sizeBuckets,
		}, []string{"endpoint", "target", "method"}))

		ebsResponseSize = registerEBSHistogramVec(prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "noebs",
			Subsystem: "ebs_client",
			Name:      "response_size_bytes",
			Help:      "Size of EBS HTTP responses.",
			Buckets:   sizeBuckets,
		}, []string{"endpoint", "target", "method"}))
	})
}

func recordEBSMetrics(endpoint, target, method string, statusCode int, err error, reqSize int, respSize int, duration time.Duration) {
	if ebsRequestsTotal == nil {
		return
	}
	status := "error"
	if statusCode > 0 {
		status = strconv.Itoa(statusCode)
	}
	result := "success"
	if err != nil {
		result = "error"
	}

	ebsRequestsTotal.WithLabelValues(endpoint, target, method, status, result).Inc()
	ebsRequestDuration.WithLabelValues(endpoint, target, method, result).Observe(duration.Seconds())
	ebsRequestSize.WithLabelValues(endpoint, target, method).Observe(float64(reqSize))
	ebsResponseSize.WithLabelValues(endpoint, target, method).Observe(float64(respSize))
}
