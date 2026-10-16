// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type hdrMeasurementFixture struct{ released bool }

func (*hdrMeasurementFixture) OpenMeasurement(context.Context, HDRMeasurementRecord) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("measurement")), nil
}
func (r *hdrMeasurementFixture) Release() error { r.released = true; return nil }

func TestHDRMeasurementCheckpointSurvivesLaterFailureAndRejectsStaleRevision(t *testing.T) {
	module, repo := newTestModule(t, testPreparer())
	created := executeCommand(t, module, CreateWorkflowCommand{WorkflowID: "measurement-checkpoint"})
	state, err := repo.Load(t.Context(), testOwnerID, created.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	release := api.ReleaseRef{SourcePath: "synthetic.mkv", Generation: 1}
	native := HDRExtractionRecord{
		ID:                "native",
		Release:           release,
		TargetID:          "target",
		SourceFingerprint: "source",
		SHA256:            strings.Repeat("a", 64),
	}
	state.HDRExtractions[native.ID] = native
	if err := repo.CheckpointHDRExtractions(t.Context(), testOwnerID, state.Workflow.ID, state.Workflow.Revision, state.HDRExtractions); err != nil {
		t.Fatal(err)
	}
	store := module.hdrMeasurementStore(t.Context(), testOwnerID, &state, release, time.Now())
	record := HDRMeasurementRecord{
		ID:                "measurement",
		Release:           release,
		TargetID:          native.TargetID,
		SourceFingerprint: native.SourceFingerprint,
		ExtractionID:      native.ID,
		ExtractionSHA256:  native.SHA256,
		Identity:          "identity",
		Size:              11,
		SHA256:            strings.Repeat("b", 64),
		Result: api.HDRDecodedResult{
			Mode:    api.HDRDecodedSceneSync,
			State:   api.HDRDecodedInconclusive,
			Profile: "hdr-scene-v1",
		},
	}
	resource := &hdrMeasurementFixture{}
	if err := store.Publish(record, resource); err != nil {
		t.Fatal(err)
	}
	duplicate := &hdrMeasurementFixture{}
	if err := store.Publish(record, duplicate); !errors.Is(err, ErrPrivateResourceIntegrity) {
		t.Fatalf("duplicate measurement: %v", err)
	}
	if !duplicate.released || resource.released || store.Resources[record.ID] != resource || len(state.HDRMeasurements) != 1 {
		t.Fatal("duplicate checkpoint leaked resource or replaced retained authority")
	}
	loaded, err := repo.Load(t.Context(), testOwnerID, state.Workflow.ID)
	if err != nil || len(loaded.HDRMeasurements) != 1 || loaded.Workflow.Revision != created.Workflow.Revision || loaded.Workflow.HDRAnalysis != nil || resource.released {
		t.Fatalf("measurement was not independently checkpointed: %+v %v", loaded, err)
	}
	rehydrated := module.hdrMeasurementStore(t.Context(), testOwnerID, &loaded, release, time.Now())
	if rehydrated.Resources[record.ID] != resource {
		t.Fatal("retained mode lost private authority")
	}
	foreign := module.hdrMeasurementStore(t.Context(), "foreign", &loaded, release, time.Now())
	if len(foreign.Resources) != 0 {
		t.Fatal("foreign owner accessed measurement")
	}
	state.Workflow.Revision++
	record.ID = "stale"
	staleResource := &hdrMeasurementFixture{}
	if err := store.Publish(record, staleResource); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale measurement checkpoint: %v", err)
	}
	if !staleResource.released || len(state.HDRMeasurements) != 1 {
		t.Fatal("failed checkpoint leaked resource or authority")
	}
}
