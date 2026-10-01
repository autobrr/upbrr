// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestTraceSearchRequestRedactsSelectedValuesWithoutMutation(t *testing.T) {
	t.Parallel()
	logger := &recordingDupeLogger{}
	params := map[string]any{
		"tmdbId":       123,
		"categories[]": []string{"2", "9", "29"},
		"search": url.Values{
			"imdb":    {"tt0000123"},
			"api_key": {"nested-credential"},
			"name":    {"Example Release token=embedded-credential"},
		},
	}
	before, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	TraceSearchRequest(logger, "EXAMPLE", http.MethodGet, "/api/torrents/filter", params)
	if len(logger.trace) != 1 || len(logger.debug) != 0 || len(logger.info) != 0 {
		t.Fatalf("request should only be logged at TRACE: %#v", logger)
	}
	log := logger.trace[0]
	for _, want := range []string{
		"dupechecking: request tracker=EXAMPLE method=GET endpoint=/api/torrents/filter",
		`"tmdbId":123`, `"categories[]":["2","9","29"]`, `"imdb":["tt0000123"]`, "[REDACTED]",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("request log missing %q", want)
		}
	}
	for _, secret := range []string{"nested-credential", "embedded-credential"} {
		if strings.Contains(log, secret) {
			t.Fatal("request log exposed a credential")
		}
	}
	after, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("logging mutated the request filters")
	}
}

func TestTraceSearchRequestHandlesUnavailableDiagnostics(t *testing.T) {
	t.Parallel()
	params := map[string]any{"unsupported": func() {}}
	TraceSearchRequest(nil, "EXAMPLE", http.MethodPost, "/api/search", params)
	logger := &recordingDupeLogger{}
	TraceSearchRequest(logger, "EXAMPLE", http.MethodPost, "/api/search", params)
	if len(logger.trace) != 1 || !strings.HasSuffix(logger.trace[0], "params=unavailable") {
		t.Fatalf("unavailable diagnostic = %#v", logger.trace)
	}
}
