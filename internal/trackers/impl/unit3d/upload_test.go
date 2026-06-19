// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveUnit3DCategory(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "movie"}}
	if got := resolveUnit3DCategory(meta); got != "MOVIE" {
		t.Fatalf("expected MOVIE, got %q", got)
	}
	meta.ExternalIDs.Category = "TV"
	if got := resolveUnit3DCategory(meta); got != "TV" {
		t.Fatalf("expected TV, got %q", got)
	}
}

func TestBuildAitherNameDVDRip(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName: "Movie.2020.DVD.DVDRIP.AAC.XVID-GRP",
		Release:     api.ReleaseInfo{Year: 2020, Resolution: "480p"},
		Type:        "DVDRIP",
		Source:      "DVD",
		Audio:       "AAC 2.0",
		VideoEncode: "XVID",
	}
	name := BuildAitherName(meta)
	if name == "" {
		t.Fatalf("expected aither name")
	}
	if name == meta.ReleaseName {
		t.Fatalf("expected name to be adjusted, got %q", name)
	}
}

func TestBuildUnit3DDataMovieOmitsTVOnlyFields(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.1080p.WEB-DL.DD5.1.H264-GRP",
			ExternalIDs: api.ExternalIDs{
				Category: "MOVIE",
				TMDBID:   123,
				IMDBID:   456,
				TVDBID:   789,
			},
			Type: "WEBDL",
			Release: api.ReleaseInfo{
				Resolution: "1080p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if _, ok := data["season_number"]; ok {
		t.Fatalf("season_number should be omitted for movie payload")
	}
	if _, ok := data["episode_number"]; ok {
		t.Fatalf("episode_number should be omitted for movie payload")
	}
	if _, ok := data["tvdb"]; ok {
		t.Fatalf("tvdb should be omitted for movie payload")
	}
}

func TestBuildUnit3DDataAitherHDR10PExcludesHDR(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.2160p.WEB-DL.HDR10+.DV.H265-GRP",
			HDR:         "HDR10+ DV",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Type:        "WEBDL",
			Release: api.ReleaseInfo{
				Resolution: "2160p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := data["hdr10p"]; got != "1" {
		t.Fatalf("expected hdr10p=1, got %q", got)
	}
	if _, ok := data["hdr"]; ok {
		t.Fatalf("did not expect hdr when hdr10p is set")
	}
	if got := data["dv"]; got != "1" {
		t.Fatalf("expected dv=1, got %q", got)
	}
}

func TestBuildUnit3DDataAitherHDRWithoutHDR10P(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.2160p.WEB-DL.HDR.H265-GRP",
			HDR:         "HDR",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Type:        "WEBDL",
			Release: api.ReleaseInfo{
				Resolution: "2160p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := data["hdr"]; got != "1" {
		t.Fatalf("expected hdr=1, got %q", got)
	}
	if _, ok := data["hdr10p"]; ok {
		t.Fatalf("did not expect hdr10p for plain HDR")
	}
}

func TestBuildUnit3DDataTVIncludesTVOnlyFields(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Show.S02E03.1080p.WEB-DL.DD5.1.H264-GRP",
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
				TMDBID:   123,
				IMDBID:   456,
				TVDBID:   789,
			},
			Type:       "WEBDL",
			SeasonInt:  2,
			EpisodeInt: 3,
			Release: api.ReleaseInfo{
				Resolution: "1080p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := data["season_number"]; got != "2" {
		t.Fatalf("expected season_number=2, got %q", got)
	}
	if got := data["episode_number"]; got != "3" {
		t.Fatalf("expected episode_number=3, got %q", got)
	}
	if got := data["tvdb"]; got != "789" {
		t.Fatalf("expected tvdb=789, got %q", got)
	}
}

func TestBuildUnit3DDataFailsOnUnknownType(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.1080p.UNKNOWN-GRP",
			ExternalIDs: api.ExternalIDs{
				Category: "MOVIE",
			},
			Type: "",
			Release: api.ReleaseInfo{
				Type:       "",
				Resolution: "1080p",
			},
		},
	}

	_, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err == nil {
		t.Fatalf("expected unresolved type_id error")
	}
	if !strings.Contains(err.Error(), "unsupported type value") {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func TestResolveUnit3DTypeIDInfersWEBDLFromSourceWhenReleaseTypeIsMovie(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:   "movie",
		Source: "WEB-DL",
		Release: api.ReleaseInfo{
			Type:   "movie",
			Source: "WEB-DL",
		},
	}

	got, err := resolveUnit3DTypeID(meta)
	if err != nil {
		t.Fatalf("expected type id, got error: %v", err)
	}
	if got != "4" {
		t.Fatalf("expected WEBDL type_id=4, got %q", got)
	}
}

