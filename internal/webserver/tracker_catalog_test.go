// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestListTrackerCatalogComposesIdentitySchemaAndConfiguredState(t *testing.T) {
	t.Parallel()

	backend := &Backend{cfg: config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
		"RHD":     {APIKey: "configured"},
		"BTN":     {Username: "partial"},
		"AITHER":  {ImageHost: "imgbox"},
		"RETIRED": {Unknown: map[string]any{"keep_me": "retained"}},
	}}}}

	catalog, err := backend.ListTrackerCatalog()
	if err != nil {
		t.Fatalf("ListTrackerCatalog: %v", err)
	}
	if !slices.Equal(catalog.Unsupported, []string{"RETIRED"}) {
		t.Fatalf("unsupported = %#v", catalog.Unsupported)
	}

	rhd := catalogEntryByName(t, catalog.Entries, "RHD")
	if !rhd.Configured || rhd.Family != string(trackers.FamilyUnit3D) || rhd.BaseURL == "" {
		t.Fatalf("RHD catalog entry = %#v", rhd)
	}
	if !catalogEntryByName(t, catalog.Entries, "BTN").Configured {
		t.Fatal("partial BTN credentials should count as configured")
	}
	if catalogEntryByName(t, catalog.Entries, "AITHER").Configured {
		t.Fatal("optional AITHER image host should not count as configured")
	}

	schemas, err := config.OrderedTrackerSchemas()
	if err != nil {
		t.Fatalf("OrderedTrackerSchemas: %v", err)
	}
	rhdSchema := slices.IndexFunc(schemas, func(schema config.TrackerSchema) bool { return schema.Name == "RHD" })
	if rhdSchema < 0 {
		t.Fatal("RHD schema missing")
	}
	wantKeys := make([]string, len(schemas[rhdSchema].Fields))
	for index, field := range schemas[rhdSchema].Fields {
		wantKeys[index] = field.JSONKey
	}
	gotKeys := make([]string, len(rhd.Fields))
	for index, field := range rhd.Fields {
		gotKeys[index] = field.Key
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("RHD field order = %#v, want %#v", gotKeys, wantKeys)
	}
}

func catalogEntryByName(t *testing.T, entries []api.TrackerCatalogEntry, name string) api.TrackerCatalogEntry {
	t.Helper()
	index := slices.IndexFunc(entries, func(entry api.TrackerCatalogEntry) bool { return entry.Name == name })
	if index < 0 {
		t.Fatalf("catalog entry %s missing", name)
	}
	return entries[index]
}
