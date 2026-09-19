// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicateCompletionRestoresMediaUnderCurrentTrackerAuthority(t *testing.T) {
	t.Parallel()
	for _, mode := range []TrackerDecisionMode{TrackerDecisionModeWebUIControls, TrackerDecisionModePostDupeGate} {
		t.Run(string(mode), func(t *testing.T) {
			restorer := &compatibleMediaRestorerFake{testing: t}
			module, repository := newTestModule(t, testPreparer(),
				WithTrackerPreflightBuilder(readyPreflightBuilder(t)),
				WithDupeAssessmentBuilder(readyDupeBuilder(t)),
				WithMediaArtifactBuilder(restorer),
			)
			result := executeCommand(t, module, CreateWorkflowCommand{TrackerDecisionMode: mode})
			result = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       result.Workflow.ID,
				ExpectedRevision: result.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Release.mkv")},
			})
			result = executeTestPublication(t, module, trackerContextPublication{
				WorkflowID:       result.Workflow.ID,
				ExpectedRevision: result.Workflow.Revision,
				Catalog:          testCatalog(t),
				Runtime:          testRuntime(t),
				Selection:        api.TrackerSelection{TrackerIDs: []api.TrackerID{"ALPHA", "BETA"}},
			})
			result = executeTestPublication(t, module, projectionSetPublication{
				WorkflowID:       result.Workflow.ID,
				ExpectedRevision: result.Workflow.Revision,
				Snapshot:         testProjectionSet(t),
			})
			result = executeCommand(t, module, PreflightTrackersCommand{
				WorkflowID: result.Workflow.ID, ExpectedRevision: result.Workflow.Revision,
			})
			command := CheckDuplicatesCommand{
				WorkflowID:       result.Workflow.ID,
				ExpectedRevision: result.Workflow.Revision,
				IdempotencyKey:   "restore-on-dupes",
			}
			result = executeCommand(t, module, command)
			wantTrackers := []api.TrackerID{"ALPHA", "BETA"}
			if mode == TrackerDecisionModePostDupeGate {
				if result.Media != nil || restorer.calls != 0 {
					t.Fatal("media restored before explicit tracker approval")
				}
				state, err := repository.Load(t.Context(), testOwnerID, result.Workflow.ID)
				if err != nil {
					t.Fatal(err)
				}
				action, _, err := projectedTrackerApprovalAction(&state, module.clock.Now())
				if err != nil || action == nil {
					t.Fatalf("approval action: %v, %v", action, err)
				}
				wantTrackers = []api.TrackerID{"BETA"}
				result = executeCommand(t, module, ApproveTrackersCommand{
					WorkflowID:       result.Workflow.ID,
					ExpectedRevision: result.Workflow.Revision,
					Approval: api.TrackerApproval{
						ActionID:         action.ID,
						Dupes:            *result.Workflow.Dupes,
						InputFingerprint: result.Dupes.InputFingerprint,
						TrackerIDs:       wantTrackers,
					},
				})
			} else {
				replayed := executeCommand(t, module, command)
				if replayed.Media == nil || replayed.Media.ID != result.Media.ID || restorer.calls != 1 {
					t.Fatal("duplicate command replay repeated restoration")
				}
			}
			if restorer.calls != 1 || !slices.Equal(restorer.trackers, wantTrackers) {
				t.Fatalf("restore calls=%d trackers=%v", restorer.calls, restorer.trackers)
			}
			if result.Media == nil || result.Media.WorkflowID != result.Workflow.ID || result.Media.Release != *result.Workflow.Release ||
				result.Media.ProjectionSet != *result.Workflow.TrackerProjections || result.Media.Artifacts[0].Selected || result.Media.Artifacts[0].Order != 7 {
				t.Fatalf("restored media lost current authority or selection/order: %#v", result.Media)
			}
			if _, err := module.private.Get(testOwnerID, result.Workflow.ID, mediaPrivateResourceID(result.Media.ID), module.clock.Now()); err != nil {
				t.Fatalf("restored media unavailable in new workflow: %v", err)
			}
			attempt := result.Media.HostAttempts[0]
			if attempt.Media != *result.Workflow.Media || !attempt.AttemptedAt.Equal(result.Media.CreatedAt) {
				t.Fatalf("restored host coverage lost current media lineage: %#v", attempt)
			}
		})
	}
}

