// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBoundaryScoreKeepsMetadataPairAtOffsetCut(t *testing.T) {
	for _, offset := range []int{-3, 0, 3} {
		ring := make([][]byte, 8)
		for i := range ring {
			ring[i] = make([]byte, sceneWidth*sceneHeight*2)
			if i >= offset+4 {
				for p := range sceneWidth * sceneHeight {
					binary.LittleEndian.PutUint16(ring[i][2*p:], 50000)
				}
			}
		}
		observation := scoreBoundary(50, pictureTime{
			Value:     2000,
			Timescale: 1000,
			Valid:     true,
		}, ring)
		if observation.BestOffset != offset || observation.Confidence != "dominant_cut" {
			t.Fatalf("offset=%d observation=%+v", offset, observation)
		}
		before, after := boundaryThumbnail(49, 50, ring[3]), boundaryThumbnail(50, 50, ring[4])
		if before.Index != 49 || after.Index != 50 || before.Candidate != 50 || after.Candidate != 50 {
			t.Fatal("pair moved to detected cut")
		}
	}
}

func TestBoundaryScoreRequiresVisibleLight(t *testing.T) {
	for _, litPixels := range []int{0, sceneWidth * sceneHeight / 100, (sceneWidth*sceneHeight + 99) / 100} {
		t.Run(strconv.Itoa(litPixels), func(t *testing.T) {
			ring := make([][]byte, 8)
			for i := range ring {
				ring[i] = make([]byte, sceneWidth*sceneHeight*2)
				for pixel := range sceneWidth * sceneHeight {
					value := uint16(655)
					if i >= 4 {
						value = 4587
						if pixel < litPixels {
							value = 10000
						}
					}
					binary.LittleEndian.PutUint16(ring[i][2*pixel:], value)
				}
			}
			got := scoreBoundary(50, pictureTime{
				Value:     2,
				Timescale: 1,
				Valid:     true,
			}, ring)
			want := "unclear"
			if litPixels*100 >= sceneWidth*sceneHeight {
				want = "dominant_cut"
			}
			if got.Confidence != want || got.BestOffset != 0 || got.Scores[3] < .059 || got.Noise != .005 {
				t.Fatalf("lit pixels=%d: %+v, want %s", litPixels, got, want)
			}
		})
	}
}

