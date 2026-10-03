// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDataCacheKeyTracksEffectiveRequest(t *testing.T) {
	t.Parallel()
	apiKey, rssKey := strings.Repeat("a", minDataTokenLength), strings.Repeat("r", minDataTokenLength)
	for _, test := range []struct {
		name     string
		id       string
		change   func(*dataLookup, *trackers.DataLookupRequest)
		wantSame bool
	}{
		{name: "search RSS", change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
			l.cfg.Trackers.Trackers["BHD"] = config.TrackerConfig{APIKey: apiKey, BhdRSSKey: strings.Repeat("s", minDataTokenLength)}
		}},
		{
			name:     "unused valid RSS with ID",
			id:       "42",
			wantSame: true,
			change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
				l.cfg.Trackers.Trackers["BHD"] = config.TrackerConfig{APIKey: apiKey, BhdRSSKey: strings.Repeat("s", minDataTokenLength)}
			},
		},
		{
			name: "RSS required with ID",
			id:   "42",
			change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
				l.cfg.Trackers.Trackers["BHD"] = config.TrackerConfig{APIKey: apiKey, BhdRSSKey: "short"}
			},
		},
		{
			name: "API key with ID",
			id:   "42",
			change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
				l.cfg.Trackers.Trackers["BHD"] = config.TrackerConfig{APIKey: strings.Repeat("b", minDataTokenLength), BhdRSSKey: rssKey}
			},
		},
		{
			name:     "credential alias and whitespace",
			wantSame: true,
			change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
				l.cfg.Trackers.Trackers = map[string]config.TrackerConfig{" bHd ": {APIKey: " " + apiKey + " ", BhdRSSKey: " " + rssKey + " "}}
			},
		},
		{
			name:     "ID whitespace",
			id:       "42",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.TrackerID = " 42 " },
		},
		{
			name:   "ID",
			id:     "42",
			change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.TrackerID = "43" },
		},
		{
			name:     "unused search with ID",
			id:       "42",
			wantSame: true,
			change: func(_ *dataLookup, req *trackers.DataLookupRequest) {
				req.SearchName, req.Meta.DiscType, req.Meta.SourcePath = "Other.Release.mkv", "BDMV", "Other.Release"
				req.Meta.FileList = nil
			},
		},
		{name: "filename", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = "Other.Release.mkv" }},
		{
			name:     "filename whitespace",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = " Example.Release.mkv " },
		},
		{
			name:     "unused file list contents",
			wantSame: true,
			change:   func(_ *dataLookup, req *trackers.DataLookupRequest) { req.Meta.FileList = []string{"other.mkv"} },
		},
		{name: "folder branch", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.Meta.FileList = nil }},
		{name: "disc branch", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.Meta.DiscType = "BDMV" }},
		{name: "empty filename skips lookup", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.SearchName = " " }},
		{name: "description demand", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.OnlyID = true }},
		{name: "image demand", change: func(_ *dataLookup, req *trackers.DataLookupRequest) { req.KeepImages = true }},
		{name: "endpoint", change: func(l *dataLookup, _ *trackers.DataLookupRequest) { l.baseURL += "/other" }},
		{
			name:     "endpoint trailing slash",
			wantSame: true,
			change:   func(l *dataLookup, _ *trackers.DataLookupRequest) { l.baseURL += "/" },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lookup := &dataLookup{
				cfg:     config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BHD": {APIKey: apiKey, BhdRSSKey: rssKey}}}},
				baseURL: "https://tracker.example/api/torrents",
			}
			req := trackers.DataLookupRequest{
				TrackerID:  test.id,
				SearchName: "Example.Release.mkv",
				Meta:       api.UploadSubject{SourcePath: "Example.Release", FileList: []string{"Example.Release.mkv"}},
			}
			before := lookup.CacheKey(req)
			test.change(lookup, &req)
			if same := reflect.DeepEqual(before, lookup.CacheKey(req)); same != test.wantSame {
				t.Fatalf("cache key unchanged = %t, want %t", same, test.wantSame)
			}
		})
	}
}

func TestDataCacheKeyUsesPortableFolderName(t *testing.T) {
	t.Parallel()
	token := strings.Repeat("a", minDataTokenLength)
	lookup := &dataLookup{cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BHD": {APIKey: token, BhdRSSKey: token}}}}}
	req := trackers.DataLookupRequest{Meta: api.UploadSubject{SourcePath: `C:\Incoming\Example.Release\`}}
	key := lookup.CacheKey(req)
	req.Meta.SourcePath = "  incoming/Example.Release/  "
	req.SearchName, req.Meta.DiscType = "ignored.mkv", "BDMV"
	req.Meta.FileList = []string{"one.mkv", "two.mkv"}
	if !reflect.DeepEqual(key, lookup.CacheKey(req)) {
		t.Fatal("equivalent folder queries have different cache keys")
	}
	req.Meta.SourcePath = "incoming/Other.Release"
	if reflect.DeepEqual(key, lookup.CacheKey(req)) {
		t.Fatal("changed folder query retained cache key")
	}
}
