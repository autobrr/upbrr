// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package connectiontest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestArrChecksSelectedUnsavedInstanceAndResponse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{
			name:   "authenticated",
			body:   `{"appName":"Sonarr","version":"4.0.0"}`,
			code:   "success",
			status: http.StatusOK,
		},
		{
			name:   "unauthorized",
			body:   "synthetic-secret",
			code:   "authentication_required",
			status: http.StatusUnauthorized,
		},
		{
			name:   "forbidden is not proof of invalid credentials",
			body:   "synthetic-secret",
			code:   "access_denied",
			status: http.StatusForbidden,
		},
		{
			name:   "HTML login",
			body:   "<html>synthetic-secret</html>",
			code:   "unexpected_response",
			status: http.StatusOK,
		},
		{
			name:   "wrong application",
			body:   `{"appName":"Radarr","version":"6.0.0"}`,
			code:   "unexpected_response",
			status: http.StatusOK,
		},
		{
			name:   "oversized",
			body:   strings.Repeat("x", responseLimit+1),
			code:   "unexpected_response",
			status: http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/base/api/v3/system/status" || r.Header.Get("X-Api-Key") != "synthetic-draft-key" {
					t.Error("request did not use only the selected draft URL/key")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			cfg := config.Config{ArrIntegration: config.ArrIntegrationConfig{
				SonarrURL:     "http://unused.invalid",
				SonarrAPIKey:  "unused-key",
				SonarrURL2:    server.URL + "/base/",
				SonarrAPIKey2: "synthetic-draft-key",
			}}
			got := Test(t.Context(), cfg, api.ConnectionTestTarget{Kind: "sonarr", Instance: "2"})
			if got.Code != tc.code || requests.Load() != 1 || strings.Contains(got.Message, "synthetic-") || strings.Contains(got.Message, server.URL) {
				t.Fatalf("unexpected or unsafe outcome: %s", got.Code)
			}
		})
	}
}

func TestArrRedirectDoesNotForwardCredentials(t *testing.T) {
	t.Parallel()
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer origin.Close()
	cfg := config.Config{ArrIntegration: config.ArrIntegrationConfig{RadarrURL: origin.URL, RadarrAPIKey: "synthetic-key"}}
	got := Test(t.Context(), cfg, api.ConnectionTestTarget{Kind: "radarr", Instance: "0"})
	if got.Code != "unexpected_response" || forwarded.Load() != 0 {
		t.Fatalf("redirect was accepted or followed: %s", got.Code)
	}
}

func TestPublicProviderProbesUseFixedContentFreeRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, instance, host, path, method, response, code string
		status                                             int
	}{
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `{"success":true,"status_code":1}`,
			code:     "success",
		},
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `{"status_code":1,"status_message":"Success."}`,
			code:     "success",
		},
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `{"success":true}`,
			code:     "success",
		},
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `{}`,
			code:     "success",
		},
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `{"success":true,"status_code":7}`,
			code:     "remote_error",
		},
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `<html>Generic availability page</html>`,
			code:     "unexpected_response",
		},
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `null`,
			code:     "unexpected_response",
		},
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `{"success":false,"status_code":7,"status_message":"synthetic-private-error"}`,
			code:     "authentication_required",
			status:   http.StatusUnauthorized,
		},
		{
			kind:     "tmdb",
			host:     "api.themoviedb.org",
			path:     "/3/authentication",
			method:   http.MethodGet,
			response: `{"success":false,"status_code":7,"status_message":"synthetic-private-error"}`,
			code:     "remote_error",
		},
		{
			kind:     "srrdb",
			host:     "api.srrdb.com",
			path:     "/v1/search/r:upbrr.connection.test.synthetic",
			method:   http.MethodGet,
			response: `{"resultsCount":0,"results":[]}`,
			code:     "success",
		},
	} {
		t.Run(tc.kind+tc.instance, func(t *testing.T) {
			t.Parallel()
			requests := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.URL.Host != tc.host || r.URL.Path != tc.path || r.Method != tc.method {
					t.Fatal("probe used an unexpected operation")
				}
				if tc.kind == "tmdb" {
					if r.URL.Query().Get("api_key") != "synthetic-tmdb-key" || r.Header.Get("Authorization") != "" {
						t.Fatal("TMDB key was not used")
					}
				} else if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
					t.Fatal("unrelated credentials sent")
				}

				status := tc.status
				if status == 0 {
					status = http.StatusOK
				}
				return &http.Response{
					StatusCode: status,
					Body:       io.NopCloser(strings.NewReader(tc.response)),
					Header:     make(http.Header),
				}, nil
			})}
			cfg := config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "synthetic-tmdb-key"}}
			got := test(t.Context(), cfg, api.ConnectionTestTarget{Kind: tc.kind, Instance: tc.instance}, client)
			if got.Code != tc.code || requests != 1 || strings.Contains(got.Message, "synthetic-") {
				t.Fatalf("probe outcome: %s", got.Code)
			}
		})
	}
}

