package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc"
)

const applicationShutdownTimeout = 30 * time.Second

func runHTTPServer(ctx context.Context, app *fiber.App, listener net.Listener, shutdownTimeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- app.Listener(listener)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP: %w", err)
	}
	if err := <-serveErr; err != nil {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}
	return nil
}

func runGRPCServer(ctx context.Context, server *grpc.Server, listener net.Listener, shutdownTimeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve gRPC: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()

	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	select {
	case <-stopped:
	case <-timer.C:
		server.Stop()
		<-stopped
	}

	if err := <-serveErr; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve gRPC during shutdown: %w", err)
	}
	return nil
}
