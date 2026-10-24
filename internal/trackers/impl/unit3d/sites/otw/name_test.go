// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameUsesCurrentEnglishTMDBTVTitleAndDebutYear(t *testing.T) {
	t.Parallel()
	const sourcePath = "Example.Show.S01E02.1080p.WEB-DL.H.264-GRP"
	meta := api.UploadSubject{
		SourcePath: sourcePath,
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 4,
			Category:   api.CanonicalCategoryTV,
			TMDBID:     987650001,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 4,
			TMDB: &api.TMDBMetadata{
				TMDBID:        987650001,
				Title:         "Example Show",
				OriginalTitle: "Example Série",
				Year:          2020,
			},
		},
		Release: api.ReleaseInfo{
			Title:      "Parsed Show",
			Alt:        "AKA Example Série",
			Resolution: "1080p",
		},
		SeasonInt:    1,
		EpisodeInt:   2,
		SeasonStr:    "S01",
		EpisodeStr:   "E02",
		EpisodeTitle: "Example Episode",
		Edition:      "Limited Edition",
		Distributor:  "Criterion Collection",
		Service:      "DSNP",
		Type:         "WEBDL",
		Audio:        "DD+",
		Channels:     "5.1",
		VideoCodec:   "H.264",
		Tag:          "-GRP",
	}

	const want = "Example Show 2020 S01E02 Example Episode 1080p DSNP WEB-DL DD+ 5.1 H.264-GRP"
	if got := buildName(meta, config.TrackerConfig{}); got != want {
		t.Fatalf("OTW name = %q, want %q", got, want)
	} else if strings.Contains(got, "AKA") || strings.Contains(got, "Limited") || strings.Contains(got, "Criterion") {
		t.Fatalf("OTW non-disc name retained forbidden elements: %q", got)
	}
}

func TestBuildNameAllowsDistributorOnlyForCompleteDisc(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryMovie,
			TMDBID:   987650002,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID: 987650002,
				Title:  "Example Release",
				Year:   2026,
			},
		},
		Release:     api.ReleaseInfo{Resolution: "1080p"},
		Type:        "DISC",
		DiscType:    "BLURAY",
		Distributor: "Criterion Collection",
		Region:      "USA",
		Edition:     "Limited Edition",
		Source:      "Blu-ray",
		VideoCodec:  "AVC",
		Audio:       "DTS-HD MA",
		Channels:    "5.1",
	}
	got := buildName(meta, config.TrackerConfig{})
	if !strings.Contains(got, "1080p Criterion Collection USA") {
		t.Fatalf("OTW disc distributor missing: %q", got)
	}
	if strings.Contains(got, "Limited") {
		t.Fatalf("OTW disc retained edition: %q", got)
	}
}

func TestBuildNameUsesPreparedSeasonFactsOnly(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{
			Category: api.CanonicalCategoryTV,
			TMDBID:   987650003,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{
				TMDBID: 987650003,
				Title:  "Example Show",
				Year:   2020,
			},
		},
		FileList: []string{
			filepath.Join("Example Show", "Season 1", "Example.Show.S02E01.mkv"),
			filepath.Join("Example Show", "Season 1", "Example.Show.S02E02.mkv"),
		},
		Release:   api.ReleaseInfo{Resolution: "1080p"},
		SeasonInt: 2,
		SeasonStr: "S02",
		TVPack:    true,
		Type:      "WEBDL",
	}
	const want = "Example Show 2020 S02 1080p WEB-DL"
	if got := buildName(meta, config.TrackerConfig{}); got != want {
		t.Fatalf("OTW name = %q, want prepared season facts only: %q", got, want)
	}
}

func TestProfileBuildNameVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().Site.BuildNameVersion; got != "v7" {
		t.Fatalf("OTW BuildNameVersion = %q, want v7", got)
	}
}

func TestOTWVideoCodecUsesResolvedMediaFact(t *testing.T) {
	t.Parallel()

	if got := otwVideoCodec(api.UploadSubject{VideoCodec: "H.265", Release: api.ReleaseInfo{Codec: []string{"H.264"}}}); got != "H.265" {
		t.Fatalf("resolved video codec = %q", got)
	}
	if got := otwVideoCodec(api.UploadSubject{Release: api.ReleaseInfo{Codec: []string{"H.265"}}}); got != "" {
		t.Fatalf("raw codec fallback = %q", got)
	}
}

func TestBuildNamePrefersManualTitleAndYear(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{TMDBID: 912345},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			TMDBID: 912345,
			Title:  "Provider Title",
			Year:   2020,
		}},
		Release: api.ReleaseInfo{
			Title:      "Parsed Title",
			Year:       2021,
			Resolution: "1080p",
		},
		EffectiveMetadata: api.EffectiveMetadata{
			Title:           "Manual Title",
			TitleProvenance: api.FactProvenanceManual,
			Year:            2030,
			YearProvenance:  api.FactProvenanceManual,
		},
	}
	if got := buildName(meta, config.TrackerConfig{}); !strings.HasPrefix(got, "Manual Title 2030 ") {
		t.Fatalf("manual name = %q", got)
	}
}

