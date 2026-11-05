// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package seasonep

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCoveragePreservesPackIdentityWithoutInventingMembership(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		source    string
		files     []string
		inventory api.MetadataEvidenceStatus
		part      string
		episodes  []int
		status    api.MetadataEvidenceStatus
	}{
		{"first part", "Example.Show.S01.Part.1", []string{"Example.S01E01-E03.mkv", "Example.S01E04.mkv"}, api.MetadataEvidenceStatusComplete, "Part.1", []int{1, 2, 3, 4}, api.MetadataEvidenceStatusComplete},
		{"second part", "Example.Show.S01.Part.2", []string{"Example.S01E09.mkv", "Example.S01E10+E12.mkv"}, api.MetadataEvidenceStatusComplete, "Part.2", []int{9, 10, 12}, api.MetadataEvidenceStatusComplete},
		{"part has no range", "Example.Show.S01.Part.2", []string{"one.mkv", "two.mkv"}, api.MetadataEvidenceStatusComplete, "Part.2", nil, api.MetadataEvidenceStatusUnavailable},
		{"one unknown member", "Example.Show.S01.Part.1", []string{"Example.S01E01.mkv", "unknown.mkv"}, api.MetadataEvidenceStatusComplete, "Part.1", []int{1}, api.MetadataEvidenceStatusPartial},
		{"partial inventory", "Example.Show.S01.Part.1", []string{"Example.S01E01.mkv"}, api.MetadataEvidenceStatusPartial, "Part.1", []int{1}, api.MetadataEvidenceStatusPartial},
		{"mixed seasons", "Example.Show.S01.Part.1", []string{"Example.S01E01.mkv", "Example.S02E09.mkv"}, api.MetadataEvidenceStatusComplete, "Part.1", []int{1}, api.MetadataEvidenceStatusContradictory},
		{"source range conflict", "Example.Show.S01E01-E04.Part.1", []string{"Example.S01E09.mkv"}, api.MetadataEvidenceStatusComplete, "Part.1", []int{9}, api.MetadataEvidenceStatusContradictory},
		{"explicit range", "Example.Show.S01E01-E04", nil, api.MetadataEvidenceStatusUnavailable, "E01-E04", []int{1, 2, 3, 4}, api.MetadataEvidenceStatusComplete},
		{"descending range", "Example.Show.S01.Part.1", []string{"Example.S01E09-E01.mkv"}, api.MetadataEvidenceStatusComplete, "Part.1", []int{9}, api.MetadataEvidenceStatusContradictory},
		{"conflicting parts", "Example.Show.S01.Part.1.Part.2", []string{"Example.S01E01.mkv"}, api.MetadataEvidenceStatusComplete, "Part.2", []int{1}, api.MetadataEvidenceStatusContradictory},
		{"date annotation", "Example.Show.S01.Part.1", []string{"Example.S01E01-2026.09.09.mkv"}, api.MetadataEvidenceStatusComplete, "Part.1", []int{1}, api.MetadataEvidenceStatusComplete},
		{"unsupported membership", "Example.Show.S01.Part.1", []string{"Example.S01E01,E09.mkv"}, api.MetadataEvidenceStatusComplete, "Part.1", []int{1}, api.MetadataEvidenceStatusContradictory},
		{"other video format", "Example.Show.S01.Part.2", []string{"Example.S01E09.mkv", "Example.S01E02.m4v"}, api.MetadataEvidenceStatusComplete, "Part.2", []int{2, 9}, api.MetadataEvidenceStatusComplete},
		{"unknown other video", "Example.Show.S01.Part.2", []string{"Example.S01E09.mkv", "unknown.m4v"}, api.MetadataEvidenceStatusComplete, "Part.2", []int{9}, api.MetadataEvidenceStatusPartial},
		{"contradictory source range", "Example.Show.S01E01-E03+S02E09", nil, api.MetadataEvidenceStatusUnavailable, "", []int{1, 2, 3}, api.MetadataEvidenceStatusContradictory},
		{"unidentified files retain source range", "Example.Show.S01E01-E04", []string{"one.mkv", "two.mkv"}, api.MetadataEvidenceStatusComplete, "E01-E04", []int{1, 2, 3, 4}, api.MetadataEvidenceStatusPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Coverage(1, tc.source, tc.files, tc.inventory)
			if got.Part != tc.part || !slices.Equal(got.Episodes, tc.episodes) || got.Status != tc.status {
				t.Fatalf("coverage=%+v want part=%s episodes=%v status=%s", got, tc.part, tc.episodes, tc.status)
			}
		})
	}
}

func TestCoverageBindsCompleteAssertionsAndExplicitSeasonZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		source   string
		season   int
		known    bool
		complete bool
		status   api.MetadataEvidenceStatus
	}{
		{"Example.S01.COMPLETE", 2, false, true, api.MetadataEvidenceStatusContradictory},
		{"Example.COMPLETE.S01", 2, false, true, api.MetadataEvidenceStatusContradictory},
		{"Example.S00.Part.1", 0, true, false, api.MetadataEvidenceStatusUnavailable},
		{"Example.Part.1", 0, false, false, api.MetadataEvidenceStatusUnavailable},
	} {
		t.Run(tc.source, func(t *testing.T) {
			got := Coverage(tc.season, tc.source, []string{"one.mkv", "two.mkv"}, api.MetadataEvidenceStatusComplete)
			if got.SeasonKnown != (tc.known || tc.season > 0) || got.CompleteSeason != tc.complete || got.Status != tc.status || got.Episodes == nil {
				t.Fatalf("coverage=%+v", got)
			}
		})
	}
}

func TestCoverageRequiresAnExplicitCompleteSeasonAssertion(t *testing.T) {
	t.Parallel()
	files := []string{"Example.S01E01.mkv", "Example.S01E02.mkv"}
	for _, tc := range []struct {
		source   string
		complete bool
		status   api.MetadataEvidenceStatus
	}{
		{"Example.Show.S01", false, api.MetadataEvidenceStatusComplete},
		{"Example.Show.S01.COMPLETE", true, api.MetadataEvidenceStatusComplete},
		{"Example.Show.S01.COMPLETE.Part.1", true, api.MetadataEvidenceStatusContradictory},
	} {
		t.Run(tc.source, func(t *testing.T) {
			got := Coverage(1, tc.source, files, api.MetadataEvidenceStatusComplete)
			if got.CompleteSeason != tc.complete || got.Status != tc.status {
				t.Fatalf("coverage=%+v", got)
			}
		})
	}
}
