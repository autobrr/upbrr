// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"errors"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSAMReviewUsesInternationalTitleWithYearAndVerifiedAudioMarker(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		tracks     [][]string
		marker     string
		year       int
		lastTitle  string
		lastManual bool
		locale     string
	}{
		{"dual", [][]string{{"Japanese"}, {"Portuguese [pt-BR]"}}, " DUAL", 2026, "", false, "en-US"},
		{"multi", [][]string{{"Japanese"}, {"English"}, {"pt-BR"}}, " MULTI", 2026, "", false, "en-US"},
		{"unqualified Portuguese", [][]string{{"Japanese"}, {"Portuguese"}}, "", 2026, "", false, "en-US"},
		{"explicit Brazilian track title", [][]string{{"Japanese"}, {"Portuguese"}}, " DUAL", 2026, "Portuguese (Brazil)", false, "en-US"},
		{"manual language beats track title", [][]string{{"Japanese"}, {"Portuguese"}}, "", 2026, "Portuguese (Brazil)", true, "en-US"},
		{"conflicting explicit region", [][]string{{"Japanese"}, {"pt-PT"}}, "", 2026, "Portuguese (Brazil)", false, "en-US"},
		{"conflicting European title", [][]string{{"Japanese"}, {"pt-BR"}}, "", 2026, "pt-PT", false, "en-US"},
		{"no linguistic content", [][]string{{"Japanese"}, {"pt-BR"}, {"zxx"}}, " DUAL", 2026, "", false, "en-US"},
		{"one multilingual track", [][]string{{"Japanese", "pt-BR"}}, "", 2026, "", false, "en-US"},
		{"no year in matched provider", [][]string{{"Japanese"}}, "", 0, "", false, "en-US"},
		{"regional English translation", [][]string{{"Japanese"}}, "", 2026, "", false, "en-GB"},
		{"verified English title from IMDb", [][]string{{"Japanese"}}, "", 2026, "", false, ""},
		{"verified TVDB title replaces the provider default", [][]string{{"Japanese"}}, "", 2026, "", false, "tvdb"},
		{"verified AniList movie title", [][]string{{"Japanese"}}, "", 2026, "", false, "anilist"},
		{"English original title without translations", [][]string{{"English"}}, "", 2026, "", false, "original"},
		{"missing English title", [][]string{{"Japanese"}}, "", 2026, "", false, "missing"},
		{"stale English title", [][]string{{"Japanese"}}, "", 2026, "", false, "stale"},
		{"explicit title clear", [][]string{{"Japanese"}}, "", 2026, "", false, "clear"},
		{"explicit English title is cleaned", [][]string{{"Japanese"}}, "", 2026, "", false, "manual"},
		{"opaque override is rebuilt from the English title", [][]string{{"Japanese"}}, "", 2026, "", false, "opaque"},
		{"bracketed AKA suffix", [][]string{{"Japanese"}}, "", 2026, "", false, "bracket"},
		{"dash before AKA suffix", [][]string{{"Japanese"}}, "", 2026, "", false, "dash"},
		{"compact em dash before AKA suffix", [][]string{{"Japanese"}}, "", 2026, "", false, "compact"},
		{"nonbreaking space before AKA suffix", [][]string{{"Japanese"}}, "", 2026, "", false, "nbsp"},
		{"terminal AKA is part of the primary title", [][]string{{"Japanese"}}, "", 2026, "", false, "terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
			source := t.TempDir()
			media := api.MediaFacts{TrackCoverageComplete: true}
			for i, languages := range test.tracks {
				track := api.MediaTrackFacts{
					ResourceID: "file",
					Kind:       api.MediaTrackAudio,
					Role:       api.AudioRoleProgramme,
					Languages:  languages,
				}
				if i == len(test.tracks)-1 {
					track.Title = test.lastTitle
					if test.lastManual {
						track.LanguageProvenance = api.FactProvenanceManual
					}
				}
				media.Tracks = append(media.Tracks, track)
			}
			input := trackers.PreparationInput{
				Tracker: "SAM",
				Meta: api.UploadSubject{
					ReleaseName: "原題 AKA Localized Series S01E02 1080p WEB-DL DUAL-GRP",
					SourcePath:  source,
					Identity: api.ExternalIdentity{
						Category:   api.CanonicalCategoryTV,
						TMDBID:     123,
						TVDBID:     456,
						SourcePath: source,
						Generation: 1,
					},
					ProviderMetadata: api.SourceScopedMetadata{
						SourcePath: source,
						Generation: 1,
						TMDB: &api.TMDBMetadata{
							TMDBID:          123,
							Title:           "原題",
							Year:            test.year,
							Category:        "TV",
							LocalizedTitles: map[string]string{test.locale: "Example Series"},
						},
						TVDB: &api.TVDBMetadata{TVDBID: 456, Name: "原題"},
					},
					Tag:             "-GRP",
					LanguageFacts:   mediafacts.ResolveLanguages(media),
					ManualLanguages: media.ManualLanguages(),
					MediaFileFacts:  api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{ResourceID: "file"}}},
					GeneratedName: &api.ReleaseNameDocument{
						Version: api.ReleaseNameDocumentVersionV1,
						Components: []api.ReleaseNameComponent{
							{
								Role:    api.NameRoleTitle,
								Value:   "原題",
								Present: true,
							},
							{
								Role:    api.NameRoleAlternateTitle,
								Value:   "AKA Localized Series",
								Present: true,
								Join:    " ",
								Manual:  true,
							},
							{
								Role:    api.NameRoleEpisode,
								Value:   "S01E02",
								Present: true,
								Join:    " ",
							},
							{
								Role:    api.NameRoleResolution,
								Value:   "1080p",
								Present: true,
								Join:    " ",
							},
							{
								Role:    api.NameRoleSource,
								Value:   "WEB-DL",
								Present: true,
								Join:    " ",
							},
							{
								Role:    api.NameRoleDualAudio,
								Value:   "DUAL",
								Present: true,
								Join:    " ",
							},
							{
								Role:    api.NameRoleGroup,
								Value:   "-GRP",
								Present: true,
							},
						},
					},
				},
			}
			episode := " S01E02"
			wantTitle := "Example Series"
			switch test.locale {
			case "original":
				data := input.Meta.ProviderMetadata.TMDB
				data.LocalizedTitles = nil
				data.OriginalLanguage = "en"
				data.OriginalTitle = "Example Series"
			case "missing":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = nil
			case "stale":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"en-US": "Old English Title"}
				input.Meta.ProviderMetadata.Generation++
			case "clear":
				input.Meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManualEmpty
			case "manual":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = nil
				document := input.Meta.GeneratedName
				document.Components[0].Value = "Example  Series (A.K.A.: Translated Title)"
				document.Components[0].Manual = true
				input.Meta.ReleaseName = document.Render().Name
			case "opaque":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"en-US": "Example Series (2026) aka Translated Title"}
				input.RequestedUploadName = new("Requested Title AKA Translation 2025 720p-GRP")
			case "bracket":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"en-US": "Example, Inc. [AKA: Translated Title]"}
				wantTitle = "Example, Inc."
			case "dash":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"en-US": "Example Series - AKA Translated Title"}
			case "compact":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"en-US": "Example Series—AKA: Translated Title"}
			case "nbsp":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"en-US": "Example Series\u00a0AKA\u00a0Translated Title"}
			case "terminal":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"en-US": "Example Series AKA"}
				wantTitle = "Example Series AKA"
			case "tvdb":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"en-US": "Other English Title"}
				input.Meta.ProviderMetadata.TVDB.NameEnglish = "Example Series (2026)"
			case "anilist":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = nil
				input.Meta.ProviderMetadata.TMDB.Category = "MOVIE"
				input.Meta.Identity.Category = api.CanonicalCategoryMovie
				input.Meta.Identity.MALID = 987
				input.Meta.ProviderMetadata.AniList = &api.AniListMetadata{
					MALID:        987,
					Format:       "MOVIE",
					TitleEnglish: "Example Series",
				}
				document := input.Meta.GeneratedName
				document.Components = slices.DeleteFunc(document.Components, func(component api.ReleaseNameComponent) bool {
					return component.Role == api.NameRoleEpisode
				})
				input.Meta.ReleaseName = document.Render().Name
				episode = ""
			case "":
				input.Meta.ProviderMetadata.TMDB.LocalizedTitles = nil
				input.Meta.Identity.IMDBID = 789
				input.Meta.ProviderMetadata.IMDB = &api.IMDBMetadata{
					IMDBID: 789,
					Akas: []api.IMDBAKA{
						{
							Title:      "Working Title",
							Country:    "United States",
							Language:   "English",
							Attributes: []string{"working title"},
						},
						{
							Title:    "Example Series",
							Country:  "United States",
							Language: "English",
						},
					},
				}
			}
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(input, binding)
			if test.locale == "missing" || test.locale == "stale" || test.locale == "clear" {
				rule, ok := errors.AsType[*trackers.NameRuleError](failure)
				if !ok || rule.Role != api.NameRoleTitle || (test.locale != "stale" && rule.Rule != "sam_english_title") {
					t.Fatalf("unverified title did not block the name as expected: %v", failure.Unwrap())
				}
				return
			}
			if test.year == 0 {
				rule, ok := errors.AsType[*trackers.NameRuleError](failure)
				if !ok || rule.Role != api.NameRoleYear {
					t.Fatalf("unavailable year did not block the automatic name: %v", failure)
				}
				return
			}
			if failure != nil {
				t.Fatalf("%v: %v", failure, failure.Unwrap())
			}
			name, err := prepared.ReviewedUploadName()
			want := wantTitle + " 2026" + episode + " 1080p WEB-DL" + test.marker + "-GRP"
			if err != nil || name != want || prepared.Projection.DuplicateCriteria.Name != want {
				t.Fatalf("upload = %q, search = %q, err = %v, want %q", name, prepared.Projection.DuplicateCriteria.Name, err, want)
			}
		})
	}
}

