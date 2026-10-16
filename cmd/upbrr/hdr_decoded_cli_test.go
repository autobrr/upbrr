// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"

	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRStandaloneMissingComparisonPreservesPrimaryMeasurements(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual calibrated decoder")
	}
	source := filepath.Join("..", "..", "internal", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv")
	identity, err := verifyStandaloneHDRInput(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	err = hdranalysis.New(hdranalysis.NewAdmission(), nil).WithSession(t.Context(), func(session *hdranalysis.Session) error {
		seed, err := session.ExtractNativeFile(t.Context(), filepath.Join(filepath.Dir(source), "single-hybrid.hevc"), api.HDRAnalysisFormatsBoth)
		if err != nil {
			return fmt.Errorf("extract authored metadata: %w", err)
		}
		defer seed.Release()
		evidence, err := seed.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return fmt.Errorf("prepare authored evidence: %w", err)
		}
		defer evidence.Release()
		base := evidence.DolbyVision.Records[0]
		evidence.HDR10Plus.Release()
		evidence.HDR10Plus = nil
		a := &dovi.Analysis{
			SchemaVersion: 1,
			Timeline:      dovi.PresentationFrames,
			Complete:      true,
			TrackID:       1,
			ObservedRPUs:  120,
			Records:       make([]dovi.RPU, 120),
			Pictures:      make([]dovi.Picture, 120),
		}
		for i := range a.Pictures {
			record := base
			record.DM = new(*base.DM)
			record.DM.SceneRefresh = 0
			if slices.Contains([]int{12, 36, 60, 84, 108}, i) {
				record.DM.SceneRefresh = 1
			}
			record.DM.L5 = &dovi.L5{Top: 12, Bottom: 12}
			record.DM.Blocks = append(slices.Clone(base.DM.Blocks), dovi.Block{
				Level:  5,
				Length: 7,
				Values: []int64{0, 0, 12, 12},
			})
			a.Records[i] = record
			a.Pictures[i] = dovi.Picture{
				Stream:        dovi.StreamKey{TrackID: 1},
				AUOrdinal:     uint64(i),
				DecodeOrdinal: uint64(i),
				POC:           int64(i),
				POCKnown:      true,
				PTS: dovi.Timestamp{
					Value:     int64(i * 40),
					Timescale: 1000,
					Valid:     true,
				},
				HasRPU:   true,
				RPUIndex: uint32(i),
			}
		}
		if err := a.Validate(dovi.AnalysisOptions{}); err != nil {
			return fmt.Errorf("validate authored metadata: %w", err)
		}
		evidence.DolbyVision = a
		output := t.TempDir()
		target := api.HDRAnalysisTargetResult{}
		reports, measurementErr := standaloneHDRMeasurements(t.Context(), session, cliOptions{
			HDRSceneSync:     true,
			HDRActiveArea:    true,
			HDRGrade:         true,
			HDRColour:        true,
			HDRCompareSource: filepath.Join(t.TempDir(), "Unavailable.Comparison.mkv"),
			HDRCompareOffset: "0",
			HDRCompareCrop:   "0,12,192,88",
		}, source, output, identity.SourceVersion, evidence, &target)
		if measurementErr == nil {
			t.Fatal("missing comparison did not preserve command failure")
		}
		if len(reports) != 2 || len(target.Decoded) != 4 {
			t.Fatalf("independent modes suppressed: reports=%d results=%+v error=%v", len(reports), target.Decoded, measurementErr)
		}
		for _, result := range target.Decoded {
			//nolint:exhaustive // This fixture exercises the legacy decoded modes.
			switch result.Mode {
			case api.HDRDecodedAutomatic, api.HDRDecodedAudio:
				t.Fatal("manual measurement unexpectedly used automatic mode")
			case api.HDRDecodedSceneSync, api.HDRDecodedActiveArea:
				if result.State != api.HDRDecodedComplete || result.Measured == 0 {
					t.Fatalf("primary check failed: %+v", result)
				}
				for _, extension := range []string{".json", ".png"} {
					if _, err := os.Stat(filepath.Join(output, string(result.Mode)+extension)); err != nil {
						return fmt.Errorf("inspect primary measurement artifact: %w", err)
					}
				}
			case api.HDRDecodedGrade, api.HDRDecodedColour:
				if result.State != api.HDRDecodedFailed || result.Reason != "comparison_source_unavailable" {
					t.Fatalf("comparison failure lost: %+v", result)
				}
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ctx = hdranalysis.WithProgress(ctx, func(hdranalysis.Progress) { cancel() })
		canceled := api.HDRAnalysisTargetResult{}
		reports, measurementErr = standaloneHDRMeasurements(ctx, session, cliOptions{
			HDRSceneSync:     true,
			HDRActiveArea:    true,
			HDRGrade:         true,
			HDRColour:        true,
			HDRCompareSource: source,
			HDRCompareOffset: "0",
			HDRCompareCrop:   "0,12,192,88",
		}, source, t.TempDir(), identity.SourceVersion, evidence, &canceled)
		if !errors.Is(measurementErr, context.Canceled) || len(reports) != 0 || len(canceled.Decoded) != 4 {
			t.Fatalf("comparison preparation cancellation lost: reports=%d results=%+v error=%v", len(reports), canceled.Decoded, measurementErr)
		}
		for _, result := range canceled.Decoded {
			if result.State != api.HDRDecodedCanceled {
				t.Fatalf("cancellation became comparison failure: %+v", result)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHDRDecodedCLIRequiresExplicitMappingAndSeparatesSelection(t *testing.T) {
	options := cliOptions{
		HDRAnalysis:      true,
		HDRCompareSource: "Synthetic.Comparison.mkv",
		HDRCompareOffset: "0",
		HDRCompareCrop:   "0,0,192,112",
		HDRGrade:         true,
	}
	for _, change := range []func(*cliOptions){
		func(o *cliOptions) { o.HDRCompareOffset = "" },
		func(o *cliOptions) { o.HDRCompareSource = "" },
		func(o *cliOptions) { o.HDRCompareCrop = "0,0,193,112" },
		func(o *cliOptions) { o.HDRGrade = false },
	} {
		invalid := options
		change(&invalid)
		if _, err := cliHDRDecoded(invalid, ""); err == nil {
			t.Fatalf("invalid mapping accepted: %+v", invalid)
		}
	}
	request := cliHDRUploadRequest(options)
	selection := cliHDRComparisonSelection(options)
	if err := selection.Validate(request); err != nil {
		t.Fatal(err)
	}
	if request.Decoded.Comparison.SourceID != "" || *request.Decoded.Comparison.FrameOffset != 0 {
		t.Fatal("CLI fabricated comparison authority or lost explicit zero")
	}
	if _, err := request.Normalize(); err == nil {
		t.Fatal("pending selection accepted as prepared source authority")
	}
	for _, flag := range []string{"hdr-retry-modes", "hdr-retry-attempt", "hdr-image-ids"} {
		if err := validateCLIHDR(cliOptions{HDRAnalysisOnly: true, HDROutputDir: "output"}, map[string]bool{flag: true}); err == nil {
			t.Fatalf("standalone accepted workflow-only %s", flag)
		}
	}
}

func TestHDRStandaloneDecodedUsesActualProbeAndRetainsReports(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual calibrated decoder")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	sources := t.TempDir()
	a, b := filepath.Join(sources, "Synthetic.Primary.mkv"), filepath.Join(sources, "Synthetic.Comparison.mkv")
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := t.TempDir()
	var stdout, stderr bytes.Buffer
	err = runHDRAnalysisOnly(t.Context(), cliOptions{
		HDRAnalysisOnly:  true,
		HDROutputDir:     output,
		HDRCompareSource: b,
		HDRCompareOffset: "0",
		HDRCompareCrop:   "0,12,192,88",
		HDRGrade:         true,
		HDRColour:        true,
	},
		[]string{a}, cliIO{out: &stdout, errOut: &stderr})
	// This analytic fixture has no dynamic metadata, so native discovery remains
	// incomplete while its successfully measured comparison outputs are retained.
	entries, readErr := os.ReadDir(output)
	if readErr != nil || len(entries) != 1 {
		t.Fatalf("missing standalone comparison output: %v %v %s", err, readErr, stderr.String())
	}
	for _, mode := range []string{"grade", "colour"} {
		directory := filepath.Join(output, entries[0].Name())
		payload, readErr := os.ReadFile(filepath.Join(directory, mode+".json"))
		if readErr != nil {
			t.Fatalf("missing measurement: %v analysis=%v logs=%s", readErr, err, stderr.String())
		}
		var report struct {
			Result api.HDRDecodedResult `json:"result"`
		}
		if err := json.Unmarshal(payload, &report); err != nil {
			t.Fatal(err)
		}
		if report.Result.State != api.HDRDecodedComplete || report.Result.Measured != 120 {
			t.Fatalf("real probe failed measurement: %+v analysis=%v logs=%s", report.Result, err, stderr.String())
		}
		if bytes.Contains(payload, []byte("Synthetic.Primary")) || bytes.Contains(payload, []byte("Synthetic.Comparison")) {
			t.Fatal("shareable report exposed source path")
		}
		if info, err := os.Stat(filepath.Join(directory, mode+".png")); err != nil || info.Size() == 0 {
			t.Fatalf("missing rendered comparison: %v", err)
		}
	}
	automaticOutput := t.TempDir()
	_ = runHDRAnalysisOnly(
		t.Context(),
		cliOptions{
			HDRAnalysisOnly: true,
			HDRPictures:     true,
			HDROutputDir:    automaticOutput,
		},
		[]string{a},
		cliIO{out: &stdout, errOut: &stderr},
	)
	automaticEntries, err := os.ReadDir(automaticOutput)
	if err != nil || len(automaticEntries) != 1 {
		t.Fatalf("automatic output: %v", err)
	}
	directory := filepath.Join(automaticOutput, automaticEntries[0].Name())
	payload, err := os.ReadFile(filepath.Join(directory, "automatic.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := hdranalysis.ReadMeasurement(t.Context(), bytes.NewReader(payload))
	if err != nil || report.Result.State != api.HDRDecodedComplete || report.Result.Measured != 5 || !report.Options.Sampled {
		t.Fatalf("explicit picture checks did not sample pictures: %v", err)
	}
	for _, name := range []string{"picture_brightness_plot.png", "audio.json"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	audioOutput := t.TempDir()
	_ = runHDRAnalysisOnly(t.Context(), cliOptions{HDRAnalysisOnly: true, HDROutputDir: audioOutput}, []string{a}, cliIO{out: &stdout, errOut: &stderr})
	audioEntries, err := os.ReadDir(audioOutput)
	if err != nil || len(audioEntries) != 1 {
		t.Fatalf("default audio output: %v", err)
	}
	audioDirectory := filepath.Join(audioOutput, audioEntries[0].Name())
	if _, err := os.Stat(filepath.Join(audioDirectory, "audio.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(audioDirectory, "automatic.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default unexpectedly measured pictures: %v", err)
	}
}

func TestHDRPictureCoverageCLI(t *testing.T) {
	for _, args := range [][]string{{"--hdr-analysis"}, {"--hdr-analysis-only", "--hdr-output-dir", "output"}} {
		opts, _, _, err := parseCLIOptions(args)
		if err != nil {
			t.Fatal(err)
		}
		options, err := cliHDRDecoded(opts, "")
		if err != nil || options == nil || !options.Audio || options.Automatic || options.Sampled || options.AllAudioTracks {
			t.Fatalf("default must select metadata and primary audio: %+v %v", options, err)
		}
	}
	for _, full := range []bool{false, true} {
		options, err := cliHDRDecoded(cliOptions{
			HDRAnalysis:   true,
			HDRPictures:   !full,
			HDRFullFrames: full,
		}, "")
		if err != nil || options == nil || !options.Automatic || options.Sampled == full {
			t.Fatalf("full=%v options=%+v err=%v", full, options, err)
		}
	}
	audio, err := cliHDRDecoded(cliOptions{HDRAnalysis: true, HDRAudioOnly: true}, "")
	if err != nil || audio == nil || !audio.Audio || audio.Automatic || audio.Sampled {
		t.Fatalf("metadata/audio selection failed: %+v %v", audio, err)
	}
	for _, invalid := range []cliOptions{
		{
			HDRAnalysis:  true,
			HDRPictures:  true,
			HDRAudioOnly: true,
		},
		{
			HDRAnalysis:     true,
			HDRPictures:     true,
			HDRMetadataOnly: true,
		},
		{
			HDRAnalysis:     true,
			HDRPictures:     true,
			HDRBoundarySync: true,
		},
		{
			HDRAnalysis:   true,
			HDRAudioOnly:  true,
			HDRFullFrames: true,
		},
		{
			HDRAnalysis:     true,
			HDRAudioOnly:    true,
			HDRMetadataOnly: true,
		},
		{
			HDRAnalysis:  true,
			HDRAudioOnly: true,
			HDRSceneSync: true,
		},
		{
			HDRAnalysis:  true,
			HDRAudioOnly: true,
			HDRGrade:     true,
		},
	} {
		if _, err := cliHDRDecoded(invalid, ""); err == nil {
			t.Fatal("audio-only accepted conflicting picture/metadata-only options")
		}
	}
	for _, args := range [][]string{{"--hdr-analysis", "--hdr-pictures"}, {"--hdr-analysis-only", "--hdr-output-dir", "output", "--hdr-pictures"}} {
		opts, visited, _, err := parseCLIOptions(args)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateCLIHDR(opts, visited); err != nil {
			t.Fatal(err)
		}
		decoded, err := cliHDRDecoded(opts, "")
		if err != nil || decoded == nil || !decoded.Automatic || !decoded.Sampled {
			t.Fatalf("explicit picture selection: %+v %v", decoded, err)
		}
	}
	if err := validateCLIHDR(cliOptions{HDRPictures: true}, map[string]bool{"hdr-pictures": true}); err == nil {
		t.Fatal("picture checks accepted without HDR analysis")
	}
	if _, err := cliHDRDecoded(cliOptions{
		HDRAnalysis:     true,
		HDRMetadataOnly: true,
		HDRFullFrames:   true,
	}, ""); err == nil {
		t.Fatal("metadata-only accepted full-frame measurement")
	}
}

func TestHDRBoundaryCLIOptionsAndExplicitAudioFlag(t *testing.T) {
	for _, audio := range []bool{true, false} {
		args := []string{"--hdr-analysis", "--hdr-boundary-sync", "--hdr-active-area", "--hdr-decoder-threads=4"}
		if audio {
			args = append(args, "--hdr-all-audio-tracks")
		} else {
			args = append(args, "--hdr-boundary-audio=false")
		}
		opts, visited, _, err := parseCLIOptions(args)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateCLIHDR(opts, visited); err != nil {
			t.Fatal(err)
		}
		decoded, err := cliHDRDecoded(opts, "")
		if err != nil || decoded == nil || !decoded.BoundarySync || decoded.Audio != audio || decoded.AllAudioTracks != audio ||
			!decoded.ActiveArea || decoded.Automatic || decoded.Sampled || decoded.VideoDecoderThreads != 4 {
			t.Fatalf("boundary CLI options=%+v err=%v", decoded, err)
		}
	}
	for _, args := range [][]string{
		{"--hdr-analysis", "--hdr-boundary-audio=false"},
		{"--hdr-analysis", "--hdr-boundary-sync", "--hdr-audio-only"},
		{"--hdr-analysis", "--hdr-boundary-sync", "--hdr-metadata-only"},
		{"--hdr-analysis", "--hdr-boundary-sync", "--hdr-full-frames"},
		{"--hdr-analysis", "--hdr-boundary-sync", "--hdr-scene-sync"},
		{"--hdr-analysis", "--hdr-boundary-sync", "--hdr-boundary-audio=false", "--hdr-all-audio-tracks"},
		{"--hdr-analysis", "--hdr-audio-only", "--hdr-decoder-threads=4"},
		{"--hdr-analysis", "--hdr-metadata-only", "--hdr-all-audio-tracks"},
		{"--hdr-analysis", "--hdr-boundary-sync", "--hdr-decoder-threads=65"},
	} {
		opts, visited, _, err := parseCLIOptions(args)
		if err == nil {
			err = validateCLIHDR(opts, visited)
		}
		if err == nil {
			t.Fatalf("accepted conflicting flags: %v", args)
		}
	}
}

func TestHDRStandaloneMetadataAudioOnly(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "Synthetic.HDR.Audio.mkv")
	fixture := filepath.Join("..", "..", "internal", "services", "hdranalysis", "testdata", "decoded-pq-cuts.mkv")
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-i", fixture,
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:duration=4.8", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "pcm_s16le", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("author HDR/audio fixture: %v %s", err, output)
	}
	output := filepath.Join(directory, "output")
	result := executeCLIForTest(t.Context(), t, []string{"--hdr-analysis-only", "--hdr-audio-only", "--hdr-output-dir", output, path})
	// This HDR10 fixture has no dynamic metadata. Its native absence keeps
	// the command partial while the requested audio must remain complete.
	if result.code != 0 && result.code != 1 {
		t.Fatalf("metadata/audio-only command failed: %s", result.stderr)
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 1 {
		t.Fatalf("standalone output: %v", err)
	}
	root := filepath.Join(output, entries[0].Name())
	data, err := os.ReadFile(filepath.Join(root, "audio.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := hdranalysis.ReadMeasurement(t.Context(), bytes.NewReader(data))
	if err != nil || report.Audio == nil || report.Result.State != api.HDRDecodedComplete || report.Audio.Track.SampleFrames != 230400 {
		t.Fatalf("complete audio was not retained: %v", err)
	}
	for _, name := range []string{"automatic.json", "picture_brightness_plot.png"} {
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("audio-only retained picture output %s: %v", name, err)
		}
	}
}
