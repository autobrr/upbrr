// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json/v2"
	"testing"
)

func TestHDRLegacyBrowserRetryNormalization(t *testing.T) {
	// This is the serialized request produced by the retained/pending v1 browser retry.
	const payload = `{"release":{"SourcePath":"Synthetic.HDR.mkv","Generation":1},"targetIds":["hdr_11111111111111111111111111111111"],"peakSource":"histogram","profileVersion":"hdr-analysis-v1"}`
	var instructions HDRAnalysisInstructions
	if err := json.Unmarshal([]byte(payload), &instructions); err != nil {
		t.Fatal(err)
	}
	normalized, err := instructions.Normalize()
	if err != nil || normalized.ProfileVersion != HDRAnalysisProfileVersion || normalized.Formats != "" {
		t.Fatalf("legacy retry changed: %#v, %v", normalized, err)
	}
	instructions.Formats = HDRAnalysisFormatsAuto
	if _, err := instructions.Normalize(); err == nil {
		t.Fatal("v1 accepted widened format selection")
	}
	instructions.Formats = ""
	instructions.ProfileVersion = HDRAnalysisProfileV2
	normalized, err = instructions.Normalize()
	if err != nil || normalized.Formats != HDRAnalysisFormatsAuto {
		t.Fatalf("v2 omitted format lost default: %#v, %v", normalized, err)
	}
}

func TestDescriptionSubjectPreservesGlobalFinalGroupAuthority(t *testing.T) {
	input := UploadSubject{DescriptionGroupsFinal: true, DescriptionGroups: []DescriptionBuilderGroup{{
		GroupKey:       "default",
		Trackers:       []string{"EXAMPLE"},
		RawDescription: "Owner's complete text",
		HasOverride:    true,
	}}}
	projected := NewDescriptionSubject(input)
	if !projected.DescriptionGroups[0].Final || input.DescriptionGroups[0].Final {
		t.Fatal("global finality was lost or input mutated during description projection")
	}
}

func TestHDRSelectionNormalization(t *testing.T) {
	for input, want := range map[string]string{
		"1":          "00001.MPLS",
		"00001.mpls": "00001.MPLS",
		"99999":      "99999.MPLS",
		"0":          "00000.MPLS",
	} {
		got, err := NormalizeHDRPlaylist(input)
		if err != nil || got != want {
			t.Fatalf("playlist %q = %q %v", input, got, err)
		}
	}
	if value, err := NormalizeHDRPlaylist(" 1 "); err != nil || value != "00001.MPLS" {
		t.Fatalf("trim playlist = %q %v", value, err)
	}
	for _, input := range []string{"", "../1", "1/2", "C:\\1", "-1", "1.MPLS.MPLS", "100000", "1.0"} {
		if _, err := NormalizeHDRPlaylist(input); err == nil {
			t.Fatalf("unsafe playlist %q accepted", input)
		}
	}
	id := HDRTargetID("private-source", "00001.MPLS")
	instructions := HDRAnalysisInstructions{Release: ReleaseRef{SourcePath: "Synthetic.HDR.mkv", Generation: 1}, TargetIDs: []string{id}}
	normalized, err := instructions.Normalize()
	if err != nil || normalized.PeakSource != HDRPeakHistogram || normalized.ProfileVersion != HDRAnalysisProfileVersion {
		t.Fatalf("normalize=%#v %v", normalized, err)
	}
	normalized.TargetIDs[0] = "changed"
	if instructions.TargetIDs[0] != id {
		t.Fatal("normalization retained caller slice")
	}
	instructions.TargetIDs = append(instructions.TargetIDs, id)
	if _, err := instructions.Normalize(); err == nil {
		t.Fatal("duplicate target accepted")
	}
	if _, err := HDRPeakSource("unknown").Normalize(); err == nil {
		t.Fatal("unknown estimator accepted")
	}
}

func TestHDRFormatSelectionRejectsContradictoryOutcomes(t *testing.T) {
	for _, selection := range []HDRAnalysisFormats{HDRAnalysisFormatsAuto, HDRAnalysisFormatsBoth, HDRAnalysisFormatsHDR10Plus, HDRAnalysisFormatsDolbyVision} {
		t.Run(string(selection), func(t *testing.T) {
			results := []HDRFormatResult{
				{Format: HDRNativeHDR10Plus, State: HDRExtractionFailed},
				{Format: HDRNativeDolbyVision, State: HDRExtractionAbsent},
			}
			results[0].Requested = selection == HDRAnalysisFormatsBoth || selection == HDRAnalysisFormatsHDR10Plus
			results[1].Requested = selection == HDRAnalysisFormatsBoth || selection == HDRAnalysisFormatsDolbyVision
			if selection == HDRAnalysisFormatsHDR10Plus {
				results[1].State = HDRExtractionNotAnalyzed
			}
			if selection == HDRAnalysisFormatsDolbyVision {
				results[0].State = HDRExtractionNotAnalyzed
			}
			if err := selection.ValidateResults(results); err != nil {
				t.Fatal(err)
			}
			for i := range results {
				invalid := append([]HDRFormatResult(nil), results...)
				invalid[i].Requested = !invalid[i].Requested
				if err := selection.ValidateResults(invalid); err == nil {
					t.Fatal("accepted contradictory request authority")
				}
				invalid = append([]HDRFormatResult(nil), results...)
				invalid[i].State = HDRExtractionNotAnalyzed
				if results[i].State == HDRExtractionNotAnalyzed {
					invalid[i].State = HDRExtractionAbsent
				}
				if err := selection.ValidateResults(invalid); err == nil {
					t.Fatal("accepted outcome for wrong collector selection")
				}
			}
		})
	}
}
