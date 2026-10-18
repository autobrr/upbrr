// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildReleaseNameEditionDisplayPreservesVisibleWording(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, category, releaseType, discType, source, edition, display, want string
	}{
		{
			name:        "movie web mixed annotation",
			category:    "MOVIE",
			releaseType: "WEBDL",
			edition:     "Collector's",
			display:     "4K Remaster Collector's",
			want:        "Feature Story 2026 Extended 4K Remaster Collector's Open Matte 1080p WEB-DL-GRP",
		},
		{
			name:        "tv web mixed annotation",
			category:    "TV",
			releaseType: "WEBDL",
			edition:     "Collector's",
			display:     "4K Remaster Collector's",
			want:        "Feature Story 2026 S01E02 Pilot Extended 4K Remaster Collector's Open Matte 1080p WEB-DL-GRP",
		},
		{
			name:        "movie encode annotation only",
			category:    "MOVIE",
			releaseType: "ENCODE",
			source:      "BluRay",
			display:     "4K Restoration",
			want:        "Feature Story 2026 Extended 4K Restoration Open Matte 1080p BluRay-GRP",
		},
		{
			name:        "movie dvd disc",
			category:    "MOVIE",
			releaseType: "DISC",
			discType:    "DVD",
			source:      "PAL DVD",
			edition:     "Collector's",
			display:     "Collector's 2-Disc Set",
			want:        "Feature Story 2026 Extended Collector's 2-Disc Set Open Matte PAL DVD-GRP",
		},
		{
			name:        "movie remux",
			category:    "MOVIE",
			releaseType: "REMUX",
			source:      "BluRay",
			edition:     "Collector's",
			display:     "Extras Collector's",
			want:        "Feature Story 2026 Extended Extras Collector's Open Matte 1080p BluRay REMUX-GRP",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := api.ReleaseNameRequest{
				Category:     test.category,
				Type:         test.releaseType,
				DiscType:     test.discType,
				Title:        "Feature Story",
				Year:         2026,
				SearchYear:   "2026",
				Season:       "S01",
				Episode:      "E02",
				EpisodeTitle: "Pilot",
				Resolution:   "1080p",
				Source:       test.source,
				Tag:          "-GRP",
				Cut:          "Extended",
				Presentation: "Open Matte",
				Edition:      test.display,
			}
			previous := BuildReleaseName(request, nil)
			request.Edition, request.EditionDisplay = test.edition, test.display
			result := BuildReleaseName(request, nil)
			if result.Name != test.want || result.Name != previous.Name || result.NameNoTag != previous.NameNoTag ||
				result.CleanName != previous.CleanName || result.GeneratedVariants != previous.GeneratedVariants {
				t.Fatalf("display name = %#v, previous = %#v, want %q", result, previous, test.want)
			}
			if err := result.GeneratedName.Validate(); err != nil {
				t.Fatal(err)
			}
			edition, ok := result.GeneratedName.Component(api.NameRoleEdition)
			if !ok || edition.Present || edition.AvailableValue != test.edition {
				t.Fatalf("canonical edition = %#v, found=%t", edition, ok)
			}
			display, ok := result.GeneratedName.Component(api.NameRoleEditionDisplay)
			if !ok || !display.Present || display.Value != test.display || display.AvailableValue != test.display {
				t.Fatalf("display edition = %#v, found=%t", display, ok)
			}
		})
	}
}

func TestBuildReleaseNameEditionDisplayPreservesSetSuppression(t *testing.T) {
	t.Parallel()
	request := api.ReleaseNameRequest{
		Category:       "MOVIE",
		Type:           "ENCODE",
		Title:          "Feature Story",
		Year:           2026,
		EditionSet:     "2in1",
		Cut:            "Extended",
		Presentation:   "Open Matte",
		Edition:        "Collector's",
		EditionDisplay: "4K Remaster Collector's",
	}
	result := BuildReleaseName(request, nil)
	if result.Name != "Feature Story 2026 2in1" {
		t.Fatalf("set name = %q", result.Name)
	}
	for _, role := range []api.ReleaseNameRole{api.NameRoleCut, api.NameRoleEdition, api.NameRoleEditionDisplay, api.NameRolePresentation} {
		if component, ok := result.GeneratedName.Component(role); ok && (component.Present || component.AvailableValue != "") {
			t.Fatalf("set retained suppressed role %s: %#v", role, component)
		}
	}
}

