// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
)

type serverCloseCore struct {
	preparedMetaTestCore
	closed *bool
}

func (c *serverCloseCore) Close() error {
	*c.closed = true
	return nil
}

func TestNewRejectsDevelopmentNoAuthOnNonLoopbackHost(t *testing.T) {
	_, err := New(Options{
		CLIConfig:         CLIConfig{Host: "0.0.0.0"},
		DevelopmentNoAuth: true,
	})
	if err == nil {
		t.Fatal("expected development no-auth on non-loopback host to fail")
	}
	if !strings.Contains(err.Error(), "--dev-no-auth requires a loopback host") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsDevelopmentNoAuthHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "localhost:7480", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "[::1]", want: true},
		{host: "[::1]:7480", want: true},
		{host: "0.0.0.0", want: false},
		{host: "::", want: false},
		{host: "192.168.1.20", want: false},
		{host: "example.com", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := isDevelopmentNoAuthHost(tc.host); got != tc.want {
				t.Fatalf("isDevelopmentNoAuthHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestNewClosesBackendWhenAssetSetupFails(t *testing.T) {
	oldNewBackend := newBackendWithContextForServer
	oldResolveAssets := resolveWebAssetsForServer
	oldNewSessionManager := newSessionManagerForServer
	oldRandomString := randomStringForServer
	t.Cleanup(func() {
		newBackendWithContextForServer = oldNewBackend
		resolveWebAssetsForServer = oldResolveAssets
		newSessionManagerForServer = oldNewSessionManager
		randomStringForServer = oldRandomString
	})

	closed := false
	newBackendWithContextForServer = func(_ context.Context, cfg config.Config, _ *eventHub) (*Backend, error) {
		return &Backend{
			cfg:       cfg,
			coreOwner: &serverCloseCore{closed: &closed},
		}, nil
	}
	resolveWebAssetsForServer = func() (fs.FS, error) {
		return nil, errors.New("asset setup failed")
	}
	newSessionManagerForServer = func(int, string) (*sessionManager, error) {
		t.Fatal("session manager should not be created after asset setup failure")
		return nil, nil
	}

	_, err := New(Options{
		Config: config.Config{
			MainSettings:       config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "state.db")},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
			Logging:            config.LoggingConfig{Level: "info"},
		},
	})
	if err == nil {
		t.Fatal("expected asset setup failure")
	}
	if !closed {
		t.Fatal("expected backend core to close after post-construction startup failure")
	}
}

func TestNewClosesSessionManagerWhenDevelopmentCSRFFails(t *testing.T) {
	oldNewBackend := newBackendWithContextForServer
	oldResolveAssets := resolveWebAssetsForServer
	oldNewSessionManager := newSessionManagerForServer
	oldRandomString := randomStringForServer
	t.Cleanup(func() {
		newBackendWithContextForServer = oldNewBackend
		resolveWebAssetsForServer = oldResolveAssets
		newSessionManagerForServer = oldNewSessionManager
		randomStringForServer = oldRandomString
	})

	newBackendWithContextForServer = func(_ context.Context, cfg config.Config, _ *eventHub) (*Backend, error) {
		return &Backend{cfg: cfg}, nil
	}
	resolveWebAssetsForServer = func() (fs.FS, error) {
		return fstest.MapFS{"index.html": {Data: []byte("ok")}}, nil
	}
	sessionClosed := make(chan struct{})
	newSessionManagerForServer = func(int, string) (*sessionManager, error) {
		manager := &sessionManager{
			stopCh:   make(chan struct{}),
			doneCh:   make(chan struct{}),
			sessions: make(map[string]session),
		}
		go func() {
			<-manager.stopCh
			close(sessionClosed)
			close(manager.doneCh)
		}()
		return manager, nil
	}
	randomStringForServer = func(int) (string, error) {
		return "", errors.New("csrf failure")
	}

	_, err := New(Options{
		Config: config.Config{
			MainSettings:       config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "state.db")},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
			Logging:            config.LoggingConfig{Level: "info"},
		},
		CLIConfig:         CLIConfig{Host: "127.0.0.1"},
		DevelopmentNoAuth: true,
	})
	if err == nil {
		t.Fatal("expected CSRF failure")
	}
	select {
	case <-sessionClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session manager close")
	}
}

func TestIsLoopbackHostPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{host: "localhost:5173", want: true},
		{host: "127.0.0.1:7480", want: true},
		{host: "[::1]:7480", want: true},
		{host: "0.0.0.0:7480", want: false},
		{host: "192.168.1.20:7480", want: false},
		{host: "example.com:7480", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := isLoopbackHostPort(tc.host); got != tc.want {
				t.Fatalf("isLoopbackHostPort(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestLogServeAddressUsesInfo(t *testing.T) {
	t.Parallel()

	logger, err := logging.NewWithLevel(config.LoggingConfig{Level: "info"}, filepath.Join(t.TempDir(), "upbrr.db"), "")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	defer logger.Close()

	server := &Server{
		backend: &Backend{
			logger: logger,
		},
	}
	server.logServeAddress(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7480}, "http://127.0.0.1:7480")

	entries := logger.Recent(1)
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	if entries[0].Level != "info" {
		t.Fatalf("expected info log, got %q", entries[0].Level)
	}
	if !strings.Contains(entries[0].Message, "127.0.0.1:7480") {
		t.Fatalf("expected address in log, got %q", entries[0].Message)
	}
	if !strings.Contains(entries[0].Message, "http://127.0.0.1:7480") {
		t.Fatalf("expected browser URL in log, got %q", entries[0].Message)
	}
}

func TestLogServeAddressRedactsBrowserURLSecrets(t *testing.T) {
	t.Parallel()

	logger, err := logging.NewWithLevel(config.LoggingConfig{Level: "info"}, filepath.Join(t.TempDir(), "upbrr.db"), "")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	defer logger.Close()

	server := &Server{
		backend: &Backend{
			logger: logger,
		},
	}
	server.logServeAddress(
		&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7480},
		"https://user:pass@example.test/upbrr/?next=dashboard&token=abc123",
	)

	entries := logger.Recent(1)
	if len(entries) != 1 {
		t.Fatalf("expected one log entry, got %d", len(entries))
	}
	message := entries[0].Message
	if strings.Contains(message, "user") || strings.Contains(message, "pass") || strings.Contains(message, "abc123") || strings.Contains(message, "dashboard") {
		t.Fatalf("expected browser URL secrets redacted, got %q", message)
	}
	if !strings.Contains(message, "https://example.test/upbrr/") {
		t.Fatalf("expected safe browser URL host/path retained, got %q", message)
	}
}

func TestServerLoggerAccessSynchronizedDuringRuntimeReplacement(t *testing.T) {
	t.Parallel()

	loggingConfig := config.LoggingConfig{
		Level:          "info",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       1,
	}
	loggerA, err := logging.NewWithLevel(loggingConfig, filepath.Join(t.TempDir(), "runtime-a.db"), "")
	if err != nil {
		t.Fatalf("new logger A: %v", err)
	}
	loggerA.SetConsoleOutput(io.Discard, io.Discard)

	backend := &Backend{logger: loggerA}
	server := &Server{backend: backend}
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7480}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	replacementLogDir := t.TempDir()
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				logger, err := logging.NewWithLevel(loggingConfig, filepath.Join(replacementLogDir, "runtime-replacement-"+strconv.Itoa(i)+".db"), "")
				if err != nil {
					t.Errorf("new replacement logger: %v", err)
					return
				}
				logger.SetConsoleOutput(io.Discard, io.Discard)
				_, oldLogger := backend.replaceRuntime(config.Config{}, CoreCapabilities{}, logger)
				if oldLogger != nil {
					_ = oldLogger.Close()
				}
			}
		}
	})
	defer func() {
		close(stop)
		wg.Wait()
		if logger := backend.currentLogger(); logger != nil {
			_ = logger.Close()
		}
	}()

	for range 200 {
		server.logServeAddress(addr, "http://127.0.0.1:7480")
		backend.logWarnf("web: retained session persistence check")
	}
}

func TestBaseURLUsesLoopbackForWildcardBindHost(t *testing.T) {
	t.Parallel()

	server := &Server{
		cliCfg: CLIConfig{
			Host: "0.0.0.0",
			Port: 7480,
		},
	}
	got := server.baseURL(&net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 49152})
	if got != "http://localhost:49152" {
		t.Fatalf("baseURL() = %q, want loopback URL with listener port", got)
	}
}

func TestBaseURLPreservesExplicitBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "path trailing slash",
			baseURL: " https://example.test/upbrr/ ",
			want:    "https://example.test/upbrr/",
		},
		{
			name:    "root slash",
			baseURL: "https://example.test/",
			want:    "https://example.test/",
		},
		{
			name:    "query fragment stripped",
			baseURL: "https://example.test/upbrr/?token=secret#frag",
			want:    "https://example.test/upbrr/",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := &Server{
				cliCfg: CLIConfig{
					Host:    "0.0.0.0",
					Port:    7480,
					BaseURL: tc.baseURL,
				},
			}
			got := server.baseURL(&net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 49152})
			if got != tc.want {
				t.Fatalf("baseURL() = %q, want explicit BaseURL %q", got, tc.want)
			}
		})
	}
}

func TestBaseURLSynthesizesLocalOriginForPathOnlyBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		host    string
		baseURL string
		want    string
	}{
		{
			name:    "wildcard ipv4",
			host:    "0.0.0.0",
			baseURL: " /upbrr/ ",
			want:    "http://localhost:49152/upbrr/",
		},
		{
			name:    "wildcard ipv6",
			host:    "::",
			baseURL: "upbrr",
			want:    "http://localhost:49152/upbrr/",
		},
		{
			name:    "bracketed wildcard ipv6",
			host:    "[::]",
			baseURL: "/tools/upbrr",
			want:    "http://localhost:49152/tools/upbrr/",
		},
		{
			name:    "scoped ipv6",
			host:    "fe80::1%zone",
			baseURL: "/upbrr/",
			want:    "http://[fe80::1%25zone]:49152/upbrr/",
		},
		{
			name:    "query and fragment dropped",
			host:    "127.0.0.1",
			baseURL: "/upbrr/?token=secret#frag",
			want:    "http://127.0.0.1:49152/upbrr/",
		},
		{
			name:    "root path",
			host:    "127.0.0.1",
			baseURL: "/",
			want:    "http://127.0.0.1:49152",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := &Server{
				cliCfg: CLIConfig{
					Host:    tc.host,
					Port:    7480,
					BaseURL: tc.baseURL,
				},
			}
			got := server.baseURL(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 49152})
			if got != tc.want {
				t.Fatalf("baseURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBrowserHostNormalization(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want string
	}{
		{host: "", want: "localhost"},
		{host: "0.0.0.0", want: "localhost"},
		{host: "::", want: "localhost"},
		{host: "[::]", want: "localhost"},
		{host: "[fe80::1%zone]", want: "fe80::1%zone"},
		{host: "[::", want: "[::"},
		{host: "::]", want: "::]"},
		{host: "[[::]]", want: "[[::]]"},
		{host: "[]", want: "[]"},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := browserHost(tc.host); got != tc.want {
				t.Fatalf("browserHost(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

func TestBaseURLEscapesScopedIPv6Zone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want string
	}{
		{host: "fe80::1%zone", want: "http://[fe80::1%25zone]:49152"},
		{host: "[fe80::1%zone]", want: "http://[fe80::1%25zone]:49152"},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			server := &Server{
				cliCfg: CLIConfig{
					Host: tc.host,
					Port: 7480,
				},
			}
			got := server.baseURL(&net.TCPAddr{
				IP:   net.ParseIP("fe80::1"),
				Port: 49152,
				Zone: "zone",
			})
			if got != tc.want {
				t.Fatalf("baseURL() = %q, want %q", got, tc.want)
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", got, err)
			}
			if parsed.Hostname() != "fe80::1%zone" {
				t.Fatalf("parsed hostname = %q, want scoped IPv6 host", parsed.Hostname())
			}
		})
	}
}

func TestBaseURLPreservesMalformedBracketHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want string
	}{
		{host: "[::", want: "http://[[::]:49152"},
		{host: "[[::]]", want: "http://[[[::]]]:49152"},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			server := &Server{
				cliCfg: CLIConfig{
					Host: tc.host,
					Port: 7480,
				},
			}
			got := server.baseURL(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 49152})
			if got != tc.want {
				t.Fatalf("baseURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunAfterListenSkipsCallbackWhenBindFails(t *testing.T) {
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fixture: %v", err)
	}
	defer listener.Close()

	server := &Server{
		server: &http.Server{
			Addr: listener.Addr().String(),
		},
	}
	called := false
	err = server.RunAfterListen(context.Background(), func() error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "webserver: listen") {
		t.Fatalf("expected listen error, got %v", err)
	}
	if called {
		t.Fatal("afterListen callback ran despite bind failure")
	}
}

