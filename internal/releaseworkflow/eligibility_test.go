// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowModuleDoesNotReadRawTrackerSelectionDownstream(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve release workflow source path")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "module.go"))
	if err != nil {
		t.Fatalf("read release workflow module: %v", err)
	}
	if strings.Contains(string(source), "Selection.TrackerIDs") {
		t.Fatal("release workflow module must resolve downstream trackers through resolveDownstreamTrackerSet")
	}
}

func TestDownstreamEligibleProjectionsAfterMediaExcludesOnlyFailedImageHostTracker(t *testing.T) {
	t.Parallel()

	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{
			TrackerID:   "ALPHA",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		},
		{
			TrackerID:   "BETA",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		},
	}}
	dupes := api.DupeAssessment{Results: []api.TrackerDupeAssessment{
		{
			TrackerID: "ALPHA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
		{
			TrackerID: "BETA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
	}}
	media := api.MediaArtifactSet{Failures: []api.WorkflowFailure{
		{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureImageHostUnavailable,
				Operation: api.OperationKindImageHosting,
				Message:   "Required image host failed.",
				Recovery:  api.OperationRecoveryRetry,
			},
			TrackerID: "beta",
			Resource:  "pixhost",
		},
		{
			Failure: api.OperationFailure{
				Code:      api.OperationFailureInternal,
				Operation: api.OperationKindMedia,
				Message:   "Unrelated media warning.",
				Recovery:  api.OperationRecoveryRetry,
			},
			TrackerID: "ALPHA",
		},
	}}

	eligible := DownstreamEligibleProjectionsAfterMedia(projections, dupes, media)
	if len(eligible.Projections) != 1 || eligible.Projections[0].TrackerID != "ALPHA" {
		t.Fatalf("media-aware eligible projections = %#v", eligible.Projections)
	}
	failure, failed := TrackerImageHostFailure(media, "BETA")
	if !failed || failure.Resource != "pixhost" {
		t.Fatalf("tracker image-host failure = %#v, found=%t", failure, failed)
	}

	media.Failures = nil
	eligible = DownstreamEligibleProjectionsAfterMedia(projections, dupes, media)
	if len(eligible.Projections) != 2 {
		t.Fatalf("eligible projections after successful retry = %#v", eligible.Projections)
	}
}

func TestDownstreamEligibleProjectionsExcludesUnapprovedRuleWarning(t *testing.T) {
	t.Parallel()

	projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
		{
			TrackerID:   "ALPHA",
			Readiness:   api.ReadinessStatusBlocked,
			UploadReady: false,
		},
		{
			TrackerID:   "BETA",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		},
	}}
	dupes := api.DupeAssessment{Results: []api.TrackerDupeAssessment{
		{
			TrackerID: "BETA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		},
	}}

	eligible := DownstreamEligibleProjections(projections, dupes)
	if len(eligible.Projections) != 1 || eligible.Projections[0].TrackerID != "BETA" {
		t.Fatalf("eligible projections = %#v", eligible.Projections)
	}
}

