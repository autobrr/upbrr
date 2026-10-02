// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowMediaIncrementalCapturePreservesGeneratedArtifacts(t *testing.T) {
	for _, name := range []string{"automatic first", "manual first"} {
		t.Run(name, func(t *testing.T) {
			manualFirst := name == "manual first"
			root := t.TempDir()
			plan := &api.ScreenshotPlan{
				Discs: []api.ScreenshotDiscPlan{{DiscID: "source"}},
				SuggestedSelections: []api.ScreenshotSelection{
					{
						DiscID:           "source",
						Index:            0,
						TimestampSeconds: 60,
					},
					{
						DiscID:           "source",
						Index:            1,
						TimestampSeconds: 120,
					},
				},
			}
			screenshots := &workflowScreenshotFake{root: root, plan: plan}
			builder := workflowMediaBuilder{resolver: workflowMediaResolverFake{}, screenshots: screenshots}
			release := api.ReleaseRef{SourcePath: filepath.Join(root, "example.mkv"), Generation: 1}
			projections := api.TrackerReleaseProjectionSet{
				ID:          "projections",
				Revision:    1,
				Projections: []api.TrackerReleaseProjection{{TrackerID: "ALPHA", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 2}}},
			}
			automatic := api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal, ScreenshotCount: 2}
			manual := api.MediaCaptureInstructions{
				Purpose:         api.ScreenshotPurposeFinal,
				ScreenshotCount: 1,
				Selections: []api.ScreenshotSelection{{
					DiscID:           "source",
					Index:            2,
					TimestampSeconds: 180,
					Source:           "manual",
				}},
			}
			firstRequest, secondRequest := automatic, manual
			if manualFirst {
				firstRequest, secondRequest = manual, automatic
			}
			first, private, err := builder.BuildIncremental(t.Context(), release, projections, firstRequest, nil, nil, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			first.Artifacts[0].Selected = false
			first.Artifacts[0].Order = 7
			retained, ok := private.(workflowMediaPrivateArtifacts)
			if !ok {
				t.Fatalf("retained resource = %T", private)
			}
			// A real plan includes generated baseline files, but not an extra manual frame.
			if !manualFirst {
				plan.ExistingScreenshots = append([]api.ScreenshotImage(nil), retained.Screenshots...)
			}
			second, secondPrivate, err := builder.BuildIncremental(t.Context(), release, projections, secondRequest, &first, private, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Artifacts) != 3 || len(screenshots.selections) != 3 {
				t.Fatalf("after both captures: artifacts=%d rendered frames=%d, want 3 each", len(second.Artifacts), len(screenshots.selections))
			}
			if !reflect.DeepEqual(first.Artifacts, second.Artifacts[:len(first.Artifacts)]) {
				t.Fatal("existing artifact identity, selection, or ordering changed")
			}
			retained, ok = secondPrivate.(workflowMediaPrivateArtifacts)
			if !ok {
				t.Fatalf("retained resource = %T", secondPrivate)
			}
			plan.SuggestedSelections = nil
			plan.ExistingScreenshots = nil
			for _, image := range retained.Screenshots {
				if image.Index < 2 {
					plan.ExistingScreenshots = append(plan.ExistingScreenshots, image)
				}
			}
			manual.Selections[0].Index = 3
			manual.Selections[0].TimestampSeconds = 240
			third, thirdPrivate, err := builder.BuildIncremental(t.Context(), release, projections, manual, &second, secondPrivate, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(third.Artifacts) != 4 || len(screenshots.selections) != 4 {
				t.Fatalf("extra capture: artifacts=%d rendered frames=%d, want 4 each", len(third.Artifacts), len(screenshots.selections))
			}
			if !reflect.DeepEqual(second.Artifacts, third.Artifacts[:len(second.Artifacts)]) {
				t.Fatal("extra capture changed retained artifacts")
			}
			repeated, _, err := builder.BuildIncremental(t.Context(), release, projections, manual, &third, thirdPrivate, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(third.Artifacts, repeated.Artifacts) || len(screenshots.selections) != 4 {
				t.Fatal("repeated capture was not a no-op")
			}
			plan.Discs = append(plan.Discs, api.ScreenshotDiscPlan{DiscID: "other"})
			manual.Selections[0].DiscID = "other"
			manual.Selections[0].Index = 0
			otherDisc, _, err := builder.BuildIncremental(t.Context(), release, projections, manual, &third, thirdPrivate, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(otherDisc.Artifacts) != 5 || otherDisc.Artifacts[4].DiscID != "other" || otherDisc.Artifacts[4].Index != 0 {
				t.Fatalf("same frame index on another disc was not retained: %#v", otherDisc.Artifacts)
			}
		})
	}
}
