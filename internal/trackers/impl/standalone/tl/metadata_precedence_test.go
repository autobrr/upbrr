// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveCategoryPrefersManualOriginalLanguage(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "en"}},
		EffectiveMetadata: api.EffectiveMetadata{
			OriginalLanguage: "fr", OriginalLanguageProvenance: api.FactProvenanceManual,
		},
	}
	if got := resolveCategory(meta); got != "36" {
		t.Fatalf("manual foreign category = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = ""
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
	meta.ProviderMetadata.TMDB.OriginalLanguage = "fr"
	if got := resolveCategory(meta); got != "32" {
		t.Fatalf("manual-empty category = %q", got)
	}
}

func TestResolveCategoryAcceptsManualCanonicalEnglish(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}, EffectiveMetadata: api.EffectiveMetadata{OriginalLanguage: "English", OriginalLanguageProvenance: api.FactProvenanceManual}}
	if got := resolveCategory(meta); got != "32" {
		t.Fatalf("manual English category = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = "en"
	if got := resolveCategory(meta); got != "32" {
		t.Fatalf("manual ISO English category = %q", got)
	}
}
