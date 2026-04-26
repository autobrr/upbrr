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
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/browser"

	"github.com/autobrr/upbrr/internal/config"
)

type Options struct {
	StartupContext context.Context
	Config         config.Config
	CLIConfig      CLIConfig
	Assets         fs.FS
}

type Server struct {
	cfg            config.Config
	cliCfg         CLIConfig
	backend        *Backend
	picker         nativePicker
	auth           *authStore
	hub            *eventHub
	authLimiter    *fixedWindowLimiter
	generalLimiter *fixedWindowLimiter
	trustedProxies []*net.IPNet
	server         *http.Server
	assets         fs.FS
}

func New(opts Options) (*Server, error) {
	cfg := opts.Config
	cliCfg := normalizeCLIConfig(opts.CLIConfig)

	hub := newEventHub()
	authStore, err := newAuthStore(cfg.MainSettings.DBPath)
	if err != nil {
		return nil, err
	}
	backend, err := NewBackendWithContext(opts.StartupContext, cfg, hub)
	if err != nil {
		return nil, err
	}
	hub.SetLogger(backend.logger)
	assets := opts.Assets
	if assets == nil {
		var err error
		assets, err = resolveWebAssets()
		if err != nil {
			return nil, err
		}
	}
	srv := &Server{
		cfg:            cfg,
		cliCfg:         cliCfg,
		backend:        backend,
		picker:         newNativePicker(),
		auth:           authStore,
		hub:            hub,
		authLimiter:    newFixedWindowLimiter(10, 5*time.Minute),
		generalLimiter: newFixedWindowLimiter(300, time.Minute),
		trustedProxies: parseTrustedProxies(cliCfg.TrustedProxies),
		assets:         assets,
	}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	srv.server = &http.Server{
		Addr:              net.JoinHostPort(cliCfg.Host, strconv.Itoa(cliCfg.Port)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv, nil
}

func (s *Server) Close() error {
	if s.backend != nil {
		_ = s.backend.Close()
	}
	return nil
}

func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.server.ListenAndServe()
	}()

	if s.cliCfg.OpenBrowser {
		go func() {
			timer := time.NewTimer(300 * time.Millisecond)
			defer timer.Stop()

			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}

			if ctx.Err() != nil {
				return
			}
			_ = browser.OpenURL(s.baseURL())
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) ServeListener(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return errors.New("web server listener is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) baseURL() string {
	if strings.TrimSpace(s.cliCfg.BaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(s.cliCfg.BaseURL), "/")
	}
	return fmt.Sprintf("http://%s:%d", s.cliCfg.Host, s.cliCfg.Port)
}

func resolveWebAssets() (fs.FS, error) {
	// Keep the legacy repo-local fallback so local development can still serve
	// generated assets even if embedding was skipped for some reason.
	distPath := filepath.Join("gui", "frontend", "dist")
	if stat, statErr := os.Stat(filepath.Join(distPath, "index.html")); statErr == nil && !stat.IsDir() {
		return os.DirFS(distPath), nil
	}

	return nil, errors.New("web assets not found: build gui/frontend and retry")
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
