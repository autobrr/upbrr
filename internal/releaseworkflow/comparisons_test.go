// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonMediaFake struct {
	hostSpecificMediaMutator
	edits, uploads, restores int
}

func (b *comparisonMediaFake) RestoreComparisons(_ context.Context, _ api.ReleaseRef, projections api.TrackerReleaseProjectionSet,
	previous api.MediaArtifactSet, private any, _ time.Time,
) (api.MediaArtifactSet, RetainedMediaResource, error) {
	b.restores++
	snapshot, err := previous.Clone()
	if err != nil {
		return snapshot, nil, fmt.Errorf("clone retained comparison fake: %w", err)
	}
	snapshot.Comparisons.ApprovedFingerprint = ""
	snapshot.RequirementsFingerprint, err = mediaRequirementsFingerprint(projections.Projections)
	if err != nil {
		return snapshot, nil, err
	}
	retained, _ := private.(RetainedMediaResource)
	return snapshot, retained, nil
}

func (b *comparisonMediaFake) EditComparisons(_ context.Context, _ api.ReleaseRef, projections api.TrackerReleaseProjectionSet,
	prior *api.MediaArtifactSet, _ any, instructions api.ComparisonInstructions, _ []StagedMediaAttachment, _ time.Time,
) (api.MediaArtifactSet, RetainedMediaResource, error) {
	b.edits++
	var snapshot api.MediaArtifactSet
	if prior != nil {
		var err error
		snapshot, err = prior.Clone()
		if err != nil {
			return snapshot, nil, fmt.Errorf("clone fake comparison snapshot: %w", err)
		}
	}
	plan := instructions.Plan
	if instructions.Action == "configure" && len(plan.Sources) == 0 && len(plan.Rows) == 0 {
		snapshot.Comparisons = nil
		snapshot.Artifacts = nil
		var err error
		snapshot.RequirementsFingerprint, err = mediaRequirementsFingerprint(projections.Projections)
		if err != nil {
			return snapshot, nil, err
		}
		return snapshot, &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, nil
	}
	var err error
	plan.Fingerprint, err = comparisons.ReviewFingerprint(plan)
	if err != nil {
		return snapshot, nil, fmt.Errorf("fingerprint fake comparison plan: %w", err)
	}
	if instructions.Action == "approve" {
		plan.ApprovedFingerprint = instructions.ExpectedFingerprint
	}
	snapshot.Comparisons = &plan
	snapshot.RequirementsFingerprint, err = mediaRequirementsFingerprint(projections.Projections)
	if err != nil {
		return snapshot, nil, err
	}
	snapshot.CaptureFingerprint = plan.Fingerprint
	if len(snapshot.Artifacts) == 0 {
		for _, row := range plan.Rows {
			for _, cell := range row.Cells {
				snapshot.Artifacts = append(snapshot.Artifacts, api.MediaArtifact{
					ID:       cell.ArtifactID,
					Kind:     api.MediaArtifactComparison,
					Purpose:  api.ScreenshotPurposeComparison,
					Selected: true,
				})
			}
		}
	}
	return snapshot, &retainedMediaResourceFake{stats: &retainedMediaResourceStats{}}, nil
}

func (b *comparisonMediaFake) PreviewComparisonFrame(context.Context, api.ReleaseRef, api.MediaArtifactSet, any,
	string, float64, int) (MediaPreviewContent, error) {
	return MediaPreviewContent{}, errors.New("preview not used by review test")
}

func (b *comparisonMediaFake) UploadImages(ctx context.Context, release api.ReleaseRef, projections api.TrackerReleaseProjectionSet,
	snapshot api.MediaArtifactSet, private any, ids []api.PublicResourceID, host string, retry bool, preferred *string, now time.Time,
) (api.MediaArtifactSet, RetainedMediaResource, []api.HostedImageAttempt, error) {
	b.uploads++
	updated, retained, attempts, err := b.hostSpecificMediaMutator.UploadImages(ctx, release, projections, snapshot, private, ids, host, retry, preferred, now)
	if err != nil {
		return updated, retained, attempts, err
	}
	for _, projection := range projections.Projections {
		attempts[0].TrackerIDs = append(attempts[0].TrackerIDs, projection.TrackerID)
	}
	for index, id := range ids {
		hosted := attempts[0].Results[0]
		hosted.ID = api.PublicResourceID(fmt.Sprintf("comparison-hosted-%d", index))
		hosted.Source = string(id)
		hosted.Purpose = api.ScreenshotPurposeComparison
		if index == 0 {
			updated.Artifacts[len(updated.Artifacts)-1] = hosted
			attempts[0].Results[0] = hosted
		} else {
			updated.Artifacts = append(updated.Artifacts, hosted)
			attempts[0].Results = append(attempts[0].Results, hosted)
		}
	}
	return updated, retained, attempts, nil
}

