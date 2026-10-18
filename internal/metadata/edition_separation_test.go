// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strings"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAutomaticHybridIsNotCanonicalEdition(t *testing.T) {
	for _, edition := range []string{"", "Collector's"} {
		parts := editionFromMeta(preparationstate.State{Edition: edition, Release: api.ReleaseInfo{Other: []string{"Hybrid"}}}, mediaInfoDoc{})
		if parts.Edition != edition || !slices.Contains(parts.Features, api.ReleaseFeatureHybrid) {
			t.Errorf("canonical edition = %q, want %q", parts.Edition, edition)
		}
	}
}

func TestAutomaticHybridSurvivesGeneratedNaming(t *testing.T) {
	for _, edition := range []string{"", "Collector's"} {
		state := preparationstate.State{
			Edition: edition,
			Release: api.ReleaseInfo{
				Category: "MOVIE",
				Title:    "Example Movie",
				Year:     2026,
				Other:    []string{"Hybrid"},
			},
			Type:        "ENCODE",
			Source:      "BluRay",
			VideoEncode: "x264",
		}
		parts := editionFromMeta(state, mediaInfoDoc{})
		state.Edition, state.ReleaseFeatures = parts.Edition, parts.Features
		RebuildReleaseName(&state, api.NopLogger{})
		if strings.Count(state.ReleaseName, "Hybrid") != 1 || state.Edition != edition {
			t.Fatalf("name=%q edition=%q", state.ReleaseName, state.Edition)
		}
		state.ReleaseNameOverrides.NoEdition = new(true)
		applyReleaseNameValueOverrides(&state)
		RebuildReleaseName(&state, api.NopLogger{})
		if strings.Contains(state.ReleaseName, "Hybrid") {
			t.Fatalf("omitted marker restored: %q", state.ReleaseName)
		}
	}
}

func TestAutomaticEditionHybridSurvivesCleanup(t *testing.T) {
	parts := editionFromMeta(preparationstate.State{Edition: "Extended Hybrid"}, mediaInfoDoc{})
	if parts.Edition != "Extended" || !slices.Contains(parts.Features, api.ReleaseFeatureHybrid) {
		t.Fatalf("parts=%+v", parts)
	}
}

