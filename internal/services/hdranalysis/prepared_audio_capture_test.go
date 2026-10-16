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
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedAudioCapturePublishesNativeBeforeOrderedTracks(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "Synthetic.Prepared.Audio.mkv")
	args := make([]string, 0, 33)
	args = append(args, []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-i", filepath.Join("testdata", "decoded-pq-cuts-25.mkv"),
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=4.8", "-map", "0:v:0"}...)
	for range 5 {
		args = append(args, "-map", "1:a:0")
	}
	args = append(args, "-c:v", "copy", "-c:a", "pcm_s16le", "-c:a:1", "eac3", "-c:a:3", "ac3", "-t", "4.8", path)
	if output, err := exec.CommandContext(t.Context(), executable, args...).CombinedOutput(); err != nil {
		t.Fatalf("author mixed-codec source: %v: %s", err, output)
	}
	facts, err := InspectFileSource(t.Context(), path)
	if err != nil || facts.Decoded == nil {
		t.Fatalf("inspect prepared source: %v", err)
	}
	input := DecodedInput{
		Path:        path,
		Fingerprint: "synthetic-prepared-audio",
		Source:      *facts.Decoded,
	}
	subject := api.AudioAnalysisSubject{
		VideoPath:           path,
		SourceFingerprint:   input.Fingerprint,
		ResourceID:          "resource",
		ManifestFingerprint: "manifest",
	}
	selected := make([]string, 0, 5)
	for i, codec := range []string{"pcm", "eac3", "pcm", "ac3", "pcm"} {
		id := fmt.Sprintf("prepared-%d", i+1)
		selected = append(selected, id)
		subject.Tracks = append(subject.Tracks, api.MediaTrackFacts{
			ID:            id,
			ResourceID:    "resource",
			Kind:          api.MediaTrackAudio,
			Ordinal:       i + 1,
			Channels:      1,
			SampleRate:    48000,
			Codec:         codec,
			ChannelLayout: "mono",
		})
	}
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("metadata_only=%t", fallback), func(t *testing.T) {
			input := input
			if fallback {
				input.Source.Codec = "unsupported"
			}
			checkpointed, count := false, 0
			err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				native, reports, err := session.AnalyzePreparedAutomatic(t.Context(), input, nil, api.HDRDecodedOptions{Audio: true, AllAudioTracks: true},
					api.HDRAnalysisFormatsAuto, directory, 0, PreparedAudioCapture{
						Subject:  subject,
						Selected: selected,
						CheckpointNative: func(native *NativeExtraction, reports []*MeasurementReport) error {
							if checkpointed || !native.ScanComplete || len(reports) != 0 {
								return errors.New("invalid native checkpoint order")
							}
							checkpointed = true
							native.Release()
							return nil
						},
						Publish: func(report *MeasurementReport) error {
							count++
							if !checkpointed || report.AudioAuthority == nil || report.AudioAuthority.TrackID != fmt.Sprintf("prepared-%d", count) ||
								!fallback && (report.Result.Measured != 1 || report.Result.Coverage != api.HDRCoverageComplete || len(MeasurementArtifacts(report)) != 3) {
								return fmt.Errorf("invalid ordered track publication %d: %+v", count, report.Result)
							}
							if fallback && (report.Result.Measured != 0 || report.Result.State == api.HDRDecodedComplete || len(MeasurementArtifacts(report)) != 0) {
								return errors.New("metadata-only capture fabricated audio evidence")
							}
							if !fallback && count == 5 && !strings.Contains(report.Processing[0], "traversals started=1") {
								return errors.New("continuation traversal was not recorded")
							}
							var retained bytes.Buffer
							if err := WriteMeasurement(t.Context(), &retained, report); err != nil {
								return err
							}
							_, err := ReadMeasurement(t.Context(), &retained)
							return err
						},
					})
				if native != nil {
					native.Release()
				}
				if err == nil && len(reports) != 0 {
					return errors.New("audio image reports accumulated outside the incremental callback")
				}
				return err
			})
			if err != nil || count != 5 {
				t.Fatalf("prepared capture tracks=%d: %v", count, err)
			}
		})
	}
}
