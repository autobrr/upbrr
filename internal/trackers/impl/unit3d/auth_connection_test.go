// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestConnectionProbeUsesDraftBearerAndRejectsUnrelatedResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, response, want string }{
		{"authorized empty search", `{"data":[],"meta":{"current_page":1,"total":0}}`, "success"},
		{"login page", `<html><input name="password"></html>`, "unexpected_response"},
		{"api failure", `{"message":"synthetic-private-error"}`, "unexpected_response"},
		{"missing pagination", `{"data":[]}`, "unexpected_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/base/api/torrents/filter" || r.URL.Query().Get("perPage") != "1" ||
					r.URL.Query().Get("name") != "upbrr.connection.test.synthetic" || r.URL.Query().Has("api_token") || r.Header.Get("Authorization") != "Bearer synthetic-draft-key" {
					t.Error("probe did not preserve the read-only production API contract")
				}
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			definition := NewWithProfile(Profile{Name: "EXAMPLE", BaseURL: server.URL + "/base"})
			got := connectiontest.Run(t.Context(), func(ctx context.Context, client *http.Client) api.ConnectionTestResult {
				return definition.ConnectionProbe().Test(ctx, trackers.ConnectionProbeInput{Config: config.TrackerConfig{APIKey: " synthetic-draft-key "}}, client)
			})
			if got.Code != tc.want || requests != 1 {
				t.Fatalf("result=%#v requests=%d", got, requests)
			}
			if strings.Contains(got.Message, "synthetic-") {
				t.Fatal("remote content or key leaked")
			}
		})
	}
}
