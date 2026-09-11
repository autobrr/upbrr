// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tik

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestProfileResolvers(t *testing.T) {
	profile := Profile().Site
	if got := profile.ResolveTypeID(api.UploadSubject{Release: api.ReleaseInfo{Size: "BD50"}, DiscType: "BDMV"}); got != "5" {
		t.Fatalf("BD50 type = %q", got)
	}
	if got := profile.ResolveTypeID(api.UploadSubject{ReleaseName: "Example.Release.2026.BD50.COMPLETE-GRP", DiscType: "BDMV"}); got != "1" {
		t.Fatalf("raw-name BD50 type = %q", got)
	}
	disc := "BD66"
	if got := profile.ResolveTypeID(api.UploadSubject{TrackerSiteOverrides: api.TrackerSiteOverrides{TIK: api.TIKOverrides{DiscType: &disc}}}); got != "4" {
		t.Fatalf("override type = %q", got)
	}
	foreign := api.UploadSubject{Identity: api.ExternalIdentity{Category: "MOVIE"}, ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "fr"}}}
	if got := profile.ResolveCategoryID(foreign); got != "3" {
		t.Fatalf("foreign category = %q", got)
	}
	asian := api.UploadSubject{
		Identity:          api.ExternalIdentity{Category: "MOVIE"},
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		ProviderMetadata:  api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "en", OriginCountry: []string{"JP"}}},
	}
	if got := profile.ResolveCategoryID(asian); got != "6" {
		t.Fatalf("Asian category = %q", got)
	}
	opera := api.UploadSubject{
		Identity:          api.ExternalIdentity{Category: "TV"},
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		Release:           api.ReleaseInfo{Genre: "Opera"},
	}
	if got := profile.ResolveCategoryID(opera); got != "5" {
		t.Fatalf("opera category = %q", got)
	}
}

func TestAutomaticLanguageClassificationUsesOnlyTMDB(t *testing.T) {
	t.Parallel()

	englishTracks := api.UploadSubject{AudioLanguages: []string{"English"}, SubtitleLanguages: []string{"English"}}
	automatic := englishTracks
	automatic.EffectiveMetadata = api.EffectiveMetadata{OriginalLanguage: "ja"}
	if isForeign(automatic) || isAsian(automatic) {
		t.Fatalf("automatic effective language changed TIK classification: foreign=%t asian=%t", isForeign(automatic), isAsian(automatic))
	}

	automatic.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalLanguage: ""}
	if isForeign(automatic) || isAsian(automatic) {
		t.Fatalf("blank TMDB language changed TIK classification: foreign=%t asian=%t", isForeign(automatic), isAsian(automatic))
	}

	manual := englishTracks
	manual.EffectiveMetadata = api.EffectiveMetadata{OriginalLanguage: "ja", OriginalLanguageProvenance: api.FactProvenanceManual}
	if !isForeign(manual) || !isAsian(manual) {
		t.Fatalf("manual Japanese language was ignored: foreign=%t asian=%t", isForeign(manual), isAsian(manual))
	}

	manual.EffectiveMetadata = api.EffectiveMetadata{OriginalLanguageProvenance: api.FactProvenanceManualEmpty}
	manual.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalLanguage: "ja", OriginCountry: []string{"JP"}}
	if isForeign(manual) || !isAsian(manual) {
		t.Fatalf("manual-empty language changed independent facts: foreign=%t asian=%t", isForeign(manual), isAsian(manual))
	}
}

func TestOperaClassificationPreservesAutomaticAndManualGenrePrecedence(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{ProviderMetadata: api.SourceScopedMetadata{
		TMDB: &api.TMDBMetadata{Genres: "Drama"},
		IMDB: &api.IMDBMetadata{Genres: "Opera"},
	}}
	if !isOpera(meta) {
		t.Fatal("IMDb opera genre was ignored")
	}
	meta.ProviderMetadata.IMDB.Genres = "Drama"
	meta.Release.Genre = "Opera"
	if !isOpera(meta) {
		t.Fatal("release opera genre was ignored")
	}

	meta.EffectiveMetadata = api.EffectiveMetadata{Genres: []string{"Drama"}, GenresProvenance: api.FactProvenanceManual}
	meta.ProviderMetadata.IMDB.Genres = "Opera"
	meta.Release.Genre = "Opera"
	meta.ProviderMetadata.TMDB.Keywords = ""
	if isOpera(meta) {
		t.Fatal("manual genres fell back to automatic provider genres")
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{GenresProvenance: api.FactProvenanceManualEmpty}
	meta.ProviderMetadata.TMDB.Keywords = "Opera"
	if !isOpera(meta) {
		t.Fatal("manual-empty genres suppressed independent keyword evidence")
	}
}

func TestManualCanonicalLanguageClassification(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: "Japanese", OriginalLanguageProvenance: api.FactProvenanceManual},
	}
	if !isAsian(meta) {
		t.Fatal("manual Japanese language was not Asian")
	}
	meta.EffectiveMetadata.OriginalLanguage = "ja"
	if !isAsian(meta) {
		t.Fatal("manual ISO Japanese language was not Asian")
	}
	meta.EffectiveMetadata.OriginalLanguage = "English"
	if isForeign(meta) {
		t.Fatal("manual English language was foreign")
	}
	meta.EffectiveMetadata.OriginalLanguage = "en"
	if isForeign(meta) {
		t.Fatal("manual ISO English language was foreign")
	}
}
