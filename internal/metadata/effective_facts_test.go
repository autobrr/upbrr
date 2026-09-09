// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRebuildReleaseNameUsesManualMetadataFacts(t *testing.T) {
	t.Parallel()

	meta := preparationstate.State{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		Release: api.ReleaseInfo{
			Title:  "Automatic Title",
			Genre:  "Drama",
			Source: "Web",
			Type:   "WEBDL",
		},
		MetadataOverrides: api.MetadataOverrides{
			Title:            new("Manual Title"),
			AlternateTitle:   new(""),
			OriginalTitle:    new("Manual Original"),
			Genres:           &[]string{"Drama", " drama ", "Mystery"},
			OriginalLanguage: new("fra"),
		},
	}

	RebuildReleaseName(&meta, api.NopLogger{})
	if meta.ResolvedNaming.Title != "Manual Title" || meta.ResolvedNaming.AlternateTitle != "" || meta.ResolvedNaming.OriginalTitle != "Manual Original" {
		t.Fatalf("resolved naming = %#v", meta.ResolvedNaming)
	}
	if !slices.Equal(meta.EffectiveMetadata.Genres, []string{"Drama", "Mystery"}) || meta.EffectiveMetadata.OriginalLanguage != "French" ||
		meta.EffectiveMetadata.AlternateTitleProvenance != api.FactProvenanceManualEmpty {
		t.Fatalf("effective metadata = %#v", meta.EffectiveMetadata)
	}
	if originalAudioLanguage(meta) != "fra" {
		t.Fatalf("original language = %q", originalAudioLanguage(meta))
	}
}

func TestRebuildReleaseNameManualYearOverridesTVDBAliasYear(t *testing.T) {
	t.Parallel()

	meta := preparationstate.State{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV, TVDBID: 123},
		Release:  api.ReleaseInfo{Title: "Parsed Show", Resolution: "1080p"},
		ProviderMetadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{
			TVDBID:        123,
			NameEnglish:   "Provider Show",
			Year:          2024,
			YearFromAlias: true,
		}},
		ReleaseNameOverrides: api.ReleaseNameOverrides{ManualYear: new(2030)},
	}
	RebuildReleaseName(&meta, api.NopLogger{})
	if meta.ResolvedNaming.Year != 2030 || meta.EffectiveMetadata.Year != 2030 || meta.EffectiveMetadata.YearProvenance != api.FactProvenanceManual {
		t.Fatalf("manual year facts = naming=%d effective=%#v", meta.ResolvedNaming.Year, meta.EffectiveMetadata)
	}

	meta.ReleaseNameOverrides.ManualYear = nil
	RebuildReleaseName(&meta, api.NopLogger{})
	if meta.ResolvedNaming.Year != 2024 || meta.EffectiveMetadata.Year != 2024 || meta.EffectiveMetadata.YearProvenance != api.FactProvenanceAutomatic {
		t.Fatalf("automatic TVDB alias year facts = naming=%d effective=%#v", meta.ResolvedNaming.Year, meta.EffectiveMetadata)
	}
}