func TestSAMPackNameRequiresTheMarkerOnEveryEpisode(t *testing.T) {
	meta := samSeasonPack(t)
	dub := api.MediaTrackFacts{
		ResourceID:         "episode-2",
		Kind:               api.MediaTrackAudio,
		Role:               api.AudioRoleProgramme,
		Languages:          []string{"pt-BR"},
		LanguageProvenance: api.FactProvenanceManual,
	}
	media := api.MediaFacts{TrackCoverageComplete: true, Tracks: append(meta.LanguageFacts.Tracks, dub)}
	meta.LanguageFacts = mediafacts.ResolveLanguages(media)
	meta.ManualLanguages = media.ManualLanguages()
	if got := programmeMarker(meta); got != "" {
		t.Fatalf("marker with a dub on one episode = %q, want empty", got)
	}
	dub.ResourceID = "episode-1"
	dub.Languages = []string{"pt"}
	dub.Title = "Portuguese (Brazil)"
	dub.LanguageProvenance = api.FactProvenanceAutomatic
	media.Tracks = append(media.Tracks, dub)
	meta.LanguageFacts = mediafacts.ResolveLanguages(media)
	meta.ManualLanguages = media.ManualLanguages()
	if got := programmeMarker(meta); got != "DUAL" {
		t.Fatalf("marker with a dub on both episodes = %q, want DUAL", got)
	}
}

