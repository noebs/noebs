package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const backgroundHealthShutdownTimeout = 5 * time.Second

func startBackgroundHealthServer(ctx context.Context, role serviceRole, addr string) (*http.Server, error) {
	if !role.startsBackgroundHealth() {
		return nil, fmt.Errorf("%w: %s", errHealthNotAllowed, role)
	}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("%w: %s requires noebs.port", errMissingHealthPort, role)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/test", backgroundHealthHandler)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for %s background health on %s: %w", role, addr, err)
	}
	server.Addr = listener.Addr().String()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), backgroundHealthShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logrusLogger.WithError(err).Warnf("%s background health shutdown failed", role)
		}
	}()
	go func() {
		logrusLogger.Printf("%s background health listening on %s", role, server.Addr)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logrusLogger.WithError(err).Errorf("%s background health server stopped", role)
		}
	}()
	return server, nil
}

func backgroundHealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]bool{"message": true}); err != nil {
		logrusLogger.WithError(err).Warn("background health response failed")
	}
}
