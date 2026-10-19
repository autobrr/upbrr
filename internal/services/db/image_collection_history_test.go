// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestHistoryPurgeResetsUnknownCollectionAttempt(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE release_workflow_states SET state_json = json_set(state_json, '$.SourcePath', ?) WHERE owner_id = ? AND workflow_id = ?`, binding.SourcePath, effect.OwnerID, effect.WorkflowID); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, effect.OwnerID, effect.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE release_workflow_operations SET status = 'failed' WHERE owner_id = ? AND workflow_id = ?`, effect.OwnerID, effect.WorkflowID); err != nil {
		t.Fatal(err)
	}
	slot, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CloseIdleActiveInput(ctx, slot, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := repo.PurgeContentData(ctx, binding.SourcePath); err != nil {
		t.Fatal(err)
	}
	if stored, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint); err != nil || stored != nil {
		t.Fatalf("history deletion retained collection: %#v err=%v", stored, err)
	}
	if _, err := repo.LoadReleaseWorkflowState(ctx, effect.OwnerID, effect.WorkflowID); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
		t.Fatalf("history deletion retained workflow: %v", err)
	}
	next := record
	next.AttemptID, next.EffectID = "fresh-attempt", ""
	if _, acquired, err := repo.ReserveImageCollection(t.Context(), binding, next); err != nil || !acquired {
		t.Fatalf("fresh attempt remains blocked: acquired=%v err=%v", acquired, err)
	}
	record.URL, record.Completed = "https://slow.pics/c/late", true
	if err := repo.CompleteImageCollection(ctx, binding, record); err == nil {
		t.Fatal("deleted workflow completed its old attempt")
	}
	if stored, err := repo.LoadImageCollection(t.Context(), binding, next.Fingerprint); err != nil || stored == nil || stored.AttemptID != next.AttemptID || stored.Completed {
		t.Fatalf("late completion changed replacement: %#v err=%v", stored, err)
	}
}

func TestHistoryPurgeResetsOnlySelectedSourceCollections(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "completed"}[completed], func(t *testing.T) {
			repo := openMigratedTestRepo(t)
			binding := testPreparedMediaBinding(filepath.Join(t.TempDir(), "Example.mkv"))
			other := testPreparedMediaBinding(filepath.Join(t.TempDir(), "Other.mkv"))
			record := api.ImageCollection{
				Fingerprint:        "set",
				ContentFingerprint: "content",
				AttemptID:          "old-attempt",
				Host:               "slowpics",
				Purpose:            api.ScreenshotPurposeComparison,
				Category:           "Comps",
				Label:              "Set",
				AccountScope:       "anonymous",
				Members:            []api.ImageCollectionMember{{Identity: "hash", Label: "Image"}},
			}
			for _, source := range []api.PreparedMediaBinding{binding, other} {
				if _, acquired, err := repo.ReserveImageCollection(t.Context(), source, record); err != nil || !acquired {
					t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
				}
				if completed {
					done := record
					done.Completed, done.URL = true, "https://slow.pics/c/example"
					if err := repo.CompleteImageCollection(t.Context(), source, done); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := repo.PurgeContentData(t.Context(), binding.SourcePath); err != nil {
				t.Fatal(err)
			}
			if stored, err := repo.LoadImageCollection(t.Context(), binding, record.Fingerprint); err != nil || stored != nil {
				t.Fatalf("deleted source retained collection: %#v err=%v", stored, err)
			}
			if stored, err := repo.LoadImageCollection(t.Context(), other, record.Fingerprint); err != nil || stored == nil || stored.Completed != completed {
				t.Fatalf("other source changed: %#v err=%v", stored, err)
			}
			next := record
			next.AttemptID = "new-attempt"
			if _, acquired, err := repo.ReserveImageCollection(t.Context(), binding, next); err != nil || !acquired {
				t.Fatalf("new attempt: acquired=%v err=%v", acquired, err)
			}
			record.Completed, record.URL = true, "https://slow.pics/c/late"
			if err := repo.CompleteImageCollection(t.Context(), binding, record); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
				t.Fatalf("stale completion: %v", err)
			}
			if err := repo.ReleaseImageCollection(t.Context(), binding, record); !errors.Is(err, api.ErrReleaseWorkflowEffectConflict) {
				t.Fatalf("stale release: %v", err)
			}
		})
	}
}

