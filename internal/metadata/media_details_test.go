// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"testing"

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
		_, typeValue := sourceAndType(api.PreparedMetadata{
			DiscType:   discType,
			SourcePath: "/media/disc",
			Release:    api.ReleaseInfo{},
		}, mediaInfoDoc{})
		if typeValue != "DISC" {
			t.Fatalf("disc type %q should default to DISC, got %q", discType, typeValue)
		}
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

func TestAudioFromMediaAddsEXFormatSetting(t *testing.T) {
	doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"},{"@type":"Audio","Format":"AC-3","Format_Settings":"Dolby Surround EX","Channels":"6","ChannelLayout":"L R C LFE Ls Rs","StreamOrder":"1"}]}}`)
	audio, _, _ := audioFromMedia(api.PreparedMetadata{}, doc, nil)
	if audio != "DD EX 5.1" {
		t.Fatalf("expected DD EX 5.1, got %q", audio)
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
