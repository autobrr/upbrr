// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRStandalonePreparedBindingPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	complete, err := standalonePreparedHDR(ctx, nil, cliOptions{}, filepath.Join("testdata", "synthetic-cli-hdr.mkv"),
		"prior-fingerprint", t.TempDir(), api.HDRAnalysisFormatsAuto, api.HDRPeakHistogram, api.HDRDecodedOptions{})
	if complete || !errors.Is(err, context.Canceled) || errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("canceled binding lost its cause: complete=%v err=%v", complete, err)
	}
	complete, err = standalonePreparedHDR(t.Context(), nil, cliOptions{}, filepath.Join("testdata", "synthetic-cli-hdr.mkv"),
		"prior-fingerprint", t.TempDir(), api.HDRAnalysisFormatsAuto, api.HDRPeakHistogram, api.HDRDecodedOptions{})
	if complete || !errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("changed binding was not rejected: complete=%v err=%v", complete, err)
	}
}

func TestHDRStandalonePreparedAllAudioAndBoundary(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "Synthetic.Two.Tracks.mkv")
	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel",
		"error",
		"-i",
		filepath.Join("..", "..", "internal", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"),
		"-f",
		"lavfi",
		"-i",
		"sine=frequency=440:sample_rate=48000:duration=4.8",
		"-map",
		"0:v:0",
		"-map",
		"1:a:0",
		"-map",
		"1:a:0",
		"-c:v",
		"copy",
		"-c:a",
		"pcm_s16le",
		source,
	}
	if output, err := exec.CommandContext(t.Context(), executable, args...).CombinedOutput(); err != nil {
		t.Fatalf("author standalone source: %v %s", err, output)
	}
	for _, mode := range []string{"--hdr-audio-only", "--hdr-boundary-sync"} {
		t.Run(mode, func(t *testing.T) {
			output := t.TempDir()
			result := executeCLIForTest(t.Context(), t, []string{"--hdr-analysis-only", mode, "--hdr-all-audio-tracks", "--hdr-output-dir", output, source})
			if result.code != 0 && result.code != 1 {
				t.Fatalf("standalone command: %s", result.stderr)
			}
			entries, err := os.ReadDir(output)
			if err != nil || len(entries) != 1 {
				t.Fatalf("standalone output: %v %s", err, result.stderr)
			}
			root := filepath.Join(output, entries[0].Name())
			for _, filename := range []string{"audio-track-1.json", "audio-track-2.json"} {
				data, err := os.ReadFile(filepath.Join(root, filename))
				if err != nil {
					t.Fatalf("track report: %v %s", err, result.stderr)
				}
				report, err := hdranalysis.ReadMeasurement(t.Context(), bytes.NewReader(data))
				if err != nil || report.AudioAuthority == nil || report.Result.Measured != 1 || report.Audio == nil ||
					report.Audio.Track.SampleFrames != 230400 {
					t.Fatalf("prepared track evidence missing: %v", err)
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "summary.json"))
			if err != nil {
				t.Fatal(err)
			}
			var summary struct {
				Profile string                 `json:"profile"`
				Decoded []api.HDRDecodedResult `json:"decoded"`
			}
			if err := json.Unmarshal(data, &summary); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, decoded := range summary.Decoded {
				if decoded.Mode == api.HDRDecodedAudio {
					count++
					if decoded.Selected != 2 || decoded.Measured != 2 || len(decoded.AudioTracks) != 2 || len(decoded.Artifacts) != 6 ||
						decoded.Validate() != nil {
						t.Fatalf("standalone aggregate: %+v", decoded)
					}
				}
			}
			if count != 1 || summary.Profile != api.HDRAnalysisProfileV3 || bytes.Contains(data, []byte(`"audioAuthority"`)) {
				t.Fatal("summary duplicated audio bodies or lost v3 aggregate")
			}
		})
	}
}
