// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdrSourceChangedDuringCapture struct {
	*hdrResolverFixture
	checks int
}

func (r *hdrSourceChangedDuringCapture) ValidateHDRAnalysisSource(ctx context.Context, release api.ReleaseRef) error {
	r.checks++
	if r.checks > 1 {
		return api.ErrActiveInputChanged
	}
	return r.hdrResolverFixture.ValidateHDRAnalysisSource(ctx, release)
}

func TestAutomaticHDRWorkflowRecapturesSelectedModes(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	for _, name := range []string{"decoder_change", "metadata_audio_decoder_change", "v3_decoder_change", "v3_decoder_change_audio_publish_failure", "audio_retry", "sampled_audio_retry", "audio_retry_source_missing", "audio_retry_source_changed", "picture_retry"} {
		t.Run(name, func(t *testing.T) {
			builder, resolver, _ := hdrFixture(t)
			executable, err := screenshots.ResolveFFmpegExecutable()
			if err != nil {
				t.Fatal(err)
			}
			audioRetry := name == "audio_retry" || name == "sampled_audio_retry" || name == "audio_retry_source_missing" || name == "audio_retry_source_changed"
			audio := "sine=frequency=440:sample_rate=48000:duration=4.8"
			if audioRetry {
				audio = "aevalsrc=0|0|0|0|0|0|0|0|0:s=48000:d=4.8"
			}
			source := filepath.Join(t.TempDir(), "Synthetic.Recovery.mkv")
			command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error",
				"-i", filepath.Join("..", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"),
				"-f", "lavfi", "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "pcm_s16le", source)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("author recovery source: %v %s", err, output)
			}
			target := &resolver.subject.Targets[0]
			target.Path, target.DiscRoot = source, ""
			target.Target.Evidence, err = hdranalysis.InspectFileSource(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			options := api.HDRDecodedOptions{Automatic: true, Sampled: name == "sampled_audio_retry"}
			if name == "metadata_audio_decoder_change" {
				options = api.HDRDecodedOptions{Audio: true}
			}
			request := api.HDRAnalysisInstructions{
				Release:        resolver.subject.Release,
				TargetIDs:      []string{target.Target.ID},
				ProfileVersion: api.HDRAnalysisProfileV2,
				Decoded:        &options,
			}
			if name == "v3_decoder_change" || name == "v3_decoder_change_audio_publish_failure" {
				request.ProfileVersion = api.HDRAnalysisProfileV3
				target.Audio = &api.AudioAnalysisSubject{
					VideoPath:           source,
					SourceFingerprint:   resolver.subject.SourceFingerprint,
					ResourceID:          "resource",
					ManifestFingerprint: "manifest",
					PrimaryTrackID:      "main",
					Tracks: []api.MediaTrackFacts{{
						ID:            "main",
						ResourceID:    "resource",
						Kind:          api.MediaTrackAudio,
						Ordinal:       1,
						Codec:         "pcm",
						Channels:      1,
						SampleRate:    48000,
						ChannelLayout: "mono",
					}},
				}
			}
			entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
			resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
			publish := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
				file, err := resource.OpenExtraction(t.Context(), record)
				if err != nil {
					return fmt.Errorf("open native checkpoint: %w", err)
				}
				if err := file.Close(); err != nil {
					return fmt.Errorf("close native checkpoint: %w", err)
				}
				entries[record.ID], resources[record.ID] = record, resource
				return nil
			}
			store := releaseworkflow.HDRMeasurementStore{
				Records:   make(map[string]releaseworkflow.HDRMeasurementRecord),
				Resources: make(map[string]releaseworkflow.RetainedHDRMeasurementResource),
			}
			var sequence uint64
			var published []api.HDRDecodedMode
			failAudioPublication := false
			store.Publish = func(record releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
				if failAudioPublication && record.Result.Mode == api.HDRDecodedAudio {
					return errors.New("synthetic audio publication failure")
				}
				if _, err := restoreHDRMeasurement(t.Context(), record, resource); err != nil {
					return err
				}
				sequence++
				record.Sequence = sequence
				store.Records[record.ID], store.Resources[record.ID] = record, resource
				published = append(published, record.Result.Mode)
				return nil
			}
			build := func(ctx context.Context, attempt string, prior *api.HDRAnalysisResult) api.HDRAnalysisResult {
				t.Helper()
				result, _, err := builder.Build(ctx, releaseworkflow.HDRPreparation{Release: request.Release}, request,
					attempt, time.Now(), prior, nil, entries, resources, publish, &store)
				if err != nil || len(result.Targets) != 1 {
					t.Fatalf("%s: %+v %v", attempt, result, err)
				}
				return result
			}
			first := build(t.Context(), "first", nil)
			for _, result := range first.Targets[0].Decoded {
				want := api.HDRDecodedComplete
				if audioRetry && result.Mode == api.HDRDecodedAudio {
					want = api.HDRDecodedUnsupported
				}
				if result.State != want {
					t.Fatalf("initial %s: %+v", result.Mode, result)
				}
			}
			var wantModes []api.HDRDecodedMode
			switch {
			case audioRetry:
				request.DecodedRetry = &api.HDRDecodedRetry{AnalysisAttemptID: first.AttemptID, Modes: []api.HDRDecodedMode{api.HDRDecodedAudio}}
				wantModes = []api.HDRDecodedMode{api.HDRDecodedAudio}
				if name == "audio_retry_source_missing" {
					if err := os.Remove(source); err != nil {
						t.Fatal(err)
					}
					wantModes = nil
				}
				if name == "audio_retry_source_changed" {
					builder.resolver = &hdrSourceChangedDuringCapture{hdrResolverFixture: resolver}
					wantModes = nil
				}
			case name == "picture_retry":
				for id, record := range store.Records {
					if record.Result.Mode == api.HDRDecodedAutomatic {
						delete(store.Resources, id)
					}
				}
				first = build(t.Context(), "missing-picture", &first)
				request.DecodedRetry = &api.HDRDecodedRetry{AnalysisAttemptID: first.AttemptID, Modes: []api.HDRDecodedMode{api.HDRDecodedAutomatic}}
				wantModes = []api.HDRDecodedMode{api.HDRDecodedAutomatic}
			default:
				// Model a decoder replacement with valid retained reports carrying
				// the former build identity; all other checkpoint authority stays intact.
				for _, record := range slices.Collect(maps.Values(store.Records)) {
					report, err := restoreHDRMeasurement(t.Context(), record, store.Resources[record.ID])
					if err != nil {
						t.Fatal(err)
					}
					report.Decoder.Fingerprint = "former-decoder-build"
					identity := record.Identity
					if report.AudioAuthority != nil {
						identity, err = hdranalysis.AudioMeasurementIdentity(report)
						if err != nil {
							t.Fatal(err)
						}
					}
					updated, resource, err := builder.retainMeasurement(t.Context(), entries[record.ExtractionID], identity, "old-"+record.ID, report)
					if err != nil {
						t.Fatal(err)
					}
					updated.Sequence = record.Sequence
					delete(store.Records, record.ID)
					delete(store.Resources, record.ID)
					store.Records[updated.ID], store.Resources[updated.ID] = updated, resource
				}
				wantModes = slices.Clone(published)
				if name == "v3_decoder_change_audio_publish_failure" {
					failAudioPublication = true
					wantModes = []api.HDRDecodedMode{api.HDRDecodedAutomatic}
				}
			}
			before := sequence
			if request.DecodedRetry != nil {
				store.RetryAfterSequence = new(before)
			}
			published = nil
			var measuredPictures atomic.Bool
			ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) {
				if progress.Phase == "measuring_pictures" {
					measuredPictures.Store(true)
				}
			})
			second := build(ctx, "recovered", &first)
			if !slices.Equal(published, wantModes) || len(entries) != 1 {
				t.Fatalf("capture replaced sibling authority: published=%v want=%v native=%d", published, wantModes, len(entries))
			}
			if audioRetry && measuredPictures.Load() {
				t.Fatal("audio-only retry decoded pictures")
			}
			for _, result := range second.Targets[0].Decoded {
				want := api.HDRDecodedComplete
				if audioRetry && result.Mode == api.HDRDecodedAudio {
					want = api.HDRDecodedUnsupported
				}
				if name == "v3_decoder_change_audio_publish_failure" && result.Mode == api.HDRDecodedAudio {
					want = api.HDRDecodedFailed
				}
				if result.State != want || want == api.HDRDecodedComplete && len(result.Artifacts) == 0 {
					t.Fatalf("recovered %s: %+v", result.Mode, result)
				}
			}
			if len(second.Targets[0].Decoded) != len(first.Targets[0].Decoded) {
				t.Fatal("recovery omitted or duplicated a requested mode")
			}
			if name != "audio_retry_source_missing" {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			}
			published = nil
			// A resumed retry must not recapture its newly checkpointed mode.
			replayed := build(t.Context(), "replayed", &first)
			if len(published) != 0 || len(replayed.Targets[0].Decoded) != len(second.Targets[0].Decoded) {
				t.Fatalf("source-free recovery recaptured or omitted modes: %v %+v", published, replayed.Targets[0])
			}
			for _, result := range replayed.Targets[0].Decoded {
				index := slices.IndexFunc(second.Targets[0].Decoded, func(previous api.HDRDecodedResult) bool { return previous.Mode == result.Mode })
				if index < 0 || result.State != second.Targets[0].Decoded[index].State || result.Reason != second.Targets[0].Decoded[index].Reason {
					t.Fatalf("source-free recovery lost mode %s: %+v", result.Mode, result)
				}
			}
		})
	}
}