func TestRunAfterListenRunsCallbackAfterBind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &Server{
		server: &http.Server{
			Addr:    "127.0.0.1:0",
			Handler: http.NewServeMux(),
		},
	}
	afterListenCalled := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- server.RunAfterListen(ctx, func() error {
			close(afterListenCalled)
			cancel()
			return nil
		})
	}()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run after listen: %v", err)
		}
	case <-time.After(2 * time.Second):
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Fatalf("run after listen after timeout cancellation: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for RunAfterListen to return")
		}
	}
	select {
	case <-afterListenCalled:
	default:
		t.Fatal("afterListen callback was not called")
	}
}

func TestServeDoesNotOpenBrowserAfterServeError(t *testing.T) {
	originalOpenBrowserURL := openBrowserURL
	defer func() {
		openBrowserURL = originalOpenBrowserURL
	}()

	opened := make(chan string, 1)
	openBrowserURL = func(url string) error {
		opened <- url
		return nil
	}

	server := &Server{
		cliCfg: CLIConfig{
			Host:        "127.0.0.1",
			Port:        7480,
			OpenBrowser: true,
		},
		server: &http.Server{
			Handler: http.NewServeMux(),
		},
	}

	err := server.serve(context.Background(), failingListener{
		addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 49152},
		err:  errors.New("accept boom"),
	})
	if err == nil || !strings.Contains(err.Error(), "accept boom") {
		t.Fatalf("serve error = %v, want accept failure", err)
	}

	select {
	case url := <-opened:
		t.Fatalf("unexpected browser open for %q", url)
	case <-time.After(400 * time.Millisecond):
	}
}

func TestServeOpensBrowserAfterSuccessfulListen(t *testing.T) {
	originalOpenBrowserURL := openBrowserURL
	defer func() {
		openBrowserURL = originalOpenBrowserURL
	}()

	opened := make(chan string, 1)
	openBrowserURL = func(url string) error {
		opened <- url
		return nil
	}

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fixture: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &Server{
		cliCfg: CLIConfig{
			Host:        "127.0.0.1",
			Port:        7480,
			OpenBrowser: true,
		},
		server: &http.Server{
			Handler: http.NewServeMux(),
		},
	}

	runErr := make(chan error, 1)
	go func() {
		runErr <- server.serve(ctx, listener)
	}()

	select {
	case url := <-opened:
		want := "http://" + listener.Addr().String()
		if url != want {
			t.Fatalf("browser URL = %q, want %q", url, want)
		}
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for browser open")
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for serve shutdown")
	}
}

func TestServeOpensAbsoluteBrowserURLForPathOnlyBaseURL(t *testing.T) {
	originalOpenBrowserURL := openBrowserURL
	defer func() {
		openBrowserURL = originalOpenBrowserURL
	}()

	opened := make(chan string, 1)
	openBrowserURL = func(url string) error {
		opened <- url
		return nil
	}

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fixture: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &Server{
		cliCfg: CLIConfig{
			Host:        "127.0.0.1",
			Port:        7480,
			OpenBrowser: true,
			BaseURL:     "/upbrr/",
		},
		server: &http.Server{
			Handler: http.NewServeMux(),
		},
	}

	runErr := make(chan error, 1)
	go func() {
		runErr <- server.serve(ctx, listener)
	}()

	select {
	case url := <-opened:
		want := "http://" + listener.Addr().String() + "/upbrr/"
		if url != want {
			t.Fatalf("browser URL = %q, want %q", url, want)
		}
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for browser open")
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for serve shutdown")
	}
}

type failingListener struct {
	addr net.Addr
	err  error
}

func (l failingListener) Accept() (net.Conn, error) {
	return nil, l.err
}

func (l failingListener) Close() error {
	return nil
}

func (l failingListener) Addr() net.Addr {
	return l.addr
}
