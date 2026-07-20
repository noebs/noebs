package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

const internalHealthPath = "/internal/health"

func isInternalHealthcheckCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "internal-healthcheck"
}

func checkInternalHealth() error {
	payload, err := loadConfig()
	if err != nil {
		return err
	}
	var cfg ebs_fields.NoebsConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return fmt.Errorf("decode healthcheck config: %w", err)
	}
	role, err := parseServiceRole(cfg.ServiceRole)
	if err != nil {
		return err
	}
	if !role.startsHTTP() {
		return errors.New("internal healthcheck is only available for HTTP services")
	}
	tlsConfig, err := cfg.InternalTransport.ClientTLSConfig(string(role))
	if err != nil {
		return err
	}
	tlsConfig.ServerName = string(role)
	port := strings.TrimPrefix(strings.TrimSpace(cfg.Port), ":")
	if port == "" {
		return errors.New("internal healthcheck port is required")
	}
	transport := &http.Transport{
		Proxy:           nil,
		TLSClientConfig: tlsConfig,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
		},
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get("https://" + string(role) + ":" + port + internalHealthPath)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("internal healthcheck returned %s", response.Status)
	}
	return nil
}
