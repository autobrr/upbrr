// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cbr

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestCBRUsesLocalizedStructuredNamePolicy verifies CBR binds its dedicated policy version.
func TestCBRUsesLocalizedStructuredNamePolicy(t *testing.T) {
	t.Parallel()
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/cbr/v3" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("CBR policy = %#v", policy)
	}
}

// TestCBRNamePolicyRemovesAKAAndUsesTVDBOnlyForTitleCollisions covers the reported CBR release shape.
func TestCBRNamePolicyRemovesAKAAndUsesTVDBOnlyForTitleCollisions(t *testing.T) {
	t.Parallel()
	base := cbrSubject(t)
	base.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: base.SourcePath,
		TVDB: &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
			CanonicalName: "Scissor Seven",
			SeriesYear:    2023,
			IncludeYear:   true,
			IncludeLocale: true,
			Locale:        "CN",
		}},
	}
	if got, want := cbrReviewedName(t, base), "Scissor Seven CN 2023 S01 1080p WEB-DL DDP2.0 H.264-Mys"; got != want {
		t.Fatalf("CBR collision name = %q, want %q", got, want)
	}

	nonColliding := base
	nonColliding.GeneratedName = base.GeneratedName.Clone()
	nonColliding.ProviderMetadata.TVDB.NameDisambiguation.IncludeYear = false
	nonColliding.ProviderMetadata.TVDB.NameDisambiguation.IncludeLocale = false
	nonColliding.ProviderMetadata.TVDB.NameDisambiguation.Locale = ""
	nonColliding.ReleaseName = nonColliding.GeneratedName.Render().Name
	if got, want := cbrReviewedName(t, nonColliding), "Scissor Seven S01 1080p WEB-DL DDP2.0 H.264-Mys"; got != want {
		t.Fatalf("CBR non-collision name = %q, want %q", got, want)
	}
}

// cbrSubject builds the generated Scissor Seven release used by CBR policy tests.
func cbrSubject(t *testing.T) api.UploadSubject {
	t.Helper()
	result := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Scissor Seven",
		AltTitle:   "AKA Cike Wu Liuqi",
		Year:       2023,
		Season:     "S01",
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "DD+ 2.0",
		VideoCodec: "H.264",
		Tag:        "-Mys",
		SearchYear: "2023",
	}, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		SourcePath:       "scissor-seven",
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryTV, SourcePath: "scissor-seven"},
		Release: api.ReleaseInfo{
			Category: "TV",
			Title:    "Scissor Seven",
			Year:     2023,
		},
	}
}

// cbrReviewedName renders a subject through CBR's reviewed-name policy.
func cbrReviewedName(t *testing.T, subject api.UploadSubject) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{Tracker: "CBR", Meta: subject},
		unit3d.NewWithProfile(Profile()).ReleaseNamePolicy(),
	)
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}
