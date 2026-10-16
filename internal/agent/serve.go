package agent

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/protocol"
)

// Server mode (`healthbridge serve`): one long-running process that keeps the
// local store current and answers MCP over HTTP, so assistants that run in the
// cloud (ChatGPT, Claude on the web and in its apps) can use the data without a
// computer that is switched on. Setup: docs/SERVER_START.md.

const (
	defaultServeAddr     = ":8788"
	defaultFetchInterval = 15 * time.Minute
	mcpTokenFile         = "mcp-token"
	mcpTokenBytes        = 32
)

// ServeOptions configures `healthbridge serve`.
type ServeOptions struct {
	Addr       string
	FetchEvery time.Duration // 0 disables the background fetch
}

func (e Env) logf(format string, args ...any) {
	fmt.Fprintf(e.Err, "%s %s\n", e.now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// Serve runs until ctx is cancelled.
func Serve(ctx context.Context, env Env, stateDir string, o ServeOptions) error {
	if o.Addr == "" {
		o.Addr = defaultServeAddr
	}
	s, err := OpenState(stateDir)
	if err != nil {
		return err
	}
	_, err = s.MCPToken(false)
	dir := s.Dir
	s.Close()
	if err != nil {
		return err
	}

	var mu sync.Mutex
	m := &MCP{Env: env, StateDir: dir, Lock: &mu}
	srv := &http.Server{
		Addr:              o.Addr,
		Handler:           m.HTTPHandler(func() (string, error) { return readMCPToken(dir) }),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	if o.FetchEvery > 0 {
		go fetchLoop(ctx, env, dir, o.FetchEvery, &mu)
	}
	env.logf("healthbridge serve: MCP on %s at /mcp/<secret>; print the full URL with: healthbridge connector-url", o.Addr)

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	case err := <-errc:
		return err
	}
}

// fetchLoop keeps the local store current. An error is logged when it first
// appears and when it clears, so a relay outage does not flood the log.
func fetchLoop(ctx context.Context, env Env, stateDir string, every time.Duration, mu sync.Locker) {
	lastErr := ""
	run := func() {
		mu.Lock()
		defer mu.Unlock()
		err := withState(stateDir, func(s *State) error {
			res, err := Fetch(ctx, env, s, FetchOptions{})
			if err == nil && res.Envelopes > 0 {
				env.logf("fetched %d envelopes, %d days updated", res.Envelopes, res.Days)
			}
			return err
		})
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		switch {
		case msg != "" && msg != lastErr:
			env.logf("fetch failed (retrying every %s): %s", every, msg)
		case msg == "" && lastErr != "":
			env.logf("fetch works again")
		}
		lastErr = msg
	}
	run()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// MCPToken returns the secret last path segment of the connector URL, creating
// it on first use. rotate replaces it; the old URL stops working at once.
func (s *State) MCPToken(rotate bool) (string, error) {
	if !rotate {
		token, err := readMCPToken(s.Dir)
		if err != nil || token != "" {
			return token, err
		}
	}
	b := make([]byte, mcpTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := protocol.B64U(b)
	if err := writePrivateAtomic(s.path(mcpTokenFile), []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
}

// readMCPToken returns "" when no token exists yet. It needs no state lock: the
// file is replaced atomically.
func readMCPToken(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, mcpTokenFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if _, err := protocol.DecodeB64U(token, mcpTokenBytes); err != nil {
		return "", fmt.Errorf("%s is damaged; create a new one with: healthbridge connector-url --rotate", mcpTokenFile)
	}
	return token, nil
}

// ConnectorURL joins the public base URL of the server and the secret path.
// Plain http is accepted only for localhost (local testing): assistants in the
// cloud require https anyway.
func ConnectorURL(publicURL, token string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(publicURL), "/"))
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && isLocalHost(u.Hostname()))) {
		return "", fmt.Errorf("public URL must look like https://your-server.example (got %q)", publicURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("public URL must not contain ? or #: %q", publicURL)
	}
	return u.String() + "/mcp/" + token, nil
}

func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
