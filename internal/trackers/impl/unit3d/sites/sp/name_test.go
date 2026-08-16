// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNamePreservesExactSceneNameBeforeEpisodeDefault(t *testing.T) {
	t.Parallel()
	const (
		exact     = "Example Show [SCENE].S01E02-GRP"
		generated = "Example.Show.S01E02.Example.Episode.1080p.WEB-DL.H.264-GRP"
	)
	got := buildName(api.UploadSubject{
		Scene:        true,
		SceneName:    exact,
		SceneRenamed: true,
		ReleaseName:  generated,
		EpisodeTitle: "Example Episode",
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: generated},
			OmitEpisodeTitle:    api.ReleaseNameVariant{Name: exact},
		},
	}, config.TrackerConfig{})
	if got != exact {
		t.Fatalf("SP exact scene name = %q, want %q", got, exact)
	}
}

func TestBuildNamePreservesExactSourceFolderName(t *testing.T) {
	t.Parallel()
	const exact = "Example.Release.2026.1080p.WEB-DL.H.264-GRP"
	if got := buildName(api.UploadSubject{SourcePath: "C:/media/" + exact}, config.TrackerConfig{}); got != exact {
		t.Fatalf("SP exact source folder name = %q, want %q", got, exact)
	}
}

func TestBuildNameUsesExactSourceFilenameWithoutExtension(t *testing.T) {
	t.Parallel()
	const exact = "Example.Release.2026.1080p.WEB-DL.H.264-GRP"
	if got := buildName(api.UploadSubject{Filename: exact + ".mkv"}, config.TrackerConfig{}); got != exact {
		t.Fatalf("SP source filename = %q, want %q", got, exact)
	}
}

func TestReleaseNamePolicyUsesConfirmedNonSceneName(t *testing.T) {
	t.Parallel()

	confirmed := "Example.Release.2026.CONFIRMED-GRP"
	resolved, err := Profile().ReleaseNamePolicy.Resolver(trackers.ReleaseNameInput{
		Subject:       api.UploadSubject{SourcePath: "C:/media/Example.Release.2026-GRP"},
		RequestedName: &confirmed,
	})
	if err != nil {
		t.Fatalf("resolve confirmed SP name: %v", err)
	}
	if resolved.Upload != confirmed {
		t.Fatalf("confirmed SP name = %q, want %q", resolved.Upload, confirmed)
	}
}

func TestBuildNameUsesGeneratedLookingSourceFilenameForConfirmation(t *testing.T) {
	t.Parallel()
	const generated = "Example.Show.S01E02.Example.Episode.1080p.WEB-DL.H.264-GRP"
	got := buildName(api.UploadSubject{
		SourcePath:  generated + ".mkv",
		ReleaseName: generated,
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: generated},
		},
	}, config.TrackerConfig{})
	if got != generated {
		t.Fatalf("SP source filename = %q, want %q", got, generated)
	}
}

func TestProfileBuildNameVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().Site.BuildNameVersion; got != "v4" {
		t.Fatalf("SP BuildNameVersion = %q, want v4", got)
	}
	if got := Profile().ReleaseNamePolicy.Confirmation; got != trackers.ReleaseNameConfirmationNonScene {
		t.Fatalf("SP release-name confirmation mode = %q", got)
	}
}
