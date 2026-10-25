// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAutomaticScreenshotSelectionUsesIntentNotImagePresence(t *testing.T) {
	for _, test := range []struct {
		name         string
		instructions api.MediaCaptureInstructions
		prior        *api.MediaArtifactSet
		want         bool
	}{
		{name: "new automatic empty media", want: true},
		{
			name:  "automatic retains known origin",
			prior: &api.MediaArtifactSet{AutomaticScreenshotSelection: true},
			want:  true,
		},
		{name: "legacy is conservative", prior: &api.MediaArtifactSet{}},
		{name: "Generate count", instructions: api.MediaCaptureInstructions{ScreenshotCount: 3}},
		{
			name:         "empty selections equal omitted selections",
			instructions: api.MediaCaptureInstructions{Selections: []api.ScreenshotSelection{}},
			want:         true,
		},
		{name: "Generate selections", instructions: api.MediaCaptureInstructions{Selections: []api.ScreenshotSelection{{Index: 1}}}},
		{name: "manual frames", instructions: api.MediaCaptureInstructions{ManualFrames: []int{12}}},
		{name: "saved plan", instructions: api.MediaCaptureInstructions{SavedImagePlanID: "saved"}},
		{
			name:         "menu preserves automatic",
			instructions: api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeMenu},
			prior:        &api.MediaArtifactSet{AutomaticScreenshotSelection: true},
			want:         true,
		},
		{name: "menu cannot authorize screenshots", instructions: api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeMenu}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := automaticScreenshotSelection(test.instructions, test.prior); got != test.want {
				t.Fatalf("automatic=%v want=%v", got, test.want)
			}
		})
	}
}

func TestScreenshotSelectionPolicyPersistsAndRemainsTargetScoped(t *testing.T) {
	state := State{
		Workflow: api.ReleaseWorkflow{Release: &api.ReleaseSnapshotRef{ID: "release", Revision: 1}, Media: &api.MediaArtifactSetRef{ID: "media", Revision: 2}},
		Media: map[api.MediaArtifactSetID]api.MediaArtifactSet{"media": {
			ID:                           "media",
			Revision:                     2,
			Release:                      api.ReleaseSnapshotRef{ID: "release", Revision: 1},
			AutomaticScreenshotSelection: true,
		}},
	}
	instructions := map[api.TrackerID]api.TrackerProjectionInstructions{
		"DEFAULT":  {ScreenshotCount: new(3), ScreenshotCountIsDefault: true},
		"EXPLICIT": {ScreenshotCount: new(3)},
		"ZERO":     {ScreenshotCount: new(0)},
	}
	targets := []api.TrackerID{"DEFAULT", "EXPLICIT", "ZERO"}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	suppressed := screenshotReferenceSuppression(&restored, targets, instructions)
	if suppressed["DEFAULT"] || !suppressed["EXPLICIT"] || !suppressed["ZERO"] {
		t.Fatalf("wrong target policy: %+v", suppressed)
	}
	media := restored.Media["media"]
	media.Artifacts = []api.MediaArtifact{{ID: "screen", Kind: api.MediaArtifactScreenshot}, {
		ID:      "menu",
		Kind:    api.MediaArtifactDVDMenu,
		Purpose: api.ScreenshotPurposeMenu,
	}}
	markExplicitScreenshotSelection(&media, []api.PublicResourceID{"menu"})
	if !media.AutomaticScreenshotSelection {
		t.Fatal("menu selection cleared screenshot intent")
	}
	markExplicitScreenshotSelection(&media, []api.PublicResourceID{"screen"})
	restored.Media["media"] = media
	if !screenshotReferenceSuppression(&restored, targets, instructions)["DEFAULT"] {
		t.Fatal("manual screenshot selection still permits imported additions")
	}
}

