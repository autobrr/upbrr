// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"
	dv "github.com/Audionut/go-dovi-extract/extract"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNativeReportPreservesLosslessTrackIdentity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "reordered-hybrid.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := dv.Extract(t.Context(), bytes.NewReader(data), dv.Options{})
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := extraction.Analysis(dovi.AnalysisOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const trackID = uint64(9007199254740993)
	analysis.TrackID = trackID
	for i := range analysis.Pictures {
		analysis.Pictures[i].Stream.TrackID = trackID
	}
	evidence := &NativeEvidence{DolbyVision: analysis}
	summary, err := evidence.summarizeDV()
	if err != nil {
		t.Fatal(err)
	}
	encodedSummary, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(encodedSummary, &want); err != nil {
		t.Fatal(err)
	}
	want["track_id"] = strconv.FormatUint(trackID, 10)
	var output bytes.Buffer
	if err := evidence.WriteReport(t.Context(), &output, api.HDRPeakHistogram, "", api.HDRAnalysisTargetResult{}, nil); err != nil {
		t.Fatal(err)
	}
	var report struct {
		DolbyVision map[string]any `json:"dolbyVision"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.DolbyVision, want) {
		t.Fatalf("public summary changed or lost track identity: got %#v, want %#v", report.DolbyVision, want)
	}
	if analysis.TrackID != trackID || analysis.Pictures[0].Stream.TrackID != trackID {
		t.Fatal("public report changed private native identity")
	}
}
