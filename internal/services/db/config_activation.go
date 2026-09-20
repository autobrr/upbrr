// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// ConfigActivationWorkflowTransform selectively invalidates one owner-scoped
// workflow record inside the configuration activation transaction.
type ConfigActivationWorkflowTransform func(api.ReleaseWorkflowStateRecord, api.ConfigImpactDetail) (api.ReleaseWorkflowStateRecord, error)

// migrateAddConfigActivation creates the singleton effective-config generation
// and the one durable, encrypted candidate permitted while work is active.
func migrateAddConfigActivation(ctx context.Context, exec migrationExecutor) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS config_activation (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			generation INTEGER NOT NULL, fingerprint TEXT NOT NULL DEFAULT '', impacts_json BLOB NOT NULL, updated_at TEXT NOT NULL,
			failed_activation_id TEXT NOT NULL DEFAULT '', failed_code TEXT NOT NULL DEFAULT '',
			failed_impacts_json BLOB NOT NULL DEFAULT '[]', failed_at TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT OR IGNORE INTO config_activation (singleton, generation, impacts_json, updated_at)
			VALUES (1, 0, '[]', '1970-01-01T00:00:00Z')`,
		`CREATE TABLE IF NOT EXISTS config_activation_pending (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			activation_id TEXT NOT NULL, initiating_owner TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status = 'pending'), base_generation INTEGER NOT NULL,
			impacts_json BLOB NOT NULL, candidate_json BLOB NOT NULL, updated_at TEXT NOT NULL
		)`,
	} {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db migrate config activation: %w", err)
		}
	}
	return nil
}

// migrateAddConfigActivationFailure adds terminal failure state for deferred
// candidates created before the failure columns existed.
func migrateAddConfigActivationFailure(ctx context.Context, exec migrationExecutor) error {
	columns, err := tableColumns(ctx, exec, "config_activation")
	if err != nil {
		return fmt.Errorf("db migrate config activation failure columns: %w", err)
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "failed_activation_id", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "failed_code", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "failed_impacts_json", definition: "BLOB NOT NULL DEFAULT '[]'"},
		{name: "failed_at", definition: "TEXT NOT NULL DEFAULT ''"},
	} {
		if _, ok := columns[column.name]; ok {
			continue
		}
		if _, err := exec.ExecContext(ctx, fmt.Sprintf("ALTER TABLE config_activation ADD COLUMN %s %s", column.name, column.definition)); err != nil {
			return fmt.Errorf("db add config activation %s: %w", column.name, err)
		}
	}
	return nil
}

// LoadConfigActivation returns the active generation and, when present, its
// one pending candidate's requested generation. Candidate bytes never leave
// this adapter through this method.
func (r *SQLiteRepository) LoadConfigActivation(ctx context.Context) (api.ConfigActivation, error) {
	if r == nil || r.db == nil {
		return api.ConfigActivation{}, errors.New("db: repository not initialized")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("db load config activation begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return loadConfigActivation(ctx, tx)
}

// InitializeConfigActivationFingerprint records the fingerprint for a legacy
// generation that predates durable fingerprints. It never replaces a
// fingerprint already associated with an active generation.
func (r *SQLiteRepository) InitializeConfigActivationFingerprint(
	ctx context.Context, fingerprint api.WorkflowFingerprint,
) (api.ConfigActivation, error) {
	if r == nil || r.db == nil {
		return api.ConfigActivation{}, errors.New("db: repository not initialized")
	}
	if strings.TrimSpace(string(fingerprint)) == "" {
		return api.ConfigActivation{}, errors.New("db: config activation fingerprint is required")
	}
	var activation api.ConfigActivation
	err := r.withWriteTx(ctx, "initialize config activation fingerprint", func(tx *sql.Tx) error {
		current, err := loadConfigActivation(ctx, tx)
		if err != nil {
			return err
		}
		if current.Fingerprint != "" && current.Fingerprint != fingerprint {
			return api.ErrConfigActivationChanged
		}
		if current.Fingerprint == "" {
			if _, err := tx.ExecContext(ctx, `UPDATE config_activation SET fingerprint = ? WHERE singleton = 1`, fingerprint); err != nil {
				return fmt.Errorf("db initialize config activation fingerprint: %w", err)
			}
		}
		activation, err = loadConfigActivation(ctx, tx)
		return err
	})
	return activation, err
}

func loadConfigActivation(ctx context.Context, query workflowStateQueryer) (api.ConfigActivation, error) {
	var activation api.ConfigActivation
	var generation uint64
	var fingerprint api.WorkflowFingerprint
	var impactsJSON, failedImpactsJSON []byte
	var updated, failedActivationID, failedCode, failedAt string
	if err := query.QueryRowContext(ctx, `
		SELECT generation, fingerprint, impacts_json, updated_at, failed_activation_id, failed_code, failed_impacts_json, failed_at
		FROM config_activation WHERE singleton = 1`).Scan(
		&generation, &fingerprint, &impactsJSON, &updated, &failedActivationID, &failedCode, &failedImpactsJSON, &failedAt); err != nil {
		return activation, fmt.Errorf("db load config activation: %w", err)
	}
	impactDetails, err := decodeConfigImpactDetails(impactsJSON)
	if err != nil {
		return activation, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return activation, fmt.Errorf("db parse config activation update: %w", err)
	}
	activation = api.ConfigActivation{
		Status:           api.ConfigActivationActive,
		ActiveGeneration: generation,
		Fingerprint:      fingerprint,
		Impacts:          configImpactKinds(impactDetails),
		UpdatedAt:        updatedAt,
	}
	var activationID string
	var pendingBase uint64
	var pendingImpactsJSON []byte
	var pendingUpdated string
	err = query.QueryRowContext(ctx, `SELECT activation_id, base_generation, impacts_json, updated_at FROM config_activation_pending WHERE singleton = 1`).Scan(
		&activationID, &pendingBase, &pendingImpactsJSON, &pendingUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		if failedActivationID == "" {
			return activation, nil
		}
		if !validConfigActivationFailureCode(api.ConfigActivationFailureCode(failedCode)) {
			return api.ConfigActivation{}, errors.New("db: config activation failure code is invalid")
		}
		failureImpacts, decodeErr := decodeConfigImpactDetails(failedImpactsJSON)
		if decodeErr != nil {
			return api.ConfigActivation{}, decodeErr
		}
		failedUpdatedAt, parseErr := time.Parse(time.RFC3339Nano, failedAt)
		if parseErr != nil {
			return api.ConfigActivation{}, fmt.Errorf("db parse config activation failure update: %w", parseErr)
		}
		activation.Status = api.ConfigActivationFailed
		activation.ActivationID = failedActivationID
		activation.FailureCode = api.ConfigActivationFailureCode(failedCode)
		activation.Impacts = configImpactKinds(failureImpacts)
		activation.UpdatedAt = failedUpdatedAt
		return activation, nil
	}
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("db load pending config activation: %w", err)
	}
	if pendingBase != generation || generation == math.MaxInt64 {
		return api.ConfigActivation{}, errors.New("db: pending config activation generation is invalid")
	}
	pendingImpactDetails, err := decodeConfigImpactDetails(pendingImpactsJSON)
	if err != nil {
		return api.ConfigActivation{}, err
	}
	pendingAt, err := time.Parse(time.RFC3339Nano, pendingUpdated)
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("db parse pending config activation update: %w", err)
	}
	activation.Status = api.ConfigActivationPending
	activation.ActivationID = activationID
	activation.PendingGeneration = generation + 1
	activation.Impacts = configImpactKinds(pendingImpactDetails)
	activation.UpdatedAt = pendingAt
	return activation, nil
}

// ClearConfigActivationFailure acknowledges one exact terminal failure after
// the caller has retained or replaced the active configuration.
func (r *SQLiteRepository) ClearConfigActivationFailure(ctx context.Context, activationID string) (api.ConfigActivation, error) {
	if r == nil || r.db == nil {
		return api.ConfigActivation{}, errors.New("db: repository not initialized")
	}
	activationID = strings.TrimSpace(activationID)
	if activationID == "" {
		return api.ConfigActivation{}, errors.New("db: config activation ID is required")
	}
	var activation api.ConfigActivation
	err := r.withWriteTx(ctx, "clear config activation failure", func(tx *sql.Tx) error {
		current, err := loadConfigActivation(ctx, tx)
		if err != nil {
			return err
		}
		if current.Status != api.ConfigActivationFailed || current.ActivationID != activationID {
			activation = current
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE config_activation
			SET failed_activation_id = '', failed_code = '', failed_impacts_json = '[]', failed_at = ''
			WHERE singleton = 1`); err != nil {
			return fmt.Errorf("db clear config activation failure: %w", err)
		}
		activation, err = loadConfigActivation(ctx, tx)
		return err
	})
	return activation, err
}