func reviewedComparisonFixture(t *testing.T) api.ComparisonPlan {
	t.Helper()
	plan := api.ComparisonPlan{
		Imported: true,
		Sources: []api.ComparisonSource{
			{
				ID:       "1",
				Label:    "source",
				Imported: true,
			},
			{
				ID:       "2",
				Label:    "encode",
				Imported: true,
			},
		},
		MainSourceID:         "2",
		ReuseMainScreenshots: true,
		Rows: []api.ComparisonRow{{
			ID: "1",
			Cells: []api.ComparisonCell{
				{
					SourceID:      "1",
					ArtifactID:    "source-image",
					ContentSHA256: strings.Repeat("a", 64),
				},
				{
					SourceID:      "2",
					ArtifactID:    "main-image",
					ContentSHA256: strings.Repeat("b", 64),
				},
			},
		}},
	}
	var err error
	plan.Fingerprint, err = comparisons.ReviewFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestComparisonReviewBlocksHostingAndCannotApplyUnreviewedEdits(t *testing.T) {
	module, _, _, current := newDuplicateReuseWorkflow(t)
	builder := &comparisonMediaFake{host: "synthetic"}
	module.mediaBuilder = builder
	plan := reviewedComparisonFixture(t)
	configured := executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Instructions:     api.ComparisonInstructions{Action: "configure", Plan: plan},
	})
	if configured.Media == nil || ComparisonReviewSatisfied(configured.Media.Comparisons) {
		t.Fatal("new comparison images inherited approval")
	}
	media := api.MediaArtifactSetRef{ID: configured.Media.ID, Revision: configured.Media.Revision}
	_, err := module.execute(t.Context(), testOwnerID, UploadMediaImagesCommand{
		WorkflowID:       configured.Workflow.ID,
		ExpectedRevision: configured.Workflow.Revision,
		Media:            media,
		Host:             "synthetic",
		IdempotencyKey:   "unapproved-upload",
	})
	if !errors.Is(err, ErrInvalidTransition) || builder.uploads != 0 {
		t.Fatalf("unapproved rows reached hosting: calls=%d err=%v", builder.uploads, err)
	}
	retained, loadErr := module.Current(t.Context(), testOwnerID, configured.Workflow.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	command, step := planContinuationCommand(api.ContinueReleaseWorkflowRequest{
		Goal:           api.WorkflowGoalDescriptionsReady,
		IdempotencyKey: "unapproved-continuation",
		Intent:         api.WorkflowIntent{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
	}, retained, module.clock.Now())
	if command != nil || step != "comparison-review-required" {
		t.Fatalf("continuation bypassed review: %T, %s", command, step)
	}
	_, err = module.execute(t.Context(), testOwnerID, EditComparisonsCommand{
		WorkflowID:       configured.Workflow.ID,
		ExpectedRevision: configured.Workflow.Revision,
		Media:            &media,
		Instructions:     api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: testFingerprint(t, "stale-review")},
		IdempotencyKey:   "stale-review",
	})
	if !errors.Is(err, ErrInvalidTransition) || builder.edits != 1 {
		t.Fatalf("stale review reached builder: calls=%d err=%v", builder.edits, err)
	}
	plan.Sources[0].Label = "unreviewed label"
	approved := executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       configured.Workflow.ID,
		ExpectedRevision: configured.Workflow.Revision,
		Media:            &media,
		Instructions: api.ComparisonInstructions{
			Action:              "approve",
			ExpectedFingerprint: configured.Media.Comparisons.Fingerprint,
			Plan:                plan,
		},
	})
	if !ComparisonReviewSatisfied(approved.Media.Comparisons) || approved.Media.Comparisons.Sources[0].Label != "source" {
		t.Fatal("approval applied edits that were absent from the reviewed snapshot")
	}
	media = api.MediaArtifactSetRef{ID: approved.Media.ID, Revision: approved.Media.Revision}
	for _, mutation := range []mutation{
		SetMediaSelectionCommand{
			WorkflowID:       approved.Workflow.ID,
			ExpectedRevision: approved.Workflow.Revision,
			Media:            media,
			ArtifactIDs:      []api.PublicResourceID{"source-image"},
			Selected:         false,
			IdempotencyKey:   "comparison-selection",
		},
		ReorderMediaArtifactsCommand{
			WorkflowID:       approved.Workflow.ID,
			ExpectedRevision: approved.Workflow.Revision,
			Media:            media,
			ArtifactIDs:      []api.PublicResourceID{"main-image", "source-image"},
			IdempotencyKey:   "comparison-reorder",
		},
	} {
		if _, err := module.execute(t.Context(), testOwnerID, mutation); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("generic %T changed structured comparison meaning: %v", mutation, err)
		}
	}
	hosted := executeCommand(t, module, UploadMediaImagesCommand{
		WorkflowID:       approved.Workflow.ID,
		ExpectedRevision: approved.Workflow.Revision,
		Media:            media,
		ArtifactIDs:      []api.PublicResourceID{"source-image"},
	})
	if builder.uploads != 1 || !ComparisonReviewSatisfied(hosted.Media.Comparisons) {
		t.Fatal("approved comparison did not retain review through hosting")
	}
	if len(hosted.Media.HostAttempts) != 1 || len(hosted.Media.HostAttempts[0].Results) != 2 {
		t.Fatal("explicit image list omitted selected comparison cells from required hosting")
	}
	media = api.MediaArtifactSetRef{ID: hosted.Media.ID, Revision: hosted.Media.Revision}
	plan = *hosted.Media.Comparisons
	plan.MainSourceID = "1"
	changed := executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       hosted.Workflow.ID,
		ExpectedRevision: hosted.Workflow.Revision,
		Media:            &media,
		Instructions:     api.ComparisonInstructions{Action: "configure", Plan: plan},
	})
	if ComparisonReviewSatisfied(changed.Media.Comparisons) || changed.Media.Comparisons.ApprovedFingerprint != "" {
		t.Fatal("changed mapping retained prior approval")
	}
	deleted := executeCommand(t, module, DeleteMediaArtifactsCommand{
		WorkflowID:       changed.Workflow.ID,
		ExpectedRevision: changed.Workflow.Revision,
		Media:            api.MediaArtifactSetRef{ID: changed.Media.ID, Revision: changed.Media.Revision},
		ArtifactIDs:      []api.PublicResourceID{"source-image"},
	})
	cell := deleted.Media.Comparisons.Rows[0].Cells[0]
	if cell.ArtifactID != "" || cell.ContentSHA256 != "" || len(deleted.Media.Comparisons.Problems) == 0 ||
		ComparisonReviewSatisfied(deleted.Media.Comparisons) {
		t.Fatal("deleted image remained a complete reviewed comparison cell")
	}
	for _, artifact := range deleted.Media.Artifacts {
		if artifact.ID == "source-image" || artifact.Source == "source-image" {
			t.Fatal("deleted comparison image retained a local or hosted association")
		}
	}
}