func TestBoundaryFiniteDecoderCalibration(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("set UPBRR_HDR_DECODE_CALIBRATION=1 for actual-decoder calibration")
	}
	executable, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Fatal(err)
	}
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		path := filepath.Join("testdata", "decoded-pq-cuts.mkv")
		native, err := session.ExtractNativeFile(t.Context(), path, api.HDRAnalysisFormatsBoth)
		if err != nil {
			return err
		}
		defer native.Release()
		evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return err
		}
		defer evidence.Release()
		input := DecodedInput{
			Path:        path,
			Fingerprint: "synthetic",
			Native:      evidence,
			Source: api.HDRDecodedSourceEvidence{
				VideoStreams:           1,
				Codec:                  "hevc",
				Width:                  192,
				Height:                 112,
				BitDepth:               10,
				PixelAspectNumerator:   1,
				PixelAspectDenominator: 1,
				Range:                  "limited",
				Matrix:                 "bt2020nc",
				Primaries:              "bt2020",
				Transfer:               "smpte2084",
			},
		}
		times, err := decodedTimeline(input)
		if err != nil {
			return err
		}
		// The encoded fixture has constant native metadata. Author known metadata
		// cuts over its real timeline to exercise all five actual image transitions.
		evidence.DolbyVision = decodedMetadataFixture(t, times)
		for i := range evidence.Results {
			if evidence.Results[i].Format == api.HDRNativeDolbyVision {
				evidence.Results[i].State = api.HDRExtractionComplete
			}
		}
		plan, err := PlanBoundary(input, true)
		if err != nil {
			return fmt.Errorf("plan calibration: %w", err)
		}
		if len(plan.report.Windows) == 0 {
			return errors.New("calibration has no planned boundaries")
		}
		decoder := pictureDecoder{executable: executable}
		for _, window := range plan.report.Windows {
			path, proof, err := decoder.prepareBoundaryClip(t.Context(), input, plan, window, t.TempDir())
			if err != nil {
				return fmt.Errorf("prepare calibration %d-%d PTS=%+v: %w", window.First, window.Last, plan.times[window.First], err)
			}
			result := decoder.decodeBoundaryWindow(t.Context(), input, plan, window, path, proof, 2, 2, 512<<20)
			if result.err != nil {
				t.Log(result.diagnostics)
				return fmt.Errorf("window %d-%d: %w", window.First, window.Last, result.err)
			}
			if result.invocation.ExposedPictures > proof.vcl || len(result.invocation.PTS) == 0 {
				return errors.New("missing decode work proof")
			}
			for _, observation := range result.observations {
				if observation.BestOffset != 0 || observation.Confidence != "dominant_cut" {
					return fmt.Errorf("unexpected synthetic cut: %+v", observation)
				}
				indices := []int{observation.Candidate - 1, observation.Candidate}
				colours := decoder.decodeBoundaryPictures(t.Context(), input, plan, window, path, proof, 2, 2, 512<<20, indices)
				if colours.err != nil {
					return fmt.Errorf("selected-pair colour decode: %w", colours.err)
				}
				if len(colours.previews) != 2 || len(colours.previews[indices[0]]) != sceneWidth*sceneHeight*3 ||
					len(colours.previews[indices[1]]) != sceneWidth*sceneHeight*3 {
					return errors.New("selected-pair colour decode returned unexpected frames")
				}
				limited := *plan
				limited.report = BoundaryReport{
					ChargedVCL: boundaryMaxVCL,
					Windows: []BoundaryWindow{{
						First:       window.First,
						Last:        window.Last,
						Invocations: []BoundaryInvocation{{State: "complete"}, result.invocation},
					}},
					Formats: []BoundaryFormat{{Thumbnails: result.pairs[observation.Candidate]}},
				}
				scratch := t.TempDir()
				options := api.HDRDecodedOptions{BoundarySync: true, VideoDecoderThreads: 2}
				canceled, cancel := context.WithCancel(t.Context())
				cancel()
				colourBoundaryPairs(canceled, decoder, input, &limited, options, scratch, 0)
				if len(limited.report.Windows[0].Invocations) != 2 {
					return errors.New("canceled preview started a child")
				}
				colourBoundaryPairs(t.Context(), decoder, input, &limited, options, scratch, 0)
				files, err := os.ReadDir(scratch)
				if err != nil || len(files) != 0 || limited.report.ChargedVCL != boundaryMaxVCL ||
					len(limited.report.Windows[0].Invocations) != 3 || len(limited.report.Formats[0].Thumbnails[0].RGB) != 0 {
					return errors.New("preview exceeded shared work budget or retained its scratch clip")
				}
			}
		}
		evidence.Release()
		native.Release()
		published := 0
		err = session.MeasureBoundary(t.Context(), input, plan, api.HDRDecodedOptions{
			BoundarySync:        true,
			ActiveArea:          true,
			VideoDecoderThreads: 2,
		}, t.TempDir(), 0, func(report *MeasurementReport) error {
			if err := report.Validate(); err != nil {
				return fmt.Errorf("validate published %s: %w", report.Result.Mode, err)
			}
			if report.Result.Measured == 0 || report.Result.Measured != report.Result.Selected ||
				report.Result.Mode == api.HDRDecodedActiveArea && report.Result.Coverage != api.HDRCoverageComplete {
				return fmt.Errorf("incomplete synthetic session: %+v", report.Result)
			}
			published++
			if report.Result.Mode == api.HDRDecodedBoundarySync {
				if report.Result.Measured != 5 {
					return fmt.Errorf("measured %d synthetic cuts, want 5", report.Result.Measured)
				}
				for _, format := range report.Boundary.Formats {
					for _, thumb := range format.Thumbnails {
						if len(thumb.RGB) != sceneWidth*sceneHeight*3 {
							return errors.New("selected boundary frame lacks colour preview")
						}
					}
				}
				var retained bytes.Buffer
				if err := WriteMeasurement(t.Context(), &retained, report); err != nil {
					return err
				}
				restored, err := ReadMeasurement(t.Context(), &retained)
				if err != nil || len(restored.Boundary.Formats[0].Thumbnails[0].RGB) == 0 {
					return fmt.Errorf("colour report roundtrip: %w", err)
				}
				artifacts := MeasurementArtifacts(report)
				if len(artifacts) != 1 || artifacts[0].Kind != api.HDRArtifactBoundaryDVSceneSheet {
					return fmt.Errorf("wrong boundary artifact owners: %+v", artifacts)
				}
				var encoded bytes.Buffer
				if err := session.RenderMeasurementArtifact(t.Context(), report, artifacts[0].Kind, "Synthetic boundary calibration", &encoded); err != nil {
					return err
				}
				config, err := png.DecodeConfig(bytes.NewReader(encoded.Bytes()))
				if err != nil || config.Width != 1800 || config.Height != 900 {
					return fmt.Errorf("invalid boundary sheet dimensions: %+v %w", config, err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if published != 2 {
			return fmt.Errorf("published %d reports, want boundary and area", published)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBoundarySheetRendersColourAndGrayscaleFallback(t *testing.T) {
	before := MeasurementThumbnail{
		Index:     49,
		Candidate: 50,
		Width:     sceneWidth,
		Height:    sceneHeight,
		Luma:      bytes.Repeat([]byte{64}, sceneWidth*sceneHeight),
		RGB:       bytes.Repeat([]byte{220, 50, 30}, sceneWidth*sceneHeight),
	}
	after := before
	after.Index, after.RGB = 50, nil
	report := &MeasurementReport{Boundary: &BoundaryReport{Formats: []BoundaryFormat{{
		Format: api.HDRNativeDolbyVision, Thumbnails: []MeasurementThumbnail{before, after},
	}}}}
	var encoded bytes.Buffer
	if err := renderBoundarySheet(t.Context(), report, api.HDRArtifactBoundaryDVSceneSheet, "Synthetic colour boundary", &encoded); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(25, 120).RGBA()
	if r != 220*257 || g != 50*257 || b != 30*257 {
		t.Fatalf("colour thumbnail became grayscale: %d %d %d", r, g, b)
	}
	r, g, b, _ = img.At(311, 120).RGBA()
	if r != 64*257 || g != r || b != r {
		t.Fatal("missing colour did not retain luma fallback")
	}
}
