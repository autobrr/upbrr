// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDataCacheKeyTracksEffectiveRequest(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		id       string
		change   func(*dataLookup, *trackers.DataLookupRequest)
		wantSame bool
	}{
		{name: "API user", change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
			l.cfg.Trackers.Trackers["PTP"] = config.TrackerConfig{PTPAPIUser: "other", PTPAPIKey: "key"}
		}},
		{name: "API key", change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
			l.cfg.Trackers.Trackers["PTP"] = config.TrackerConfig{PTPAPIUser: "user", PTPAPIKey: "other"}
		}},
		{
			name:     "credential alias and whitespace",
			wantSame: true,
			change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
				l.cfg.Trackers.Trackers = map[string]config.TrackerConfig{" pTp ": {
					PTPAPIUser: " user ",
					PTPAPIKey:  " key ",
					APIKey:     "unused",
				}}
			},
		},
		{
			name:   "ID",
			id:     "42",
			change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.TrackerID = "43" },
		},
		{
			name:     "ID whitespace",
			id:       "42",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.TrackerID = " 42 " },
		},
		{
			name:     "unused search with ID",
			id:       "42",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "Other.Release.mkv" },
		},
		{name: "search name", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "Other.Release.mkv" }},
		{name: "search case", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "example.release.mkv" }},
		{
			name:     "search whitespace and recognized extension",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = " Example.Release.MP4 " },
		},
		{
			name:     "search without extension",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "Example.Release" },
		},
		{name: "unrecognized extension", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "Example.Release.txt" }},
		{name: "empty search skips lookup", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = " " }},
		{name: "description demand", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.OnlyID = true }},
		{name: "image demand", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.KeepImages = true }},
		{name: "disc projection", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.Meta.DiscType = "DVD" }},
		{
			name:     "disc normalization",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.Meta.DiscType = " bdmv " },
		},
		{
			name:     "unused source metadata",
			wantSame: true,
			change: func(_ *dataLookup, req *trackers.DataLookupRequest) {
				req.Meta.SourcePath, req.Meta.Tag, req.Meta.InfoHash = "Other.Release", "OTHER", "new-hash"
				req.Meta.FileList = []string{"one.mkv", "two.mkv"}
			},
		},
		{name: "endpoint", change: func(l *dataLookup, _ *trackers.DataLookupRequest) { l.endpoint += "/other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lookup := &dataLookup{
				cfg:      config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"PTP": {PTPAPIUser: "user", PTPAPIKey: "key"}}}},
				endpoint: "https://tracker.example/torrents.php",
			}
			req := trackers.DataLookupRequest{
				TrackerID:  test.id,
				SearchName: "Example.Release.mkv",
				Meta:       api.UploadSubject{DiscType: "BDMV"},
			}
			before := lookup.CacheKey(req)
			test.change(lookup, &req)
			if same := reflect.DeepEqual(before, lookup.CacheKey(req)); same != test.wantSame {
				t.Fatalf("cache key unchanged = %t, want %t", same, test.wantSame)
			}
		})
	}
}

func TestDataCacheKeyIgnoresUnusedDiscProjection(t *testing.T) {
	t.Parallel()
	lookup := &dataLookup{cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"PTP": {PTPAPIUser: "user", PTPAPIKey: "key"}}}}}
	req := trackers.DataLookupRequest{
		TrackerID: "42",
		OnlyID:    true,
		Meta:      api.UploadSubject{DiscType: "BDMV"},
	}
	key := lookup.CacheKey(req)
	req.Meta.DiscType = "DVD"
	if !reflect.DeepEqual(key, lookup.CacheKey(req)) {
		t.Fatal("ID-only lookup includes unused disc projection")
	}
	req.KeepImages = true
	if reflect.DeepEqual(key, lookup.CacheKey(req)) {
		t.Fatal("image demand retained ID-only cache key")
	}
	key = lookup.CacheKey(req)
	req.Meta.DiscType = "BDMV"
	if !reflect.DeepEqual(key, lookup.CacheKey(req)) {
		t.Fatal("image-only lookup includes unused disc projection")
	}
	req.Meta.DiscType = "unknown"
	key = lookup.CacheKey(req)
	req.Meta.DiscType = ""
	if !reflect.DeepEqual(key, lookup.CacheKey(req)) {
		t.Fatal("equivalent default disc projections have different cache keys")
	}
}
