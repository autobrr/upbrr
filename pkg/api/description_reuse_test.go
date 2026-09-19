// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReusableDescriptionValid(t *testing.T) {
	t.Parallel()

	reusable := ReusableDescription{
		CompatibilityFingerprint: WorkflowFingerprint(strings.Repeat("a", 64)),
		Descriptions: []RenderedDescription{{
			GroupKey:           "main",
			TrackerIDs:         []TrackerID{"AITHER"},
			Source:             "source",
			Rendered:           "rendered",
			ContentFingerprint: WorkflowFingerprint(strings.Repeat("b", 64)),
		}},
		TrackerResults: []DescriptionTrackerResult{{TrackerID: "AITHER", Status: StageStatusCompleted}},
		Overrides:      []DescriptionOverrideInput{{GroupKey: "main", Source: "edited source"}},
	}
	if !reusable.Valid() {
		t.Fatal("valid reusable description rejected")
	}

	reusable.Overrides = append(reusable.Overrides, DescriptionOverrideInput{GroupKey: "MAIN", Source: "duplicate"})
	if reusable.Valid() {
		t.Fatal("duplicate override groups accepted")
	}
}

func TestReusableDescriptionRecordCloneAndWorkflowStateJSON(t *testing.T) {
	t.Parallel()
	record := ReusableDescriptionRecord{
		SourcePath: "C:\\releases\\Example.Release.2026.mkv",
		Description: ReusableDescription{
			CompatibilityFingerprint: WorkflowFingerprint(strings.Repeat("a", 64)),
			Descriptions: []RenderedDescription{{
				GroupKey:           "main",
				TrackerIDs:         []TrackerID{"AITHER"},
				Source:             "source",
				Rendered:           "rendered",
				ContentFingerprint: WorkflowFingerprint(strings.Repeat("b", 64)),
			}},
		},
	}
	cloned := record.Clone()
	cloned.Description.Descriptions[0].TrackerIDs[0] = "BLU"
	if record.Description.Descriptions[0].TrackerIDs[0] != "AITHER" {
		t.Fatal("clone mutated reusable description source")
	}
	payload, err := json.Marshal(ReleaseWorkflowStateRecord{DescriptionReuse: &record})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), record.SourcePath) || strings.Contains(string(payload), "rendered") {
		t.Fatalf("workflow state JSON exposed reusable description: %s", payload)
	}
}