func TestResolveUnit3DTypeIDInfersEncodeFromBluraySourceWhenReleaseTypeIsMovie(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:   "movie",
		Source: "BluRay",
		Release: api.ReleaseInfo{
			Type:   "movie",
			Source: "BluRay",
		},
	}

	got, err := resolveUnit3DTypeID(meta)
	if err != nil {
		t.Fatalf("expected type id, got error: %v", err)
	}
	if got != "3" {
		t.Fatalf("expected ENCODE type_id=3, got %q", got)
	}
}

func TestResolveUnit3DIDsUseSharedTrackerdataMappings(t *testing.T) {
	meta := api.PreparedMetadata{
		Type: "WEB-DL",
		Release: api.ReleaseInfo{
			Resolution: "1080P",
		},
	}

	typeID, err := resolveUnit3DTypeID(meta)
	if err != nil {
		t.Fatalf("expected type id, got error: %v", err)
	}
	if typeID != "4" {
		t.Fatalf("expected WEBDL type_id=4, got %q", typeID)
	}

	if got := resolveUnit3DResolutionID(meta); got != "3" {
		t.Fatalf("expected 1080P resolution_id=3, got %q", got)
	}
}

func TestBuildUnit3DDataSkipsTVFieldsWhenMovieSignalsExist(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Watcher.2022.2160p.WEB-DL.DDP5.1.H.265-FLUX",
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
				TMDBID:   807356,
				IMDBID:   12004038,
			},
			MediaInfoCategory: "movie",
			Type:              "movie",
			Source:            "WEB-DL",
			Release: api.ReleaseInfo{
				Type:       "movie",
				Source:     "WEB-DL",
				Resolution: "2160p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := data["type_id"]; got != "4" {
		t.Fatalf("expected type_id=4 for WEBDL, got %q", got)
	}
	if _, ok := data["season_number"]; ok {
		t.Fatalf("season_number should be omitted when movie signals are explicit")
	}
	if _, ok := data["episode_number"]; ok {
		t.Fatalf("episode_number should be omitted when movie signals are explicit")
	}
	if _, ok := data["tvdb"]; ok {
		t.Fatalf("tvdb should be omitted when movie signals are explicit")
	}
}

func TestParseUnit3DUploadArtifactDownloadURL(t *testing.T) {
	t.Parallel()

	artifact := parseUnit3DUploadArtifact("https://aither.cc", "https://aither.cc/torrent/download/374352.382")
	if artifact.TorrentID != "374352" {
		t.Fatalf("expected torrent ID 374352, got %q", artifact.TorrentID)
	}
	if artifact.DownloadURL != "https://aither.cc/torrent/download/374352.382" {
		t.Fatalf("unexpected download URL: %q", artifact.DownloadURL)
	}
	if artifact.TorrentURL != "https://aither.cc/torrents/374352" {
		t.Fatalf("unexpected torrent URL: %q", artifact.TorrentURL)
	}
}

func TestParseUnit3DUploadArtifactNumericID(t *testing.T) {
	t.Parallel()

	artifact := parseUnit3DUploadArtifact("https://aither.cc", "374352")
	if artifact.TorrentID != "374352" {
		t.Fatalf("expected torrent ID 374352, got %q", artifact.TorrentID)
	}
	if artifact.DownloadURL != "https://aither.cc/torrent/download/374352" {
		t.Fatalf("unexpected download URL: %q", artifact.DownloadURL)
	}
	if artifact.TorrentURL != "https://aither.cc/torrents/374352" {
		t.Fatalf("unexpected torrent URL: %q", artifact.TorrentURL)
	}
}

