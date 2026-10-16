// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Audionut/go-hdr10-plus/extract"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMetadataAudioWithoutPictureMapping(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	for _, otherBytes := range []int64{0, 1536 << 20, MaxRestoreBytes} {
		t.Run(fmt.Sprintf("retained_%d_mib", otherBytes>>20), func(t *testing.T) {
			executable, err := screenshots.ResolveFFmpegExecutable()
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			path := filepath.Join(directory, "Synthetic.Metadata.Audio.mkv")
			video := automaticHybridInput(t)
			command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
				"-i", video.Path, "-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:duration=4.8",
				"-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "pcm_s16le", path)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("author metadata/audio fixture: %v %s", err, output)
			}
			facts, err := InspectFileSource(t.Context(), path)
			if err != nil || facts.Decoded == nil {
				t.Fatalf("inspect authored fixture: %v", err)
			}
			// Missing colour/geometry mapping prevents picture analysis but must not
			// prevent compressed metadata and complete primary audio processing.
			facts.Decoded.Width, facts.Decoded.Height, facts.Decoded.BitDepth, facts.Decoded.Transfer = 0, 0, 0, ""
			var measuredPicture atomic.Bool
			var metadataProgress atomic.Bool
			ctx := WithProgress(t.Context(), func(progress Progress) {
				if progress.Phase == "measuring_pictures" {
					measuredPicture.Store(true)
				}
				if progress.Phase == "reading_shared_media" && progress.Message != "Reading HDR metadata and primary audio" {
					metadataProgress.Store(true)
				}
			})
			err = New(NewAdmission(), nil).WithSession(ctx, func(session *Session) error {
				native, reports, err := session.AnalyzeAutomatic(ctx, DecodedInput{
					Path:        path,
					Fingerprint: "synthetic-metadata-audio",
					Source:      *facts.Decoded,
				},
					nil, api.HDRDecodedOptions{Audio: true}, api.HDRAnalysisFormatsBoth, directory, otherBytes, true)
				if otherBytes == MaxRestoreBytes {
					if !errors.Is(err, extract.ErrResourceLimit) {
						t.Fatalf("exhausted operation budget was not rejected: %v", err)
					}
					return nil
				}
				if err != nil {
					return err
				}
				defer native.Release()
				if !native.ScanComplete || native.DolbyVision == nil || native.HDR10Plus == nil ||
					len(native.DolbyVision.Pictures) != 120 || len(native.HDR10Plus.Frames) != 120 || len(reports) != 1 || measuredPicture.Load() || !metadataProgress.Load() {
					t.Fatal("metadata/audio-only did not preserve complete metadata independently of video decoding")
				}
				report := reports[0]
				if report.Result.Mode != api.HDRDecodedAudio || report.Result.State != api.HDRDecodedComplete || report.Automatic != nil ||
					report.Audio == nil || report.Audio.Track.SampleFrames != 230400 || len(report.Audio.Images) != 2 || report.Audio.Stats == "" {
					t.Fatalf("audio-only outcome: %+v", report.Result)
				}
				evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, facts)
				if err != nil {
					return err
				}
				defer evidence.Release()
				if evidence.DolbyVision == nil || evidence.Results[1].State != api.HDRExtractionComplete {
					t.Fatalf("audio scratch prevented Dolby Vision conversion: %+v", evidence.Results)
				}
				manifest, _, err := PublishNativeEvidence(ctx, t.TempDir(), testIdentity(), api.HDRAnalysisFormatsBoth, evidence)
				if err != nil || manifest.DolbyVision == nil || manifest.HDR10Plus == nil {
					t.Fatalf("native evidence was not saved: %+v %v", manifest, err)
				}
				if err := session.RenderNative(ctx, evidence, api.HDRNativeDolbyVision, api.HDRPeakHistogram,
					"Synthetic metadata and audio", filepath.Join(directory, "dolby-vision.png"), 0); err != nil {
					return err
				}
				var retained bytes.Buffer
				if err := WriteMeasurement(ctx, &retained, report); err != nil {
					return err
				}
				if err := os.Remove(path); err != nil {
					return fmt.Errorf("remove metadata/audio source: %w", err)
				}
				restored, err := ReadMeasurement(ctx, &retained)
				if err != nil {
					return err
				}
				for _, artifact := range MeasurementArtifacts(restored) {
					if err := session.RenderMeasurementArtifact(ctx, restored, artifact.Kind, "Synthetic metadata and audio", &bytes.Buffer{}); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
