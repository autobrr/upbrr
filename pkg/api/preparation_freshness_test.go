// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestExternalFreshnessRequiresRefresh(t *testing.T) {
	for _, freshness := range []ExternalFreshness{ExternalFreshnessReuse, ExternalFreshnessLoad, ExternalFreshnessRefresh} {
		if got := freshness.RequiresRefresh(); got != (freshness == ExternalFreshnessRefresh) {
			t.Errorf("freshness %q requires refresh = %t", freshness, got)
		}
	}
}
