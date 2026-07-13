// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/discparse"
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

	edition, repack := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "2in1 Theatrical / Extended" {
		t.Fatalf("expected aggregated edition, got %q", edition)
	}
	if repack != "" {
		t.Fatalf("expected no repack, got %q", repack)
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

	edition, _ := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "Director's Cut" {
		t.Fatalf("expected deduped edition, got %q", edition)
	}
}

func TestEditionFromMetaMultiPlaylistTieBreaksEqualRuntimeMatches(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		SelectedBDMVPlaylists: []api.PlaylistInfo{
			{File: "00001.MPLS", Duration: 7500},
			{File: "00002.MPLS", Duration: 7500},
		},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"124": {DisplayName: "2h 4m 50s", Seconds: 7490, Minutes: 124, Attributes: []string{"extended cut"}},
					"125": {DisplayName: "2h 5m 10s", Seconds: 7510, Minutes: 125, Attributes: []string{"director's cut"}},
				},
			},
		},
	}

	edition, _ := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "Director's Cut" {
		t.Fatalf("expected deterministic tie-broken edition, got %q", edition)
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

	edition, _ := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "Collector's" {
		t.Fatalf("expected fallback edition, got %q", edition)
	}
}

func TestEditionFromMetaRecoversHybridFromParsedTokens(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  `D:\Shows\Example Series S01 2026 Hybrid BDRemux 1080p`,
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		Release:     api.ReleaseInfo{Other: []string{"HYBRiD"}},
	}

	edition, _ := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "Hybrid" {
		t.Fatalf("expected hybrid edition, got %q", edition)
	}
}

func TestEditionFromMetaKeepsHybridAlongsideEdition(t *testing.T) {
	meta := api.PreparedMetadata{
		SourcePath:  `D:\Movies\Example.Movie.2026.Hybrid.Extended.1080p.BluRay.REMUX.AVC-GRP`,
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		Release:     api.ReleaseInfo{Edition: []string{"Extended"}, Other: []string{"HYBRiD"}},
	}

	edition, _ := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "Hybrid Extended" {
		t.Fatalf("expected hybrid to join the edition, got %q", edition)
	}
}

func TestEditionFromMetaMatchesIMDbRuntimeForSingleFile(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"100": {DisplayName: "1h 40m", Seconds: 6000, Minutes: 100},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125, Attributes: []string{"extended edition"}},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7502.000"}]}}`)

	edition, repack := editionFromMeta(meta, doc)
	if edition != "Extended" {
		t.Fatalf("expected IMDb runtime edition, got %q", edition)
	}
	if repack != "" {
		t.Fatalf("expected no repack, got %q", repack)
	}
}

func TestEditionFromMetaIgnoresIMDbRuntimeTheatricalOnlyForSingleFile(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"100": {DisplayName: "1h 40m", Seconds: 6000, Minutes: 100},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7502.000"}]}}`)

	edition, _ := editionFromMeta(meta, doc)
	if edition != "" {
		t.Fatalf("expected theatrical-only IMDb runtime match to be ignored, got %q", edition)
	}
}

func TestEditionFromMetaChoosesClosestIMDbRuntimeForSingleFile(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"124": {DisplayName: "2h 4m", Seconds: 7440, Minutes: 124, Attributes: []string{"director's cut"}},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125, Attributes: []string{"extended cut"}},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7478.000"}]}}`)

	edition, _ := editionFromMeta(meta, doc)
	if edition != "Extended" {
		t.Fatalf("expected closest IMDb runtime edition, got %q", edition)
	}
}

func TestEditionFromMetaSuppressesEditionWhenCloserIMDbRuntimeIsTheatrical(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"124": {DisplayName: "2h 4m", Seconds: 7440, Minutes: 124, Attributes: []string{"director's cut"}},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7498.000"}]}}`)

	edition, _ := editionFromMeta(meta, doc)
	if edition != "" {
		t.Fatalf("expected closer theatrical match to suppress edition, got %q", edition)
	}
}

func TestEditionFromMetaSkipsIMDbRuntimeWhenManualEditionOverridePresent(t *testing.T) {
	manual := "Hybrid"
	meta := api.PreparedMetadata{
		ReleaseNameOverrides: api.ReleaseNameOverrides{Edition: &manual},
		Release:              api.ReleaseInfo{Edition: []string{"Collector's", "Edition"}},
		ExternalIDs:          api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"100": {DisplayName: "1h 40m", Seconds: 6000, Minutes: 100},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125, Attributes: []string{"extended"}},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7500.000"}]}}`)

	edition, _ := editionFromMeta(meta, doc)
	if edition != "Collector's" {
		t.Fatalf("expected parsed release edition when manual override skips IMDb auto edition, got %q", edition)
	}
}

func TestEditionFromMetaSkipsIMDbRuntimeWhenNoEditionOverridePresent(t *testing.T) {
	noEdition := true
	meta := api.PreparedMetadata{
		ReleaseNameOverrides: api.ReleaseNameOverrides{NoEdition: &noEdition},
		Edition:              "IMAX",
		Release:              api.ReleaseInfo{Edition: []string{"Collector's", "Edition"}},
		ExternalIDs:          api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"100": {DisplayName: "1h 40m", Seconds: 6000, Minutes: 100},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125, Attributes: []string{"extended"}},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7500.000"}]}}`)

	edition, _ := editionFromMeta(meta, doc)
	if edition != "" {
		t.Fatalf("expected no edition when no-edition override is present, got %q", edition)
	}
}