func TestReleaseNameRequestKeepsEditionDisplaySeparate(t *testing.T) {
	t.Parallel()
	request := releaseNameRequestFromMeta(preparationstate.State{
		Edition: "Collector's", EditionDisplay: []api.EditionDisplayPart{
			{Text: "4K Remaster", Feature: api.ReleaseFeature4KRemaster},
			{Text: "Collector's"},
		},
	}, nil)
	if request.Edition != "Collector's" || request.EditionDisplay != "4K Remaster Collector's" {
		t.Fatalf("edition request = %q, display = %q", request.Edition, request.EditionDisplay)
	}
}

func TestBuildReleaseNameEditionDisplayKeepsHybridSeparate(t *testing.T) {
	t.Parallel()
	request := api.ReleaseNameRequest{
		Category:       "MOVIE",
		Type:           "ENCODE",
		Title:          "Feature Story",
		Year:           2026,
		Edition:        "Collector's",
		EditionDisplay: "4K Remaster Hybrid Collector's",
		Hybrid:         true,
	}
	result := BuildReleaseName(request, nil)
	if result.Name != "Feature Story 2026 4K Remaster Collector's Hybrid" {
		t.Fatalf("Hybrid display name = %q", result.Name)
	}
	display, _ := result.GeneratedName.Component(api.NameRoleEditionDisplay)
	if display.Value != "4K Remaster Collector's" {
		t.Fatalf("Hybrid remained in display component: %#v", display)
	}
}

func TestBuildReleaseNameEditionDisplayRetainsDVDRipOmission(t *testing.T) {
	t.Parallel()
	request := api.ReleaseNameRequest{
		Category:       "MOVIE",
		Type:           "DVDRIP",
		Title:          "Feature Story",
		Year:           2026,
		Edition:        "Collector's",
		EditionDisplay: "4K Remaster Collector's",
	}
	result := BuildReleaseName(request, nil)
	if result.Name != "Feature Story 2026 DVDRip" {
		t.Fatalf("DVD rip restored edition wording: %q", result.Name)
	}
	for role, want := range map[api.ReleaseNameRole]string{
		api.NameRoleEdition: "Collector's", api.NameRoleEditionDisplay: "4K Remaster Collector's",
	} {
		component, ok := result.GeneratedName.Component(role)
		if !ok || component.Present || component.AvailableValue != want {
			t.Fatalf("retained %s = %#v, found=%t", role, component, ok)
		}
	}
}

func TestReleaseNameOverridesKeepEditionDisplaySubordinate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		overrides api.ReleaseNameOverrides
		want      string
	}{
		{
			name:      "manual edition",
			overrides: api.ReleaseNameOverrides{Edition: new("Custom Extras Edition")},
			want:      "Feature Story 2026 Custom Extras Edition 4K Remaster",
		},
		{
			name:      "omit edition",
			overrides: api.ReleaseNameOverrides{NoEdition: new(true)},
			want:      "Feature Story 2026",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := preparationstate.State{
				Type: "ENCODE",
				Release: api.ReleaseInfo{
					Category: "MOVIE",
					Title:    "Feature Story",
					Year:     2026,
				},
				Edition: "Collector's",
				EditionDisplay: []api.EditionDisplayPart{
					{Text: "4K Remaster", Feature: api.ReleaseFeature4KRemaster},
					{Text: "Collector's"},
				},
				ReleaseNameOverrides: test.overrides,
			}
			applyReleaseNameValueOverrides(&state)
			RebuildReleaseName(&state, nil)
			if state.ReleaseName != test.want {
				t.Fatalf("overridden display = %#v, name = %q, want %q", state.EditionDisplay, state.ReleaseName, test.want)
			}
		})
	}
	request := applyReleaseNameOverrides(api.ReleaseNameRequest{EditionDisplay: "Extras"}, api.ReleaseNameOverrides{NoEdition: new(true)}, nil)
	if request.EditionDisplay != "" {
		t.Fatalf("omission retained request display: %q", request.EditionDisplay)
	}
}
