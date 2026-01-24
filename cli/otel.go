package main

import (
	"context"
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
	if !cfg.OtelEnabled {
		return
	}
	if cfg.OtelEndpoint == "" {
		logger.Warn("otel enabled but endpoint is empty; tracing disabled")
		return
	}

	opts := []otlptracegrpc.Option{}
	opts = append(opts, otlptracegrpc.WithEndpoint(cfg.OtelEndpoint))
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
		"endpoint":    cfg.OtelEndpoint,
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
