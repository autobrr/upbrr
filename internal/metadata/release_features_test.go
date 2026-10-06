// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"github.com/autobrr/rls"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestReleaseFeaturesExcludeEpisodeTitle(t *testing.T) {
	for _, input := range []string{
		"Example.Show.2026.S01E01.Extras.1080p.WEB-DL.x264-GRP.mkv",
		"Example.Show.2026.10.04.Extras.1080p.WEB-DL.x264-GRP.mkv",
		"Example.Show.S01E01.Rifftrax.1080p.WEB-DL.x264-GRP.mkv",
	} {
		parsed := rls.ParseString(input)
		if got := parsedReleaseFeatures(parsed); len(got) != 0 {
			t.Errorf("features %v from episode title %s (%s, subtitle=%q)", got, input, parsed.Type, parsed.Subtitle)
		}
	}
}

func TestReleaseFeaturesAcceptUnderscoreTechnicalTokens(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"Example_Movie_2026_4K_Restoration_1080p_BluRay_x264-GRP.mkv", "4K Restoration"},
		{"Example_Movie_2026_Rifftrax_1080p_BluRay_x264-GRP.mkv", "Rifftrax"},
		{"Example_Movie_2026_3D_Half_SBS_1080p_BluRay_x264-GRP.mkv", "3D Half SBS"},
		{"Example.Show.S01E01.1080p.WEB-DL.Rifftrax.x264-GRP.mkv", "Rifftrax"},
	} {
		got := parsedReleaseFeatures(rls.ParseString(test.input))
		found := false
		for _, value := range got {
			found = found || value == test.want
		}
		if !found {
			t.Errorf("features %v, want %q for %s", got, test.want, test.input)
		}
	}
}

func TestAutomaticHybridDoesNotKeepSolitaryTheatrical(t *testing.T) {
	state := preparationstate.State{Release: api.ReleaseInfo{Cut: []string{"Theatrical.Cut"}, Other: []string{"HYBRiD"}}}
	for range 2 {
		parts := editionFromMeta(state, mediaInfoDoc{})
		if parts.Cut != "" || parts.Edition != "Hybrid" {
			t.Fatalf("automatic Hybrid retained ordinary cut: %#v", parts)
		}
		state.Edition = parts.Edition
	}
	state = preparationstate.State{ReleaseNameOverrides: api.ReleaseNameOverrides{Edition: new("Theatrical / Hybrid")}}
	applyReleaseNameValueOverrides(&state)
	if state.Edition != "Theatrical / Hybrid" {
		t.Fatalf("opaque manual compound rewritten: %#v", state)
	}
}

func TestReleaseVersionInstructionsPreserveCanonicalFeatureRoles(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		overrides api.ReleaseNameOverrides
		version   string
		features  bool
	}{
		{
			name:     "automatic",
			version:  "REPACK2",
			features: true,
		},
		{
			name:      "explicit",
			overrides: api.ReleaseNameOverrides{Repack: new("PROPER")},
			version:   "PROPER",
			features:  true,
		},
		{
			name:      "clear",
			overrides: api.ReleaseNameOverrides{Repack: new("")},
			features:  true,
		},
		{
			name:      "no edition",
			overrides: api.ReleaseNameOverrides{NoEdition: new(true), Repack: new("PROPER")},
			version:   "PROPER",
		},
		{
			name:      "manual theatrical",
			overrides: api.ReleaseNameOverrides{Edition: new("Theatrical"), Repack: new("PROPER")},
			version:   "PROPER",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := preparationstate.State{
				Cut:                  "Theatrical",
				Presentation:         "Open Matte",
				ReleaseFeatures:      []api.ReleaseFeature{api.ReleaseFeatureExtras},
				Repack:               "REPACK2",
				ReleaseNameOverrides: test.overrides,
			}
			applyReleaseNameValueOverrides(&state)
			if state.Cut != "" || state.Edition != "" || state.Repack != test.version {
				t.Fatalf("canonical role/version corrections diverged: %#v", state)
			}
			if (state.Presentation == "Open Matte") != test.features || (len(state.ReleaseFeatures) == 1) != test.features {
				t.Fatalf("version correction changed presentation/features: %#v", state)
			}
		})
	}
}
