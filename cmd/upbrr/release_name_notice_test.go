// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"io"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLIProjectionShowsMandatoryNamingWithoutVerbosePolicyDetails(t *testing.T) {
	for _, code := range []string{"release_name_override", "release_name_override_generated"} {
		t.Run(code, func(t *testing.T) {
			output := captureWriter(func(output io.Writer) {
				printCLIWorkflowProjections(output, &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
					DisplayName:       "Example",
					Readiness:         api.ReadinessStatusReady,
					UploadReleaseName: "Example 2026-GRP",
					PolicyDecisions: []api.TrackerPolicyDecision{{
						Code:         code,
						Decision:     "enforced",
						NamingRole:   "edition",
						NamingRuleID: "example/v1",
						Message:      "Tracker requires edition omission.",
					}},
				}}}, nil, false)
			})
			if !strings.Contains(output, "naming: Tracker requires edition omission.") {
				t.Fatalf("missing mandatory naming notice: %s", output)
			}
		})
	}
}

func TestCLIBlockedProjectionShowsMandatoryNamingWithoutVerbosePolicyDetails(t *testing.T) {
	output := captureWriter(func(output io.Writer) {
		printCLIWorkflowProjections(output, &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
			DisplayName: "Example",
			Readiness:   api.ReadinessStatusBlocked,
			PolicyDecisions: []api.TrackerPolicyDecision{
				{Code: "release_name_override", Message: "Tracker requires edition omission."},
				{
					Code:     "auth_required",
					Blocking: true,
					Message:  "Authentication required.",
				},
			},
		}}}, nil, false)
	})
	if !strings.Contains(output, "Example naming: Tracker requires edition omission.") {
		t.Fatalf("missing tracker-scoped mandatory naming notice: %s", output)
	}
	if !strings.Contains(output, "Blocked/ineligible: Example") || strings.Contains(output, "readiness=") || strings.Contains(output, "Authentication required.") {
		t.Fatalf("blocked projection should retain compact output: %s", output)
	}
}
