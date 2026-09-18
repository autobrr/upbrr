// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestNormalizeEpisodeTitleModeDefaultsToInclude(t *testing.T) {
	t.Parallel()

	if got := NormalizeEpisodeTitleMode(EpisodeTitleModeUnspecified); got != EpisodeTitleModeInclude {
		t.Fatalf("unspecified mode = %q, want include", got)
	}
	if got := NormalizeEpisodeTitleMode(" OMIT "); got != EpisodeTitleModeOmit {
		t.Fatalf("normalized omit mode = %q", got)
	}
}

func TestReleaseNameElementPolicyNormalizedPreservesVersion(t *testing.T) {
	t.Parallel()

	got := (ReleaseNameElementPolicy{
		Version: " release-name-elements/v1 ",
	}).Normalized()
	if got.Version != ReleaseNameElementPolicyVersionV1 || got.EpisodeTitleMode != EpisodeTitleModeInclude {
		t.Fatalf("normalized element policy = %#v", got)
	}
}