func TestEditionFromMetaSkipsIMDbRuntimeWhenAnimeOverridePresent(t *testing.T) {
	anime := true
	meta := api.PreparedMetadata{
		MetadataOverrides: api.MetadataOverrides{Anime: &anime},
		ExternalIDs:       api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"100": {DisplayName: "1h 40m", Seconds: 6000, Minutes: 100},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125, Attributes: []string{"extended"}},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7500.000"}]}}`)

	edition, _ := editionFromMeta(meta, doc)
	if edition != "" {
		t.Fatalf("expected anime override to skip IMDb runtime edition, got %q", edition)
	}
}

func TestEditionFromMetaExtractsRepackAndCleansEdition(t *testing.T) {
	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{Edition: []string{"Limited", "Extended", "Edition", "REPACK2"}},
	}

	edition, repack := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "Extended" {
		t.Fatalf("expected cleaned edition, got %q", edition)
	}
	if repack != "REPACK2" {
		t.Fatalf("expected repack extraction, got %q", repack)
	}
}

func TestEditionFromMetaDropsPunctuationOnlyEditionResidue(t *testing.T) {
	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{Edition: []string{"Limited.Edition", "Limited.Edition"}},
	}

	edition, repack := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "" {
		t.Fatalf("expected punctuation-only edition residue to be dropped, got %q", edition)
	}
	if repack != "" {
		t.Fatalf("expected no repack, got %q", repack)
	}
}

func TestEditionFromMetaTrimsPunctuationAroundKeptEdition(t *testing.T) {
	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{Edition: []string{"Limited.Edition", "Extended.Edition"}},
	}

	edition, _ := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "Extended" {
		t.Fatalf("expected punctuation around kept edition to be trimmed, got %q", edition)
	}
}

func TestEditionFromMetaStripsRepackAliasesFromEdition(t *testing.T) {
	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{Edition: []string{"Director's", "Cut", "V3"}},
	}

	edition, repack := editionFromMeta(meta, mediaInfoDoc{})
	if edition != "Director's Cut" {
		t.Fatalf("expected cleaned edition without repack alias, got %q", edition)
	}
	if repack != "REPACK2" {
		t.Fatalf("expected normalized repack alias, got %q", repack)
	}
}

func TestEditionFromMetaExtractsRepackFromSourcePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "explicit repack2",
			path: `C:\Movies\Example.Movie.2026.REPACK2.1080p.BluRay.DTS.x264-GRP`,
			want: "REPACK2",
		},
		{
			name: "v3 maps to repack2",
			path: `C:\Movies\Example.Movie.2026.V3.1080p.BluRay.DTS.x264-GRP`,
			want: "REPACK2",
		},
		{
			name: "v4 maps to repack3",
			path: `C:\Movies\Example.Movie.2026.V4.1080p.BluRay.DTS.x264-GRP`,
			want: "REPACK3",
		},
		{
			name: "proper2",
			path: `C:\Movies\Example.Movie.2026.PROPER2.1080p.BluRay.DTS.x264-GRP`,
			want: "PROPER2",
		},
		{
			name: "parent path marker ignored",
			path: `C:\Movies\REPACK\Example.Movie.2026.1080p.BluRay.DTS.x264-GRP`,
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			edition, repack := editionFromMeta(api.PreparedMetadata{SourcePath: tc.path}, mediaInfoDoc{})
			if edition != "" {
				t.Fatalf("expected empty edition, got %q", edition)
			}
			if repack != tc.want {
				t.Fatalf("expected repack %q, got %q", tc.want, repack)
			}
		})
	}
}

func TestMediaDurationSecondsParsesMediaInfoDurationFormats(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want float64
	}{
		{
			name: "numeric duration",
			doc:  `{"media":{"track":[{"@type":"General","Duration":"7,502.000"}]}}`,
			want: 7502,
		},
		{
			name: "string3 colon duration",
			doc:  `{"media":{"track":[{"@type":"General","Duration/String3":"02:05:02.000"}]}}`,
			want: 7502,
		},
		{
			name: "token duration",
			doc:  `{"media":{"track":[{"@type":"General","Duration/String":"2 h 5 min 2 s"}]}}`,
			want: 7502,
		},
		{
			name: "invalid duration",
			doc:  `{"media":{"track":[{"@type":"General","Duration":"not a duration"}]}}`,
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := mustParseMediaInfoDoc(tt.doc)
			if got := mediaDurationSeconds(doc); got != tt.want {
				t.Fatalf("expected duration %.3f, got %.3f", tt.want, got)
			}
		})
	}
}

func TestEditionFromMetaMatchesIMDbRuntimeFromDurationString(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"100": {DisplayName: "1h 40m", Seconds: 6000, Minutes: 100},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125, Attributes: []string{"extended"}},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration/String":"2 h 5 min"}]}}`)

	edition, _ := editionFromMeta(meta, doc)
	if edition != "Extended" {
		t.Fatalf("expected IMDb runtime edition from duration string, got %q", edition)
	}
}

func TestEditionFromMetaPreservesIMDbEditionAttributeText(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{
				EditionDetails: map[string]api.IMDBEditionDetail{
					"100": {DisplayName: "1h 40m", Seconds: 6000, Minutes: 100},
					"125": {DisplayName: "2h 5m", Seconds: 7500, Minutes: 125, Attributes: []string{"IMAX", "remastered version"}},
				},
			},
		},
	}
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General","Duration":"7500.000"}]}}`)

	edition, _ := editionFromMeta(meta, doc)
	if edition != "IMAX Remastered Version" {
		t.Fatalf("expected preserved IMDb attribute edition, got %q", edition)
	}
}

