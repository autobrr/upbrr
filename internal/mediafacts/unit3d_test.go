// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnit3DHDRDVRoundTrip(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"HDR10",
		"HDR10+",
		"DV P5",
		"DV P7 HDR",
		"DV P8 HDR",
		"DV P10 HDR",
		"DV P7 HDR10+",
		"DV P8 HDR10+",
		"DV P10 HDR10+",
		"DV P5 HDR Vivid",
		"DV P7 HDR Vivid",
		"DV P8 HDR Vivid",
		"DV P10 HDR Vivid",
		"DV P20 HDR Vivid",
		"DV P20",
		"HDR Vivid",
		"HLG",
		"PQ10",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			facts := HDRFromUnit3DHDRDV(value)
			if facts.Status != api.HDREvidenceComplete || facts.Origin != api.HDREvidenceTrackerAPI ||
				!slices.Equal(facts.SourceFields, []string{"hdr_dv"}) {
				t.Fatalf("facts = %#v", facts)
			}
			got, ok := Unit3DHDRDVFromFacts(facts)
			if !ok || got != value {
				t.Fatalf("round trip = %q, %t; want %q, true", got, ok, value)
			}
		})
	}
}

func TestUnit3DHDRDVRejectsUnknownOrIncompleteFacts(t *testing.T) {
	t.Parallel()

	unknown := HDRFromUnit3DHDRDV("DV P9 HDR")
	if unknown.Status != api.HDREvidencePartial || len(unknown.Formats) != 0 {
		t.Fatalf("unknown facts = %#v", unknown)
	}
	for _, facts := range []api.HDRFacts{
		{Formats: []api.HDRFormat{api.HDRFormatHDR10}, Status: api.HDREvidencePartial},
		{Formats: []api.HDRFormat{api.HDRFormatWCG}, Status: api.HDREvidenceComplete},
		{
			Formats:            []api.HDRFormat{api.HDRFormatDolbyVision},
			DolbyVisionProfile: "7",
			Status:             api.HDREvidenceComplete,
		},
	} {
		if value, ok := Unit3DHDRDVFromFacts(facts); ok {
			t.Fatalf("unsupported facts %#v mapped to %q", facts, value)
		}
	}
}

func TestUnit3DHDRDVUsesDolbyVisionMajorProfile(t *testing.T) {
	t.Parallel()

	value, ok := Unit3DHDRDVFromFacts(api.HDRFacts{
		Formats:            []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10},
		DolbyVisionProfile: "8.1",
		Status:             api.HDREvidenceComplete,
	})
	if !ok || value != "DV P8 HDR" {
		t.Fatalf("value = %q, %t", value, ok)
	}
}
