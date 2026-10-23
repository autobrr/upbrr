// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNativeResourcePolicyRecovery(t *testing.T) {
	for _, tc := range []struct {
		name          string
		legacy        bool
		failed        int
		absent        bool
		missingSource bool
		captured      bool
		policy        string
		audio         bool
	}{
		{
			name:   "legacy HDR quota",
			legacy: true,
			failed: 0,
		},
		{
			name:   "legacy DV quota",
			legacy: true,
			failed: 1,
		},
		{
			name:          "legacy HDR quota without source",
			legacy:        true,
			failed:        0,
			missingSource: true,
		},
		{
			name:          "legacy DV quota without source",
			legacy:        true,
			failed:        1,
			missingSource: true,
		},
		{
			name:          "legacy captured HDR quota without source",
			legacy:        true,
			failed:        0,
			missingSource: true,
			captured:      true,
		},
		{
			name:          "legacy captured DV quota without source",
			legacy:        true,
			failed:        1,
			missingSource: true,
			captured:      true,
		},
		{name: "current quota", failed: 1},
		{
			name:   "previous three GiB DV quota",
			legacy: true,
			policy: "3gib-v1",
			failed: 1,
		},
		{
			name:   "previous three GiB HDR quota",
			legacy: true,
			policy: "3gib-v1",
			failed: 0,
		},
		{
			name:   "previous three GiB complete",
			legacy: true,
			policy: "3gib-v1",
			failed: -1,
		},
		{
			name:   "previous three GiB absence",
			legacy: true,
			policy: "3gib-v1",
			failed: -1,
			absent: true,
		},
		{
			name:   "previous three GiB DV quota with completed audio",
			legacy: true,
			policy: "3gib-v1",
			failed: 1,
			audio:  true,
		},
		{
			name:          "previous three GiB DV quota with audio and missing source",
			legacy:        true,
			policy:        "3gib-v1",
			failed:        1,
			audio:         true,
			missingSource: true,
		},
		{
			name:   "legacy complete",
			legacy: true,
			failed: -1,
		},
		{
			name:   "legacy absence",
			legacy: true,
			failed: -1,
			absent: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.audio && os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
				t.Skip("requires actual shared decoder")
			}
			t.Parallel()
			builder, resolver, _ := hdrFixture(t)
			data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "single-hybrid.mkv"))
			if err != nil {
				t.Fatal(err)
			}
			source := resolver.subject.Targets[0].Path
			if err := os.WriteFile(source, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.audio {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				executable, err := screenshots.ResolveFFmpegExecutable()
				if err != nil {
					t.Fatal(err)
				}
				command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error",
					"-i", filepath.Join("..", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"),
					"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:duration=4.8",
					"-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "pcm_s16le", source)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("author policy/audio fixture: %v %s", err, output)
				}
			}
			target := resolver.subject.Targets[0].Target
			release := resolver.subject.Release
			formats := api.HDRAnalysisFormatsBoth
			var evidence *hdranalysis.NativeEvidence
			var trackID uint64
			var audioReport *hdranalysis.MeasurementReport
			err = builder.service.WithSession(t.Context(), func(session *hdranalysis.Session) error {
				var native *hdranalysis.NativeExtraction
				var scanErr error
				if tc.audio {
					facts, err := hdranalysis.InspectFileSource(t.Context(), source)
					if err != nil || facts.Decoded == nil {
						t.Fatalf("inspect policy/audio fixture: %+v %v", facts, err)
					}
					resolver.subject.Targets[0].Target.Evidence = facts
					var reports []*hdranalysis.MeasurementReport
					native, reports, scanErr = session.AnalyzeAutomatic(t.Context(), hdranalysis.DecodedInput{
						Path:        source,
						Fingerprint: resolver.subject.SourceFingerprint,
						Source:      *facts.Decoded,
					}, nil, api.HDRDecodedOptions{Audio: true}, formats, t.TempDir(), 0, true)
					if scanErr == nil && len(reports) == 1 {
						audioReport = reports[0]
					}
				} else {
					native, scanErr = session.ExtractNativeFile(t.Context(), source, formats)
				}
				if scanErr != nil {
					return fmt.Errorf("extract synthetic native metadata: %w", scanErr)
				}
				defer native.Release()
				trackID = native.TrackID
				evidence, scanErr = native.Evidence(formats, api.HDRSourceEvidence{
					UniqueHEVC:  true,
					HDR10Plus:   true,
					DolbyVision: true,
				})
				if scanErr != nil {
					return fmt.Errorf("convert synthetic native metadata: %w", scanErr)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer evidence.Release()
			if tc.failed >= 0 {
				evidence.Results[tc.failed] = api.HDRFormatResult{
					Format:    evidence.Results[tc.failed].Format,
					Requested: true,
					State:     api.HDRExtractionFailed,
					Coverage:  api.HDRCoverageUnknown,
					Failure:   &api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceLimit, Message: "Synthetic parser quota exceeded."},
				}
				if tc.failed == 0 {
					evidence.HDR10Plus.Release()
					evidence.HDR10Plus = nil
				} else {
					evidence.DolbyVision = nil
				}
			}
			if tc.absent {
				evidence.DolbyVision = nil
				evidence.Results[1] = api.HDRFormatResult{
					Format:    api.HDRNativeDolbyVision,
					Requested: true,
					State:     api.HDRExtractionAbsent,
					Coverage:  api.HDRCoverageAbsent,
				}
			}
			record, resource, err := builder.retainNativeExtraction(t.Context(), release, hdranalysis.ExtractionIdentity{
				SourceFingerprint: resolver.subject.SourceFingerprint,
				TargetID:          target.ID,
				SelectionPolicy:   target.SelectionPolicy,
				ResolvedTrackID:   trackID,
			}, formats, evidence, "policy-checkpoint")
			if err != nil {
				t.Fatal(err)
			}
			if tc.legacy {
				file := resource.Files["metadata"]
				encoded, readErr := os.ReadFile(file.Path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var manifest map[string]any
				if err := json.Unmarshal(encoded, &manifest); err != nil {
					t.Fatal(err)
				}
				if tc.policy == "" {
					delete(manifest, "resourcePolicy")
				} else {
					manifest["resourcePolicy"] = tc.policy
				}
				encoded, err = json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file.Path, encoded, 0o600); err != nil {
					t.Fatal(err)
				}
				file, err = hdrFileIntegrity(file.Path)
				if err != nil {
					t.Fatal(err)
				}
				resource.Files["metadata"] = file
				record.Size, record.SHA256 = file.Size, file.SHA256
			}
			entries := map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{record.ID: record}
			resources := map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource{record.ID: resource}
			request := api.HDRAnalysisInstructions{
				Release:        release,
				TargetIDs:      []string{target.ID},
				ProfileVersion: api.HDRAnalysisProfileV2,
				Formats:        formats,
			}
			var store *releaseworkflow.HDRMeasurementStore
			if tc.audio {
				request.Decoded = &api.HDRDecodedOptions{Audio: true}
				if audioReport == nil || audioReport.Result.State != api.HDRDecodedComplete {
					t.Fatal("policy fixture lacks completed primary audio")
				}
				identity, err := hdrMeasurementIdentity(record, *request.Decoded, api.HDRDecodedAudio)
				if err != nil {
					t.Fatal(err)
				}
				measurement, retained, err := builder.retainMeasurement(t.Context(), record, identity, "policy-audio", audioReport)
				if err != nil {
					t.Fatal(err)
				}
				store = &releaseworkflow.HDRMeasurementStore{
					Records:   map[string]releaseworkflow.HDRMeasurementRecord{measurement.ID: measurement},
					Resources: map[string]releaseworkflow.RetainedHDRMeasurementResource{measurement.ID: retained},
				}
				store.Publish = func(next releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
					store.Records[next.ID], store.Resources[next.ID] = next, resource
					return nil
				}
				if len(automaticHDRCaptureModes(t.Context(), request, record, store)) > 0 {
					t.Fatal("completed audio checkpoint does not reproduce cached-run reuse")
				}
			}
			published := 0
			publish := func(next releaseworkflow.HDRExtractionRecord, owned releaseworkflow.RetainedHDRExtractionResource) error {
				entries[next.ID], resources[next.ID] = next, owned
				published++
				return nil
			}
			if tc.captured {
				selected := &resolver.subject.Targets[0]
				selected.DiscRoot = filepath.Join(t.TempDir(), "BDMV")
				selected.CapturedNativePath = resource.Files["metadata"].Path
				selected.CapturedNativeSize = record.Size
				selected.CapturedNativeSHA256 = record.SHA256
				selected.CapturedTrackID = record.ResolvedTrackID
				clear(entries)
				clear(resources)
			}
			if tc.failed < 0 || !tc.legacy || tc.missingSource {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			}
			reading := false
			ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) {
				reading = reading || progress.Phase == "reading_metadata" || progress.Phase == "reading_shared_media"
			})
			result, artifacts, err := builder.Build(ctx, releaseworkflow.HDRPreparation{Release: release}, request, "policy-first", time.Now().UTC(), nil, nil, entries, resources, publish, store)
			wantPublished := 0
			if tc.captured {
				wantPublished = 1
			}
			if err != nil || reading || published != wantPublished {
				t.Fatalf("retained attempt err=%v reading=%v published=%d", err, reading, published)
			}
			if tc.failed >= 0 {
				want := api.HDRAnalysisFailureResourceLimit
				if tc.legacy {
					want = api.HDRAnalysisFailureResourceUnavailable
				}
				got := result.Targets[0].Formats[tc.failed].Failure
				if got == nil || got.Code != want {
					t.Fatalf("failure=%+v, want %s", got, want)
				}
				if tc.audio {
					if len(result.Targets[0].Decoded) != 1 || result.Targets[0].Decoded[0].State != api.HDRDecodedComplete || len(result.Targets[0].Artifacts) != 4 {
						t.Fatalf("completed audio was lost during old-quota recovery: %+v", result)
					}
				} else if result.Targets[0].Formats[1-tc.failed].State != api.HDRExtractionComplete || len(result.Targets[0].Artifacts) != 2 {
					t.Fatalf("valid legacy sibling was lost: %+v", result)
				}
				if tc.captured {
					selection := api.HDRAnalysisFormatsHDR10Plus
					if tc.failed == 0 {
						selection = api.HDRAnalysisFormatsDolbyVision
					}
					projected, projectErr := builder.readCapturedNative(t.Context(), resolver.subject.Targets[0], hdranalysis.ExtractionIdentity{
						SourceFingerprint: resolver.subject.SourceFingerprint,
						TargetID:          target.ID,
						SelectionPolicy:   target.SelectionPolicy,
					}, selection, 0)
					if projectErr != nil {
						t.Fatal(projectErr)
					}
					defer projected.Release()
					if projected.Results[tc.failed].State != api.HDRExtractionNotAnalyzed || projected.Results[1-tc.failed].State != api.HDRExtractionComplete {
						t.Fatalf("successful capture projection lost: %+v", projected.Results)
					}
				}
				if tc.missingSource {
					return
				}
				reading = false
				retry, _, retryErr := builder.Build(ctx, releaseworkflow.HDRPreparation{Release: release}, request, "policy-retry", time.Now().UTC(), &result, artifacts, entries, resources, publish, store)
				if retryErr != nil {
					t.Fatal(retryErr)
				}
				if tc.legacy {
					wantArtifacts := 3
					wantStatus := api.StageStatusCompleted
					if tc.audio {
						wantArtifacts = 4
						// This audio fixture carries HDR10 pixels without dynamic metadata.
						wantStatus = api.StageStatusPartial
					}
					if retry.Status != wantStatus || published != 1 || len(retry.Targets[0].Artifacts) != wantArtifacts || !reading {
						t.Fatalf("source retry=%+v published=%d", retry, published)
					}
					if tc.audio && (retry.Targets[0].Formats[1].Failure != nil || retry.Targets[0].Formats[1].State != api.HDRExtractionAbsent ||
						len(retry.Targets[0].Decoded) != 1 || retry.Targets[0].Decoded[0].State != api.HDRDecodedComplete || len(store.Records) != 2) {
						t.Fatalf("explicit retry did not replace the obsolete DV failure and refresh audio: %+v", retry)
					}
				} else if retry.Targets[0].Formats[tc.failed].Failure == nil || retry.Targets[0].Formats[tc.failed].Failure.Code != want || published != 0 {
					t.Fatalf("current-policy failure did not remain retained: %+v", retry)
				}
				return
			}
			if result.Targets[0].Formats[0].State != api.HDRExtractionComplete {
				t.Fatalf("legacy HDR lost: %+v", result)
			}
			if tc.absent {
				if result.Targets[0].Formats[1].State != api.HDRExtractionAbsent {
					t.Fatalf("legacy absence lost: %+v", result)
				}
				return
			}
			if result.Status != api.StageStatusCompleted || len(result.Targets[0].Artifacts) != 3 {
				t.Fatalf("legacy complete restore=%+v", result)
			}
			result.Targets[0].Formats[0].Checks = append(result.Targets[0].Formats[0].Checks, api.HDRCheckResult{
				Code:    "plot_unavailable",
				State:   api.HDRCheckUnsupported,
				Message: "Synthetic previous render limit.",
			})
			result.Targets[0].Status = result.Targets[0].AnalysisStatus()
			result.Status = api.StageStatusPartial
			rendered := false
			ctx = hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) { rendered = rendered || progress.Phase == "rendering" })
			retry, _, err := builder.Build(ctx, releaseworkflow.HDRPreparation{Release: release}, request, "policy-render", time.Now().UTC(), &result, artifacts, entries, resources, publish, nil)
			if err != nil || !rendered || retry.Status != api.StageStatusCompleted || len(retry.Targets[0].Artifacts) != 3 || published != 0 {
				t.Fatalf("source-less plot retry=%+v err=%v rendered=%v published=%d", retry, err, rendered, published)
			}
		})
	}
}
