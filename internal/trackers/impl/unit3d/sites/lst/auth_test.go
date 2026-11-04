// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAPIKeyExpiryGuidance(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		header []string
		want   string
	}{
		{name: "missing", want: "Legacy LST API keys expire on 2027-01-01"},
		{
			name:   "empty",
			header: []string{""},
			want:   "unreadable",
		},
		{
			name:   "malformed",
			header: []string{"not-a-date"},
			want:   "unreadable",
		},
		{
			name:   "no timezone",
			header: []string{"2026-12-04T12:00:00"},
			want:   "unreadable",
		},
		{
			name:   "exact boundary",
			header: []string{"2026-12-04T12:00:00Z"},
			want:   "2026-12-04T12:00:00Z",
		},
		{
			name:   "offset boundary",
			header: []string{"2026-12-04T07:00:00-05:00"},
			want:   "2026-12-04T12:00:00Z",
		},
		{
			name:   "inside",
			header: []string{"2026-12-04T11:59:59.999999999Z"},
			want:   "2026-12-04T11:59:59.999999999Z",
		},
		{name: "outside", header: []string{"2026-12-04T12:00:00.000000001Z"}},
		{name: "outside offset", header: []string{"2026-12-04T14:00:01+02:00"}},
		{
			name:   "expired",
			header: []string{"2026-11-01T00:00:00Z"},
			want:   "2026-11-01T00:00:00Z",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guidance := apiKeyGuidance()
			header := make(http.Header)
			if tc.header != nil {
				header[http.CanonicalHeaderKey(guidance.Header)] = tc.header
			}
			guidance.Observe("synthetic-key", header, http.StatusOK)
			got := guidance.Guidance("synthetic-key", now)
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("guidance mismatch: %q", got)
			}
			if got != "" && !strings.Contains(got, "update the LST API key configured in upbrr") {
				t.Fatal("guidance omitted configuration update advice")
			}
		})
	}
}

func TestAPIKeyGuidanceReplacementAndFailedAuthentication(t *testing.T) {
	t.Parallel()
	guidance := apiKeyGuidance()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	header := make(http.Header)
	header.Set(guidance.Header, "2026-11-20T12:00:00Z")
	guidance.Observe("synthetic-old-key", header, http.StatusOK)
	if !strings.Contains(guidance.Guidance("synthetic-old-key", now), "2026-11-20") {
		t.Fatal("reported expiry was lost")
	}
	if !strings.Contains(guidance.Guidance("synthetic-new-key", now), "did not report") {
		t.Fatal("replacement inherited another key's expiry")
	}
	header.Set(guidance.Header, "2027-06-01T00:00:00Z")
	guidance.Observe("synthetic-new-key", header, http.StatusOK)
	guidance.Observe("synthetic-old-key", nil, http.StatusUnauthorized)
	guidance.Observe("synthetic-new-key", nil, http.StatusForbidden)
	if guidance.Guidance("synthetic-new-key", now) != "" || !strings.Contains(guidance.Guidance("synthetic-old-key", now), "2026-11-20") {
		t.Fatal("failed authentication replaced a successful observation")
	}
	guidance.Observe("synthetic-new-key", nil, http.StatusOK)
	if !strings.Contains(guidance.Guidance("synthetic-new-key", now), "did not report") {
		t.Fatal("a headerless non-expiring key retained an old expiry")
	}
}
