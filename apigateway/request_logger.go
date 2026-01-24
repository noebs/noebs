package gateway

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type LogSamplingConfig struct {
	Tick  time.Duration
	After time.Duration
}

type logSampler struct {
	tick  time.Duration
	after time.Duration
	next  time.Time
	mu    sync.Mutex
}

func newLogSampler(cfg LogSamplingConfig) *logSampler {
	return &logSampler{tick: cfg.Tick, after: cfg.After}
}

func (s *logSampler) Allow(duration time.Duration) bool {
	if s.after > 0 && duration >= s.after {
		return true
	}
	if s.tick <= 0 {
		return true
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next.IsZero() || now.After(s.next) {
		s.next = now.Add(s.tick)
		return true
	}
	return false
}

func RequestLogger(logger *logrus.Logger, cfg LogSamplingConfig) fiber.Handler {
	sampler := newLogSampler(cfg)
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		duration := time.Since(start)

		status := c.Response().StatusCode()
		routePath := c.Path()
		if r := c.Route(); r != nil && r.Path != "" {
			routePath = r.Path
		}

		shouldLog := false
		if status >= fiber.StatusInternalServerError || err != nil {
			shouldLog = true
		} else if sampler.Allow(duration) {
			shouldLog = true
		}
		if !shouldLog {
			return err
		}

		entry := logger.WithFields(logrus.Fields{
			"request_id":  RequestIDFromCtx(c),
			"method":      c.Method(),
			"path":        routePath,
			"status":      status,
			"duration_ms": duration.Milliseconds(),
			"bytes_in":    len(c.Body()),
			"bytes_out":   len(c.Response().Body()),
			"ip":          c.IP(),
		})
		if tenantID := c.Locals("tenant_id"); tenantID != nil {
			entry = entry.WithField("tenant_id", tenantID)
		}
		if userAgent := c.Get("User-Agent"); userAgent != "" {
			entry = entry.WithField("user_agent", userAgent)
		}
		if err != nil {
			entry = entry.WithField("error", err.Error())
		}

		switch {
		case status >= fiber.StatusInternalServerError || err != nil:
			entry.Error("http_request")
		case status >= fiber.StatusBadRequest:
			entry.Warn("http_request")
		default:
			entry.Info("http_request")
		}

		return err
	}
}
