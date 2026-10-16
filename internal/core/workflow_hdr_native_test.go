// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	dv "github.com/Audionut/go-dovi-extract/extract"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNativeHDRRejectedCollectorsPublishTerminalResult(t *testing.T) {
	for _, mode := range []struct {
		name, profile string
		decoded       *api.HDRDecodedOptions
	}{
		{name: "v2", profile: api.HDRAnalysisProfileV2},
		{name: "v3", profile: api.HDRAnalysisProfileV3},
		{
			name:    "boundary",
			profile: api.HDRAnalysisProfileV3,
			decoded: &api.HDRDecodedOptions{BoundarySync: true},
		},
	} {
		for _, retry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retry=%t", mode.name, retry), func(t *testing.T) {
				builder, resolver, sidecar := hdrFixture(t)
				data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "single-hybrid.mkv"))
				if err != nil {
					t.Fatal(err)
				}
				// Reject the codec configuration in both real native collectors.
				at := bytes.Index(data, []byte{0x40, 1, 0x2c, 1, 0xff, 0xff, 2})
				if at < 0 {
					t.Fatal("authored VPS missing")
				}
				data[at] |= 0x80
				source := filepath.Join(t.TempDir(), "Synthetic.Unsupported.mkv")
				if err := os.WriteFile(source, data, 0o600); err != nil {
					t.Fatal(err)
				}
				resolver.subject.Targets[0].Path = source
				release := resolver.subject.Release
				entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
				resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
				var prior *api.HDRAnalysisResult
				if retry {
					native := &hdranalysis.NativeExtraction{
						HDR10Plus:        sidecar.Extraction,
						DolbyVisionError: dv.ErrUnsupportedInput,
						ScanComplete:     true,
						TrackID:          sidecar.Identity.ResolvedTrackID,
					}
					evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
					if err != nil {
						t.Fatal(err)
					}
					defer evidence.Release()
					record, owned, err := builder.retainNativeExtraction(t.Context(), release, sidecar.Identity, api.HDRAnalysisFormatsBoth, evidence, "previous")
					if err != nil {
						t.Fatal(err)
					}
					entries[record.ID], resources[record.ID] = record, owned
					prior = &api.HDRAnalysisResult{Release: release, Targets: []api.HDRAnalysisTargetResult{{
						TargetID: sidecar.Identity.TargetID,
						Status:   api.StageStatusFailed,
						Failure:  &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable},
					}}}
				}
				result, artifacts, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: release}, api.HDRAnalysisInstructions{
					Release:        release,
					TargetIDs:      []string{sidecar.Identity.TargetID},
					ProfileVersion: mode.profile,
					Decoded:        mode.decoded,
				}, "rejected", time.Now().UTC(), prior, nil, entries, resources,
					func(releaseworkflow.HDRExtractionRecord, releaseworkflow.RetainedHDRExtractionResource) error {
						t.Error("rejected native scan published a checkpoint")
						return nil
					}, nil)
				if err != nil || result.Status != api.StageStatusFailed || len(result.Targets) != 1 {
					t.Fatalf("terminal rejection: result=%+v err=%v", result, err)
				}
				result.ID, result.WorkflowID, result.Revision = "rejected-hdr", "workflow", 1
				if err := result.Validate(); err != nil {
					t.Fatalf("typed parser failures cannot be published: %v", err)
				}
				outcome := result.Targets[0]
				for _, format := range outcome.Formats {
					if format.State != api.HDRExtractionFailed || format.Failure == nil {
						t.Fatalf("typed parser failure lost: %+v", format)
					}
				}
				if mode.profile == api.HDRAnalysisProfileV3 {
					if len(outcome.Artifacts) != 0 {
						t.Fatalf("uncheckpointed evidence published artifacts: %+v", outcome.Artifacts)
					}
				} else {
					if len(outcome.Artifacts) != 1 || outcome.Artifacts[0].Kind != api.HDRArtifactStructuralReport {
						t.Fatalf("v2 diagnostic report lost: %+v", outcome.Artifacts)
					}
					content, err := artifacts.OpenArtifact(t.Context(), result, outcome.Artifacts[0].ID)
					if err != nil {
						t.Fatal(err)
					}
					if err := content.Body.Close(); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestNativeHDRV3ReportUsesCurrentCheckpoint(t *testing.T) {
	for _, source := range []struct {
		name, fixture string
		state         api.HDRExtractionState
	}{
		{
			name:    "present",
			fixture: "single-hybrid.mkv",
			state:   api.HDRExtractionComplete,
		},
		{
			name:    "absent",
			fixture: "decoded-pq-cuts.mkv",
			state:   api.HDRExtractionAbsent,
		},
	} {
		t.Run(source.name, func(t *testing.T) {
			builder, resolver, _ := hdrFixture(t)
			resolver.subject.Targets[0].Path = filepath.Join("..", "services", "hdranalysis", "testdata", source.fixture)
			release := resolver.subject.Release
			var checkpoint releaseworkflow.HDRExtractionRecord
			result, _, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: release}, api.HDRAnalysisInstructions{
				Release:        release,
				TargetIDs:      []string{resolver.subject.Targets[0].Target.ID},
				ProfileVersion: api.HDRAnalysisProfileV3,
			}, "checkpoint", time.Now().UTC(), nil, nil, nil, nil,
				func(record releaseworkflow.HDRExtractionRecord, _ releaseworkflow.RetainedHDRExtractionResource) error {
					checkpoint = record
					return nil
				}, nil)
			if err != nil || len(result.Targets) != 1 || checkpoint.ID == "" {
				t.Fatalf("complete native scan: result=%+v checkpoint=%+v err=%v", result, checkpoint, err)
			}
			result.ID, result.WorkflowID, result.Revision = "checkpoint-hdr", "workflow", 1
			if err := result.Validate(); err != nil {
				t.Fatal(err)
			}
			outcome := result.Targets[0]
			if outcome.Formats[0].State != source.state {
				t.Fatalf("native HDR10+ outcome: %+v", outcome.Formats[0])
			}
			reportFound := false
			for _, artifact := range outcome.Artifacts {
				if artifact.MeasurementFingerprint != checkpoint.SHA256 {
					t.Fatalf("artifact does not belong to current checkpoint: %+v", artifact)
				}
				reportFound = reportFound || artifact.Kind == api.HDRArtifactStructuralReport
			}
			if !reportFound {
				t.Fatal("completed scan lost its structural report")
			}
		})
	}
}