func TestSmartEditionWordHandlesUnicodeFirstRune(t *testing.T) {
	if got := smartEditionWord("édition"); got != "Édition" {
		t.Fatalf("expected unicode-safe titlecase, got %q", got)
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

func TestSourceAndTypeBareWebEncodeDefaultsToWebDL(t *testing.T) {
	source, typeValue := sourceAndType(api.PreparedMetadata{
		SourcePath: "Movie.2026.HDR.2160p.WEB.h265-GRP.mkv",
		Release: api.ReleaseInfo{
			Source: "WEB",
			Type:   "ENCODE",
		},
	}, mediaInfoDoc{})

	if source != "Web" {
		t.Fatalf("expected Web source, got %q", source)
	}
	if typeValue != "WEBDL" {
		t.Fatalf("expected WEBDL type, got %q", typeValue)
	}
}

func TestSourceAndTypeBareWebMissingTypeDefaultsToWebDL(t *testing.T) {
	source, typeValue := sourceAndType(api.PreparedMetadata{
		SourcePath: "Movie.2026.HDR.2160p.WEB.h265-GRP.mkv",
		Release: api.ReleaseInfo{
			Source: "WEB",
		},
	}, mediaInfoDoc{})

	if source != "Web" {
		t.Fatalf("expected Web source, got %q", source)
	}
	if typeValue != "WEBDL" {
		t.Fatalf("expected WEBDL type, got %q", typeValue)
	}
}

func TestApplyMediaDetailsFallsBackToFilenameHDRWhenMediaInfoHasNoHDR(t *testing.T) {
	miPath := filepath.Join(t.TempDir(), "mediainfo.json")
	if err := os.WriteFile(miPath, []byte(`{"media":{"track":[{"@type":"General"},{"@type":"Video","Format":"HEVC","Width":"3840","Height":"2160"}]}}`), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	meta, err := svc.ApplyMediaDetails(context.Background(), api.PreparedMetadata{
		SourcePath:        "Movie.2026.[DV].HDR10+.HLG.2160p.WEB-DL.H.265-GRP.mkv",
		MediaInfoJSONPath: miPath,
		Release:           api.ReleaseInfo{HDR: []string{"HDR"}, Resolution: "2160p"},
	})
	if err != nil {
		t.Fatalf("apply media details: %v", err)
	}
	if meta.HDR != "DV HDR10+ HLG" {
		t.Fatalf("expected filename HDR fallback, got %q", meta.HDR)
	}
	if !strings.Contains(meta.ReleaseName, "DV HDR10+ HLG") {
		t.Fatalf("expected rebuilt release name to include filename HDR, got %q", meta.ReleaseName)
	}
}

func TestHDRFromMediaPrefersMediaInfoOverFilenameHDR(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","colour_primaries":"BT.2020","HDR_Format":"HDR10+"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{
		SourcePath: "Movie.2026.DV.HDR.2160p.WEB-DL.H.265-GRP.mkv",
	})
	if got != "HDR10+" {
		t.Fatalf("expected MediaInfo HDR precedence, got %q", got)
	}
}

func TestHDRFromMediaNormalizesPQTransferToHDR(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","colour_primaries":"BT.2020","transfer_characteristics":"PQ"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{})
	if got != "HDR" {
		t.Fatalf("expected PQ transfer to normalize to HDR, got %q", got)
	}
}

func TestHDRFromMediaDetectsDolbyVisionHDR10Compatibility(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","HDR_Format":"Dolby Vision","HDR_Format_String":"Dolby Vision, Version 1.0, Profile 8.1, dvhe.08.06, BL+RPU, no metadata compression, HDR10 compatible"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{})
	if got != "DV HDR" {
		t.Fatalf("expected Dolby Vision HDR10 compatibility to normalize to DV HDR, got %q", got)
	}
}

func TestHDRFromMediaDetectsDolbyVisionHDR10PlusCompatibility(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","HDR_Format":"Dolby Vision","HDR_Format_String":"Dolby Vision, Version 1.0, Profile 8.1, dvhe.08.06, BL+RPU, no metadata compression, HDR10 compatible / SMPTE ST 2094 App 4, Version 1, HDR10+ Profile B compatible"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{})
	if got != "DV HDR10+" {
		t.Fatalf("expected Dolby Vision HDR10+ compatibility to normalize to DV HDR10+, got %q", got)
	}
}

func TestHDRFromMediaDetectsDolbyVisionOnly(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","HDR_Format_String":"Dolby Vision, Version 1.0, Profile 5"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{})
	if got != "DV" {
		t.Fatalf("expected Dolby Vision metadata to normalize to DV, got %q", got)
	}
}

func TestHDRFromMediaDetectsSMPTE2094AsHDR(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","colour_primaries":"BT.2020","HDR_Format_String":"SMPTE ST 2094 App 4, Version 1"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{})
	if got != "HDR" {
		t.Fatalf("expected SMPTE ST 2094 metadata to normalize to HDR, got %q", got)
	}
}

func TestHDRFromMediaDetectsHLGFormat(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","colour_primaries":"BT.2020","HDR_Format_String":"HLG"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{})
	if got != "HLG" {
		t.Fatalf("expected HLG format metadata to normalize to HLG, got %q", got)
	}
}

func TestHDRFromMediaDetectsHLGTransfer(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","colour_primaries":"BT.2020","transfer_characteristics":"HLG"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{})
	if got != "HLG" {
		t.Fatalf("expected HLG transfer metadata to normalize to HLG, got %q", got)
	}
}

func TestHDRFromMediaDetectsBT2020TransferAsWCG(t *testing.T) {
	doc, err := loadMediaInfoDocFromJSONPayload(`{"media":{"track":[{"@type":"General"},{"@type":"Video","colour_primaries":"BT.2020","transfer_characteristics_Original":"BT.2020 (10-bit)"}]}}`)
	if err != nil {
		t.Fatalf("parse mediainfo: %v", err)
	}

	got := hdrFromMedia(doc, nil, api.PreparedMetadata{})
	if got != "WCG" {
		t.Fatalf("expected BT.2020 transfer metadata to normalize to WCG, got %q", got)
	}
}

func TestHDRFromMediaPrefersBDInfoOverFilenameHDR(t *testing.T) {
	got := hdrFromMedia(mediaInfoDoc{}, &discparse.BDInfo{
		Video: []discparse.BDVideo{
			{HDRDV: "HDR10+"},
			{HDRDV: "Dolby Vision"},
		},
	}, api.PreparedMetadata{
		SourcePath: "Movie.2026.HDR.2160p.BluRay-GRP",
	})
	if got != "DV HDR10+" {
		t.Fatalf("expected BDInfo HDR precedence, got %q", got)
	}
}

func TestFilenameHDRFromMetaNormalizesSafeTokens(t *testing.T) {
	tests := []struct {
		name string
		meta api.PreparedMetadata
		want string
	}{
		{
			name: "dot bracket space tokens",
			meta: api.PreparedMetadata{SourcePath: "Movie.2026.[DV].HDR10+.HLG.2160p.WEB-DL.H.265-GRP.mkv"},
			want: "DV HDR10+ HLG",
		},
		{
			name: "underscore separator",
			meta: api.PreparedMetadata{SourcePath: "Movie_2026_HDR_2160p_WEB-DL_H265-GRP.mkv"},
			want: "HDR",
		},
		{
			name: "hyphen separator",
			meta: api.PreparedMetadata{SourcePath: "Movie-2026-HDR10-2160p-WEB-DL-H265-GRP.mkv"},
			want: "HDR",
		},
		{
			name: "source path tokens win over stale parsed release tokens",
			meta: api.PreparedMetadata{
				SourcePath: "Movie.2026.DV.HDR10+.2160p.WEB-DL.H265-GRP.mkv",
				Release:    api.ReleaseInfo{HDR: []string{"HDR"}},
			},
			want: "DV HDR10+",
		},
		{
			name: "parsed release tokens fallback when source path has no hdr",
			meta: api.PreparedMetadata{Release: api.ReleaseInfo{HDR: []string{"DV", "HDR10+", "SDR"}}},
			want: "DV HDR10+",
		},
		{
			name: "sdr token ignored",
			meta: api.PreparedMetadata{SourcePath: "Movie.2026.SDR.2160p.WEB-DL.H.265-GRP.mkv"},
			want: "",
		},
		{
			name: "hdrip source ignored",
			meta: api.PreparedMetadata{SourcePath: "Movie.2026.1080p.HDRip.H.264-GRP.mkv"},
			want: "",
		},
		{
			name: "group suffix ignored",
			meta: api.PreparedMetadata{SourcePath: "Movie.2026.2160p.WEB-DL.H.265-GRP-HDR.mkv"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filenameHDRFromMeta(tt.meta)
			if got != tt.want {
				t.Fatalf("expected filename HDR %q, got %q", tt.want, got)
			}
		})
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

// Python get_type() falls back to "ENCODE" for any release that is not a disc
// and does not match a known keyword. Verify Go does the same.
func TestSourceAndTypeDefaultsToEncodeForUnknownRelease(t *testing.T) {
	_, typeValue := sourceAndType(api.PreparedMetadata{
		SourcePath: "Some.Unknown.Movie.2026-GRP.mkv",
		Release:    api.ReleaseInfo{},
	}, mediaInfoDoc{})

	if typeValue != "ENCODE" {
		t.Fatalf("expected ENCODE type for unknown release, got %q", typeValue)
	}
}

func TestSourceAndTypeEncodeDefaultNotAppliedForDiscs(t *testing.T) {
	for _, discType := range []string{"BDMV", "DVD", "HDDVD"} {
		t.Run(discType, func(t *testing.T) {
			t.Parallel()
			_, typeValue := sourceAndType(api.PreparedMetadata{
				DiscType:   discType,
				SourcePath: "/media/disc",
				Release:    api.ReleaseInfo{},
			}, mediaInfoDoc{})
			if typeValue != "DISC" {
				t.Fatalf("disc type %q should default to DISC, got %q", discType, typeValue)
			}
		})
	}
}

func TestSourceAndTypeDefaultsDiscSourceForBDMV(t *testing.T) {
	source, typeValue := sourceAndType(api.PreparedMetadata{
		DiscType:   "BDMV",
		SourcePath: "/media/disc",
		Release:    api.ReleaseInfo{},
	}, mediaInfoDoc{})

	if typeValue != "DISC" {
		t.Fatalf("expected DISC type for BDMV, got %q", typeValue)
	}
	if source != "Blu-ray" {
		t.Fatalf("expected Blu-ray source for BDMV DISC, got %q", source)
	}
}

// Python get_uhd() does NOT include WEBRIP in the 2160p→UHD check.
// Verify that a 2160p WEBRIP does not produce a UHD flag.
func TestUHDFromMetaWEBRIP2160pNotUHD(t *testing.T) {
	meta := api.PreparedMetadata{
		Type: "WEBRIP",
		Release: api.ReleaseInfo{
			Resolution: "2160p",
		},
	}
	if uhd := uhdFromMeta(meta); uhd != "" {
		t.Fatalf("expected no UHD for WEBRIP 2160p, got %q", uhd)
	}
}

func TestUHDFromMetaWEBDL2160pNotUHD(t *testing.T) {
	meta := api.PreparedMetadata{
		Type: "WEBDL",
		Release: api.ReleaseInfo{
			Resolution: "2160p",
		},
	}
	if uhd := uhdFromMeta(meta); uhd != "" {
		t.Fatalf("expected no UHD for WEBDL 2160p, got %q", uhd)
	}
}

func TestUHDFromMetaBareWebEncode2160pNotUHD(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:   "ENCODE",
		Source: "WEB",
		Release: api.ReleaseInfo{
			Resolution: "2160p",
			Source:     "WEB",
		},
	}
	if uhd := uhdFromMeta(meta); uhd != "" {
		t.Fatalf("expected no UHD for WEB ENCODE 2160p, got %q", uhd)
	}
}

func TestUHDFromMetaENCODE2160pIsUHD(t *testing.T) {
	meta := api.PreparedMetadata{
		Type: "ENCODE",
		Release: api.ReleaseInfo{
			Resolution: "2160p",
		},
	}
	if uhd := uhdFromMeta(meta); uhd != "UHD" {
		t.Fatalf("expected UHD for ENCODE 2160p, got %q", uhd)
	}
}

func TestUHDFromMetaUHDInPath(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:       "WEBRIP",
		SourcePath: "/media/Movie.2160p.UHD.WEBRip-GRP.mkv",
		Release: api.ReleaseInfo{
			Resolution: "2160p",
		},
	}
	if uhd := uhdFromMeta(meta); uhd != "UHD" {
		t.Fatalf("expected UHD when path contains UHD, got %q", uhd)
	}
}

func TestUHDFromMetaUltraHDReleaseOther(t *testing.T) {
	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{
			Other: []string{"Ultra HD"},
		},
	}
	if uhd := uhdFromMeta(meta); uhd != "UHD" {
		t.Fatalf("expected UHD when release other contains Ultra HD, got %q", uhd)
	}
}

