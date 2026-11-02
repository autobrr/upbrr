// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// The envelope is a repository format, never an adapter-visible workflow. Only
// the requested workflow is hydrated. A digest identifies the exact JSON bytes,
// including historical versions of a snapshot with the same application ID.
type workflowStorageEnvelope struct {
	Version      int                        `json:"$upbrrWorkflowStorage"`
	State        map[string]json.RawMessage `json:"state"`
	Collections  []string                   `json:"collections"`
	SnapshotRefs int                        `json:"snapshotRefs"`
	ReceiptCount int                        `json:"receiptCount"`
}

var workflowSnapshotCollections = []string{
	"FactInstructions", "Releases", "InputReadiness", "Catalogs", "Runtimes", "Selections",
	"ProjectionInstructions", "Projections", "Preflights", "Dupes", "TrackerApprovals", "Media",
	"HDRAnalyses", "AudioAnalyses", "Descriptions", "DryRuns", "UploadResults", "Operations",
}

// These are the snapshot-bearing fields of api.ReleaseWorkflowCurrent. Other
// receipt fields, including continuation and operation metadata, remain intact.
var workflowReceiptSnapshotFields = []string{
	"factInstructions", "release", "corrections", "inputReadiness", "catalog", "runtime", "selection",
	"projectionInstructions", "projections", "preflight", "dupes", "trackerApproval", "media",
	"hdrAnalysis", "audioAnalysis", "descriptions", "dryRun", "uploadResult",
}

