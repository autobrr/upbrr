// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowPackCaptureUsesEachPTPMemberWithoutChangingOtherTrackerCapture(t *testing.T) {
	primary, secondary := "Movie.cd1.mkv", "Movie.cd2.mkv"
	subject := api.ScreenshotSubject{
		SourcePath:        "Example.Movie.2026",
		VideoPath:         primary,
		MediaInfoJSONPath: "primary.json",
		PackMembers: []api.ScreenshotPackMember{
			{ID: "first", Path: primary},
			{ID: "second", Path: secondary},
		},
	}
	for _, short := range []bool{false, true} {
		var planned []api.ScreenshotSubject
		capture := &workflowScreenshotFake{root: t.TempDir(), planFor: func(input api.ScreenshotSubject, count int) api.ScreenshotPlan {
			planned = append(planned, input)
			if input.PackMemberID != "" && (count != 2 || len(input.ManualFrames) != 0) {
				t.Errorf("pack capture should select two frames independently of manual release controls: %+v", input)
			}
			selections := []api.ScreenshotSelection{{Index: 1, TimestampSeconds: 60}}
			if input.PackMemberID != "" && (!short || input.PackMemberID != "second") {
				selections = append(selections, api.ScreenshotSelection{Index: 2, TimestampSeconds: 120})
			}
			return api.ScreenshotPlan{SuggestedSelections: selections}
		}}
		builder := workflowMediaBuilder{resolver: workflowMediaResolverFake{screenshotSubject: &subject}, screenshots: capture}
		snapshot, _, err := builder.Build(
			t.Context(),
			api.ReleaseRef{SourcePath: subject.SourcePath, Generation: 1},
			api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
				{TrackerID: "AITHER", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}},
				{TrackerID: "PTP", Artifacts: api.TrackerArtifactRequirements{
					PackScreenshots: &api.PackScreenshotRequirement{MemberIDs: []string{"first", "second"}, CountPerMember: 2},
				}},
			}},
			api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal},
			time.Now(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(planned) != 3 || planned[0].PackMemberID != "" || planned[1].VideoPath != primary ||
			planned[2].VideoPath != secondary || planned[2].MediaInfoJSONPath != "" {
			t.Fatalf("capture scope = %+v", planned)
		}
		counts := make(map[string]int)
		for _, artifact := range snapshot.Artifacts {
			counts[artifact.PackMemberID]++
		}
		if counts[""] != 1 {
			t.Fatalf("normal release screenshots = %d, want 1", counts[""])
		}
		if short {
			if len(snapshot.Artifacts) != 1 || len(snapshot.Failures) != 1 || snapshot.Failures[0].TrackerID != "PTP" {
				t.Fatalf("short second file changed unrelated screenshots or lost PTP failure: %+v", snapshot)
			}
		} else if counts["first"] != 2 || counts["second"] != 2 || len(snapshot.Failures) != 0 {
			t.Fatalf("pack captures = %+v, failures = %+v", counts, snapshot.Failures)
		}
	}
}
