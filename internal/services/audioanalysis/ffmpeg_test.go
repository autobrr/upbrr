// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	args := ffmpegDecodeArgs(decodeRequest{
		path:         "input.mkv",
		audioOrdinal: 2,
		codec:        "E-AC-3",
	})
	wantTail := []string{
		"-i", "input.mkv", "-map", "0:a:2", "-vn", "-sn", "-dn", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1",
	}
	if !slices.Equal(args[len(args)-len(wantTail):], wantTail) {
		t.Fatalf("decode args = %v", args)
	}
	if !slices.Contains(args, "-nostdin") || !slices.Contains(args, "-drc_scale") {
		t.Fatalf("decode args missing stdin/DRC safety: %v", args)
	}
	for _, arg := range args {
		if arg == "pcm.wav" || arg == "pcm.raw" {
			t.Fatalf("decode args contain temporary PCM output: %v", args)
		}
	}
}

func TestFFmpegDecodeArgsDisableDolbyDecoderDRCForPreparedAliases(t *testing.T) {
	t.Parallel()

	for _, codec := range []string{"DD", "DD+", "AC-3", "E-AC-3", "Dolby Digital Plus"} {
		t.Run(codec, func(t *testing.T) {
			args := ffmpegDecodeArgs(decodeRequest{path: "input.mkv", codec: codec})
			if !slices.Contains(args, "-drc_scale") {
				t.Fatalf("decode args for %q omit DRC disable: %v", codec, args)
			}
		})
	}
	if args := ffmpegDecodeArgs(decodeRequest{path: "input.mkv", codec: "FLAC"}); slices.Contains(args, "-drc_scale") {
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
	err := decoder.Decode(context.Background(), decodeRequest{}, func(reader io.Reader) error {
		var copyErr error
		decoded, copyErr = io.Copy(io.Discard, reader)
		if copyErr != nil {
			return fmt.Errorf("discard decoded PCM: %w", copyErr)
		}
		return nil
	})
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
	err := decoder.Decode(context.Background(), decodeRequest{}, func(io.Reader) error { return wantErr })
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
	err := decoder.Decode(ctx, decodeRequest{}, func(reader io.Reader) error {
		_, copyErr := io.Copy(io.Discard, reader)
		if copyErr != nil {
			return fmt.Errorf("discard decoded PCM: %w", copyErr)
		}
		return nil
	})
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
	err := decoder.Decode(context.Background(), decodeRequest{}, func(reader io.Reader) error {
		var copyErr error
		decoded, copyErr = io.Copy(io.Discard, reader)
		if copyErr != nil {
			return fmt.Errorf("discard decoded PCM: %w", copyErr)
		}
		return nil
	})
	if err == nil || decoded != 4 {
		t.Fatalf("decode bytes = %d, error = %v", decoded, err)
	}
	if failure, typed := api.AsAudioAnalysisFailure(err); typed {
		t.Fatalf("corrupt decode was classified as typed failure %#v", failure)
	}
}

func TestFFmpegDecodeClassifiesUnavailableCodecWithoutExposingDiagnostics(t *testing.T) {
	decoder := helperProcessDecoder("unsupported-codec")
	err := decoder.Decode(context.Background(), decodeRequest{}, func(reader io.Reader) error {
		_, copyErr := io.Copy(io.Discard, reader)
		if copyErr != nil {
			return fmt.Errorf("discard decoded PCM: %w", copyErr)
		}
		return nil
	})
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureUnsupportedCodec {
		t.Fatalf("decode error = %v, failure = %#v", err, failure)
	}
	if strings.Contains(err.Error(), "secret-decoder-detail") {
		t.Fatalf("decode error exposed FFmpeg diagnostics: %v", err)
	}
}

func helperProcessDecoder(mode string) *ffmpegDecoder {
	return &ffmpegDecoder{
		executable: "test-helper",
		command: func(ctx context.Context, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, os.Args[0], "-test.run=TestFFmpegDecodeHelperProcess", "--", mode)
		},
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
	default:
		t.Fatalf("unknown helper mode %q", os.Args[separator+1])
	}
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