func TestResolveUnit3DTypeIDForTrackerOE(t *testing.T) {
	meta := api.PreparedMetadata{Type: "WEBRIP", VideoCodec: "HEVC"}
	got, err := resolveUnit3DTypeIDForTracker("OE", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "10" {
		t.Fatalf("expected OE WEBRIP HEVC type_id=10, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerOEFallsBackToOtherUnknown(t *testing.T) {
	meta := api.PreparedMetadata{Type: "WEBRIP", VideoCodec: "VP9"}
	got, err := resolveUnit3DTypeIDForTracker("OE", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "16" {
		t.Fatalf("expected OE WEBRIP unknown codec type_id=16, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerOTWDVD(t *testing.T) {
	meta := api.PreparedMetadata{DiscType: "DVD", Type: "REMUX"}
	got, err := resolveUnit3DTypeIDForTracker("OTW", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "7" {
		t.Fatalf("expected OTW DVD type_id=7, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerRF(t *testing.T) {
	meta := api.PreparedMetadata{Type: "ENCODE"}
	got, err := resolveUnit3DTypeIDForTracker("RF", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "41" {
		t.Fatalf("expected RF ENCODE type_id=41, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerYUS(t *testing.T) {
	meta := api.PreparedMetadata{Type: "DISC"}
	got, err := resolveUnit3DTypeIDForTracker("YUS", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "17" {
		t.Fatalf("expected YUS DISC type_id=17, got %q", got)
	}
}

func TestResolveUnit3DResolutionIDForTrackerRF(t *testing.T) {
	meta := api.PreparedMetadata{Release: api.ReleaseInfo{Resolution: "1440p"}}
	if got := resolveUnit3DResolutionIDForTracker("RF", meta); got != "10" {
		t.Fatalf("expected RF 1440p to fall back to 10, got %q", got)
	}
}

func TestBuildUnit3DDataLSTAdditionalFields(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "LST",
		TrackerConfig: config.TrackerConfig{
			Draft: true,
		},
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.1080p.WEB-DL.DD5.1.H264-GRP",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Type:        "WEBDL",
			Edition:     "Director's Cut",
			Release: api.ReleaseInfo{
				Resolution: "1080p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := data["draft_queue_opt_in"]; got != "1" {
		t.Fatalf("expected draft_queue_opt_in=1, got %q", got)
	}
	if got := data["edition_id"]; got != "2" {
		t.Fatalf("expected edition_id=2, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerA4K(t *testing.T) {
	meta := api.PreparedMetadata{Type: "WEBRIP"}
	_, err := resolveUnit3DTypeIDForTracker("A4K", meta)
	if err == nil {
		t.Fatalf("expected unsupported type for A4K WEBRIP")
	}
}

func TestResolveUnit3DTypeIDForTrackerPT(t *testing.T) {
	meta := api.PreparedMetadata{Type: "WEBRIP"}
	got, err := resolveUnit3DTypeIDForTracker("PT", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "39" {
		t.Fatalf("expected PT WEBRIP type_id=39, got %q", got)
	}
}

func TestBuildUnit3DDataPTAdditionalFields(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "PT",
		Meta: api.PreparedMetadata{
			ReleaseName:       "Movie.2025.1080p.WEB-DL.DD5.1.H264-GRP",
			ExternalIDs:       api.ExternalIDs{Category: "MOVIE"},
			Type:              "WEBDL",
			AudioLanguages:    []string{"Portuguese"},
			SubtitleLanguages: []string{"PT-BR", "English"},
			Release: api.ReleaseInfo{
				Resolution: "1080p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := data["audio_pt"]; got != "1" {
		t.Fatalf("expected audio_pt=1, got %q", got)
	}
	if got := data["legenda_pt"]; got != "0" {
		t.Fatalf("expected legenda_pt=0 when only BR Portuguese subtitles are present, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerSTCPack(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:   "WEBDL",
		TVPack: true,
		Release: api.ReleaseInfo{
			Resolution: "1080p",
		},
	}
	got, err := resolveUnit3DTypeIDForTracker("STC", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "13" {
		t.Fatalf("expected STC HD web pack type_id=13, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerTLZ(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "TV"}, TVPack: true}
	got, err := resolveUnit3DTypeIDForTracker("TLZ", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "4" {
		t.Fatalf("expected TLZ TV pack type_id=4, got %q", got)
	}
}

func TestResolveUnit3DCategoryIDForTrackerTOS(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "TV"}, TVPack: true, Tag: "-vostfr"}
	if got := resolveUnit3DCategoryIDForTracker("TOS", meta); got != "9" {
		t.Fatalf("expected TOS vostfr TV pack category_id=9, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerBLU(t *testing.T) {
	meta := api.PreparedMetadata{Type: "ENCODE"}
	got, err := resolveUnit3DTypeIDForTracker("BLU", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "12" {
		t.Fatalf("expected BLU ENCODE type_id=12, got %q", got)
	}
}

func TestResolveUnit3DResolutionIDForTrackerBLU(t *testing.T) {
	meta := api.PreparedMetadata{Release: api.ReleaseInfo{Resolution: "2160p"}}
	if got := resolveUnit3DResolutionIDForTracker("BLU", meta); got != "1" {
		t.Fatalf("expected BLU 2160p resolution_id=1, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerITTFromName(t *testing.T) {
	meta := api.PreparedMetadata{ReleaseName: "Movie.2025.1080p.DLMux-GRP", Type: "WEBDL"}
	got, err := resolveUnit3DTypeIDForTracker("ITT", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "27" {
		t.Fatalf("expected ITT DLMux type_id=27, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerSHRI(t *testing.T) {
	meta := api.PreparedMetadata{Type: "REMUX"}
	got, err := resolveUnit3DTypeIDForTracker("SHRI", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "7" {
		t.Fatalf("expected SHRI REMUX type_id=7, got %q", got)
	}
}

func TestResolveUnit3DCategoryIDForTrackerIHDAnimeMovie(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "MOVIE"}, Anime: true}
	if got := resolveUnit3DCategoryIDForTracker("IHD", meta); got != "4" {
		t.Fatalf("expected IHD anime movie category_id=4, got %q", got)
	}
}

func TestResolveUnit3DCategoryIDForTrackerR4EDocumentaryTV(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "TV"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{GenreIDs: "99,18"},
		},
	}
	if got := resolveUnit3DCategoryIDForTracker("R4E", meta); got != "2" {
		t.Fatalf("expected R4E documentary tv category_id=2, got %q", got)
	}
}

func TestResolveUnit3DResolutionIDForTrackerUTPDefaultOther(t *testing.T) {
	meta := api.PreparedMetadata{Release: api.ReleaseInfo{Resolution: "720p"}}
	if got := resolveUnit3DResolutionIDForTracker("UTP", meta); got != "11" {
		t.Fatalf("expected UTP 720p resolution fallback=11, got %q", got)
	}
}

func TestBuildUnit3DDataOmitsLegacyModQAliasForA4K(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "A4K",
		TrackerConfig: config.TrackerConfig{
			ModQ: true,
		},
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.2160p.WEB-DL.DD5.1.H264-GRP",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Type:        "WEBDL",
			Release: api.ReleaseInfo{
				Resolution: "2160p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, ok := data["modq"]; ok {
		t.Fatalf("did not expect legacy modq alias for A4K")
	}
}

func TestResolveUnit3DTypeIDForTrackerTIKDiscMarker(t *testing.T) {
	meta := api.PreparedMetadata{ReleaseName: "Movie.2025.BD50.COMPLETE-GRP", DiscType: "BDMV"}
	got, err := resolveUnit3DTypeIDForTracker("TIK", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "5" {
		t.Fatalf("expected TIK BD50 type_id=5, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerTIKNonDiscFails(t *testing.T) {
	meta := api.PreparedMetadata{Type: "WEBDL", DiscType: ""}
	_, err := resolveUnit3DTypeIDForTracker("TIK", meta)
	if err == nil {
		t.Fatalf("expected error for TIK when disc type cannot be resolved")
	}
}

func TestResolveUnit3DCategoryIDForTrackerLDUAnimeTV(t *testing.T) {
	meta := api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "TV"}, Anime: true}
	if got := resolveUnit3DCategoryIDForTracker("LDU", meta); got != "9" {
		t.Fatalf("expected LDU anime tv category_id=9, got %q", got)
	}
}

func TestResolveUnit3DCategoryIDForTrackerLDUNonEnglishMovie(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs:       api.ExternalIDs{Category: "MOVIE"},
		AudioLanguages:    []string{"Portuguese"},
		SubtitleLanguages: []string{"Portuguese"},
	}
	if got := resolveUnit3DCategoryIDForTracker("LDU", meta); got != "22" {
		t.Fatalf("expected LDU non-english movie category_id=22, got %q", got)
	}
}

func TestBuildUnit3DNameLDUUsesFirstParseableLanguages(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName:       "Movie.2025.1080p.WEB-DL.DD5.1.H264-GRP",
		ExternalIDs:       api.ExternalIDs{Category: "MOVIE"},
		AudioLanguages:    []string{"", "Japanese", "English"},
		SubtitleLanguages: []string{"", "English"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "ja"},
		},
	}

	got := buildUnit3DName("LDU", meta, config.TrackerConfig{})
	if !strings.Contains(got, "[JPN]") {
		t.Fatalf("expected first parseable audio language suffix, got %q", got)
	}
	if !strings.Contains(got, "[Subs ENG]") {
		t.Fatalf("expected first parseable subtitle language suffix, got %q", got)
	}
}

func TestBuildUnit3DDataAddsSHRINumericIDs(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "SHRI",
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.1080p.WEB-DL.DD5.1.H264-GRP",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Type:        "WEBDL",
			Region:      "3",
			Distributor: "42",
			Release: api.ReleaseInfo{
				Resolution: "1080p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := data["region_id"]; got != "3" {
		t.Fatalf("expected SHRI region_id=3, got %q", got)
	}
	if got := data["distributor_id"]; got != "42" {
		t.Fatalf("expected SHRI distributor_id=42, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerACM(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:   "BDMV",
		UHD:        "UHD",
		SourceSize: 60 * (1 << 30),
	}
	got, err := resolveUnit3DTypeIDForTracker("ACM", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "2" {
		t.Fatalf("expected ACM UHD 66 type_id=2, got %q", got)
	}
}

func TestResolveUnit3DResolutionIDForTrackerACM(t *testing.T) {
	meta := api.PreparedMetadata{Release: api.ReleaseInfo{Resolution: "1080i"}}
	if got := resolveUnit3DResolutionIDForTracker("ACM", meta); got != "2" {
		t.Fatalf("expected ACM 1080i resolution_id=2, got %q", got)
	}
}

func TestBuildUnit3DDataACMKeywordsAndNumericIDs(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "ACM",
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.1080p.WEB-DL.DD5.1.H264-GRP",
			ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
			Type:        "WEBDL",
			Region:      "3",
			Distributor: "42",
			ExternalMetadata: api.ExternalMetadata{
				TMDB: &api.TMDBMetadata{Keywords: "one, two words, three,four,five,six,seven,eight,nine,ten,eleven,twelve"},
			},
			Release: api.ReleaseInfo{
				Resolution: "1080p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := data["keywords"]; got != "one, three, four, five, six, seven, eight, nine, ten, eleven" {
		t.Fatalf("unexpected ACM keywords %q", got)
	}
	if got := data["region_id"]; got != "3" {
		t.Fatalf("expected ACM region_id=3, got %q", got)
	}
	if got := data["distributor_id"]; got != "42" {
		t.Fatalf("expected ACM distributor_id=42, got %q", got)
	}
}

func TestBuildUnit3DNameACM(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName: "Movie.2024.1080p.BluRay.REMUX.H.265.DD+ 5.1 Atmos-GRP",
		Audio:       "DD+ 5.1",
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{Title: "Movie", OriginalTitle: "Original Movie"},
		},
		SubtitleLanguages: []string{"Japanese"},
	}
	got := buildUnit3DName("ACM", meta, config.TrackerConfig{})
	if !strings.Contains(got, "Movie / Original Movie") {
		t.Fatalf("expected ACM original title injection, got %q", got)
	}
	if strings.Contains(got, "H.265") || strings.Contains(got, " Atmos") {
		t.Fatalf("expected ACM codec/audio cleanup, got %q", got)
	}
	if !strings.Contains(got, "[Jpn subs only]") {
		t.Fatalf("expected ACM subtitle suffix, got %q", got)
	}
}

func TestBuildUnit3DNameULCXRemovesHybridFromWebDV(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ReleaseName: "Movie 2026 Hybrid 1080p WEB-DL DDP5.1 DV H.265-GRP",
		Type:        "WEBDL",
		Edition:     "Hybrid",
		WebDV:       true,
	}
	got := buildUnit3DName("ULCX", meta, config.TrackerConfig{})
	if strings.Contains(got, "Hybrid") {
		t.Fatalf("expected Hybrid removed for ULCX WEB-DL WebDV, got %q", got)
	}
}

func TestResolveUnit3DCategoryIDForTrackerTIKForeignMovie(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "fr"},
		},
	}
	if got := resolveUnit3DCategoryIDForTracker("TIK", meta); got != "3" {
		t.Fatalf("expected TIK foreign movie category_id=3, got %q", got)
	}
}

func TestEnsureUnit3DDVDVOBDescriptionAppendsForOverride(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:            "DVD",
		DVDVOBMediaInfoText: "VOB_CONTENT",
	}

	result := ensureUnit3DDVDVOBDescription("override description", meta)
	if !strings.Contains(result, "override description") {
		t.Fatalf("expected override description to be kept, got %q", result)
	}
	if !strings.Contains(result, "[spoiler=VOB MediaInfo][code]VOB_CONTENT[/code][/spoiler]") {
		t.Fatalf("expected dvd vob mediainfo block, got %q", result)
	}
}

func TestEnsureUnit3DDVDVOBDescriptionAvoidsDuplicate(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:            "DVD",
		DVDVOBMediaInfoText: "VOB_CONTENT",
	}
	block := descriptionunit3d.DVDVOBMediaInfoBlock(meta)
	description := "override description\n\n" + block

	result := ensureUnit3DDVDVOBDescription(description, meta)
	if strings.Count(result, "[spoiler=VOB MediaInfo][code]") != 1 {
		t.Fatalf("expected single dvd vob mediainfo block, got %q", result)
	}
}

func TestLoadUnit3DMediaUsesIFOTextPathForDVD(t *testing.T) {
	root := t.TempDir()
	miPath := filepath.Join(root, "mediainfo.txt")
	if err := os.WriteFile(miPath, []byte("IFO_MEDIAINFO"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	meta := api.PreparedMetadata{
		DiscType:            "DVD",
		MediaInfoTextPath:   miPath,
		DVDVOBMediaInfoText: "VOB_MEDIAINFO",
	}

	mediainfo, bdinfo, err := loadUnit3DMedia(meta, "", api.NopLogger{})
	if err != nil {
		t.Fatalf("load unit3d media: %v", err)
	}
	if mediainfo != "IFO_MEDIAINFO" {
		t.Fatalf("expected IFO mediainfo from MediaInfoTextPath, got %q", mediainfo)
	}
	if bdinfo != "" {
		t.Fatalf("expected empty bdinfo for DVD fallback path, got %q", bdinfo)
	}
}

func TestResolveUnit3DCategoryIDForTrackerTIKAsianMovie(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs:       api.ExternalIDs{Category: "MOVIE"},
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{OriginalLanguage: "en", OriginCountry: []string{"JP"}},
		},
	}
	if got := resolveUnit3DCategoryIDForTracker("TIK", meta); got != "6" {
		t.Fatalf("expected TIK asian movie category_id=6, got %q", got)
	}
}

func TestResolveUnit3DCategoryIDForTrackerTIKOperaTV(t *testing.T) {
	meta := api.PreparedMetadata{
		ExternalIDs:       api.ExternalIDs{Category: "TV"},
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		Release:           api.ReleaseInfo{Genre: "Opera"},
	}
	if got := resolveUnit3DCategoryIDForTracker("TIK", meta); got != "5" {
		t.Fatalf("expected TIK opera tv category_id=5, got %q", got)
	}
}

func TestResolveUnit3DCategoryIDForTrackerTIKOverrideForeignMovie(t *testing.T) {
	forceForeign := true
	meta := api.PreparedMetadata{
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		TrackerSiteOverrides: api.TrackerSiteOverrides{
			TIK: api.TIKOverrides{
				Foreign: &forceForeign,
			},
		},
	}
	if got := resolveUnit3DCategoryIDForTracker("TIK", meta); got != "3" {
		t.Fatalf("expected TIK foreign override movie category_id=3, got %q", got)
	}
}

func TestResolveUnit3DCategoryIDForTrackerTIKOverrideAsianMovie(t *testing.T) {
	forceAsian := true
	forceForeign := false
	meta := api.PreparedMetadata{
		ExternalIDs:       api.ExternalIDs{Category: "MOVIE"},
		AudioLanguages:    []string{"English"},
		SubtitleLanguages: []string{"English"},
		TrackerSiteOverrides: api.TrackerSiteOverrides{
			TIK: api.TIKOverrides{
				Foreign: &forceForeign,
				Asian:   &forceAsian,
			},
		},
	}
	if got := resolveUnit3DCategoryIDForTracker("TIK", meta); got != "6" {
		t.Fatalf("expected TIK asian override movie category_id=6, got %q", got)
	}
}

func TestResolveUnit3DTypeIDForTrackerTIKOverrideDiscType(t *testing.T) {
	discType := "BD66"
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		TrackerSiteOverrides: api.TrackerSiteOverrides{
			TIK: api.TIKOverrides{
				DiscType: &discType,
			},
		},
	}
	got := resolveUnit3DTIKTypeID(meta)
	if got != "4" {
		t.Fatalf("expected TIK BD66 override type_id=4, got %q", got)
	}
}
