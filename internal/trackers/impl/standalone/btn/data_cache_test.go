// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

func TestDataCacheKeyResolvesLegacyToken(t *testing.T) {
	t.Parallel()
	token := strings.Repeat("a", 30)
	for _, test := range []struct {
		name     string
		change   func(*dataLookup, *trackers.DataLookupRequest)
		wantSame bool
	}{
		{name: "legacy token", change: func(l *dataLookup, _ *trackers.DataLookupRequest) { l.cfg.Metadata.BTNAPI = strings.Repeat("b", 30) }},
		{
			name:     "legacy token whitespace",
			wantSame: true,
			change:   func(l *dataLookup, _ *trackers.DataLookupRequest) { l.cfg.Metadata.BTNAPI = " " + token + " " },
		},
		{
			name:     "tracker alias same token",
			wantSame: true,
			change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
				l.cfg.Trackers.Trackers = map[string]config.TrackerConfig{"bTn": {APIKey: " " + token + " "}}
				l.cfg.Metadata.BTNAPI = "ignored-legacy-token"
			},
		},
		{name: "tracker token overrides legacy", change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
			l.cfg.Trackers.Trackers = map[string]config.TrackerConfig{"btn": {APIKey: strings.Repeat("b", 30)}}
		}},
		{name: "short token skips lookup", change: func(l *dataLookup, _ *trackers.DataLookupRequest) { l.cfg.Metadata.BTNAPI = "short" }},
		{name: "tracker ID", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.TrackerID = "43" }},
		{
			name:     "tracker ID whitespace",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.TrackerID = " 42 " },
		},
		{name: "empty ID skips lookup", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.TrackerID = " " }},
		{name: "endpoint", change: func(l *dataLookup, _ *trackers.DataLookupRequest) { l.endpoint += "other" }},
		{
			name:     "unused search and projections",
			wantSame: true,
			change: func(_ *dataLookup, req *trackers.DataLookupRequest) {
				req.SearchName, req.OnlyID, req.KeepImages = "Other.Release.mkv", true, true
				req.Meta.DiscType, req.Meta.SourcePath = "BDMV", "Other.Release"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lookup := &dataLookup{
				cfg:      config.Config{Metadata: config.MetadataConfig{BTNAPI: token}},
				endpoint: "https://tracker.example/",
			}
			req := trackers.DataLookupRequest{TrackerID: "42"}
			before := lookup.CacheKey(req)
			test.change(lookup, &req)
			if same := reflect.DeepEqual(before, lookup.CacheKey(req)); same != test.wantSame {
				t.Fatalf("cache key unchanged = %t, want %t", same, test.wantSame)
			}
		})
	}
}
