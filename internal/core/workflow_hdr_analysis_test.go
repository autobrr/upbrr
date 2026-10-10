// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Audionut/go-hdr10-plus/extract"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdrResolverFixture struct {
	subject api.HDRAnalysisSubject
	stale   bool
}

func (f *hdrResolverFixture) ResolveHDRAnalysisSubject(_ context.Context, instructions api.HDRAnalysisInstructions) (api.HDRAnalysisSubject, error) {
	if f.stale {
		return api.HDRAnalysisSubject{}, fmt.Errorf(
			"resolve synthetic HDR source: %w",
			api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureStaleSource, Message: "source changed"}, nil),
		)
	}
	subject := f.subject
	subject.Release = instructions.Release
	return subject, nil
}

func hdrFixture(t *testing.T) (workflowHDRAnalysisBuilder, *hdrResolverFixture, hdranalysis.Sidecar) {
	t.Helper()
	release := api.ReleaseRef{SourcePath: filepath.Join(t.TempDir(), "Synthetic.HDR.2026.mkv"), Generation: 1}
	target := api.HDRTargetID(release.SourcePath, "")
	resolver := &hdrResolverFixture{subject: api.HDRAnalysisSubject{
		Release:             release,
		SourceFingerprint:   "synthetic-source",
		ManifestFingerprint: "synthetic-manifest",
		Title:               "Synthetic HDR",
		Targets: []api.HDRAnalysisTargetSubject{{Target: api.HDRAnalysisTarget{
			ID:              target,
			Label:           "Video 1",
			Supported:       true,
			SelectionPolicy: "unique_hevc",
		}, Path: release.SourcePath}},
	}}
	distributions := make([]extract.Distribution, 9)
	for index, percentile := range []uint8{1, 5, 10, 25, 50, 75, 90, 95, 99} {
		distributions[index] = extract.Distribution{Index: percentile, Value: uint32(index+1) * 1000}
	}
	metadata := &extract.Extraction{
		Profile: "A",
		Payloads: []extract.Payload{{
			ApplicationVersion: 1,
			NumWindows:         1,
			MaxSCL:             [3]uint32{5000, 6000, 7000},
			AverageRGB:         1000,
			Distributions:      distributions,
		}},
		Frames:      []extract.Picture{{Stream: extract.StreamKey{TrackID: 1}, PTS: extract.Timestamp{Valid: true, Timescale: 24000}}},
		SceneStarts: []uint64{0},
	}
	sidecar := hdranalysis.Sidecar{Identity: hdranalysis.ExtractionIdentity{
		SourceFingerprint: resolver.subject.SourceFingerprint,
		TargetID:          target,
		SelectionPolicy:   "unique_hevc",
		ResolvedTrackID:   1,
	}, Extraction: metadata}
	return workflowHDRAnalysisBuilder{
		resolver: resolver,
		service:  hdranalysis.New(hdranalysis.NewAdmission(), nil),
		root:     t.TempDir(),
	}, resolver, sidecar
}

