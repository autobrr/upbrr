// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import "testing"

func TestTrackResourcePreservesLexicalIdentity(t *testing.T) {
	if TrackResourceID(" source ") != "media_41cf6794ba4200b839c53531" {
		t.Fatal("existing resource hash changed")
	}
	if TrackResourceID(`E:\Media\Film.mkv`) == TrackResourceID(`e:\media\film.mkv`) {
		t.Fatal("path was case folded")
	}
}
