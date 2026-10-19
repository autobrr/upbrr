// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestImageCollectionReconciliationFencesExactAttempt(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	resolve := func() error {
		return repo.ResolveReleaseWorkflowEffectUnknown(ctx, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, time.Now().UTC())
	}
	if err := resolve(); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("live attempt reconciliation: %v", err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, effect.OwnerID, effect.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	complete := record
	complete.URL, complete.Completed = "https://slow.pics/c/exact", true
	if err := repo.CompleteImageCollection(ctx, binding, complete); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("late completion: %v", err)
	}
	if err := resolve(); err != nil {
		t.Fatal(err)
	}
	next := record
	next.AttemptID, next.EffectID = "next-attempt", "next-effect"
	nextEffect := effect
	nextEffect.EffectID, nextEffect.ScopeID = next.EffectID, api.ImageCollectionEffectScope(next.AttemptID)
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, nextEffect); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, next); err != nil || !acquired {
		t.Fatalf("next reserve: acquired=%v err=%v", acquired, err)
	}
	if err := resolve(); err != nil {
		t.Fatalf("idempotent old confirmation: %v", err)
	}
	if err := repo.ReleaseImageCollection(ctx, binding, record); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("stale release: %v", err)
	}
	if err := repo.CompleteImageCollection(ctx, binding, complete); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("stale completion: %v", err)
	}
	retained, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint)
	if err != nil || retained == nil || retained.AttemptID != next.AttemptID {
		t.Fatalf("replacement lost: %#v err=%v", retained, err)
	}
}

func TestImageCollectionCompletionRetainsSucceededEffect(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	record.URL, record.Completed = "https://slow.pics/c/exact", true
	if err := repo.CompleteImageCollection(ctx, binding, record); err != nil {
		t.Fatal(err)
	}
	latest, err := loadLatestReleaseWorkflowEffect(ctx, repo.db, effect.OwnerID, effect.WorkflowID, effect.Kind, effect.ScopeID)
	if err != nil || latest.Status != api.WorkflowEffectStatusSucceeded {
		t.Fatalf("effect: %#v err=%v", latest, err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(ctx, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, time.Now().UTC()); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("completed reconciliation: %v", err)
	}
	// A legacy split write may leave a completed collection behind an unknown effect.
	if _, err := repo.db.ExecContext(ctx, `UPDATE release_workflow_effects SET status = ? WHERE effect_id = ?`, api.WorkflowEffectStatusUnknown, effect.EffectID); err != nil {
		t.Fatal(err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(ctx, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, time.Now().UTC()); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("completed reservation behind unknown effect: %v", err)
	}
	retained, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint)
	if err != nil || retained == nil || !retained.Completed {
		t.Fatalf("completed cache lost: %#v err=%v", retained, err)
	}
}

func TestImageCollectionRecoveryRejectsOldCoordinator(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	active, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := active.LeaseExpiresAt.Add(time.Second)
	takeover := active
	takeover.Revision++
	takeover.Fence++
	takeover.State = api.ActiveInputRecovering
	takeover.CoordinatorID, takeover.LeaseExpiresAt = "replacement", now.Add(time.Hour)
	if err := repo.CompareAndSwapActiveInput(t.Context(), active, takeover, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReleaseImageCollection(ctx, binding, record); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("old coordinator release: %v", err)
	}
	recovered := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: takeover.CoordinatorID, Fence: takeover.Fence})
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(recovered, effect.OwnerID, effect.WorkflowID, effect.OperationID, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(recovered, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, now); err != nil {
		t.Fatal(err)
	}
}

func TestImageCollectionOrphanEffectDoesNotReleaseUnrelatedReservation(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	unrelated := record
	unrelated.AttemptID, unrelated.EffectID = "unlinked-attempt", ""
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, unrelated); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, effect.OwnerID, effect.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(ctx, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	retained, err := repo.LoadImageCollection(ctx, binding, unrelated.Fingerprint)
	if err != nil || retained == nil || retained.AttemptID != unrelated.AttemptID {
		t.Fatalf("unrelated reservation lost: %#v err=%v", retained, err)
	}
}

