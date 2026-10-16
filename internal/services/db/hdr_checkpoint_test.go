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

func TestHDRCheckpointRevisionFenceAndTypedInputAssociation(t *testing.T) {
	repo := openMigratedTestRepo(t)
	now := time.Now().UTC()
	source := filepath.Join(t.TempDir(), "Synthetic.HDR.mkv")
	if _, err := repo.SaveInputRecord(t.Context(), api.InputRecord{
		ID:            "hdr-input",
		CanonicalPath: source,
		SourceVersion: "version",
		Manifest:      []byte(`{}`),
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	record := workflowStateRecordForTest("hdr-workflow", api.WorkflowStatusActive, now, `{"Workflow":{}}`)
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `INSERT INTO input_workflow_associations(canonical_path,owner_id,source_version,workflow_id,audio_analysis_id,hdr_analysis_id,updated_at) VALUES(?,?,?,?,?,NULL,?)`, source, record.OwnerID, "version", record.WorkflowID, "retained-audio", formatWorkflowStateTime(now)); err != nil {
		t.Fatal(err)
	}
	record.Payload = []byte(`{"Workflow":{},"HDRExtractions":{"metadata":{"TargetID":"target"}}}`)
	if err := repo.CheckpointReleaseWorkflowResources(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	persisted, err := repo.LoadReleaseWorkflowState(t.Context(), record.OwnerID, record.WorkflowID)
	if err != nil || persisted.Revision != 1 || string(persisted.Payload) != string(record.Payload) {
		t.Fatalf("checkpoint=%#v %v", persisted, err)
	}
	stale := record
	stale.Revision = 2
	if err := repo.CheckpointReleaseWorkflowResources(t.Context(), stale); !errors.Is(err, api.ErrReleaseWorkflowRevisionConflict) {
		t.Fatalf("stale checkpoint=%v", err)
	}
	record.Revision = 2
	record.Payload = []byte(`{"Workflow":{"hdrAnalysis":{"id":"retained-hdr"}},"HDRExtractions":{"metadata":{}}}`)
	if err := repo.SaveReleaseWorkflowState(t.Context(), 1, record); err != nil {
		t.Fatal(err)
	}
	association, err := repo.LoadInputWorkflowAssociation(t.Context(), source, record.OwnerID, "version")
	if err != nil || association.WorkflowID != record.WorkflowID || association.AudioAnalysisID != "retained-audio" || association.HDRAnalysisID != "retained-hdr" {
		t.Fatalf("typed association=%#v %v", association, err)
	}
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	association, err = repo.LoadInputWorkflowAssociation(t.Context(), source, record.OwnerID, "version")
	if err != nil || association.AudioAnalysisID != "retained-audio" || association.HDRAnalysisID != "retained-hdr" {
		t.Fatalf("migration lost association=%#v %v", association, err)
	}
	foreign, err := repo.LoadInputWorkflowAssociation(t.Context(), source, "foreign", "version")
	if err != nil || foreign.WorkflowID != "" {
		t.Fatalf("foreign association=%#v %v", foreign, err)
	}
	active := createIdleActiveInputForTest(t.Context(), t, repo, now, record.OwnerID, record.WorkflowID, "coordinator")
	if err := repo.CheckpointReleaseWorkflowResources(t.Context(), record); !errors.Is(err, api.ErrActiveInputLeaseLost) {
		t.Fatalf("unfenced active checkpoint=%v", err)
	}
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: active.CoordinatorID, Fence: active.Fence})
	if err := repo.CheckpointReleaseWorkflowResources(ctx, record); err != nil {
		t.Fatal(err)
	}
}
