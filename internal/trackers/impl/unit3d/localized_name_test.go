// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLocalizedReleaseNamePolicyUsesGeneratedComponents(t *testing.T) {
	t.Parallel()
	subject := localizedGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Release",
		AltTitle:   "AKA Example Alternate",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "DD+ 5.1",
		VideoCodec: "H.264",
		Tag:        "-GRP",
	})
	subject.AudioLanguages = []string{"English", "Portuguese"}
	if got, want := localizedReviewedName(t, subject, config.TrackerConfig{}, nil), "Example Release 2026 1080p WEB-DL DDP5.1 H.264 DUAL-GRP"; got != want {
		t.Fatalf("localized name = %q, want %q", got, want)
	}
	multi := subject
	multi.AudioLanguages = []string{"English", "Portuguese", "French", "Japanese"}
	if got, want := localizedReviewedName(t, multi, config.TrackerConfig{}, nil), "Example Release 2026 1080p WEB-DL DDP5.1 H.264 MULTI-GRP"; got != want {
		t.Fatalf("multi-audio localized name = %q, want %q", got, want)
	}
	portuguese := localizedGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Foreign Movie",
		AltTitle:   "AKA Filme Brasileiro",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		VideoCodec: "H.264",
		Tag:        "-GRP",
	})
	portuguese.SourcePath = "current-source"
	portuguese.Identity.SourcePath = portuguese.SourcePath
	portuguese.ProviderMetadata = api.SourceScopedMetadata{SourcePath: portuguese.SourcePath, TMDB: &api.TMDBMetadata{OriginalLanguage: "pt"}}
	if got, want := localizedReviewedName(t, portuguese, config.TrackerConfig{}, nil), "Filme Brasileiro 2026 1080p WEB-DL H.264-GRP"; got != want {
		t.Fatalf("Portuguese title role = %q, want %q", got, want)
	}
	stalePortuguese := portuguese
	stalePortuguese.SourcePath = "current-source"
	stalePortuguese.Identity.SourcePath = stalePortuguese.SourcePath
	stalePortuguese.ProviderMetadata.SourcePath = "stale-source"
	if got, want := localizedReviewedName(t, stalePortuguese, config.TrackerConfig{}, nil), "Foreign Movie 2026 1080p WEB-DL H.264-GRP"; got != want {
		t.Fatalf("stale Portuguese provider title = %q, want %q", got, want)
	}
	stalePortuguese.EffectiveMetadata.OriginalLanguage = "pt"
	stalePortuguese.EffectiveMetadata.OriginalLanguageProvenance = api.FactProvenanceManual
	if got, want := localizedReviewedName(t, stalePortuguese, config.TrackerConfig{}, nil), "Filme Brasileiro 2026 1080p WEB-DL H.264-GRP"; got != want {
		t.Fatalf("manual Portuguese title over stale provider = %q, want %q", got, want)
	}

	custom := localizedGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Release",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		VideoCodec: "H.264",
		Tag:        "-CBR",
	})
	custom.Release.Group = "ORIG"
	custom.AudioLanguages = []string{"English", "Portuguese"}
	if got, want := localizedReviewedName(t, custom, config.TrackerConfig{TagForCustomRelease: "-CBR"}, nil), "Example Release 2026 1080p WEB-DL H.264-ORIG DUAL-CBR"; got != want {
		t.Fatalf("custom localized name = %q, want %q", got, want)
	}
}

func TestLocalizedReleaseNamePolicyPreservesManualAndOpaqueNames(t *testing.T) {
	t.Parallel()
	subject := localizedGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Japanese Year Show",
		Year:       2026,
		Season:     "S01",
		Episode:    "E02",
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	subject.Identity.Category = api.CanonicalCategoryTV
	markLocalizedManual(t, subject.GeneratedName, api.NameRoleYear)
	subject.ReleaseName = subject.GeneratedName.Render().Name
	if got, want := localizedReviewedName(t, subject, config.TrackerConfig{}, nil), subject.ReleaseName; got != want {
		t.Fatalf("manual year changed: %q, want %q", got, want)
	}
	override := "Opaque Localized Name-GRP"
	if got := localizedReviewedName(t, subject, config.TrackerConfig{}, &override); got != override {
		t.Fatalf("opaque name = %q, want %q", got, override)
	}
}

func TestLocalizedReleaseNamePolicyDoesNotBypassManualAudioMarkers(t *testing.T) {
	t.Parallel()
	manual := localizedGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Manual Markers",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "Dubbed Dual-Audio AAC",
		Tag:        "-GRP",
	})
	manual.AudioLanguages = []string{"English", "Portuguese"}
	markLocalizedManual(t, manual.GeneratedName, api.NameRoleDubbed)
	markLocalizedManual(t, manual.GeneratedName, api.NameRoleDualAudio)
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got, want := localizedReviewedName(t, manual, config.TrackerConfig{}, nil), manual.ReleaseName; got != want {
		t.Fatalf("manual audio markers changed or duplicated: %q, want %q", got, want)
	}

	noDual := localizedGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "No Dual",
		Year:       2026,
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "AAC",
		Tag:        "-GRP",
	})
	noDual.AudioLanguages = []string{"English", "Portuguese"}
	markLocalizedManual(t, noDual.GeneratedName, api.NameRoleDualAudio)
	noDual.ReleaseName = noDual.GeneratedName.Render().Name
	if got, want := localizedReviewedName(t, noDual, config.TrackerConfig{}, nil), noDual.ReleaseName; got != want {
		t.Fatalf("manual no-dual choice was bypassed: %q, want %q", got, want)
	}
}

func localizedGeneratedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
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
			Group:    request.Tag,
		},
	}
}

func localizedReviewedName(t *testing.T, subject api.UploadSubject, cfg config.TrackerConfig, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{
			Tracker:             "Localized",
			Meta:                subject,
			TrackerConfig:       cfg,
			RequestedUploadName: requested,
		},
		LocalizedReleaseNamePolicy("unit3d/localized/test/v2"),
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

func markLocalizedManual(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole) {
	t.Helper()
	for index := range document.Components {
		if document.Components[index].Role == role {
			document.Components[index].Manual = true
			return
		}
	}
	t.Fatalf("generated document missing %s", role)
}