func TestProviderHybridIsSeparateFromMatchedCut(t *testing.T) {
	state := preparationstate.State{Identity: api.ExternalIdentity{Category: "MOVIE"}, ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{EditionDetails: map[string]api.IMDBEditionDetail{
		"6000": {Seconds: 6000, Minutes: 100},
		"6600": {
			Seconds:    6600,
			Minutes:    110,
			Attributes: []string{"Director's Cut", "Hybrid"},
		},
	}}}}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"6600.000"}]}}`)
	parts := editionFromMeta(state, doc)
	if parts.Cut != "Director's Cut" || parts.Edition != "" || !slices.Contains(parts.Features, api.ReleaseFeatureHybrid) {
		t.Fatalf("provider parts=%+v", parts)
	}
}

func TestHybridMultiPlaylistNameHasOneMarker(t *testing.T) {
	state := preparationstate.State{
		DiscType:   "BDMV",
		Type:       "DISC",
		Source:     "BluRay",
		VideoCodec: "AVC",
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Title:    "Example Movie",
			Year:     2026,
		},
		SelectedBDMVPlaylists: []api.PlaylistInfo{{Duration: 6000}, {Duration: 6600}, {Duration: 7200}},
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{EditionDetails: map[string]api.IMDBEditionDetail{
			"6000": {Seconds: 6000, Minutes: 100},
			"6600": {
				Seconds:    6600,
				Minutes:    110,
				Attributes: []string{"Director's Cut", "Hybrid"},
			},
			"7200": {
				Seconds:    7200,
				Minutes:    120,
				Attributes: []string{"Extended"},
			},
		}}},
	}
	parts := editionFromMeta(state, mediaInfoDoc{})
	state.EditionSet, state.Cut, state.Edition, state.ReleaseFeatures = parts.Set, parts.Cut, parts.Edition, parts.Features
	RebuildReleaseName(&state, api.NopLogger{})
	if strings.Count(state.ReleaseName, "Hybrid") != 1 || strings.Contains(parts.Edition, "Hybrid") {
		t.Fatalf("parts=%+v name=%q", parts, state.ReleaseName)
	}
	if !strings.Contains(state.ReleaseName, "3in1") || !strings.Contains(state.ReleaseName, "Director's Cut") || !strings.Contains(state.ReleaseName, "Extended") {
		t.Fatalf("lost genuine set identity: %q", state.ReleaseName)
	}
}

func TestKnownProviderFeaturesKeepDisplayWithoutEditionIdentity(t *testing.T) {
	for _, test := range []struct {
		label   string
		feature api.ReleaseFeature
	}{
		{"2-Disc Set", api.ReleaseFeatureTwoDiscSet},
		{"4K Restoration", api.ReleaseFeature4KRestoration},
		{"4K Remaster", api.ReleaseFeature4KRemaster},
		{"Extras", api.ReleaseFeatureExtras},
		{"2D/3D Edition", api.ReleaseFeature2D3DEdition},
		{"3D Anaglyph", api.ReleaseFeature3DAnaglyph},
		{"3D Full SBS", api.ReleaseFeature3DFullSBS},
		{"3D Half OU", api.ReleaseFeature3DHalfOU},
		{"3D Half SBS", api.ReleaseFeature3DHalfSBS},
		{"remastered version", api.ReleaseFeatureRemaster},
		{"Restored", api.ReleaseFeatureRestoration},
	} {
		t.Run(test.label, func(t *testing.T) {
			parts := imdbEditionParts([]string{"Collector's", test.label, "Unknown Label"})
			display := "Collector's " + cleanIMDbEditionText(smartEditionTitle(test.label)) + " Unknown Label"
			if parts.Edition != "Collector's Unknown Label" || api.EditionDisplayText(parts.EditionDisplay) != display || !slices.Contains(parts.Features, test.feature) {
				t.Fatalf("parts=%+v want display=%q feature=%s", parts, display, test.feature)
			}
		})
	}
}

func TestProviderOnlyFeatureAndFilenameFeatureAreBothRetained(t *testing.T) {
	state := preparationstate.State{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		Release:  api.ReleaseInfo{Other: []string{"Extras"}},
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{EditionDetails: map[string]api.IMDBEditionDetail{
			"6000": {Seconds: 6000, Minutes: 100},
			"6600": {
				Seconds:    6600,
				Minutes:    110,
				Attributes: []string{"4K Restoration"},
			},
		}}},
	}
	parts := editionFromMeta(state, mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"6600.000"}]}}`))
	if parts.Edition != "" || api.EditionDisplayText(parts.EditionDisplay) != "4K Restoration" || !slices.Contains(parts.Features, api.ReleaseFeature4KRestoration) || !slices.Contains(parts.Features, api.ReleaseFeatureExtras) {
		t.Fatalf("parts=%+v", parts)
	}
}

func TestParserGenericMasteringDoesNotInvent4K(t *testing.T) {
	for _, test := range []struct {
		label, display string
		feature        api.ReleaseFeature
	}{
		{"Restored.Edition", "Restored", api.ReleaseFeatureRestoration},
		{"Remastered.Edition", "", api.ReleaseFeatureRemaster},
	} {
		parts := editionFromMeta(preparationstate.State{Release: api.ReleaseInfo{Edition: []string{test.label}}}, mediaInfoDoc{})
		if parts.Edition != "" || api.EditionDisplayText(parts.EditionDisplay) != test.display || !slices.Equal(parts.Features, []api.ReleaseFeature{test.feature}) {
			t.Fatalf("parts=%+v", parts)
		}
	}
}

