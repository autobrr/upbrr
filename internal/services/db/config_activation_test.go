// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	modernsqlite "modernc.org/sqlite"

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
	pending, err := repo.SavePendingConfigActivation(
		ctx,
		"session-owner",
		[]byte(`{"stored":"candidate"}`),
		[]api.ConfigImpactDetail{{Kind: api.ConfigImpactDescription}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != api.ConfigActivationPending || pending.PendingGeneration != 1 {
		t.Fatalf("pending activation = %#v", pending)
	}
	if pending.ActivationID == "" {
		t.Fatal("pending activation ID is empty")
	}
	second, err := repo.SavePendingConfigActivation(
		ctx,
		"other-session",
		[]byte(`{"stored":"second"}`),
		[]api.ConfigImpactDetail{{Kind: api.ConfigImpactProvider}},
	)
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
	activation, err := repo.ActivateConfigTx(ctx, tx, pending, "next-fingerprint", []api.ConfigImpactDetail{{Kind: api.ConfigImpactDescription}}, nil)
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
	tx, err = repo.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.ActivateConfigTx(ctx, tx, initial, "stale-fingerprint", nil, nil)
	_ = tx.Rollback()
	if !errors.Is(err, api.ErrConfigActivationChanged) {
		t.Fatalf("stale generation error = %v", err)
	}
}

func TestConfigActivationRejectsReplacedCandidate(t *testing.T) {
	for _, consumePending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending=%t", consumePending), func(t *testing.T) {
			ctx := t.Context()
			repo, err := Open(filepath.Join(t.TempDir(), "activation.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.MigrateContext(ctx); err != nil {
				t.Fatal(err)
			}
			expected, err := repo.LoadConfigActivation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if consumePending {
				expected, err = repo.SavePendingConfigActivation(ctx, "owner", []byte(`{"candidate":"old"}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := repo.FailPendingConfigActivation(ctx, expected.ActivationID, api.ConfigActivationFailureBuild); err != nil {
					t.Fatal(err)
				}
			}
			candidate := []byte(`{"candidate":"replacement"}`)
			replacement, err := repo.SavePendingConfigActivation(ctx, "other-owner", candidate, nil)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := repo.RawDB().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = repo.ActivateConfigTx(ctx, tx, expected, "stale", nil, nil)
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if !errors.Is(err, api.ErrConfigActivationChanged) && !errors.Is(err, api.ErrConfigActivationPending) {
				t.Fatalf("stale activation error = %v", err)
			}
			payload, retained, err := repo.LoadPendingConfigActivationCandidate(ctx)
			if err != nil || string(payload) != string(candidate) || retained.ActivationID != replacement.ActivationID ||
				retained.ActiveGeneration != expected.ActiveGeneration {
				t.Fatalf("replacement changed: activation=%#v payload=%q err=%v", retained, payload, err)
			}
		})
	}
}

func TestConfigActivationReadsUseConsistentSnapshot(t *testing.T) {
	for _, test := range []struct {
		name            string
		load            func(*SQLiteRepository) configActivationReadResult
		expectCandidate bool
	}{
		{
			name: "activation",
			load: func(repo *SQLiteRepository) configActivationReadResult {
				activation, err := repo.LoadConfigActivation(t.Context())
				return configActivationReadResult{activation: activation, err: err}
			},
		},
		{
			name: "candidate",
			load: func(repo *SQLiteRepository) configActivationReadResult {
				candidate, activation, err := repo.LoadPendingConfigActivationCandidate(t.Context())
				return configActivationReadResult{
					candidate:  candidate,
					activation: activation,
					err:        err,
				}
			},
			expectCandidate: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			started, release := armConfigActivationReadHook(t)
			releaseClosed := false
			defer func() {
				if !releaseClosed {
					close(release)
				}
			}()

			repo, err := Open(filepath.Join(t.TempDir(), "config-activation-consistent-read.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			pending, err := repo.SavePendingConfigActivation(
				t.Context(),
				"owner",
				[]byte(`{"candidate":"pending"}`),
				[]api.ConfigImpactDetail{
					{Kind: api.ConfigImpactDescription},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := installConfigActivationReadHookView(t, repo); err != nil {
				t.Fatal(err)
			}

			resultCh := make(chan configActivationReadResult, 1)
			go func() { resultCh <- test.load(repo) }()
			select {
			case <-started:
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}

			writer, err := Open(repo.DBPath())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })
			tx, err := writer.RawDB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = writer.ActivateConfigTx(
				t.Context(),
				tx,
				pending,
				"next",
				[]api.ConfigImpactDetail{
					{Kind: api.ConfigImpactDescription},
				},
				nil,
			)
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}

			close(release)
			releaseClosed = true
			result := <-resultCh
			if result.err != nil {
				t.Fatalf("load = %v", result.err)
			}
			if result.activation.Status != api.ConfigActivationPending || result.activation.ActivationID != pending.ActivationID ||
				result.activation.ActiveGeneration != pending.ActiveGeneration || result.activation.PendingGeneration != pending.PendingGeneration {
				t.Fatalf("activation = %#v, want pending %#v", result.activation, pending)
			}
			if test.expectCandidate && string(result.candidate) != `{"candidate":"pending"}` {
				t.Fatalf("candidate = %q", result.candidate)
			}
		})
	}
}

type configActivationReadResult struct {
	candidate  []byte
	activation api.ConfigActivation
	err        error
}

var configActivationReadHook struct {
	sync.Mutex
	started chan<- struct{}
	release <-chan struct{}
	armed   bool
}

var registerConfigActivationReadHook sync.Once
var errRegisterConfigActivationReadHook error

func armConfigActivationReadHook(t *testing.T) (<-chan struct{}, chan struct{}) {
	t.Helper()
	registerConfigActivationReadHook.Do(func() {
		errRegisterConfigActivationReadHook = modernsqlite.RegisterScalarFunction(
			"config_activation_test_read_hook",
			0,
			func(_ *modernsqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
				configActivationReadHook.Lock()
				started, release, armed := configActivationReadHook.started, configActivationReadHook.release, configActivationReadHook.armed
				configActivationReadHook.armed = false
				configActivationReadHook.Unlock()
				if !armed {
					return int64(0), nil
				}
				started <- struct{}{}
				<-release
				return int64(0), nil
			},
		)
	})
	if errRegisterConfigActivationReadHook != nil {
		t.Fatal(errRegisterConfigActivationReadHook)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	configActivationReadHook.Lock()
	configActivationReadHook.started = started
	configActivationReadHook.release = release
	configActivationReadHook.armed = true
	configActivationReadHook.Unlock()
	return started, release
}

func installConfigActivationReadHookView(t *testing.T, repo *SQLiteRepository) error {
	t.Helper()
	for _, statement := range []string{
		`ALTER TABLE config_activation RENAME TO config_activation_data`,
		`CREATE VIEW config_activation AS
			SELECT singleton, generation + config_activation_test_read_hook() AS generation,
				fingerprint, impacts_json, updated_at, failed_activation_id, failed_code, failed_impacts_json, failed_at
			FROM config_activation_data`,
		`CREATE TRIGGER config_activation_test_update INSTEAD OF UPDATE ON config_activation BEGIN
			UPDATE config_activation_data
			SET generation = NEW.generation, fingerprint = NEW.fingerprint, impacts_json = NEW.impacts_json,
				updated_at = NEW.updated_at, failed_activation_id = NEW.failed_activation_id, failed_code = NEW.failed_code,
				failed_impacts_json = NEW.failed_impacts_json, failed_at = NEW.failed_at
			WHERE singleton = OLD.singleton;
		END`,
	} {
		if _, err := repo.RawDB().ExecContext(t.Context(), statement); err != nil {
			return fmt.Errorf("install config activation read hook view: %w", err)
		}
	}
	return nil
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
	if _, err := repo.RawDB().ExecContext(
		ctx,
		`UPDATE active_input SET record_json = ? WHERE singleton = 1`,
		`{"State":"active","Revision":1,"Fence":1,"CoordinatorID":"test","OwnerID":"owner","WorkflowID":"workflow","InputID":"input","SourceVersion":"version"}`,
	); err != nil {
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
	if _, err := repo.RawDB().ExecContext(
		ctx,
		`UPDATE active_input SET revision = 5, fence = 1, record_json = ? WHERE singleton = 1`,
		`{"State":"active","Revision":5,"Fence":1,"CoordinatorID":"test","OwnerID":"owner","WorkflowID":"workflow","InputID":"input","SourceVersion":"version"}`,
	); err != nil {
		t.Fatal(err)
	}
	tx, err := repo.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.ActivateConfigTx(
		ctx,
		tx,
		api.ConfigActivation{},
		"next-fingerprint",
		[]api.ConfigImpactDetail{{Kind: api.ConfigImpactDescription}},
		func(record api.ReleaseWorkflowStateRecord, _ api.ConfigImpactDetail) (api.ReleaseWorkflowStateRecord, error) {
			record.Revision++
			record.UpdatedAt = record.UpdatedAt.Add(time.Nanosecond)
			record.Payload = []byte(`{"safe":"updated"}`)
			return record, nil
		},
	)
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	var workflowRevision uint64
	if err := repo.RawDB().QueryRowContext(
		ctx,
		`SELECT revision FROM release_workflow_states WHERE owner_id = 'owner' AND workflow_id = 'workflow'`,
	).Scan(
		&workflowRevision,
	); err != nil {
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
