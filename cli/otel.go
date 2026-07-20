package main

import (
	"context"
	"errors"
	"fmt"
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

var (
	errMissingOtelEndpoint    = errors.New("missing noebs.otel_endpoint")
	errMissingOtelServiceName = errors.New("missing noebs.otel_service_name")
	errInvalidOtelServiceName = errors.New("invalid noebs.otel_service_name")
	errInvalidOtelSampleRate  = errors.New("invalid noebs.otel_sample_rate")
)

func validateOTelRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	if !cfg.OtelEnabled {
		return nil
	}
	if strings.TrimSpace(cfg.OtelEndpoint) == "" {
		return errMissingOtelEndpoint
	}
	serviceName := strings.TrimSpace(cfg.OtelServiceName)
	if serviceName == "" {
		return errMissingOtelServiceName
	}
	if serviceName != string(role) {
		return fmt.Errorf("%w: got %q want %q", errInvalidOtelServiceName, serviceName, role)
	}
	if cfg.OtelSampleRate <= 0 || cfg.OtelSampleRate > 1 {
		return fmt.Errorf("%w: %f", errInvalidOtelSampleRate, cfg.OtelSampleRate)
	}
	return nil
}

func initOTel(ctx context.Context, role serviceRole, cfg ebs_fields.NoebsConfig, logger *logrus.Logger) error {
	if !cfg.OtelEnabled {
		return nil
	}
	if err := validateOTelRuntimeConfig(role, cfg); err != nil {
		return err
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(strings.TrimSpace(cfg.OtelEndpoint)),
	)
	if err != nil {
		return fmt.Errorf("otel trace exporter init failed: %w", err)
	}

	serviceName := strings.TrimSpace(cfg.OtelServiceName)
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
		semconv.ServiceVersionKey.String(cfg.OtelServiceVersion),
	))
	if err != nil {
		return fmt.Errorf("otel resource init failed: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.OtelSampleRate))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	otelEnabled = true
	otelShutdown = tp.Shutdown

	logger.WithFields(logrus.Fields{
		"endpoint":    cfg.OtelEndpoint,
		"sample_rate": cfg.OtelSampleRate,
		"service":     serviceName,
	}).Info("otel tracing enabled")
	return nil
}