func TestManualEditionRetainsIndependentDisplayProjection(t *testing.T) {
	for _, overrides := range []api.ReleaseNameOverrides{{Edition: new("Custom wording")}, {Edition: new("Hybrid Uncut")}, {Edition: new("")}, {NoEdition: new(true)}} {
		state := preparationstate.State{
			Edition:              "Collector's",
			EditionDisplay:       []api.EditionDisplayPart{{Text: "Collector's"}, {Text: "Restored", Feature: api.ReleaseFeatureRestoration}},
			ReleaseFeatures:      []api.ReleaseFeature{api.ReleaseFeatureRestoration, api.ReleaseFeatureHybrid},
			ReleaseNameOverrides: overrides,
		}
		applyReleaseNameValueOverrides(&state)
		if overrides.NoEdition != nil {
			if len(state.EditionDisplay) != 0 || len(state.ReleaseFeatures) != 0 {
				t.Fatal("legacy omission retained annotations")
			}
		} else if !slices.Equal(state.ReleaseFeatures, []api.ReleaseFeature{api.ReleaseFeatureRestoration, api.ReleaseFeatureHybrid}) || !strings.Contains(api.EditionDisplayText(state.EditionDisplay), "Restored") {
			t.Fatalf("edition edit erased independent annotations: edition=%q features=%v display=%v", state.Edition, state.ReleaseFeatures, state.EditionDisplay)
		}
		if overrides.Edition != nil && state.Edition != *overrides.Edition {
			t.Fatalf("manual wording changed: %q, want %q", state.Edition, *overrides.Edition)
		}
	}
}

func TestSeparatedDisplayRetainsExistingRifftraxEdition(t *testing.T) {
	state := preparationstate.State{
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Title:    "Example Movie",
			Year:     2026,
			Edition:  []string{"Restored.Edition"},
			Other:    []string{"Rifftrax"},
		},
		Type:        "ENCODE",
		Source:      "BluRay",
		VideoEncode: "x264",
	}
	parts := editionFromMeta(state, mediaInfoDoc{})
	state.Edition, state.EditionDisplay, state.ReleaseFeatures = parts.Edition, parts.EditionDisplay, parts.Features
	RebuildReleaseName(&state, api.NopLogger{})
	if parts.Edition != "Rifftrax" || api.EditionDisplayText(parts.EditionDisplay) != "Restored / Rifftrax" || !strings.Contains(state.ReleaseName, "Restored / Rifftrax") {
		t.Fatalf("parts=%+v name=%q", parts, state.ReleaseName)
	}
}

func TestFilenameAndMultiPlaylistFeaturesDoNotOverwriteEachOther(t *testing.T) {
	state := preparationstate.State{
		DiscType: "BDMV",
		Release: api.ReleaseInfo{
			Cut:   []string{"Uncut"},
			Other: []string{"2-Disc Set", "4K Remaster", "Extras", "Hybrid"},
		},
		SelectedBDMVPlaylists: []api.PlaylistInfo{{Duration: 6000}, {Duration: 6600}},
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{EditionDetails: map[string]api.IMDBEditionDetail{
			"6000": {
				Seconds:    6000,
				Minutes:    100,
				Attributes: []string{"Extended"},
			},
			"6600": {
				Seconds:    6600,
				Minutes:    110,
				Attributes: []string{"Director's Cut", "4K Restoration"},
			},
		}}},
	}
	parts := editionFromMeta(state, mediaInfoDoc{})
	if parts.Set != "2in1" || parts.Cut != "Uncut" {
		t.Fatalf("lost filename/set evidence: %+v", parts)
	}
	for _, feature := range []api.ReleaseFeature{api.ReleaseFeatureTwoDiscSet, api.ReleaseFeature4KRemaster, api.ReleaseFeatureExtras, api.ReleaseFeatureHybrid, api.ReleaseFeature4KRestoration} {
		if !slices.Contains(parts.Features, feature) {
			t.Errorf("missing feature %s from %v", feature, parts.Features)
		}
	}
}

func TestProviderDisplayFragmentsDoNotInventCompoundMasteringFacts(t *testing.T) {
	parts := imdbEditionParts([]string{"4K", "restoration"})
	if parts.Edition != "4K" || !slices.Equal(parts.Features, []api.ReleaseFeature{api.ReleaseFeatureRestoration}) || api.EditionDisplayText(parts.EditionDisplay) != "4K Restoration" {
		t.Fatalf("provider parts=%+v", parts)
	}
}