type compatibleMediaRestorerFake struct {
	testing  *testing.T
	calls    int
	trackers []api.TrackerID
}

func (*compatibleMediaRestorerFake) Build(context.Context, api.ReleaseRef, api.TrackerReleaseProjectionSet, api.MediaCaptureInstructions, time.Time) (api.MediaArtifactSet, any, error) {
	return api.MediaArtifactSet{}, nil, errors.New("restoration must not capture images")
}

func (f *compatibleMediaRestorerFake) RestoreCompatible(_ context.Context, _ api.ReleaseRef, projections api.TrackerReleaseProjectionSet, _ time.Time) (api.MediaArtifactSet, RetainedMediaResource, error) {
	f.calls++
	for _, projection := range projections.Projections {
		f.trackers = append(f.trackers, projection.TrackerID)
	}
	requirements, err := mediaRequirementsFingerprint(projections.Projections)
	if err != nil {
		return api.MediaArtifactSet{}, nil, err
	}
	return api.MediaArtifactSet{
		CaptureFingerprint:      testFingerprint(f.testing, "restored-media"),
		RequirementsFingerprint: requirements,
		Artifacts: []api.MediaArtifact{{
			ID:      "restored-screen",
			Kind:    api.MediaArtifactScreenshot,
			Purpose: api.ScreenshotPurposeFinal,
			Order:   7,
		}},
		HostAttempts: []api.HostedImageAttempt{{
			ID:     "restored-host",
			Host:   "images.example",
			Status: api.StageStatusCompleted,
		}},
		Status: api.StageStatusCompleted,
	}, durableReusableMediaResource{}, nil
}

func TestOpenInputReconcilesCommittedMediaAfterPostSaveRecorderFailure(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repository := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	firstPath := writeActiveInputRecoverySource(t, "first.mkv", "first source")
	switchPath := writeActiveInputRecoverySource(t, "switch.mkv", "switched source")
	verifier := &hashingActiveInputVerifier{}
	module := newActiveInputRecoveryModule(t, persistent, repository, verifier, clock, "media-reuse")
	recorder := &reusableMediaRecorderFake{err: errors.New("post-save media association failure")}
	module.mediaBuilder = recorder

	opened, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: firstPath},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open first input: %v", err)
	}
	media := seedReusableMedia(t, module, persistent, opened.WorkflowID, true)
	if err := recorder.RecordReusableMedia(ctx, media, reusableMediaRetainedValue); err == nil {
		t.Fatal("post-save recorder failure = nil")
	}

	switched, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
		ExpectedRevision: opened.Revision,
		Input:            api.PrepareInput{SourcePath: switchPath},
		IdempotencyKey:   "switch-after-recorder-failure",
	})
	if err != nil {
		t.Fatalf("switch after post-save recorder failure: %v", err)
	}
	if switched.WorkflowID == opened.WorkflowID || verifier.calls != 2 {
		t.Fatalf("switched input = %#v verifier calls=%d", switched, verifier.calls)
	}
	if len(recorder.snapshots) != 2 || recorder.retained[1] != reusableMediaRetainedValue {
		t.Fatalf("recorder calls = %#v retained = %#v", recorder.snapshots, recorder.retained)
	}
	recorded := recorder.snapshots[1]
	if len(recorded.Artifacts) != 2 || !recorded.Artifacts[0].Selected || recorded.Artifacts[0].Order != 2 ||
		recorded.Artifacts[1].Selected || recorded.Artifacts[1].Order != 7 || recorded.Artifacts[1].URL != "https://images.invalid/retained.png" {
		t.Fatalf("reconciled media selection = %#v", recorded.Artifacts)
	}
	module.private.Delete(testOwnerID, opened.WorkflowID, mediaPrivateResourceID(media.ID))
	if err := module.reconcileReusableMedia(ctx, testOwnerID, opened.WorkflowID); err != nil {
		t.Fatalf("reconcile already committed media without retained resource: %v", err)
	}
}

