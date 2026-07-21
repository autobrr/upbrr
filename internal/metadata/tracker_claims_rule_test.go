// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerClaimRuleFailureIsStrict(t *testing.T) {
	t.Parallel()

	failure := trackerClaimRuleFailure("active claim")
	if failure.Rule != trackerClaimRuleActive || failure.Disposition != api.RuleDispositionStrict {
		t.Fatalf("claim failure = %#v", failure)
	}
}