func TestComparisonMainColumnIsTheOnlyReusableScreenshot(t *testing.T) {
	plan := reviewedComparisonFixture(t)
	plan.ApprovedFingerprint = plan.Fingerprint
	snapshot := api.MediaArtifactSet{
		Comparisons: &plan,
		Artifacts: []api.MediaArtifact{
			{
				ID:       "source-image",
				Kind:     api.MediaArtifactComparison,
				Purpose:  api.ScreenshotPurposeComparison,
				Selected: true,
			},
			{
				ID:       "main-image",
				Kind:     api.MediaArtifactComparison,
				Purpose:  api.ScreenshotPurposeComparison,
				Selected: true,
			},
			{
				ID:       "hosted-main",
				Kind:     api.MediaArtifactHostedImage,
				Purpose:  api.ScreenshotPurposeComparison,
				Source:   "main-image",
				Selected: true,
			},
		},
		HostAttempts: []api.HostedImageAttempt{{
			TrackerIDs: []api.TrackerID{"ALPHA"},
			Results:    []api.MediaArtifact{{ID: "hosted-main"}},
		}},
	}
	projections := []api.TrackerReleaseProjection{{
		TrackerID: "ALPHA",
		Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1},
	}}
	if !ComparisonMediaReviewSatisfied(snapshot) {
		t.Fatal("complete approved comparison cells failed their artifact binding")
	}
	snapshot.Artifacts[0].Selected = false
	if ComparisonMediaReviewSatisfied(snapshot) {
		t.Fatal("partial row selection retained comparison upload authority")
	}
	snapshot.Artifacts[0].Selected = true
	snapshot.Comparisons = nil
	if ComparisonMediaReviewSatisfied(snapshot) {
		t.Fatal("orphan comparison images bypassed review")
	}
	snapshot.Comparisons = &plan
	if HostedScreenshotRequirementsMet(snapshot, projections) {
		t.Fatal("partial comparison hosting satisfied readiness")
	}
	projections[0].Artifacts.ScreenshotCount = 0
	if HostedScreenshotRequirementsMet(snapshot, projections) {
		t.Fatal("zero ordinary screenshot minimum bypassed comparison hosting")
	}
	sourceHosted := api.MediaArtifact{
		ID:       "hosted-source",
		Kind:     api.MediaArtifactHostedImage,
		Purpose:  api.ScreenshotPurposeComparison,
		Source:   "source-image",
		Selected: true,
	}
	snapshot.Artifacts = append(snapshot.Artifacts, sourceHosted)
	snapshot.HostAttempts = append(snapshot.HostAttempts, api.HostedImageAttempt{
		Host:       "other-host",
		TrackerIDs: []api.TrackerID{"ALPHA"},
		Results:    []api.MediaArtifact{sourceHosted},
	})
	if HostedScreenshotRequirementsMet(snapshot, projections) {
		t.Fatal("split-host comparison images satisfied complete set coverage")
	}
	snapshot.HostAttempts[0].Results = append(snapshot.HostAttempts[0].Results, sourceHosted)
	projections[0].Artifacts.ScreenshotCount = 1
	if !HostedScreenshotRequirementsMet(snapshot, projections) {
		t.Fatal("hosted main-column image did not satisfy the screenshot requirement")
	}
	snapshot.ImageHostPreference = new("primary")
	snapshot.HostAttempts[0].Host = "fallback"
	if !HostedScreenshotRequirementsMet(snapshot, projections) {
		t.Fatal("complete fallback-hosted comparison rows failed readiness")
	}
	projections[0].Artifacts.ScreenshotCount = 2
	if HostedScreenshotRequirementsMet(snapshot, projections) {
		t.Fatal("other comparison columns inflated the normal screenshot count")
	}
	projections[0].Artifacts.ScreenshotCount = 1
	plan.ReuseMainScreenshots = false
	if HostedScreenshotRequirementsMet(snapshot, projections) {
		t.Fatal("disabled main-column reuse still satisfied normal screenshots")
	}
	snapshot.ImageHostPreference = new("primary")
	snapshot.Comparisons = nil
	snapshot.Artifacts[0].Kind = api.MediaArtifactScreenshot
	snapshot.Artifacts[0].Purpose = api.ScreenshotPurposeFinal
	snapshot.Artifacts[2].Source = string(snapshot.Artifacts[0].ID)
	snapshot.Artifacts[2].Purpose = api.ScreenshotPurposeFinal
	snapshot.HostAttempts[0].Host = "fallback"
	if !HostedScreenshotRequirementsMet(snapshot, projections) {
		t.Fatal("accepted ordinary fallback upload failed the screenshot requirement")
	}
}