func collectionRecoveryFixture(t *testing.T) (*SQLiteRepository, context.Context, api.PreparedMediaBinding, api.ImageCollection, api.ReleaseWorkflowEffectRecord) {
	t.Helper()
	repo := openMigratedTestRepo(t)
	now := time.Now().UTC()
	workflow := workflowStateRecordForTest("collection-recovery", api.WorkflowStatusActive, now, `{"revision":1}`)
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), workflow); err != nil {
		t.Fatal(err)
	}
	active := createIdleActiveInputForTest(t.Context(), t, repo, now, workflow.OwnerID, workflow.WorkflowID, "collection-coordinator")
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence})
	operation := workflowOperationRecordForTest(t, workflow.OwnerID, workflow.WorkflowID, "collection-operation", now)
	if _, _, err := repo.CreateReleaseWorkflowOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	record := api.ImageCollection{
		Fingerprint:        "collection-fingerprint",
		ContentFingerprint: "collection-content",
		AttemptID:          "collection-attempt",
		EffectID:           "collection-effect",
		Host:               "slowpics",
		Purpose:            api.ScreenshotPurposeComparison,
		Category:           "Comps",
		Label:              "Set",
		AccountScope:       "anonymous",
		Members:            []api.ImageCollectionMember{{Identity: "hash", Label: "Image"}},
	}
	effect := api.ReleaseWorkflowEffectRecord{
		OwnerID:             workflow.OwnerID,
		WorkflowID:          workflow.WorkflowID,
		OperationID:         operation.OperationID,
		EffectID:            record.EffectID,
		Kind:                string(api.WorkflowExternalEffectImageHosting),
		ScopeID:             api.ImageCollectionEffectScope(record.AttemptID),
		SemanticFingerprint: "collection-semantic",
		Status:              api.WorkflowEffectStatusStarted,
		StartedAt:           now,
		UpdatedAt:           now,
	}
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	return repo, ctx, testPreparedMediaBinding(filepath.Join(t.TempDir(), "source.mkv")), record, effect
}

