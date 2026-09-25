// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAudioAnalysisOnlyRequiresOneInputAndOutput(t *testing.T) {
	input := filepath.Join(t.TempDir(), "Synthetic.Audio.2026.wav")
	if err := os.WriteFile(input, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--audio-analysis-only", input}, "requires --audio-output"},
		{[]string{"--audio-analysis-only", "--audio-output", t.TempDir()}, "requires exactly one media file"},
		{[]string{"--audio-analysis-only", "--audio-output", t.TempDir(), input, input}, "requires exactly one media file"},
		{[]string{"--audio-analysis-only", "--audio-output", t.TempDir(), "--config", "missing.yaml", input}, "--config cannot be used"},
		{[]string{"--audio-output", t.TempDir(), input}, "--audio-output requires --audio-analysis-only"},
	} {
		result := executeCLIForTest(t.Context(), t, test.args)
		if result.code != 2 || !strings.Contains(result.stderr, test.want) {
			t.Fatalf("args=%v code=%d stderr=%q", test.args, result.code, result.stderr)
		}
	}
}

func TestAudioAnalysisOnlyWritesUserSelectedOutputWithoutConfig(t *testing.T) {
	ffmpeg, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Skipf("FFmpeg unavailable: %v", err)
	}
	input := filepath.Join(t.TempDir(), "Synthetic.Audio.2026.wav")
	command := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
		"sine=frequency=440:duration=0.1", "-c:a", "pcm_s16le", input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate test audio: %v: %s", err, output)
	}
	output := filepath.Join(t.TempDir(), "analysis")
	result := executeCLIForTest(t.Context(), t, []string{
		"--audio-analysis-only", "--audio-output", output, "--audio-images", "waveform", input,
	})
	if result.code != 0 || result.err != nil || !strings.Contains(result.stdout, "Audio track 1 waveform:") ||
		!strings.Contains(result.stdout, "Audio track 1 stats:") ||
		!strings.Contains(result.stderr, "Audio analysis 10% | T1 10%") ||
		!strings.Contains(result.stderr, "Audio analysis complete 100% | T1 100%") {
		t.Fatalf("result: code=%d err=%v stdout=%q stderr=%q", result.code, result.err, result.stdout, result.stderr)
	}
	for _, name := range []string{"waveform.png", "stats.txt"} {
		matches, err := filepath.Glob(filepath.Join(output, "analysis-*", "track_1", name))
		if err != nil || len(matches) != 1 {
			t.Fatalf("%s: matches=%v err=%v", name, matches, err)
		}
		if info, err := os.Stat(matches[0]); err != nil || info.Size() == 0 {
			t.Fatalf("%s: info=%v err=%v", name, info, err)
		}
	}
	repeated := executeCLIForTest(t.Context(), t, []string{
		"--audio-analysis-only", "--audio-output", output, "--audio-images", "waveform", input,
	})
	if repeated.code != 0 || repeated.err != nil {
		t.Fatalf("repeat: code=%d err=%v stderr=%q", repeated.code, repeated.err, repeated.stderr)
	}
	matches, err := filepath.Glob(filepath.Join(output, "analysis-*", "track_1", "waveform.png"))
	if err != nil || len(matches) != 2 {
		t.Fatalf("repeat output: matches=%v err=%v", matches, err)
	}
}

