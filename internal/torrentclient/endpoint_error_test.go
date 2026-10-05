// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	qbittorrent "github.com/autobrr/go-qbittorrent"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
	retry "github.com/avast/retry-go"
)

func TestSafeClientErrorPreservesCause(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"https://synthetic-user:synthetic-pass@client.example:8443/private-prefix", "https://client.example:8443/private-prefix", "http://192.0.2.7:8080/private-prefix", "http://[2001:db8::7]:8080/private-prefix"} {
		cause := &url.Error{
			Op:  "Get",
			URL: endpoint + "/api/v2/torrents/info?token=synthetic-token",
			Err: context.DeadlineExceeded,
		}
		original := fmt.Errorf("clients: synthetic search: %w", cause)
		safe := safeClientError(original, endpoint)
		if !errors.Is(safe, context.DeadlineExceeded) {
			t.Fatal("lost deadline identity")
		}
		var retained *url.Error
		if !errors.As(safe, &retained) || retained != cause {
			t.Fatal("lost request error identity")
		}
		if !strings.Contains(safe.Error(), "clients: synthetic search:") || !strings.Contains(safe.Error(), "deadline exceeded") {
			t.Fatalf("lost safe context: %s", safe)
		}
		for _, private := range []string{endpoint, "private-prefix", "synthetic-token", "synthetic-user", "synthetic-pass"} {
			if strings.Contains(safe.Error(), private) {
				t.Fatalf("retained private component %q", private)
			}
		}
	}
	dns := &net.DNSError{
		Err:    "no such host",
		Name:   "client.example",
		Server: "192.0.2.53:53",
	}
	if got := safeClientError(fmt.Errorf("clients: lookup: %w", dns), "http://client.example").Error(); strings.Contains(got, "client.example") || strings.Contains(got, "192.0.2.53") {
		t.Fatalf("network diagnostic retained endpoint: %s", got)
	}
	original := errors.New("HTTP status 403")
	if !errors.Is(safeClientError(original, "https://client.example"), original) {
		t.Fatal("changed safe status error")
	}
	if safeClientError(nil, "") != nil {
		t.Fatal("changed nil error")
	}
}

