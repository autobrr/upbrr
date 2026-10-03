// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

func TestDataCacheKeyTracksFilenameRequest(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		change   func(*dataLookup, *trackers.DataLookupRequest)
		wantSame bool
	}{
		{name: "filename with known ID", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "Other.Release.mkv" }},
		{name: "filename extension", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "Example.Release.mp4" }},
		{name: "filename case", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "example.release.mkv" }},
		{
			name:     "filename whitespace",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = " Example.Release.mkv " },
		},
		{
			name:     "unused tracker ID",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.TrackerID = "43" },
		},
		{
			name:     "unused projections",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.OnlyID, req.KeepImages = true, true },
		},
		{
			name:     "unused metadata",
			wantSame: true,
			change: func(_ *dataLookup, req *trackers.DataLookupRequest) {
				req.Meta.SourcePath, req.Meta.InfoHash, req.Meta.Tag = "Other.Release", "new-hash", "OTHER"
			},
		},
		{name: "disc skips lookup", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.Meta.DiscType = "BDMV" }},
		{name: "empty filename skips lookup", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = " " }},
		{name: "endpoint", change: func(l *dataLookup, _ *trackers.DataLookupRequest) { l.endpoint += "/other" }},
		{name: "API key", change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
			l.cfg.Trackers.Trackers["ANT"] = config.TrackerConfig{APIKey: "other-key"}
		}},
		{
			name:     "credential alias and whitespace",
			wantSame: true,
			change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
				l.cfg.Trackers.Trackers = map[string]config.TrackerConfig{" aNt ": {APIKey: " key "}}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lookup := &dataLookup{
				cfg:      config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"ANT": {APIKey: "key"}}}},
				endpoint: "https://tracker.example/api.php",
			}
			req := trackers.DataLookupRequest{TrackerID: "42", SearchName: "Example.Release.mkv"}
			before := lookup.CacheKey(req)
			test.change(lookup, &req)
			if same := reflect.DeepEqual(before, lookup.CacheKey(req)); same != test.wantSame {
				t.Fatalf("cache key unchanged = %t, want %t", same, test.wantSame)
			}
		})
	}
}
