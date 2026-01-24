package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const otelShutdownTimeout = 5 * time.Second

func initOTel(ctx context.Context, cfg ebs_fields.NoebsConfig, logger *logrus.Logger) {
	endpoint := firstNonEmpty(cfg.OtelEndpoint, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	enabled := cfg.OtelEnabled || endpoint != ""
	if !enabled {
		return
	}

	opts := []otlptracegrpc.Option{}
	if endpoint != "" {
		opts = append(opts, otlptracegrpc.WithEndpoint(endpoint))
	}
	if cfg.OtelInsecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		logger.WithError(err).Warn("otel trace exporter init failed")
		return
	}

	serviceName := cfg.OtelServiceName
	if serviceName == "" {
		serviceName = os.Getenv("OTEL_SERVICE_NAME")
	}
	if serviceName == "" {
		serviceName = "noebs"
	}

	sampleRate := clamp01(cfg.OtelSampleRate)
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
		semconv.ServiceVersionKey.String(cfg.OtelServiceVersion),
	))
	if err != nil {
		logger.WithError(err).Warn("otel resource init failed")
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRate))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	otelEnabled = true
	otelShutdown = tp.Shutdown

	logger.WithFields(logrus.Fields{
		"endpoint":    endpoint,
		"sample_rate": sampleRate,
		"service":     serviceName,
		"insecure":    cfg.OtelInsecure,
	}).Info("otel tracing enabled")
}

func clamp01(v float64) float64 {
	if v <= 0 {
		return 0.1
	}
	if v > 1 {
		return 1
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
