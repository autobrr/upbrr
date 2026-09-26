// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

// TestSAMUsesLocalizedStructuredNamePolicy verifies SAM-specific structured naming.
func TestSAMUsesLocalizedStructuredNamePolicy(t *testing.T) {
	t.Parallel()
	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/sam/v3" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("SAM policy = %#v", policy)
	}
}