func TestOTWAudioMarkersUseProgrammeFactsAndPresentationControls(t *testing.T) {
	t.Parallel()
	original := api.LanguageFacts{
		OriginalLanguages:      []string{"Japanese"},
		OriginalLanguagesKnown: true,
		ProgrammeLanguages:     []string{"Japanese", "English"},
		ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
		Tracks: []api.MediaTrackFacts{
			{
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Languages: []string{"Japanese"},
			},
			{
				Kind:        api.MediaTrackAudio,
				Role:        api.AudioRoleProgramme,
				Languages:   []string{"English"},
				AudioOrigin: api.TrackAudioDubbed,
			},
		},
	}
	for _, audio := range []string{"DD+ 5.1 Dual-Audio", "Dual-Audio DD+ 5.1", "Dubbed Dual-Audio DD+ 5.1"} {
		meta := api.UploadSubject{
			Type:          "REMUX",
			DiscType:      "BDMV",
			Audio:         audio,
			LanguageFacts: original,
		}
		if got := otwAudio(meta); got != "Dual Audio DD+ 5.1" {
			t.Fatalf("remux marker: %q", got)
		}
		yes := true
		meta.ReleaseNameOverrides.NoDual = &yes
		if got := otwAudio(meta); got != "DD+ 5.1" {
			t.Fatalf("NoDual retained marker: %q", got)
		}
		meta.ReleaseNameOverrides.DualAudio = &yes
		if got := otwAudio(meta); got != "Dual Audio DD+ 5.1" {
			t.Fatalf("forced dual lost precedence: %q", got)
		}
		meta.ReleaseNameOverrides.DualAudio = nil
		meta.ReleaseNameOverrides.NoDual = nil
		meta.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
		if got := otwAudio(meta); got != "DD+ 5.1" {
			t.Fatalf("contradictory facts added marker: %q", got)
		}
		meta.Type = "DISC"
		if got := otwAudio(meta); got != audio {
			t.Fatalf("full disc changed: %q", got)
		}
	}
	meta := api.UploadSubject{Audio: "DD+ 5.1", LanguageFacts: original}
	meta.LanguageFacts.ProgrammeLanguages = []string{"English"}
	meta.LanguageFacts.Tracks = original.Tracks[1:]
	if got := otwAudio(meta); got != "Dubbed DD+ 5.1" {
		t.Fatalf("dub-only marker: %q", got)
	}
	meta.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
	if got := otwAudio(meta); got != "DD+ 5.1" {
		t.Fatalf("partial facts claimed dub-only: %q", got)
	}
	meta.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusComplete
	meta.LanguageFacts.ProgrammeLanguages = []string{"Japanese", "English", "German"}
	meta.LanguageFacts.Tracks = original.Clone().Tracks
	meta.LanguageFacts.Tracks = append(meta.LanguageFacts.Tracks, api.MediaTrackFacts{
		Kind:        api.MediaTrackAudio,
		Role:        api.AudioRoleProgramme,
		Languages:   []string{"German"},
		AudioOrigin: api.TrackAudioDubbed,
	})
	if got := otwAudio(meta); got != "MULTI DD+ 5.1" {
		t.Fatalf("multiple marker: %q", got)
	}
	meta.LanguageFacts.ProgrammeLanguages = []string{"Japanese"}
	meta.LanguageFacts.Tracks = original.Tracks[:1]
	meta.LanguageFacts.SubtitleLanguages = []string{"English", "German"}
	if got := otwAudio(meta); got != "DD+ 5.1" {
		t.Fatalf("subtitles created audio marker: %q", got)
	}
}

func TestBuildNameRetainsCanonicalModifiersInAitherOrder(t *testing.T) {
	t.Parallel()
	for _, releaseType := range []string{"DISC", "REMUX", "WEBDL", "ENCODE"} {
		t.Run(releaseType, func(t *testing.T) {
			meta := api.UploadSubject{
				Release: api.ReleaseInfo{
					Title:      "Example",
					Year:       2026,
					Resolution: "1080p",
				},
				Type:   releaseType,
				Repack: "REPACK2",
				GeneratedName: &api.ReleaseNameDocument{
					Version: api.ReleaseNameDocumentVersionV1,
					Components: []api.ReleaseNameComponent{
						{
							Role:    api.NameRoleHybrid,
							Value:   "Hybrid",
							Present: true,
						},
						{
							Role:    api.NameRolePresentation,
							Value:   "Open Matte IMAX",
							Present: true,
						},
						{
							Role:    api.NameRoleEdition,
							Value:   "Collector Edition",
							Present: true,
						},
						{
							Role:    api.NameRoleCut,
							Value:   "Extended Cut",
							Present: true,
						},
					},
				},
			}
			const prefix = "Example 2026 Extended Cut Open Matte IMAX Hybrid REPACK2 1080p"
			if got := buildName(meta, config.TrackerConfig{}); !strings.HasPrefix(got, prefix) {
				t.Fatalf("OTW modifiers = %q, want prefix %q", got, prefix)
			}
		})
	}
}

