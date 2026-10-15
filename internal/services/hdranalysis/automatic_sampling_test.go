// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func sampledReportFixture() *MeasurementReport {
	r := automaticReportFixture()
	r.Options.Sampled, r.Result.Profile = true, SampledMeasurementProfile
	r.Automatic.Sampling = &AutomaticSampling{
		Step:    25,
		Frames:  [2]int{26, 0},
		Origins: [2]MeasurementTimestamp{r.Automatic.Frames[0].PTS, {}},
	}
	r.Automatic.Frames[1].Index = 25
	r.Automatic.Frames[1].PTS = measurementTimestamp(pictureTime{
		Value:     1,
		Timescale: 1,
		Valid:     true,
	})
	return r
}

func TestSampledReportCoverageAndReplay(t *testing.T) {
	r := sampledReportFixture()
	var encoded bytes.Buffer
	if err := WriteMeasurement(t.Context(), &encoded, r); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMeasurement(t.Context(), &encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*MeasurementReport){
		"skipped first": func(r *MeasurementReport) { r.Automatic.Frames[0].Index = 1 },
		"missing last":  func(r *MeasurementReport) { r.Automatic.Sampling.Frames[0] = 51 },
		"zero step":     func(r *MeasurementReport) { r.Automatic.Sampling.Step = 0 },
		"invented colour": func(r *MeasurementReport) {
			r.Automatic.Chromaticity[0] = []ChromaticityPoint{{
				Index: 1,
				X:     .3,
				Y:     .3,
			}}
		},
		"orphan reference": func(r *MeasurementReport) { r.Automatic.Sampling.Frames[1] = 26 },
		"wrong profile":    func(r *MeasurementReport) { r.Result.Profile = AutomaticMeasurementProfile },
		"wrong origin":     func(r *MeasurementReport) { r.Automatic.Sampling.Origins[0].Value = "1" },
	} {
		t.Run(name, func(t *testing.T) {
			r := sampledReportFixture()
			mutate(r)
			if err := r.Validate(); err == nil {
				t.Fatal("accepted unverifiable sampled coverage")
			}
		})
	}
}

func TestSampledReferenceOffsetsUseSourceFrames(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires actual shared decoder")
	}
	path := filepath.Join("testdata", "decoded-pq-cuts-25.mkv")
	facts, err := InspectFileSource(t.Context(), path)
	if err != nil || facts.Decoded == nil {
		t.Fatalf("source facts: %v", err)
	}
	input := DecodedInput{
		Path:        path,
		Fingerprint: "synthetic-sampled-primary",
		Source:      *facts.Decoded,
	}
	directory := t.TempDir()
	reference := input
	reference.Path, reference.Fingerprint = filepath.Join(directory, "Synthetic.Reference.mkv"), "synthetic-sampled-reference"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reference.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{-3, 3, 0} {
		t.Run(strconv.Itoa(offset), func(t *testing.T) {
			err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				options := api.HDRDecodedOptions{
					Automatic: true,
					Sampled:   true,
					Comparison: &api.HDRComparisonOptions{
						SourceID:    "synthetic-reference",
						FrameOffset: &offset,
						Crop: api.HDRCrop{
							Top:    12,
							Width:  192,
							Height: 88,
						},
						Grade:  true,
						Colour: true,
					},
				}
				native, reports, err := session.AnalyzeAutomatic(t.Context(), input, &reference, options, api.HDRAnalysisFormatsAuto, directory, 0)
				if err != nil {
					return err
				}
				defer native.Release()
				r := reports[0]
				if r.Automatic == nil || r.Automatic.Sampling == nil || len(r.Automatic.Frames) != 5 || len(r.Automatic.Reference) == 0 {
					t.Fatalf("sampled comparison was not retained: %+v", r.Result)
				}
				if err := r.Validate(); err != nil {
					return err
				}
				for slot, frame := range r.Automatic.Frames {
					other, exists := automaticFrameAt(r, frame.Index+offset, true)
					values, available := automaticCurveValues(r, api.HDRArtifactAutomaticGrade, slot)
					if exists != available || exists && (values[0] != picturePQ(frame.MaxRGBNits)-picturePQ(other.MaxRGBNits) || values[1] != picturePQ(frame.FALLNits)-picturePQ(other.FALLNits)) {
						t.Fatal("sample slot was used as original frame number")
					}
				}
				for _, artifact := range MeasurementArtifacts(r) {
					if err := session.RenderMeasurementArtifact(t.Context(), r, artifact.Kind, "Synthetic paired samples", &bytes.Buffer{}); err != nil {
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

func TestParallelPictureMeasurementMatchesSerial(t *testing.T) {
	source := api.HDRDecodedSourceEvidence{
		Width:                  1024,
		Height:                 512,
		BitDepth:               10,
		Codec:                  "hevc",
		VideoStreams:           1,
		PixelAspectNumerator:   1,
		PixelAspectDenominator: 1,
	}
	data := make([]byte, source.Width*source.Height*6)
	for i := 0; i < len(data); i += 2 {
		binary.LittleEndian.PutUint16(data[i:], uint16(10000+(i/2)%40000))
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	serial, err := newAutomaticPictures(source, &api.HDRCrop{Width: 1024, Height: 512})
	if err != nil {
		t.Fatal(err)
	}
	if err := serial.observe(data); err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	parallel, err := newAutomaticPictures(source, &api.HDRCrop{Width: 1024, Height: 512})
	if err != nil {
		t.Fatal(err)
	}
	if err := parallel.observe(data); err != nil {
		t.Fatal(err)
	}
	a, b := serial.frames[0], parallel.frames[0]
	if math.Abs(a.FALLNits-b.FALLNits) > 1e-8 || a.MaxRGBNits != b.MaxRGBNits || a.Borders != b.Borders || a.ColourPixels != b.ColourPixels ||
		a.ChromaticityPixels != b.ChromaticityPixels || !slices.Equal(serial.chroma, parallel.chroma) {
		t.Fatal("parallel row reduction changed picture coverage or cloud selection")
	}
	for i := range 3 {
		if math.Abs(a.MeanRGBPQ[i]-b.MeanRGBPQ[i]) > 1e-10 {
			t.Fatal("parallel row reduction changed channel mean")
		}
	}
}
