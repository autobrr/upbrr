// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestParseInspectedStreamsPreservesFFmpegAudioOrderAndFacts(t *testing.T) {
	streams, err := parseInspectedStreams(`
  Stream #0:0: Video: h264, yuv420p, 1920x1080
  Stream #0:1[0x1100](eng): Audio: eac3, 48000 Hz, 5.1(side), fltp, 640 kb/s
  Stream #0:2(jpn): Audio: flac (24 bit), 96000 Hz, stereo, s32 (24 bit)
At least one output file must be specified
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 2 || streams[0].codec != "eac3" ||
		streams[0].sampleRate != 48_000 || streams[0].channels != 6 ||
		streams[1].sampleRate != 96_000 || streams[1].channels != 2 {
		t.Fatalf("streams = %#v", streams)
	}
}

func TestFFmpegInspectClassifiesNoAudioOnlyAfterCompletedProbe(t *testing.T) {
	decoder := helperProcessInspectDecoder("no-audio")
	_, err := decoder.Inspect(t.Context(), "synthetic.mkv")
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureNoAudio {
		t.Fatalf("inspect error = %v, failure = %#v", err, failure)
	}
}

func TestFFmpegInspectClassifiesNoAudioWhenCompletionExceedsDiagnosticLimit(t *testing.T) {
	decoder := helperProcessInspectDecoder("no-audio-long")
	_, err := decoder.Inspect(t.Context(), "synthetic.mkv")
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureNoAudio {
		t.Fatalf("inspect error = %v, failure = %#v", err, failure)
	}
}

func TestFFmpegInspectPreservesAudioStreamBeyondDiagnosticLimit(t *testing.T) {
	decoder := helperProcessInspectDecoder("audio-after-long-diagnostics")
	streams, err := decoder.Inspect(t.Context(), "synthetic.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].codec != "flac" || streams[0].sampleRate != 48_000 || streams[0].channels != 2 {
		t.Fatalf("streams = %#v", streams)
	}
}

func TestFFmpegInspectPreservesAudioWhenInputPathContainsStreamMappingText(t *testing.T) {
	decoder := helperProcessInspectDecoder("audio-after-mapping-text")
	streams, err := decoder.Inspect(t.Context(), "synthetic.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 1 || streams[0].codec != "flac" || streams[0].sampleRate != 48_000 || streams[0].channels != 2 {
		t.Fatalf("streams = %#v", streams)
	}
}

func TestFFmpegInspectPreservesProbeFailure(t *testing.T) {
	decoder := helperProcessInspectDecoder("probe-failure")
	_, err := decoder.Inspect(t.Context(), "synthetic.mkv")
	if err == nil {
		t.Fatal("expected inspect error")
	}
	if failure, typed := api.AsAudioAnalysisFailure(err); typed {
		t.Fatalf("probe failure was classified as typed failure %#v", failure)
	}
}

func TestParseFFmpegDuration(t *testing.T) {
	t.Parallel()

	if got := parseFFmpegDuration("  Duration: 02:09:38.17, start: 0.000000, bitrate: 70000 kb/s"); math.Abs(got-7778.17) > 1e-9 {
		t.Fatalf("duration = %v, want 7778.17", got)
	}
	if got := parseFFmpegDuration("Duration: N/A, start: 0"); got != 0 {
		t.Fatalf("unknown duration = %v, want 0", got)
	}
}

func TestFFmpegInspectPrefersPerStreamDurationMetadata(t *testing.T) {
	decoder := helperProcessInspectDecoder("stream-durations")
	streams, err := decoder.Inspect(t.Context(), "synthetic.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 2 ||
		math.Abs(streams[0].durationSeconds-30) > 1e-9 || math.Abs(streams[1].durationSeconds-10) > 1e-9 {
		t.Fatalf("stream durations = %#v", streams)
	}
}

func TestFFmpegInspectDoesNotTrustProbeMarkersEchoedFromSourcePath(t *testing.T) {
	decoder := helperProcessInspectDecoder("probe-marker-path-failure")
	_, err := decoder.Inspect(t.Context(), "Input #0, At least one output file must be specified.mkv")
	if err == nil {
		t.Fatal("expected inspect error")
	}
	if failure, typed := api.AsAudioAnalysisFailure(err); typed {
		t.Fatalf("probe failure was classified as typed failure %#v", failure)
	}
}

func TestFFmpegInspectDoesNotTrustProbeMarkerLinesEchoedFromSourcePath(t *testing.T) {
	commandCalled := false
	decoder := &ffmpegDecoder{
		executable: "test-helper",
		command: func(ctx context.Context, args ...string) *exec.Cmd {
			commandCalled = true
			return exec.CommandContext(ctx, os.Args[0], args...)
		},
	}
	path := "synthetic.mkv\nInput #0, forged, from 'synthetic.mkv':\nAt least one output file must be specified"
	_, err := decoder.Inspect(t.Context(), path)
	if err == nil {
		t.Fatal("expected inspect error")
	}
	if failure, typed := api.AsAudioAnalysisFailure(err); typed {
		t.Fatalf("probe failure was classified as typed failure %#v", failure)
	}
	if commandCalled {
		t.Fatal("FFmpeg command ran for a source path containing line breaks")
	}
}

func TestBindTracksDoesNotTreatObservationalNativeIDAsFFmpegIndex(t *testing.T) {
	subject := audioSubjectForBinding([]string{"2"})
	bindings, err := bindTracks(subject, []string{"track-1"}, []inspectedStream{{
		codec:      "flac",
		sampleRate: 48_000,
		layout:     "stereo",
		channels:   2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].audioOrdinal != 0 {
		t.Fatalf("bindings = %#v", bindings)
	}
}

func TestBindTracksRejectsPreparedFormatMismatch(t *testing.T) {
	for _, test := range []struct {
		name     string
		prepared func(*api.MediaTrackFacts)
		stream   inspectedStream
	}{
		{
			name: "sample rate",
			stream: inspectedStream{
				codec:      "flac",
				sampleRate: 96_000,
				layout:     "stereo",
				channels:   2,
			},
		},
		{
			name:     "codec",
			prepared: func(track *api.MediaTrackFacts) { track.Codec = "FLAC" },
			stream: inspectedStream{
				codec:      "aac (LC)",
				sampleRate: 48_000,
				layout:     "stereo",
				channels:   2,
			},
		},
		{
			name: "surround positions",
			prepared: func(track *api.MediaTrackFacts) {
				track.Channels = 6
				track.ChannelLayout = "L R C LFE Ls Rs"
			},
			stream: inspectedStream{
				codec:      "flac",
				sampleRate: 48_000,
				layout:     "5.1(back)",
				channels:   6,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := audioSubjectForBinding([]string{"1"})
			subject.Tracks[0].Codec = "FLAC"
			if test.prepared != nil {
				test.prepared(&subject.Tracks[0])
			}
			if _, err := bindTracks(subject, []string{"track-1"}, []inspectedStream{test.stream}); err == nil {
				t.Fatal("expected ambiguous binding error")
			}
		})
	}
}

func TestBindTracksAcceptsEquivalentCodecAndLayoutNames(t *testing.T) {
	for _, test := range []struct {
		name      string
		prepared  string
		inspected string
	}{
		{
			name:      "MediaInfo DD plus",
			prepared:  "DD+",
			inspected: "eac3",
		},
		{
			name:      "MediaInfo DD",
			prepared:  "DD",
			inspected: "ac3",
		},
		{
			name:      "Dolby name",
			prepared:  "Dolby Digital Plus",
			inspected: "eac3",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := audioSubjectForBinding([]string{"1"})
			subject.Tracks[0].Codec = test.prepared
			subject.Tracks[0].Channels = 6
			subject.Tracks[0].ChannelLayout = "L R C LFE Ls Rs"
			bindings, err := bindTracks(subject, []string{"track-1"}, []inspectedStream{{
				codec:      test.inspected,
				sampleRate: 48_000,
				layout:     "5.1(side)",
				channels:   6,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if len(bindings) != 1 || bindings[0].audioOrdinal != 0 {
				t.Fatalf("bindings = %#v", bindings)
			}
		})
	}
}

func TestBindTracksRejectsUnknownPreparedCodec(t *testing.T) {
	subject := audioSubjectForBinding([]string{"1"})
	subject.Tracks[0].Codec = "unknown prepared codec"
	_, err := bindTracks(subject, []string{"track-1"}, []inspectedStream{{
		codec:      "flac",
		sampleRate: 48_000,
		layout:     "stereo",
		channels:   2,
	}})
	if err == nil {
		t.Fatal("expected unknown prepared codec to fail closed")
	}
}

func TestBindTracksAcceptsOrderedMatchingPreparedInventory(t *testing.T) {
	subject := audioSubjectForBinding([]string{"1", "2"})
	bindings, err := bindTracks(subject, []string{"track-2"}, []inspectedStream{
		{
			codec:      "flac",
			sampleRate: 48_000,
			layout:     "stereo",
			channels:   2,
		},
		{
			codec:      "aac",
			sampleRate: 48_000,
			layout:     "mono",
			channels:   1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].track.ID != "track-2" || bindings[0].audioOrdinal != 1 {
		t.Fatalf("bindings = %#v", bindings)
	}
}

func TestBindTracksAppliesLayoutLimitOnlyToSelectedTracks(t *testing.T) {
	subject := audioSubjectForBinding([]string{"1", "2"})
	subject.Tracks[1].Channels = 12
	subject.Tracks[1].ChannelLayout = "12 channels"
	bindings, err := bindTracks(subject, []string{"track-1"}, []inspectedStream{
		{
			codec:      "flac",
			sampleRate: 48_000,
			layout:     "stereo",
			channels:   2,
		},
		{
			codec:      "flac",
			sampleRate: 48_000,
			layout:     "12 channels",
			channels:   12,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].track.ID != "track-1" || bindings[0].track.Channels != 2 {
		t.Fatalf("bindings = %#v", bindings)
	}
}

func TestFFmpegDecodeArgsStreamFloatPCMToStdoutWithoutTemporaryOutput(t *testing.T) {
	t.Parallel()

	request := decodeRequestForTest()
	request.path = "input.mkv"
	request.streams[0] = decodeStreamRequest{audioOrdinal: 2, codec: "E-AC-3"}
	args := ffmpegDecodeArgs(request, []string{"pipe:1"})
	wantTail := []string{
		"-i", "input.mkv", "-map", "0:a:2", "-vn", "-sn", "-dn", "-c:a", "pcm_f32le", "-f", "f32le",
		"-flush_packets", "0", "pipe:1",
	}
	if !slices.Equal(args[len(args)-len(wantTail):], wantTail) {
		t.Fatalf("decode args = %v", args)
	}
	if !slices.Contains(args, "-nostdin") || !slices.Contains(args, "-drc_scale") {
		t.Fatalf("decode args missing stdin/DRC safety: %v", args)
	}
	if !slices.Contains(args, "-threads") || slices.Contains(args, "-readrate") {
		t.Fatalf("decode args should limit decoder threads without throttling input: %v", args)
	}
	for _, arg := range args {
		if arg == "pcm.wav" || arg == "pcm.raw" {
			t.Fatalf("decode args contain temporary PCM output: %v", args)
		}
	}
}

func TestFFmpegDecodeArgsBufferEveryPCMOutput(t *testing.T) {
	t.Parallel()

	request := decodeRequestForTest()
	request.path = "input.mkv"
	request.streams = append(request.streams, decodeStreamRequest{audioOrdinal: 1, codec: "AAC"})
	args := ffmpegDecodeArgs(request, []string{"pipe:3", "pipe:4"})
	wantTail := []string{
		"-i", "input.mkv",
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-c:a", "pcm_f32le", "-f", "f32le",
		"-flush_packets", "0", "pipe:3",
		"-map", "0:a:1", "-vn", "-sn", "-dn", "-c:a", "pcm_f32le", "-f", "f32le",
		"-flush_packets", "0", "pipe:4",
	}
	if !slices.Equal(args[len(args)-len(wantTail):], wantTail) {
		t.Fatalf("decode args = %v, want tail %v", args, wantTail)
	}
}

func TestFFmpegDecodeArgsDisableDolbyDecoderDRCForPreparedAliases(t *testing.T) {
	t.Parallel()

	for _, codec := range []string{"DD", "DD+", "AC-3", "E-AC-3", "Dolby Digital Plus"} {
		t.Run(codec, func(t *testing.T) {
			request := decodeRequestForTest()
			request.path = "input.mkv"
			request.streams[0].codec = codec
			args := ffmpegDecodeArgs(request, []string{"pipe:1"})
			if !slices.Contains(args, "-drc_scale") {
				t.Fatalf("decode args for %q omit DRC disable: %v", codec, args)
			}
		})
	}
	request := decodeRequestForTest()
	request.path = "input.mkv"
	request.streams[0].codec = "FLAC"
	if args := ffmpegDecodeArgs(request, []string{"pipe:1"}); slices.Contains(args, "-drc_scale") {
		t.Fatalf("FLAC decode args unexpectedly change DRC: %v", args)
	}
}

func TestLimitedDiagnosticBufferRemainsBounded(t *testing.T) {
	t.Parallel()

	var buffer limitedBuffer
	input := make([]byte, diagnosticLimit*2)
	written, err := buffer.Write(input)
	if err != nil || written != len(input) || len(buffer.String()) != diagnosticLimit {
		t.Fatalf("bounded diagnostic write = %d bytes, len=%d, err=%v", written, len(buffer.String()), err)
	}
}

func TestFFmpegDecodeDrainsFloodedStderr(t *testing.T) {
	decoder := helperProcessDecoder("stderr-flood")
	var decoded int64
	err := decoder.Decode(context.Background(), decodeRequestForTest(), []func(io.Reader) error{func(reader io.Reader) error {
		var copyErr error
		decoded, copyErr = io.Copy(io.Discard, reader)
		if copyErr != nil {
			return fmt.Errorf("discard decoded PCM: %w", copyErr)
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if decoded != 16 {
		t.Fatalf("decoded bytes = %d, want 16", decoded)
	}
}

func TestFFmpegDecodeKillsBlockedProducerAfterConsumerFailure(t *testing.T) {
	decoder := helperProcessDecoder("blocked-stdout")
	wantErr := errors.New("consumer stopped")
	started := time.Now()
	err := decoder.Decode(context.Background(), decodeRequestForTest(), []func(io.Reader) error{func(io.Reader) error { return wantErr }})
	if !errors.Is(err, wantErr) {
		t.Fatalf("decode error = %v, want consumer error", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("blocked producer cleanup took %s", elapsed)
	}
}

func TestFFmpegDecodeCancellationStopsProcessAndJoinsPipes(t *testing.T) {
	decoder := helperProcessDecoder("blocked-stdout")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	err := decoder.Decode(ctx, decodeRequestForTest(), []func(io.Reader) error{func(reader io.Reader) error {
		_, copyErr := io.Copy(io.Discard, reader)
		if copyErr != nil {
			return fmt.Errorf("discard decoded PCM: %w", copyErr)
		}
		return nil
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("decode error = %v, want context cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("decoder cancellation took %s", elapsed)
	}
}

func TestFFmpegDecodeRejectsNonzeroExitAfterPCM(t *testing.T) {
	decoder := helperProcessDecoder("pcm-then-fail")
	var decoded int64
	err := decoder.Decode(context.Background(), decodeRequestForTest(), []func(io.Reader) error{func(reader io.Reader) error {
		var copyErr error
		decoded, copyErr = io.Copy(io.Discard, reader)
		if copyErr != nil {
			return fmt.Errorf("discard decoded PCM: %w", copyErr)
		}
		return nil
	}})
	if err == nil || decoded != 4 {
		t.Fatalf("decode bytes = %d, error = %v", decoded, err)
	}
	if failure, typed := api.AsAudioAnalysisFailure(err); typed {
		t.Fatalf("corrupt decode was classified as typed failure %#v", failure)
	}
}

func TestFFmpegDecodeClassifiesUnavailableCodecWithoutExposingDiagnostics(t *testing.T) {
	decoder := helperProcessDecoder("unsupported-codec")
	err := decoder.Decode(context.Background(), decodeRequestForTest(), []func(io.Reader) error{func(reader io.Reader) error {
		_, copyErr := io.Copy(io.Discard, reader)
		if copyErr != nil {
			return fmt.Errorf("discard decoded PCM: %w", copyErr)
		}
		return nil
	}})
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureUnsupportedCodec {
		t.Fatalf("decode error = %v, failure = %#v", err, failure)
	}
	if strings.Contains(err.Error(), "secret-decoder-detail") {
		t.Fatalf("decode error exposed FFmpeg diagnostics: %v", err)
	}
}

func TestServiceClassifiesUnsupportedCodecWhenDecoderWritesNoPCM(t *testing.T) {
	decoder := inspectedHelperDecoder{
		ffmpegDecoder: helperProcessDecoder("unsupported-codec"),
		streams: []inspectedStream{{
			codec:      "aac",
			sampleRate: 48_000,
			layout:     "mono",
			channels:   1,
		}},
	}
	track := api.MediaTrackFacts{
		ID:                  "track-one",
		Kind:                api.MediaTrackAudio,
		ResourceID:          "resource-one",
		ManifestFingerprint: "manifest",
		Ordinal:             1,
		Codec:               "aac",
		ChannelLayout:       "mono",
		Channels:            1,
		SampleRate:          48_000,
	}
	release := api.ReleaseRef{SourcePath: "Synthetic.Release.2026.mkv", Generation: 1}
	results, err := newService(api.NopLogger{}, decoder).Analyze(t.Context(), api.AudioAnalysisSubject{
		Release:             release,
		SourcePath:          release.SourcePath,
		VideoPath:           "synthetic.mkv",
		ResourceID:          track.ResourceID,
		ManifestFingerprint: track.ManifestFingerprint,
		PrimaryTrackID:      track.ID,
		Tracks:              []api.MediaTrackFacts{track},
	}, api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     track.ResourceID,
		Selection:      api.AudioAnalysisSelectionPrimary,
		TrackIDs:       []string{track.ID},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}, "attempt_unsupported_codec", t.TempDir())
	if err != nil || len(results) != 1 || results[0].Public.Status != api.StageStatusFailed ||
		results[0].Public.Failure == nil || results[0].Public.Failure.Code != api.AudioAnalysisFailureUnsupportedCodec {
		t.Fatalf("error = %v, results = %#v", err, results)
	}
}

func TestFFmpegDecodeStreamsMultipleTracksFromOneProcess(t *testing.T) {
	decoder := helperProcessDecoder("multi-output")
	request := decodeRequestForTest()
	request.streams = append(request.streams, decodeStreamRequest{audioOrdinal: 1, codec: "AAC"})
	decoded := make([]int64, 2)
	err := decoder.Decode(t.Context(), request, []func(io.Reader) error{
		func(reader io.Reader) error {
			written, copyErr := io.Copy(io.Discard, reader)
			decoded[0] = written
			if copyErr != nil {
				return fmt.Errorf("copy first PCM stream: %w", copyErr)
			}
			return nil
		},
		func(reader io.Reader) error {
			written, copyErr := io.Copy(io.Discard, reader)
			decoded[1] = written
			if copyErr != nil {
				return fmt.Errorf("copy second PCM stream: %w", copyErr)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(decoded, []int64{4, 4}) {
		t.Fatalf("decoded bytes = %v, want [4 4]", decoded)
	}
}

func TestFFmpegDecodeKeepsHealthySiblingAfterEmptyTrack(t *testing.T) {
	decoder := helperProcessDecoder("multi-output-empty-first")
	request := decodeRequestForTest()
	request.streams = append(request.streams, decodeStreamRequest{audioOrdinal: 1, codec: "AAC"})
	wantErr := errors.New("decoded audio was empty")
	var healthyBytes int64
	err := decoder.Decode(t.Context(), request, []func(io.Reader) error{
		func(reader io.Reader) error {
			decoded, copyErr := io.Copy(io.Discard, reader)
			if copyErr != nil {
				return fmt.Errorf("read first PCM stream: %w", copyErr)
			}
			if decoded == 0 {
				return wantErr
			}
			return nil
		},
		func(reader io.Reader) error {
			var copyErr error
			healthyBytes, copyErr = io.Copy(io.Discard, reader)
			if copyErr != nil {
				return fmt.Errorf("read healthy PCM stream: %w", copyErr)
			}
			return nil
		},
	})
	completed, processFailure := completedDecodeStreams(err, 2)
	if !errors.Is(err, wantErr) || healthyBytes != 4 || !slices.Equal(completed, []bool{false, true}) || processFailure {
		t.Fatalf("decode error = %v, healthy bytes = %d, completed = %v, process failure = %t",
			err, healthyBytes, completed, processFailure)
	}
}

func TestFFmpegDecodeMarksProcessFailureEvenWithConsumerFailure(t *testing.T) {
	decoder := helperProcessDecoder("multi-output-nonzero")
	request := decodeRequestForTest()
	request.streams = append(request.streams, decodeStreamRequest{audioOrdinal: 1, codec: "AAC"})
	wantErr := errors.New("synthetic PCM consumer failure")
	err := decoder.Decode(t.Context(), request, []func(io.Reader) error{
		func(reader io.Reader) error {
			_, _ = io.Copy(io.Discard, reader)
			return wantErr
		},
		func(reader io.Reader) error {
			_, copyErr := io.Copy(io.Discard, reader)
			if copyErr != nil {
				return fmt.Errorf("read sibling PCM: %w", copyErr)
			}
			return nil
		},
	})
	completed, processFailure := completedDecodeStreams(err, 2)
	if !errors.Is(err, wantErr) || !processFailure || !completed[1] {
		t.Fatalf("decode error = %v, completed = %v, process failure = %t", err, completed, processFailure)
	}
}

type inspectedHelperDecoder struct {
	*ffmpegDecoder
	streams []inspectedStream
}

func (d inspectedHelperDecoder) Inspect(context.Context, string) ([]inspectedStream, error) {
	return append([]inspectedStream(nil), d.streams...), nil
}

func TestServiceRetriesTracksAfterSharedDecoderFailure(t *testing.T) {
	for _, test := range []struct {
		name            string
		mode            string
		wantFirstStatus api.StageStatus
	}{
		{
			name:            "unsupported codec",
			mode:            "multi-unsupported-sibling",
			wantFirstStatus: api.StageStatusFailed,
		},
		{
			name:            "loopback failure",
			mode:            "multi-loopback-fail",
			wantFirstStatus: api.StageStatusCompleted,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoder := inspectedHelperDecoder{
				ffmpegDecoder: helperProcessDecoder(test.mode),
				streams: []inspectedStream{
					{
						codec:      "aac",
						sampleRate: 48_000,
						layout:     "mono",
						channels:   1,
					},
					{
						codec:      "aac",
						sampleRate: 48_000,
						layout:     "mono",
						channels:   1,
					},
				},
			}
			release := api.ReleaseRef{SourcePath: "Synthetic.Release.2026.mkv", Generation: 1}
			tracks := make([]api.MediaTrackFacts, 2)
			trackIDs := make([]string, 2)
			for index := range tracks {
				trackIDs[index] = fmt.Sprintf("track-%d", index+1)
				tracks[index] = api.MediaTrackFacts{
					ID:                  trackIDs[index],
					Kind:                api.MediaTrackAudio,
					ResourceID:          "resource-1",
					ManifestFingerprint: "manifest-1",
					Ordinal:             index + 1,
					Codec:               "aac",
					ChannelLayout:       "mono",
					Channels:            1,
					SampleRate:          48_000,
				}
			}
			service := newService(api.NopLogger{}, decoder)
			results, err := service.Analyze(t.Context(), api.AudioAnalysisSubject{
				Release:             release,
				SourcePath:          release.SourcePath,
				VideoPath:           "synthetic.mkv",
				ResourceID:          "resource-1",
				ManifestFingerprint: "manifest-1",
				PrimaryTrackID:      trackIDs[0],
				Tracks:              tracks,
			}, api.AudioAnalysisInstructions{
				Release:        release,
				ResourceID:     "resource-1",
				Selection:      api.AudioAnalysisSelectionAll,
				TrackIDs:       trackIDs,
				Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
				ProfileVersion: api.AudioAnalysisProfileVersion,
			}, "attempt_retry", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 2 || results[0].Public.Status != test.wantFirstStatus ||
				results[1].Public.Status != api.StageStatusCompleted || len(results[1].Artifacts) != 1 {
				t.Fatalf("results = %#v", results)
			}
		})
	}
}

func TestFFmpegDecodeClassifiesUnavailableCodecBeforeLoopbackAcceptFailures(t *testing.T) {
	decoder := helperProcessDecoder("multi-unsupported-codec")
	request := decodeRequestForTest()
	request.streams = append(request.streams, decodeStreamRequest{audioOrdinal: 1, codec: "unsupported"})
	err := decoder.Decode(t.Context(), request, []func(io.Reader) error{
		func(io.Reader) error { return nil },
		func(io.Reader) error { return nil },
	})
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureUnsupportedCodec {
		t.Fatalf("decode error = %v, failure = %#v", err, failure)
	}
}

func helperProcessDecoder(mode string) *ffmpegDecoder {
	return &ffmpegDecoder{
		executable: "test-helper",
		command: func(ctx context.Context, args ...string) *exec.Cmd {
			helperArgs := make([]string, 0, 3+len(args))
			helperArgs = append(helperArgs, "-test.run=TestFFmpegDecodeHelperProcess", "--", mode)
			helperArgs = append(helperArgs, args...)
			return exec.CommandContext(ctx, os.Args[0], helperArgs...)
		},
	}
}

func decodeRequestForTest() decodeRequest {
	return decodeRequest{
		streams: []decodeStreamRequest{{audioOrdinal: 0, codec: "FLAC"}},
		resourceLimits: api.AudioAnalysisResourceLimits{
			DecoderThreads: api.AudioAnalysisDefaultDecoderThreads,
		},
	}
}

func helperProcessInspectDecoder(mode string) *ffmpegDecoder {
	return &ffmpegDecoder{
		executable: "test-helper",
		command: func(ctx context.Context, args ...string) *exec.Cmd {
			helperArgs := make([]string, 0, 3+len(args))
			helperArgs = append(helperArgs, "-test.run=TestFFmpegInspectHelperProcess", "--", mode)
			helperArgs = append(helperArgs, args...)
			return exec.CommandContext(ctx, os.Args[0], helperArgs...)
		},
	}
}

func TestFFmpegInspectHelperProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "no-audio":
		_, _ = io.WriteString(os.Stderr, `Input #0, matroska,webm, from 'synthetic.mkv':
  Stream #0:0: Video: h264, yuv420p, 1920x1080
At least one output file must be specified
`)
		os.Exit(1)
	case "no-audio-long":
		_, _ = fmt.Fprintf(os.Stderr, "Input #0, matroska,webm, from 'synthetic.mkv':\n%s\n%s\n",
			strings.Repeat("metadata", diagnosticLimit), probeCompletedLine)
		os.Exit(1)
	case "audio-after-long-diagnostics":
		_, _ = fmt.Fprintf(os.Stderr, "Input #0, matroska,webm, from 'synthetic.mkv':\n%s\n  Stream #0:1: Audio: flac, 48000 Hz, stereo, s32\n%s\n",
			strings.Repeat("metadata", diagnosticLimit), probeCompletedLine)
		os.Exit(1)
	case "audio-after-mapping-text":
		_, _ = fmt.Fprintf(os.Stderr, "Input #0, matroska,webm, from '/media/Stream mapping:.mkv':\n  Stream #0:1: Audio: flac, 48000 Hz, stereo, s32\n%s\n",
			probeCompletedLine)
		os.Exit(1)
	case "probe-failure":
		_, _ = io.WriteString(os.Stderr, "synthetic.mkv: Invalid data found when processing input")
		os.Exit(1)
	case "probe-marker-path-failure":
		_, _ = fmt.Fprintf(os.Stderr, "Error opening input file %s\nError opening input files: Invalid argument\n", os.Args[len(os.Args)-1])
		os.Exit(1)
	case "stream-durations":
		_, _ = io.WriteString(os.Stderr, `Input #0, matroska,webm, from 'synthetic.mkv':
  Duration: 00:01:00.000, start: 0.000000, bitrate: 1000 kb/s
  Stream #0:0: Audio: flac, 48000 Hz, stereo, s32
    Metadata:
      DURATION        : 00:00:30.000000000
  Stream #0:1: Audio: ac3, 48000 Hz, mono, fltp
    Metadata:
      DURATION-eng    : 00:00:10.000000000
At least one output file must be specified
`)
		os.Exit(1)
	default:
		t.Fatalf("unknown helper mode %q", os.Args[separator+1])
	}
}

func TestFFmpegDecodeHelperProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "stderr-flood":
		_, _ = io.WriteString(os.Stderr, strings.Repeat("diagnostic", diagnosticLimit))
		_, _ = os.Stdout.Write(make([]byte, 16))
		os.Exit(0)
	case "blocked-stdout":
		block := make([]byte, 32<<10)
		for {
			if _, err := os.Stdout.Write(block); err != nil {
				return
			}
		}
	case "pcm-then-fail":
		_, _ = os.Stdout.Write([]byte("pcm!"))
		os.Exit(7)
	case "unsupported-codec":
		_, _ = io.WriteString(os.Stderr, "Decoder (codec secret-decoder-detail) not found for input stream #0:1")
		os.Exit(8)
	case "multi-unsupported-codec":
		_, _ = io.WriteString(os.Stderr, "Decoder (codec secret-decoder-detail) not found for input stream #0:2")
		os.Exit(8)
	case "multi-unsupported-sibling", "multi-loopback-fail":
		args := os.Args[separator+2:]
		for _, argument := range args {
			if isPrivatePCMOutput(argument) {
				if os.Args[separator+1] == "multi-unsupported-sibling" {
					_, _ = io.WriteString(os.Stderr, "Decoder (codec synthetic) not found for input stream #0:1")
					os.Exit(8)
				}
				os.Exit(9)
			}
		}
		if os.Args[separator+1] == "multi-unsupported-sibling" && slices.Contains(args, "0:a:0") {
			_, _ = io.WriteString(os.Stderr, "Decoder (codec synthetic) not found for input stream #0:1")
			os.Exit(8)
		}
		_, _ = os.Stdout.Write([]byte{0, 0, 128, 62})
		os.Exit(0)
	case "multi-output", "multi-output-nonzero":
		destinationIndex := 0
		for _, argument := range os.Args[separator+2:] {
			if !isPrivatePCMOutput(argument) {
				continue
			}
			connection, err := openHelperPCM(argument)
			if err != nil {
				_, _ = fmt.Fprintln(os.Stderr, err)
				os.Exit(9 + destinationIndex)
			}
			_, _ = connection.Write([]byte("pcm!"))
			_ = connection.Close()
			destinationIndex++
		}
		if os.Args[separator+1] == "multi-output-nonzero" {
			os.Exit(8)
		}
		os.Exit(0)
	case "multi-output-empty-first":
		var destinations []string
		for _, argument := range os.Args[separator+2:] {
			if isPrivatePCMOutput(argument) {
				destinations = append(destinations, argument)
			}
		}
		if len(destinations) != 2 {
			os.Exit(9)
		}
		first, err := openHelperPCM(destinations[0])
		if err != nil {
			os.Exit(9)
		}
		_ = first.Close()
		time.Sleep(200 * time.Millisecond)
		second, err := openHelperPCM(destinations[1])
		if err != nil {
			os.Exit(9)
		}
		_, _ = second.Write([]byte("pcm!"))
		_ = second.Close()
		os.Exit(0)
	case "rogue-pipe":
		connection, err := openHelperPCM(os.Args[separator+2])
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(9)
		}
		_, _ = connection.Write([]byte("bad!"))
		_ = connection.Close()
		os.Exit(0)
	default:
		t.Fatalf("unknown helper mode %q", os.Args[separator+1])
	}
}

func isPrivatePCMOutput(destination string) bool {
	//pathpolicy:allow Windows named-pipe namespace is IPC, not a local filesystem path.
	if strings.HasPrefix(destination, `\\.\pipe\upbrr-audio-`) {
		return true
	}
	fdText, ok := strings.CutPrefix(destination, "pipe:")
	if !ok {
		return false
	}
	fd, err := strconv.Atoi(fdText)
	return err == nil && fd >= 3
}

func openHelperPCM(destination string) (io.WriteCloser, error) {
	//pathpolicy:allow Windows named-pipe namespace is IPC, not a local filesystem path.
	if strings.HasPrefix(destination, `\\.\pipe\upbrr-audio-`) {
		connection, err := os.OpenFile(destination, os.O_WRONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("open private PCM pipe: %w", err)
		}
		return connection, nil
	}
	fdText, ok := strings.CutPrefix(destination, "pipe:")
	if !ok {
		return nil, fmt.Errorf("unsupported private PCM output %q", destination)
	}
	fd, err := strconv.Atoi(fdText)
	if err != nil || fd < 3 {
		return nil, fmt.Errorf("invalid private PCM output %q", destination)
	}
	return os.NewFile(uintptr(fd), destination), nil
}

func audioSubjectForBinding(nativeIDs []string) api.AudioAnalysisSubject {
	tracks := make([]api.MediaTrackFacts, len(nativeIDs))
	for index, nativeID := range nativeIDs {
		channels := 2
		layout := "stereo"
		if index == 1 {
			channels = 1
			layout = "mono"
		}
		tracks[index] = api.MediaTrackFacts{
			ID:            "track-" + strconv.Itoa(index+1),
			Kind:          api.MediaTrackAudio,
			NativeID:      nativeID,
			Ordinal:       index + 1,
			Channels:      channels,
			SampleRate:    48_000,
			ChannelLayout: layout,
		}
	}
	return api.AudioAnalysisSubject{Tracks: tracks}
}
