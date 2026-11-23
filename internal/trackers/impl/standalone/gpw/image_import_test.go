// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type importTestTransport func(*http.Request) (*http.Response, error)

func (f importTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNativeImportProtocolAndNoRedirectForwarding(t *testing.T) {
	for _, test := range []struct {
		name, body       string
		status           int
		unknown, success bool
	}{
		{"success", `{"status":200,"response":{"files":[{"name":"https://kshare.club/example.png"}]}}`, 200, false, true},
		{"API error", `{"status":200,"response":{"Error":"rejected"}}`, 200, false, false},
		{"HTML", `<html>unavailable</html>`, 200, true, false},
		{"wrong count", `{"status":200,"response":{"files":[]}}`, 200, true, false},
		{"wrong host", `{"status":200,"response":{"files":[{"name":"https://kshare.club.attacker.example/example.png"}]}}`, 200, true, false},
		{"unsafe scheme", `{"status":200,"response":{"files":[{"name":"file:///example.png"}]}}`, 200, true, false},
		{"empty JSON", `{}`, 200, true, false},
		{"unexpected status", `{"status":500}`, 200, true, false},
		{"redirect302", ``, 302, true, false}, {"redirect303", ``, 303, true, false},
		{"redirect307", ``, 307, true, false}, {"redirect308", ``, 308, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var redirected atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				redirected.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer destination.Close()
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodPost || r.URL.Query().Get("action") != "img_upload" || r.URL.Query().Get("api_key") != "synthetic-key" {
					t.Error("invalid native request")
				}
				if err := r.ParseForm(); err != nil || len(r.PostForm["urls[]"]) != 1 || r.PostForm.Get("urls[]") != "https://images.example/source.png" {
					t.Error("invalid source association")
				}
				w.Header().Set("Location", destination.URL+"/private")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			local, _ := url.Parse(server.URL)
			client := server.Client()
			client.Transport = importTestTransport(func(r *http.Request) (*http.Response, error) {
				clone := r.Clone(r.Context())
				clone.URL.Scheme = local.Scheme
				clone.URL.Host = local.Host
				return http.DefaultTransport.RoundTrip(clone)
			})
			result, err := importImageURL(t.Context(), client, "synthetic-key", "https://images.example/source.png")
			failure, unknown := api.AsOperationFailure(err)
			unknown = unknown && failure.Code == api.OperationFailureUnknownOutcome
			if requests != 1 || redirected.Load() != 0 || (err == nil) != test.success || unknown != test.unknown || test.success && result != "https://kshare.club/example.png" {
				t.Fatal("native protocol outcome or redirect boundary changed")
			}
		})
	}
}

func TestNativeImportCancellationPreservesUnknownOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	client := &http.Client{Transport: importTestTransport(func(*http.Request) (*http.Response, error) { cancel(); return nil, context.Canceled })}
	_, err := importImageURL(ctx, client, "synthetic-key", "https://images.example/source.png")
	if failure, ok := api.AsOperationFailure(err); !ok || failure.Code != api.OperationFailureUnknownOutcome {
		t.Fatal("interrupted POST became retryable")
	}
}