func TestAudioFromMediaAddsDualAudioForEnglishAndOriginalLanguage(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Language":"en","StreamOrder":"1"},{"@type":"Audio","Format":"AC-3","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Language":"ja","StreamOrder":"2"}]}}`)
	meta := api.PreparedMetadata{
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	audio, channels, commentary := audioFromMedia(meta, doc, nil)
	if audio != "Dual-Audio DD 5.1" {
		t.Fatalf("expected Dual-Audio DD 5.1, got %q", audio)
	}
	if channels != "5.1" || commentary {
		t.Fatalf("expected 5.1 with no commentary, got channels=%q commentary=%t", channels, commentary)
	}
}

func TestAudioFromMediaSkipsCommentaryTitleVariantsForDualAudio(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"MLP FBA","Format_AdditionalFeatures":"16-ch","Channels":"8","ChannelLayout":"L R C LFE Ls Rs Lb Rb","Language":"en","StreamOrder":"1"},{"@type":"Audio","Format":"AC-3","Channels":"2","ChannelLayout":"L R","Language":"ja","StreamOrder":"2","Title_String":"Director Commentary"}]}}`)
	meta := api.PreparedMetadata{
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}

	audio, channels, commentary := audioFromMedia(meta, doc, nil)
	if audio != "Dubbed TrueHD 7.1 Atmos" {
		t.Fatalf("expected commentary track to be ignored for dual-audio prefix, got %q", audio)
	}
	if channels != "7.1" || !commentary {
		t.Fatalf("expected 7.1 with commentary detected, got channels=%q commentary=%t", channels, commentary)
	}
}

func TestAudioFromMediaSkipsCompatibilityTitleStringForPrimaryAudio(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Channels":"2","ChannelLayout":"L R","StreamOrder":"0","Title_String":"Compatibility Track"},{"@type":"Audio","Format":"MLP FBA","Format_AdditionalFeatures":"16-ch","Channels":"8","ChannelLayout":"L R C LFE Ls Rs Lb Rb","StreamOrder":"1"}]}}`)

	audio, channels, commentary := audioFromMedia(api.PreparedMetadata{}, doc, nil)
	if audio != "TrueHD 7.1 Atmos" {
		t.Fatalf("expected compatibility Title_String track to be ignored for primary audio, got %q", audio)
	}
	if channels != "7.1" || commentary {
		t.Fatalf("expected 7.1 with no commentary, got channels=%q commentary=%t", channels, commentary)
	}
}

