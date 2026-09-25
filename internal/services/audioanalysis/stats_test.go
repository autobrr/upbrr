// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSoxSampleFromFloat32TruncatesAndClips(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		input float32
		want  int32
	}{
		{
			name:  "negative full scale",
			input: -1,
			want:  math.MinInt32,
		},
		{
			name:  "negative half scale",
			input: -0.5,
			want:  -1 << 30,
		},
		{
			name:  "negative fractional signed32 LSB",
			input: float32(-1.75 / soxSampleScale),
			want:  -1,
		},
		{
			name:  "silence",
			input: 0,
			want:  0,
		},
		{
			name:  "positive fractional signed32 LSB",
			input: float32(1.75 / soxSampleScale),
			want:  1,
		},
		{
			name:  "positive half scale",
			input: 0.5,
			want:  1 << 30,
		},
		{
			name:  "positive full scale",
			input: 1,
			want:  math.MaxInt32,
		},
		{
			name:  "clipped positive",
			input: 1.5,
			want:  math.MaxInt32,
		},
		{
			name:  "clipped negative",
			input: -1.5,
			want:  math.MinInt32,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := soxSampleFromFloat32(test.input); got != test.want {
				t.Fatalf("quantized sample = %d, want %d", got, test.want)
			}
		})
	}
}

func TestStatsAnalysisStereoReportGolden(t *testing.T) {
	analysis := newStatsAnalysis(2, 20)
	for _, frame := range [][]float32{
		{-0.5, 0.25},
		{-0.5, 0.25},
		{0.5, 0.25},
		{0.5, 0.25},
	} {
		analysis.add(frame)
	}
	got := analysis.report()
	const want = "             Overall     Left      Right\n" +
		"DC offset   0.250000  0.000000  0.250000\n" +
		"Min level  -0.500000 -0.500000  0.250000\n" +
		"Max level   0.500000  0.500000  0.250000\n" +
		"Pk lev dB      -6.02     -6.02    -12.04\n" +
		"RMS lev dB     -8.06     -6.02    -12.04\n" +
		"RMS Pk dB      -6.02     -6.02    -12.04\n" +
		"RMS Tr dB     -12.04     -6.02    -12.04\n" +
		"Crest factor       -      1.00      1.00\n" +
		"Flat factor    10.46      6.02     12.04\n" +
		"Pk count           6         4         8\n" +
		"Bit-depth       2/3       1/2       1/3 \n" +
		"Num samples        4\n" +
		"Length s       0.200\n" +
		"Scale max   1.000000\n" +
		"Window s       0.050\n"
	if got != want {
		t.Fatalf("statistics report mismatch:\n%q", got)
	}
}

func TestStatsAnalysisUsesFullRMSAtWarmupBoundary(t *testing.T) {
	analysis := newStatsAnalysis(1, 20)
	for range 5 { // five samples is this rate's 250 ms warm-up boundary.
		analysis.add([]float32{0.5})
	}
	report := analysis.report()
	for _, line := range []string{
		"RMS lev dB     -6.02",
		"RMS Pk dB      -6.02",
		"RMS Tr dB      -6.02",
	} {
		if !strings.Contains(report, line) {
			t.Fatalf("report lacks %q:\n%s", line, report)
		}
	}
}

func TestStatsAnalysisPreservesSilentRunsAndLowAmplitudeBitDepth(t *testing.T) {
	silence := newStatsAnalysis(1, 20)
	for range 4 {
		silence.add([]float32{0})
	}
	if report := silence.report(); !strings.Contains(report, "Flat factor    12.04") || !strings.Contains(report, "Pk count           8") {
		t.Fatalf("silent report =\n%s", report)
	}

	quiet := newStatsAnalysis(1, 20)
	quiet.add([]float32{-1.0 / 32768})
	quiet.add([]float32{1.0 / 32768})
	if report := quiet.report(); !strings.Contains(report, "Bit-depth       1/16") {
		t.Fatalf("low-amplitude report =\n%s", report)
	}
}

func TestStatsVariantIsAllocatedOnlyWhenRequested(t *testing.T) {
	binding := trackBinding{track: audioTrackForStatsTest()}
	if analysis := newTrackAnalysis(binding, []api.AudioAnalysisVariant{api.AudioAnalysisWaveform}); analysis.stats != nil {
		t.Fatal("unrequested statistics accumulator was created")
	}
	if analysis := newTrackAnalysis(binding, []api.AudioAnalysisVariant{api.AudioAnalysisStats}); analysis.stats == nil {
		t.Fatal("requested statistics accumulator was not created")
	}
}

func audioTrackForStatsTest() api.MediaTrackFacts {
	return api.MediaTrackFacts{
		ID:         "stats-track",
		Kind:       api.MediaTrackAudio,
		Ordinal:    1,
		Channels:   1,
		SampleRate: 48_000,
	}
}