func TestCommandReceiptReplayRetriesPostSaveReusableMediaRecording(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repository := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &hashingActiveInputVerifier{}
	module := newActiveInputRecoveryModule(t, persistent, repository, verifier, clock, "media-receipt-replay")
	vaultRoot := t.TempDir()
	vault, err := NewPrivateArtifactVault(vaultRoot, durableReusableMediaCodec())
	if err != nil {
		t.Fatalf("create private vault: %v", err)
	}
	module.private = vault
	recorder := &reusableMediaRecorderFake{err: errors.New("post-save media association failure")}
	module.mediaBuilder = recorder
	path := writeActiveInputRecoverySource(t, "source.mkv", "source bytes")
	opened, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: path},
		IdempotencyKey: "open-input",
	})
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	priorRevision := state.Workflow.Revision
	media := api.MediaArtifactSet{
		ID:         "media-receipt",
		WorkflowID: opened.WorkflowID,
		Revision:   priorRevision + 1,
		Artifacts: []api.MediaArtifact{
			{
				ID:       "kept",
				Kind:     api.MediaArtifactScreenshot,
				Selected: true,
				Order:    7,
			},
			{
				ID:       "removed",
				Kind:     api.MediaArtifactScreenshot,
				Selected: false,
				Order:    2,
			},
		},
	}
	state.Workflow.Revision++
	state.Workflow.UpdatedAt = clock.Now()
	state.Workflow.Media = &api.MediaArtifactSetRef{ID: media.ID, Revision: media.Revision}
	state.Media[media.ID] = media
	command := SetMediaSelectionCommand{
		WorkflowID:       opened.WorkflowID,
		ExpectedRevision: state.Workflow.Revision,
		Media:            *state.Workflow.Media,
		ArtifactIDs:      []api.PublicResourceID{"kept"},
		Selected:         true,
		IdempotencyKey:   "media-selection",
	}
	fingerprint, err := acceptedCommandFingerprint(command)
	if err != nil {
		t.Fatalf("fingerprint media command: %v", err)
	}
	state.Receipts[commandReceiptKey(command.commandName(), command.IdempotencyKey, fingerprint)] = commandReceipt{
		Fingerprint: fingerprint,
		Result: CommandResult{
			Workflow: state.Workflow,
			Media:    &media,
		},
	}
	retainedValue := durableReusableMediaResource{}
	if err := module.private.Put(testOwnerID, opened.WorkflowID, mediaPrivateResourceID(media.ID), retainedValue, clock.Now().Add(time.Hour)); err != nil {
		t.Fatalf("retain media resource: %v", err)
	}
	active, err := module.activeMutationContext(ctx, testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatalf("admit receipt state: %v", err)
	}
	if err := persistent.Save(active, testOwnerID, priorRevision, state); err != nil {
		t.Fatalf("save receipt state: %v", err)
	}
	if err := recorder.RecordReusableMedia(ctx, media, retainedValue); err == nil {
		t.Fatal("initial post-save media record error = nil")
	}

	restartedVault, err := NewPrivateArtifactVault(vaultRoot, durableReusableMediaCodec())
	if err != nil {
		t.Fatalf("reopen private vault: %v", err)
	}
	restarted := newActiveInputRecoveryModule(t, persistent, repository, verifier, clock, "media-receipt-replay")
	restarted.private = restartedVault
	restarted.mediaBuilder = recorder
	replayed, err := restarted.Execute(ctx, testOwnerID, command)
	if err != nil {
		t.Fatalf("replay media receipt: %v", err)
	}
	if replayed.Media == nil || replayed.Media.Revision != media.Revision || !recorder.committed || len(recorder.snapshots) != 2 {
		t.Fatalf("replayed media = %#v recorder = %#v", replayed.Media, recorder)
	}
	if got := recorder.snapshots[1]; len(got.Artifacts) != 2 || got.Artifacts[0].Order != 7 || !got.Artifacts[0].Selected ||
		got.Artifacts[1].Order != 2 || got.Artifacts[1].Selected {
		t.Fatalf("replayed media selection/order = %#v", got.Artifacts)
	}
	if _, ok := recorder.retained[1].(durableReusableMediaResource); !ok {
		t.Fatalf("replayed retained media = %T, want durable retained media", recorder.retained[1])
	}
}

