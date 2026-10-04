// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicateSearchUsesOperationLogLevel(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"item":[],"total":0}`)),
			Header:     make(http.Header),
		}, nil
	})}
	root, err := logging.New(config.LoggingConfig{Level: "info"}, "")
	if err != nil {
		t.Fatal(err)
	}
	root.SetConsoleOutput(io.Discard, io.Discard)
	t.Cleanup(func() { _ = root.Close() })
	scoped, err := logging.NewOperationLogger(root, "trace")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ANT": {APIKey: "synthetic-key"}}}}
	searcher := dupe.NewAdapter(New(), "ANT", cfg, client, root)
	subject := api.DuplicateSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}, Release: api.ReleaseInfo{Resolution: "1080p"}}
	if result := searcher.Search(logging.WithOperationLogger(t.Context(), scoped), subject); result.Cause() != nil {
		t.Fatal("synthetic duplicate search failed")
	}
	entries := root.Recent(100)
	if len(entries) == 0 {
		t.Fatal("duplicate request trace ignored operation verbosity")
	}
	for _, entry := range entries {
		if entry.Level != "trace" {
			t.Fatalf("unexpected duplicate log level %s", entry.Level)
		}
	}
	searcher.Search(t.Context(), subject)
	if len(root.Recent(100)) != len(entries) {
		t.Fatal("duplicate search changed the shared adapter logger")
	}
}
