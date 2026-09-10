// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCinemaZTitlePrefersManualOriginalTitle(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		ProviderMetadata: api.SourceScopedMetadata{IMDB: &api.IMDBMetadata{
			Akas: []api.IMDBAKA{{
				Title:    "Provider English",
				Country:  "US",
				Language: "en",
			}},
		}},
		EffectiveMetadata: api.EffectiveMetadata{
			OriginalTitle: "Manual Original", OriginalTitleProvenance: api.FactProvenanceManual,
		},
	}
	if got := cinemaZTitle(meta); got != "Manual Original" {
		t.Fatalf("manual original title = %q", got)
	}
	meta.EffectiveMetadata.OriginalTitle = ""
	meta.EffectiveMetadata.OriginalTitleProvenance = api.FactProvenanceManualEmpty
	if got := cinemaZTitle(meta); got != "" {
		t.Fatalf("manual-empty original title = %q", got)
	}
}

func TestResolveSearchNameDoesNotRestoreProviderAfterManualClear(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		Filename: "Fallback File",
		Release:  api.ReleaseInfo{Title: "Parsed Title"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title: "Provider Title",
		}},
		EffectiveMetadata: api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty},
	}
	if got := resolveSearchName(meta); got != "" {
		t.Fatalf("manual-empty search name = %q", got)
	}
	if got := lookupTitle(meta); got != "" {
		t.Fatalf("manual-empty lookup title = %q", got)
	}
}

func TestLookupTitlePreservesAutomaticCanonicalPrecedence(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Filename: "Fallback File",
		Release:  api.ReleaseInfo{Title: "Canonical Title"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title: "Provider Title",
		}},
	}
	if got := lookupTitle(meta); got != "Canonical Title" {
		t.Fatalf("canonical lookup title = %q", got)
	}

	meta.Release.Title = ""
	if got := lookupTitle(meta); got != "Provider Title" {
		t.Fatalf("provider lookup title = %q", got)
	}

	meta.EffectiveMetadata = api.EffectiveMetadata{Title: "Manual Title", TitleProvenance: api.FactProvenanceManual}
	if got := lookupTitle(meta); got != "Manual Title" {
		t.Fatalf("manual lookup title = %q", got)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{TitleProvenance: api.FactProvenanceManualEmpty}
	if got := lookupTitle(meta); got != "" {
		t.Fatalf("manual-empty lookup title = %q", got)
	}
}
