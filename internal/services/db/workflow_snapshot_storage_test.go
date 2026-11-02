// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowSnapshotStoragePreservesHistoricalReceiptVersions(t *testing.T) {
	raw := workflowSnapshotStorageTestDB(t)
	ctx := t.Context()
	const current = `{"id":"release","revision":2,"release":{"generation":12},"opaque":{"nested":"new"}}`
	const historical = `{"id":"release","revision":1,"release":{"generation":11},"opaque":{"nested":"old"}}`
	payload := []byte(`{"SourcePath":"synthetic","unknown":{"future":[1,2]},"Catalogs":{},"Media":null,
		"Releases":{"release":` + current + `},"Receipts":{
		"prepare\u0000first":{"Fingerprint":"original","Result":{"release":` + historical + `,"continuation":{"state":"pending"}}},
		"prepare\u0000second":{"Fingerprint":"current","Result":{"release":` + current + `,"workflow":{"revision":9}}}}}`)
	record := api.ReleaseWorkflowStateRecord{
		OwnerID:    "owner",
		WorkflowID: "workflow",
		Payload:    payload,
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO release_workflow_states
		(owner_id, workflow_id, revision, status, state_json, created_at, updated_at) VALUES (?, ?, 9, 'blocked', ?, 'time', 'time')`,
		record.OwnerID, record.WorkflowID, record.Payload); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateWorkflowSnapshotStorage(ctx, raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.QueryRowContext(ctx, `SELECT state_json FROM release_workflow_states`).Scan(&record.Payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(record.Payload, []byte(`"generation"`)) {
		t.Fatal("operational state still embeds release snapshots")
	}
	var snapshots, refs, receipts int
	if err := raw.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM release_workflow_snapshots),
		(SELECT COUNT(*) FROM release_workflow_snapshot_refs), (SELECT COUNT(*) FROM release_workflow_receipts)`).
		Scan(&snapshots, &refs, &receipts); err != nil {
		t.Fatal(err)
	}
	if snapshots != 2 || refs != 3 || receipts != 2 {
		t.Fatalf("snapshot/refs/receipt counts = %d/%d/%d, want 2/3/2", snapshots, refs, receipts)
	}
	loaded, err := loadWorkflowSnapshotStorage(ctx, raw, record)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowStorageJSONEqual(t, loaded.Payload, payload)
	rows, err := raw.QueryContext(ctx, `SELECT snapshot_json FROM release_workflow_snapshots`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stored := make(map[string]bool)
	for rows.Next() {
		var snapshot []byte
		if err := rows.Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		stored[string(snapshot)] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !stored[current] || !stored[historical] {
		t.Fatal("immutable snapshot bytes were rewritten")
	}
	// Replacing current state must keep the historical receipt's exact generation
	// until that receipt itself is removed, then retire the unreferenced object.
	replacement := []byte(`{"Releases":{"release":` + current + `},"Receipts":{},"Catalogs":{}}`)
	record.Payload = replacement
	compact, err := saveWorkflowSnapshotStorage(ctx, raw, record)
	if err != nil {
		t.Fatal(err)
	}
	record.Payload = compact
	loaded, err = loadWorkflowSnapshotStorage(ctx, raw, record)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowStorageJSONEqual(t, loaded.Payload, replacement)
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM release_workflow_snapshots`).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatalf("unreferenced historical snapshot retained: count=%d err=%v", snapshots, err)
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM release_workflow_states`); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM release_workflow_snapshots`).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatalf("workflow deletion left snapshots: count=%d err=%v", snapshots, err)
	}
}

func TestWorkflowSnapshotStorageRejectsIncompleteOrChangedReferences(t *testing.T) {
	for _, damage := range []struct {
		name  string
		query string
		want  string
	}{
		{"missing reference", `DELETE FROM release_workflow_snapshot_refs`, "incomplete"},
		{"missing receipt", `DELETE FROM release_workflow_receipts`, "incomplete"},
		{"changed snapshot", `UPDATE release_workflow_snapshots SET snapshot_json = '{}'`, "digest mismatch"},
	} {
		t.Run(damage.name, func(t *testing.T) {
			raw := workflowSnapshotStorageTestDB(t)
			ctx := t.Context()
			record := api.ReleaseWorkflowStateRecord{
				OwnerID:    "owner",
				WorkflowID: "workflow",
				Payload:    []byte(`{"Releases":{"release":{"id":"release","revision":1}},"Receipts":{"key":{"Fingerprint":"fp","Result":{"release":{"id":"release","revision":1}}}}}`),
			}
			if _, err := raw.ExecContext(ctx, `INSERT INTO release_workflow_states
				(owner_id, workflow_id, revision, status, state_json, created_at, updated_at) VALUES (?, ?, 1, 'blocked', ?, 'time', 'time')`,
				record.OwnerID, record.WorkflowID, record.Payload); err != nil {
				t.Fatal(err)
			}
			var err error
			record.Payload, err = saveWorkflowSnapshotStorage(ctx, raw, record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.ExecContext(ctx, damage.query); err != nil {
				t.Fatal(err)
			}
			if _, err := loadWorkflowSnapshotStorage(ctx, raw, record); err == nil || !strings.Contains(err.Error(), damage.want) {
				t.Fatalf("damaged snapshot storage error = %v, want %s", err, damage.want)
			}
		})
	}
}

func TestWorkflowSnapshotStorageRetainsMalformedLegacyEvidence(t *testing.T) {
	for _, payload := range []string{`{"Releases":`, `null`, `{"Releases":{"r":{"id":"r"}},"Receipts":{"key":null}}`} {
		t.Run(payload, func(t *testing.T) {
			raw := workflowSnapshotStorageTestDB(t)
			record := api.ReleaseWorkflowStateRecord{
				OwnerID:    "owner",
				WorkflowID: "workflow",
				Payload:    []byte(payload),
			}
			stored, err := saveWorkflowSnapshotStorage(t.Context(), raw, record)
			if err != nil || string(stored) != payload {
				t.Fatalf("legacy evidence changed: stored=%s err=%v", stored, err)
			}
		})
	}
}

func workflowSnapshotStorageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.ExecContext(t.Context(), `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if err := migrateAddReleaseWorkflowStates(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	if err := migrateWorkflowSnapshotStorage(t.Context(), raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertWorkflowStorageJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("workflow round trip changed logical JSON:\ngot %s\nwant %s", got, want)
	}
}