func TestAudioFromMediaSkipsCommentaryTitleStringForPrimaryAudio(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Channels":"2","ChannelLayout":"L R","StreamOrder":"0","Title_String":"Director Commentary"},{"@type":"Audio","Format":"MLP FBA","Format_AdditionalFeatures":"16-ch","Channels":"8","ChannelLayout":"L R C LFE Ls Rs Lb Rb","StreamOrder":"1"}]}}`)

	audio, channels, commentary := audioFromMedia(api.PreparedMetadata{}, doc, nil)
	if audio != "TrueHD 7.1 Atmos" {
		t.Fatalf("expected commentary Title_String track to be ignored for primary audio, got %q", audio)
	}
	if channels != "7.1" || !commentary {
		t.Fatalf("expected 7.1 with commentary detected, got channels=%q commentary=%t", channels, commentary)
	}
}

func TestAudioFromMediaDetectsAuro3DFromPrimaryAudioTitle(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Channels":"2","ChannelLayout":"L R","StreamOrder":"0","Title_String":"Compatibility Track"},{"@type":"Audio","Format":"DTS","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","StreamOrder":"1","Title_String":"Auro3D"}]}}`)

	audio, channels, commentary := audioFromMedia(api.PreparedMetadata{}, doc, nil)
	if audio != "DTS 5.1 Auro3D" {
		t.Fatalf("expected selected primary audio title to drive Auro3D marker, got %q", audio)
	}
	if channels != "5.1" || commentary {
		t.Fatalf("expected 5.1 with no commentary, got channels=%q commentary=%t", channels, commentary)
	}
}

