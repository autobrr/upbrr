// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSkipReasonIgnoresWarnings(t *testing.T) {
	t.Parallel()
	meta := api.PreparedMetadata{TrackerRuleFailures: map[string][]api.RuleFailure{
		"PTP": {{Rule: "require_metadata_id", Reason: "IMDb recommended", Severity: api.RuleFailureSeverityWarning}},
	}}
	reason, rules := skipReason(meta, "PTP")
	if reason != "" || len(rules) != 0 {
		t.Fatalf("expected warning not to skip dupe checking, got reason=%q rules=%v", reason, rules)
	}
}
