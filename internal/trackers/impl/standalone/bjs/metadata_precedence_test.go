// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBJSMetadataSelectorsPreferManualFacts(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title:            "Provider Title",
			OriginalLanguage: "en",
			Genres:           "Porn",
			Localized:        map[string]api.TMDBLocalizedData{"pt-BR": {Title: "Titulo Localizado", Genres: "Ação"}},
		}},
		EffectiveMetadata: api.EffectiveMetadata{
			Title:                      "Manual Title",
			TitleProvenance:            api.FactProvenanceManual,
			OriginalLanguage:           "pt",
			OriginalLanguageProvenance: api.FactProvenanceManual,
			Genres:                     []string{"Drama"},
			GenresProvenance:           api.FactProvenanceManual,
		},
	}
	if got := resolveLanguage(meta); got != "Português" {
		t.Fatalf("manual language = %q", got)
	}
	if got := resolveTags(meta, api.ExtractTrackerLocalizedPTBR(meta)); got != "drama" {
		t.Fatalf("manual tags = %q", got)
	}
	if got := buildFields(meta, "", "", nil)["titulobrasileiro"]; got != "Manual Title" {
		t.Fatalf("manual localized title = %q", got)
	}
	meta.EffectiveMetadata.Title = ""
	meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManualEmpty
	meta.EffectiveMetadata.OriginalTitleProvenance = api.FactProvenanceManualEmpty
	fields := buildFields(meta, "", "", nil)
	if got := fields["title"]; got != "" {
		t.Fatalf("manual-empty original title = %q", got)
	}
	if got := fields["titulobrasileiro"]; got != "" {
		t.Fatalf("manual-empty localized title = %q", got)
	}
	automatic := api.UploadSubject{
		Release: api.ReleaseInfo{Title: "Legacy Release Title"},
		EffectiveMetadata: api.EffectiveMetadata{
			Title:         "Effective Title",
			OriginalTitle: "Effective Original Title",
		},
	}
	fields = buildFields(automatic, "", "", nil)
	if got := fields["title"]; got != "Effective Original Title" {
		t.Fatalf("automatic original title = %q", got)
	}
	if got := fields["titulobrasileiro"]; got != "Effective Title" {
		t.Fatalf("automatic localized title = %q", got)
	}
	meta.EffectiveMetadata.Genres = nil
	meta.EffectiveMetadata.GenresProvenance = api.FactProvenanceManualEmpty
	if got := resolveAdult(meta); got != "2" {
		t.Fatalf("manual-empty adult flag = %q", got)
	}
}

func TestResolveLanguageAcceptsManualCanonicalPortuguese(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: "Portuguese", OriginalLanguageProvenance: api.FactProvenanceManual}}
	if got := resolveLanguage(meta); got != "Português" {
		t.Fatalf("manual Portuguese language = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = "pt"
	if got := resolveLanguage(meta); got != "Português" {
		t.Fatalf("manual ISO Portuguese language = %q", got)
	}
}
