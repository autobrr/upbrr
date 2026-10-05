// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCanonicalEditionPartsPreserveParserCategories(t *testing.T) {
	t.Parallel()
	release := ParseReleaseInfo("Example.Movie.2026.Directors.Cut.Collectors.Edition.IMAX.Open.Matte.1080p.BluRay.x264-GRP.mkv")
	if len(release.Cut) != 1 || len(release.Edition) != 2 || release.Collection != "IMAX" {
		t.Fatalf("parser categories = %#v", release)
	}
	parts := editionFromMeta(preparationstate.State{Release: release}, mediaInfoDoc{})
	if parts.Cut != "Director's Cut" || parts.Presentation != "IMAX Open Matte" || !strings.Contains(parts.Edition, "Collectors") || strings.Contains(parts.Edition, "Open") {
		t.Fatalf("canonical parts = %#v", parts)
	}
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:     "MOVIE",
		Type:         "ENCODE",
		Title:        "Cut IMAX Open Matte",
		Year:         2026,
		Cut:          parts.Cut,
		Edition:      parts.Edition,
		Presentation: parts.Presentation,
		Resolution:   "1080p",
		Source:       "BluRay",
		VideoEncode:  "x264",
		Tag:          "-GRP",
	}, api.NopLogger{})
	for role, want := range map[api.ReleaseNameRole]string{
		api.NameRoleTitle:        "Cut IMAX Open Matte",
		api.NameRoleCut:          parts.Cut,
		api.NameRoleEdition:      parts.Edition,
		api.NameRolePresentation: parts.Presentation,
	} {
		component, ok := result.GeneratedName.Component(role)
		if !ok || !component.Present || component.Value != want {
			t.Fatalf("%s = %#v, want %q", role, component, want)
		}
	}
}

func TestCanonicalProviderAttributesKeepCategories(t *testing.T) {
	t.Parallel()
	parts := imdbEditionParts([]string{"Director's Cut", "IMAX", "Open Matte", "remastered version", "Unknown Label"})
	if parts.Cut != "Director's Cut" || parts.Presentation != "IMAX Open Matte" || parts.Edition != "Remastered Version Unknown Label" {
		t.Fatalf("provider parts = %#v", parts)
	}
	if got := imdbEditionParts([]string{"A Director Remastered This"}); got.Cut != "" || got.Edition != "A Director Remastered This" {
		t.Fatalf("unknown provider attribute classified as cut: %#v", got)
	}
}

func TestCanonicalEditionOverridesProtectAllRoles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides api.ReleaseNameOverrides
		want      string
	}{
		{"manual value", api.ReleaseNameOverrides{Edition: new("Custom Cut IMAX Edition")}, "Custom Cut IMAX Edition"},
		{"manual clear", api.ReleaseNameOverrides{Edition: new("")}, ""},
		{"omission", api.ReleaseNameOverrides{NoEdition: new(true)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := preparationstate.State{
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Release: api.ReleaseInfo{
					Category: "MOVIE",
					Title:    "Example Movie",
					Year:     2026,
					Cut:      []string{"Extended.Cut"},
				},
				Type:                 "ENCODE",
				Source:               "BluRay",
				VideoEncode:          "x264",
				Cut:                  "Extended Cut",
				Edition:              "Collector's",
				Presentation:         "Open Matte",
				ReleaseNameOverrides: tc.overrides,
			}
			captureAvailableGeneratedName(&meta, api.NopLogger{})
			applyReleaseNameValueOverrides(&meta)
			RebuildReleaseName(&meta, api.NopLogger{})
			if meta.Cut != "" || meta.Presentation != "" || meta.Edition != tc.want {
				t.Fatalf("final parts = %q / %q / %q", meta.Cut, meta.Edition, meta.Presentation)
			}
			for _, role := range []api.ReleaseNameRole{api.NameRoleCut, api.NameRoleEdition, api.NameRolePresentation} {
				component, ok := meta.GeneratedName.Component(role)
				if !ok || !component.Manual {
					t.Fatalf("manual %s = %#v", role, component)
				}
				if role != api.NameRoleEdition && component.Present {
					t.Fatalf("automatic component restored: %#v", component)
				}
				if tc.name == "omission" && component.AvailableValue == "" {
					t.Fatalf("omitted availability lost: %#v", component)
				}
			}
		})
	}
}

func TestCanonicalCutDisplayUsesParserTitles(t *testing.T) {
	t.Parallel()
	if got := canonicalCutLabel([]string{"Directors.Cut", "Extended.Cut", "Original.Version"}); got != "Director's Cut Extended Cut Original Version" {
		t.Fatalf("canonical cut display = %q", got)
	}
}

