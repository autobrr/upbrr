// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func memorySnapshotFixture() State {
	return State{
		Workflow: api.ReleaseWorkflow{
			ID:       "snapshot",
			Revision: 1,
			Release:  &api.ReleaseSnapshotRef{ID: "release", Revision: 1},
		},
		TrackerInputAnswers: map[api.TrackerID]map[string]string{"tracker": {"answer": "original"}},
		Releases: map[api.ReleaseSnapshotID]api.ReleaseSnapshot{"release": {
			ID:       "release",
			Revision: 1,
			Release:  api.PreparedRelease{Generation: 1, Source: api.SourceManifest{SourcePath: "synthetic.mkv"}},
		}},
	}
}

func newMemorySnapshotFixture(t *testing.T) (*MemoryRepository, State) {
	t.Helper()
	repository := NewMemoryRepository()
	state, replay, err := repository.Create(t.Context(), "owner", "creation", "fingerprint", memorySnapshotFixture())
	if err != nil || replay {
		t.Fatalf("create: replay=%v err=%v", replay, err)
	}
	return repository, state
}

func TestMemorySnapshotIsolationReplayAndSave(t *testing.T) {
	repository := NewMemoryRepository()
	input := memorySnapshotFixture()
	created, _, err := repository.Create(t.Context(), "owner", "creation", "fingerprint", input)
	if err != nil {
		t.Fatal(err)
	}
	input.TrackerInputAnswers["tracker"]["answer"] = "input mutation"
	created.TrackerInputAnswers["tracker"]["answer"] = "output mutation"
	for range 2 {
		loaded, err := repository.Load(t.Context(), "owner", created.Workflow.ID)
		if err != nil || loaded.TrackerInputAnswers["tracker"]["answer"] != "original" {
			t.Fatalf("isolated load: %+v %v", loaded, err)
		}
		loaded.TrackerInputAnswers["tracker"]["answer"] = "load mutation"
	}
	replay, reused, err := repository.Create(t.Context(), "owner", "creation", "fingerprint", input)
	if err != nil || !reused || replay.TrackerInputAnswers["tracker"]["answer"] != "original" {
		t.Fatalf("replay: %+v %v %v", replay, reused, err)
	}
	replay.Workflow.Revision++
	replay.OwnerID = "foreign"
	replay.TrackerInputAnswers["tracker"]["answer"] = "saved"
	if err := repository.Save(t.Context(), "owner", 1, replay); err != nil {
		t.Fatal(err)
	}
	replay.TrackerInputAnswers["tracker"]["answer"] = "later mutation"
	loaded, err := repository.Load(t.Context(), "owner", replay.Workflow.ID)
	if err != nil || loaded.OwnerID != "owner" || loaded.Workflow.Revision != 2 || loaded.TrackerInputAnswers["tracker"]["answer"] != "saved" {
		t.Fatalf("saved isolation: %+v %v", loaded, err)
	}
	if err := repository.Save(t.Context(), "owner", 1, replay); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale save: %v", err)
	}
	if _, err := repository.Load(t.Context(), "foreign", replay.Workflow.ID); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("foreign load: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.Load(ctx, "owner", replay.Workflow.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load: %v", err)
	}
	if err := repository.Delete(t.Context(), "owner", replay.Workflow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(t.Context(), "owner", replay.Workflow.ID); !errors.Is(err, ErrWorkflowNotFound) {
		t.Fatalf("deleted load: %v", err)
	}
	if _, replayed, err := repository.Create(t.Context(), "owner", "creation", "fingerprint", memorySnapshotFixture()); err != nil || replayed {
		t.Fatalf("recreate: %v %v", replayed, err)
	}
}