func TestNativeDiscRetrySelectsOnlyTargetsNeedingSource(t *testing.T) {
	_, resolver, _ := hdrFixture(t)
	subject := resolver.subject
	target := subject.Targets[0]
	target.DiscRoot, target.Path = filepath.Join(t.TempDir(), "BDMV"), ""
	target.Target.Playlist, target.Target.SelectionPolicy = "00001.MPLS", "primary_hevc_angle_zero"
	entry := releaseworkflow.HDRExtractionRecord{
		Release:           subject.Release,
		TargetID:          target.Target.ID,
		SourceFingerprint: subject.SourceFingerprint,
		SelectionPolicy:   target.Target.SelectionPolicy,
		Schema:            api.HDRExtractionSchemaV2,
	}
	entries := map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{"retained": entry}
	prior := &api.HDRAnalysisResult{Release: subject.Release, Targets: []api.HDRAnalysisTargetResult{{
		TargetID: target.Target.ID,
		Status:   api.StageStatusFailed,
		Failure:  &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable},
	}}}
	store := &releaseworkflow.HDRMeasurementStore{NativeRetryAfterSequence: new(uint64(1))}
	if !nativeHDRNeedsSource(subject, target, prior, entries, store) {
		t.Fatal("old checkpoint suppressed explicit source recovery")
	}
	entry.Sequence = 2
	entries["retained"] = entry
	if nativeHDRNeedsSource(subject, target, prior, entries, store) {
		t.Fatal("resumed recovery selected an already checkpointed playlist")
	}
	entry.Schema = api.HDRExtractionSchemaVersion
	entries["retained"] = entry
	if !nativeHDRNeedsSource(subject, target, prior, entries, store) {
		t.Fatal("incompatible checkpoint consumed native recovery")
	}
	entry.Schema = api.HDRExtractionSchemaV2
	entries["retained"] = entry
	if nativeHDRNeedsSource(subject, target, nil, entries, nil) {
		t.Fatal("retained target was selected for fresh assembly")
	}
	// Even when the sidecar is unavailable, a matching retained identity is a
	// recovery outcome and cannot authorize an implicit physical rescan.
	entry.SHA256, entry.Size = "missing", 1
	entries["retained"] = entry
	if nativeHDRNeedsSource(subject, target, nil, entries, nil) {
		t.Fatal("unavailable sidecar silently selected a rescan")
	}
	delete(entries, "retained")
	if !nativeHDRNeedsSource(subject, target, nil, entries, nil) {
		t.Fatal("fresh target omitted from scan")
	}
	target.CapturedNativePath = filepath.Join(t.TempDir(), "retained.json")
	if nativeHDRNeedsSource(subject, target, nil, entries, nil) {
		t.Fatal("captured target selected for another assembly")
	}
	target.CapturedNativePath = ""
	entry.Release.Generation++
	entries["retained"] = entry
	if !nativeHDRNeedsSource(subject, target, nil, entries, nil) {
		t.Fatal("other generation suppressed fresh target")
	}
}