func TestAudioFromMediaAddsDubbedWhenOnlyEnglishTrackPresent(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Language":"en","StreamOrder":"1"}]}}`)
	meta := api.PreparedMetadata{
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	audio, _, _ := audioFromMedia(meta, doc, nil)
	if audio != "Dubbed DD 5.1" {
		t.Fatalf("expected Dubbed DD 5.1, got %q", audio)
	}
}

func TestAudioFromMediaSkipsLanguagePrefixForDiscs(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Language":"en","StreamOrder":"1"},{"@type":"Audio","Format":"AC-3","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","Language":"ja","StreamOrder":"2"}]}}`)
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}
	audio, _, _ := audioFromMedia(meta, doc, nil)
	if audio != "DD 5.1" {
		t.Fatalf("expected disc audio to skip Dual-Audio prefix, got %q", audio)
	}
}

func TestApplyAudioLanguagePrefixFiltersCommentaryAndCompatibilityEntries(t *testing.T) {
	meta := api.PreparedMetadata{
		AudioLanguages: []string{"English Commentary", "Compatibility Track", "Japanese"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}

	got := applyAudioLanguagePrefix("DD 5.1", meta)
	if got != "DD 5.1" {
		t.Fatalf("expected commentary and compatibility entries to be ignored, got %q", got)
	}
}

func TestAudioFromMediaAddsEXFormatSetting(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Format_Settings":"Dolby Surround EX","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","StreamOrder":"1"}]}}`)
	audio, _, _ := audioFromMedia(api.PreparedMetadata{}, doc, nil)
	if audio != "DD EX 5.1" {
		t.Fatalf("expected DD EX 5.1, got %q", audio)
	}
}

func TestAudioFromMediaUsesCodecIDWhenFormatIsGenericAudio(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"Audio","CodecID":"A_VORBIS","Channels":"2","StreamOrder":"1"}]}}`)
	audio, channels, _ := audioFromMedia(api.PreparedMetadata{}, doc, nil)
	if audio != "VORBIS 2.0" {
		t.Fatalf("expected VORBIS 2.0, got %q", audio)
	}
	if channels != "2.0" {
		t.Fatalf("expected 2.0 channels, got %q", channels)
	}
}

func TestAudioFromMediaKeepsGenericFormatWhenCodecIDUnknown(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"Audio","CodecID":"A_UNKNOWN","Channels":"2","StreamOrder":"1"}]}}`)
	audio, channels, _ := audioFromMedia(api.PreparedMetadata{}, doc, nil)
	if audio != "Audio 2.0" {
		t.Fatalf("expected Audio 2.0, got %q", audio)
	}
	if channels != "2.0" {
		t.Fatalf("expected 2.0 channels, got %q", channels)
	}
}

func TestRemoveTrackerBlockReasonDoesNotMutateInput(t *testing.T) {
	original := []api.TrackerBlockReason{api.TrackerBlockReasonAudio, api.TrackerBlockReasonClaim}
	blocked := map[string][]api.TrackerBlockReason{
		"AITHER": append([]api.TrackerBlockReason{}, original...),
	}

	filtered := removeTrackerBlockReason(blocked, api.TrackerBlockReasonAudio)

	if got := blocked["AITHER"]; len(got) != len(original) || got[0] != original[0] || got[1] != original[1] {
		t.Fatalf("expected input blocked map to remain unchanged, got %#v", blocked)
	}
	if got := filtered["AITHER"]; len(got) != 1 || got[0] != api.TrackerBlockReasonClaim {
		t.Fatalf("expected filtered map to keep only claim block, got %#v", filtered)
	}
}

func TestRefreshPreparedMetadataPreservesNonRequestScopedFailures(t *testing.T) {
	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	meta := api.PreparedMetadata{
		BlockedTrackers: map[string][]api.TrackerBlockReason{
			"AITHER": {api.TrackerBlockReasonAudio, api.TrackerBlockReasonClaim},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"AITHER": {
				{Rule: "audio_bloat", Reason: "audio languages French may be considered bloated"},
				{Rule: trackerClaimRuleActive, Reason: "AITHER has an active claim for this release"},
				{Rule: "language_rule", Reason: "missing original language coverage"},
			},
			"ANT": {
				{Rule: "require_movie_only", Reason: "category tv is not movie"},
			},
		},
	}

	refreshed, err := svc.RefreshPreparedMetadata(context.Background(), meta)
	if err != nil {
		t.Fatalf("refresh prepared metadata: %v", err)
	}
	if refreshed.BlockedTrackers != nil {
		t.Fatalf("expected request-scoped tracker blocks cleared, got %#v", refreshed.BlockedTrackers)
	}
	if failures := refreshed.TrackerRuleFailures["AITHER"]; len(failures) != 1 || failures[0].Rule != "language_rule" {
		t.Fatalf("expected only non-request-scoped AITHER failure preserved, got %#v", refreshed.TrackerRuleFailures)
	}
	if failures := refreshed.TrackerRuleFailures["ANT"]; len(failures) != 1 || failures[0].Rule != "require_movie_only" {
		t.Fatalf("expected unrelated ANT failure preserved, got %#v", refreshed.TrackerRuleFailures)
	}
	for tracker, failures := range refreshed.TrackerRuleFailures {
		for _, failure := range failures {
			if failure.Rule == "audio_bloat" || failure.Rule == trackerClaimRuleActive {
				t.Fatalf("did not expect request-scoped failure %q for %s after refresh: %#v", failure.Rule, tracker, refreshed.TrackerRuleFailures)
			}
		}
	}
}

