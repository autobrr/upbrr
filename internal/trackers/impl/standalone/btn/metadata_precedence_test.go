// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNMetadataSelectorsPreferManualFacts(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		ProviderMetadata: api.SourceScopedMetadata{
			TVDB: &api.TVDBMetadata{OriginalLanguage: "ja", Genres: "Action"},
		},
		EffectiveMetadata: api.EffectiveMetadata{
			OriginalLanguage:           "en",
			OriginalLanguageProvenance: api.FactProvenanceManual,
			Genres:                     []string{"Drama"},
			GenresProvenance:           api.FactProvenanceManual,
		},
	}
	if got := resolveBTNOriginalLanguage(meta); got != "en" {
		t.Fatalf("manual original language = %q", got)
	}
	if got := resolveBTNTags(meta, nil); got != "Drama" {
		t.Fatalf("manual tags = %q", got)
	}
	meta.EffectiveMetadata.Genres = nil
	meta.EffectiveMetadata.GenresProvenance = api.FactProvenanceManualEmpty
	if got := resolveBTNTags(meta, nil); got != "" {
		t.Fatalf("manual-empty tags = %q", got)
	}
}

func TestBTNTitleSearchPrefersManualFacts(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Filename:          "Fallback File",
		Release:           api.ReleaseInfo{Title: "Parsed Title"},
		ProviderMetadata:  api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{NameEnglish: "Provider Title"}},
		EffectiveMetadata: api.EffectiveMetadata{Title: "Manual Title", TitleProvenance: api.FactProvenanceManual},
	}
	if got := resolveSearchName(meta); got != "Manual Title" {
		t.Fatalf("manual upload search = %q", got)
	}
	meta.EffectiveMetadata.Title = ""
	meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManualEmpty
	if got := resolveSearchName(meta); got != "" {
		t.Fatalf("manual-empty upload search = %q", got)
	}

	duplicate := api.DuplicateSubject{
		Filename:          "Fallback File",
		Release:           api.ReleaseInfo{Title: "Parsed Title"},
		ProviderMetadata:  api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{NameEnglish: "Provider Title"}},
		EffectiveMetadata: api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty},
	}
	if got := searchTitle(duplicate); got != "" {
		t.Fatalf("manual-empty duplicate search = %q", got)
	}
}