func TestNativeHDRWorkflowCheckpointArtifactsAndReuse(t *testing.T) {
	builder, resolver, _ := hdrFixture(t)
	data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "reordered-hybrid.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "synthetic.mkv")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	resolver.subject.Targets[0].Path = source
	resolver.subject.Targets[0].DiscRoot = ""
	resolver.subject.Targets[0].Target.Evidence = api.HDRSourceEvidence{
		UniqueHEVC:  true,
		HDR10Plus:   true,
		DolbyVision: true,
	}
	release := resolver.subject.Release
	request := api.HDRAnalysisInstructions{
		Release:        release,
		TargetIDs:      []string{resolver.subject.Targets[0].Target.ID},
		ProfileVersion: api.HDRAnalysisProfileV2,
	}
	entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
	publish := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
		entries[record.ID] = record
		resources[record.ID] = resource
		return nil
	}
	ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) {
		if progress.Phase == "rendering" && len(entries) != 1 {
			t.Error("rendering preceded native checkpoint")
		}
	})
	result, artifacts, err := builder.Build(ctx, releaseworkflow.HDRPreparation{Release: release}, request, "native-first", time.Now().UTC(), nil, nil, entries, resources, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != api.StageStatusCompleted || len(result.Targets) != 1 || len(result.Targets[0].Artifacts) != 3 || len(entries) != 1 {
		t.Fatalf("result=%+v checkpoints=%d", result, len(entries))
	}
	result.ID = "native-result"
	result.WorkflowID = "workflow-native"
	result.Revision = 1
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range result.Targets[0].Artifacts {
		content, err := artifacts.OpenArtifact(t.Context(), result, artifact.ID)
		if err != nil {
			t.Fatal(err)
		}
		if content.ContentType != artifact.MIME || content.Filename != artifact.Filename() {
			t.Fatal("artifact media contract differs")
		}
		if err := content.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	copyContext := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) {
		if progress.Phase == "reading_metadata" || progress.Phase == "rendering" {
			t.Error("unchanged native images were regenerated")
		}
	})
	copied, copiedResource, err := builder.Build(copyContext, releaseworkflow.HDRPreparation{Release: release}, request, "native-copy", time.Now().UTC(), &result, artifacts, nil, nil, publish, nil)
	if err != nil || copied.Status != api.StageStatusCompleted || copiedResource == nil || len(copied.Targets[0].Artifacts) != 3 {
		t.Fatalf("native artifact reuse: %+v %v", copied, err)
	}
	legacyFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Title      string
		Peak       api.HDRPeakSource
		Profile    string
		Formats    api.HDRAnalysisFormats
		Dependency string
	}{resolver.subject.Title, result.PeakSource, result.ProfileVersion, result.Formats, hdranalysis.NativeDependencyFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if result.RenderFingerprint == string(legacyFingerprint) {
		t.Fatal("new plots reuse the old renderer identity")
	}
	legacy := result
	legacy.RenderFingerprint = string(legacyFingerprint)
	renderedDV := false
	renderContext := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) {
		if progress.Phase == "reading_metadata" {
			t.Error("renderer migration reread the removed source")
		}
		if progress.Phase == "rendering_dolby_vision" {
			renderedDV = true
		}
	})
	refreshed, _, err := builder.Build(renderContext, releaseworkflow.HDRPreparation{Release: release}, request,
		"native-renderer-upgrade", time.Now().UTC(), &legacy, artifacts, entries, resources, publish, nil)
	if err != nil || refreshed.Status != api.StageStatusCompleted || !renderedDV || len(entries) != 1 {
		t.Fatalf("source-less renderer upgrade: result=%+v rendered=%t err=%v", refreshed, renderedDV, err)
	}
	request.PeakSource = api.HDRPeakMaxSCL
	reused, _, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: release}, request, "native-second", time.Now().UTC(), &result, artifacts, entries, resources, publish, nil)
	if err != nil || reused.Status != api.StageStatusCompleted || len(entries) != 1 {
		t.Fatalf("retained native reuse: %+v %v", reused, err)
	}
}

func TestNativeHDRCheckpointSurvivesRenderCancellation(t *testing.T) {
	for _, profile := range []string{api.HDRAnalysisProfileV2, api.HDRAnalysisProfileV3} {
		t.Run(profile, func(t *testing.T) {
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
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx = hdranalysis.WithProgress(ctx, func(p hdranalysis.Progress) {
				if p.Phase == "rendering" {
					cancel()
				}
			})
			published := 0
			var decoded *api.HDRDecodedOptions
			if profile == api.HDRAnalysisProfileV3 {
				decoded = &api.HDRDecodedOptions{BoundarySync: true}
			}
			result, _, err := builder.Build(ctx, releaseworkflow.HDRPreparation{Release: resolver.subject.Release}, api.HDRAnalysisInstructions{
				Release:        resolver.subject.Release,
				TargetIDs:      []string{resolver.subject.Targets[0].Target.ID},
				ProfileVersion: profile,
				Decoded:        decoded,
			}, "native-cancel", time.Now().UTC(), nil, nil, nil, nil, func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
				published++
				reader, err := resource.OpenExtraction(t.Context(), record)
				if err != nil {
					return fmt.Errorf("open checkpoint: %w", err)
				}
				return reader.Close()
			}, nil)
			if err != nil || published != 1 || result.Status != api.StageStatusCanceled {
				t.Fatalf("status=%s published=%d err=%v", result.Status, published, err)
			}
			result.ID, result.WorkflowID, result.Revision = "canceled-hdr", "workflow", 1
			if err := result.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
