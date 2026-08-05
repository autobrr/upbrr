// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"testing"
)

func TestPreparedReleaseCloneDetachesCollections(t *testing.T) {
	source := PreparedRelease{
		Source: SourceManifest{
			SourcePath: "Example.Release.2026.1080p-GRP.mkv",
			Entries: []SourceManifestEntry{
				{
					Path: "Example.Release.2026.1080p-GRP.mkv",
					Type: SourceEntryTypeFile,
					Size: 42,
				},
			},
		},
		Naming: NamingFacts{
			Codecs: []string{"H.264"},
			GeneratedReleaseNames: GeneratedReleaseNameVariants{
				IncludeEpisodeTitle: ReleaseNameVariant{
					Name: "Example.Show.S01E02.Example.Episode.1080p.WEB-DL-GRP",
				},
				OmitEpisodeTitle: ReleaseNameVariant{
					Name: "Example.Show.S01E02.1080p.WEB-DL-GRP",
				},
			},
		},
		ProviderMetadata: SourceScopedMetadata{
			TMDB: &TMDBMetadata{LocalizedTitles: map[string]string{"en": "Example Release 2026"}},
			TVDB: &TVDBMetadata{
				TVDBID: 987650001,
				Name:   "Example Series",
				NameDisambiguation: TVDBNameDisambiguation{
					CanonicalName: "Example Series",
					SeriesYear:    2026,
					Status:        MetadataEvidenceStatusPartial,
					Source:        "tvdb_v4_search_unpaged",
				},
			},
			ProviderAvailability: []ProviderAvailabilityEvidence{{
				Provider: IdentityProviderTMDB,
				Status:   ProviderAvailabilityStatusNotFound,
				Source:   "tmdb_find/v1",
			}},
		},
		Assessments: ReleaseAssessments{
			Naming: NamingAssessment{Status: NamingStatusIncomplete, Missing: []NamingRequirement{"year"}},
		},
	}

	cloned, err := source.Clone()
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	cloned.Source.Entries[0].Path = "changed.mkv"
	cloned.Naming.Codecs[0] = "changed"
	cloned.Naming.GeneratedReleaseNames.IncludeEpisodeTitle.Name = "changed"
	cloned.ProviderMetadata.TMDB.LocalizedTitles["en"] = "changed"
	cloned.ProviderMetadata.TVDB.NameDisambiguation.CanonicalName = "changed"
	cloned.ProviderMetadata.ProviderAvailability[0].Source = "changed"
	cloned.Assessments.Naming.Missing[0] = "changed"

	if source.Source.Entries[0].Path == cloned.Source.Entries[0].Path {
		t.Fatal("source entries share storage")
	}
	if source.Naming.Codecs[0] == cloned.Naming.Codecs[0] {
		t.Fatal("naming codecs share storage")
	}
	if source.Naming.GeneratedReleaseNames.IncludeEpisodeTitle.Name == cloned.Naming.GeneratedReleaseNames.IncludeEpisodeTitle.Name {
		t.Fatal("generated release-name variants were not cloned")
	}
	if source.ProviderMetadata.TMDB.LocalizedTitles["en"] == cloned.ProviderMetadata.TMDB.LocalizedTitles["en"] {
		t.Fatal("provider metadata shares storage")
	}
	if source.ProviderMetadata.TVDB.NameDisambiguation.CanonicalName == cloned.ProviderMetadata.TVDB.NameDisambiguation.CanonicalName {
		t.Fatal("TVDB disambiguation shares storage")
	}
	if source.ProviderMetadata.ProviderAvailability[0].Source == cloned.ProviderMetadata.ProviderAvailability[0].Source {
		t.Fatal("provider availability shares storage")
	}
	if source.Assessments.Naming.Missing[0] == cloned.Assessments.Naming.Missing[0] {
		t.Fatal("naming assessment shares storage")
	}
}