func TestSAMAggregateCorrectionPreservesCorroboratedPhysicalRegion(t *testing.T) {
	correction := []string{"en", "pt-BR"}
	corroborated := api.MediaTrackFacts{Languages: []string{"pt"}, Title: "Portuguese (Brazil)"}
	if got, ok := programmeTrackLanguage(corroborated, correction); !ok || got != "pt-BR" {
		t.Fatalf("corroborated region = %q, known = %v", got, ok)
	}
	conflict := api.MediaTrackFacts{Languages: []string{"pt-BR"}, Title: "pt-PT"}
	if got, ok := programmeTrackLanguage(conflict, correction); ok {
		t.Fatalf("aggregate correction certified a conflicting regional label: %q", got)
	}
}

func TestSAMHonoursAggregateRegionalAudioCorrection(t *testing.T) {
	media := api.MediaFacts{
		TrackCoverageComplete:    true,
		AudioLanguages:           []string{"en", "pt-PT"},
		AudioLanguagesProvenance: api.FactProvenanceManual,
		Tracks: []api.MediaTrackFacts{
			{
				ResourceID: "movie",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Languages:  []string{"en"},
			},
			{
				ResourceID: "movie",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Languages:  []string{"pt-BR"},
			},
		},
	}
	meta := api.UploadSubject{
		LanguageFacts:   mediafacts.ResolveLanguages(media),
		ManualLanguages: media.ManualLanguages(),
		MediaFileFacts:  api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{ResourceID: "movie", Primary: true}}},
	}
	if marker := programmeMarker(meta); marker != "" {
		t.Errorf("measured region overruled corrected audio in the marker: %s", marker)
	}
	evidence, _ := targetAudioEvidence(meta)
	for _, track := range evidence.ProgrammeAudio {
		if track.Language == "pt-BR" {
			t.Fatalf("withdrawn Brazilian language remains in duplicate evidence: %+v", evidence.ProgrammeAudio)
		}
	}
}