func TestCLIAudioAnalysisProgressAggregatesTracks(t *testing.T) {
	var output bytes.Buffer
	progress := newCLIAudioProgress(&output)
	ctx := api.WithWorkflowProgressReporter(t.Context(), progress.update)
	emit := func(phase, track string, percent int) {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     phase,
			ItemID:    track,
			Label:     "Audio track " + strings.TrimPrefix(track, "audio-"),
			Status:    api.StageStatusRunning,
			Completed: percent,
			Total:     100,
		})
	}
	emit("other", "audio-1", 10)
	for _, track := range []string{"audio-1", "audio-2", "audio-3"} {
		emit("audio_analysis_decode", track, 0)
	}
	emit("audio_analysis_decode", "audio-1", 10)
	emit("audio_analysis_decode", "audio-1", 10)
	emit("audio_analysis_decode", "audio-2", 10)
	emit("audio_analysis_decode", "audio-3", 10)
	emit("audio_analysis_decode", "audio-1", 35)
	emit("audio_analysis_decode", "audio-2", 30)
	emit("audio_analysis_decode", "audio-1", 5)
	progress.finish([]audioanalysis.TrackResult{
		{Public: api.AudioAnalysisTrackResult{TrackID: "audio-1", Status: api.StageStatusCompleted}},
		{Public: api.AudioAnalysisTrackResult{TrackID: "audio-2", Status: api.StageStatusCompleted}},
		{Public: api.AudioAnalysisTrackResult{TrackID: "audio-3", Status: api.StageStatusCompleted}},
	}, true)
	want := "Audio analysis 3% | T1 10% | T2 0% | T3 0%\n" +
		"Audio analysis 10% | T1 10% | T2 10% | T3 10%\n" +
		"Audio analysis 25% | T1 35% | T2 30% | T3 10%\n" +
		"Audio analysis 15% | T1 5% | T2 30% | T3 10%\n" +
		"Audio analysis complete 100% | T1 100% | T2 100% | T3 100%\n"
	if output.String() != want {
		t.Fatalf("progress = %q, want %q", output.String(), want)
	}
}

func TestCLIAudioAnalysisProgressRedrawsOneTerminalLine(t *testing.T) {
	var output bytes.Buffer
	progress := newCLIAudioProgress(&output)
	progress.terminal = true
	ctx := api.WithWorkflowProgressReporter(t.Context(), progress.update)
	for _, track := range []string{"audio-1", "audio-2"} {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:  "audio_analysis_decode",
			ItemID: track,
			Status: api.StageStatusRunning,
			Total:  100,
		})
	}
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		Phase:     "audio_analysis_decode",
		ItemID:    "audio-1",
		Status:    api.StageStatusRunning,
		Completed: 50,
		Total:     100,
	})
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		Phase:     "audio_analysis_decode",
		ItemID:    "audio-2",
		Status:    api.StageStatusRunning,
		Completed: 50,
		Total:     100,
	})
	progress.finish([]audioanalysis.TrackResult{
		{Public: api.AudioAnalysisTrackResult{TrackID: "audio-1", Status: api.StageStatusCompleted}},
		{Public: api.AudioAnalysisTrackResult{TrackID: "audio-2", Status: api.StageStatusCompleted}},
	}, true)
	if strings.Count(output.String(), "\n") != 1 || strings.Count(output.String(), "\r") != 3 ||
		!strings.Contains(output.String(), "Audio analysis 25% | T1 50% | T2 0%") ||
		!strings.Contains(output.String(), "Audio analysis complete 100% | T1 100% | T2 100%") {
		t.Fatalf("terminal progress = %q", output.String())
	}
}

func TestCLIAudioAnalysisProgressFitsNineTracksInTerminal(t *testing.T) {
	var output bytes.Buffer
	progress := newCLIAudioProgress(&output)
	progress.terminal = true
	progress.width = 80
	ctx := api.WithWorkflowProgressReporter(t.Context(), progress.update)
	results := make([]audioanalysis.TrackResult, 0, 9)
	for ordinal := 1; ordinal <= 9; ordinal++ {
		trackID := fmt.Sprintf("audio-%d", ordinal)
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:  "audio_analysis_decode",
			ItemID: trackID,
			Status: api.StageStatusRunning,
			Total:  100,
		})
		results = append(results, audioanalysis.TrackResult{
			Public: api.AudioAnalysisTrackResult{TrackID: trackID, Status: api.StageStatusCompleted},
		})
	}
	for ordinal := 1; ordinal <= 9; ordinal++ {
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     "audio_analysis_decode",
			ItemID:    fmt.Sprintf("audio-%d", ordinal),
			Status:    api.StageStatusRunning,
			Completed: 50,
			Total:     100,
		})
	}
	progress.finish(results, true)
	for line := range strings.SplitSeq(output.String(), "\r") {
		if len(strings.TrimSuffix(line, "\n")) >= 80 {
			t.Fatalf("terminal line wraps at 80 columns: %q", line)
		}
	}
	if !strings.Contains(output.String(), "Done 100% | 1:100% 2:100% 3:100% 4:100% 5:100% 6:100% 7:100% 8:100% 9:100%") {
		t.Fatalf("final terminal progress omits a track: %q", output.String())
	}
}