// FailPendingConfigActivation records a terminal safe failure and deletes the
// encrypted candidate. The active generation remains unchanged so a corrected
// settings save can create a new candidate.
func (r *SQLiteRepository) FailPendingConfigActivation(
	ctx context.Context, activationID string, code api.ConfigActivationFailureCode,
) (api.ConfigActivation, error) {
	if r == nil || r.db == nil {
		return api.ConfigActivation{}, errors.New("db: repository not initialized")
	}
	activationID = strings.TrimSpace(activationID)
	if activationID == "" || !validConfigActivationFailureCode(code) {
		return api.ConfigActivation{}, errors.New("db: config activation failure identity is required")
	}
	var activation api.ConfigActivation
	err := r.withWriteTx(ctx, "fail pending config activation", func(tx *sql.Tx) error {
		current, err := loadConfigActivation(ctx, tx)
		if err != nil {
			return err
		}
		if current.Status != api.ConfigActivationPending || current.ActivationID != activationID {
			activation = current
			return nil
		}
		var impactsJSON []byte
		if err := tx.QueryRowContext(ctx, `
			SELECT impacts_json FROM config_activation_pending WHERE singleton = 1 AND activation_id = ?`, activationID,
		).Scan(&impactsJSON); err != nil {
			return fmt.Errorf("db load failed config activation impacts: %w", err)
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `
			UPDATE config_activation
			SET failed_activation_id = ?, failed_code = ?, failed_impacts_json = ?, failed_at = ?
			WHERE singleton = 1`, activationID, code, impactsJSON, formatWorkflowStateTime(now)); err != nil {
			return fmt.Errorf("db save config activation failure: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM config_activation_pending WHERE singleton = 1 AND activation_id = ?`, activationID); err != nil {
			return fmt.Errorf("db clear failed config activation candidate: %w", err)
		}
		activation, err = loadConfigActivation(ctx, tx)
		return err
	})
	return activation, err
}

func validConfigActivationFailureCode(code api.ConfigActivationFailureCode) bool {
	switch code {
	case api.ConfigActivationFailureNormalize,
		api.ConfigActivationFailureValidateStored,
		api.ConfigActivationFailureValidateRuntime,
		api.ConfigActivationFailureBuild,
		api.ConfigActivationFailureCookies,
		api.ConfigActivationFailurePersist:
		return true
	default:
		return false
	}
}

// SavePendingConfigActivation atomically replaces the one pending encrypted
// candidate. It does not inspect active work because a candidate is allowed to
// wait while an operation owns the active-input slot.
func (r *SQLiteRepository) SavePendingConfigActivation(
	ctx context.Context, initiatingOwner string, candidate []byte, impacts []api.ConfigImpactDetail,
) (api.ConfigActivation, error) {
	if r == nil || r.db == nil {
		return api.ConfigActivation{}, errors.New("db: repository not initialized")
	}
	if len(candidate) == 0 {
		return api.ConfigActivation{}, errors.New("db: pending config activation candidate is required")
	}
	initiatingOwner = strings.TrimSpace(initiatingOwner)
	if initiatingOwner == "" {
		return api.ConfigActivation{}, errors.New("db: pending config activation owner is required")
	}
	activationID, err := newConfigActivationID()
	if err != nil {
		return api.ConfigActivation{}, err
	}
	impacts = normalizeConfigImpactDetails(impacts)
	impactsJSON, err := json.Marshal(impacts)
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("db encode config activation impacts: %w", err)
	}
	var activation api.ConfigActivation
	err = r.withWriteTx(ctx, "save pending config activation", func(tx *sql.Tx) error {
		current, err := loadConfigActivation(ctx, tx)
		if err != nil {
			return err
		}
		if current.Status == api.ConfigActivationPending {
			activation = current
			return &api.ConfigActivationPendingError{Activation: current}
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `
			UPDATE config_activation
			SET failed_activation_id = '', failed_code = '', failed_impacts_json = '[]', failed_at = ''
			WHERE singleton = 1`); err != nil {
			return fmt.Errorf("db clear prior config activation failure: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO config_activation_pending (
				singleton, activation_id, initiating_owner, status, base_generation, impacts_json, candidate_json, updated_at
			) VALUES (1, ?, ?, 'pending', ?, ?, ?, ?)
		`, activationID, initiatingOwner, current.ActiveGeneration, impactsJSON, candidate, formatWorkflowStateTime(now)); err != nil {
			return fmt.Errorf("db save pending config activation: %w", err)
		}
		activation = api.ConfigActivation{
			Status:            api.ConfigActivationPending,
			ActivationID:      activationID,
			ActiveGeneration:  current.ActiveGeneration,
			PendingGeneration: current.ActiveGeneration + 1,
			Impacts:           configImpactKinds(impacts),
			UpdatedAt:         now,
		}
		return nil
	})
	return activation, err
}

// LoadPendingConfigActivationCandidate returns the encrypted candidate only to
// the local runtime activator. Browser-facing callers must use
// LoadConfigActivation instead.
func (r *SQLiteRepository) LoadPendingConfigActivationCandidate(ctx context.Context) ([]byte, api.ConfigActivation, error) {
	if r == nil || r.db == nil {
		return nil, api.ConfigActivation{}, errors.New("db: repository not initialized")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, api.ConfigActivation{}, fmt.Errorf("db load pending config activation begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	activation, err := loadConfigActivation(ctx, tx)
	if err != nil {
		return nil, api.ConfigActivation{}, err
	}
	if activation.Status != api.ConfigActivationPending {
		return nil, activation, sql.ErrNoRows
	}
	var candidate []byte
	if err := tx.QueryRowContext(ctx, `SELECT candidate_json FROM config_activation_pending WHERE singleton = 1`).Scan(&candidate); err != nil {
		return nil, api.ConfigActivation{}, fmt.Errorf("db load pending config activation candidate: %w", err)
	}
	return slices.Clone(candidate), activation, nil
}

// ActivateConfigTx commits one configuration generation only while the exact
// transaction observes no busy input, unfinished operation, or unresolved
// external effect. The caller owns the surrounding full-config transaction.
// expected binds the active generation and, when pending, the candidate ID;
// stale generations or candidates return api.ErrConfigActivationChanged.
// An immediate activation rejects a newly pending candidate without consuming it.
func (r *SQLiteRepository) ActivateConfigTx(
	ctx context.Context,
	tx *sql.Tx,
	expected api.ConfigActivation,
	nextFingerprint api.WorkflowFingerprint,
	impacts []api.ConfigImpactDetail,
	transform ConfigActivationWorkflowTransform,
) (api.ConfigActivation, error) {
	if r == nil || r.db == nil || tx == nil {
		return api.ConfigActivation{}, errors.New("db: config activation repository and transaction are required")
	}
	current, err := loadConfigActivation(ctx, tx)
	if err != nil {
		return api.ConfigActivation{}, err
	}
	if strings.TrimSpace(string(nextFingerprint)) == "" {
		return api.ConfigActivation{}, errors.New("db: config activation fingerprint is required")
	}
	if expected.ActiveGeneration >= math.MaxInt64 {
		return api.ConfigActivation{}, errors.New("db: config activation generation exhausted")
	}
	if current.ActiveGeneration != expected.ActiveGeneration {
		return api.ConfigActivation{}, api.ErrConfigActivationChanged
	}
	if expected.Status == api.ConfigActivationPending {
		if expected.ActivationID == "" || current.Status != api.ConfigActivationPending || current.ActivationID != expected.ActivationID {
			return api.ConfigActivation{}, api.ErrConfigActivationChanged
		}
	} else if current.Status == api.ConfigActivationPending {
		return current, &api.ConfigActivationPendingError{Activation: current}
	}
	slot, err := requireConfigActivationSafe(ctx, tx)
	if err != nil {
		return api.ConfigActivation{}, err
	}
	impacts = normalizeConfigImpactDetails(impacts)
	if slot.State == api.ActiveInputActive && transform != nil {
		if err := applyConfigActivationWorkflowImpacts(ctx, tx, slot, impacts, transform); err != nil {
			return api.ConfigActivation{}, err
		}
		if slot.Revision >= math.MaxInt64 {
			return api.ConfigActivation{}, errors.New("db: active input revision exhausted during config activation")
		}
		slot.Revision++
		if err := writeActiveInput(ctx, tx, slot); err != nil {
			return api.ConfigActivation{}, err
		}
	}
	impactsJSON, err := json.Marshal(impacts)
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("db encode config activation impacts: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE config_activation
		SET generation = ?, fingerprint = ?, impacts_json = ?, updated_at = ?,
			failed_activation_id = '', failed_code = '', failed_impacts_json = '[]', failed_at = ''
		WHERE singleton = 1`, expected.ActiveGeneration+1, nextFingerprint, impactsJSON, formatWorkflowStateTime(now)); err != nil {
		return api.ConfigActivation{}, fmt.Errorf("db update config activation: %w", err)
	}
	if expected.Status == api.ConfigActivationPending {
		if _, err := tx.ExecContext(ctx, `DELETE FROM config_activation_pending WHERE singleton = 1 AND activation_id = ?`, expected.ActivationID); err != nil {
			return api.ConfigActivation{}, fmt.Errorf("db clear pending config activation: %w", err)
		}
	}
	return api.ConfigActivation{
		Status:           api.ConfigActivationActive,
		ActiveGeneration: expected.ActiveGeneration + 1,
		Fingerprint:      nextFingerprint,
		Impacts:          configImpactKinds(impacts),
		UpdatedAt:        now,
	}, nil
}

// ConfigActivationSafe reports whether an effective configuration may be
// built. ActivateConfigTx repeats this check in the commit transaction.
func (r *SQLiteRepository) ConfigActivationSafe(ctx context.Context) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("db: repository not initialized")
	}
	err := r.withWriteTx(ctx, "check config activation safety", func(tx *sql.Tx) error {
		_, err := requireConfigActivationSafe(ctx, tx)
		return err
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, api.ErrActiveInputBusy) || errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
		return false, nil
	}
	return false, err
}

