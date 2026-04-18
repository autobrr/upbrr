// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestEditionFromMetaMultiPlaylistAggregatesIMDbMatches(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		SelectedBDMVPlaylists: []api.PlaylistInfo{
			{File: "00001.MPLS", Duration: 7200},
			{File: "00002.MPLS", Duration: 7500},
		},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"120": {DisplayName: "2h", Seconds: 7200, Minutes: 120},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125, Attributes: []string{"Extended"}},
				},
			},
		},
	}

	edition, repack, hybrid := editionFromMeta(meta)
	if edition != "2in1 Theatrical / Extended" {
		t.Fatalf("expected aggregated edition, got %q", edition)
	}
	if repack != "" || hybrid {
		t.Fatalf("expected no repack/hybrid, got %q %t", repack, hybrid)
	}
}

func TestEditionFromMetaMultiPlaylistDeduplicatesMatches(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		SelectedBDMVPlaylists: []api.PlaylistInfo{
			{File: "00001.MPLS", Duration: 7200},
			{File: "00002.MPLS", Duration: 7205},
		},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"120": {DisplayName: "2h", Seconds: 7200, Minutes: 120, Attributes: []string{"Director's Cut"}},
				},
			},
		},
	}

	edition, _, _ := editionFromMeta(meta)
	if edition != "Director's Cut" {
		t.Fatalf("expected deduped edition, got %q", edition)
	}
}

func TestEditionFromMetaMultiPlaylistFallsBackWhenNoIMDbMatch(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		SelectedBDMVPlaylists: []api.PlaylistInfo{
			{File: "00001.MPLS", Duration: 7200},
			{File: "00002.MPLS", Duration: 7500},
		},
		Release: api.ReleaseInfo{
			Edition: []string{"Collector's", "Edition"},
		},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"90": {DisplayName: "1h 30m", Seconds: 5400, Minutes: 90, Attributes: []string{"Extended"}},
				},
			},
		},
	}

	edition, _, _ := editionFromMeta(meta)
	if edition != "Collector's Edition" {
		t.Fatalf("expected fallback edition, got %q", edition)
	}
}

func TestSourceAndTypeInfersWebDLFromParsedRelease(t *testing.T) {
	source, typeValue := sourceAndType(api.PreparedMetadata{
		SourcePath: "Movie.2026.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
		Release: api.ReleaseInfo{
			Source: "Web",
			Type:   "WEBDL",
		},
	}, mediaInfoDoc{})

	if source != "Web" {
		t.Fatalf("expected Web source, got %q", source)
	}
	if typeValue != "WEBDL" {
		t.Fatalf("expected WEBDL type, got %q", typeValue)
	}
}

func TestSourceAndTypeInfersRemuxWhenReleaseTypeMissing(t *testing.T) {
	source, typeValue := sourceAndType(api.PreparedMetadata{
		SourcePath: "Movie.2026.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP.mkv",
		Release: api.ReleaseInfo{
			Source: "BluRay",
		},
	}, mediaInfoDoc{})

	if source != "BluRay" {
		t.Fatalf("expected BluRay source, got %q", source)
	}
	if typeValue != "REMUX" {
		t.Fatalf("expected REMUX type, got %q", typeValue)
	}
}
