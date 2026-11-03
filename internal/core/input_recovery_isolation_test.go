// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceRecoveryQueryPreservesUnfilteredOwnerDiscovery(t *testing.T) {
	ctx := t.Context()
	repo, err := db.Open(filepath.Join(t.TempDir(), "recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	persistent, err := releaseworkflow.NewPersistentRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	module, err := releaseworkflow.New(persistent, releaseworkflow.NewMemoryPrivateResourceStore(), releaseworkflow.ReleasePreparerFunc{},
		releaseworkflow.WithActiveInputs(repo, func(_ context.Context, input api.PrepareInput) (api.InputRecord, error) {
			return api.InputRecord{
				CanonicalPath: input.SourcePath,
				SourceVersion: "verified",
				Manifest:      []byte(`{}`),
			}, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := module.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	sourceA := filepath.Join(t.TempDir(), "Example.A.2026.mkv")
	opened, err := module.OpenInput(ctx, "cli", releaseworkflow.OpenInputRequest{Input: api.PrepareInput{SourcePath: sourceA}, IdempotencyKey: "open-A"})
	if err != nil {
		t.Fatal(err)
	}
	authority := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: opened.CoordinatorID, Fence: opened.Fence})
	effect := api.ReleaseWorkflowEffectRecord{
		OwnerID:             "cli",
		WorkflowID:          opened.WorkflowID,
		OperationID:         "operation-A",
		EffectID:            "effect-A",
		Kind:                "tracker_submission",
		ScopeID:             "ALPHA",
		SemanticFingerprint: "synthetic-A",
		StartedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	if _, _, err := persistent.BeginEffect(authority, effect); err != nil {
		t.Fatal(err)
	}
	if err := persistent.MarkOperationEffectsUnknown(authority, "cli", opened.WorkflowID, effect.OperationID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := module.ReleaseInput(ctx, "cli", opened.Revision); err != nil {
		t.Fatal(err)
	}
	core := &Core{workflow: module}
	for _, test := range []struct {
		owner  string
		source string
		want   []api.WorkflowID
	}{
		{
			owner:  "cli",
			source: sourceA,
			want:   []api.WorkflowID{opened.WorkflowID},
		},
		{owner: "cli", source: sourceA + ".other"},
		{owner: "other-owner", source: sourceA},
	} {
		view, err := core.GetActiveInputForSource(ctx, test.owner, test.source)
		if err != nil || view.State != api.ActiveInputEmpty || !slices.Equal(view.RecoveryWorkflowIDs, test.want) {
			t.Fatalf("source recovery query owner=%s: view=%+v err=%v", test.owner, view, err)
		}
	}
	global, err := core.GetActiveInput(ctx, "cli")
	if err != nil || !slices.Equal(global.RecoveryWorkflowIDs, []api.WorkflowID{opened.WorkflowID}) {
		t.Fatalf("source queries removed unfiltered recovery: view=%+v err=%v", global, err)
	}
}