func TestClientEndpointSearchLogs(t *testing.T) {
	t.Parallel()
	for _, proxy := range []bool{false, true} {
		t.Run(strconv.FormatBool(proxy), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/auth/login") {
					_, _ = w.Write([]byte("Ok."))
					return
				}
				_, _ = w.Write([]byte("[]"))
			}))
			defer server.Close()
			endpoint := server.URL + "/private-prefix"
			dbPath := filepath.Join(t.TempDir(), "upbrr.db")
			root, err := logging.New(config.LoggingConfig{
				Level:          "trace",
				FileEnabled:    true,
				MaxTotalSizeMB: 1,
				MaxFiles:       1,
			}, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			var output bytes.Buffer
			root.SetConsoleOutput(&output, &output)
			subscription, stream := root.Subscribe(100)
			defer root.Unsubscribe(subscription)
			scoped, err := logging.NewOperationLogger(root, "trace")
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.TorrentClientConfig{
				Type:     "qbit",
				QbitURL:  endpoint,
				QbitUser: "synthetic",
				QbitPass: "synthetic",
			}
			if proxy {
				cfg.QuiProxyURL = endpoint
			}
			service := NewService(config.Config{TorrentClients: map[string]config.TorrentClientConfig{"synthetic": cfg}}, root)
			ctx := logging.WithOperationLogger(context.Background(), scoped)
			_, err = service.SearchPathedTorrents(ctx, api.ClientSubject{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.2026.mkv")})
			if err != nil {
				t.Fatal(err)
			}
			err = service.Inject(ctx, api.ClientSubject{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.2026.mkv")}, api.TorrentResult{URL: "https://tracker.example/download/1"})
			if err != nil {
				t.Fatal(err)
			}
			cause := &url.Error{
				Op:  "Get",
				URL: endpoint + "/api/v2/torrents/properties?token=synthetic-token",
				Err: context.DeadlineExceeded,
			}
			for _, write := range []func(string, ...any){scoped.Tracef, scoped.Debugf, scoped.Infof, scoped.Warnf, scoped.Errorf} {
				write("clients: synthetic lookup: %v", safeClientError(cause, endpoint))
			}
			if strings.Contains(output.String(), server.URL) || strings.Contains(output.String(), "private-prefix") {
				t.Fatal("console retained client endpoint")
			}
			for _, entry := range root.Recent(100) {
				if strings.Contains(entry.Message, server.URL) || strings.Contains(entry.Message, "private-prefix") {
					t.Fatal("buffer retained client endpoint")
				}
			}
			for len(stream) > 0 {
				entry := <-stream
				if strings.Contains(entry.Message, server.URL) {
					t.Fatal("subscriber retained endpoint")
				}
			}
			public := "https://image.example/public.jpg"
			root.Debugf("image=%s", public)
			if !strings.Contains(output.String(), public) {
				t.Fatal("public URL was scrubbed")
			}
			if err := root.Close(); err != nil {
				t.Fatal(err)
			}
			file, err := os.ReadFile(filepath.Join(filepath.Dir(dbPath), "logs", "upbrr.log"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(file), server.URL) || strings.Contains(string(file), "private-prefix") {
				t.Fatal("file retained endpoint")
			}
		})
	}
}

func TestClientEndpointInjectionFailure(t *testing.T) {
	t.Parallel()
	for _, proxy := range []bool{false, true} {
		t.Run(strconv.FormatBool(proxy), func(t *testing.T) {
			server := httptest.NewServer(http.NotFoundHandler())
			endpoint := server.URL + "/private-prefix"
			server.Close()
			cfg := config.TorrentClientConfig{
				Type:     "qbit",
				QbitURL:  endpoint,
				QbitUser: "synthetic",
				QbitPass: "synthetic",
			}
			if proxy {
				cfg.QuiProxyURL = endpoint
			}
			logger, err := logging.New(config.LoggingConfig{Level: "trace"}, "")
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			var output bytes.Buffer
			logger.SetConsoleOutput(&output, &output)
			service := NewService(config.Config{TorrentClients: map[string]config.TorrentClientConfig{"synthetic": cfg}}, logger)
			err = service.Inject(context.Background(), api.ClientSubject{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.2026.mkv")}, api.TorrentResult{URL: "https://tracker.example/download/1"})
			if err == nil {
				t.Fatal("expected connection failure")
			}
			if _, ok := errors.AsType[*url.Error](err); !ok {
				t.Fatalf("lost connection error: %v", err)
			}
			for _, text := range []string{err.Error(), output.String()} {
				if strings.Contains(text, server.URL) || strings.Contains(text, "private-prefix") || strings.Contains(text, strings.TrimPrefix(server.URL, "http://")) {
					t.Fatalf("failure retained endpoint: %s", text)
				}
				if !strings.Contains(text, "client request failed") {
					t.Fatal("lost safe failure classification")
				}
			}
		})
	}
}

func TestClientEndpointRetriedExportFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "synthetic failure", http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, suffix := range []string{"/private-prefix", "/private-prefix?configured=1", "/private-prefix//nested/../?configured=1"} {
		endpoint := server.URL + suffix
		logger := &captureLogger{}
		cfg := config.Config{}
		cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "upbrr.db")
		service := NewService(cfg, logger)
		client := qbittorrent.NewClient(qbittorrent.Config{Host: endpoint, RetryAttempts: 1})
		_, err := service.selectValidTorrent(context.Background(), api.ClientSubject{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.2026.mkv")}, []api.TorrentMatch{{Hash: strings.Repeat("a", 40)}}, nil, pieceConstraints{}, client, nil, endpoint, false)
		if err != nil {
			t.Fatal(err)
		}
		message := strings.Join(logger.debug, "\n")
		if !strings.Contains(message, "export torrent failed") || !strings.Contains(message, "500") {
			t.Fatalf("missing safe export failure: %s", message)
		}
		if strings.Contains(message, server.URL) || strings.Contains(message, "private-prefix") || strings.Contains(message, strings.TrimPrefix(server.URL, "http://")) {
			t.Fatalf("retained export endpoint: %s", message)
		}
	}
}

func TestClientEndpointRetryAggregate(t *testing.T) {
	t.Parallel()
	endpoint := "https://client.example:8443/private-prefix"
	request := &url.Error{
		Op:  "Get",
		URL: endpoint + "/api/v2/torrents/info",
		Err: &net.DNSError{
			Err:    "no such host",
			Name:   "client.example",
			Server: "192.0.2.53:53",
		},
	}
	attempts := retry.Error{request}
	original := fmt.Errorf("clients: synthetic search: %w", attempts)
	safe := safeClientError(original, endpoint)
	var retained retry.Error
	if !errors.As(safe, &retained) || len(retained) != 1 || !errors.Is(retained[0], request) {
		t.Fatal("lost retry error identity")
	}
	for _, private := range []string{"client.example", "192.0.2.53", "private-prefix"} {
		if strings.Contains(safe.Error(), private) {
			t.Fatalf("retry retained %s: %s", private, safe)
		}
	}
	if !strings.Contains(safe.Error(), "client request failed") {
		t.Fatalf("lost classification: %s", safe)
	}
}