func TestHDRRetainedMetadataRerenderCopyAndTamper(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	release := resolver.subject.Release
	record, extractionResource, err := builder.retainExtraction(t.Context(), release, sidecar, "retained")
	if err != nil {
		t.Fatal(err)
	}
	entries := map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{record.ID: record}
	resources := map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource{record.ID: extractionResource}
	request := api.HDRAnalysisInstructions{Release: release, TargetIDs: []string{sidecar.Identity.TargetID}}
	phases := make(map[string]bool)
	ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) { phases[progress.Phase] = true })
	first, plots, err := builder.Build(ctx, release, request, "first", time.Now().UTC(), nil, nil, entries, resources, nil)
	if err != nil || first.Status != api.StageStatusCompleted || plots == nil {
		t.Fatalf("render=%#v, %v", first, err)
	}
	// Source does not exist; successful rerender proves that retained metadata suffices.
	if phases["reading_metadata"] {
		t.Fatal("retained metadata reread source")
	}
	clear(phases)
	second, copied, err := builder.Build(ctx, release, request, "second", time.Now().UTC(), &first, plots, entries, resources, nil)
	if err != nil || second.Status != api.StageStatusCompleted || phases["rendering"] {
		t.Fatalf("PNG reuse=%#v phases=%v err=%v", second, phases, err)
	}
	releaser, ok := plots.(interface{ Release() error })
	if !ok {
		t.Fatal("plot fixture has no resource owner")
	}
	if err := releaser.Release(); err != nil {
		t.Fatal(err)
	}
	content, err := copied.OpenArtifact(t.Context(), second, second.Targets[0].Artifact.ID)
	if err != nil {
		t.Fatalf("copied plot depended on old owner: %v", err)
	}
	_ = content.Body.Close()
	resolver.subject.Release.Generation = 2
	cloned, retained, err := builder.CloneExtractions(t.Context(), resolver.subject.Release, entries, resources, "rebound")
	if err != nil || len(cloned) != 1 {
		t.Fatalf("rebind=%#v %v", cloned, err)
	}
	if err := extractionResource.Release(); err != nil {
		t.Fatal(err)
	}
	for id, entry := range cloned {
		reader, err := retained[id].OpenExtraction(t.Context(), entry)
		if err != nil {
			t.Fatalf("copied extraction depended on old owner: %v", err)
		}
		_ = reader.Close()
	}
	maps.Copy(entries, cloned)
	request.Release = resolver.subject.Release
	rebound, _, err := builder.Build(t.Context(), request.Release, request, "rebound-render", time.Now().UTC(), nil, nil, entries, retained, nil)
	if err != nil || rebound.Status != api.StageStatusCompleted {
		t.Fatalf("old generation shadowed rebound extraction: %#v %v", rebound, err)
	}
	for id, entry := range cloned {
		resource, ok := retained[id].(hdrManagedResource)
		if !ok {
			t.Fatal("retained fixture is not a managed resource")
		}
		if err := os.WriteFile(resource.Files["metadata"].Path, []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := resource.OpenExtraction(t.Context(), entry)
		if !errors.Is(err, releaseworkflow.ErrPrivateResourceIntegrity) {
			t.Fatalf("tamper accepted: %v", err)
		}
	}
}

func TestHDRMultiTargetProgressStaysBelowCompletionUntilSettled(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
	original := resolver.subject.Targets[0]
	resolver.subject.Targets = nil
	for index := range 2 {
		target := original
		target.Target.ID = api.HDRTargetID(original.Path, strconv.Itoa(index))
		target.Target.Label = fmt.Sprintf("Video %d", index+1)
		resolver.subject.Targets = append(resolver.subject.Targets, target)
		sidecar.Identity.TargetID = target.Target.ID
		record, resource, err := builder.retainExtraction(t.Context(), resolver.subject.Release, sidecar, fmt.Sprintf("retained-%d", index))
		if err != nil {
			t.Fatal(err)
		}
		entries[record.ID], resources[record.ID] = record, resource
	}
	var updates []api.WorkflowProgressUpdate
	ctx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) { updates = append(updates, update) })
	result, _, err := builder.Build(
		ctx,
		resolver.subject.Release,
		api.HDRAnalysisInstructions{
			Release:   resolver.subject.Release,
			TargetIDs: []string{resolver.subject.Targets[0].Target.ID, resolver.subject.Targets[1].Target.ID},
		},
		"progress",
		time.Now().UTC(),
		nil,
		nil,
		entries,
		resources,
		nil,
	)
	if err != nil || result.Status != api.StageStatusCompleted {
		t.Fatalf("analysis=%#v err=%v", result, err)
	}
	first, second := false, false
	for _, update := range updates {
		if update.Total == 0 {
			continue
		}
		if update.Total != 200 || update.Completed >= 200 {
			t.Fatalf("premature completion: %#v", update)
		}
		if strings.HasPrefix(update.Message, "Video 1:") {
			first = true
			if update.Completed > 100 {
				t.Fatalf("first target claimed second target: %#v", update)
			}
		}
		if strings.HasPrefix(update.Message, "Video 2:") {
			second = true
			if update.Completed < 100 {
				t.Fatalf("second target reset progress: %#v", update)
			}
		}
	}
	if !first || !second {
		t.Fatalf("missing target progress: %#v", updates)
	}
}