func TestCanonicalFilenameEditionPrecedesRuntimeEvidence(t *testing.T) {
	t.Parallel()
	meta := preparationstate.State{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		Release:  ParseReleaseInfo("Example.Movie.2026.Directors.Cut.Open.Matte.1080p.BluRay.x264-GRP.mkv"),
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{EditionDetails: map[string]api.IMDBEditionDetail{
			"100": {Seconds: 6000, Minutes: 100},
			"125": {
				Seconds:    7500,
				Minutes:    125,
				Attributes: []string{"Extended"},
			},
		}}},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7500.000"}]}}`)
	parts := editionFromMeta(meta, doc)
	if parts.Cut != "Director's Cut" || parts.Presentation != "Open Matte" || parts.Edition != "" {
		t.Fatalf("filename evidence replaced by provider: %#v", parts)
	}
	meta.Release.Cut, meta.Release.Edition = nil, nil
	if fallback := editionFromMeta(meta, doc); fallback.Cut != "Extended" {
		t.Fatalf("missing filename did not use provider: %#v", fallback)
	}
}

func TestCanonicalMultiEditionSetNames(t *testing.T) {
	for _, test := range []struct {
		name       string
		attributes [][]string
		wantSet    string
	}{
		{"two mixed categories", [][]string{nil, {"IMAX"}}, "2in1"},
		{"two presentations same cut", [][]string{{"Director's Cut", "IMAX"}, {"Director's Cut", "Open Matte"}}, "2in1"},
		{"three variants retain wording", [][]string{nil, {"Director's Cut"}, {"Extended"}}, "3in1 Theatrical / Director's Cut / Extended"},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := preparationstate.State{DiscType: "BDMV", ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{EditionDetails: map[string]api.IMDBEditionDetail{}}}}
			for index, attributes := range test.attributes {
				seconds := 6000 + index*600
				meta.SelectedBDMVPlaylists = append(meta.SelectedBDMVPlaylists, api.PlaylistInfo{File: strconv.Itoa(index) + ".MPLS", Duration: float64(seconds)})
				meta.ProviderMetadata.IMDB.EditionDetails[strconv.Itoa(seconds)] = api.IMDBEditionDetail{
					Seconds:    seconds,
					Minutes:    seconds / 60,
					Attributes: attributes,
				}
			}
			parts := editionFromMeta(meta, mediaInfoDoc{})
			if parts.Set != test.wantSet {
				t.Fatalf("set = %#v, want %q", parts, test.wantSet)
			}
			req := api.ReleaseNameRequest{
				Category:     "MOVIE",
				Type:         "DISC",
				DiscType:     "BDMV",
				Title:        "Example Movie",
				Year:         2026,
				EditionSet:   parts.Set,
				Cut:          parts.Cut,
				Edition:      parts.Edition,
				Presentation: parts.Presentation,
				Resolution:   "1080p",
				Source:       "BluRay",
				VideoCodec:   "AVC",
				Tag:          "-GRP",
			}
			result := BuildReleaseName(req, api.NopLogger{})
			set, ok := result.GeneratedName.Component(api.NameRoleEditionSet)
			if !ok || !set.Present || set.Value != test.wantSet {
				t.Fatalf("set component = %#v", set)
			}
			for _, role := range []api.ReleaseNameRole{api.NameRoleCut, api.NameRoleEdition, api.NameRolePresentation} {
				part, ok := result.GeneratedName.Component(role)
				if !ok || part.Present || part.AvailableValue != "" {
					t.Fatalf("individual variant exposed for naming: %#v", part)
				}
			}
			// Filename facts remain authoritative even when a selected set hides their labels.
			meta.Release.Cut = []string{"Unrated.Cut"}
			withFilename := editionFromMeta(meta, mediaInfoDoc{})
			if withFilename.Set != test.wantSet || withFilename.Cut != "Unrated Cut" {
				t.Fatalf("filename/set authority = %#v", withFilename)
			}
		})
	}
}

