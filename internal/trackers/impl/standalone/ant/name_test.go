// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveUploadNamePreservesExactRenamedSceneName(t *testing.T) {
	t.Parallel()

	const exact = "Example Release [SCENE].2026-GRP"
	got := resolveUploadName(api.UploadSubject{
		Scene:        true,
		SceneName:    exact,
		SceneRenamed: true,
		ReleaseName:  "Generated.Example.Release.2026-GRP",
	})
	if got != exact {
		t.Fatalf("ANT scene name = %q, want %q", got, exact)
	}
	if got := Profile().ReleaseNamePolicy.ID; got != "standalone/ant/v1" {
		t.Fatalf("ANT release-name policy = %q, want standalone/ant/v1", got)
	}
}
