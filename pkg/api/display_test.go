// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPreparedReleaseDisplayFixtureAndFrontendContract(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "prepared_release_display.json"))
	if err != nil {
		t.Fatal(err)
	}
	var display PreparedReleaseDisplay
	if err := json.Unmarshal(data, &display); err != nil {
		t.Fatal(err)
	}
	wantOrder := []IdentityProvider{
		IdentityProviderTMDB,
		IdentityProviderIMDB,
		IdentityProviderTVDB,
		IdentityProviderTVmaze,
		IdentityProviderMAL,
	}
	if len(display.Providers) != len(wantOrder) {
		t.Fatalf("provider count = %d, want %d", len(display.Providers), len(wantOrder))
	}
	for index, provider := range display.Providers {
		if provider.Provider != wantOrder[index] {
			t.Fatalf("provider[%d] = %q, want %q", index, provider.Provider, wantOrder[index])
		}
		value := reflect.ValueOf(provider.Details)
		nonNil := 0
		for _, field := range value.Fields() {
			if !field.IsNil() {
				nonNil++
			}
		}
		if nonNil != 1 {
			t.Fatalf("provider %q detail variants = %d, want 1", provider.Provider, nonNil)
		}
	}

	frontendTypes := readRepoFile(t, "webui", "src", "types.ts")
	for _, required := range []string{
		"export type PreparedReleaseDisplay = {",
		"export type ProviderDisplaySummary = {",
		`Provider: "tmdb"; Details: { TMDB: TMDBMetadata }`,
		`Provider: "imdb"; Details: { IMDB: IMDBMetadata }`,
		`Provider: "tvdb"; Details: { TVDB: TVDBMetadata }`,
		`Provider: "tvmaze"; Details: { TVmaze: TVmazeMetadata }`,
		`Provider: "mal"; Details: { AniList: AniListMetadata }`,
	} {
		if !strings.Contains(frontendTypes, required) {
			t.Fatalf("frontend display contract missing %q", required)
		}
	}
}
