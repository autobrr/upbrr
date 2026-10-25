// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

func TestScreenshotCountOriginRoundTrip(t *testing.T) {
	t.Parallel()
	for _, isDefault := range []bool{false, true} {
		instructions := TrackerProjectionInstructions{ScreenshotCount: new(7), ScreenshotCountIsDefault: isDefault}
		payload, err := json.Marshal(instructions)
		if err != nil {
			t.Fatal(err)
		}
		var restored TrackerProjectionInstructions
		if err := json.Unmarshal(payload, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.ScreenshotCount == nil || *restored.ScreenshotCount != 7 || restored.ScreenshotCountIsDefault != isDefault {
			t.Fatalf("restored projection count = %#v", restored)
		}
		cloned, err := (TrackerProjectionInstructionSnapshot{
			Instructions: map[TrackerID]TrackerProjectionInstructions{"EXAMPLE": instructions},
		}).Clone()
		if err != nil {
			t.Fatal(err)
		}
		if cloned.Instructions["EXAMPLE"].ScreenshotCountIsDefault != isDefault {
			t.Fatal("snapshot clone changed screenshot count origin")
		}
		*cloned.Instructions["EXAMPLE"].ScreenshotCount = 3
		if *instructions.ScreenshotCount != 7 {
			t.Fatal("snapshot clone retained shared screenshot count storage")
		}
		media := ReleaseWorkflowUploadScreenshots{Count: new(7), CountIsDefault: isDefault}
		payload, err = json.Marshal(media)
		if err != nil {
			t.Fatal(err)
		}
		var restoredMedia ReleaseWorkflowUploadScreenshots
		if err := json.Unmarshal(payload, &restoredMedia); err != nil {
			t.Fatal(err)
		}
		if restoredMedia.Count == nil || *restoredMedia.Count != 7 || restoredMedia.CountIsDefault != isDefault {
			t.Fatalf("restored upload count = %#v", restoredMedia)
		}
	}
	var legacy TrackerProjectionInstructions
	if err := json.Unmarshal([]byte(`{"screenshotCount":7}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.ScreenshotCountIsDefault {
		t.Fatal("legacy count was downgraded to an automatic default")
	}
}

func TestScreenshotCountOriginRequiresCount(t *testing.T) {
	t.Parallel()
	if err := validateTrackerProjectionInstructions(map[TrackerID]TrackerProjectionInstructions{
		"EXAMPLE": {ScreenshotCountIsDefault: true},
	}); err == nil {
		t.Fatal("accepted default origin without a projection count")
	}
	request := CreateReleaseWorkflowUploadRequest{
		Source:         ReleaseWorkflowUploadSource{Path: "Example.Release.mkv"},
		Unattended:     &ReleaseWorkflowUploadUnattended{},
		Media:          ReleaseWorkflowUploadMedia{Screenshots: ReleaseWorkflowUploadScreenshots{CountIsDefault: true}},
		IdempotencyKey: "count-origin",
	}
	if err := request.Validate(); err == nil {
		t.Fatal("accepted default origin without an upload count")
	}
	request.Media.Screenshots.Count = new(7)
	if err := request.Validate(); err != nil {
		t.Fatalf("rejected default origin with count: %v", err)
	}
	request.Media.Screenshots.Count = new(0)
	request.Media.Screenshots.CountIsDefault = false
	if err := request.Validate(); err != nil {
		t.Fatalf("rejected explicit zero count: %v", err)
	}
}
