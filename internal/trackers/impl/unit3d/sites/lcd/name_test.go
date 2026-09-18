// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lcd

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

func TestLCDUsesLocalizedStructuredNamePolicy(t *testing.T) {
	t.Parallel()
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/lcd/v2" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("LCD policy = %#v", policy)
	}
}