func TestCLIAudioAnalysisProgressFitsResizedTerminal(t *testing.T) {
	var output bytes.Buffer
	progress := newCLIAudioProgress(&output)
	progress.terminal = true
	progress.width = 80
	for ordinal := 1; ordinal <= 3; ordinal++ {
		progress.update(api.WorkflowProgressUpdate{
			Phase:  "audio_analysis_decode",
			ItemID: fmt.Sprintf("audio-%d", ordinal),
			Status: api.StageStatusRunning,
			Total:  100,
		})
	}
	progress.update(api.WorkflowProgressUpdate{
		Phase:     "audio_analysis_decode",
		ItemID:    "audio-1",
		Status:    api.StageStatusRunning,
		Completed: 50,
		Total:     100,
	})
	progress.width = 30
	progress.update(api.WorkflowProgressUpdate{
		Phase:     "audio_analysis_decode",
		ItemID:    "audio-2",
		Status:    api.StageStatusRunning,
		Completed: 50,
		Total:     100,
	})
	progress.finish(nil, false)
	lines := strings.Split(output.String(), "\r")
	if len(lines) != 4 {
		t.Fatalf("terminal updates = %q", output.String())
	}
	for _, line := range lines[2:] {
		if len(strings.TrimSuffix(line, "\n")) >= 30 {
			t.Fatalf("resized terminal line wraps: %q", line)
		}
	}
}

func TestAudioAnalysisOnlyDefaultsToMainAudioTrack(t *testing.T) {
	ffmpeg, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Skipf("FFmpeg unavailable: %v", err)
	}
	input := filepath.Join(t.TempDir(), "Synthetic.TwoTracks.2026.mkv")
	command := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=0.1",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=0.1",
		"-map", "0:a", "-map", "1:a", "-c:a", "flac",
		"-metadata:s:a:0", "title=Commentary", "-metadata:s:a:1", "title=Main", input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate test audio: %v: %s", err, output)
	}
	result := executeCLIForTest(t.Context(), t, []string{
		"--audio-analysis-only", "--audio-output", t.TempDir(), "--audio-images", "waveform", input,
	})
	if result.code != 0 || !strings.Contains(result.stdout, "Audio track 2 waveform:") ||
		strings.Contains(result.stdout, "Audio track 1 waveform:") {
		t.Fatalf("result: code=%d err=%v stdout=%q stderr=%q", result.code, result.err, result.stdout, result.stderr)
	}
	all := executeCLIForTest(t.Context(), t, []string{
		"--audio-analysis-only", "--audio-tracks", "all", "--audio-output", t.TempDir(), "--audio-images", "waveform", input,
	})
	if all.code != 0 || !strings.Contains(all.stderr, " | T1 ") || !strings.Contains(all.stderr, " | T2 ") ||
		!strings.Contains(all.stdout, "Audio track 1 waveform:") || !strings.Contains(all.stdout, "Audio track 2 waveform:") {
		t.Fatalf("all tracks: code=%d err=%v stdout=%q stderr=%q", all.code, all.err, all.stdout, all.stderr)
	}
}

func TestReportAudioAnalysisResultsIncludesVariantFailure(t *testing.T) {
	failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureOutput, Message: "could not publish analysis image"}
	var output bytes.Buffer
	err := reportAudioAnalysisResults([]audioanalysis.TrackResult{{
		Public: api.AudioAnalysisTrackResult{
			Ordinal: 1,
			Status:  api.StageStatusPartial,
			Artifacts: []api.AudioAnalysisArtifact{
				{Variant: api.AudioAnalysisWaveform, Status: api.StageStatusCompleted},
				{
					Variant: api.AudioAnalysisSpectrogram,
					Status:  api.StageStatusFailed,
					Failure: &failure,
				},
			},
		},
		Artifacts: []audioanalysis.Artifact{{
			Public: api.AudioAnalysisArtifact{Variant: api.AudioAnalysisWaveform, Status: api.StageStatusCompleted},
			Path:   "waveform.png",
		}},
	}}, &output)
	if err == nil || !strings.Contains(err.Error(), "track 1 spectrogram: could not publish analysis image") ||
		!strings.Contains(output.String(), "Audio track 1 waveform: waveform.png") {
		t.Fatalf("error=%v output=%q", err, output.String())
	}
}
