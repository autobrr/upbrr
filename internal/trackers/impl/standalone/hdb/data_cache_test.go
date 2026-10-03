// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

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
		{name: "username", change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
			l.cfg.Trackers.Trackers["HDB"] = config.TrackerConfig{Username: "other", Passkey: "pass"}
		}},
		{name: "passkey", change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
			l.cfg.Trackers.Trackers["HDB"] = config.TrackerConfig{Username: "user", Passkey: "other"}
		}},
		{
			name:     "credential alias and whitespace",
			wantSame: true,
			change: func(l *dataLookup, _ *trackers.DataLookupRequest) {
				l.cfg.Trackers.Trackers = map[string]config.TrackerConfig{" hDb ": {Username: " user ", Passkey: " pass "}}
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
			change: func(_ *dataLookup, req *trackers.DataLookupRequest) {
				req.SearchName, req.Meta.SourcePath, req.Meta.DiscType = "Other.Release.mkv", "Other.Release", "BDMV"
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
		{name: "endpoint", change: func(l *dataLookup, _ *trackers.DataLookupRequest) { l.endpoint += "/other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lookup := &dataLookup{
				cfg:      config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"HDB": {Username: "user", Passkey: "pass"}}}},
				endpoint: "https://tracker.example/api/torrents",
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
	lookup := &dataLookup{cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"HDB": {Username: "user", Passkey: "pass"}}}}}
	req := trackers.DataLookupRequest{Meta: api.UploadSubject{SourcePath: `C:\Incoming\Example.Release\`}}
	key := lookup.CacheKey(req)
	req.Meta.SourcePath = "  incoming/Example.Release/  "
	req.SearchName, req.Meta.DiscType = "ignored.mkv", "DVD"
	req.Meta.FileList = []string{"one.mkv", "two.mkv"}
	if !reflect.DeepEqual(key, lookup.CacheKey(req)) {
		t.Fatal("equivalent folder queries have different cache keys")
	}
	req.Meta.SourcePath = "incoming/Other.Release"
	if reflect.DeepEqual(key, lookup.CacheKey(req)) {
		t.Fatal("changed folder query retained cache key")
	}
}