func TestCanonicalEditionSetHonorsManualEditionControls(t *testing.T) {
	for _, test := range []struct {
		name        string
		overrides   api.ReleaseNameOverrides
		wantEdition string
	}{
		{"value", api.ReleaseNameOverrides{Edition: new("Manual Edition")}, "Manual Edition"},
		{"clear", api.ReleaseNameOverrides{Edition: new("")}, ""},
		{"omit", api.ReleaseNameOverrides{NoEdition: new(true)}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := preparationstate.State{
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Release: api.ReleaseInfo{
					Category: "MOVIE",
					Title:    "Example Movie",
					Year:     2026,
				},
				Type:                 "ENCODE",
				Source:               "BluRay",
				VideoEncode:          "x264",
				EditionSet:           "2in1",
				Cut:                  "Theatrical / Extended",
				Presentation:         "IMAX",
				ReleaseNameOverrides: test.overrides,
			}
			captureAvailableGeneratedName(&meta, api.NopLogger{})
			applyReleaseNameValueOverrides(&meta)
			RebuildReleaseName(&meta, api.NopLogger{})
			if meta.EditionSet != "" || meta.Edition != test.wantEdition || meta.Cut != "" || meta.Presentation != "" {
				t.Fatalf("manual set facts = %#v", meta)
			}
			set, ok := meta.GeneratedName.Component(api.NameRoleEditionSet)
			if !ok || set.Present || !set.Manual {
				t.Fatalf("manual set component = %#v", set)
			}
			if test.name == "omit" && set.AvailableValue != "2in1" {
				t.Fatalf("omitted set availability = %#v", set)
			}
		})
	}
}

func TestCanonicalSolitaryTheatricalIsIgnored(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"Theatrical", "Theatrical.Cut", "Theatrical Edition", "theatrical version", "Theatrical-Cut", "Theatrical_Edition", "Theatrical Cut / Theatrical Edition"} {
		t.Run(label, func(t *testing.T) {
			meta := preparationstate.State{Release: api.ReleaseInfo{Cut: []string{label}}}
			parts := editionFromMeta(meta, mediaInfoDoc{})
			if parts.Cut != "" || parts.Edition != "" || parts.Presentation != "" {
				t.Fatalf("solitary theatrical retained: %#v", parts)
			}
			meta.ReleaseNameOverrides.Edition = new(label)
			applyReleaseNameValueOverrides(&meta)
			if meta.Edition != "" || meta.Cut != "" {
				t.Fatalf("manual solitary theatrical retained: %#v", meta)
			}
		})
	}
	for _, test := range []preparationstate.State{
		{Release: api.ReleaseInfo{Cut: []string{"Theatrical.Cut", "Extended.Cut"}}},
		{Release: api.ReleaseInfo{Cut: []string{"Theatrical.Cut"}, Edition: []string{"Collectors.Edition"}}},
		{ReleaseNameOverrides: api.ReleaseNameOverrides{Edition: new("Theatrical / Uncut")}},
	} {
		parts := editionFromMeta(test, mediaInfoDoc{})
		test.Cut, test.Edition = parts.Cut, parts.Edition
		applyReleaseNameValueOverrides(&test)
		if !strings.Contains(strings.ToLower(test.Cut+test.Edition), "theatrical") {
			t.Fatalf("compound theatrical lost: %#v", test)
		}
	}
}

func TestCanonicalReleaseFeaturesPreserveExplicitEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		token, edition, set string
		feature             api.ReleaseFeature
	}{
		{"Rifftrax", "Rifftrax", "", ""},
		{"4K.Restoration", "", "", api.ReleaseFeature4KRestoration},
		{"4K.Remaster", "", "", api.ReleaseFeature4KRemaster},
		{"EXTRAS", "", "", api.ReleaseFeatureExtras},
		{"2-Disc.Set", "", "", api.ReleaseFeatureTwoDiscSet},
		{"2in1", "", "2in1", ""},
		{"2D.3D.Edition", "", "", api.ReleaseFeature2D3DEdition},
		{"3D.Anaglyph", "", "", api.ReleaseFeature3DAnaglyph},
		{"3D.Full.SBS", "", "", api.ReleaseFeature3DFullSBS},
		{"HSBS", "", "", api.ReleaseFeature3DHalfSBS},
		{"Half.OU", "", "", api.ReleaseFeature3DHalfOU},
	} {
		t.Run(test.token, func(t *testing.T) {
			release := ParseReleaseInfo("Example.Movie.2026." + test.token + ".1080p.BluRay.x264-GRP.mkv")
			got := editionFromMeta(preparationstate.State{Release: release}, mediaInfoDoc{})
			var features []api.ReleaseFeature
			if test.feature != "" {
				features = []api.ReleaseFeature{test.feature}
			}
			if got.Edition != test.edition || got.Presentation != "" || got.Set != test.set || !slices.Equal(got.Features, features) {
				t.Fatalf("features = %#v, source=%#v", got, release)
			}
			if test.feature != "" {
				req := api.ReleaseNameRequest{
					Category:    "MOVIE",
					Type:        "ENCODE",
					Title:       "Example Movie",
					Year:        2026,
					Resolution:  "1080p",
					Source:      "BluRay",
					VideoEncode: "x264",
					ThreeD:      "3D",
					Tag:         "-GRP",
				}
				baseline := BuildReleaseName(req, api.NopLogger{})
				req.Cut, req.Edition, req.Presentation, req.EditionSet = got.Cut, got.Edition, got.Presentation, got.Set
				if named := BuildReleaseName(req, api.NopLogger{}); named.Name != baseline.Name {
					t.Fatalf("feature changed shared naming: %q != %q", named.Name, baseline.Name)
				}
			}
		})
	}
	for _, name := range []string{
		"4K.Restoration.Rifftrax.Extras.2026.2160p.BluRay.x265-GRP.mkv",
		"Example.Movie.2026.2160p.BluRay.x265-Rifftrax.mkv",
		"Example.Movie.2026.2160p.BluRay.x265-GRP.mkv",
	} {
		parts := editionFromMeta(preparationstate.State{Release: ParseReleaseInfo(name)}, mediaInfoDoc{})
		if len(parts.Features) != 0 || strings.Contains(parts.Edition, "Rifftrax") {
			t.Fatalf("title/group/resolution invented feature: %s => %#v", name, parts)
		}
	}
	if collection := ParseReleaseInfo("Example.Movie.2026.Criterion.1080p.BluRay.x264-GRP.mkv").Collection; collection != "Criterion.Collection" {
		t.Fatalf("filename collection lost: %q", collection)
	}
}

