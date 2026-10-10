// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnit3DSpecialsPreflightAndPayloadAgree(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                    string
		season                  int
		label                   string
		episode                 int
		pack, multiple, allowed bool
	}{
		{
			name:    "single special",
			label:   "S00",
			episode: 1,
			allowed: true,
		},
		{name: "unknown season", episode: 1},
		{
			name:    "ambiguous label",
			label:   "S01",
			episode: 1,
		},
		{
			name:    "negative season",
			season:  -1,
			label:   "S00",
			episode: 1,
		},
		{name: "zero episode", label: "S00"},
		{
			name:    "negative episode",
			label:   "S00",
			episode: -1,
		},
		{
			name:  "specials pack",
			label: "S00",
			pack:  true,
		},
		{
			name:    "pack with episode",
			label:   "S00",
			episode: 1,
			pack:    true,
		},
		{
			name:     "combined episodes",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:    "ordinary episode",
			season:  1,
			episode: 1,
			allowed: true,
		},
		{
			name:    "ordinary pack",
			season:  1,
			pack:    true,
			allowed: true,
		},
	} {
		for _, allowSpecials := range []bool{false, true} {
			for _, kind := range []string{"WEBDL", "ENCODE", "REMUX"} {
				t.Run(test.name+"/"+kind+"/"+boolFlag(allowSpecials), func(t *testing.T) {
					profile := SiteProfile{AllowSeasonZeroEpisodes: allowSpecials}
					meta := api.UploadSubject{
						Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryTV},
						Release:          api.ReleaseInfo{Resolution: "1080p"},
						Type:             kind,
						SeasonInt:        test.season,
						SeasonStr:        test.label,
						EpisodeInt:       test.episode,
						TVPack:           test.pack,
						MultipleEpisodes: test.multiple,
					}
					want := test.allowed && (test.season > 0 || allowSpecials)
					failures := validateUnit3DConstructibility("EXAMPLE", api.NewTrackerValidationSubject(meta, "EXAMPLE"), profile)
					if (len(failures) == 0) != want {
						t.Fatalf("preflight allowed=%t failures=%+v", want, failures)
					}
					data, err := buildUnit3DData(trackers.PreparationInput{Tracker: "EXAMPLE", Meta: meta}, "Example Series", "", "", "", profile)
					if err != nil {
						t.Fatal(err)
					}
					if (unit3DTVPayloadMetadataMessage(meta, data, profile) == "") != want {
						t.Fatalf("late validation disagrees with preflight allowed=%t", want)
					}
					if want && test.season == 0 && (data["season_number"] != "0" || data["episode_number"] != "1") {
						t.Fatalf("special payload coordinates = %q/%q", data["season_number"], data["episode_number"])
					}
				})
			}
		}
	}
}

func TestValidationAdapterPreservesSpecialFacts(t *testing.T) {
	subject := api.TrackerValidationSubject{
		SeasonStr:        "S00",
		EpisodeInt:       1,
		MultipleEpisodes: true,
	}
	got := unit3DUploadSubject(subject)
	if got.SeasonStr != "S00" || !got.MultipleEpisodes {
		t.Fatalf("specials facts lost: %+v", got)
	}
}

func TestSpecialSearchKeepsSeasonNameAndBroadEpisodeScope(t *testing.T) {
	params := buildDupeSearchParams(api.DuplicateSubject{
		Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryTV, TMDBID: 123456},
		SeasonStr:   "S00",
		EpisodeInt:  1,
		ReleaseName: "Example.Series.S00E01.1080p.WEB-DL-GRP",
	}, SiteProfile{AllowSeasonZeroEpisodes: true})
	if params.Get("name") != " S00" || params.Get("tmdbId") != "123456" || params.Get("categories[]") != "2" {
		t.Fatalf("special search lost work/season scope: %v", params)
	}
	if params.Has("episodeNumber") || params.Has("seasonNumber") || params.Has("types[]") || params.Has("resolutions[]") {
		t.Fatalf("special search gained unsafe narrowing: %v", params)
	}
}