func TestHDRRetainedFailuresDoNotReportSuccessfulCompletion(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			builder, resolver, sidecar := hdrFixture(t)
			entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
			resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
			if partial {
				record, resource, err := builder.retainExtraction(t.Context(), resolver.subject.Release, sidecar, "successful")
				if err != nil {
					t.Fatal(err)
				}
				entries[record.ID], resources[record.ID] = record, resource
				failed := resolver.subject.Targets[0]
				failed.Target.ID = api.HDRTargetID(failed.Path, "failed")
				resolver.subject.Targets = append(resolver.subject.Targets, failed)
			}
			failed := &resolver.subject.Targets[len(resolver.subject.Targets)-1]
			failed.CapturedFailure = &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceLimit, Message: "synthetic resource limit"}
			ids := make([]string, 0, len(resolver.subject.Targets))
			for _, target := range resolver.subject.Targets {
				ids = append(ids, target.Target.ID)
			}
			var updates []api.WorkflowProgressUpdate
			ctx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) { updates = append(updates, update) })
			result, _, err := builder.Build(ctx, resolver.subject.Release, api.HDRAnalysisInstructions{Release: resolver.subject.Release, TargetIDs: ids},
				"retained-failure", time.Now().UTC(), nil, nil, entries, resources, nil)
			want := api.StageStatusFailed
			if partial {
				want = api.StageStatusPartial
			}
			if err != nil || result.Status != want || len(updates) == 0 {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			for _, update := range updates {
				if update.Phase == "hdr_analysis_complete" || update.Status == api.StageStatusCompleted {
					t.Fatalf("false completion: %#v", update)
				}
			}
			if last := updates[len(updates)-1]; last.Status != want {
				t.Fatalf("terminal progress=%#v want=%s", last, want)
			}
		})
	}
}

func TestHDRExtractionCheckpointSurvivesRenderCancellation(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	record, captured, err := builder.retainExtraction(t.Context(), resolver.subject.Release, sidecar, "capture")
	if err != nil {
		t.Fatal(err)
	}
	target := &resolver.subject.Targets[0]
	target.CapturedPath, target.CapturedSize, target.CapturedSHA256, target.CapturedTrackID = captured.Files["metadata"].Path, record.Size, record.SHA256, record.ResolvedTrackID
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	published := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	ctx = hdranalysis.WithProgress(ctx, func(progress hdranalysis.Progress) {
		if progress.Phase == "rendering" {
			if len(published) != 1 {
				t.Error("render began before extraction checkpoint")
			}
			cancel()
		}
	})
	result, plots, err := builder.Build(
		ctx,
		resolver.subject.Release,
		api.HDRAnalysisInstructions{Release: resolver.subject.Release, TargetIDs: []string{sidecar.Identity.TargetID}},
		"cancel",
		time.Now().UTC(),
		nil,
		nil,
		nil,
		nil,
		func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
			published[record.ID] = record
			reader, err := resource.OpenExtraction(t.Context(), record)
			if err == nil {
				_ = reader.Close()
			}
			if err != nil {
				return fmt.Errorf("verify published HDR extraction: %w", err)
			}
			return nil
		},
	)
	if err != nil || result.Status != api.StageStatusCanceled || plots != nil || len(published) != 1 {
		t.Fatalf("cancel=%#v checkpoint=%d err=%v", result, len(published), err)
	}
	if err := captured.Release(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range published {
		path, err := hdrAttemptRoot(builder.root, entry.Release, "hdr-extraction", string(entry.ID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(path, "metadata.json")); err != nil {
			t.Fatalf("published extraction lost: %v", err)
		}
	}
}

func TestHDRArtifactSourceAndPathAuthority(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	if err := builder.ValidateAuthority(t.Context(), api.HDRAnalysisResult{
		Release:             resolver.subject.Release,
		TargetIDs:           []string{sidecar.Identity.TargetID},
		ManifestFingerprint: "other",
	}); err == nil {
		t.Fatal("different manifest accepted")
	}
	resolver.stale = true
	if err := builder.ValidateAuthority(t.Context(), api.HDRAnalysisResult{
		Release:             resolver.subject.Release,
		TargetIDs:           []string{sidecar.Identity.TargetID},
		ManifestFingerprint: resolver.subject.ManifestFingerprint,
	}); err == nil {
		t.Fatal("mutated source accepted")
	}
	escape := hdrManagedResource{
		Root:      builder.root,
		Directory: t.TempDir(),
		Kind:      "analysis",
	}
	if err := escape.Release(); err == nil {
		t.Fatal("outside deletion authority accepted")
	}
}

func TestHDRMissingMetadataRequiresExplicitSourceRetry(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	release := resolver.subject.Release
	record, resource, err := builder.retainExtraction(t.Context(), release, sidecar, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.Release(); err != nil {
		t.Fatal(err)
	}
	entries := map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{record.ID: record}
	retained := map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource{record.ID: resource}
	request := api.HDRAnalysisInstructions{Release: release, TargetIDs: []string{record.TargetID}}
	reading := false
	ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) { reading = reading || progress.Phase == "inspecting_track" })
	first, _, err := builder.Build(ctx, release, request, "missing-first", time.Now().UTC(), nil, nil, entries, retained, nil)
	if err != nil || reading || first.Targets[0].Failure == nil || first.Targets[0].Failure.Code != api.HDRAnalysisFailureResourceUnavailable {
		t.Fatalf("missing metadata triggered source read: reading=%v result=%#v err=%v", reading, first, err)
	}
	// A second explicit attempt may reread the source; this synthetic source deliberately does not exist.
	retry, _, err := builder.Build(ctx, release, request, "missing-retry", time.Now().UTC(), &first, nil, entries, retained, nil)
	if err != nil || !reading || retry.Targets[0].Failure == nil || retry.Targets[0].Failure.Code != api.HDRAnalysisFailureRead {
		t.Fatalf("explicit retry did not recover source access: reading=%v result=%#v err=%v", reading, retry, err)
	}
}