func TestImageCollectionLegacyReservationRemainsBlocked(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	legacy := record
	legacy.EffectID = ""
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, legacy); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE image_collections SET record_json = json_remove(record_json, '$.AttemptID')`); err != nil {
		t.Fatal(err)
	}
	pending, acquired, err := repo.ReserveImageCollection(ctx, binding, record)
	if err != nil || acquired || pending == nil || pending.AttemptID != "" {
		t.Fatalf("legacy conflict: %#v acquired=%v err=%v", pending, acquired, err)
	}
	legacy.URL, legacy.Completed = "https://slow.pics/c/legacy", true
	if err := repo.CompleteImageCollection(ctx, binding, legacy); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("legacy completion: %v", err)
	}
	if err := repo.ReleaseImageCollection(ctx, binding, legacy); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
		t.Fatalf("legacy release: %v", err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, effect.OwnerID, effect.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(ctx, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	retained, err := repo.LoadImageCollection(ctx, binding, legacy.Fingerprint)
	if err != nil || retained == nil || retained.AttemptID != "" {
		t.Fatalf("legacy fence lost: %#v err=%v", retained, err)
	}
}

func TestImageCollectionKnownFailureReleasesAtomically(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	if err := repo.ReleaseImageCollection(ctx, binding, record); err != nil {
		t.Fatal(err)
	}
	latest, err := loadLatestReleaseWorkflowEffect(ctx, repo.db, effect.OwnerID, effect.WorkflowID, effect.Kind, effect.ScopeID)
	if err != nil || latest.Status != api.WorkflowEffectStatusFailed {
		t.Fatalf("effect: %#v err=%v", latest, err)
	}
	retained, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint)
	if err != nil || retained != nil {
		t.Fatalf("known-failed reservation retained: %#v err=%v", retained, err)
	}
}

func TestUnknownCollectionEffectInventoryIsOwnerAndStatusScoped(t *testing.T) {
	repo, ctx, _, _, effect := collectionRecoveryFixture(t)
	if effects, err := repo.ListUnknownCollectionEffects(ctx, effect.OwnerID, effect.WorkflowID); err != nil || len(effects) != 0 {
		t.Fatalf("started effects offered: %#v err=%v", effects, err)
	}
	unrelated := effect
	unrelated.EffectID, unrelated.ScopeID = "ordinary-effect", "ordinary-image"
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, unrelated); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, effect.OwnerID, effect.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	effects, err := repo.ListUnknownCollectionEffects(ctx, effect.OwnerID, effect.WorkflowID)
	if err != nil || len(effects) != 1 || effects[0].EffectID != effect.EffectID {
		t.Fatalf("unknown collection inventory: %#v err=%v", effects, err)
	}
	if effects, err := repo.ListUnknownCollectionEffects(ctx, "other-owner", effect.WorkflowID); err != nil || len(effects) != 0 {
		t.Fatalf("foreign owner effects: %#v err=%v", effects, err)
	}
	if effects, err := repo.ListUnknownCollectionEffects(ctx, effect.OwnerID, "other-workflow"); err != nil || len(effects) != 0 {
		t.Fatalf("foreign workflow effects: %#v err=%v", effects, err)
	}
}

func TestImageCollectionConcurrentConfirmationsPreserveReplacement(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, effect.OwnerID, effect.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	confirmTogether := func() {
		t.Helper()
		ready := make(chan struct{}, 2)
		start := make(chan struct{})
		results := make(chan error, 2)
		for range 2 {
			go func() {
				ready <- struct{}{}
				<-start
				results <- repo.ResolveReleaseWorkflowEffectUnknown(ctx, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, time.Now().UTC())
			}()
		}
		<-ready
		<-ready
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				t.Errorf("concurrent confirmation: %v", err)
			}
		}
	}
	confirmTogether()
	if t.Failed() {
		return
	}
	latest, err := loadLatestReleaseWorkflowEffect(ctx, repo.db, effect.OwnerID, effect.WorkflowID, effect.Kind, effect.ScopeID)
	if err != nil || latest.Status != api.WorkflowEffectStatusFailed {
		t.Fatalf("confirmed effect: %#v err=%v", latest, err)
	}
	if retained, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint); err != nil || retained != nil {
		t.Fatalf("confirmed reservation retained: %#v err=%v", retained, err)
	}
	replacement := record
	replacement.AttemptID, replacement.EffectID = "replacement-attempt", "replacement-effect"
	nextEffect := effect
	nextEffect.EffectID, nextEffect.ScopeID = replacement.EffectID, api.ImageCollectionEffectScope(replacement.AttemptID)
	if _, _, err := repo.BeginReleaseWorkflowEffect(ctx, nextEffect); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, replacement); err != nil || !acquired {
		t.Fatalf("replacement reserve: acquired=%v err=%v", acquired, err)
	}
	confirmTogether()
	retained, err := repo.LoadImageCollection(ctx, binding, replacement.Fingerprint)
	if err != nil || retained == nil || retained.AttemptID != replacement.AttemptID || retained.EffectID != replacement.EffectID {
		t.Fatalf("concurrent stale confirmations lost replacement: %#v err=%v", retained, err)
	}
	latest, err = loadLatestReleaseWorkflowEffect(ctx, repo.db, nextEffect.OwnerID, nextEffect.WorkflowID, nextEffect.Kind, nextEffect.ScopeID)
	if err != nil || latest.Status != api.WorkflowEffectStatusStarted {
		t.Fatalf("replacement effect changed: %#v err=%v", latest, err)
	}
}

func TestImageCollectionConfirmationRollsBackReservationDelete(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, effect.OwnerID, effect.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Fail only after the reservation has been deleted inside the transaction.
	if _, err := repo.db.ExecContext(ctx, `CREATE TRIGGER fail_collection_effect_confirmation
  BEFORE UPDATE OF status ON release_workflow_effects
  WHEN OLD.effect_id = 'collection-effect' AND OLD.status = 'unknown' AND NEW.status = 'failed'
   AND NOT EXISTS (SELECT 1 FROM image_collections WHERE json_extract(record_json, '$.EffectID') = OLD.effect_id)
  BEGIN SELECT RAISE(ABORT, 'synthetic collection effect update failure'); END`); err != nil {
		t.Fatal(err)
	}
	err := repo.ResolveReleaseWorkflowEffectUnknown(ctx, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "synthetic collection effect update failure") {
		t.Fatalf("expected injected effect update failure: %v", err)
	}
	retained, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint)
	if err != nil || retained == nil || retained.AttemptID != record.AttemptID || retained.EffectID != record.EffectID || retained.Completed {
		t.Fatalf("rollback lost reservation: %#v err=%v", retained, err)
	}
	latest, err := loadLatestReleaseWorkflowEffect(ctx, repo.db, effect.OwnerID, effect.WorkflowID, effect.Kind, effect.ScopeID)
	if err != nil || latest.Status != api.WorkflowEffectStatusUnknown {
		t.Fatalf("rollback changed effect: %#v err=%v", latest, err)
	}
	if _, err := repo.db.ExecContext(ctx, `DROP TRIGGER fail_collection_effect_confirmation`); err != nil {
		t.Fatal(err)
	}
	if err := repo.ResolveReleaseWorkflowEffectUnknown(ctx, effect.OwnerID, effect.WorkflowID, api.WorkflowExternalEffectImageHosting, effect.ScopeID, time.Now().UTC()); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}