func requireConfigActivationSafe(ctx context.Context, tx *sql.Tx) (api.ActiveInputRecord, error) {
	slot, err := loadActiveInput(ctx, tx)
	if err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("db inspect config activation active input: %w", err)
	}
	if slot.State != api.ActiveInputEmpty && slot.State != api.ActiveInputActive {
		return api.ActiveInputRecord{}, api.ErrActiveInputBusy
	}
	var running int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM release_workflow_operations WHERE status IN ('queued', 'running'))`,
	).Scan(
		&running,
	); err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("db inspect config activation work: %w", err)
	}
	if running != 0 {
		return api.ActiveInputRecord{}, api.ErrActiveInputBusy
	}
	var unresolved int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM release_workflow_effects WHERE status IN ('started', 'unknown'))`,
	).Scan(
		&unresolved,
	); err != nil {
		return api.ActiveInputRecord{}, fmt.Errorf("db inspect config activation effects: %w", err)
	}
	if unresolved != 0 {
		return api.ActiveInputRecord{}, api.ErrReleaseWorkflowEffectOutcomeUnknown
	}
	return slot, nil
}

// requireConfigActivationGeneration runs in the same transaction that admits
// an external effect, so a runtime bundle cannot submit after a newer durable
// configuration has become active.
func requireConfigActivationGeneration(ctx context.Context, tx *sql.Tx) error {
	authority, ok := api.ConfigActivationAuthorityFromContext(ctx)
	if !ok {
		return nil
	}
	if authority.Fingerprint == "" {
		return api.ErrConfigActivationChanged
	}
	activation, err := loadConfigActivation(ctx, tx)
	if err != nil {
		return err
	}
	if activation.ActiveGeneration != authority.Generation || activation.Fingerprint != authority.Fingerprint {
		return api.ErrConfigActivationChanged
	}
	return nil
}

