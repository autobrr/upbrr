// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"os"
	"path/filepath"
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

func TestApplyMediaDetailsDoesNotInferHDRFromName(t *testing.T) {
	miPath := filepath.Join(t.TempDir(), "mediainfo.json")
	if err := os.WriteFile(miPath, []byte(`{"media":{"track":[{"@type":"General"},{"@type":"Video","Format":"HEVC","Width":"3840","Height":"2160"}]}}`), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	svc := NewService(&fakeRepo{}, WithConfig(config.Config{}))
	meta, err := svc.ApplyMediaDetails(context.Background(), api.PreparedMetadata{
		SourcePath:        "Movie.2026.[DV].HDR10+.HLG.2160p.WEB-DL.H.265-GRP.mkv",
		MediaInfoJSONPath: miPath,
		Release:           api.ReleaseInfo{Resolution: "2160p"},
	})
	if err != nil {
		t.Fatalf("apply media details: %v", err)
	}
	if meta.HDR != "" {
		t.Fatalf("expected HDR to come only from MediaInfo/BDInfo, got %q", meta.HDR)
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

func TestRefreshPreparedMetadataClearsResolvedRuleFailures(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, WithConfig(config.Config{}))
	meta := api.PreparedMetadata{
		SourcePath: "/media/example.mkv",
		Trackers:   []string{"ANT"},
		ExternalIDs: api.ExternalIDs{
			Category: "MOVIE",
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

	if audio != "TrueHD Atmos 7.1" {
		t.Fatalf("expected normalized BDInfo audio to be TrueHD Atmos 7.1, got %q", audio)
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