func TestCanonicalTheatricalIgnoresPresentationsAndFeatures(t *testing.T) {
	t.Parallel()
	for _, release := range []api.ReleaseInfo{
		{Cut: []string{"Theatrical.Cut"}, Edition: []string{"Open.Matte"}},
		{Cut: []string{"Theatrical.Cut"}, Collection: "IMAX"},
		{Cut: []string{"Theatrical.Cut"}, Other: []string{"4K Restoration", "Extras", "3D Half SBS"}},
	} {
		parts := editionFromMeta(preparationstate.State{Release: release}, mediaInfoDoc{})
		if parts.Cut != "" || parts.Edition != "" {
			t.Fatalf("presentation/features retained ordinary cut: %#v", parts)
		}
		if parts.Presentation == "" && len(parts.Features) == 0 {
			t.Fatalf("presentation/features lost: %#v", parts)
		}
	}
	for _, edition := range []string{"Theatrical / Custom Variant", "Theatrical / Open Matte", "Theatrical / 4K Restoration"} {
		state := preparationstate.State{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureExtras}, ReleaseNameOverrides: api.ReleaseNameOverrides{Edition: new(edition)}}
		applyReleaseNameValueOverrides(&state)
		if state.Edition != edition || len(state.ReleaseFeatures) != 0 {
			t.Fatalf("manual compound repartitioned: %#v", state)
		}
	}
	state := preparationstate.State{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureExtras}, ReleaseNameOverrides: api.ReleaseNameOverrides{NoEdition: new(true)}}
	applyReleaseNameValueOverrides(&state)
	if len(state.ReleaseFeatures) != 0 {
		t.Fatalf("omitted automatic features restored: %v", state.ReleaseFeatures)
	}
}

func TestFilenameMultiEditionMarkerPreservesPlaylistCuts(t *testing.T) {
	meta := preparationstate.State{
		DiscType:              "BDMV",
		Release:               api.ReleaseInfo{Other: []string{"2in1"}},
		SelectedBDMVPlaylists: []api.PlaylistInfo{{File: "00001.MPLS", Duration: 6000}, {File: "00002.MPLS", Duration: 6600}},
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{EditionDetails: map[string]api.IMDBEditionDetail{
			"6000": {Seconds: 6000, Minutes: 100},
			"6600": {
				Seconds:    6600,
				Minutes:    110,
				Attributes: []string{"Extended"},
			},
		}}},
	}
	parts := editionFromMeta(meta, mediaInfoDoc{})
	if parts.Set != "2in1" || parts.Cut != "Theatrical / Extended" {
		t.Fatalf("set marker lost playlist cuts: %#v", parts)
	}
	meta.SelectedBDMVPlaylists = nil
	meta.ProviderMetadata.IMDB = nil
	parts = editionFromMeta(meta, mediaInfoDoc{})
	if parts.Set != "2in1" {
		t.Fatalf("explicit set marker lost without runtime matches: %#v", parts)
	}
}