func TestDownstreamTrackerSetEnforcesModeAndExactApproval(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	releaseRef := api.ReleaseSnapshotRef{ID: "release-authority", Revision: 2}
	selectionRef := api.TrackerSelectionRef{ID: "selection-authority", Revision: 3}
	projectionRef := api.TrackerReleaseProjectionSetRef{ID: "projections-authority", Revision: 4}
	preflightRef := api.TrackerPreflightAssessmentRef{ID: "preflight-authority", Revision: 5}
	dupeRef := api.DupeAssessmentRef{ID: "dupes-authority", Revision: 6}
	projections := api.TrackerReleaseProjectionSet{
		ID:        projectionRef.ID,
		Revision:  projectionRef.Revision,
		Preflight: &preflightRef,
		Status:    api.StageStatusReady,
		Projections: []api.TrackerReleaseProjection{
			{
				TrackerID:   "ALPHA",
				DisplayName: "Alpha",
				Readiness:   api.ReadinessStatusReady,
				UploadReady: true,
			},
			{
				TrackerID:   "BETA",
				DisplayName: "Beta",
				Readiness:   api.ReadinessStatusReady,
				UploadReady: true,
			},
		},
	}
	preflight := api.TrackerPreflightAssessment{
		ID:        preflightRef.ID,
		Revision:  preflightRef.Revision,
		Status:    api.StageStatusReady,
		ExpiresAt: now.Add(time.Hour),
	}
	dupes := api.DupeAssessment{
		ID:               dupeRef.ID,
		Revision:         dupeRef.Revision,
		Selection:        selectionRef,
		ProjectionSet:    projectionRef,
		InputFingerprint: testFingerprint(t, "tracker-authority-dupes"),
		Status:           api.StageStatusCompleted,
		ExpiresAt:        now.Add(time.Hour),
		Results: []api.TrackerDupeAssessment{
			{
				TrackerID: "ALPHA",
				Decision:  api.DupeDecisionNoMatch,
				Status:    api.StageStatusCompleted,
			},
			{
				TrackerID: "BETA",
				Decision:  api.DupeDecisionNoMatch,
				Status:    api.StageStatusCompleted,
			},
		},
	}
	state := State{
		TrackerDecisionMode: TrackerDecisionModePostDupeGate,
		Workflow: api.ReleaseWorkflow{
			ID:                 "workflow-authority",
			Revision:           7,
			Release:            &releaseRef,
			Selection:          &selectionRef,
			TrackerProjections: &projectionRef,
			TrackerPreflight:   &preflightRef,
			Dupes:              &dupeRef,
		},
		Projections:      map[api.TrackerReleaseProjectionSetID]api.TrackerReleaseProjectionSet{projections.ID: projections},
		Preflights:       map[api.TrackerPreflightAssessmentID]api.TrackerPreflightAssessment{preflight.ID: preflight},
		Dupes:            map[api.DupeAssessmentID]api.DupeAssessment{dupes.ID: dupes},
		TrackerApprovals: make(map[api.TrackerApprovalSnapshotID]api.TrackerApprovalSnapshot),
	}

	pendingDupes := dupes
	pendingDupes.Results = append([]api.TrackerDupeAssessment(nil), dupes.Results...)
	pendingDupes.Results[1].Decision = api.DupeDecisionPending
	state.Dupes[dupes.ID] = pendingDupes
	if action, _, err := projectedTrackerApprovalAction(&state, now); err != nil || action != nil {
		t.Fatalf("pending duplicate decision advertised approval: action=%#v err=%v", action, err)
	}
	failedDupes := dupes
	failedDupes.Results = append([]api.TrackerDupeAssessment(nil), dupes.Results...)
	failedDupes.Results[1].Decision = api.DupeDecisionSkipped
	failedDupes.Results[1].Status = api.StageStatusFailed
	failedDupes.Results[1].Failures = []api.WorkflowFailure{{
		Failure:   api.OperationFailure{Code: api.OperationFailureInternal},
		TrackerID: "BETA",
	}}
	state.Dupes[dupes.ID] = failedDupes
	if action, _, err := projectedTrackerApprovalAction(&state, now); err != nil || action == nil ||
		len(action.Options) != 1 || action.Options[0].Value != "ALPHA" {
		t.Fatalf("failed sibling tracker approval action = %#v err=%v", action, err)
	}
	state.Dupes[dupes.ID] = dupes

	action, actionInput, err := projectedTrackerApprovalAction(&state, now)
	if err != nil || action == nil || action.Kind != api.RequiredActionApproveTrackers ||
		len(action.Options) != 2 || action.Options[0].Value != "ALPHA" || action.Options[1].Value != "BETA" {
		t.Fatalf("gated tracker action = %#v fingerprint=%s err=%v", action, actionInput, err)
	}
	if _, err := resolveDownstreamTrackerSet(&state, nil, downstreamStageMedia, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("gated downstream without approval error = %v", err)
	}

	state.TrackerDecisionMode = "unknown-mode"
	if action, _, err := projectedTrackerApprovalAction(&state, now); err != nil || action == nil {
		t.Fatalf("unknown mode must fail closed with gated action: action=%#v err=%v", action, err)
	}

	state.TrackerDecisionMode = TrackerDecisionModeWebUIControls
	if action, _, err := projectedTrackerApprovalAction(&state, now); err != nil || action != nil {
		t.Fatalf("WebUI tracker action = %#v err=%v", action, err)
	}
	webTargets, err := resolveDownstreamTrackerSet(&state, []api.TrackerID{"BETA"}, downstreamStageUpload, now)
	if err != nil || !slices.Equal(webTargets.TrackerIDs(), []api.TrackerID{"BETA"}) || webTargets.TrackerApproval() != nil {
		t.Fatalf("WebUI downstream targets = %#v err=%v", webTargets, err)
	}
	media := api.MediaArtifactSet{
		ID:       "media-authority",
		Revision: 8,
		Failures: []api.WorkflowFailure{{
			Failure:   api.OperationFailure{Operation: api.OperationKindImageHosting},
			TrackerID: "BETA",
		}},
	}
	state.Workflow.Media = &api.MediaArtifactSetRef{ID: media.ID, Revision: media.Revision}
	state.Media = map[api.MediaArtifactSetID]api.MediaArtifactSet{media.ID: media}
	webTargets, err = resolveDownstreamTrackerSet(&state, nil, downstreamStageUpload, now)
	if err != nil || !slices.Equal(webTargets.TrackerIDs(), []api.TrackerID{"ALPHA"}) {
		t.Fatalf("WebUI upload targets after image-host failure = %#v err=%v", webTargets, err)
	}

	// Losing every tracker to image hosting must name the cause here instead of
	// handing an empty target set to upload-plan contract validation.
	allFailed := media
	allFailed.Failures = append(append([]api.WorkflowFailure(nil), media.Failures...), api.WorkflowFailure{
		Failure:   api.OperationFailure{Operation: api.OperationKindImageHosting},
		TrackerID: "ALPHA",
	})
	state.Media[media.ID] = allFailed
	emptied, err := resolveDownstreamTrackerSet(&state, nil, downstreamStageUpload, now)
	if !errors.Is(err, ErrInvalidTransition) || len(emptied.TrackerIDs()) != 0 {
		t.Fatalf("upload targets with every tracker image-host blocked = %#v err=%v", emptied, err)
	}
	if !strings.Contains(err.Error(), "ALPHA, BETA") {
		t.Fatalf("image-host exhaustion error must name the blocked trackers: %v", err)
	}
	state.Media[media.ID] = media
	state.Workflow.Media = nil

	state.TrackerDecisionMode = TrackerDecisionModePostDupeGate
	approvalFingerprint, err := trackerApprovalFingerprint(actionInput, []api.TrackerID{"ALPHA", "BETA"}, []api.TrackerID{"ALPHA"})
	if err != nil {
		t.Fatalf("fingerprint approval: %v", err)
	}
	approval := api.TrackerApprovalSnapshot{
		ID:                  "approval-authority",
		WorkflowID:          state.Workflow.ID,
		Revision:            8,
		Release:             releaseRef,
		Selection:           selectionRef,
		ProjectionSet:       projectionRef,
		Preflight:           preflightRef,
		Dupes:               dupeRef,
		CandidateTrackerIDs: []api.TrackerID{"ALPHA", "BETA"},
		ApprovedTrackerIDs:  []api.TrackerID{"ALPHA"},
		InputFingerprint:    approvalFingerprint,
		CreatedAt:           now,
	}
	state.Workflow.Revision = approval.Revision
	state.Workflow.TrackerApproval = &api.TrackerApprovalSnapshotRef{ID: approval.ID, Revision: approval.Revision}
	state.TrackerApprovals[approval.ID] = approval
	gatedTargets, err := resolveDownstreamTrackerSet(&state, nil, downstreamStageMedia, now)
	if err != nil || !slices.Equal(gatedTargets.TrackerIDs(), []api.TrackerID{"ALPHA"}) ||
		gatedTargets.TrackerApproval() == nil {
		t.Fatalf("approved downstream targets = %#v err=%v", gatedTargets, err)
	}

	dupes.InputFingerprint = testFingerprint(t, "changed-tracker-authority-dupes")
	state.Dupes[dupes.ID] = dupes
	if _, err := resolveDownstreamTrackerSet(&state, nil, downstreamStageMedia, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("changed dupe input retained stale approval: %v", err)
	}
}
