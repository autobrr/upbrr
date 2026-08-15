// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseNamePolicyUsesNormalNamesForSceneRelease(t *testing.T) {
	t.Parallel()

	const normal = "Example.Show.S01E02-GRP"
	meta := api.UploadSubject{
		Scene:        true,
		SceneName:    "Example Show [SCENE].S01E02-GRP",
		SceneRenamed: true,
		ReleaseName:  normal,
		Release:      api.ReleaseInfo{Title: "Example Show"},
	}
	if got := resolveUploadName(meta); got != normal {
		t.Fatalf("NBL upload name = %q, want %q", got, normal)
	}
	if got := resolveSearchName(meta); got != "Example Show" {
		t.Fatalf("NBL search name = %q, want %q", got, "Example Show")
	}
	if got := Profile().ReleaseNamePolicy.ID; got != "standalone/nbl/v3" {
		t.Fatalf("NBL release-name policy = %q, want standalone/nbl/v3", got)
	}
}
