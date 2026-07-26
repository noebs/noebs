package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

func TestRunHTTPServerDrainsActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/work", func(c *fiber.Ctx) error {
		close(started)
		<-release
		return c.SendStatus(http.StatusNoContent)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runHTTPServer(ctx, app, listener, 2*time.Second)
	}()

	type requestResult struct {
		status int
		err    error
	}
	requestDone := make(chan requestResult, 1)
	go func() {
		response, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://" + listener.Addr().String() + "/work")
		if err != nil {
			requestDone <- requestResult{err: err}
			return
		}
		defer response.Body.Close()
		requestDone <- requestResult{status: response.StatusCode}
	}()

	waitForSignal(t, started, "HTTP request start")
	cancel()
	select {
	case err := <-runDone:
		t.Fatalf("server returned before request drained: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)

	request := <-requestDone
	if request.err != nil {
		t.Fatalf("request error = %v", request.err)
	}
	if request.status != http.StatusNoContent {
		t.Fatalf("request status = %d, want %d", request.status, http.StatusNoContent)
	}
	if err := <-runDone; err != nil {
		t.Fatalf("runHTTPServer() error = %v", err)
	}
}

func TestRunGRPCServerDrainsActiveRPC(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := grpc.NewServer()
	healthv1.RegisterHealthServer(server, &blockingHealthServer{started: started, release: release})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runGRPCServer(ctx, server, listener, 2*time.Second)
	}()

	connection, err := grpc.NewClient(
		"passthrough:///"+listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	rpcCtx, cancelRPC := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRPC()
	rpcDone := make(chan error, 1)
	go func() {
		_, err := healthv1.NewHealthClient(connection).Check(rpcCtx, &healthv1.HealthCheckRequest{})
		rpcDone <- err
	}()

	waitForSignal(t, started, "gRPC request start")
	cancel()
	select {
	case err := <-runDone:
		t.Fatalf("server returned before RPC drained: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)

	if err := <-rpcDone; err != nil {
		t.Fatalf("RPC error = %v", err)
	}
	if err := <-runDone; err != nil {
		t.Fatalf("runGRPCServer() error = %v", err)
	}
}

type blockingHealthServer struct {
	healthv1.UnimplementedHealthServer
	started chan struct{}
	release chan struct{}
}

func (s *blockingHealthServer) Check(context.Context, *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	close(s.started)
	<-s.release
	return &healthv1.HealthCheckResponse{Status: healthv1.HealthCheckResponse_SERVING}, nil
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
