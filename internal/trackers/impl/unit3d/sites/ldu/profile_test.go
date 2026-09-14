// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ldu

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLDUStructuredReleaseNamePolicyUsesLanguageRoles(t *testing.T) {
	t.Parallel()
	subject := lduGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Release",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		VideoCodec: "H.264",
		Tag:        "-GRP",
	})
	subject.AudioLanguages = []string{"", "Japanese", "English"}
	subject.SubtitleLanguages = []string{"", "English"}
	subject.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalLanguage: "ja"}
	if got, want := lduReviewedName(t, subject, nil), "Example Release 2026 1080p WEB-DL H.264-GRP [JPN] [Subs ENG]"; got != want {
		t.Fatalf("LDU name = %q, want %q", got, want)
	}

	disc := subject
	disc.DiscType = "BDMV"
	if got, want := lduReviewedName(t, disc, nil), subject.ReleaseName; got != want {
		t.Fatalf("disc name = %q, want %q", got, want)
	}
	override := "Opaque LDU Name-GRP"
	if got := lduReviewedName(t, subject, &override); got != override {
		t.Fatalf("opaque name = %q, want %q", got, override)
	}
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/ldu/v2" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("LDU policy = %#v", policy)
	}
}

func TestOriginalLanguagePrefersManualFacts(t *testing.T) {
	t.Parallel()
	meta := api.UploadSubject{
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"}},
		EffectiveMetadata: api.EffectiveMetadata{
			OriginalLanguage: "en", OriginalLanguageProvenance: api.FactProvenanceManual,
		},
	}
	if got := originalLanguage(meta); got != "en" {
		t.Fatalf("manual original language = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = ""
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManualEmpty
	if got := originalLanguage(meta); got != "" {
		t.Fatalf("manual-empty original language = %q", got)
	}
	meta.SourcePath = "current-source"
	meta.Identity.SourcePath = meta.SourcePath
	meta.ProviderMetadata.SourcePath = "stale-source"
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceAutomatic
	if got := originalLanguage(meta); got != "" {
		t.Fatalf("stale provider original language = %q", got)
	}
	meta.EffectiveMetadata.OriginalLanguage = "en"
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManual
	if got := originalLanguage(meta); got != "en" {
		t.Fatalf("manual original language over stale provider = %q", got)
	}
	meta.ProviderMetadata.SourcePath = meta.SourcePath
	meta.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceAutomatic
	if got := originalLanguage(meta); got != "ja" {
		t.Fatalf("current provider original language = %q", got)
	}
}

func TestLDUIgnoresStaleProviderOriginalLanguage(t *testing.T) {
	t.Parallel()
	subject := lduGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Provider Language",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		VideoCodec: "H.264",
		Tag:        "-GRP",
	})
	subject.SourcePath = "current-source"
	subject.Identity.SourcePath = subject.SourcePath
	subject.AudioLanguages = []string{"English"}
	subject.ProviderMetadata = api.SourceScopedMetadata{SourcePath: subject.SourcePath, TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"}}
	if got, want := lduReviewedName(t, subject, nil), "Provider Language 2026 1080p WEB-DL H.264-GRP [ENG]"; got != want {
		t.Fatalf("current provider language name = %q, want %q", got, want)
	}
	stale := subject
	stale.ProviderMetadata.SourcePath = "stale-source"
	if got, want := lduReviewedName(t, stale, nil), subject.ReleaseName; got != want {
		t.Fatalf("stale provider language name = %q, want %q", got, want)
	}
	stale.EffectiveMetadata.OriginalLanguage = "ja"
	stale.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManual
	if got, want := lduReviewedName(t, stale, nil), "Provider Language 2026 1080p WEB-DL H.264-GRP [ENG]"; got != want {
		t.Fatalf("manual language over stale provider = %q, want %q", got, want)
	}
}

func lduGeneratedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	result := metadata.BuildReleaseName(request, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		Release: api.ReleaseInfo{
			Category: request.Category,
			Title:    request.Title,
			Year:     request.Year,
		},
	}
}

func lduReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{
			Tracker:             "LDU",
			Meta:                subject,
			RequestedUploadName: requested,
		},
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