func TestHDRFailedCombinedCaptureRequiresExplicitRetry(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	release := resolver.subject.Release
	failure := api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceLimit, Message: "capture exceeded its limit"}
	resolver.subject.Targets[0].CapturedFailure = &failure
	request := api.HDRAnalysisInstructions{Release: release, TargetIDs: []string{sidecar.Identity.TargetID}}
	reading := false
	ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) { reading = reading || progress.Phase == "inspecting_track" })
	first, _, err := builder.Build(ctx, release, request, "capture-failed", time.Now().UTC(), nil, nil, nil, nil, nil)
	if err != nil || reading || first.Status != api.StageStatusFailed || first.Targets[0].Failure == nil || first.Targets[0].Failure.Code != failure.Code {
		t.Fatalf("failed capture triggered reread: reading=%v result=%#v err=%v", reading, first, err)
	}
	retry, _, err := builder.Build(ctx, release, request, "capture-retry", time.Now().UTC(), &first, nil, nil, nil, nil)
	if err != nil || !reading || retry.Targets[0].Failure == nil || retry.Targets[0].Failure.Code != api.HDRAnalysisFailureRead {
		t.Fatalf("explicit capture retry did not read source: reading=%v result=%#v err=%v", reading, retry, err)
	}
	// A complete extraction from a successful retry wins over the old capture failure.
	reading = false
	record, retained, err := builder.retainExtraction(t.Context(), release, sidecar, "capture-recovered")
	if err != nil {
		t.Fatal(err)
	}
	recovered, _, err := builder.Build(ctx, release, request, "capture-rerender", time.Now().UTC(), &first, nil,
		map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{record.ID: record},
		map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource{record.ID: retained}, nil)
	if err != nil || reading || recovered.Status != api.StageStatusCompleted {
		t.Fatalf("complete metadata was shadowed by capture failure: reading=%v result=%#v err=%v", reading, recovered, err)
	}
}

func TestHDRInterruptedCleanupPreservesCheckpointedMetadata(t *testing.T) {
	builder, resolver, sidecar := hdrFixture(t)
	release := resolver.subject.Release
	protected := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	paths := make(map[string]string)
	for _, id := range []string{"operation-1", "operation-2", "operation-rebind-1", "operation-rebind-2"} {
		record, resource, err := builder.retainExtraction(t.Context(), release, sidecar, id)
		if err != nil {
			t.Fatal(err)
		}
		paths[id] = resource.Directory
		if id == "operation-1" || id == "operation-rebind-1" {
			protected[record.ID] = record
		}
	}
	plot, err := hdrAttemptRoot(builder.root, release, "hdr-analysis", "operation")
	if err != nil {
		t.Fatal(err)
	}
	if err := createHDRDirectory(plot); err != nil {
		t.Fatal(err)
	}
	if err := builder.CleanupAttempt(release, "operation", protected); err != nil {
		t.Fatal(err)
	}
	for id, path := range paths {
		_, err := os.Stat(path)
		_, retained := protected[api.HDRExtractionID(id)]
		if retained && err != nil || !retained && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleanup id=%s retained=%v err=%v", id, retained, err)
		}
	}
	if _, err := os.Stat(plot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unpublished plot remains: %v", err)
	}
	if err := builder.CleanupAttempt(release, "operation", protected); err != nil {
		t.Fatalf("repeated cleanup failed: %v", err)
	}
}
