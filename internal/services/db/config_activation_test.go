// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestConfigActivationPendingAndCommit(t *testing.T) {
	ctx := t.Context()
	repo, err := Open(filepath.Join(t.TempDir(), "config-activation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrateAddConfigActivation(ctx, repo.RawDB()); err != nil {
		t.Fatal(err)
	}

	initial, err := repo.LoadConfigActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Status != api.ConfigActivationActive || initial.ActiveGeneration != 0 {
		t.Fatalf("initial activation = %#v", initial)
	}
	pending, err := repo.SavePendingConfigActivation(ctx, "session-owner", []byte(`{"stored":"candidate"}`), []api.ConfigImpactDetail{{Kind: api.ConfigImpactDescription}})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != api.ConfigActivationPending || pending.PendingGeneration != 1 {
		t.Fatalf("pending activation = %#v", pending)
	}
	if pending.ActivationID == "" {
		t.Fatal("pending activation ID is empty")
	}
	second, err := repo.SavePendingConfigActivation(ctx, "other-session", []byte(`{"stored":"second"}`), []api.ConfigImpactDetail{{Kind: api.ConfigImpactProvider}})
	if !errors.Is(err, api.ErrConfigActivationPending) {
		t.Fatalf("second pending activation error = %v", err)
	}
	if second.ActivationID != pending.ActivationID {
		t.Fatalf("second pending activation = %#v, want existing %#v", second, pending)
	}
	payload, loaded, err := repo.LoadPendingConfigActivationCandidate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"stored":"candidate"}` || loaded.Status != api.ConfigActivationPending {
		t.Fatalf("pending candidate = %q, activation=%#v", payload, loaded)
	}

	tx, err := repo.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := repo.ActivateConfigTx(ctx, tx, 0, "next-fingerprint", []api.ConfigImpactDetail{{Kind: api.ConfigImpactDescription}}, nil)
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	if activation.Status != api.ConfigActivationActive || activation.ActiveGeneration != 1 {
		t.Fatalf("committed activation = %#v", activation)
	}
	_, _, err = repo.LoadPendingConfigActivationCandidate(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending candidate error = %v, want no rows", err)
	}
}

func TestConfigActivationFailureClearsCandidateAndAllowsReplacement(t *testing.T) {
	ctx := t.Context()
	repo, err := Open(filepath.Join(t.TempDir(), "config-activation-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := repo.SavePendingConfigActivation(
		ctx,
		"session-owner",
		[]byte(`{"stored":"candidate"}`),
		[]api.ConfigImpactDetail{{Kind: api.ConfigImpactDescription}},
	)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := repo.FailPendingConfigActivation(ctx, pending.ActivationID, api.ConfigActivationFailureBuild)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != api.ConfigActivationFailed || failed.ActivationID != pending.ActivationID ||
		failed.FailureCode != api.ConfigActivationFailureBuild || failed.ActiveGeneration != pending.ActiveGeneration {
		t.Fatalf("failed activation = %#v", failed)
	}
	if _, _, err := repo.LoadPendingConfigActivationCandidate(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed activation retained candidate: %v", err)
	}
	replacement, err := repo.SavePendingConfigActivation(
		ctx,
		"session-owner",
		[]byte(`{"stored":"replacement"}`),
		[]api.ConfigImpactDetail{{Kind: api.ConfigImpactProvider}},
	)
	if err != nil {
		t.Fatalf("save replacement candidate: %v", err)
	}
	if replacement.Status != api.ConfigActivationPending || replacement.ActivationID == pending.ActivationID {
		t.Fatalf("replacement activation = %#v", replacement)
	}
}

func TestInitializeConfigActivationFingerprintOnlyFillsLegacyBlankValue(t *testing.T) {
	ctx := t.Context()
	repo, err := Open(filepath.Join(t.TempDir(), "config-activation-fingerprint.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	activation, err := repo.InitializeConfigActivationFingerprint(ctx, "active")
	if err != nil {
		t.Fatal(err)
	}
	if activation.Fingerprint != "active" || activation.ActiveGeneration != 0 {
		t.Fatalf("initialized activation = %#v", activation)
	}
	if _, err := repo.InitializeConfigActivationFingerprint(ctx, "replacement"); !errors.Is(err, api.ErrConfigActivationChanged) {
		t.Fatalf("replacement fingerprint error = %v", err)
	}
}

func TestConfigActivationSafeAllowsIdleWorkflowSlot(t *testing.T) {
	ctx := t.Context()
	repo, err := Open(filepath.Join(t.TempDir(), "config-activation-safe.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrateAddConfigActivation(ctx, repo.RawDB()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `UPDATE active_input SET record_json = ? WHERE singleton = 1`,
		`{"State":"active","Revision":1,"Fence":1,"CoordinatorID":"test","OwnerID":"owner","WorkflowID":"workflow","InputID":"input","SourceVersion":"version"}`); err != nil {
		t.Fatal(err)
	}
	safe, err := repo.ConfigActivationSafe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !safe {
		t.Fatal("idle active input was rejected for config activation")
	}
}

func TestActivateConfigTxTransformsIdleActiveWorkflowAndAdvancesSlot(t *testing.T) {
	ctx := t.Context()
	repo, err := Open(filepath.Join(t.TempDir(), "config-activation-idle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrateAddConfigActivation(ctx, repo.RawDB()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO release_workflow_states (
		owner_id, workflow_id, revision, status, creation_key, creation_fingerprint, state_json, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"owner", "workflow", 1, "active", "", "", []byte(`{"safe":"state"}`),
		formatWorkflowStateTime(now), formatWorkflowStateTime(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `UPDATE active_input SET revision = 5, fence = 1, record_json = ? WHERE singleton = 1`,
		`{"State":"active","Revision":5,"Fence":1,"CoordinatorID":"test","OwnerID":"owner","WorkflowID":"workflow","InputID":"input","SourceVersion":"version"}`); err != nil {
		t.Fatal(err)
	}
	tx, err := repo.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.ActivateConfigTx(ctx, tx, 0, "next-fingerprint", []api.ConfigImpactDetail{{Kind: api.ConfigImpactDescription}}, func(record api.ReleaseWorkflowStateRecord, _ api.ConfigImpactDetail) (api.ReleaseWorkflowStateRecord, error) {
		record.Revision++
		record.UpdatedAt = record.UpdatedAt.Add(time.Nanosecond)
		record.Payload = []byte(`{"safe":"updated"}`)
		return record, nil
	})
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	var workflowRevision uint64
	if err := repo.RawDB().QueryRowContext(ctx, `SELECT revision FROM release_workflow_states WHERE owner_id = 'owner' AND workflow_id = 'workflow'`).Scan(&workflowRevision); err != nil {
		t.Fatal(err)
	}
	if workflowRevision != 2 {
		t.Fatalf("workflow revision = %d, want 2", workflowRevision)
	}
	slot, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slot.Revision != 6 {
		t.Fatalf("active input revision = %d, want 6", slot.Revision)
	}
}
