// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"encoding/json"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompositeScreenshotCountOriginSurvivesRestore(t *testing.T) {
	t.Parallel()
	fingerprints := make([]api.WorkflowFingerprint, 0, 2)
	for _, isDefault := range []bool{false, true} {
		request := api.CreateReleaseWorkflowUploadRequest{
			Source:         api.ReleaseWorkflowUploadSource{Path: "Example.Release.mkv"},
			Unattended:     &api.ReleaseWorkflowUploadUnattended{},
			Trackers:       api.ReleaseWorkflowUploadTrackers{Include: []api.TrackerID{"LST", "OTHER"}},
			Media:          api.ReleaseWorkflowUploadMedia{Screenshots: api.ReleaseWorkflowUploadScreenshots{Count: new(7), CountIsDefault: isDefault}},
			IdempotencyKey: "count-origin",
		}
		session, _, err := normalizeCompositeUploadRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		fingerprints = append(fingerprints, session.RequestFingerprint)
		payload, err := json.Marshal(session)
		if err != nil {
			t.Fatal(err)
		}
		var restored compositeUploadSession
		if err := json.Unmarshal(payload, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.RequestedScreenshotCount == nil || *restored.RequestedScreenshotCount != 7 || restored.RequestedScreenshotCountIsDefault != isDefault {
			t.Fatalf("restored screenshot request = %#v", restored)
		}
		if restored.Intent.Descriptions == nil || restored.Intent.Descriptions.Options.Screens != 7 {
			t.Fatalf("description count changed: %#v", restored.Intent.Descriptions)
		}
		if restored.Intent.Media == nil || restored.Intent.Media.ScreenshotCount != 0 {
			t.Fatalf("capture instructions bypass projection requirements: %#v", restored.Intent.Media)
		}
		for _, trackerID := range request.Trackers.Include {
			instruction := restored.Intent.ProjectionInstructions[trackerID]
			if instruction.ScreenshotCount == nil || *instruction.ScreenshotCount != 7 || instruction.ScreenshotCountIsDefault != isDefault {
				t.Fatalf("tracker %s restored count = %#v", trackerID, instruction)
			}
		}
	}
	if fingerprints[0] == fingerprints[1] {
		t.Fatal("explicit and default capture origins share request authority")
	}
	var legacy compositeUploadSession
	if err := json.Unmarshal([]byte(`{"requestedScreenshotCount":7,"intent":{"projectionInstructions":{"LST":{"screenshotCount":7}}}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.RequestedScreenshotCountIsDefault || legacy.Intent.ProjectionInstructions["LST"].ScreenshotCountIsDefault {
		t.Fatal("legacy persisted counts were downgraded to automatic defaults")
	}
}
