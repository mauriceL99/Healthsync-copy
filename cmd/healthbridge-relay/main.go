// Command healthbridge-relay runs the self-hostable HealthBridge relay.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/relay"
	"github.com/mauriceL99/Healthsync-copy/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "healthbridge-relay:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", envOr("HB_RELAY_ADDR", ":8787"), "listen address")
	dbPath := flag.String("db", envOr("HB_RELAY_DB", "relay.db"), "SQLite database path")
	ttl := flag.Duration("envelope-ttl", 30*24*time.Hour, "delete envelopes that were not acknowledged after this duration")
	rate := flag.Int("rate", 120, "requests per minute per client IP")
	trustProxy := flag.Bool("trust-proxy", os.Getenv("HB_RELAY_TRUST_PROXY") == "1", "use X-Forwarded-For for rate limiting (only behind a trusted reverse proxy)")
	certFile := flag.String("tls-cert", os.Getenv("HB_RELAY_TLS_CERT"), "TLS certificate (PEM); empty serves plain HTTP for use behind a TLS proxy")
	keyFile := flag.String("tls-key", os.Getenv("HB_RELAY_TLS_KEY"), "TLS private key (PEM)")
	regToken := flag.String("registration-token", os.Getenv("HB_RELAY_REGISTRATION_TOKEN"), "require this token to register new agent accounts (recommended for public relays)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return nil
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	store, err := relay.OpenStore(*dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	srv := relay.NewServer(store, relay.Config{
		Version: version.Version, EnvelopeTTL: *ttl, RatePerMinute: *rate, TrustProxy: *trustProxy, Logger: logger,
		RegistrationToken: *regToken,
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.RunJanitor(ctx, 10*time.Minute)
	httpServer := &http.Server{
		Addr: *addr, Handler: srv,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: time.Minute,
		MaxHeaderBytes: 16 << 10,
	}
	errs := make(chan error, 1)
	go func() {
		logger.Info("relay listening", "addr", *addr, "tls", *certFile != "", "registration_token", *regToken != "", "version", version.Version)
		if *certFile != "" {
			errs <- httpServer.ListenAndServeTLS(*certFile, *keyFile)
		} else {
			errs <- httpServer.ListenAndServe()
		}
	}()
	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
