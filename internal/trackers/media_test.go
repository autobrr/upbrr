// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestReadBDInfoReturnsEveryPreparedReportInStableOrder(t *testing.T) {
	t.Parallel()

	meta := multiDiscBDInfoSubject(t.TempDir())
	text, err := ReadBDInfo("", meta)
	if err != nil {
		t.Fatalf("read BDInfo: %v", err)
	}
	want := "Disc 1 — 00001.MPLS\nBDINFO ONE\n\nDisc 2 — 00001.MPLS\nBDINFO TWO"
	if text != want {
		t.Fatalf("BDInfo = %q, want %q", text, want)
	}
	if strings.Count(text, "BDINFO ONE") != 1 || strings.Count(text, "BDINFO TWO") != 1 {
		t.Fatalf("BDInfo duplicated or omitted a report: %q", text)
	}
}

func TestReadBDInfoRejectsPartialPreparedEvidence(t *testing.T) {
	t.Parallel()

	meta := multiDiscBDInfoSubject(t.TempDir())
	meta.Disc.Items[1].Reports[0].Summary = ""
	if _, err := ReadBDInfo("", meta); err == nil || !strings.Contains(err.Error(), "incomplete prepared BDInfo evidence") {
		t.Fatalf("partial BDInfo error = %v", err)
	}
}

func TestReadPrimaryBDInfoUsesCanonicalTypedResource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	meta := multiDiscBDInfoSubject(root)
	text, path, err := ReadPrimaryBDInfo("", meta)
	if err != nil {
		t.Fatalf("read primary BDInfo: %v", err)
	}
	if text != "BDINFO ONE" || path != filepath.Join(root, "disc-one", "BDINFO.00001.txt") {
		t.Fatalf("primary BDInfo = %q at %q", text, path)
	}
}

func multiDiscBDInfoSubject(root string) api.UploadSubject {
	return api.UploadSubject{
		DiscType: "BDMV",
		Disc: api.DiscFacts{
			PrimaryDiscID:   "disc-one",
			PrimaryReportID: "disc-one:00001.MPLS",
			Items: []api.DiscItemFacts{
				{
					ID:   "disc-one",
					Name: "Disc 1",
					Type: "BDMV",
					Reports: []api.DiscReportFacts{{
						Playlist: api.PlaylistInfo{
							ID:     "disc-one:00001.MPLS",
							DiscID: "disc-one",
							File:   "00001.MPLS",
						},
						Summary: "BDINFO ONE",
					}},
				},
				{
					ID:   "disc-two",
					Name: "Disc 2",
					Type: "BDMV",
					Reports: []api.DiscReportFacts{{
						Playlist: api.PlaylistInfo{
							ID:     "disc-two:00001.MPLS",
							DiscID: "disc-two",
							File:   "00001.MPLS",
						},
						Summary: "BDINFO TWO",
					}},
				},
			},
		},
		Discs: []api.DiscEvidenceResource{
			{
				ID:   "disc-one",
				Name: "Disc 1",
				Type: "BDMV",
				Reports: []api.DiscReportResource{{
					Playlist:    api.PlaylistInfo{ID: "disc-one:00001.MPLS"},
					Summary:     "BDINFO ONE",
					SummaryPath: filepath.Join(root, "disc-one", "BDINFO.00001.txt"),
				}},
			},
			{
				ID:   "disc-two",
				Name: "Disc 2",
				Type: "BDMV",
				Reports: []api.DiscReportResource{{
					Playlist:    api.PlaylistInfo{ID: "disc-two:00001.MPLS"},
					Summary:     "BDINFO TWO",
					SummaryPath: filepath.Join(root, "disc-two", "BDINFO.00001.txt"),
				}},
			},
		},
	}
}