func TestAutomaticSkippedCaptureRetainsKnownSelectionIntent(t *testing.T) {
	module, repository, _, current := newDuplicateReuseWorkflow(t)
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	projections := state.Projections[state.Workflow.TrackerProjections.ID]
	for i := range projections.Projections {
		projections.Projections[i].Artifacts.ScreenshotCount = 0
		projections.Projections[i].Artifacts.DVDMenuCount = 0
	}
	state.Projections[projections.ID] = projections
	module.mediaBuilder = mediaArtifactBuilderFunc(func(_ context.Context, _ api.ReleaseRef, projections api.TrackerReleaseProjectionSet, _ api.MediaCaptureInstructions, _ time.Time) (api.MediaArtifactSet, any, error) {
		requirements, err := mediaRequirementsFingerprint(projections.Projections)
		return api.MediaArtifactSet{
			CaptureFingerprint:      testFingerprint(t, "empty-auto"),
			RequirementsFingerprint: requirements,
			Status:                  api.StageStatusSkipped,
			Artifacts:               []api.MediaArtifact{},
		}, &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, err
	})
	captured, err := module.captureMedia(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, module.clock.Now(), CaptureMediaCommand{Instructions: api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal}})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Media == nil || captured.Media.Status != api.StageStatusSkipped || !captured.Media.AutomaticScreenshotSelection || len(captured.Media.Artifacts) != 0 {
		t.Fatalf("empty automatic capture lost intent: %+v", captured.Media)
	}
	if suppressed := currentScreenshotReferenceSuppression(&state); len(suppressed) != 0 {
		t.Fatalf("empty automatic capture suppresses references: %+v", suppressed)
	}
}

func TestAutomaticCompositeTrimFingerprintCannotAuthorizeManualSelection(t *testing.T) {
	automatic := SetMediaSelectionCommand{preserveAutomaticScreenshotSelection: true}
	automaticFingerprint, err := automatic.commandFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	manualFingerprint, err := (SetMediaSelectionCommand{}).commandFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if automaticFingerprint == manualFingerprint {
		t.Fatal("automatic and explicit selection share command authority")
	}
	encoded, err := json.Marshal(automatic)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SetMediaSelectionCommand
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.preserveAutomaticScreenshotSelection {
		t.Fatal("internal automatic trim authority entered transport JSON")
	}
}

func TestSuccessfulScreenshotSelectionPersistsManualIntent(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(strconv.FormatBool(preserve), func(t *testing.T) {
			module, repository, _, current := newDuplicateReuseWorkflow(t)
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			module.mediaBuilder = mediaArtifactBuilderFunc(func(_ context.Context, _ api.ReleaseRef, projections api.TrackerReleaseProjectionSet, _ api.MediaCaptureInstructions, _ time.Time) (api.MediaArtifactSet, any, error) {
				requirements, err := mediaRequirementsFingerprint(projections.Projections)
				return api.MediaArtifactSet{
					CaptureFingerprint:      testFingerprint(t, "selection-auto"),
					RequirementsFingerprint: requirements,
					Status:                  api.StageStatusCompleted,
					Artifacts: []api.MediaArtifact{{
						ID:       "screen",
						Kind:     api.MediaArtifactScreenshot,
						Purpose:  api.ScreenshotPurposeFinal,
						Selected: true,
					}},
				}, &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, err
			})
			captured, err := module.captureMedia(t.Context(), testOwnerID, &state, state.Workflow.Revision+1, module.clock.Now(), CaptureMediaCommand{Instructions: api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal}})
			if err != nil {
				t.Fatal(err)
			}
			selected, err := module.setMediaSelection(t.Context(), testOwnerID, &state, captured.Media.Revision+1, module.clock.Now(), SetMediaSelectionCommand{
				Media:                                *state.Workflow.Media,
				ArtifactIDs:                          []api.PublicResourceID{"screen"},
				Selected:                             true,
				preserveAutomaticScreenshotSelection: preserve,
			})
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var restarted State
			if err := json.Unmarshal(data, &restarted); err != nil {
				t.Fatal(err)
			}
			if selected.Media.AutomaticScreenshotSelection != preserve || restarted.Media[selected.Media.ID].AutomaticScreenshotSelection != preserve {
				t.Fatal("successful selection lost exact intent across restart")
			}
		})
	}
}