func TestCommandReceiptReplayDoesNotRecordSupersededMedia(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repository := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &hashingActiveInputVerifier{}
	module := newActiveInputRecoveryModule(t, persistent, repository, verifier, clock, "media-receipt-superseded")
	recorder := &reusableMediaRecorderFake{err: errors.New("post-save media association failure")}
	module.mediaBuilder = recorder
	path := writeActiveInputRecoverySource(t, "source.mkv", "source bytes")
	opened, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: path},
		IdempotencyKey: "open-input",
	})
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	state, err := persistent.Load(ctx, testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatalf("load workflow: %v", err)
	}
	priorRevision := state.Workflow.Revision
	oldMedia := api.MediaArtifactSet{
		ID:         "media-old",
		WorkflowID: opened.WorkflowID,
		Revision:   priorRevision + 1,
		Artifacts: []api.MediaArtifact{{
			ID:       "artifact",
			Kind:     api.MediaArtifactScreenshot,
			Selected: false,
			Order:    1,
		}},
	}
	oldCommand := SetMediaSelectionCommand{
		WorkflowID:       opened.WorkflowID,
		ExpectedRevision: priorRevision,
		Media:            api.MediaArtifactSetRef{ID: oldMedia.ID, Revision: oldMedia.Revision},
		ArtifactIDs:      []api.PublicResourceID{"artifact"},
		Selected:         false,
		IdempotencyKey:   "old-media-selection",
	}
	fingerprint, err := acceptedCommandFingerprint(oldCommand)
	if err != nil {
		t.Fatalf("fingerprint old media command: %v", err)
	}
	state.Workflow.Revision = oldMedia.Revision
	state.Workflow.UpdatedAt = clock.Now()
	state.Workflow.Media = &api.MediaArtifactSetRef{ID: oldMedia.ID, Revision: oldMedia.Revision}
	state.Media[oldMedia.ID] = oldMedia
	state.Receipts[commandReceiptKey(oldCommand.commandName(), oldCommand.IdempotencyKey, fingerprint)] = commandReceipt{
		Fingerprint: fingerprint,
		Result:      CommandResult{Media: &oldMedia},
	}
	if err := module.private.Put(testOwnerID, opened.WorkflowID, mediaPrivateResourceID(oldMedia.ID), reusableMediaRetainedValue, clock.Now().Add(time.Hour)); err != nil {
		t.Fatalf("retain old media resource: %v", err)
	}
	active, err := module.activeMutationContext(ctx, testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatalf("admit superseded receipt state: %v", err)
	}
	if err := persistent.Save(active, testOwnerID, priorRevision, state); err != nil {
		t.Fatalf("save superseded receipt state: %v", err)
	}
	state, err = persistent.Load(ctx, testOwnerID, opened.WorkflowID)
	if err != nil {
		t.Fatalf("reload old media state: %v", err)
	}
	newMedia := api.MediaArtifactSet{
		ID:         "media-new",
		WorkflowID: opened.WorkflowID,
		Revision:   oldMedia.Revision + 1,
		Artifacts: []api.MediaArtifact{{
			ID:       "artifact",
			Kind:     api.MediaArtifactScreenshot,
			Selected: true,
			Order:    7,
		}},
	}
	state.Workflow.Revision = newMedia.Revision
	state.Workflow.UpdatedAt = clock.Now()
	state.Workflow.Media = &api.MediaArtifactSetRef{ID: newMedia.ID, Revision: newMedia.Revision}
	state.Media[newMedia.ID] = newMedia
	if err := persistent.Save(active, testOwnerID, oldMedia.Revision, state); err != nil {
		t.Fatalf("save newer media state: %v", err)
	}
	if err := recorder.RecordReusableMedia(ctx, oldMedia, reusableMediaRetainedValue); err == nil {
		t.Fatal("initial old-media recorder failure = nil")
	}
	if err := recorder.RecordReusableMedia(ctx, newMedia, reusableMediaRetainedValue); err != nil {
		t.Fatalf("record newer media: %v", err)
	}

	replayed, err := module.Execute(ctx, testOwnerID, oldCommand)
	if err != nil {
		t.Fatalf("replay superseded receipt: %v", err)
	}
	if replayed.Media == nil || replayed.Media.ID != oldMedia.ID || len(recorder.snapshots) != 2 {
		t.Fatalf("replayed superseded receipt = %#v recorder = %#v", replayed.Media, recorder)
	}
	if got := recorder.snapshots[1]; got.ID != newMedia.ID || len(got.Artifacts) != 1 || !got.Artifacts[0].Selected || got.Artifacts[0].Order != 7 {
		t.Fatalf("newer reusable media was overwritten: %#v", got)
	}
}