func TestMemorySnapshotJSONNormalization(t *testing.T) {
	repository := NewMemoryRepository()
	input := memorySnapshotFixture()
	input.descriptionReuse = &api.ReusableDescriptionRecord{}
	input.Catalogs = map[api.TrackerCatalogSnapshotID]api.TrackerCatalogSnapshot{"catalog": {
		Trackers: []api.TrackerCatalogDescriptor{{ConfigurationFields: []api.TrackerCatalogField{{Default: map[string]any{
			"integer": uint64(1<<63 + 1),
			"zero":    0,
			"false":   false,
		}}}}},
	}}
	input.ProjectionInstructions = map[api.TrackerProjectionInstructionSnapshotID]api.TrackerProjectionInstructionSnapshot{"projection": {
		Instructions: map[api.TrackerID]api.TrackerProjectionInstructions{
			"absent":   {},
			"reset":    {UploadReleaseName: api.WorkflowPatch[string]{Present: true, Reset: true}},
			"explicit": {UploadReleaseName: api.WorkflowPatch[string]{Present: true, Value: ""}},
		},
	}}
	want, err := cloneState(input)
	if err != nil {
		t.Fatal(err)
	}
	want.OwnerID = "owner"
	want, err = cloneState(want)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := repository.Create(t.Context(), "owner", "", "", input)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("create differs from JSON normalization: %v", err)
	}
	for range 2 {
		got, err = repository.Load(t.Context(), "owner", input.Workflow.ID)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("load differs from JSON normalization: %v", err)
		}
	}
}

func TestMemorySnapshotSavePreservesDecodeErrorAndPriorState(t *testing.T) {
	repository, state := newMemorySnapshotFixture(t)
	state.Workflow.Revision++
	state.Catalogs = map[api.TrackerCatalogSnapshotID]api.TrackerCatalogSnapshot{"catalog": {
		Trackers: []api.TrackerCatalogDescriptor{{ConfigurationFields: []api.TrackerCatalogField{{Default: json.Number("1e1000")}}}},
	}}
	if _, err := json.Marshal(state); err != nil {
		t.Fatalf("fixture must marshal: %v", err)
	}
	err := repository.Save(t.Context(), "owner", 1, state)
	if err == nil || !strings.Contains(err.Error(), "clone workflow state: unmarshal:") {
		t.Fatalf("save decode error: %v", err)
	}
	// Serialization failure still precedes ownership/revision checks.
	if err := repository.Save(t.Context(), "foreign", 99, state); err == nil || !strings.Contains(err.Error(), "unmarshal:") {
		t.Fatalf("error precedence: %v", err)
	}
	loaded, err := repository.Load(t.Context(), "owner", state.Workflow.ID)
	if err != nil || loaded.Workflow.Revision != 1 || len(loaded.Catalogs) != 0 {
		t.Fatalf("failed save changed state: %+v %v", loaded, err)
	}
}

