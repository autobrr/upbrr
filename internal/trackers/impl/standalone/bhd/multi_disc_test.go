// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBHDMultiDiscUsesPrimaryMultipartAndAllDescriptionEvidence(t *testing.T) {
	t.Parallel()

	meta := bhdMultiDiscSubject()
	media, err := resolveMediaDump(meta, "")
	if err != nil {
		t.Fatalf("resolve media dump: %v", err)
	}
	if media != "BDINFO ONE" {
		t.Fatalf("multipart media = %q", media)
	}
	if path := resolveMediaPath(meta, ""); path != `C:\private\disc-one\BDINFO.00001.txt` {
		t.Fatalf("dry-run media path = %q", path)
	}

	description := buildDiscSection(meta, "")
	wantOrder := []string{"Disc 1 — 00001.MPLS", "BDINFO ONE", "Disc 2 — 00001.MPLS", "BDINFO TWO"}
	previous := -1
	for _, value := range wantOrder {
		index := strings.Index(description, value)
		if index <= previous {
			t.Fatalf("description evidence order for %q in %q", value, description)
		}
		previous = index
		if strings.Count(description, value) != 1 {
			t.Fatalf("description count for %q = %d", value, strings.Count(description, value))
		}
	}
}

func TestBHDValidationBlocksMissingSecondDiscReport(t *testing.T) {
	t.Parallel()

	meta := bhdMultiDiscSubject()
	meta.Disc.Items[1].Reports[0].Summary = ""
	partial := api.NewTrackerValidationSubject(meta, "BHD")
	if !hasBHDValidationRule(bhdRequiredAssetFailures(partial), "bhd_required_assets_bdinfo") {
		t.Fatalf("missing second-disc BDInfo was not blocked: %+v", bhdRequiredAssetFailures(partial))
	}

	meta.Disc.Items[1].Reports[0].Summary = "BDINFO TWO"
	complete := api.NewTrackerValidationSubject(meta, "BHD")
	if hasBHDValidationRule(bhdRequiredAssetFailures(complete), "bhd_required_assets_bdinfo") {
		t.Fatalf("complete BDInfo remained blocked: %+v", bhdRequiredAssetFailures(complete))
	}
}

func hasBHDValidationRule(failures []api.RuleFailure, rule string) bool {
	for _, failure := range failures {
		if failure.Rule == rule {
			return true
		}
	}
	return false
}

func bhdMultiDiscSubject() api.UploadSubject {
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
					SummaryPath: `C:\private\disc-one\BDINFO.00001.txt`,
				}},
			},
			{
				ID:   "disc-two",
				Name: "Disc 2",
				Type: "BDMV",
				Reports: []api.DiscReportResource{{
					Playlist:    api.PlaylistInfo{ID: "disc-two:00001.MPLS"},
					Summary:     "BDINFO TWO",
					SummaryPath: `C:\private\disc-two\BDINFO.00001.txt`,
				}},
			},
		},
		ExactMedia: &api.ExactMediaAssets{},
	}
}