type cancelAtEOFReader struct {
	*strings.Reader
	cancel context.CancelFunc
}

func (r cancelAtEOFReader) Read(buffer []byte) (int, error) {
	n, err := r.Reader.Read(buffer)
	if err == io.EOF {
		r.cancel()
		return n, io.EOF
	}
	return n, nil
}

func TestCanceledAfterBodyReadSkipsNestedHTMLClassification(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Each group stays inside net/html's 512-element depth limit; the total
	// response stays below the probe's byte limit and contains no notice text.
	group := strings.Repeat(`<div class="alert-danger">`, 500) + strings.Repeat(`</div>`, 500)
	body := `<html><body>` + strings.Repeat(group, 60) + `</body></html>`
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(cancelAtEOFReader{Reader: strings.NewReader(body), cancel: cancel})}, nil
	})}
	got := test(ctx, config.Config{}, api.ConnectionTestTarget{Kind: "srrdb"}, client)
	if got.Code != "canceled" {
		t.Fatalf("canceled response was classified: %s", got.Code)
	}
}

func BenchmarkConnectionTestNestedEmptyNotices(b *testing.B) {
	group := strings.Repeat(`<div class="alert-danger">`, 500) + strings.Repeat(`</div>`, 500)
	body := `<html><body>` + strings.Repeat(group, 60) + `</body></html>`
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	b.ReportAllocs()
	for b.Loop() {
		got := test(b.Context(), config.Config{}, api.ConnectionTestTarget{Kind: "srrdb"}, client)
		if got.Code != "unexpected_response" {
			b.Fatalf("empty notices identified a service: %s", got.Code)
		}
	}
}

func TestQbitProbeLoginProxyAndTLS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, loginBody, code string
		proxy, insecure       bool
	}{
		{
			name:      "direct",
			loginBody: "Ok.",
			code:      "success",
			insecure:  true,
		},
		{
			name:      "rejected",
			loginBody: "Fails.",
			code:      "credentials_rejected",
			insecure:  true,
		},
		{
			name:      "unverified login body",
			loginBody: "<html>login</html>",
			code:      "unexpected_response",
			insecure:  true,
		},
		{name: "certificate verification", code: "tls_error"},
		{
			name:     "qui proxy",
			proxy:    true,
			code:     "success",
			insecure: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logins, versions atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/base/api/v2/auth/login":
					logins.Add(1)
					if tc.proxy {
						t.Error("proxy attempted direct login")
					}
					if r.Header.Get("Referer") == "" {
						t.Error("direct qBittorrent login lost its Referer")
					}
					if r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("username") != "synthetic-user" || r.Form.Get("password") != "synthetic-password" {
						t.Error("login did not use the draft credentials")
					}
					http.SetCookie(w, &http.Cookie{
						Name:  "SID",
						Value: "synthetic-session",
						Path:  "/",
					})
					_, _ = io.WriteString(w, tc.loginBody)
				case "/base/api/v2/app/version":
					versions.Add(1)
					if tc.proxy && (r.Header.Get("Referer") != "" || r.Header.Get("Origin") != "") {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					if r.Method != http.MethodGet {
						t.Error("version probe mutated state")
					}
					if !tc.proxy {
						if _, err := r.Cookie("SID"); err != nil {
							t.Error("version request lacked the login session")
						}
					}
					_, _ = io.WriteString(w, "v5.0.0")
				default:
					t.Error("probe used an unauthorized operation")
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			clientCfg := config.TorrentClientConfig{
				Type:          "qbit",
				QbitURL:       server.URL + "/base",
				QbitUser:      "synthetic-user",
				QbitPass:      "synthetic-password",
				TLSSkipVerify: &tc.insecure,
			}
			if tc.proxy {
				clientCfg.QuiProxyURL = server.URL + "/base"
				clientCfg.QbitURL = "http://unused.invalid"
			}
			cfg := config.Config{TorrentClients: map[string]config.TorrentClientConfig{"selected": clientCfg}}
			got := Test(t.Context(), cfg, api.ConnectionTestTarget{Kind: "torrent_client", Instance: "selected"})
			if got.Code != tc.code {
				t.Fatalf("probe outcome: %s", got.Code)
			}
			if tc.code == "success" && versions.Load() != 1 {
				t.Fatal("success without the authenticated version request")
			}
			if tc.code != "success" && versions.Load() != 0 {
				t.Fatal("version requested after rejected/unverified login")
			}
			if tc.proxy && logins.Load() != 0 {
				t.Fatal("proxy used direct login")
			}
		})
	}
}