func TestOpenInputBlocksSwitchWhenCommittedMediaRetentionIsUnavailable(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	clock := &mutableClock{now: time.Now().UTC()}
	repository := openActiveInputRecoveryRepository(ctx, t)
	persistent, err := NewPersistentRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	firstPath := writeActiveInputRecoverySource(t, "first.mkv", "first source")
	switchPath := writeActiveInputRecoverySource(t, "switch.mkv", "switched source")
	verifier := &hashingActiveInputVerifier{}
	module := newActiveInputRecoveryModule(t, persistent, repository, verifier, clock, "media-retention")
	module.mediaBuilder = &reusableMediaRecorderFake{}

	opened, err := module.OpenInput(ctx, testOwnerID, OpenInputRequest{
		Input:          api.PrepareInput{SourcePath: firstPath},
		IdempotencyKey: "open-first",
	})
	if err != nil {
		t.Fatalf("open first input: %v", err)
	}
	seedReusableMedia(t, module, persistent, opened.WorkflowID, false)

	_, err = module.OpenInput(ctx, testOwnerID, OpenInputRequest{
		ExpectedRevision: opened.Revision,
		Input:            api.PrepareInput{SourcePath: switchPath},
		IdempotencyKey:   "switch-without-retained-media",
	})
	if !errors.Is(err, ErrPrivateResourceUnavailable) {
		t.Fatalf("switch without retained media = %v, want %v", err, ErrPrivateResourceUnavailable)
	}
	slot, err := repository.LoadActiveInput(ctx)
	if err != nil {
		t.Fatalf("load rolled-back active input: %v", err)
	}
	if slot.State != api.ActiveInputActive || slot.WorkflowID != opened.WorkflowID || slot.InputID != opened.InputID || verifier.calls != 1 {
		t.Fatalf("slot after blocked switch = %#v verifier calls=%d", slot, verifier.calls)
	}
}

const reusableMediaRetainedValue = "retained media authority"

const durableReusableMediaResourceKind = "releaseworkflow/test-reusable-media/v1"

type durableReusableMediaResource struct{}

func (durableReusableMediaResource) OpenArtifact(context.Context, api.MediaArtifactSet, api.PublicResourceID) (MediaArtifactContent, error) {
	return MediaArtifactContent{}, errors.New("test reusable media does not expose artifacts")
}

func (value durableReusableMediaResource) DeleteArtifacts(
	context.Context,
	api.MediaArtifactSet,
	[]api.PublicResourceID,
) (RetainedMediaResource, error) {
	return value, nil
}