func TestHistoryPurgeRejectsLiveCollectionWorkflow(t *testing.T) {
	repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
	if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE release_workflow_states SET state_json = json_set(state_json, '$.SourcePath', ?) WHERE owner_id = ? AND workflow_id = ?`, binding.SourcePath, effect.OwnerID, effect.WorkflowID); err != nil {
		t.Fatal(err)
	}
	if err := repo.PurgeContentData(ctx, binding.SourcePath); err == nil {
		t.Fatal("deleted live collection workflow")
	}
	if stored, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint); err != nil || stored == nil || stored.AttemptID != record.AttemptID {
		t.Fatalf("live reservation changed: %#v err=%v", stored, err)
	}
	if _, err := repo.LoadReleaseWorkflowState(ctx, effect.OwnerID, effect.WorkflowID); err != nil {
		t.Fatalf("live workflow changed: %v", err)
	}
}

func TestHistoryPurgeCollectionResetRollsBackWithHistory(t *testing.T) {
	repo := openMigratedTestRepo(t)
	binding := testPreparedMediaBinding(filepath.Join(t.TempDir(), "Example.mkv"))
	record := api.ImageCollection{
		Fingerprint:        "set",
		ContentFingerprint: "content",
		AttemptID:          "attempt",
	}
	if _, acquired, err := repo.ReserveImageCollection(t.Context(), binding, record); err != nil || !acquired {
		t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
	}
	if err := repo.Save(t.Context(), FileMetadata{Path: binding.SourcePath, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `CREATE TRIGGER reject_history_delete BEFORE DELETE ON file_metadata BEGIN SELECT RAISE(ABORT, 'test rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.PurgeContentData(t.Context(), binding.SourcePath); err == nil {
		t.Fatal("history deletion unexpectedly succeeded")
	}
	if stored, err := repo.LoadImageCollection(t.Context(), binding, record.Fingerprint); err != nil || stored == nil || stored.AttemptID != record.AttemptID {
		t.Fatalf("failed history deletion released reservation: %#v err=%v", stored, err)
	}
}

func TestAutomaticCleanupPreservesPendingCollectionAuthority(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "completed"}[completed], func(t *testing.T) {
			repo, ctx, binding, record, effect := collectionRecoveryFixture(t)
			if _, acquired, err := repo.ReserveImageCollection(ctx, binding, record); err != nil || !acquired {
				t.Fatalf("reserve: acquired=%v err=%v", acquired, err)
			}
			if completed {
				record.Completed, record.URL = true, "https://slow.pics/c/completed"
				if err := repo.CompleteImageCollection(ctx, binding, record); err != nil {
					t.Fatal(err)
				}
			} else if err := repo.MarkReleaseWorkflowOperationEffectsUnknown(ctx, effect.OwnerID, effect.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.db.ExecContext(ctx, `UPDATE release_workflow_states SET status = 'failed' WHERE owner_id = ? AND workflow_id = ?`, effect.OwnerID, effect.WorkflowID); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.db.ExecContext(ctx, `UPDATE release_workflow_operations SET status = 'failed' WHERE owner_id = ? AND workflow_id = ?`, effect.OwnerID, effect.WorkflowID); err != nil {
				t.Fatal(err)
			}
			slot, err := repo.LoadActiveInput(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.CloseIdleActiveInput(ctx, slot, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.db.ExecContext(ctx, `UPDATE release_workflow_states SET state_json = json_set(state_json, '$.SourcePath', ?) WHERE owner_id = ? AND workflow_id = ?`, binding.SourcePath, effect.OwnerID, effect.WorkflowID); err != nil {
				t.Fatal(err)
			}
			paths, _, err := repo.ListOrphanedHistoryPaths(ctx)
			if err != nil || (!completed && len(paths) != 0) || (completed && len(paths) != 1) {
				t.Fatalf("startup cleanup candidates: count=%d completed=%v err=%v", len(paths), completed, err)
			}
			deleted, err := repo.DeleteTerminalReleaseWorkflowStatesBefore(ctx, time.Now().UTC().Add(time.Hour))
			want := int64(0)
			if completed {
				want = 1
			}
			if err != nil || deleted != want {
				t.Fatalf("audit retention deleted=%d want=%d err=%v", deleted, want, err)
			}
			if stored, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint); err != nil || stored == nil || stored.Completed != completed {
				t.Fatalf("audit retention changed collection: %#v err=%v", stored, err)
			}
			if !completed {
				if effects, err := repo.ListUnknownCollectionEffects(ctx, effect.OwnerID, effect.WorkflowID); err != nil || len(effects) != 1 || effects[0].EffectID != record.EffectID {
					t.Fatalf("audit retention lost recovery authority: %#v err=%v", effects, err)
				}
				if err := repo.PurgeContentData(ctx, binding.SourcePath); err != nil {
					t.Fatal(err)
				}
				if stored, err := repo.LoadImageCollection(ctx, binding, record.Fingerprint); err != nil || stored != nil {
					t.Fatalf("explicit deletion retained collection: %#v err=%v", stored, err)
				}
			}
		})
	}
}