func applyConfigActivationWorkflowImpacts(
	ctx context.Context,
	tx *sql.Tx,
	slot api.ActiveInputRecord,
	impacts []api.ConfigImpactDetail,
	transform ConfigActivationWorkflowTransform,
) error {
	record, err := loadWorkflowState(ctx, tx, slot.OwnerID, slot.WorkflowID)
	if err != nil {
		return fmt.Errorf("db load config activation workflow: %w", err)
	}
	for _, impact := range impacts {
		next, err := transform(record, impact)
		if err != nil {
			return fmt.Errorf("db apply config activation workflow impact %s: %w", impact, err)
		}
		if next.OwnerID != record.OwnerID || next.WorkflowID != record.WorkflowID || (next.Revision != record.Revision && next.Revision != record.Revision+1) ||
			next.CreatedAt != record.CreatedAt || next.UpdatedAt.Before(record.UpdatedAt) || len(next.Payload) == 0 {
			return errors.New("db: config activation workflow transform returned invalid record")
		}
		if next.Revision == record.Revision {
			if string(next.Payload) != string(record.Payload) || next.Status != record.Status || next.UpdatedAt != record.UpdatedAt {
				return errors.New("db: config activation workflow transform failed to advance revision")
			}
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE release_workflow_states
			SET revision = ?, status = ?, state_json = ?, updated_at = ?
			WHERE owner_id = ? AND workflow_id = ? AND revision = ?`,
			next.Revision, next.Status, next.Payload, formatWorkflowStateTime(next.UpdatedAt),
			record.OwnerID, record.WorkflowID, record.Revision)
		if err != nil {
			return fmt.Errorf("db save config activation workflow: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("db save config activation workflow rows: %w", err)
		}
		if rows != 1 {
			return api.ErrReleaseWorkflowRevisionConflict
		}
		record = next
	}
	return nil
}

func decodeConfigImpactDetails(payload []byte) ([]api.ConfigImpactDetail, error) {
	var impacts []api.ConfigImpactDetail
	if err := json.Unmarshal(payload, &impacts); err != nil {
		return nil, fmt.Errorf("db decode config activation impacts: %w", err)
	}
	return normalizeConfigImpactDetails(impacts), nil
}

func normalizeConfigImpactDetails(impacts []api.ConfigImpactDetail) []api.ConfigImpactDetail {
	if len(impacts) == 0 {
		return []api.ConfigImpactDetail{}
	}
	result := slices.Clone(impacts)
	for index := range result {
		result[index].TrackerIDs = normalizeConfigImpactTrackerIDs(result[index].TrackerIDs)
	}
	slices.SortFunc(result, func(left, right api.ConfigImpactDetail) int {
		if comparison := cmp.Compare(left.Kind, right.Kind); comparison != 0 {
			return comparison
		}
		return strings.Compare(strings.Join(trackerIDStrings(left.TrackerIDs), ","), strings.Join(trackerIDStrings(right.TrackerIDs), ","))
	})
	return slices.CompactFunc(result, func(left, right api.ConfigImpactDetail) bool {
		return left.Kind == right.Kind && slices.Equal(left.TrackerIDs, right.TrackerIDs)
	})
}

func configImpactKinds(impacts []api.ConfigImpactDetail) []api.ConfigImpact {
	if len(impacts) == 0 {
		return []api.ConfigImpact{}
	}
	result := make([]api.ConfigImpact, 0, len(impacts))
	for _, impact := range impacts {
		if !slices.Contains(result, impact.Kind) {
			result = append(result, impact.Kind)
		}
	}
	return result
}

func normalizeConfigImpactTrackerIDs(ids []api.TrackerID) []api.TrackerID {
	result := make([]api.TrackerID, 0, len(ids))
	for _, id := range ids {
		id = api.TrackerID(strings.ToUpper(strings.TrimSpace(string(id))))
		if id != "" && !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	slices.Sort(result)
	return result
}

func trackerIDStrings(ids []api.TrackerID) []string {
	result := make([]string, len(ids))
	for index, id := range ids {
		result[index] = string(id)
	}
	return result
}

func newConfigActivationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("db generate config activation id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