func (durableReusableMediaResource) MarshalPrivateResource() (string, []byte, error) {
	return durableReusableMediaResourceKind, []byte("durable reusable media"), nil
}

func durableReusableMediaCodec() PrivateResourceCodec {
	return PrivateResourceCodec{
		Kind: durableReusableMediaResourceKind,
		Decode: func(payload []byte) (any, error) {
			if string(payload) != "durable reusable media" {
				return nil, errors.New("invalid durable reusable media payload")
			}
			return durableReusableMediaResource{}, nil
		},
	}
}

func seedReusableMedia(
	t *testing.T,
	module *Module,
	repository *PersistentRepository,
	workflowID api.WorkflowID,
	retain bool,
) api.MediaArtifactSet {
	t.Helper()

	ctx := t.Context()
	state, err := repository.Load(ctx, testOwnerID, workflowID)
	if err != nil {
		t.Fatalf("load workflow to seed media: %v", err)
	}
	expected := state.Workflow.Revision
	media := api.MediaArtifactSet{
		ID:         "media-reuse",
		WorkflowID: workflowID,
		Revision:   expected + 1,
		Artifacts: []api.MediaArtifact{
			{
				ID:       "kept",
				Kind:     api.MediaArtifactScreenshot,
				Selected: true,
				Order:    2,
			},
			{
				ID:       "hosted",
				Kind:     api.MediaArtifactScreenshot,
				Selected: false,
				Order:    7,
				URL:      "https://images.invalid/retained.png",
			},
		},
	}
	state.Workflow.Revision++
	state.Workflow.UpdatedAt = module.clock.Now().UTC()
	state.Workflow.Media = &api.MediaArtifactSetRef{ID: media.ID, Revision: media.Revision}
	state.Media[media.ID] = media
	activeContext, err := module.activeMutationContext(ctx, testOwnerID, workflowID)
	if err != nil {
		t.Fatalf("admit seeded media state: %v", err)
	}
	if err := repository.Save(activeContext, testOwnerID, expected, state); err != nil {
		t.Fatalf("save seeded media state: %v", err)
	}
	if retain {
		if err := module.private.Put(
			testOwnerID,
			workflowID,
			mediaPrivateResourceID(media.ID),
			reusableMediaRetainedValue,
			module.clock.Now().Add(time.Hour),
		); err != nil {
			t.Fatalf("retain seeded media: %v", err)
		}
	}
	return media
}

type reusableMediaRecorderFake struct {
	err            error
	snapshots      []api.MediaArtifactSet
	retained       []any
	committed      bool
	committedMedia map[api.MediaArtifactSetRef]bool
}

func (*reusableMediaRecorderFake) Build(
	context.Context,
	api.ReleaseRef,
	api.TrackerReleaseProjectionSet,
	api.MediaCaptureInstructions,
	time.Time,
) (api.MediaArtifactSet, any, error) {
	return api.MediaArtifactSet{}, nil, nil
}

func (f *reusableMediaRecorderFake) RecordReusableMedia(
	_ context.Context,
	snapshot api.MediaArtifactSet,
	retained any,
) error {
	f.snapshots = append(f.snapshots, snapshot)
	f.retained = append(f.retained, retained)
	err := f.err
	f.err = nil
	if err != nil {
		return err
	}
	f.committed = true
	if f.committedMedia == nil {
		f.committedMedia = make(map[api.MediaArtifactSetRef]bool)
	}
	f.committedMedia[api.MediaArtifactSetRef{ID: snapshot.ID, Revision: snapshot.Revision}] = true
	return nil
}

func (f *reusableMediaRecorderFake) HasReusableMediaCommit(_ context.Context, snapshot api.MediaArtifactSet) (bool, error) {
	return f.committedMedia[api.MediaArtifactSetRef{ID: snapshot.ID, Revision: snapshot.Revision}], nil
}
