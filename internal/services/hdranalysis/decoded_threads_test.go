// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestVideoThreadRequestPreservesDefaultsAndClampsSharedRaster(t *testing.T) {
	input := DecodedInput{Source: api.HDRDecodedSourceEvidence{Width: 3840, Height: 2160}}
	if got := requestedVideoThreads(0, 2, []DecodedInput{input}); got != 2 {
		t.Fatalf("legacy default changed: %d", got)
	}
	if got := requestedVideoThreads(0, 8, []DecodedInput{input}); got != 8 {
		t.Fatalf("automatic default changed: %d", got)
	}
	if got := requestedVideoThreads(64, 2, []DecodedInput{input, input}); got != min(5, runtime.GOMAXPROCS(0)/2) {
		t.Fatalf("paired raster did not share the reservation: %d", got)
	}
	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	if got := requestedVideoThreads(64, 2, []DecodedInput{input}); got != 1 {
		t.Fatalf("processor limit ignored: %d", got)
	}
	if got := requestedVideoThreads(1, 2, []DecodedInput{input, input}); got != 0 {
		t.Fatalf("paired decoders admitted above a single CPU budget: %d", got)
	}
	runtime.GOMAXPROCS(2)
	if got := requestedVideoThreads(64, 2, []DecodedInput{input, input}); got != 1 {
		t.Fatalf("paired decoders did not divide the CPU budget: %d", got)
	}
	request := decodedProcessFixture()
	request.threads = 3
	args := pictureDecodeArgs(request)
	position := slices.Index(args, "-threads")
	if position < 0 || args[position+1] != "3" {
		t.Fatal("requested video threads did not reach FFmpeg")
	}
}

func TestAutomaticVideoThreadOverridePreservesMeasuredPictures(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	path := filepath.Join("testdata", "decoded-pq-cuts-25.mkv")
	facts, err := InspectFileSource(t.Context(), path)
	if err != nil || facts.Decoded == nil {
		t.Fatalf("inspect source: %v", err)
	}
	var previous *AutomaticMeasurement
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		for _, threads := range []int{1, 4} {
			native, reports, err := session.AnalyzePreparedAutomatic(t.Context(), DecodedInput{
				Path:        path,
				Fingerprint: "synthetic-threads",
				Source:      *facts.Decoded,
			}, nil,
				api.HDRDecodedOptions{
					Automatic:           true,
					Sampled:             true,
					VideoDecoderThreads: threads,
				}, api.HDRAnalysisFormatsAuto, t.TempDir(), 0, PreparedAudioCapture{
					Subject:          api.AudioAnalysisSubject{VideoPath: path, SourceFingerprint: "synthetic-threads"},
					CheckpointNative: func(*NativeExtraction, []*MeasurementReport) error { return nil },
					Publish:          func(*MeasurementReport) error { t.Fatal("no-audio source published an audio track"); return nil },
				})
			if native != nil {
				native.Release()
			}
			if err != nil {
				return err
			}
			if len(reports) != 1 || reports[0].Automatic == nil || reports[0].Decoder.RequestedThreads != threads ||
				!slices.Equal(reports[0].Decoder.EffectiveThreads, []int{threads}) {
				t.Fatal("automatic thread execution receipt missing")
			}
			if err := reports[0].Validate(); err != nil {
				return err
			}
			if previous != nil && !reflect.DeepEqual(previous, reports[0].Automatic) {
				t.Fatal("thread override changed retained picture measurements")
			}
			previous = reports[0].Automatic
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
