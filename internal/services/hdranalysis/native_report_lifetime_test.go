// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNativeStructuralSummarySurvivesParserRelease(t *testing.T) {
	_, times := sceneFixture(nil)
	evidence := &NativeEvidence{DolbyVision: decodedMetadataFixture(t, times)}
	var before, after bytes.Buffer
	target := api.HDRAnalysisTargetResult{}
	if err := evidence.WriteReport(t.Context(), &before, api.HDRPeakHistogram, "", target, nil); err != nil {
		t.Fatal(err)
	}
	if err := evidence.RetainReportSummary(); err != nil {
		t.Fatal(err)
	}
	evidence.Release()
	if evidence.DolbyVision != nil || evidence.HDR10Plus != nil {
		t.Fatal("native parser storage survived release")
	}
	if err := evidence.WriteReport(t.Context(), &after, api.HDRPeakHistogram, "", target, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("structural evidence changed after parser release")
	}
}
