// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

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
