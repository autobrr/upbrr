// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAnimeSearchAndAudioPreferManualFacts(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Anime:          true,
		ReleaseName:    "Fallback Release",
		AudioLanguages: []string{"Portuguese"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title: "Provider Title", OriginalLanguage: "en",
		}},
		EffectiveMetadata: api.EffectiveMetadata{
			Title:                      "Manual Title",
			TitleProvenance:            api.FactProvenanceManual,
			OriginalLanguage:           "pt",
			OriginalLanguageProvenance: api.FactProvenanceManual,
		},
	}
	if got := resolveSearchName(meta); got != "Manual Title" {
		t.Fatalf("manual anime search name = %q", got)
	}
	if got := resolveAudio(meta); got != "Nacional" {
		t.Fatalf("manual original language audio = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = ""
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
	if got := resolveLanguage(meta); got != "" {
		t.Fatalf("manual-empty original language = %q", got)
	}
	if got := resolveLanguage(api.UploadSubject{AudioLanguages: []string{"Portuguese"}}); got != "portuguese" {
		t.Fatalf("automatic audio language fallback = %q", got)
	}
	meta.EffectiveMetadata.Title = ""
	meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManualEmpty
	if got := resolveSearchName(meta); got != "" {
		t.Fatalf("manual-empty anime search name = %q", got)
	}
}

func TestAnimeDupeSearchPrefersManualTitle(t *testing.T) {
	t.Parallel()
	meta := api.DuplicateSubject{
		Anime:             true,
		Release:           api.ReleaseInfo{Title: "Parsed Title"},
		ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Title: "Provider Title"}},
		EffectiveMetadata: api.EffectiveMetadata{Title: "Manual Title", TitleProvenance: api.FactProvenanceManual},
	}
	if got := animeSearchTitle(meta); got != "Manual Title" {
		t.Fatalf("manual anime duplicate search = %q", got)
	}
	meta.EffectiveMetadata.Title = ""
	meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManualEmpty
	if got := animeSearchTitle(meta); got != "" {
		t.Fatalf("manual-empty anime duplicate search = %q", got)
	}
}

func TestResolveLanguagePreservesAutomaticTMDBAndAudioPrecedence(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		AudioLanguages:    []string{"Portuguese"},
		ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: ""}},
		EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: "Japanese"},
	}
	if got := resolveLanguage(meta); got != "portuguese" {
		t.Fatalf("automatic effective language changed audio fallback = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = "Japanese"
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManual
	if got := resolveLanguage(meta); got != "Japonês" {
		t.Fatalf("manual language = %q", got)
	}
}

func TestResolveAudioAcceptsManualCanonicalPortuguese(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{AudioLanguages: []string{"Portuguese"}, EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: "Portuguese", OriginalLanguageProvenance: api.FactProvenanceManual}}
	if got := resolveAudio(meta); got != "Nacional" {
		t.Fatalf("manual Portuguese audio = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = "pt"
	if got := resolveAudio(meta); got != "Nacional" {
		t.Fatalf("manual ISO Portuguese audio = %q", got)
	}
}