func TestComparisonReprepareRetainsEditedMappingAndRequiresFreshReview(t *testing.T) {
	module, repository, _, current := newDuplicateReuseWorkflow(t)
	builder := &comparisonMediaFake{}
	module.mediaBuilder = builder
	plan := reviewedComparisonFixture(t)
	current = executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Instructions:     api.ComparisonInstructions{Action: "configure", Plan: plan},
	})
	plan = *current.Media.Comparisons
	plan.Sources[0].Label = "corrected source"
	plan.Rows = append(plan.Rows, api.ComparisonRow{ID: "excluded", Excluded: true})
	current = executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Media:            current.Workflow.Media,
		Instructions:     api.ComparisonInstructions{Action: "configure", Plan: plan},
	})
	current = executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Media:            current.Workflow.Media,
		Instructions:     api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: current.Media.Comparisons.Fingerprint},
	})
	prior := *current.Workflow.Media
	current = executeCommand(t, module, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: current.Media.ReleaseRef.SourcePath, Force: true},
	})
	state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Workflow.Media != nil || state.ComparisonMedia == nil || *state.ComparisonMedia != prior {
		t.Fatal("preparation lost retained comparison intent or kept current-generation media authority")
	}
	if _, err := module.private.Get(testOwnerID, current.Workflow.ID, mediaPrivateResourceID(prior.ID), module.clock.Now()); err != nil {
		t.Fatalf("preparation deleted retained comparison bytes: %v", err)
	}
	current = executeCommand(t, module, ProjectTrackersCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{"ALPHA", "BETA"},
	})
	for _, trackerID := range []api.TrackerID{"ALPHA", "BETA"} {
		current = resolveDuplicateReuseRule(t, module, current, trackerID, true)
	}
	current = executeCommand(t, module, PreflightTrackersCommand{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision})
	current = executeCommand(t, module, CheckDuplicatesCommand{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision})
	current, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Continuation.ComparisonRestorePending {
		t.Fatal("continuation omitted retained comparison restoration")
	}
	command, step := planContinuationCommand(api.ContinueReleaseWorkflowRequest{
		Goal:           api.WorkflowGoalMediaReady,
		Intent:         api.WorkflowIntent{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
		IdempotencyKey: "reprepare-comparisons",
	}, current, module.clock.Now())
	if _, ok := command.(RestoreComparisonsCommand); !ok || step != "restore-comparisons" {
		t.Fatalf("continuation skipped retained mapping: %T %s", command, step)
	}
	current = executeCommand(t, module, command)
	current, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if builder.restores != 1 || current.Media == nil || current.Media.Comparisons == nil {
		t.Fatal("comparison restoration did not publish current media")
	}
	restored := current.Media.Comparisons
	if restored.Sources[0].Label != "corrected source" || len(restored.Rows) != 2 || !restored.Rows[1].Excluded ||
		restored.Rows[0].Cells[0].ArtifactID != "source-image" || restored.ApprovedFingerprint != "" {
		t.Fatal("restoration lost edited mapping or carried prior approval")
	}
	if current.Continuation.ComparisonRestorePending || ComparisonReviewSatisfied(restored) {
		t.Fatal("restoration retained pending status or authorized unreviewed images")
	}
	if _, err := module.private.Get(
		testOwnerID,
		current.Workflow.ID,
		mediaPrivateResourceID(prior.ID),
		module.clock.Now(),
	); !errors.Is(
		err,
		ErrPrivateResourceUnavailable,
	) {
		t.Fatalf("restoration leaked superseded private resource: %v", err)
	}
	if _, err := module.execute(t.Context(), testOwnerID, UploadMediaImagesCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Media:            *current.Workflow.Media,
		Host:             "synthetic",
		IdempotencyKey:   "reprepared-upload",
	}); !errors.Is(err, ErrInvalidTransition) || builder.uploads != 0 {
		t.Fatalf("restored comparison bypassed fresh review: %v", err)
	}
	current = executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Media:            current.Workflow.Media,
		Instructions:     api.ComparisonInstructions{Action: "configure"},
	})
	state, err = repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil || state.ComparisonMedia != nil || current.Media.Comparisons != nil {
		t.Fatalf("explicit clear retained comparison intent: %v", err)
	}
}