func migrateWorkflowSnapshotStorage(ctx context.Context, exec migrationExecutor) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS release_workflow_snapshots (
			owner_id TEXT NOT NULL, workflow_id TEXT NOT NULL, digest TEXT NOT NULL, snapshot_json BLOB NOT NULL,
			PRIMARY KEY (owner_id, workflow_id, digest),
			FOREIGN KEY (owner_id, workflow_id) REFERENCES release_workflow_states(owner_id, workflow_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS release_workflow_snapshot_refs (
			owner_id TEXT NOT NULL, workflow_id TEXT NOT NULL, collection TEXT NOT NULL,
			entry_key TEXT NOT NULL, field TEXT NOT NULL, digest TEXT NOT NULL,
			PRIMARY KEY (owner_id, workflow_id, collection, entry_key, field),
			FOREIGN KEY (owner_id, workflow_id, digest) REFERENCES release_workflow_snapshots(owner_id, workflow_id, digest) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_release_workflow_snapshot_refs_digest
			ON release_workflow_snapshot_refs(owner_id, workflow_id, digest)`,
		`CREATE TABLE IF NOT EXISTS release_workflow_receipts (
			owner_id TEXT NOT NULL, workflow_id TEXT NOT NULL, receipt_key TEXT NOT NULL,
			fingerprint TEXT NOT NULL, receipt_json BLOB NOT NULL,
			PRIMARY KEY (owner_id, workflow_id, receipt_key),
			FOREIGN KEY (owner_id, workflow_id) REFERENCES release_workflow_states(owner_id, workflow_id) ON DELETE CASCADE
		)`,
	} {
		if _, err := exec.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("db migrate workflow snapshot storage schema: %w", err)
		}
	}
	// Keyset traversal holds at most one retained workflow payload. The migration
	// registry commits the backfill and completion marker in the same transaction.
	var ownerID, workflowID string
	for {
		var record api.ReleaseWorkflowStateRecord
		err := exec.QueryRowContext(ctx, `SELECT owner_id, workflow_id, state_json FROM release_workflow_states
			WHERE (owner_id, workflow_id) > (?, ?) ORDER BY owner_id, workflow_id LIMIT 1`, ownerID, workflowID).
			Scan(&record.OwnerID, &record.WorkflowID, &record.Payload)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("db migrate workflow snapshot storage read: %w", err)
		}
		ownerID, workflowID = record.OwnerID, string(record.WorkflowID)
		var envelope workflowStorageEnvelope
		if json.Unmarshal(record.Payload, &envelope) == nil && envelope.Version != 0 {
			if _, err := loadWorkflowSnapshotStorage(ctx, exec, record); err != nil {
				return err
			}
			continue
		}
		payload, err := saveWorkflowSnapshotStorage(ctx, exec, record)
		if err != nil {
			return err
		}
		if _, err := exec.ExecContext(ctx, `UPDATE release_workflow_states SET state_json = ? WHERE owner_id = ? AND workflow_id = ?`,
			payload, record.OwnerID, record.WorkflowID); err != nil {
			return fmt.Errorf("db migrate workflow snapshot storage write: %w", err)
		}
	}
}

// saveWorkflowSnapshotStorage must run in the state mutation's transaction, after
// the state row exists. It retains opaque or malformed legacy payloads verbatim;
// ownership classification separately prevents unsafe cleanup of those records.
func saveWorkflowSnapshotStorage(ctx context.Context, exec migrationExecutor, record api.ReleaseWorkflowStateRecord) ([]byte, error) {
	for _, table := range []string{"release_workflow_snapshot_refs", "release_workflow_receipts"} {
		if _, err := exec.ExecContext(ctx, `DELETE FROM `+table+` WHERE owner_id = ? AND workflow_id = ?`, record.OwnerID, record.WorkflowID); err != nil {
			return nil, fmt.Errorf("db replace workflow snapshot references: %w", err)
		}
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(record.Payload, &state) != nil || state == nil {
		return retireWorkflowSnapshots(ctx, exec, record, record.Payload)
	}
	var receipts map[string]json.RawMessage
	if json.Unmarshal(state["Receipts"], &receipts) == nil {
		for _, payload := range receipts {
			var receipt map[string]json.RawMessage
			if json.Unmarshal(payload, &receipt) != nil || receipt == nil {
				return retireWorkflowSnapshots(ctx, exec, record, record.Payload)
			}
		}
	}
	envelope := workflowStorageEnvelope{Version: 1, State: state}
	written := make(map[string]struct{})
	store := func(collection, key, field string, payload json.RawMessage) error {
		sum := sha256.Sum256(payload)
		digest := hex.EncodeToString(sum[:])
		if _, exists := written[digest]; !exists {
			if _, err := exec.ExecContext(ctx, `INSERT INTO release_workflow_snapshots (owner_id, workflow_id, digest, snapshot_json)
				VALUES (?, ?, ?, ?) ON CONFLICT(owner_id, workflow_id, digest) DO NOTHING`,
				record.OwnerID, record.WorkflowID, digest, []byte(payload)); err != nil {
				return fmt.Errorf("db retain immutable workflow snapshot: %w", err)
			}
			written[digest] = struct{}{}
		}
		_, err := exec.ExecContext(ctx, `INSERT INTO release_workflow_snapshot_refs
			(owner_id, workflow_id, collection, entry_key, field, digest) VALUES (?, ?, ?, ?, ?, ?)`,
			record.OwnerID, record.WorkflowID, collection, key, field, digest)
		if err != nil {
			return fmt.Errorf("db retain workflow snapshot reference: %w", err)
		}
		envelope.SnapshotRefs++
		return nil
	}
	for _, collection := range workflowSnapshotCollections {
		var entries map[string]json.RawMessage
		if json.Unmarshal(state[collection], &entries) != nil || entries == nil {
			continue
		}
		for key, payload := range entries {
			if err := store(collection, key, "", payload); err != nil {
				return nil, err
			}
		}
		delete(state, collection)
		envelope.Collections = append(envelope.Collections, collection)
	}
	if receipts != nil {
		for key, payload := range receipts {
			var receipt map[string]json.RawMessage
			_ = json.Unmarshal(payload, &receipt) // Shape was checked before any extraction.
			var result map[string]json.RawMessage
			if json.Unmarshal(receipt["Result"], &result) == nil && result != nil {
				for _, field := range workflowReceiptSnapshotFields {
					if snapshot, ok := result[field]; ok && string(snapshot) != "null" {
						if err := store("Receipts", key, field, snapshot); err != nil {
							return nil, err
						}
						delete(result, field)
					}
				}
				var err error
				receipt["Result"], err = json.Marshal(result)
				if err != nil {
					return nil, fmt.Errorf("db encode retained workflow result: %w", err)
				}
			}
			var fingerprint string
			_ = json.Unmarshal(receipt["Fingerprint"], &fingerprint)
			compact, err := json.Marshal(receipt)
			if err != nil {
				return nil, fmt.Errorf("db encode retained workflow receipt: %w", err)
			}
			if _, err := exec.ExecContext(ctx, `INSERT INTO release_workflow_receipts
				(owner_id, workflow_id, receipt_key, fingerprint, receipt_json) VALUES (?, ?, ?, ?, ?)`,
				record.OwnerID, record.WorkflowID, key, fingerprint, compact); err != nil {
				return nil, fmt.Errorf("db retain workflow receipt: %w", err)
			}
		}
		envelope.ReceiptCount = len(receipts)
		delete(state, "Receipts")
		envelope.Collections = append(envelope.Collections, "Receipts")
	}

	if len(envelope.Collections) == 0 {
		return retireWorkflowSnapshots(ctx, exec, record, record.Payload)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("db encode workflow storage: %w", err)
	}
	return retireWorkflowSnapshots(ctx, exec, record, payload)
}

func retireWorkflowSnapshots(ctx context.Context, exec migrationExecutor, record api.ReleaseWorkflowStateRecord, payload []byte) ([]byte, error) {
	if _, err := exec.ExecContext(ctx, `DELETE FROM release_workflow_snapshots AS snapshot
		WHERE owner_id = ? AND workflow_id = ? AND NOT EXISTS (
			SELECT 1 FROM release_workflow_snapshot_refs AS ref WHERE ref.owner_id = snapshot.owner_id
			AND ref.workflow_id = snapshot.workflow_id AND ref.digest = snapshot.digest)`, record.OwnerID, record.WorkflowID); err != nil {
		return nil, fmt.Errorf("db retire unreferenced workflow snapshots: %w", err)
	}
	return payload, nil
}

func loadWorkflowSnapshotStorage(ctx context.Context, query historyQueryer, record api.ReleaseWorkflowStateRecord) (api.ReleaseWorkflowStateRecord, error) {
	var envelope workflowStorageEnvelope
	if json.Unmarshal(record.Payload, &envelope) != nil || envelope.Version == 0 {
		return record, nil
	}
	if envelope.Version != 1 || envelope.State == nil {
		return api.ReleaseWorkflowStateRecord{}, errors.New("db: unsupported workflow storage format")
	}
	collections := make(map[string]map[string]json.RawMessage, len(envelope.Collections))
	for _, collection := range envelope.Collections {
		collections[collection] = make(map[string]json.RawMessage)
	}
	// Read each immutable object once even when several historical receipts and
	// the current state refer to it.
	rows, err := query.QueryContext(ctx, `SELECT digest, snapshot_json FROM release_workflow_snapshots
		WHERE owner_id = ? AND workflow_id = ? AND digest IN (
			SELECT digest FROM release_workflow_snapshot_refs WHERE owner_id = ? AND workflow_id = ?)`,
		record.OwnerID, record.WorkflowID, record.OwnerID, record.WorkflowID)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db read workflow snapshots: %w", err)
	}
	defer rows.Close()
	snapshots := make(map[string]json.RawMessage)
	for rows.Next() {
		var digest string
		var payload []byte
		if err := rows.Scan(&digest, &payload); err != nil {
			_ = rows.Close()
			return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db scan workflow snapshot: %w", err)
		}
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) != digest {
			_ = rows.Close()
			return api.ReleaseWorkflowStateRecord{}, errors.New("db: retained workflow snapshot digest mismatch")
		}
		snapshots[digest] = payload
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db iterate workflow snapshots: %w", err)
	}
	if err := rows.Close(); err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db close workflow snapshots: %w", err)
	}
	rows, err = query.QueryContext(ctx, `SELECT receipt_key, receipt_json FROM release_workflow_receipts
		WHERE owner_id = ? AND workflow_id = ?`, record.OwnerID, record.WorkflowID)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db read workflow receipts: %w", err)
	}
	defer rows.Close()
	receiptResults := make(map[string]map[string]json.RawMessage)
	receiptMetadata := make(map[string]map[string]json.RawMessage)
	for rows.Next() {
		var key string
		var payload []byte
		if err := rows.Scan(&key, &payload); err != nil {
			_ = rows.Close()
			return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db scan workflow receipt: %w", err)
		}
		if collections["Receipts"] == nil {
			_ = rows.Close()
			return api.ReleaseWorkflowStateRecord{}, errors.New("db: workflow receipt collection is missing")
		}
		var receipt map[string]json.RawMessage
		var result map[string]json.RawMessage
		if json.Unmarshal(payload, &receipt) != nil {
			_ = rows.Close()
			return api.ReleaseWorkflowStateRecord{}, errors.New("db: retained workflow receipt is invalid")
		}
		_ = json.Unmarshal(receipt["Result"], &result)
		receiptMetadata[key], receiptResults[key] = receipt, result
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db iterate workflow receipts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db close workflow receipts: %w", err)
	}
	if len(receiptMetadata) != envelope.ReceiptCount {
		return api.ReleaseWorkflowStateRecord{}, errors.New("db: retained workflow receipts are incomplete")
	}
	rows, err = query.QueryContext(ctx, `SELECT collection, entry_key, field, digest FROM release_workflow_snapshot_refs
		WHERE owner_id = ? AND workflow_id = ?`, record.OwnerID, record.WorkflowID)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db read workflow snapshot references: %w", err)
	}
	defer rows.Close()
	refCount := 0
	for rows.Next() {
		refCount++
		var collection, key, field, digest string
		if err := rows.Scan(&collection, &key, &field, &digest); err != nil {
			return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db scan workflow snapshot reference: %w", err)
		}
		snapshot, exists := snapshots[digest]
		if !exists || collections[collection] == nil {
			return api.ReleaseWorkflowStateRecord{}, errors.New("db: retained workflow snapshot is missing")
		}
		if field == "" {
			collections[collection][key] = snapshot
			continue
		}
		if collection != "Receipts" || receiptResults[key] == nil {
			return api.ReleaseWorkflowStateRecord{}, errors.New("db: retained workflow receipt is invalid")
		}
		receiptResults[key][field] = snapshot
	}
	if err := rows.Err(); err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db iterate workflow snapshot references: %w", err)
	}
	if refCount != envelope.SnapshotRefs {
		return api.ReleaseWorkflowStateRecord{}, errors.New("db: retained workflow snapshot references are incomplete")
	}
	for key, receipt := range receiptMetadata {
		if result := receiptResults[key]; result != nil {
			receipt["Result"], err = json.Marshal(result)
			if err != nil {
				return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db restore workflow result: %w", err)
			}
		}
		collections["Receipts"][key], err = json.Marshal(receipt)
		if err != nil {
			return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db restore workflow receipt: %w", err)
		}
	}
	for collection, entries := range collections {
		payload, err := json.Marshal(entries)
		if err != nil {
			return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db restore workflow collection: %w", err)
		}
		envelope.State[collection] = payload
	}
	record.Payload, err = json.Marshal(envelope.State)
	if err != nil {
		return api.ReleaseWorkflowStateRecord{}, fmt.Errorf("db restore workflow state: %w", err)
	}
	return record, nil
}
