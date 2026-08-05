// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseNamePolicyPreservesExactRenamedSceneName(t *testing.T) {
	t.Parallel()

	const exact = "Example Show [SCENE].S01E02-GRP"
	meta := api.UploadSubject{
		Scene:        true,
		SceneName:    exact,
		SceneRenamed: true,
		ReleaseName:  "Generated.Example.Show.S01E02-GRP",
		Release:      api.ReleaseInfo{Title: "Example Show"},
	}
	if got := resolveUploadName(meta); got != exact {
		t.Fatalf("NBL upload name = %q, want %q", got, exact)
	}
	if got := resolveSearchName(meta); got != exact {
		t.Fatalf("NBL search name = %q, want %q", got, exact)
	}
	if got := Profile().ReleaseNamePolicy.ID; got != "standalone/nbl/v2" {
		t.Fatalf("NBL release-name policy = %q, want standalone/nbl/v2", got)
	}
}
