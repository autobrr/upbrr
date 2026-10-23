// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"
	dv "github.com/Audionut/go-dovi-extract/extract"

	"github.com/autobrr/upbrr/pkg/api"
)

func gradeReportFixture() *MeasurementReport {
	request, frame := gradeFixture([3]float32{100, 100, 100}, [3]float32{200, 200, 200})
	plan := &gradeMeasurement{}
	plan.observe(0, request, frame)
	options := api.HDRDecodedOptions{Comparison: &api.HDRComparisonOptions{
		SourceID:    "fixture-b",
		FrameOffset: new(2),
		Crop:        *request.crop,
		Grade:       true,
	}}
	report := newMeasurementReport(api.HDRDecodedGrade, request.inputs[0], options)
	report.Result.State, report.Result.Selected, report.Result.Measured = api.HDRDecodedComplete, 1, 1
	report.Selected, report.Grade, report.Chromaticity = request.selected, plan.observations, plan.points
	return report
}

func TestMeasurementReportRoundTripAndBounds(t *testing.T) {
	report := gradeReportFixture()
	var encoded bytes.Buffer
	if err := WriteMeasurement(t.Context(), &encoded, report); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadMeasurement(t.Context(), bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := WriteMeasurement(t.Context(), &again, decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes(), again.Bytes()) {
		t.Fatal("retained measurement changed")
	}
	for _, mutate := range []func([]byte) []byte{
		func(b []byte) []byte { return append(b, 'x') },
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1)
		},
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"measured":1`), []byte(`"measured":1,"measured":1`), 1)
		},
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"pixel":0`), []byte(`"pixel":-1`), 1) },
		func(b []byte) []byte { return append(bytes.Repeat([]byte{' '}, MaxMeasurementBytes), b...) },
	} {
		if _, err := ReadMeasurement(t.Context(), bytes.NewReader(mutate(bytes.Clone(encoded.Bytes())))); err == nil {
			t.Fatal("accepted malformed measurement report")
		}
	}
	decoded.Grade = append(decoded.Grade, decoded.Grade[0])
	if err := decoded.Validate(); err == nil {
		t.Fatal("accepted unordered comparison coordinates")
	}
}

func TestMeasurementPreflightPreservesPathBounds(t *testing.T) {
	for name, field := range map[string]string{
		"array objects":    `"selected":[{},{},{}]`,
		"array containers": `"selected":[[],[],[]]`,
		"array nulls":      `"selected":[null,null,null]`,
		"object names":     `"selected":{"2":null}`,
		"foreign scalar":   `"boundary":{"escaped~/name":true}`,
		"foreign object":   `"boundary":{"windows":{}}`,
		"foreign array":    `"areaSamples":[[]]`,
	} {
		t.Run(name, func(t *testing.T) {
			data := fmt.Sprintf(`{"schema":"hdr-measurement-v1","result":{"mode":%q},%s}`, api.HDRDecodedGrade, field)
			if _, err := ReadMeasurement(t.Context(), strings.NewReader(data)); err == nil || !strings.Contains(err.Error(), "observation bounds") {
				t.Fatalf("unbounded evidence reached typed decoding: %v", err)
			}
		})
	}
	for _, value := range []string{`0`, `null`, `true`, `false`, `"synthetic"`} {
		t.Run("selected scalars "+value, func(t *testing.T) {
			data := fmt.Sprintf(`{"schema":"hdr-measurement-v1","result":{"mode":%q},"selected":[[%s%s]]}`,
				api.HDRDecodedGrade, strings.Repeat(value+",", 600), value)
			if _, err := ReadMeasurement(t.Context(), strings.NewReader(data)); err == nil || !strings.Contains(err.Error(), "observation bounds") {
				t.Fatalf("oversized scalar array reached typed decoding: %v", err)
			}
		})
	}
	data := fmt.Sprintf(`{"schema":"hdr-measurement-v1","result":{"mode":%q},"unknown":%s0%s}`,
		api.HDRDecodedGrade, strings.Repeat(`{"nested":`, 16), strings.Repeat(`}`, 16))
	if _, err := ReadMeasurement(t.Context(), strings.NewReader(data)); err == nil || !strings.Contains(err.Error(), "allocation bounds") {
		t.Fatalf("deep scalar object reached typed decoding: %v", err)
	}
}

