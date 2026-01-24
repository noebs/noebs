package main

import (
	"os"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/sirupsen/logrus"
)

const (
	defaultLogSamplingTick  = 5 * time.Second
	defaultLogSamplingAfter = 2 * time.Second
)

func configureLogger(cfg ebs_fields.NoebsConfig) {
	logrusLogger.Out = os.Stderr
	if cfg.IsDebug {
		logrusLogger.SetLevel(logrus.DebugLevel)
		logrusLogger.SetReportCaller(true)
	} else {
		logrusLogger.SetLevel(logrus.InfoLevel)
		logrusLogger.SetReportCaller(false)
	}
	logrusLogger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339Nano,
	})

	logSampling = gateway.LogSamplingConfig{
		Tick:  durationFromMs(cfg.LogSamplingTickMs, defaultLogSamplingTick),
		After: durationFromMs(cfg.LogSamplingAfterMs, defaultLogSamplingAfter),
	}
}

func durationFromMs(ms int, def time.Duration) time.Duration {
	if ms <= 0 {
		return def
	}
	return time.Duration(ms) * time.Millisecond
}
