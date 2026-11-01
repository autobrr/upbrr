// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestConnectionProbeValidatesSnapshotWithoutLogin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ body, want string }{
		{`<meta name="_token" content="synthetic-private-token">`, "success"},
		{`<input name="password"><input name="email"><meta name="_token" content="login-token">`, "credentials_rejected"},
		{`<title>Just a moment</title><meta name="_token" content="challenge-token">`, "challenge_required"},
		{`<html>service available</html>`, "unexpected_response"},
	} {
		t.Run(tc.want+tc.body[:6], func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				cookie, err := r.Cookie("session")
				if r.Method != http.MethodGet || r.URL.Path != "/torrents" || err != nil || cookie.Value != "synthetic-session" {
					t.Error("probe did not use a read-only request with the selected session")
				}
				w.Header().Set("Set-Cookie", "session=must-not-persist")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			definition := New("AZ")
			definition.site.BaseURL = server.URL
			snapshot := map[string]string{"session": "synthetic-session"}
			got := connectiontest.Run(t.Context(), func(ctx context.Context, client *http.Client) api.ConnectionTestResult {
				return definition.ConnectionProbe().Test(ctx, trackers.ConnectionProbeInput{Cookies: snapshot}, client)
			})
			if got.Code != tc.want || requests != 1 || snapshot["session"] != "synthetic-session" {
				t.Fatalf("result=%#v requests=%d", got, requests)
			}
			if strings.Contains(got.Message, "synthetic-") {
				t.Fatal("session or remote content leaked")
			}
		})
	}
}