func TestStatsAnalysisMatchesSoxWhenConfigured(t *testing.T) {
	soxPath := strings.TrimSpace(os.Getenv("UPBRR_AUDIO_STATS_SOX_PATH"))
	if soxPath == "" {
		t.Skip("set UPBRR_AUDIO_STATS_SOX_PATH to compare native statistics against SoX")
	}
	const sampleRate = 48_000
	silentFrames := make([][]float32, 257)
	for frame := range silentFrames {
		silentFrames[frame] = []float32{0}
	}
	generated := []struct {
		name     string
		channels int
		pcm      []byte
	}{
		{
			name:     "short stereo extrema",
			channels: 2,
			pcm: encodePCM([][]float32{
				{-0.5, 0.25}, {-0.5, 0.25}, {0.5, 0.25}, {0.5, 0.25},
			}),
		},
		{
			name:     "silence",
			channels: 1,
			pcm:      encodePCM(silentFrames),
		},
		{
			name:     "varied extrema",
			channels: 3,
			pcm: encodePCM([][]float32{
				{-1, 0.25, -0.125}, {-1, 0.25, -0.125}, {0.5, -0.5, 0.125}, {0.5, -0.5, 0.125},
			}),
		},
		{
			name:     "tone after rms warmup",
			channels: 2,
			pcm:      encodePCM(statsToneFrames(14_400)),
		},
		{
			name:     "randomized low-level per-channel PCM",
			channels: 3,
			pcm:      encodePCM(statsLowLevelFrames(14_400)),
		},
	}
	for _, test := range generated {
		t.Run(test.name, func(t *testing.T) {
			assertStatsMatchSox(t, soxPath, test.pcm, sampleRate, test.channels)
		})
	}

	fixtureDirectory := strings.TrimSpace(os.Getenv("UPBRR_AUDIO_STATS_FIXTURE_DIR"))
	if fixtureDirectory == "" {
		t.Log("UPBRR_AUDIO_STATS_FIXTURE_DIR is unset; benchmark fixture parity was not requested")
		return
	}
	for _, fixture := range []string{"fidelity-dc-6ch.wav", "fidelity-nyquist-6ch.wav"} {
		t.Run(fixture, func(t *testing.T) {
			pcm := decodeFixturePCM(t, filepath.Join(fixtureDirectory, fixture))
			assertStatsMatchSox(t, soxPath, pcm, sampleRate, 6)
		})
	}
	if representative := strings.TrimSpace(os.Getenv("UPBRR_AUDIO_STATS_REPRESENTATIVE_FIXTURE")); representative != "" {
		t.Run("representative decoded stream", func(t *testing.T) {
			assertStatsMatchSox(t, soxPath, decodeFixturePCM(t, representative), sampleRate, 6)
		})
	}
}

func statsToneFrames(frameCount int) [][]float32 {
	frames := make([][]float32, frameCount)
	for frame := range frames {
		frames[frame] = []float32{
			float32(0.75 * math.Sin(2*math.Pi*float64(frame)/97)),
			float32(0.25 * math.Cos(2*math.Pi*float64(frame)/43)),
		}
	}
	return frames
}

func statsLowLevelFrames(frameCount int) [][]float32 {
	frames := make([][]float32, frameCount)
	state := uint32(0x9e3779b9)
	for frame := range frames {
		frames[frame] = make([]float32, 3)
		for channel := range frames[frame] {
			state = state*1664525 + 1013904223
			unit := float64(state>>8)/float64(1<<24) - .5
			frames[frame][channel] = float32(unit * .0037 * float64(channel+1))
		}
	}
	return frames
}

func decodeFixturePCM(t *testing.T, fixture string) []byte {
	t.Helper()
	ffmpegPath := strings.TrimSpace(os.Getenv("UPBRR_AUDIO_STATS_FFMPEG_PATH"))
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	cmd := exec.CommandContext(t.Context(), ffmpegPath,
		"-hide_banner", "-nostdin", "-v", "error", "-threads", "2", "-i", fixture,
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1",
	)
	pcm, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode benchmark fixture %q: %v", filepath.Base(fixture), err)
	}
	return pcm
}

func assertStatsMatchSox(
	t *testing.T,
	soxPath string,
	pcm []byte,
	sampleRate int,
	channels int,
) {
	t.Helper()
	analysis := newStatsAnalysis(channels, sampleRate)
	if _, err := consumePCM(bytes.NewReader(pcm), channels, func(_ int64, samples []float32) error {
		analysis.add(samples)
		return nil
	}); err != nil {
		t.Fatalf("consume synthetic PCM: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), soxPath,
		"-V0", "-D", "-t", "f32", "-L", "-r", strconv.Itoa(sampleRate), "-c", strconv.Itoa(channels), "-", "-n", "stats",
	)
	cmd.Stdin = bytes.NewReader(pcm)
	cmd.Stdout = io.Discard
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		t.Fatalf("run SoX statistics reference: %v", err)
	}
	got := analysis.report()
	want := normalizeSoxStatsForComparison(diagnostics.String())
	if got != want {
		t.Fatalf("native report differs from SoX after line-ending normalization:\n%s", statsReportDiff(got, want))
	}
}

func normalizeSoxStatsForComparison(report string) string {
	report = strings.ReplaceAll(report, "\r\n", "\n")
	// SoX 14.4.2 built with the Windows C runtime renders negative infinity as
	// -1.#J. Native reports use the portable mathematical spelling -inf.
	return strings.ReplaceAll(report, "-1.#J", " -inf")
}

func statsReportDiff(got string, want string) string {
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	lineCount := max(len(gotLines), len(wantLines))
	for line := range lineCount {
		var gotLine string
		if line < len(gotLines) {
			gotLine = gotLines[line]
		}
		var wantLine string
		if line < len(wantLines) {
			wantLine = wantLines[line]
		}
		if gotLine != wantLine {
			return fmt.Sprintf("line %d\nnative: %q\nSoX:    %q", line+1, gotLine, wantLine)
		}
	}
	return "reports have different byte lengths"
}
