package oidcauth

import (
	"net/http"
	"time"
)

// RuntimeConfig is the complete resource-server contract supplied by the
// deployment boundary. Durations are seconds in configuration files.
type RuntimeConfig struct {
	Issuer                           string   `json:"issuer"`
	JWKSURL                          string   `json:"jwks_url"`
	Audience                         string   `json:"audience"`
	AllowedClients                   []string `json:"allowed_clients"`
	AccessTokenType                  string   `json:"access_token_type"`
	MaxFutureIssuedAtSeconds         int      `json:"max_future_issued_at_seconds"`
	JWKSRefreshSeconds               int      `json:"jwks_refresh_seconds"`
	UnknownKeyRefreshIntervalSeconds int      `json:"unknown_key_refresh_interval_seconds"`
}

func NewRemoteVerifier(config RuntimeConfig, client *http.Client, clock Clock) (*Verifier, error) {
	if config.MaxFutureIssuedAtSeconds < 0 || config.JWKSRefreshSeconds <= 0 ||
		config.UnknownKeyRefreshIntervalSeconds <= 0 {
		return nil, ErrInvalidConfiguration
	}
	keys, err := NewRemoteKeySet(RemoteKeySetConfig{
		URL:                       config.JWKSURL,
		Client:                    client,
		RefreshInterval:           time.Duration(config.JWKSRefreshSeconds) * time.Second,
		UnknownKeyRefreshInterval: time.Duration(config.UnknownKeyRefreshIntervalSeconds) * time.Second,
		Clock:                     clock,
	})
	if err != nil {
		return nil, err
	}
	return NewVerifier(Config{
		Issuer:            config.Issuer,
		Audience:          config.Audience,
		AllowedClients:    config.AllowedClients,
		AccessTokenType:   config.AccessTokenType,
		MaxFutureIssuedAt: time.Duration(config.MaxFutureIssuedAtSeconds) * time.Second,
		Clock:             clock,
		Keys:              keys,
	})
}
