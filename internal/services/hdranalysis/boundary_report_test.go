// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBoundaryReportSchemaDispatch(t *testing.T) {
	report := newMeasurementReport(api.HDRDecodedBoundarySync, DecodedInput{Fingerprint: "synthetic"}, api.HDRDecodedOptions{BoundarySync: true})
	report.Boundary = &BoundaryReport{}
	report.Result.State = api.HDRDecodedUnsupported
	var data bytes.Buffer
	if err := WriteMeasurement(context.Background(), &data, report); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMeasurement(context.Background(), bytes.NewReader(data.Bytes()))
	if err != nil || got.Schema != "hdr-measurement-v2" {
		t.Fatalf("roundtrip: %v", err)
	}
	oldProfile := bytes.ReplaceAll(data.Bytes(), []byte(BoundaryMeasurementProfile), []byte("hdr-boundary-pq16-colour-v2"))
	if _, err := ReadMeasurement(t.Context(), bytes.NewReader(oldProfile)); err == nil {
		t.Fatal("old boundary scoring evidence accepted for reuse")
	}
	legacy := bytes.ReplaceAll(data.Bytes(), []byte("hdr-measurement-v2"), []byte("hdr-measurement-v1"))
	if _, err := ReadMeasurement(context.Background(), bytes.NewReader(legacy)); err == nil {
		t.Fatal("boundary body accepted by v1")
	}
	unknown := bytes.ReplaceAll(data.Bytes(), []byte("hdr-measurement-v2"), []byte("hdr-measurement-v9"))
	if _, err := ReadMeasurement(context.Background(), bytes.NewReader(unknown)); err == nil {
		t.Fatal("unknown schema accepted")
	}
}

func TestBoundaryJSONRejectsOversizedNestedEvidence(t *testing.T) {
	for _, data := range []string{
		`{"boundary":{"formats":[{},{},{}]}}`,
		`{"boundary":{"formats":[{"thumbnails":[{"luma":"AAAA"}]}]}}`,
		`{"boundary":{"windows":[{"invocations":[{},{},{},{},{}]}]}}`,
		`{"audio":{"images":[{},{},{}]}}`,
		`{"audio":{"stats":"` + strings.Repeat("x", api.AudioAnalysisStatsMaxBytes+1) + `"}}`,
	} {
		if err := preflightMeasurementV2(context.Background(), []byte(data), api.HDRDecodedBoundarySync); err == nil {
			t.Fatal("oversized evidence accepted")
		}
	}
}

func TestMeasurementSchemaReadsValuesRatherThanMemberNames(t *testing.T) {
	for _, data := range []string{
		`{"schema":"hdr-measurement-v1","result":{"mode":"automatic"}}`,
		`{"result":{"mode":"automatic"},"schema":"hdr-measurement-v1"}`,
	} {
		schema, mode, err := measurementSchema(t.Context(), []byte(data))
		if err != nil || schema != "hdr-measurement-v1" || mode != api.HDRDecodedAutomatic || measurementByteLimit(schema, mode) != MaxAutomaticMeasurementBytes {
			t.Fatalf("legacy schema dispatch: %q %q %v", schema, mode, err)
		}
	}
}

func TestBoundaryJSONAcceptsExactColourPixelValues(t *testing.T) {
	luma := base64.StdEncoding.EncodeToString(make([]byte, sceneWidth*sceneHeight))
	rgb := base64.StdEncoding.EncodeToString(make([]byte, sceneWidth*sceneHeight*3))
	for _, colour := range []string{"", `,"rgb":"` + rgb + `"`} {
		data := []byte(`{"boundary":{"formats":[{"thumbnails":[{"luma":"` + luma + `"` + colour + `}]}]}}`)
		if err := preflightMeasurementV2(t.Context(), data, api.HDRDecodedBoundarySync); err != nil {
			t.Fatalf("valid pixel values mistaken for JSON member names: %v", err)
		}
	}
}