func TestExternalIdentityRequirementsUseCanonicalFieldsOnly(t *testing.T) {
	identity := ExternalIdentity{
		TMDBID:   123456,
		Category: CanonicalCategoryMovie,
	}

	id, err := identity.RequireProviderID(IdentityProviderTMDB)
	if err != nil || id != 123456 {
		t.Fatalf("TMDB requirement = (%d, %v)", id, err)
	}
	category, err := identity.RequireCategory()
	if err != nil || category != CanonicalCategoryMovie {
		t.Fatalf("category requirement = (%q, %v)", category, err)
	}

	_, err = identity.RequireProviderID(IdentityProviderIMDB)
	assertMissingRequirement(t, err, RequirementKindProviderID, IdentityProviderIMDB)
	_, err = (ExternalIdentity{Category: CanonicalCategoryUnknown}).RequireCategory()
	assertMissingRequirement(t, err, RequirementKindCategory, "")
	_, err = (ExternalIdentity{Category: "documentary"}).RequireCategory()
	assertMissingRequirement(t, err, RequirementKindCategory, "")
}

func TestNormalizeCanonicalCategory(t *testing.T) {
	tests := map[string]CanonicalCategory{
		"":        CanonicalCategoryUnknown,
		"unknown": CanonicalCategoryUnknown,
		"MOVIE":   CanonicalCategoryMovie,
		"film":    CanonicalCategoryMovie,
		"TV":      CanonicalCategoryTV,
		"episode": CanonicalCategoryTV,
	}
	for input, want := range tests {
		got, err := NormalizeCanonicalCategory(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeCanonicalCategory(%q) = (%q, %v), want %q", input, got, err, want)
		}
	}
	if _, err := NormalizeCanonicalCategory("unsupported"); err == nil {
		t.Fatal("unsupported category succeeded")
	}
}

func TestSourceScopedMetadataIsCurrentFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		metadata   SourceScopedMetadata
		sourcePath string
		identity   ExternalIdentity
		want       bool
	}{
		{
			name:       "matching source and generation",
			metadata:   SourceScopedMetadata{SourcePath: `C:\media\Example.Release.2026.mkv`, Generation: 3},
			sourcePath: `c:\MEDIA\Example.Release.2026.mkv`,
			identity:   ExternalIdentity{SourcePath: `C:\media\Example.Release.2026.mkv`, Generation: 3},
			want:       true,
		},
		{
			name:       "legacy unscoped metadata",
			sourcePath: `C:\media\Example.Release.2026.mkv`,
			want:       true,
		},
		{
			name:       "stale metadata source",
			metadata:   SourceScopedMetadata{SourcePath: `C:\media\Old.Release.mkv`, Generation: 3},
			sourcePath: `C:\media\Example.Release.2026.mkv`,
			identity:   ExternalIdentity{SourcePath: `C:\media\Example.Release.2026.mkv`, Generation: 3},
		},
		{
			name:       "stale identity source",
			metadata:   SourceScopedMetadata{SourcePath: `C:\media\Example.Release.2026.mkv`, Generation: 3},
			sourcePath: `C:\media\Example.Release.2026.mkv`,
			identity:   ExternalIdentity{SourcePath: `C:\media\Old.Release.mkv`, Generation: 3},
		},
		{
			name:       "stale generation",
			metadata:   SourceScopedMetadata{SourcePath: `C:\media\Example.Release.2026.mkv`, Generation: 2},
			sourcePath: `C:\media\Example.Release.2026.mkv`,
			identity:   ExternalIdentity{SourcePath: `C:\media\Example.Release.2026.mkv`, Generation: 3},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.metadata.IsCurrentFor(test.sourcePath, test.identity); got != test.want {
				t.Fatalf("IsCurrentFor() = %t, want %t", got, test.want)
			}
		})
	}
}

func assertMissingRequirement(t *testing.T, err error, requirement RequirementKind, provider IdentityProvider) {
	t.Helper()
	var missing *MissingRequirementError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want MissingRequirementError", err)
	}
	if missing.Requirement != requirement || missing.Provider != provider {
		t.Fatalf("missing requirement = %#v", missing)
	}
}