func TestRefreshPreparedMetadataRefreshesFilenameHDRFromCurrentSourcePath(t *testing.T) {
	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	meta := api.PreparedMetadata{
		SourcePath: "Movie.2026.DV.HDR10+.2160p.WEB-DL.H.265-GRP.mkv",
		Source:     "Web",
		Type:       "WEBDL",
		VideoCodec: "H.265",
		HDR:        "HDR",
		Release: api.ReleaseInfo{
			Title:      "Movie",
			Year:       2026,
			Resolution: "2160p",
			HDR:        []string{"HDR"},
		},
	}

	refreshed, err := svc.RefreshPreparedMetadata(context.Background(), meta)
	if err != nil {
		t.Fatalf("refresh prepared metadata: %v", err)
	}
	if refreshed.HDR != "DV HDR10+" {
		t.Fatalf("expected current source path HDR, got %q", refreshed.HDR)
	}
	if !strings.Contains(refreshed.ReleaseName, "DV HDR10+") {
		t.Fatalf("expected refreshed release name to include current source path HDR, got %q", refreshed.ReleaseName)
	}
}

func TestRefreshPreparedMetadataKeepsDistinctMediaHDR(t *testing.T) {
	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	meta := api.PreparedMetadata{
		SourcePath: "Movie.2026.DV.2160p.WEB-DL.H.265-GRP.mkv",
		Source:     "Web",
		Type:       "WEBDL",
		VideoCodec: "H.265",
		HDR:        "HDR10+",
		Release: api.ReleaseInfo{
			Title:      "Movie",
			Year:       2026,
			Resolution: "2160p",
			HDR:        []string{"HDR"},
		},
	}

	refreshed, err := svc.RefreshPreparedMetadata(context.Background(), meta)
	if err != nil {
		t.Fatalf("refresh prepared metadata: %v", err)
	}
	if refreshed.HDR != "HDR10+" {
		t.Fatalf("expected distinct media HDR to remain, got %q", refreshed.HDR)
	}
}

func TestRefreshPreparedMetadataClearsResolvedRuleFailures(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, WithConfig(config.Config{}))
	meta := api.PreparedMetadata{
		SourcePath: "/media/example.mkv",
		Trackers:   []string{"ANT"},
		ExternalIDs: api.ExternalIDs{
			Category: "MOVIE",
			TMDBID:   1,
		},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{TMDBID: 1, Title: "Example Release"},
		},
		TrackerRuleFailures: map[string][]api.RuleFailure{
			"ANT": {
				{Rule: "require_movie_only", Reason: "category tv is not movie"},
			},
			"BTN": {
				{Rule: "language_rule", Reason: "missing original language coverage"},
			},
		},
	}

	refreshed, err := svc.RefreshPreparedMetadata(context.Background(), meta)
	if err != nil {
		t.Fatalf("refresh prepared metadata: %v", err)
	}
	if failures := refreshed.TrackerRuleFailures["ANT"]; len(failures) != 0 {
		t.Fatalf("expected resolved ANT rule failure to be cleared, got %#v", failures)
	}
	if failures := refreshed.TrackerRuleFailures["BTN"]; len(failures) != 1 || failures[0].Rule != "language_rule" {
		t.Fatalf("expected unevaluated BTN rule failure to remain, got %#v", refreshed.TrackerRuleFailures)
	}
	if len(repo.trackerRuleFailures) != 0 {
		t.Fatalf("expected cleared ANT rule failures to be persisted, got %#v", repo.trackerRuleFailures)
	}
}

func TestRefreshPreparedMetadataKeepsRepoForRulePersistence(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, WithConfig(config.Config{}))
	meta := api.PreparedMetadata{
		SourcePath: "/media/example.mkv",
		Trackers:   []string{"ANT"},
		ExternalIDs: api.ExternalIDs{
			Category: "tv",
		},
	}

	refreshed, err := svc.RefreshPreparedMetadata(context.Background(), meta)
	if err != nil {
		t.Fatalf("refresh prepared metadata: %v", err)
	}
	failures := refreshed.TrackerRuleFailures["ANT"]
	if len(failures) == 0 {
		t.Fatalf("expected refreshed metadata to retain ANT rule failure, got %#v", refreshed.TrackerRuleFailures)
	}
	if len(repo.trackerRuleFailures) == 0 {
		t.Fatalf("expected tracker rule failures to be persisted during refresh")
	}
	if repo.trackerRuleFailures[0].Tracker != "ANT" || repo.trackerRuleFailures[0].Rule != "require_movie_only" {
		t.Fatalf("unexpected persisted tracker rule failures: %#v", repo.trackerRuleFailures)
	}
}

func TestRefreshPreparedMetadataNormalizesStaleMISettingsForNonEncodes(t *testing.T) {
	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	cases := []struct {
		name       string
		meta       api.PreparedMetadata
		wantBlock  bool
		wantNormal bool
	}{
		{
			name: "remux not blocked",
			meta: api.PreparedMetadata{
				Type:                   "REMUX",
				ValidMediaInfoSettings: false,
			},
			wantNormal: true,
		},
		{
			name: "bdmv not blocked",
			meta: api.PreparedMetadata{
				Type:                   "ENCODE",
				DiscType:               "BDMV",
				ValidMediaInfoSettings: false,
			},
			wantNormal: true,
		},
		{
			name: "av1 encode not blocked",
			meta: api.PreparedMetadata{
				Type:                   "ENCODE",
				VideoCodec:             "AV1",
				ValidMediaInfoSettings: false,
			},
			wantNormal: true,
		},
		{
			name: "genuine encode stays blocked",
			meta: api.PreparedMetadata{
				Type:                   "ENCODE",
				VideoCodec:             "H.265",
				ValidMediaInfoSettings: false,
			},
			wantBlock: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := tc.meta
			meta.SourcePath = "/media/example.mkv"
			meta.Trackers = []string{"AITHER"}
			refreshed, err := svc.RefreshPreparedMetadata(context.Background(), meta)
			if err != nil {
				t.Fatalf("refresh prepared metadata: %v", err)
			}
			if tc.wantNormal && !refreshed.ValidMediaInfoSettings {
				t.Fatalf("expected ValidMediaInfoSettings normalized to true")
			}
			blocked := false
			for _, failure := range refreshed.TrackerRuleFailures["AITHER"] {
				if failure.Rule == "require_valid_mi_setting" {
					blocked = true
				}
			}
			if blocked != tc.wantBlock {
				t.Fatalf("require_valid_mi_setting blocked=%t, want %t (failures=%#v)", blocked, tc.wantBlock, refreshed.TrackerRuleFailures["AITHER"])
			}
		})
	}
}

