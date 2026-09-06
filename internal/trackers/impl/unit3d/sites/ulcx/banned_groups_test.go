// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ulcx

import (
	"slices"
	"testing"
)

func TestBannedGroupsIncludesLatestULCXAdditions(t *testing.T) {
	t.Parallel()

	groups := BannedGroups()
	for _, group := range []string{"BiTOR", "Flights", "PiRaTeS", "R&H"} {
		if !slices.Contains(groups, group) {
			t.Fatalf("missing banned group %q", group)
		}
	}
}
