// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Serial transport-only cases avoid rebuilding a database for response classification.
func TestBTNIPBanLoginClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"200_needs_code", http.StatusOK, ""},
		{"200_submitted_code", http.StatusOK, "123456"},
		{"403_submitted_code", http.StatusForbidden, "123456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			installBTNIPBanTransport(t, func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{
					StatusCode: tc.status,
					Body:       io.NopCloser(strings.NewReader(`<p>Your IP is banned indefinitely</p><input name="codenumber"><p>invalid code</p><p>192.0.2.1 synthetic-secret</p>`)),
					Request:    req,
				}, nil
			})
			client, err := loginBTNSession(t.Context(), config.TrackerConfig{Username: "synthetic-user", Password: "synthetic-password"},
				"https://tracker.invalid", api.TrackerAuthLoginRequest{Code: tc.code})
			assertBTNIPBanError(t, err)
			if client != nil || calls != 1 {
				t.Fatalf("ban returned a usable client or retried: calls=%d", calls)
			}
		})
	}
}

func TestBTNIPBanSessionRedirect(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: btnRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		response := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("Your IP is banned indefinitely")),
			Request:    req,
		}
		if req.URL.Path == btnUploadPath {
			response.StatusCode = http.StatusFound
			response.Header = http.Header{"Location": {btnLoginPath}}
		}
		return response, nil
	})}
	assertBTNIPBanError(t, validateBTNClientSession(t.Context(), client, "https://tracker.invalid"))
	if calls != 2 {
		t.Fatalf("expected one followed login redirect, got %d requests", calls)
	}
}

func installBTNIPBanTransport(t *testing.T, fn btnRoundTripperFunc) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = fn
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func assertBTNIPBanError(t *testing.T, err error) {
	t.Helper()
	resolution, ok := errors.AsType[*trackers.AuthResolutionError](err)
	if !ok || !resolution.Transient || resolution.ConfirmedInvalid || resolution.AuthRequired {
		t.Fatalf("expected cookie-preserving ban failure, got %v", err)
	}
	if errors.Is(err, ErrSubmitted2FARejected) || errors.Is(err, errBTNSessionConfirmedInvalid) {
		t.Fatal("ban classified as invalid authentication or a rejected code")
	}
	if !strings.Contains(resolution.PublicDetail, "IP indefinitely") || !strings.Contains(resolution.PublicDetail, "support process") {
		t.Fatalf("missing actionable ban feedback: %q", resolution.PublicDetail)
	}
	for _, secret := range []string{"192.0.2.1", "synthetic-secret", "<p>", "123456"} {
		if strings.Contains(err.Error(), secret) || strings.Contains(resolution.PublicDetail, secret) {
			t.Fatal("ban feedback exposed response data")
		}
	}
}
