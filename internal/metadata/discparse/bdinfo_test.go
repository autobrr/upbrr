// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package discparse

import (
	"strings"
	"testing"
)

func TestParseBDInfoFiles(t *testing.T) {
	input := "00001.m2ts        00:10:00     1,000,000,000  25.00 Mbps"
	files := ParseBDInfoFiles(input)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].File != "00001.m2ts" {
		t.Fatalf("unexpected file name: %q", files[0].File)
	}
	if files[0].Length != "00:10:00" {
		t.Fatalf("unexpected length: %q", files[0].Length)
	}
}

func TestParseBDInfoSummary(t *testing.T) {
	summary := "Playlist: 00001.MPLS\nDisc Size: 50,000,000,000 bytes\nLength: 01:30:00.000\nVideo: AVC / 30000 kbps / 1920x1080 / 23.976 fps / 16:9 / High / 8 bits / HDR10 / BT.2020\nAudio: English / DTS-HD Master Audio / 5.1 / 48 kHz / 3500 kbps / 24-bit\nSubtitle: English / SDH\nDisc Title: Example\nDisc Label: EXAMPLE"
	files := "00001.m2ts        00:10:00     1,000,000,000  25.00 Mbps"
	info := ParseBDInfoSummary(summary, files, "/disc/BDMV")
	if info.Playlist != "00001" {
		t.Fatalf("unexpected playlist: %q", info.Playlist)
	}
	if len(info.Video) != 1 {
		t.Fatalf("expected 1 video track")
	}
	if len(info.Audio) != 1 {
		t.Fatalf("expected 1 audio track")
	}
	if len(info.Subtitles) != 1 {
		t.Fatalf("expected 1 subtitle")
	}
}

func TestSplitBDInfoReport(t *testing.T) {
	report := "FILES:\n-------------\n00001.m2ts        00:10:00     1,000,000,000\nCHAPTERS:\nQUICK SUMMARY:\nPlaylist: 00001.MPLS\n********************\n[code]\nIGNORE\n[/code]\n[code]\nSUMMARY\nFILES:\n"
	summary, files, ext := SplitBDInfoReport(report)
	if summary == "" {
		t.Fatalf("expected summary")
	}
	if files == "" {
		t.Fatalf("expected files section")
	}
	if ext == "" {
		t.Fatalf("expected ext summary")
	}
}

func TestSplitBDInfoReportExtSummaryUsesSecondCodeMarker(t *testing.T) {
	tests := []struct {
		name   string
		report string
		want   string
	}{
		{
			name: "two code markers",
			report: "QUICK SUMMARY:\nS\n********************\n" +
				"[code]first block\nFILES:\nignore\n[/code]\n" +
				"[code]second block summary\nFILES:\nuse this\n[/code]",
			want: "second block summary",
		},
		{
			name: "three code markers",
			report: "QUICK SUMMARY:\nS\n********************\n" +
				"[code]first block\nFILES:\nignore\n[/code]\n" +
				"[code]second block summary\nFILES:\nuse this\n[/code]\n" +
				"[code]third block\nFILES:\nignore\n[/code]",
			want: "second block summary",
		},
		{
			name: "four code markers",
			report: "QUICK SUMMARY:\nS\n********************\n" +
				"[code]first block\nFILES:\nignore\n[/code]\n" +
				"[code]second block summary\nFILES:\nuse this\n[/code]\n" +
				"[code]third block\nFILES:\nignore\n[/code]\n" +
				"[code]fourth block\nFILES:\nignore\n[/code]",
			want: "second block summary",
		},
		{
			name: "missing closing tag",
			report: "QUICK SUMMARY:\nS\n********************\n" +
				"[code]first block\nFILES:\nignore\n[/code]\n" +
				"[code]second block summary\nFILES:\nuse this\n",
			want: "second block summary",
		},
		{
			name: "extra closing tag",
			report: "QUICK SUMMARY:\nS\n********************\n" +
				"[code]first block\nFILES:\nignore\n[/code]\n" +
				"[code]second block summary[/code][/code]\nFILES:\nuse this\n",
			want: "second block summary[/code][/code]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, got := SplitBDInfoReport(tt.report)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestNormalizePlaylistName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "00001", want: "00001.MPLS"},
		{input: "00002.mpls", want: "00002.MPLS"},
		{input: `BDMV\PLAYLIST\00003.mpls`, want: "00003.MPLS"},
	}

	for _, tt := range tests {
		if got := NormalizePlaylistName(tt.input); got != tt.want {
			t.Fatalf("normalize %q: expected %q, got %q", tt.input, tt.want, got)
		}
	}
}

func TestExtractPlaylistReports(t *testing.T) {
	report := strings.Join([]string{
		"header",
		"********************",
		"PLAYLIST: 00001.MPLS",
		"********************",
		"[code]",
		"table one",
		"[/code]",
		"[code]",
		"DISC INFO:",
		"extended summary one",
		"FILES:",
		"-------------",
		"00001.m2ts        00:10:00     1,000,000,000",
		"CHAPTERS:",
		"QUICK SUMMARY:",
		"Playlist: 00001.MPLS",
		"Disc Label: DISCONE",
		"********************",
		"FILES:",
		"[/code]",
		"********************",
		"PLAYLIST: 00002.MPLS",
		"********************",
		"[code]",
		"table two",
		"[/code]",
		"[code]",
		"DISC INFO:",
		"extended summary two",
		"FILES:",
		"-------------",
		"00002.m2ts        00:20:00     2,000,000,000",
		"CHAPTERS:",
		"QUICK SUMMARY:",
		"Playlist: 00002.MPLS",
		"Disc Label: DISCTWO",
		"********************",
		"FILES:",
		"[/code]",
	}, "\n")

	reports, err := ExtractPlaylistReports(report, []string{"00002.mpls", "00001"})
	if err != nil {
		t.Fatalf("extract reports: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}
	if reports[0].Playlist != "00002.MPLS" || !strings.Contains(reports[0].Summary, "DISCTWO") {
		t.Fatalf("unexpected first report: %#v", reports[0])
	}
	if reports[1].Playlist != "00001.MPLS" || !strings.Contains(reports[1].ExtSummary, "extended summary one") {
		t.Fatalf("unexpected second report: %#v", reports[1])
	}
}

func TestSplitBDInfoPlaylistReportsErrorsOnDuplicateNormalizedPlaylist(t *testing.T) {
	report := strings.Join([]string{
		"********************",
		"PLAYLIST: 00001.MPLS",
		"********************",
		"[code]",
		"one",
		"[/code]",
		"********************",
		"PLAYLIST: BDMV/PLAYLIST/00001",
		"********************",
		"[code]",
		"two",
		"[/code]",
	}, "\n")

	_, err := SplitBDInfoPlaylistReports(report)
	if err == nil || !strings.Contains(err.Error(), "duplicate playlist block 00001.MPLS") {
		t.Fatalf("expected duplicate playlist error, got %v", err)
	}
}
