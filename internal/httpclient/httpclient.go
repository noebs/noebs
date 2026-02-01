package httpclient

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"
)

type Options struct {
	Timeout               time.Duration
	DialTimeout           time.Duration
	KeepAlive             time.Duration
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	MaxConnsPerHost       int
	IdleConnTimeout       time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	ExpectContinueTimeout time.Duration
	DisableKeepAlives     bool
	TLSConfig             *tls.Config
}

type Option func(*Options)

var (
	defaultOnce   sync.Once
	defaultClient *http.Client
)

func Default() *http.Client {
	defaultOnce.Do(func() {
		defaultClient = New()
	})
	return defaultClient
}

func New(opts ...Option) *http.Client {
	options := DefaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return buildClient(options)
}

func DefaultOptions() Options {
	return Options{
		Timeout:               30 * time.Second,
		DialTimeout:           5 * time.Second,
		KeepAlive:             30 * time.Second,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
	}
}

func buildClient(opts Options) *http.Client {
	dialer := &net.Dialer{
		Timeout:   opts.DialTimeout,
		KeepAlive: opts.KeepAlive,
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          opts.MaxIdleConns,
		MaxIdleConnsPerHost:   opts.MaxIdleConnsPerHost,
		MaxConnsPerHost:       opts.MaxConnsPerHost,
		IdleConnTimeout:       opts.IdleConnTimeout,
		TLSHandshakeTimeout:   opts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		ExpectContinueTimeout: opts.ExpectContinueTimeout,
		DisableKeepAlives:     opts.DisableKeepAlives,
		TLSClientConfig:       cloneTLSConfig(opts.TLSConfig),
	}
	return &http.Client{
		Timeout:   opts.Timeout,
		Transport: transport,
	}
}

func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return nil
	}
	return cfg.Clone()
}

func WithTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.Timeout = d
	}
}

func WithDialTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.DialTimeout = d
	}
}

func WithKeepAlive(d time.Duration) Option {
	return func(o *Options) {
		o.KeepAlive = d
	}
}

func WithMaxIdleConns(n int) Option {
	return func(o *Options) {
		o.MaxIdleConns = n
	}
}

func WithMaxIdleConnsPerHost(n int) Option {
	return func(o *Options) {
		o.MaxIdleConnsPerHost = n
	}
}

func WithMaxConnsPerHost(n int) Option {
	return func(o *Options) {
		o.MaxConnsPerHost = n
	}
}

func WithIdleConnTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.IdleConnTimeout = d
	}
}

func WithTLSHandshakeTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.TLSHandshakeTimeout = d
	}
}

func WithResponseHeaderTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.ResponseHeaderTimeout = d
	}
}

func WithExpectContinueTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.ExpectContinueTimeout = d
	}
}

func WithDisableKeepAlives(disable bool) Option {
	return func(o *Options) {
		o.DisableKeepAlives = disable
	}
}

func WithTLSConfig(cfg *tls.Config) Option {
	return func(o *Options) {
		o.TLSConfig = cfg
	}
}
