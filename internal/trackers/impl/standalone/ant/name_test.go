// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveUploadNameUsesNormalNameForSceneRelease(t *testing.T) {
	t.Parallel()

	const normal = "Example.Release.2026-GRP"
	got := resolveUploadName(api.UploadSubject{
		Scene:        true,
		SceneName:    "Example Release [SCENE].2026-GRP",
		SceneRenamed: true,
		ReleaseName:  normal,
	})
	if got != normal {
		t.Fatalf("ANT release name = %q, want %q", got, normal)
	}
	if got := Profile().ReleaseNamePolicy.ID; got != "standalone/ant/v2" {
		t.Fatalf("ANT release-name policy = %q, want standalone/ant/v2", got)
	}
}