func TestBuildNamePreservesCanonicalModifierPresenceAndManualEdition(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Title:      "Example",
			Year:       2026,
			Resolution: "1080p",
		},
		Cut:          "Extended Cut",
		Presentation: "IMAX",
		GeneratedName: &api.ReleaseNameDocument{
			Version: api.ReleaseNameDocumentVersionV1,
			Components: []api.ReleaseNameComponent{
				{
					Role:    api.NameRoleEditionSet,
					Value:   "2in1",
					Present: true,
				},
				{
					Role:    api.NameRoleCut,
					Value:   "Extended Cut",
					Present: false,
				},
				{
					Role:    api.NameRoleEdition,
					Value:   "Custom Edition",
					Present: true,
					Manual:  true,
				},
				{
					Role:    api.NameRolePresentation,
					Value:   "IMAX",
					Present: false,
					Manual:  true,
				},
				{
					Role:    api.NameRoleHybrid,
					Value:   "Hybrid",
					Present: false,
					Manual:  true,
				},
			},
		},
	}
	const want = "Example 2026 2in1 Custom Edition 1080p"
	if got := buildName(meta, config.TrackerConfig{}); got != want {
		t.Fatalf("OTW modifier presence = %q, want %q", got, want)
	}
}

func TestOTWReleaseNamePolicyPreservesRequestedName(t *testing.T) {
	t.Parallel()
	requested := "My exact release name"
	binding := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	resolved, err := binding.Resolver(trackers.ReleaseNameInput{
		Subject:       api.UploadSubject{Release: api.ReleaseInfo{Title: "Automatic title"}},
		RequestedName: &requested,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Upload != requested {
		t.Fatalf("requested upload name = %q, want %q", resolved.Upload, requested)
	}
}

func TestOTWReviewedProjectionUsesSameModifiersForUploadAndSearch(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Release: api.ReleaseInfo{
			Title:      "Example",
			Year:       2026,
			Resolution: "1080p",
		},
		Type:       "WEBDL",
		Source:     "WEB",
		Service:    "NF",
		Audio:      "DD+ 5.1",
		VideoCodec: "H.264",
		Tag:        "-GRP",
		GeneratedName: &api.ReleaseNameDocument{
			Version: api.ReleaseNameDocumentVersionV1,
			Components: []api.ReleaseNameComponent{
				{
					Role:    api.NameRoleEditionDisplay,
					Value:   "Custom Cut / Hybrid",
					Present: true,
					Manual:  true,
				},
				{
					Role:    api.NameRolePresentation,
					Value:   "IMAX",
					Present: true,
				},
			},
		},
	}
	const want = "Example 2026 Custom Cut / Hybrid IMAX 1080p NF WEB-DL DD+ 5.1 H.264-GRP"
	binding := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	for _, requested := range []*string{nil, new("Exact manual title")} {
		prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
			Tracker:             "OTW",
			Meta:                meta,
			RequestedUploadName: requested,
		}, binding)
		if failure != nil {
			t.Fatal(failure)
		}
		name, err := prepared.ReviewedUploadName()
		if err != nil {
			t.Fatal(err)
		}
		wantUpload := want
		if requested != nil {
			wantUpload = *requested
		}
		if name != wantUpload || prepared.Projection.DuplicateCriteria.Name != want {
			t.Fatalf("upload/search = %q / %q, want %q / %q", name, prepared.Projection.DuplicateCriteria.Name, wantUpload, want)
		}
	}
}

func TestOTWNameModifiersRetainIndividualComponents(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		role  api.ReleaseNameRole
		value string
	}{
		{api.NameRoleEditionSet, "3in1"},
		{api.NameRoleCut, "Director's Cut"},
		{api.NameRolePresentation, "Open Matte"},
		{api.NameRoleHybrid, "Hybrid"},
	} {
		t.Run(string(test.role), func(t *testing.T) {
			meta := api.UploadSubject{GeneratedName: &api.ReleaseNameDocument{
				Version: api.ReleaseNameDocumentVersionV1,
				Components: []api.ReleaseNameComponent{{
					Role:    test.role,
					Value:   test.value,
					Present: true,
				}},
			}}
			if got := otwNameModifiers(meta); len(got) != 1 || got[0] != test.value {
				t.Fatalf("individual modifier = %q, want %q", got, test.value)
			}
		})
	}
	if got := otwNameModifiers(api.UploadSubject{Cut: "Extended", Presentation: "IMAX"}); len(got) != 0 {
		t.Fatalf("missing document reconstructed modifiers: %q", got)
	}
}