func BenchmarkReadMeasurementAutomatic(b *testing.B) {
	report := automaticReportFixture()
	report.Automatic.Frames = make([]AutomaticFrame, 4096)
	for i := range report.Automatic.Frames {
		report.Automatic.Frames[i] = AutomaticFrame{
			Index: i,
			PTS: MeasurementTimestamp{
				Value:     strconv.Itoa(i),
				Timescale: "25",
				Known:     true,
			},
			MaxRGBNits: 100,
			FALLNits:   50,
		}
	}
	report.Result.Selected, report.Result.Measured = len(report.Automatic.Frames), len(report.Automatic.Frames)
	var encoded bytes.Buffer
	if err := WriteMeasurement(b.Context(), &encoded, report); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(encoded.Len()))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ReadMeasurement(b.Context(), bytes.NewReader(encoded.Bytes())); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPictureProfileProjectionPreservesMeasuredEvidence(t *testing.T) {
	original := gradeReportFixture()
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	v3, err := ProjectPictureReport(original, api.HDRAnalysisProfileV3)
	if err != nil || v3.Result.Coverage != api.HDRCoverageComplete || !reflect.DeepEqual(v3.Grade, original.Grade) {
		t.Fatalf("v3 projection changed measured evidence: %v", err)
	}
	artifacts := MeasurementArtifacts(v3)
	for i := range artifacts {
		artifacts[i].Profile = api.HDRAnalysisProfileV2
	}
	if !reflect.DeepEqual(artifacts, MeasurementArtifacts(original)) {
		t.Fatal("projection changed available image variants")
	}
	v3.Options.VideoDecoderThreads = 4
	v3.Decoder.RequestedThreads = 4
	v3.Decoder.EffectiveThreads = []int{4, 4}
	v2, err := ProjectPictureReport(v3, api.HDRAnalysisProfileV2)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := json.Marshal(v2)
	if err != nil || !bytes.Equal(before, projected) {
		t.Fatalf("round-trip projection changed evidence: %v", err)
	}
	unchanged, err := json.Marshal(original)
	if err != nil || !bytes.Equal(before, unchanged) {
		t.Fatal("public projection mutated the retained checkpoint")
	}
}

func TestMeasurementRendererRequiresUsableEvidence(t *testing.T) {
	report := gradeReportFixture()
	session := &Session{}
	var rendered bytes.Buffer
	if err := session.RenderMeasurement(t.Context(), report, "Synthetic HDR comparison", &rendered); err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(rendered.Bytes()))
	if err != nil || config.Width != 1200 || config.Height != 900 {
		t.Fatalf("measurement PNG config=%+v err=%v", config, err)
	}
	report.Result.State = api.HDRDecodedCanceled
	if err := session.RenderMeasurement(t.Context(), report, "Synthetic HDR comparison", &rendered); err == nil {
		t.Fatal("canceled evidence rendered")
	}
}

