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
		Naming: NamingFacts{Codecs: []string{"H.264"}},
		ProviderMetadata: SourceScopedMetadata{
			TMDB: &TMDBMetadata{LocalizedTitles: map[string]string{"en": "Example Release 2026"}},
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
	cloned.ProviderMetadata.TMDB.LocalizedTitles["en"] = "changed"
	cloned.Assessments.Naming.Missing[0] = "changed"

	if source.Source.Entries[0].Path == cloned.Source.Entries[0].Path {
		t.Fatal("source entries share storage")
	}
	if source.Naming.Codecs[0] == cloned.Naming.Codecs[0] {
		t.Fatal("naming codecs share storage")
	}
	if source.ProviderMetadata.TMDB.LocalizedTitles["en"] == cloned.ProviderMetadata.TMDB.LocalizedTitles["en"] {
		t.Fatal("provider metadata shares storage")
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
