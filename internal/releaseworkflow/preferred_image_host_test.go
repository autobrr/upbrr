// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
	"time"
)

func TestContinuationPlannerCarriesPreferredImagePolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 23, 1, 2, 3, 0, time.UTC)
	current := CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "workflow-plan", Revision: 7},
		Release:  &api.ReleaseSnapshot{ID: "release-plan", Revision: 2},
		Selection: &api.TrackerSelection{
			TrackerIDs: []api.TrackerID{"ALPHA"},
		},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{}},
		Projections: &api.TrackerReleaseProjectionSet{
			ID:               "projections-plan",
			Revision:         3,
			InputFingerprint: testFingerprint(t, "projection-input"),
			Projections: []api.TrackerReleaseProjection{{
				TrackerID:   "ALPHA",
				Readiness:   api.ReadinessStatusReady,
				DupeReady:   true,
				UploadReady: true,
			}},
		},
		Preflight: &api.TrackerPreflightAssessment{
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-plan", Revision: 3},
			ExpiresAt:     now.Add(time.Hour),
		},
		Dupes: &api.DupeAssessment{
			ProjectionSet: api.TrackerReleaseProjectionSetRef{ID: "projections-plan", Revision: 3},
			Status:        api.StageStatusCompleted,
			ExpiresAt:     now.Add(time.Hour),
			Results: []api.TrackerDupeAssessment{{
				TrackerID: "ALPHA",
				Status:    api.StageStatusCompleted,
				Decision:  api.DupeDecisionNoMatch,
			}},
		},
		Media: &api.MediaArtifactSet{
			ID:       "media-plan",
			Revision: 6,
			Status:   api.StageStatusCompleted,
			Artifacts: []api.MediaArtifact{{
				ID:       "screen-plan",
				Kind:     api.MediaArtifactScreenshot,
				Selected: true,
			}},
		},
	}
	request := api.ContinueReleaseWorkflowRequest{Goal: api.WorkflowGoalDescriptionsReady, Intent: api.WorkflowIntent{
		TrackerIDs:             []api.TrackerID{"ALPHA"},
		ProjectionInstructions: map[api.TrackerID]api.TrackerProjectionInstructions{},
		Descriptions:           &api.DescriptionInstructions{ImageHost: api.ImageHostOverrides{PreferredHost: new("imgbb")}},
	}}
	command, stage := planContinuationCommand(request, current, now)
	upload, ok := command.(UploadMediaImagesCommand)
	if !ok || stage != "prepare-image-requirements" || upload.Host != "" || upload.PreferredHost == nil || *upload.PreferredHost != "imgbb" {
		t.Fatalf("automatic policy command: stage=%s command=%#v", stage, command)
	}
	baseline := upload
	baseline.PreferredHost = nil
	defaultFP, err := baseline.commandFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	preferredFP, err := upload.commandFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if defaultFP == preferredFP {
		t.Fatal("preferred policy reused the default command identity")
	}
}

func TestPreferredImageHostContinuationReadiness(t *testing.T) {
	current := CommandResult{
		Selection:              &api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA"}},
		ProjectionInstructions: &api.TrackerProjectionInstructionSnapshot{Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{}},
		Projections:            &api.TrackerReleaseProjectionSet{ID: "projections", Revision: 1},
		Media: &api.MediaArtifactSet{
			Status:                    api.StageStatusCompleted,
			ImageRequirementsPrepared: true,
			ImageHostPreference:       new("imgbb"),
			Artifacts: []api.MediaArtifact{{
				ID:       "screen",
				Kind:     api.MediaArtifactScreenshot,
				Selected: true,
			}},
		},
		Descriptions: &api.DescriptionSet{Status: api.StageStatusCompleted, Descriptions: []api.RenderedDescription{{GroupKey: "default", Source: "synthetic description"}}},
	}
	request := api.ContinueReleaseWorkflowRequest{Goal: api.WorkflowGoalDescriptionsReady, Intent: api.WorkflowIntent{Descriptions: &api.DescriptionInstructions{ImageHost: api.ImageHostOverrides{PreferredHost: new("imgbb")}}}}
	if !continuationGoalReached(current, request) {
		t.Fatal("matching completed policy was not reusable")
	}
	for _, preference := range []*string{new("pixhost"), nil, new("")} {
		request.Intent.Descriptions.ImageHost.PreferredHost = preference
		if continuationGoalReached(current, request) {
			t.Fatal("changed or reset preference inherited the old hosting barrier")
		}
	}
	current.Media.ImageHostPreference = nil
	request.Intent.Descriptions.ImageHost.PreferredHost = nil
	if !continuationGoalReached(current, request) {
		t.Fatal("legacy default hosting barrier was invalidated")
	}
}

// preferredPolicyMediaMutator observes the composite-to-hosting boundary while
// retaining the normal test mutator's hosted-artifact behavior.
type preferredPolicyMediaMutator struct {
	hostSpecificMediaMutator
	host       string
	preference *string
	calls      int
}

func (m *preferredPolicyMediaMutator) UploadImages(ctx context.Context, release api.ReleaseRef, projections api.TrackerReleaseProjectionSet,
	snapshot api.MediaArtifactSet, retained any, artifactIDs []api.PublicResourceID, host string, retry bool, preferredHost *string, now time.Time,
) (api.MediaArtifactSet, RetainedMediaResource, []api.HostedImageAttempt, error) {
	m.host = host
	m.preference = cloneStringPointer(preferredHost)
	m.calls++
	return m.hostSpecificMediaMutator.UploadImages(ctx, release, projections, snapshot, retained, artifactIDs, host, retry, preferredHost, now)
}
func TestCompositeUploadAppliesPreferredHostWithoutExplicitHost(t *testing.T) {
	module, _, _ := newCompositeUploadTestModule(t)
	mutator := &preferredPolicyMediaMutator{hostSpecificMediaMutator: hostSpecificMediaMutator{host: "imgbb", mediaArtifactBuilderFunc: func(_ context.Context, _ api.ReleaseRef, projections api.TrackerReleaseProjectionSet, _ api.MediaCaptureInstructions, _ time.Time) (api.MediaArtifactSet, any, error) {
		fingerprint, err := mediaRequirementsFingerprint(projections.Projections)
		return api.MediaArtifactSet{
			CaptureFingerprint:      testFingerprint(t, "preferred-image"),
			RequirementsFingerprint: fingerprint,
			Status:                  api.StageStatusCompleted,
			Artifacts: []api.MediaArtifact{{
				ID:       "screen",
				Kind:     api.MediaArtifactScreenshot,
				Purpose:  api.ScreenshotPurposeFinal,
				Selected: true,
			}},
		}, &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, err
	}}}
	module.mediaBuilder = mutator
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeDebug, "preferred-host")
	request.Trackers.Include = []api.TrackerID{"ALPHA"}
	request.ImageHosting.PreferredHost = new("imgbb")
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	current := waitCompositeUploadTestOperation(t, module, started)
	current = approveCompositeUploadTrackers(t, module, current, []api.TrackerID{"ALPHA"}, "approve-preferred-host")
	if mutator.calls != 1 || mutator.host != "" || mutator.preference == nil || *mutator.preference != "imgbb" {
		t.Fatalf("hosting calls=%d explicit=%q preferred=%v", mutator.calls, mutator.host, mutator.preference)
	}
	if current.Media == nil || !current.Media.ImageRequirementsPrepared || current.Media.ImageHostPreference == nil || *current.Media.ImageHostPreference != "imgbb" {
		t.Fatalf("missing automatic preparation barrier: %+v", current.Media)
	}
}
