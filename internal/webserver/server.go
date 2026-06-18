// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/browser"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/guiapp"
	"github.com/autobrr/upbrr/internal/redaction"
)

var openBrowserURL = browser.OpenURL

// Options configures the embedded web UI server.
type Options struct {
	// Config supplies the application configuration used by the backend.
	Config config.Config
	// CLIConfig supplies persisted or command-line web server settings.
	CLIConfig CLIConfig
	// DevelopmentNoAuth enables the development-only auth bypass for loopback hosts.
	DevelopmentNoAuth bool
}

// Server owns the embedded web UI HTTP server, backend services, auth stores, and event hub.
type Server struct {
	cfg                config.Config
	cliCfg             CLIConfig
	backend            *Backend
	picker             nativePicker
	auth               *authStore
	sessions           *sessionManager
	hub                *eventHub
	authLimiter        *fixedWindowLimiter
	generalLimiter     *fixedWindowLimiter
	trustedProxies     []*net.IPNet
	server             *http.Server
	assets             fs.FS
	developmentNoAuth  bool
	developmentSession session
}

// New constructs a server from application and web CLI configuration.
// It rejects development no-auth mode for non-loopback hosts.
func New(opts Options) (*Server, error) {
	cfg := opts.Config
	cliCfg := normalizeCLIConfig(opts.CLIConfig)
	if opts.DevelopmentNoAuth && !isDevelopmentNoAuthHost(cliCfg.Host) {
		return nil, fmt.Errorf("webserver: --dev-no-auth requires a loopback host, got %q", cliCfg.Host)
	}

	hub := newEventHub()
	authStore, err := newAuthStore(cfg.MainSettings.DBPath)
	if err != nil {
		return nil, err
	}
	backend, err := NewBackendWithContext(context.Background(), cfg, hub)
	if err != nil {
		return nil, err
	}
	hub.SetLogger(backend.logger)
	assets, err := resolveWebAssets()
	if err != nil {
		return nil, err
	}
	sessions, err := newSessionManager(cliCfg.SessionTTL, cfg.MainSettings.DBPath)
	if err != nil {
		return nil, err
	}
	srv := &Server{
		cfg:            cfg,
		cliCfg:         cliCfg,
		backend:        backend,
		picker:         newNativePicker(),
		auth:           authStore,
		sessions:       sessions,
		hub:            hub,
		authLimiter:    newFixedWindowLimiter(10, 5*time.Minute),
		generalLimiter: newFixedWindowLimiter(300, time.Minute),
		trustedProxies: parseTrustedProxies(cliCfg.TrustedProxies),
		assets:         assets,
	}
	if opts.DevelopmentNoAuth {
		csrf, err := randomString(24)
		if err != nil {
			return nil, err
		}
		srv.developmentNoAuth = true
		srv.developmentSession = session{
			ID:        "dev-no-auth",
			Username:  "dev",
			CSRFToken: csrf,
			ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
		}
	}
	sessions.SetLogger(func(format string, args ...any) {
		backend.logger.Warnf(format, args...)
	})
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	srv.server = &http.Server{
		Addr:              net.JoinHostPort(cliCfg.Host, strconv.Itoa(cliCfg.Port)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv, nil
}

func isDevelopmentNoAuthHost(host string) bool {
	return isLoopbackHostPort(host)
}

// Close releases session and backend resources owned by the server.
func (s *Server) Close() error {
	if s.sessions != nil {
		s.sessions.Close()
	}
	if s.backend != nil {
		_ = s.backend.Close()
	}
	return nil
}

// Run binds the configured TCP address and serves until ctx is cancelled or a shutdown signal arrives.
func (s *Server) Run(ctx context.Context) error {
	return s.run(ctx, nil)
}

// RunAfterListen runs the server and calls afterListen once the TCP listener
// has bound successfully, before HTTP serving starts. If afterListen fails,
// the listener is closed and the server does not start accepting requests.
func (s *Server) RunAfterListen(ctx context.Context, afterListen func() error) error {
	if afterListen == nil {
		return s.Run(ctx)
	}
	return s.run(ctx, afterListen)
}

func (s *Server) run(ctx context.Context, afterListen func() error) error {
	if ctx == nil {
		return errors.New("webserver: context is required")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("webserver: listen %s: %w", s.server.Addr, err)
	}
	if afterListen != nil {
		if err := afterListen(); err != nil {
			_ = listener.Close()
			return err
		}
	}
	return s.serve(ctx, listener)
}

func (s *Server) serve(ctx context.Context, listener net.Listener) error {
	errCh := make(chan error, 1)
	browserCtx, cancelBrowser := context.WithCancel(ctx)
	defer cancelBrowser()
	s.server.BaseContext = func(net.Listener) context.Context {
		return ctx
	}
	go func() {
		errCh <- s.server.Serve(listener)
	}()
	browserURL := s.baseURL(listener.Addr())
	s.logServeAddress(listener.Addr(), browserURL)

	if s.cliCfg.OpenBrowser {
		go func() {
			timer := time.NewTimer(300 * time.Millisecond)
			defer timer.Stop()

			select {
			case <-browserCtx.Done():
				return
			case <-timer.C:
			}

			if browserCtx.Err() != nil {
				return
			}
			_ = openBrowserURL(browserURL)
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("webserver: shutdown HTTP server: %w", err)
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// logServeAddress records the effective listener address after a successful
// bind, along with a redacted browser URL when one is available.
func (s *Server) logServeAddress(addr net.Addr, browserURL string) {
	if s == nil || s.backend == nil || s.backend.logger == nil || addr == nil {
		return
	}
	loggedBrowserURL := redactLoggedBrowserURL(browserURL)
	if loggedBrowserURL == "" {
		s.backend.logger.Infof("web: serving web UI on %s", addr.String())
		return
	}
	s.backend.logger.Infof("web: serving web UI on %s (browser URL %s)", addr.String(), loggedBrowserURL)
}

// redactLoggedBrowserURL returns a log-safe browser URL, removing userinfo and
// query values while keeping the scheme, host, path, and query keys visible.
func redactLoggedBrowserURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return redaction.RedactValue(trimmed, nil)
	}

	safe := *parsed
	safe.User = nil
	if safe.RawQuery != "" {
		safe.RawQuery = redactLoggedBrowserURLQuery(safe.Query()).Encode()
	}

	return redaction.RedactValue(safe.String(), nil)
}

// redactLoggedBrowserURLQuery preserves query keys and value counts while
// replacing every query value with a redaction marker.
func redactLoggedBrowserURLQuery(values url.Values) url.Values {
	redacted := make(url.Values, len(values))
	for key, items := range values {
		if len(items) == 0 {
			redacted[key] = nil
			continue
		}
		redactedItems := make([]string, len(items))
		for i := range items {
			redactedItems[i] = "[REDACTED]"
		}
		redacted[key] = redactedItems
	}
	return redacted
}

// baseURL returns the URL opened in a browser. Explicit BaseURL wins; otherwise
// the URL uses the effective listener port and a navigable host for wildcard binds.
func (s *Server) baseURL(addr net.Addr) string {
	if strings.TrimSpace(s.cliCfg.BaseURL) != "" {
		return strings.TrimSpace(s.cliCfg.BaseURL)
	}
	port := s.cliCfg.Port
	if tcpAddr, ok := addr.(*net.TCPAddr); ok && tcpAddr.Port > 0 {
		port = tcpAddr.Port
	}
	host := browserHost(s.cliCfg.Host)
	hostPort := net.JoinHostPort(host, strconv.Itoa(port))
	if needsBrowserURLHostEscaping(host) {
		return (&url.URL{
			Scheme: "http",
			Host:   hostPort,
		}).String()
	}
	return "http://" + hostPort
}

// browserHost maps unspecified bind addresses to localhost because wildcard
// addresses are valid listen targets but not useful browser destinations.
func browserHost(host string) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return "localhost"
	}

	normalized, ok := unwrapBrowserIPv6Host(trimmed)
	if ok {
		trimmed = normalized
	}

	if ip, err := netip.ParseAddr(trimmed); err == nil && ip.IsUnspecified() {
		return "localhost"
	}
	return trimmed
}

// needsBrowserURLHostEscaping reports whether URL construction must
// percent-encode an IPv6 zone before the host is embedded in a browser URL.
func needsBrowserURLHostEscaping(host string) bool {
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Is6() && addr.Zone() != ""
}

// unwrapBrowserIPv6Host removes balanced brackets only for valid IPv6
// literals, leaving malformed host text unchanged for the browser URL builder.
func unwrapBrowserIPv6Host(host string) (string, bool) {
	if !strings.ContainsAny(host, "[]") {
		return host, true
	}
	if !strings.HasPrefix(host, "[") || !strings.HasSuffix(host, "]") {
		return host, false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
	if inner == "" || strings.ContainsAny(inner, "[]") {
		return host, false
	}
	addr, err := netip.ParseAddr(inner)
	if err != nil || !addr.Is6() {
		return host, false
	}
	return inner, true
}

func resolveWebAssets() (fs.FS, error) {
	assets, err := guiapp.ResolveAssets(nil)
	if err == nil {
		return assets, nil
	}

	// Keep the legacy repo-local fallback so local development can still serve
	// generated assets even if embedding was skipped for some reason.
	distPath := filepath.Join("gui", "frontend", "dist")
	if stat, statErr := os.Stat(filepath.Join(distPath, "index.html")); statErr == nil && !stat.IsDir() {
		return os.DirFS(distPath), nil
	}

	return nil, fmt.Errorf("web assets not found: %w", err)
}

func parseTrustedProxies(values []string) []*net.IPNet {
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if !strings.Contains(trimmed, "/") {
			if ip := net.ParseIP(trimmed); ip != nil {
				bits := 128
				if ip.To4() != nil {
					bits = 32
				}
				result = append(result, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			}
			continue
		}
		_, network, err := net.ParseCIDR(trimmed)
		if err == nil {
			result = append(result, network)
		}
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	raw = append(raw, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(raw); err != nil {
		return
	}
}