func TestProbeExplainsHTTPAndRecognizedErrorsWithoutRemoteContent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, code, guidance string
		status                     int
	}{
		{"unauthorized is ambiguous", `synthetic-private-error`, "authentication_required", "HTTP 401", http.StatusUnauthorized},
		{"missing endpoint", `synthetic-private-error`, "unexpected_response", "HTTP 404", http.StatusNotFound},
		{"server unavailable", `synthetic-private-error`, "unavailable", "HTTP 503", http.StatusServiceUnavailable},
		{"bad gateway", `synthetic-private-error`, "unavailable", "HTTP 502", http.StatusBadGateway},
		{"redirect", `synthetic-private-error`, "unexpected_response", "HTTP 302", http.StatusFound},
		{"json failure", `{"success":false,"message":"synthetic-private-error","resultsCount":0,"results":[]}`, "remote_error", "error", http.StatusOK},
		{"json rpc failure", `{"error":{"code":-32000,"message":"synthetic-private-error"},"resultsCount":0,"results":[]}`, "remote_error", "error", http.StatusOK},
		{"validation failure", `{"errors":{"api_key":["synthetic-private-error"]},"resultsCount":0,"results":[]}`, "remote_error", "error", http.StatusOK},
		{"maintenance page", `<title>Service Unavailable</title><p>synthetic-private-error</p>`, "unavailable", "unavailable", http.StatusOK},
		{"challenge page", `<title>Just a moment...</title><p>synthetic-private-error</p>`, "challenge_required", "challenge", http.StatusOK},
		{"normal response", `{"resultsCount":0,"results":[],"error":null}`, "success", "SRRDB", http.StatusOK},
		{"spaced empty error array", `{"resultsCount":0,"results":[],"errors":[ ]}`, "success", "SRRDB", http.StatusOK},
		{"spaced empty error object", "{\"resultsCount\":0,\"results\":[],\"errors\":{\n}}", "success", "SRRDB", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			got := test(t.Context(), config.Config{}, api.ConnectionTestTarget{Kind: "srrdb"}, client)
			if got.Code != tc.code || !strings.Contains(got.Message, tc.guidance) || strings.Contains(got.Message, "synthetic-") {
				t.Fatalf("unsafe or unexplained response: %#v", got)
			}
		})
	}
}

func TestQbitProbeRejectsRedirectWithoutContactingDestination(t *testing.T) {
	t.Parallel()
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		_, _ = io.WriteString(w, "v5.0.0")
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/base/api/v2/app/version" {
			t.Error("unexpected redirect probe operation")
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer origin.Close()
	cfg := config.Config{TorrentClients: map[string]config.TorrentClientConfig{
		"selected": {Type: "qbit", QbitURL: origin.URL + "/base"},
	}}
	got := Test(t.Context(), cfg, api.ConnectionTestTarget{Kind: "torrent_client", Instance: "selected"})
	if got.Code == "success" || destinationRequests.Load() != 0 {
		t.Fatalf("redirect result=%s destination requests=%d", got.Code, destinationRequests.Load())
	}
}

func TestCanceledAndIncompleteProbesDoNotRequest(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected remote request"); return nil, nil })}
	for _, tc := range []struct {
		cfg    config.Config
		target api.ConnectionTestTarget
		code   string
	}{
		{target: api.ConnectionTestTarget{Kind: "tmdb"}, code: "missing_configuration"},
		{
			cfg:    config.Config{MainSettings: config.MainSettingsConfig{TMDBAPI: "[REDACTED]"}},
			target: api.ConnectionTestTarget{Kind: "tmdb"},
			code:   "missing_configuration",
		},
		{target: api.ConnectionTestTarget{Kind: "sonarr", Instance: "4"}, code: "invalid_configuration"},
		{target: api.ConnectionTestTarget{Kind: "radarr", Instance: "1"}, code: "missing_configuration"},
	} {
		if got := test(t.Context(), tc.cfg, tc.target, client); got.Code != tc.code {
			t.Fatalf("local outcome: %s", got.Code)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := test(ctx, config.Config{}, api.ConnectionTestTarget{Kind: "tmdb"}, client); got.Code != "canceled" {
		t.Fatalf("cancel outcome: %s", got.Code)
	}
	ctx, cancel = context.WithDeadline(t.Context(), time.Time{})
	defer cancel()
	if got := test(ctx, config.Config{}, api.ConnectionTestTarget{Kind: "tmdb"}, client); got.Code != "timeout" {
		t.Fatalf("timeout outcome: %s", got.Code)
	}
}

func TestRequestCancellationStopsRunningProbe(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(stopped) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	completed := make(chan api.ConnectionTestResult, 1)
	go func() {
		cfg := config.Config{ArrIntegration: config.ArrIntegrationConfig{SonarrURL: server.URL, SonarrAPIKey: "synthetic-key"}}
		completed <- Test(ctx, cfg, api.ConnectionTestTarget{Kind: "sonarr", Instance: "0"})
	}()
	<-started
	cancel()
	if got := <-completed; got.Code != "canceled" {
		t.Fatalf("canceled request outcome: %s", got.Code)
	}
	<-stopped
}
