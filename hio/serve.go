package hio

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	_defaultPort            = 8080
	_defaultIdleTimeout     = 1 * time.Minute
	_defaultReadTimeout     = 5 * time.Second
	_defaultWriteTimeout    = 10 * time.Second
	_defaultShutdownTimeout = 10 * time.Second
)

func Serve(ctx context.Context, h http.Handler, opts ...ServeOption) error {
	var cfg ServeConfig

	for _, opt := range opts {
		opt.apply(&cfg)
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      h,
		IdleTimeout:  cfg.IdleTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		TLSConfig:    cfg.TLS,
	}

	eg, egCtx, stop := withErrGroupNotifyContext(ctx)
	defer stop()

	eg.Go(func() error {
		if err := open(srv); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	eg.Go(func() error {
		<-egCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(ctx, cfg.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	})

	return eg.Wait()
}

// ServeConfig configures an HTTP server.
type ServeConfig struct {
	Host            string
	Port            int
	IdleTimeout     time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	ErrorLog        *log.Logger
	TLS             *tls.Config
	tlsErr          error
}

// DefaultServeConfig returns a [ServeConfig] with default values.
func DefaultServeConfig() ServeConfig {
	return ServeConfig{
		Port:            _defaultPort,
		IdleTimeout:     _defaultIdleTimeout,
		ReadTimeout:     _defaultReadTimeout,
		WriteTimeout:    _defaultWriteTimeout,
		ShutdownTimeout: _defaultShutdownTimeout,
	}
}

// Addr returns the address the server listens on.
func (c ServeConfig) Addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// Override replaces field values with non-zero values from other.
func (c *ServeConfig) Override(other ServeConfig) {
	if other.Host != "" {
		c.Host = other.Host
	}

	if other.Port != 0 {
		c.Port = other.Port
	}

	if other.IdleTimeout != 0 {
		c.IdleTimeout = other.IdleTimeout
	}

	if other.ReadTimeout != 0 {
		c.ReadTimeout = other.ReadTimeout
	}

	if other.WriteTimeout != 0 {
		c.WriteTimeout = other.WriteTimeout
	}

	if other.ShutdownTimeout != 0 {
		c.ShutdownTimeout = other.ShutdownTimeout
	}
}

// Validate checks that the configuration is valid.
func (c *ServeConfig) Validate() error {
	c.setDefaultZeroValues()

	if c.Port <= 0 {
		return errors.New("port must be greater than 0")
	}

	if c.IdleTimeout <= 0 {
		return errors.New("idle timeout must be greater than 0")
	}

	if c.ReadTimeout <= 0 {
		return errors.New("read timeout must be greater than 0")
	}

	if c.WriteTimeout <= 0 {
		return errors.New("write timeout must be greater than 0")
	}

	if c.ShutdownTimeout <= 0 {
		return errors.New("shutdown timeout must be greater than 0")
	}

	if c.tlsErr != nil {
		return fmt.Errorf("tls must be configured correctly if provided: %w", c.tlsErr)
	}

	return nil
}

func (c *ServeConfig) setDefaultZeroValues() {
	if c.Port <= 0 {
		c.Port = _defaultPort
	}

	if c.IdleTimeout <= 0 {
		c.IdleTimeout = _defaultIdleTimeout
	}

	if c.ReadTimeout <= 0 {
		c.ReadTimeout = _defaultReadTimeout
	}

	if c.WriteTimeout <= 0 {
		c.WriteTimeout = _defaultWriteTimeout
	}

	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = _defaultShutdownTimeout
	}
}

// ServeOption applies options to a [ServeConfig].
type ServeOption interface{ apply(*ServeConfig) }

type (
	hostOption            struct{ value string }
	portOption            struct{ value int }
	idleTimeoutOption     struct{ value time.Duration }
	readTimeoutOption     struct{ value time.Duration }
	writeTimeoutOption    struct{ value time.Duration }
	shutdownTimeoutOption struct{ value time.Duration }

	tlsOption struct {
		value *tls.Config
		err   error
	}

	configOption  struct{ value ServeConfig }
	configOptions struct{ value []ServeOption }
)

// WithHost sets the host.
func WithHost(v string) ServeOption { return hostOption{value: v} }

// WithPort sets the port.
func WithPort(v int) ServeOption { return portOption{value: v} }

// WithIdleTimeout sets the idle timeout.
func WithIdleTimeout(v time.Duration) ServeOption { return idleTimeoutOption{value: v} }

// WithReadTimeout sets the read timeout.
func WithReadTimeout(v time.Duration) ServeOption { return readTimeoutOption{value: v} }

// WithWriteTimeout sets the write timeout.
func WithWriteTimeout(v time.Duration) ServeOption { return writeTimeoutOption{value: v} }

// WithShutdownTimeout sets the shutdown timeout.
func WithShutdownTimeout(v time.Duration) ServeOption { return shutdownTimeoutOption{value: v} }

// WithConfig applies the provided configuration, replacing any existing values.
func WithConfig(v ServeConfig) ServeOption { return configOption{value: v} }

// WithOptions applies multiple [ServeOption]s.
func WithOptions(v ...ServeOption) ServeOption { return configOptions{value: v} }

// WithTLS configures TLS with the provided certificate authority, certificate, and key files.
func WithTLS(caFile, ceFile, keyFile string) ServeOption {
	ce, err := tls.LoadX509KeyPair(ceFile, keyFile)
	if err != nil {
		return tlsOption{err: err}
	}

	ca, err := os.ReadFile(caFile)
	if err != nil {
		return tlsOption{err: err}
	}

	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(ca); !ok {
		return tlsOption{err: errors.New("unable to append certs from PEM")}
	}

	return tlsOption{
		value: &tls.Config{
			ClientAuth:   tls.RequireAndVerifyClientCert,
			Certificates: []tls.Certificate{ce},
			ClientCAs:    pool,
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"h2", "http/1.1"},
		},
	}
}

func (o hostOption) apply(cfg *ServeConfig)            { cfg.Host = o.value }
func (o portOption) apply(cfg *ServeConfig)            { cfg.Port = o.value }
func (o idleTimeoutOption) apply(cfg *ServeConfig)     { cfg.IdleTimeout = o.value }
func (o readTimeoutOption) apply(cfg *ServeConfig)     { cfg.ReadTimeout = o.value }
func (o writeTimeoutOption) apply(cfg *ServeConfig)    { cfg.WriteTimeout = o.value }
func (o shutdownTimeoutOption) apply(cfg *ServeConfig) { cfg.ShutdownTimeout = o.value }
func (o tlsOption) apply(cfg *ServeConfig)             { cfg.TLS, cfg.tlsErr = o.value, o.err }
func (o configOption) apply(cfg *ServeConfig)          { cfg.Override(o.value) }
func (o configOptions) apply(cfg *ServeConfig) {
	for _, opt := range o.value {
		opt.apply(cfg)
	}
}

func withErrGroupNotifyContext(ctx context.Context) (*errgroup.Group, context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	eg, ctx := errgroup.WithContext(ctx)
	return eg, ctx, cancel
}

func open(srv *http.Server) error {
	if srv.TLSConfig != nil {
		return srv.ListenAndServeTLS("", "")
	}
	return srv.ListenAndServe()
}