func TestDescriptionScreenshotPolicyClearsPriorWrapperAndRejectsStaleInstructions(t *testing.T) {
	state := State{
		Workflow: api.ReleaseWorkflow{
			ID:                 "workflow",
			Release:            &api.ReleaseSnapshotRef{ID: "release", Revision: 1},
			TrackerProjections: &api.TrackerReleaseProjectionSetRef{ID: "projection", Revision: 2},
		},
		Projections: map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{"projection": {
			ID:          "projection",
			Revision:    2,
			Release:     api.ReleaseSnapshotRef{ID: "release", Revision: 1},
			Projections: []api.TrackerReleaseProjection{{TrackerID: "LST"}},
		}},
	}
	prior := DescriptionResources{Media: "media", SuppressImportedScreenshotReferences: map[string]bool{"LST": true}}
	rebuilt, ok := screenshotDescriptionResources(&state, prior).(DescriptionResources)
	if !ok {
		t.Fatal("rebuilt description resources have unexpected type")
	}
	if len(rebuilt.SuppressImportedScreenshotReferences) != 0 || rebuilt.Media != "media" {
		t.Fatal("derived suppression was not cleared on a fresh default operation")
	}
	if !prior.SuppressImportedScreenshotReferences["LST"] {
		t.Fatal("wrapper update mutated prior operation")
	}
	state.Workflow.ProjectionInstructions = &api.TrackerProjectionInstructionSnapshotRef{ID: "missing", Revision: 2}
	if !currentScreenshotReferenceSuppression(&state)["LST"] {
		t.Fatal("missing instruction authority was treated as automatic")
	}
}

func TestExplicitMediaReplacementTracksRemovedScreenshotIntent(t *testing.T) {
	for _, test := range []struct {
		name               string
		screenshotPresent  bool
		screenshotSelected bool
		wantAutomatic      bool
	}{
		{
			name:               "menu replacement removes a selected screenshot",
			screenshotPresent:  true,
			screenshotSelected: true,
		},
		{
			name:              "menu replacement leaves unselected screenshot unchanged",
			screenshotPresent: true,
			wantAutomatic:     true,
		},
		{name: "menu-only media preserves screenshot intent", wantAutomatic: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := api.MediaArtifactSet{
				AutomaticScreenshotSelection: true,
				Artifacts: []api.MediaArtifact{{
					ID:      "menu",
					Kind:    api.MediaArtifactDVDMenu,
					Purpose: api.ScreenshotPurposeMenu,
				}},
			}
			if test.screenshotPresent {
				snapshot.Artifacts = append(snapshot.Artifacts, api.MediaArtifact{
					ID:       "screen",
					Kind:     api.MediaArtifactScreenshot,
					Purpose:  api.ScreenshotPurposeFinal,
					Selected: test.screenshotSelected,
				})
			}
			// Match the host-empty explicit artifact-list upload boundary.
			ids := []api.PublicResourceID{"menu"}
			selectExplicitMediaArtifacts(&snapshot, ids)
			markExplicitScreenshotSelection(&snapshot, ids)
			if snapshot.AutomaticScreenshotSelection != test.wantAutomatic {
				t.Fatalf("automatic screenshot intent = %v, want %v", snapshot.AutomaticScreenshotSelection, test.wantAutomatic)
			}
			if !snapshot.Artifacts[0].Selected || test.screenshotPresent && snapshot.Artifacts[1].Selected {
				t.Fatalf("replacement selection changed unexpectedly: %+v", snapshot.Artifacts)
			}
		})
	}
}

func TestCaptureSelectionOriginSurvivesDurableCommandRoundTrip(t *testing.T) {
	for _, selections := range [][]api.ScreenshotSelection{nil, {}, {{Index: 1}}} {
		command := CaptureMediaCommand{Instructions: api.MediaCaptureInstructions{Selections: selections}}
		_, payload, err := (durableOperationCommand{command: command}).MarshalPrivateResource()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeDurableOperationCommand(payload)
		if err != nil {
			t.Fatal(err)
		}
		durable, ok := decoded.(durableOperationCommand)
		if !ok {
			t.Fatal("decoded operation has unexpected type")
		}
		restored, ok := durable.command.(CaptureMediaCommand)
		if !ok {
			t.Fatal("restored command has unexpected type")
		}
		wantAutomatic := len(selections) == 0
		if automaticScreenshotSelection(command.Instructions, nil) != wantAutomatic ||
			automaticScreenshotSelection(restored.Instructions, nil) != wantAutomatic {
			t.Fatalf("selection intent changed across durable command: original=%+v restored=%+v", command, restored)
		}
		before, err := command.commandFingerprint()
		if err != nil {
			t.Fatal(err)
		}
		after, err := restored.commandFingerprint()
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("durable capture command changed selection fingerprint")
		}
	}
}
