// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package auth

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/pkg/api"
)

// Use the real resolver once to protect the tracker/shared-auth boundary.
// Do not parallelize default-transport replacement; no live request is made.
func TestBTNIPBanSharedAuthPreservesStatusAndSession(t *testing.T) {
	calls := 0
	previous := http.DefaultTransport
	http.DefaultTransport = btnIPBanRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.Path != "/upload.php" {
			t.Fatalf("unexpected credential fallback: %s %s", req.Method, req.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`<p>Your IP is banned indefinitely</p><input name="codenumber"><p>192.0.2.1 synthetic-private-detail</p>`)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	dbPath := newTrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(t.Context(), dbPath, "BTN", map[string]string{"session": "synthetic-original"}); err != nil {
		t.Fatal(err)
	}
	cfg := config.TrackerConfig{
		Username: "synthetic-user",
		Password: "synthetic-password",
		APIKey:   "synthetic-api-key",
	}
	service := newTestService(t, config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Trackers:     config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BTN": cfg}},
	})
	_, err := service.EnsureSession(t.Context(), EnsureRequest{
		TrackerID: "BTN",
		Config:    cfg,
		DBPath:    dbPath,
		AutoLogin: true,
	})
	validation, ok := errors.AsType[*ValidationError](err)
	if !ok || !validation.Transient || validation.ConfirmedInvalid || validation.Submitted2FARejected {
		t.Fatalf("ban lost cookie-preserving classification: %v", err)
	}
	var status api.TrackerAuthStatus
	applyEnsureErrorToStatus(&status, err)
	if calls != 1 || status.Needs2FA || status.ChallengeID != "" || IsReadyStatus(status) {
		t.Fatalf("ban retried, requested 2FA, or reported ready: calls=%d status=%#v", calls, status)
	}
	if !strings.Contains(status.LastError, "IP indefinitely") || !strings.Contains(status.LastError, "support process") {
		t.Fatalf("shared status lost ban detail: %q", status.LastError)
	}
	for _, value := range []string{"192.0.2.1", "synthetic-private-detail", "<p>"} {
		if strings.Contains(status.LastError, value) {
			t.Fatal("status exposed response data")
		}
	}
	values, err := cookies.LoadTrackerCookieMap(t.Context(), dbPath, "BTN")
	if err != nil || len(values) != 1 || values["session"] != "synthetic-original" {
		t.Fatal("shared auth changed cookies after ban evidence")
	}
}

type btnIPBanRoundTripper func(*http.Request) (*http.Response, error)

func (fn btnIPBanRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
