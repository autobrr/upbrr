// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLookupPreservesPartialResultOnError(t *testing.T) {
	t.Parallel()
	partial := Result{
		TrackerID:  "42",
		IMDBID:     1234567,
		TorrentURL: "https://tracker.example/torrents.php?id=700&torrentid=42",
	}
	for _, cause := range []error{errors.New("description unavailable"), context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			client := &Client{lookups: map[string]trackers.DataLookup{"TEST": partialLookup{result: partial, err: cause}}}
			result, err := client.Lookup(t.Context(), " test ", "42", api.UploadSubject{}, "", false, false)
			if !reflect.DeepEqual(result, partial) || !errors.Is(err, cause) || !strings.Contains(err.Error(), "TEST lookup") {
				t.Fatalf("partial result or cause lost: result=%+v err=%v", result, err)
			}
		})
	}
}

type partialLookup struct {
	result trackers.DataLookupResult
	err    error
}

func (l partialLookup) CacheKey(req trackers.DataLookupRequest) any {
	return []any{req.TrackerID, req.SearchName, req.OnlyID, req.KeepImages}
}

func (l partialLookup) Lookup(context.Context, trackers.DataLookupRequest) (trackers.DataLookupResult, error) {
	return l.result, l.err
}

func TestLookupUnit3DResponseOutcomes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{
			name:      "server error",
			status:    http.StatusInternalServerError,
			body:      `private response`,
			wantError: true,
		},
		{
			name:   "empty",
			status: http.StatusOK,
			body:   `{"data":[]}`,
		},
		{
			name:   "not found",
			status: http.StatusNotFound,
			body:   `private response`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := NewClientWithRegistry(config.Config{}, api.NopLogger{}, server.Client(), testUnit3DRegistry(t, "TEST", server.URL))
			result, err := client.Lookup(t.Context(), "TEST", "42", api.UploadSubject{}, "", true, false)
			if (err != nil) != tt.wantError || result.HasData() {
				t.Fatalf("response outcome: result=%+v err=%v, wantError=%t", result, err, tt.wantError)
			}
			if err != nil && strings.Contains(err.Error(), "private response") {
				t.Fatalf("response content leaked: %v", err)
			}
		})
	}
}

func TestLookupUnit3DPreservesCancellation(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := NewClientWithRegistry(config.Config{}, api.NopLogger{}, server.Client(), testUnit3DRegistry(t, "TEST", server.URL))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := client.Lookup(ctx, "TEST", "42", api.UploadSubject{}, "", true, false)
	if !errors.Is(err, context.Canceled) || result.HasData() {
		t.Fatalf("canceled lookup: result=%+v err=%v", result, err)
	}
}