func TestAudioFromMediaUsesChannelsOriginalWhenPresent(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Channels":"8 / 6","Channels_Original":"6","ChannelLayout":"L R C LFE Ls Rs","StreamOrder":"1"}]}}`)
	audio, channels, _ := audioFromMedia(api.PreparedMetadata{}, doc, nil)
	if audio != "DD 5.1" {
		t.Fatalf("expected DD 5.1, got %q", audio)
	}
	if channels != "5.1" {
		t.Fatalf("expected 5.1 channels, got %q", channels)
	}
}

func TestAudioFromMediaNormalizesBDInfoCodec(t *testing.T) {
	audio, channels, commentary := audioFromMedia(api.PreparedMetadata{}, mediaInfoDoc{}, &discparse.BDInfo{
		Audio: []discparse.BDAudio{{
			Codec:    "Dolby TrueHD Audio",
			Channels: "5.1",
		}},
	})

	if audio != "TrueHD 5.1" {
		t.Fatalf("expected normalized BDInfo audio to be TrueHD 5.1, got %q", audio)
	}
	if channels != "5.1" || commentary {
		t.Fatalf("expected channels=5.1 commentary=false, got channels=%q commentary=%t", channels, commentary)
	}
}

func TestAudioFromMediaNormalizesBDInfoCodecWithAtmos(t *testing.T) {
	audio, channels, commentary := audioFromMedia(api.PreparedMetadata{}, mediaInfoDoc{}, &discparse.BDInfo{
		Audio: []discparse.BDAudio{{
			Codec:    "Dolby TrueHD Audio",
			Channels: "7.1",
			Atmos:    "Yes",
		}},
	})

	if audio != "TrueHD 7.1 Atmos" {
		t.Fatalf("expected normalized BDInfo audio to be TrueHD 7.1 Atmos, got %q", audio)
	}
	if channels != "7.1" || commentary {
		t.Fatalf("expected channels=7.1 commentary=false, got channels=%q commentary=%t", channels, commentary)
	}
}

func TestResolveAudioBloatPolicyBlocksStrictTrackersForEnglishOriginal(t *testing.T) {
	blocked, warned := resolveAudioBloatPolicy(api.PreparedMetadata{
		AudioLanguages: []string{"English", "French"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "en"},
		},
	}, []string{"ANT", "BHD", "MTV", "AITHER", "ASC"})

	if got := blocked["ANT"]; len(got) != 1 || got[0] != "French" {
		t.Fatalf("expected ANT blocked for French bloat, got %#v", blocked)
	}
	if got := blocked["BHD"]; len(got) != 1 || got[0] != "French" {
		t.Fatalf("expected BHD blocked for French bloat, got %#v", blocked)
	}
	if got := blocked["MTV"]; len(got) != 1 || got[0] != "French" {
		t.Fatalf("expected MTV blocked for French bloat, got %#v", blocked)
	}
	if got := warned["AITHER"]; len(got) != 1 || got[0] != "French" {
		t.Fatalf("expected AITHER warning for French bloat, got %#v", warned)
	}
	if _, ok := warned["ASC"]; ok {
		t.Fatalf("did not expect ASC warning, got %#v", warned)
	}
}

func TestResolveAudioBloatPolicyWarnsButDoesNotBlockNonEnglishOriginal(t *testing.T) {
	blocked, warned := resolveAudioBloatPolicy(api.PreparedMetadata{
		AudioLanguages: []string{"English", "Japanese", "French"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}, []string{"ANT", "BHD", "SPD"})

	if blocked != nil {
		t.Fatalf("expected no blocked trackers, got %#v", blocked)
	}
	if got := warned["ANT"]; len(got) != 1 || got[0] != "French" {
		t.Fatalf("expected ANT warning for French bloat, got %#v", warned)
	}
	if got := warned["BHD"]; len(got) != 1 || got[0] != "French" {
		t.Fatalf("expected BHD warning for French bloat, got %#v", warned)
	}
	if got := warned["SPD"]; len(got) != 1 || got[0] != "French" {
		t.Fatalf("expected SPD warning for French bloat, got %#v", warned)
	}
}

func TestResolveAudioBloatPolicyExemptsUTPUkrainian(t *testing.T) {
	// UTP always carries Ukrainian + original + optional English; none of those are bloat.
	blocked, warned := resolveAudioBloatPolicy(api.PreparedMetadata{
		AudioLanguages: []string{"Ukrainian", "English", "French"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "fr"},
		},
	}, []string{"UTP"})

	if blocked != nil {
		t.Fatalf("expected no blocked trackers for UTP, got %#v", blocked)
	}
	if warned != nil {
		t.Fatalf("expected no bloat warnings for UTP (uk+en+original exempt), got %#v", warned)
	}
}
