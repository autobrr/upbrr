// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestARReleaseNamePolicyPrefersProviderTitleForDuplicateSearch(t *testing.T) {
	t.Parallel()

	policy := Profile().ReleaseNamePolicy
	if policy.ID != "standalone/ar/v4" {
		t.Fatalf("release name policy ID = %q", policy.ID)
	}
	if policy.Confirmation != trackers.ReleaseNameConfirmationNonScene {
		t.Fatalf("release name confirmation mode = %q", policy.Confirmation)
	}

	resolved, err := policy.Resolver(trackers.ReleaseNameInput{
		Subject: api.UploadSubject{
			SourcePath:  "Example.Release.2026.NTSC.COMPLETE-GRP",
			ReleaseName: "Example.Release.2026.NTSC.COMPLETE-GRP",
			Release:     api.ReleaseInfo{Title: "EXAMPLE DISC EDITION", Year: 2026},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{Title: "Example Release", Year: 2026},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolve release names: %v", err)
	}
	if resolved.Duplicate != "Example Release 2026" {
		t.Fatalf("duplicate search name = %q", resolved.Duplicate)
	}
}

func TestARReleaseNamePolicyPreservesSceneNameOneToOne(t *testing.T) {
	t.Parallel()

	const exact = "Example Release [SCENE].2026-GRP"
	resolved, err := Profile().ReleaseNamePolicy.Resolver(trackers.ReleaseNameInput{
		Subject: api.UploadSubject{
			Scene:        true,
			SceneName:    exact,
			SceneRenamed: true,
			SourcePath:   "different.generated.name.mkv",
		},
	})
	if err != nil {
		t.Fatalf("resolve scene release name: %v", err)
	}
	if resolved.Upload != exact {
		t.Fatalf("scene upload name = %q, want %q", resolved.Upload, exact)
	}
}

func TestARReleaseNamePolicyUsesConfirmedNonSceneName(t *testing.T) {
	t.Parallel()

	confirmed := "Example.Release.2026.CONFIRMED-GRP"
	resolved, err := Profile().ReleaseNamePolicy.Resolver(trackers.ReleaseNameInput{
		Subject: api.UploadSubject{
			SourcePath: "C:/media/Example.Release.2026-GRP",
			Release:    api.ReleaseInfo{Title: "Example Release", Year: 2026},
		},
		RequestedName: &confirmed,
	})
	if err != nil {
		t.Fatalf("resolve confirmed AR name: %v", err)
	}
	if resolved.Upload != confirmed || resolved.Duplicate != "Example Release 2026" {
		t.Fatalf("confirmed AR names = %#v", resolved)
	}
}

func TestResolveARSearchNameUsesTVDBWhenOnlyTVDBMetadataExists(t *testing.T) {
	t.Parallel()

	got := resolveARSearchName(api.UploadSubject{
		Release: api.ReleaseInfo{Title: "EXAMPLE DISC LABEL"},
		ProviderMetadata: api.SourceScopedMetadata{
			TVDB: &api.TVDBMetadata{NameEnglish: "Example Series", Year: 2024},
		},
	})
	if got != "Example Series 2024" {
		t.Fatalf("duplicate search name = %q", got)
	}
}