func TestDecodedMeasurementActualRoundTrip(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("set UPBRR_HDR_DECODE_CALIBRATION=1 for actual-decoder calibration")
	}
	logger := &diagnosticLoggerFixture{}
	err := New(NewAdmission(), logger).WithSession(t.Context(), func(session *Session) error {
		path := filepath.Join("testdata", "decoded-pq-cuts-25.mkv")
		native, err := session.ExtractNativeFile(t.Context(), path, api.HDRAnalysisFormatsBoth)
		if err != nil {
			return fmt.Errorf("extract decoded fixture: %w", err)
		}
		defer native.Release()
		evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return fmt.Errorf("convert decoded fixture: %w", err)
		}
		defer evidence.Release()
		input := DecodedInput{
			Path:        path,
			Fingerprint: "synthetic-pq-cut-25",
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
		options := api.HDRDecodedOptions{Comparison: &api.HDRComparisonOptions{
			SourceID:    "fixture-b",
			FrameOffset: new(0),
			Crop: api.HDRCrop{
				Top:    12,
				Width:  192,
				Height: 88,
			},
			Grade:  true,
			Colour: true,
		}}
		completed := 0
		publish := func(report *MeasurementReport) error {
			expected := 120
			//nolint:exhaustive // This fixture exercises the legacy decoded modes.
			switch report.Result.Mode {
			case api.HDRDecodedAutomatic, api.HDRDecodedAudio:
				t.Fatal("manual measurement unexpectedly used automatic mode")
			case api.HDRDecodedGrade, api.HDRDecodedColour:
			case api.HDRDecodedSceneSync:
				expected = 40
			case api.HDRDecodedActiveArea:
				expected = 3
			}
			if report.Result.State != api.HDRDecodedComplete || report.Result.Selected != expected || report.Result.Measured != expected {
				t.Fatalf("actual measurement result %+v; diagnostics=%+v", report.Result, logger)
			}
			for _, grade := range report.Grade {
				if grade.Difference.MaxAbsolutePQ != [3]float64{} {
					t.Fatal("identical source pair changed grade")
				}
			}
			var stored bytes.Buffer
			if err := WriteMeasurement(t.Context(), &stored, report); err != nil {
				return err
			}
			if bytes.Contains(stored.Bytes(), []byte("testdata")) {
				t.Fatal("report exposed private source path")
			}
			restored, err := ReadMeasurement(t.Context(), bytes.NewReader(stored.Bytes()))
			if err != nil {
				return err
			}
			var output bytes.Buffer
			if err := session.RenderMeasurement(t.Context(), restored, "Synthetic HDR comparison", &output); err != nil {
				return err
			}
			if directory := os.Getenv("UPBRR_HDR_CALIBRATION_OUTPUT"); directory != "" {
				if err := os.WriteFile(filepath.Join(directory, string(report.Result.Mode)+".png"), output.Bytes(), 0o600); err != nil {
					return fmt.Errorf("save calibration preview: %w", err)
				}
				pretty, err := json.Marshal(report, jsontext.WithIndent("  "))
				if err != nil {
					return fmt.Errorf("encode calibration receipt: %w", err)
				}
				if err := os.WriteFile(filepath.Join(directory, string(report.Result.Mode)+".json"), pretty, 0o600); err != nil {
					return fmt.Errorf("save calibration receipt: %w", err)
				}
			}
			completed++
			return nil
		}
		if err := session.MeasureDecoded(t.Context(), input, &input, options, 0, publish); err != nil {
			return err
		}
		// Scene candidates and active-area offsets are authored independently
		// from the decoded pixels, on the native extractor's exact timeline.
		times, err := decodedTimeline(input)
		if err != nil {
			return err
		}
		evidence.DolbyVision = decodedMetadataFixture(t, times)
		if err := session.MeasureDecoded(t.Context(), input, nil, api.HDRDecodedOptions{SceneSync: true, ActiveArea: true}, 0, publish); err != nil {
			return err
		}
		if completed != 4 {
			t.Fatal("shared processing groups did not checkpoint every mode")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func decodedMetadataFixture(t *testing.T, times []pictureTime) *dovi.Analysis {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "single-hybrid.hevc"))
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := dv.Extract(t.Context(), bytes.NewReader(data), dv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer extraction.Release()
	seed, err := extraction.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	analysis := &dovi.Analysis{
		SchemaVersion: 1,
		Complete:      true,
		Timeline:      dovi.PresentationFrames,
		TrackID:       1,
		ObservedRPUs:  uint64(len(times)),
		Records:       make([]dovi.RPU, len(times)),
		Pictures:      make([]dovi.Picture, len(times)),
	}
	for i, stamp := range times {
		record := seed.Records[0]
		record.DM = new(*record.DM)
		record.DM.SceneRefresh = 0
		if slices.Contains([]int{12, 36, 60, 84, 108}, i) {
			record.DM.SceneRefresh = 1
		}
		record.DM.L5 = &dovi.L5{Top: 12, Bottom: 12}
		record.DM.Blocks = append(slices.Clone(record.DM.Blocks), dovi.Block{
			Level:  5,
			Length: 7,
			Values: []int64{0, 0, 12, 12},
		})
		analysis.Records[i] = record
		analysis.Pictures[i] = dovi.Picture{
			Stream:        dovi.StreamKey{TrackID: 1},
			AUOrdinal:     uint64(i),
			DecodeOrdinal: uint64(i),
			POC:           int64(i),
			POCKnown:      true,
			PTS: dovi.Timestamp{
				Value:     stamp.Value,
				Timescale: stamp.Timescale,
				Valid:     stamp.Valid,
			},
			HasRPU:   true,
			RPUIndex: uint32(i),
		}
	}
	if err := analysis.Validate(dovi.AnalysisOptions{}); err != nil {
		t.Fatal(err)
	}
	return analysis
}
