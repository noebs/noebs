package gateway

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"strconv"
	"time"
)

func Instrumentation() gin.HandlerFunc {
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

	// prometheus collector
	colls := []prometheus.Collector{counterVec, resTime, resSize, reqSize, resTimeSum}
	for _, v := range colls {
		err := prometheus.Register(v)
		if err != nil {
			panic(err)
		}
	}
	return func(c *gin.Context) {

		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		duration := float64(time.Since(start)) * 1e-6 // to millisecond

		rSize := c.Writer.Size()
		rqSize := c.Request.ContentLength

		status := strconv.Itoa(c.Writer.Status())
		url := getUrl(c)

		counterVec.WithLabelValues(status, c.Request.Method, c.HandlerName(), c.Request.Host, url).Inc()
		resTime.Observe(duration)
		resSize.Observe(float64(rSize))
		reqSize.Observe(float64(rqSize))
		resTimeSum.Observe(duration)

	}
}

func getUrl(c *gin.Context) string {
	return c.Request.URL.Path
}