func TestComparisonLongProcessingAllowsExpiredChecksToRefresh(t *testing.T) {
	for _, goal := range []api.WorkflowGoal{api.WorkflowGoalTrackersAssessed, api.WorkflowGoalDuplicatesDecided} {
		t.Run(string(goal), func(t *testing.T) {
			module, repository, _, current := newDuplicateReuseWorkflow(t)
			module.mediaBuilder = &comparisonMediaFake{}
			module.inputReadiness = readyInputReadinessEvaluator{}
			current = executeCommand(t, module, EditComparisonsCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Instructions:     api.ComparisonInstructions{Action: "configure", Plan: reviewedComparisonFixture(t)},
			})
			if goal == api.WorkflowGoalDuplicatesDecided {
				current = executeCommand(t, module, EditComparisonsCommand{
					WorkflowID:       current.Workflow.ID,
					ExpectedRevision: current.Workflow.Revision,
					Media:            current.Workflow.Media,
					Instructions:     api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: current.Media.Comparisons.Fingerprint},
				})
			}
			current, err := module.Current(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			retained := *current.Workflow.Media
			priorPreflight, priorDupes := current.Preflight.ID, current.Dupes.ID
			clock := &mutableClock{now: module.clock.Now().Add(time.Hour)}
			module.clock = clock
			for attempt := range 4 {
				clock.now = clock.now.Add(time.Second)
				started, err := module.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
					Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
					Goal:           goal,
					Intent:         api.WorkflowIntent{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
					IdempotencyKey: fmt.Sprintf("expired-comparisons-%d", attempt),
				})
				if err != nil {
					t.Fatal(err)
				}
				if started.Operation == nil || current.Operation != nil && started.Operation.ID == current.Operation.ID {
					t.Fatalf("expired check refresh reused completed work at transition %d", attempt)
				}
				waitForWorkflowOperation(t, module, current.Workflow.ID, started.Operation.ID, func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) })
				current, err = module.Current(t.Context(), testOwnerID, current.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
				if current.Preflight != nil && current.Preflight.ID != priorPreflight && current.Preflight.ExpiresAt.After(module.clock.Now()) &&
					(goal == api.WorkflowGoalTrackersAssessed || current.Dupes != nil && current.Dupes.ID != priorDupes) {
					break
				}
			}
			if current.Preflight == nil || current.Preflight.ID == priorPreflight || !current.Preflight.ExpiresAt.After(module.clock.Now()) ||
				goal == api.WorkflowGoalDuplicatesDecided && (current.Dupes == nil || current.Dupes.ID == priorDupes || !current.Dupes.ExpiresAt.After(module.clock.Now())) {
				t.Fatalf("refresh did not replace expired checks: operation=%+v actions=%+v", current.Operation, current.Workflow.RequiredActions)
			}
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.ComparisonMedia == nil || *state.ComparisonMedia != retained || state.Media[retained.ID].Comparisons == nil {
				t.Fatal("refresh discarded retained comparison rows")
			}
			if _, err := module.private.Get(testOwnerID, current.Workflow.ID, mediaPrivateResourceID(retained.ID), module.clock.Now()); err != nil {
				t.Fatalf("refresh lost retained comparison bytes: %v", err)
			}
		})
	}
}