func TestMemorySnapshotHDRCheckpointReplacementAndNormalization(t *testing.T) {
	repository, state := newMemorySnapshotFixture(t)
	// Prime the normalized entry before every same-revision replacement.
	if _, err := repository.Load(t.Context(), "owner", state.Workflow.ID); err != nil {
		t.Fatal(err)
	}
	extractions := map[api.HDRExtractionID]HDRExtractionRecord{"extraction": {ID: "extraction", Sequence: 7}}
	if err := repository.CheckpointHDRExtractions(t.Context(), "owner", state.Workflow.ID, 1, extractions); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.Load(t.Context(), "owner", state.Workflow.ID)
	if err != nil || loaded.HDRExtractions["extraction"].Sequence != 7 || loaded.Workflow.Revision != 1 {
		t.Fatalf("extraction checkpoint: %+v %v", loaded, err)
	}
	loaded.Workflow.Revision = 2
	if err := repository.Save(t.Context(), "owner", 1, loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(t.Context(), "owner", state.Workflow.ID); err != nil {
		t.Fatal(err)
	}
	measurements := map[string]HDRMeasurementRecord{"measurement": {ID: "measurement", Result: api.HDRDecodedResult{Checks: []api.HDRCheckResult{{Message: "first"}}}}}
	if err := repository.CheckpointHDRMeasurements(t.Context(), "owner", state.Workflow.ID, 2, measurements); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(t.Context(), "owner", state.Workflow.ID); err != nil {
		t.Fatal(err)
	}
	// Preserve the checkpoint method's existing nested-slice behavior. A Save
	// subsequently normalizes and detaches the entire state.
	measurements["measurement"].Result.Checks[0].Message = "changed"
	loaded, err = repository.Load(t.Context(), "owner", state.Workflow.ID)
	if err != nil || loaded.HDRMeasurements["measurement"].Result.Checks[0].Message != "changed" {
		t.Fatalf("checkpoint compatibility: %+v %v", loaded, err)
	}
	loaded.Workflow.Revision = 3
	if err := repository.Save(t.Context(), "owner", 2, loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Load(t.Context(), "owner", state.Workflow.ID); err != nil {
		t.Fatal(err)
	}
	pending := PendingHDRRequest{Instructions: api.HDRAnalysisInstructions{
		Release: api.ReleaseRef{SourcePath: "synthetic.mkv", Generation: 1}, TargetIDs: []string{"hdr_" + strings.Repeat("a", 32)},
	}, RetryAfterSequence: 9}
	if err := repository.CheckpointHDRRequest(t.Context(), "owner", state.Workflow.ID, 3, pending); err != nil {
		t.Fatal(err)
	}
	loaded, err = repository.Load(t.Context(), "owner", state.Workflow.ID)
	if err != nil || loaded.HDRPendingRequest == nil || loaded.HDRPendingRequest.RetryAfterSequence != 9 || loaded.Workflow.Revision != 3 {
		t.Fatalf("request checkpoint: %+v %v", loaded, err)
	}
	if err := repository.CheckpointHDRMeasurements(t.Context(), "owner", state.Workflow.ID, 99, nil); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale checkpoint: %v", err)
	}
	loaded.Workflow.Revision++
	if err := repository.Save(t.Context(), "owner", 3, loaded); err != nil {
		t.Fatal(err)
	}
	measurements["measurement"].Result.Checks[0].Message = "after save"
	loaded.HDRMeasurements["measurement"].Result.Checks[0].Message = "detached output"
	normalized, err := repository.Load(t.Context(), "owner", state.Workflow.ID)
	if err != nil || normalized.HDRMeasurements["measurement"].Result.Checks[0].Message != "changed" {
		t.Fatalf("save normalization: %+v %v", normalized, err)
	}
}

func TestMemorySnapshotConcurrentLoadsAndReplacements(t *testing.T) {
	repository, state := newMemorySnapshotFixture(t)
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 30 {
				loaded, err := repository.Load(t.Context(), "owner", state.Workflow.ID)
				if err != nil {
					t.Error(err)
					return
				}
				loaded.TrackerInputAnswers["tracker"]["answer"] = "reader"
			}
		})
	}
	for revision := api.WorkflowRevision(1); revision <= 15; revision++ {
		if err := repository.CheckpointHDRExtractions(t.Context(), "owner", state.Workflow.ID, revision,
			map[api.HDRExtractionID]HDRExtractionRecord{"extraction": {Sequence: uint64(revision)}}); err != nil {
			t.Fatal(err)
		}
		loaded, err := repository.Load(t.Context(), "owner", state.Workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		loaded.Workflow.Revision++
		if err := repository.Save(t.Context(), "owner", revision, loaded); err != nil {
			t.Fatal(err)
		}
	}
	readers.Wait()
	loaded, err := repository.Load(t.Context(), "owner", state.Workflow.ID)
	if err != nil || loaded.Workflow.Revision != 16 || loaded.TrackerInputAnswers["tracker"]["answer"] != "original" {
		t.Fatalf("concurrent isolation: %+v %v", loaded, err)
	}
}

func BenchmarkMemorySnapshotLoad(b *testing.B) {
	for _, entries := range []int{1, 100} {
		b.Run(fmt.Sprintf("answers_%d", entries), func(b *testing.B) {
			repository := NewMemoryRepository()
			state := memorySnapshotFixture()
			for i := range entries {
				state.TrackerInputAnswers["tracker"][fmt.Sprintf("question-%d", i)] = strings.Repeat("synthetic answer ", 16)
			}
			if _, _, err := repository.Create(b.Context(), "owner", "", "", state); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := repository.Load(b.Context(), "owner", state.Workflow.ID); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
