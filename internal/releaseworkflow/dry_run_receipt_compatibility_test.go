// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDryRunUploadsReplaysPersistedLegacyReceipts(t *testing.T) {
	t.Parallel()
	for _, enveloped := range []bool{false, true} {
		name := "raw payload"
		if enveloped {
			name = "authority envelope"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo, err := db.Open(filepath.Join(t.TempDir(), "legacy-dry-run.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			persistent, err := NewPersistentRepository(repo)
			if err != nil {
				t.Fatal(err)
			}
			uploads := &uploadPlanBuilderFake{testing: t}
			module, err := New(persistent, NewMemoryPrivateResourceStore(), testPreparer(), WithUploadPlanBuilder(uploads))
			if err != nil {
				t.Fatal(err)
			}
			created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "legacy-dry-run"})
			command := DryRunUploadsCommand{
				WorkflowID:       created.Workflow.ID,
				ExpectedRevision: created.Workflow.Revision,
				TrackerIDs:       []api.TrackerID{"ALPHA"},
				IdempotencyKey:   "legacy-dry-run",
			}
			// These are the persisted shapes before ReviewOnly existed. Do not
			// derive either fixture through the current command implementation.
			fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
				ExpectedRevision api.WorkflowRevision
				NoSeed           bool
				TrackerIDs       []api.TrackerID
			}{command.ExpectedRevision, command.NoSeed, command.TrackerIDs})
			if err != nil {
				t.Fatal(err)
			}
			if enveloped {
				fingerprint, err = api.CanonicalWorkflowFingerprint(struct {
					Command          string
					WorkflowID       api.WorkflowID
					ExpectedRevision api.WorkflowRevision
					Operation        api.OperationKind
					Payload          api.WorkflowFingerprint
				}{"dry_run_uploads", command.WorkflowID, command.ExpectedRevision, api.OperationKindUploadDryRun, fingerprint})
				if err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			_, _, err = persistent.CreateOperation(t.Context(), api.ReleaseWorkflowOperationRecord{
				OwnerID:            testOwnerID,
				WorkflowID:         command.WorkflowID,
				OperationID:        "prior-dry-run",
				ExpectedRevision:   command.ExpectedRevision,
				IdempotencyKey:     command.IdempotencyKey,
				CommandFingerprint: fingerprint,
				ProcessEpoch:       "prior-process",
				Status: api.WorkflowOperationStatus{
					ID:          "prior-dry-run",
					WorkflowID:  command.WorkflowID,
					Revision:    command.ExpectedRevision,
					Sequence:    1,
					Command:     "dry_run_uploads",
					Operation:   api.OperationKindUploadDryRun,
					Status:      api.StageStatusFailed,
					StartedAt:   now,
					UpdatedAt:   now,
					CompletedAt: &now,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := api.StageStatusStale
			if enveloped {
				wantStatus = api.StageStatusFailed
			}
			for range 2 {
				replayed, err := module.Start(t.Context(), testOwnerID, command)
				if err != nil || replayed.ID != "prior-dry-run" || replayed.Status != wantStatus || uploads.builds != 0 {
					t.Fatalf("legacy replay=%#v err=%v builds=%d", replayed, err, uploads.builds)
				}
			}
			command.ReviewOnly = true
			if _, err := module.Start(t.Context(), testOwnerID, command); !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("review-only reused explicit dry-run receipt: %v", err)
			}
		})
	}
}