func TestLegacyCompositeComparisonImportsRequireFreshReimport(t *testing.T) {
	module, repository, _, current := newDuplicateReuseWorkflow(t)
	for _, scenario := range []string{"menu only", "legacy comparison", "typed comparison"} {
		t.Run(scenario, func(t *testing.T) {
			session := &compositeUploadSession{
				Version:            compositeUploadSessionVersion,
				RequestFingerprint: testFingerprint(t, "legacy-comparison"),
				ActiveOperationID:  "legacy-operation",
				ManualMedia: compositeUploadManualMedia{Attached: true, Attachments: []api.MediaAttachment{{
					Kind: api.MediaArtifactDVDMenu, Purpose: api.ScreenshotPurposeMenu,
				}}},
			}
			if scenario != "menu only" {
				session.ManualMedia.Attachments[0].Kind = api.MediaArtifactScreenshot
				session.ManualMedia.Attachments[0].Purpose = api.ScreenshotPurposeFinal
			}
			if scenario == "typed comparison" {
				session.ManualMedia.Comparisons = &api.ComparisonInstructions{Action: "configure"}
			}
			state, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
			if err != nil {
				t.Fatal(err)
			}
			state.Composite = session
			if err := module.saveCompositeMetadata(t.Context(), testOwnerID, &state); err != nil {
				t.Fatal(err)
			}
			_, _, _, err = module.currentCompositeUpload(t.Context(), testOwnerID,
				CompositeUploadCommand{WorkflowID: current.Workflow.ID, SessionFingerprint: session.RequestFingerprint}, session.ActiveOperationID)
			if scenario == "legacy comparison" {
				if !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "reimport") {
					t.Fatalf("legacy comparison bypassed row review: %v", err)
				}
			} else if err != nil {
				t.Fatalf("unrelated or typed session was rejected: %v", err)
			}
		})
	}
}
