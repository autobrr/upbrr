// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDecodedWorkflowRetainsUnsupportedAndRequiresExplicitRetryForMissingReport(t *testing.T) {
	for _, name := range []string{"legacy_scene", "bounded_boundary", "v3_scene"} {
		t.Run(name, func(t *testing.T) {
			boundary := name == "bounded_boundary"
			builder, resolver, _ := hdrFixture(t)
			data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "single-hybrid.mkv"))
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "synthetic.mkv")
			if err := os.WriteFile(source, data, 0o600); err != nil {
				t.Fatal(err)
			}
			resolver.subject.Targets[0].Path = source
			resolver.subject.Targets[0].DiscRoot = ""
			release := resolver.subject.Release
			request := api.HDRAnalysisInstructions{
				Release:        release,
				TargetIDs:      []string{resolver.subject.Targets[0].Target.ID},
				ProfileVersion: api.HDRAnalysisProfileV2,
				Decoded:        &api.HDRDecodedOptions{SceneSync: true},
			}
			if boundary {
				request.ProfileVersion = api.HDRAnalysisProfileV3
				request.Decoded = &api.HDRDecodedOptions{BoundarySync: true, VideoDecoderThreads: 4}
			}
			if name == "v3_scene" {
				request.ProfileVersion = api.HDRAnalysisProfileV3
				request.Decoded.VideoDecoderThreads = 4
			}
			entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
			resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
			publish := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
				entries[record.ID], resources[record.ID] = record, resource
				return nil
			}
			store := releaseworkflow.HDRMeasurementStore{
				Records:   make(map[string]releaseworkflow.HDRMeasurementRecord),
				Resources: make(map[string]releaseworkflow.RetainedHDRMeasurementResource),
			}
			store.Publish = func(record releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
				if len(entries) != 1 {
					t.Fatal("decoded checkpoint preceded native checkpoint")
				}
				if _, err := restoreHDRMeasurement(t.Context(), record, resource); err != nil {
					t.Fatal(err)
				}
				store.Records[record.ID], store.Resources[record.ID] = record, resource
				return nil
			}
			first, plots, err := builder.Build(
				t.Context(),
				releaseworkflow.HDRPreparation{Release: release},
				request,
				"decoded-first",
				time.Now(),
				nil,
				nil,
				entries,
				resources,
				publish,
				&store,
			)
			if err != nil || len(store.Records) != 1 || len(first.Targets[0].Decoded) != 1 || first.Targets[0].Decoded[0].State != api.HDRDecodedUnsupported {
				t.Fatalf("initial decoded result=%+v records=%d err=%v", first, len(store.Records), err)
			}
			first.ID, first.WorkflowID, first.Revision = "decoded", "workflow", 1
			for _, format := range first.Targets[0].Formats {
				for _, check := range format.Checks {
					if check.Code == "report_unavailable" {
						t.Fatal("unsupported picture mapping must retain the structural report")
					}
				}
			}
			if err := first.Validate(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			second, _, err := builder.Build(
				t.Context(),
				releaseworkflow.HDRPreparation{Release: release},
				request,
				"decoded-reuse",
				time.Now(),
				&first,
				plots,
				entries,
				resources,
				publish,
				&store,
			)
			if err != nil || len(store.Records) != 1 || second.Targets[0].Decoded[0].State != api.HDRDecodedUnsupported {
				t.Fatalf("terminal decoded result was not reused: %+v %v", second, err)
			}
			second.ID, second.WorkflowID, second.Revision = "decoded-reused", "workflow", 1
			if err := second.Validate(); err != nil {
				t.Fatal(err)
			}
			if !boundary {
				for _, profile := range []string{api.HDRAnalysisProfileV3, api.HDRAnalysisProfileV2} {
					projectedRequest := request
					projectedRequest.ProfileVersion = profile
					projectedRequest.Decoded = &api.HDRDecodedOptions{SceneSync: true}
					if profile == api.HDRAnalysisProfileV3 {
						projectedRequest.Decoded.VideoDecoderThreads = 4
					}
					projected, _, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: release}, projectedRequest,
						"project-"+profile, time.Now(), &second, nil, entries, resources, publish, &store)
					if err != nil || len(store.Records) != 1 {
						t.Fatalf("profile projection remeasured source: %v", err)
					}
					projected.ID, projected.WorkflowID, projected.Revision = "projected", "workflow", 1
					if err := projected.Validate(); err != nil {
						t.Fatal(err)
					}
				}
			}
			clear(store.Resources)
			third, _, err := builder.Build(
				t.Context(),
				releaseworkflow.HDRPreparation{Release: release},
				request,
				"decoded-missing",
				time.Now(),
				&second,
				nil,
				entries,
				resources,
				publish,
				&store,
			)
			if err != nil || len(store.Records) != 1 || third.Targets[0].Decoded[0].State != api.HDRDecodedFailed ||
				third.Targets[0].Decoded[0].Reason != "resource_unavailable" {
				t.Fatalf("missing report granted source reads: %+v %v", third, err)
			}
			third.ID, third.WorkflowID, third.Revision = "decoded-missing", "workflow", 1
			if err := third.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDecodedComparisonWorkflowCheckpointAndRenderReuse(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires calibrated actual decoder")
	}
	builder, resolver, _ := hdrFixture(t)
	data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	primary, comparison := filepath.Join(dir, "Synthetic.Primary.mkv"), filepath.Join(dir, "Synthetic.Comparison.mkv")
	for _, path := range []string{primary, comparison} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	verified, err := verifyWorkflowInput(t.Context(), api.PrepareInput{SourcePath: comparison})
	if err != nil {
		t.Fatal(err)
	}
	evidence := api.HDRSourceEvidence{UniqueHEVC: true, Decoded: &api.HDRDecodedSourceEvidence{
		VideoStreams:           1,
		Codec:                  "hevc",
		Width:                  192,
		Height:                 112,
		BitDepth:               10,
		PixelAspectNumerator:   1,
		PixelAspectDenominator: 1,
		Range:                  "limited",
		Matrix:                 "bt2020nc",
		Transfer:               "smpte2084",
		Primaries:              "bt2020",
	}}
	resolver.subject.Targets[0].Path, resolver.subject.Targets[0].DiscRoot = primary, ""
	resolver.subject.Targets[0].Target.Evidence = evidence
	release := resolver.subject.Release
	request := api.HDRAnalysisInstructions{
		Release:        release,
		TargetIDs:      []string{resolver.subject.Targets[0].Target.ID},
		ProfileVersion: api.HDRAnalysisProfileV2,
		Decoded: &api.HDRDecodedOptions{Comparison: &api.HDRComparisonOptions{
			SourceID:    "source-b",
			FrameOffset: new(0),
			Crop: api.HDRCrop{
				Top:    12,
				Width:  192,
				Height: 88,
			},
			Grade:  true,
			Colour: true,
		}},
	}
	databasePath, vaultRoot := filepath.Join(t.TempDir(), "hdr.sqlite"), t.TempDir()
	repository, err := db.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	prepared, err := preparedrelease.New(repository, workflowMediaIdentityResolver{}, hdrWorkflowCollector{evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := prepared.Prepare(t.Context(), api.PrepareInput{SourcePath: primary})
	if err != nil {
		t.Fatal(err)
	}
	release = api.ReleaseRef{SourcePath: primary, Generation: facts.Release.Generation}
	targets, err := prepared.HDRAnalysisTargets(t.Context(), release)
	if err != nil || len(targets) != 1 {
		t.Fatalf("prepared targets: %+v %v", targets, err)
	}
	preparation := releaseworkflow.HDRPreparation{Release: release, Targets: targets}
	request.Release, request.TargetIDs = release, []string{targets[0].ID}
	builder.resolver = prepared
	persistent, err := releaseworkflow.NewPersistentRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state, _, err := persistent.Create(t.Context(), "owner", "create", "fixture", releaseworkflow.State{
		OwnerID: "owner", Workflow: api.ReleaseWorkflow{
			ID:        "workflow",
			Revision:  1,
			Status:    api.WorkflowStatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	vault, err := releaseworkflow.NewPrivateArtifactVault(
		vaultRoot,
		hdrResourceCodec(builder.root, "extraction"),
		hdrResourceCodec(builder.root, "measurement"),
	)
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
	publish := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
		entries[record.ID], resources[record.ID] = record, resource
		if err := vault.PutWithoutExpiry("owner", state.Workflow.ID, string(record.ID), resource); err != nil {
			t.Fatal(err)
		}
		if err := persistent.CheckpointHDRExtractions(t.Context(), "owner", state.Workflow.ID, state.Workflow.Revision, entries); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	store := releaseworkflow.HDRMeasurementStore{
		Comparison: &releaseworkflow.HDRComparisonSource{
			Ref: api.HDRComparisonSourceRef{
				ID:          "source-b",
				Release:     release,
				Fingerprint: verified.SourceVersion,
			},
			Path:     verified.CanonicalPath,
			Evidence: evidence,
		},
		Records:   make(map[string]releaseworkflow.HDRMeasurementRecord),
		Resources: make(map[string]releaseworkflow.RetainedHDRMeasurementResource),
	}
	if err := verifyHDRComparison(t.Context(), *store.Comparison); err != nil {
		t.Fatalf("comparison verification: %v", err)
	}
	interruptedCtx, interrupt := context.WithCancel(t.Context())
	defer interrupt()
	cancelAfterFirst := true
	store.Publish = func(record releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
		if len(entries) != 2 {
			t.Fatal("measurement preceded source B native checkpoint")
		}
		store.Records[record.ID], store.Resources[record.ID] = record, resource
		if err := vault.PutWithoutExpiry("owner", state.Workflow.ID, record.ID, resource); err != nil {
			t.Fatal(err)
		}
		if err := persistent.CheckpointHDRMeasurements(t.Context(), "owner", state.Workflow.ID, state.Workflow.Revision, store.Records); err != nil {
			t.Fatal(err)
		}
		if cancelAfterFirst {
			interrupt()
		}
		return nil
	}
	interrupted, _, err := builder.Build(
		interruptedCtx,
		preparation,
		request,
		"comparison-interrupted",
		time.Now(),
		nil,
		nil,
		entries,
		resources,
		publish,
		&store,
	)
	if err != nil || interrupted.Status != api.StageStatusCanceled || len(store.Records) != 1 {
		t.Fatalf("completed mode did not survive cancellation: %+v records=%d err=%v", interrupted, len(store.Records), err)
	}
	retainedGradeID := ""
	for id, record := range store.Records {
		if record.Result.Mode != api.HDRDecodedGrade || record.Result.State != api.HDRDecodedComplete {
			t.Fatalf("cancellation changed completed measurement: %+v", record)
		}
		retainedGradeID = id
	}
	cancelAfterFirst = false
	first, _, err := builder.Build(t.Context(), preparation, request, "comparison-first", time.Now(), &interrupted, nil, entries, resources, publish, &store)
	if err != nil || len(store.Records) != 2 || len(first.Targets) != 1 || len(first.Targets[0].Decoded) != 2 {
		t.Fatalf("comparison result %+v %v", first, err)
	}
	if _, ok := store.Records[retainedGradeID]; !ok {
		t.Fatal("resume discarded the completed grade checkpoint")
	}
	for _, mode := range first.Targets[0].Decoded {
		if mode.State != api.HDRDecodedComplete || len(mode.Artifacts) != 1 {
			t.Fatalf("comparison mode: %+v", mode)
		}
	}
	first.ID, first.WorkflowID, first.Revision = "comparison", "workflow", 1
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	// A fresh SQLite connection and durable vault must restore both numeric modes
	// even after the source files disappear and there is no retained PNG result.
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err = db.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	persistent, err = releaseworkflow.NewPersistentRepository(repository)
	if err != nil {
		t.Fatal(err)
	}
	state, err = persistent.Load(t.Context(), "owner", "workflow")
	if err != nil || len(state.HDRExtractions) != 2 || len(state.HDRMeasurements) != 2 {
		t.Fatalf("restarted checkpoint: %+v %v", state, err)
	}
	vault, err = releaseworkflow.NewPrivateArtifactVault(vaultRoot, hdrResourceCodec(builder.root, "extraction"), hdrResourceCodec(builder.root, "measurement"))
	if err != nil {
		t.Fatal(err)
	}
	entries, store.Records = state.HDRExtractions, state.HDRMeasurements
	clear(resources)
	clear(store.Resources)
	for id := range entries {
		value, err := vault.Get("owner", "workflow", string(id), now)
		if err != nil {
			t.Fatal(err)
		}
		resource, ok := value.(releaseworkflow.RetainedHDRExtractionResource)
		if !ok {
			t.Fatal("restored native resource has wrong type")
		}
		resources[id] = resource
	}
	for id := range store.Records {
		if _, err := vault.Get("foreign", "workflow", id, now); err == nil {
			t.Fatal("foreign owner read numeric evidence")
		}
		value, err := vault.Get("owner", "workflow", id, now)
		if err != nil {
			t.Fatal(err)
		}
		resource, ok := value.(releaseworkflow.RetainedHDRMeasurementResource)
		if !ok {
			t.Fatal("restored measurement has wrong type")
		}
		store.Resources[id] = resource
	}
	for _, path := range []string{primary, comparison} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err = preparedrelease.New(repository, workflowMediaIdentityResolver{}, hdrWorkflowCollector{evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	builder.resolver = prepared
	second, _, err := builder.Build(t.Context(), preparation, request, "comparison-render", time.Now(), &first, nil, entries, resources, publish, &store)
	if err != nil || len(store.Records) != 2 || len(entries) != 2 {
		t.Fatalf("render reuse reread native or decoded source: %+v %v", second, err)
	}
	for _, mode := range second.Targets[0].Decoded {
		if mode.State != api.HDRDecodedComplete || len(mode.Artifacts) != 1 {
			t.Fatalf("retained comparison mode: %+v", mode)
		}
	}
}

// The pipeline supplies authored MediaInfo facts; generation persistence, source
// validation and retained authority are the production prepared-release module.
type hdrWorkflowCollector struct{ evidence api.HDRSourceEvidence }

func (c hdrWorkflowCollector) HydratePrivateResources(ctx context.Context, request preparationstate.Request) (preparedrelease.CollectedResources, error) {
	facts, err := c.Collect(ctx, request)
	return facts.Resources, err
}
func (c hdrWorkflowCollector) Collect(ctx context.Context, request preparationstate.Request) (preparedrelease.CollectedFacts, error) {
	facts, err := (workflowMediaCollector{}).Collect(ctx, request)
	key := filepath.Clean(request.Manifest.SourcePath)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	facts.Resources.HDRFileEvidence = map[string]api.HDRSourceEvidence{key: c.evidence}
	return facts, err
}
