// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
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
	tests := []struct {
		name string
		meta api.PreparedMetadata
		want string
	}{
		{
			name: "external movie",
			meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "movie"}},
			want: "MOVIE",
		},
		{
			name: "external tv",
			meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: "TV"}},
			want: "TV",
		},
		{
			name: "external tv alias",
			meta: api.PreparedMetadata{ExternalIDs: api.ExternalIDs{Category: " tv-show "}},
			want: "TV",
		},
		{
			name: "external movie alias",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: " film "},
				Release:     api.ReleaseInfo{Category: "TV"},
			},
			want: "MOVIE",
		},
		{
			name: "external wins over release",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
				Release:     api.ReleaseInfo{Category: "TV"},
			},
			want: "MOVIE",
		},
		{
			name: "mediainfo wins over release",
			meta: api.PreparedMetadata{
				MediaInfoCategory: "movie",
				Release:           api.ReleaseInfo{Category: "episode"},
			},
			want: "MOVIE",
		},
		{
			name: "unknown external uses mediainfo",
			meta: api.PreparedMetadata{
				ExternalIDs:       api.ExternalIDs{Category: "documentary"},
				MediaInfoCategory: "TV",
			},
			want: "TV",
		},
		{
			name: "unknown external uses release",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "documentary"},
				Release:     api.ReleaseInfo{Category: "series"},
			},
			want: "TV",
		},
		{
			name: "release category tv alias",
			meta: api.PreparedMetadata{Release: api.ReleaseInfo{Category: " series "}},
			want: "TV",
		},
		{
			name: "release category movie alias",
			meta: api.PreparedMetadata{
				ReleaseName: "S1m0ne.2002.1080p.WEB-DL-GRP",
				Release:     api.ReleaseInfo{Category: "film"},
			},
			want: "MOVIE",
		},
		{
			name: "structured episode fields",
			meta: api.PreparedMetadata{
				ReleaseName: "Show.1x01.1080p.WEB-DL-GRP",
				SeasonInt:   1,
				EpisodeInt:  1,
			},
			want: "TV",
		},
		{
			name: "unknown mediainfo uses release name fallback",
			meta: api.PreparedMetadata{
				MediaInfoCategory: "documentary",
				ReleaseName:       "Show.S01E01.1080p.WEB-DL-GRP",
			},
			want: "TV",
		},
		{
			name: "whitespace external uses structured episode fields",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: " \t "},
				SeasonInt:   1,
				EpisodeInt:  1,
			},
			want: "TV",
		},
		{
			name: "release name fallback",
			meta: api.PreparedMetadata{ReleaseName: "Show.S01E01.1080p.WEB-DL-GRP"},
			want: "TV",
		},
	}

	for _, tc := range tests {
		got := resolveUnit3DCategory(tc.meta)
		if got != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, got)
		}
	}
}

func TestBuildUnit3DDataUsesParsedCategoryWhenExplicitCategoryUnsupported(t *testing.T) {
	tvReq := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Show.S02E03.Episode.Title.1080p.WEB-DL-GRP",
			ExternalIDs: api.ExternalIDs{
				Category: "documentary",
				TMDBID:   123,
				IMDBID:   456,
				TVDBID:   789,
			},
			Type:       "WEBDL",
			SeasonInt:  2,
			EpisodeInt: 3,
			Release: api.ReleaseInfo{
				Category:   "TV",
				Season:     2,
				Episode:    3,
				Resolution: "1080p",
			},
		},
	}

	tvData, err := buildUnit3DData(tvReq, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected TV payload, got error: %v", err)
	}
	if got := tvData["category_id"]; got != "2" {
		t.Fatalf("expected TV category_id=2, got %q", got)
	}
	if got := tvData["season_number"]; got != "2" {
		t.Fatalf("expected season_number=2, got %q", got)
	}
	if got := tvData["episode_number"]; got != "3" {
		t.Fatalf("expected episode_number=3, got %q", got)
	}
	if got := tvData["tvdb"]; got != "789" {
		t.Fatalf("expected tvdb=789, got %q", got)
	}

	movieReq := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Movie.2025.1080p.WEB-DL-GRP",
			ExternalIDs: api.ExternalIDs{
				Category: "documentary",
				TVDBID:   789,
			},
			Type: "WEBDL",
			Release: api.ReleaseInfo{
				Category:   "MOVIE",
				Resolution: "1080p",
			},
		},
	}

	movieData, err := buildUnit3DData(movieReq, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected movie payload, got error: %v", err)
	}
	if got := movieData["category_id"]; got != "1" {
		t.Fatalf("expected MOVIE category_id=1, got %q", got)
	}
	if _, ok := movieData["season_number"]; ok {
		t.Fatalf("season_number should be omitted for movie payload")
	}
	if _, ok := movieData["episode_number"]; ok {
		t.Fatalf("episode_number should be omitted for movie payload")
	}
	if _, ok := movieData["tvdb"]; ok {
		t.Fatalf("tvdb should be omitted for movie payload")
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

func TestBuildUnit3DDataTVUsesParsedReleaseSeasonEpisodeFallback(t *testing.T) {
	req := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Daily.Show.2025.07.01.1080p.WEB-DL-GRP",
			ExternalIDs: api.ExternalIDs{
				Category: "TV",
				TVDBID:   789,
			},
			Type: "WEBDL",
			Release: api.ReleaseInfo{
				Category:   "TV",
				Season:     2025,
				Episode:    701,
				Resolution: "1080p",
			},
		},
	}

	data, err := buildUnit3DData(req, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := data["season_number"]; got != "2025" {
		t.Fatalf("expected season_number=2025, got %q", got)
	}
	if got := data["episode_number"]; got != "701" {
		t.Fatalf("expected episode_number=701, got %q", got)
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

func TestBuildUnit3DDataUsesParsedCategoryWhenExplicitCategoriesBlank(t *testing.T) {
	tvReq := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "Show.1x01.Episode.Title.1080p.WEB-DL-GRP",
			ExternalIDs: api.ExternalIDs{TVDBID: 789},
			Type:        "WEBDL",
			SeasonInt:   1,
			EpisodeInt:  1,
			Release: api.ReleaseInfo{
				Category:   "TV",
				Season:     1,
				Episode:    1,
				Resolution: "1080p",
			},
		},
	}

	tvData, err := buildUnit3DData(tvReq, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected TV payload, got error: %v", err)
	}
	if got := tvData["category_id"]; got != "2" {
		t.Fatalf("expected TV category_id=2, got %q", got)
	}
	if got := tvData["season_number"]; got != "1" {
		t.Fatalf("expected season_number=1, got %q", got)
	}
	if got := tvData["episode_number"]; got != "1" {
		t.Fatalf("expected episode_number=1, got %q", got)
	}
	if got := tvData["tvdb"]; got != "789" {
		t.Fatalf("expected tvdb=789, got %q", got)
	}

	movieReq := trackers.UploadRequest{
		Tracker: "AITHER",
		Meta: api.PreparedMetadata{
			ReleaseName: "S1m0ne.2002.1080p.WEB-DL-GRP",
			ExternalIDs: api.ExternalIDs{TVDBID: 789},
			Type:        "WEBDL",
			Release: api.ReleaseInfo{
				Category:   "MOVIE",
				Year:       2002,
				Resolution: "1080p",
			},
		},
	}

	movieData, err := buildUnit3DData(movieReq, "name", "desc", "mi", "")
	if err != nil {
		t.Fatalf("expected movie payload, got error: %v", err)
	}
	if got := movieData["category_id"]; got != "1" {
		t.Fatalf("expected MOVIE category_id=1, got %q", got)
	}
	if _, ok := movieData["season_number"]; ok {
		t.Fatalf("season_number should be omitted for parsed movie payload")
	}
	if _, ok := movieData["episode_number"]; ok {
		t.Fatalf("episode_number should be omitted for parsed movie payload")
	}
	if _, ok := movieData["tvdb"]; ok {
		t.Fatalf("tvdb should be omitted for parsed movie payload")
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

func TestResolveUnit3DTypeIDForTrackerZNTH(t *testing.T) {
	meta := api.PreparedMetadata{Type: "DVDRIP"}
	got, err := resolveUnit3DTypeIDForTracker("ZNTH", meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "11" {
		t.Fatalf("expected ZNTH DVDRIP type_id=11, got %q", got)
	}
}

func TestBuildZNTHNameTV(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName:  "Show.S01E01.Episode.Title.1080p.WEB-DL-GRP",
		EpisodeTitle: "Episode Title",
		ExternalIDs:  api.ExternalIDs{Category: "TV"},
		Release:      api.ReleaseInfo{Resolution: "1080p"},
	}
	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "Show.S01E01.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameMovieYearMismatch(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName: "Movie.2024.1080p.WEB-DL-GRP",
		Release:     api.ReleaseInfo{Year: 2024},
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}
	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "Movie.2025.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameBlankCategoryUsesParsedTVCategory(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName:  "Show.1x01.Episode.Title.1080p.WEB-DL-GRP",
		EpisodeTitle: "Episode Title",
		SeasonInt:    1,
		EpisodeInt:   1,
		Release: api.ReleaseInfo{
			Category:   "TV",
			Resolution: "1080p",
		},
	}

	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "Show.1x01.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameUnknownExplicitCategoryUsesParsedTVCategory(t *testing.T) {
	tests := []struct {
		name string
		meta api.PreparedMetadata
	}{
		{
			name: "external unknown",
			meta: api.PreparedMetadata{
				ExternalIDs: api.ExternalIDs{Category: "animation"},
			},
		},
		{
			name: "mediainfo unknown",
			meta: api.PreparedMetadata{
				MediaInfoCategory: "animation",
			},
		},
		{
			name: "external unknown falls through to mediainfo tv",
			meta: api.PreparedMetadata{
				ExternalIDs:       api.ExternalIDs{Category: "animation"},
				MediaInfoCategory: "TV",
			},
		},
	}

	for _, tc := range tests {
		tc.meta.ReleaseName = "Show.S01E01.2024.Episode.Title.1080p.WEB-DL-GRP"
		tc.meta.EpisodeTitle = "Episode Title"
		tc.meta.SeasonInt = 1
		tc.meta.EpisodeInt = 1
		tc.meta.Release = api.ReleaseInfo{
			Category:   "TV",
			Resolution: "1080p",
			Year:       2024,
		}
		tc.meta.ExternalMetadata = api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		}

		got := buildUnit3DName("ZNTH", tc.meta, config.TrackerConfig{})
		expected := "Show.S01E01.2024.1080p.WEB-DL-GRP"
		if got != expected {
			t.Fatalf("%s: expected %q, got %q", tc.name, expected, got)
		}
	}
}

func TestBuildZNTHNameExplicitMoviePreservesMovieBranchOverParsedTV(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName:  "Show.S01E01.2024.Episode.Title.1080p.WEB-DL-GRP",
		EpisodeTitle: "Episode Title",
		ExternalIDs:  api.ExternalIDs{Category: "MOVIE"},
		SeasonInt:    1,
		EpisodeInt:   1,
		Release: api.ReleaseInfo{
			Category:   "TV",
			Resolution: "1080p",
			Year:       2024,
		},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}

	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "Show.S01E01.2025.Episode.Title.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameBlankCategoryUsesParsedMovieCategory(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName: "S1m0ne.2002.1080p.WEB-DL-GRP",
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Year:     2002,
		},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{Year: 2003},
		},
	}

	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "S1m0ne.2003.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameMovieYearMismatchNoResolutionHyphenatedTitle(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName: "Movie - Part One 2024",
		Release:     api.ReleaseInfo{Year: 2024},
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}
	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "Movie - Part One 2025"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameMovieYearMismatchNoResolutionGroupSuffix(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName: "Movie.Title.2024-GRP2024",
		Release:     api.ReleaseInfo{Year: 2024, Group: "GRP2024"},
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}
	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "Movie.Title.2025-GRP2024"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameTVUnicodePrefix(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName:  "\u212aShow.S01E01.Episode.Title.1080p.WEB-DL-GRP",
		EpisodeTitle: "Episode Title",
		ExternalIDs:  api.ExternalIDs{Category: "TV"},
		Release:      api.ReleaseInfo{Resolution: "1080p"},
	}
	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "\u212aShow.S01E01.1080p.WEB-DL-GRP"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestBuildZNTHNameMovieYearMismatchUnicodeTitle(t *testing.T) {
	meta := api.PreparedMetadata{
		ReleaseName: "\u212aMovie.2024",
		Release:     api.ReleaseInfo{Year: 2024},
		ExternalIDs: api.ExternalIDs{Category: "MOVIE"},
		ExternalMetadata: api.ExternalMetadata{
			IMDB: &api.IMDBMetadata{Year: 2025},
		},
	}
	got := buildUnit3DName("ZNTH", meta, config.TrackerConfig{})
	expected := "\u212aMovie.2025"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFindZNTHTokenIndexesUnicodeBoundaries(t *testing.T) {
	got := findZNTHTokenIndexes("Title.\u212a.1080p.Source", "1080p")
	expected := len("Title.\u212a.")
	if len(got) != 1 || got[0] != expected {
		t.Fatalf("expected index %d, got %#v", expected, got)
	}

	if got := findZNTHTokenIndexes("Title.\u06611080p.Source", "1080p"); len(got) != 0 {
		t.Fatalf("expected adjacent Unicode digit prefix to reject token, got %#v", got)
	}
	if got := findZNTHTokenIndexes("Title.1080p\u0661.Source", "1080p"); len(got) != 0 {
		t.Fatalf("expected adjacent Unicode digit suffix to reject token, got %#v", got)
	}
}

func TestZNTHEmptyTokenInputs(t *testing.T) {
	name := "Show.S01E01.1080p.WEB-DL-GRP"
	if got := replaceZNTHEpisodeTitle(name, "", "1080p"); got != name {
		t.Fatalf("expected empty episode title to leave name unchanged, got %q", got)
	}
	if got := findZNTHTokenIndexes(name, " "); got != nil {
		t.Fatalf("expected empty token indexes to be nil, got %#v", got)
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

func TestBuildUnit3DNameRHDBuildsFromTMDBWhenBaseNameBlank(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Type:           "WEBDL",
		Tag:            "-GRP",
		Audio:          "DD+ 5.1",
		VideoEncode:    "H.264",
		AudioLanguages: []string{"German"},
		Release: api.ReleaseInfo{
			Resolution: "1080p",
		},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{
				Year:            2025,
				LocalizedTitles: map[string]string{"de": "Die Probe"},
			},
		},
	}

	got := buildUnit3DName("RHD", meta, config.TrackerConfig{})
	want := "Die Probe 2025 GERMAN 1080p WEB-DL DD+ 5.1 H.264-GRP"
	if got != want {
		t.Fatalf("expected RHD TMDB-derived name %q, got %q", want, got)
	}
}

func TestBuildUnit3DNameRHDUsesTMDBTitleFallback(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Type:           "WEBDL",
		Tag:            "-GRP",
		AudioLanguages: []string{"English"},
		Release: api.ReleaseInfo{
			Resolution: "720p",
		},
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{
				Title: "TMDB Title",
				Year:  2024,
			},
		},
	}

	got := buildUnit3DName("RHD", meta, config.TrackerConfig{})
	if !strings.HasPrefix(got, "TMDB Title 2024 ENGLISH 720p WEB-DL") {
		t.Fatalf("expected RHD TMDB title fallback, got %q", got)
	}
}

func TestBuildUnit3DNameRHDFullDiscOmitsLanguageTag(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ReleaseName:    "Movie 2024 1080p Blu-ray AVC DTS-HD MA 5.1-GRP",
		Type:           "DISC",
		Region:         "GER",
		Tag:            "-GRP",
		Audio:          "DTS-HD MA 5.1",
		VideoCodec:     "AVC",
		AudioLanguages: []string{"German", "English"},
		Release: api.ReleaseInfo{
			Title:      "Movie",
			Year:       2024,
			Resolution: "1080p",
			Source:     "Blu-ray",
			Size:       "BD50",
		},
	}

	got := buildUnit3DName("RHD", meta, config.TrackerConfig{})
	want := "Movie 2024 1080p COMPLETE GER Blu-ray BD50 DTS-HD MA 5.1 AVC-GRP"
	if got != want {
		t.Fatalf("expected RHD full-disc name %q, got %q", want, got)
	}
	if strings.Contains(got, "GERMAN") || strings.Contains(got, " DL") || strings.Contains(got, " ML") {
		t.Fatalf("expected RHD full-disc name to omit language tag, got %q", got)
	}
}

func TestBuildUnit3DNameRHDFullDiscUsesDiscTypeWhenTypeEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mediaInfoPath := filepath.Join(dir, "mediainfo.txt")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nComplete name: Movie"), 0o600); err != nil {
		t.Fatalf("write mediainfo fixture: %v", err)
	}
	torrentPath := filepath.Join(dir, "movie.torrent")
	if err := os.WriteFile(torrentPath, []byte("torrent"), 0o600); err != nil {
		t.Fatalf("write torrent fixture: %v", err)
	}

	meta := api.PreparedMetadata{
		ReleaseName:            "Movie.2024.1080p.COMPLETE.Blu-ray.BD50.DTS-HD.MA.5.1.AVC-GRP",
		DiscType:               " bdmv ",
		Region:                 "GER",
		Tag:                    "-GRP",
		Audio:                  "DTS-HD MA 5.1",
		VideoCodec:             "AVC",
		AudioLanguages:         []string{"German", "English"},
		ValidMediaInfoSettings: true,
		MediaInfoTextPath:      mediaInfoPath,
		TorrentPath:            torrentPath,
		ExternalIDs:            api.ExternalIDs{Category: "MOVIE"},
		Release: api.ReleaseInfo{
			Title:      "Movie",
			Year:       2024,
			Resolution: "1080p",
			Source:     "Blu-ray",
			Size:       "BD50",
		},
	}

	want := "Movie 2024 1080p COMPLETE GER Blu-ray BD50 DTS-HD MA 5.1 AVC-GRP"
	got := buildUnit3DName("RHD", meta, config.TrackerConfig{})
	if got != want {
		t.Fatalf("expected RHD DiscType-only full-disc name %q, got %q", want, got)
	}
	if strings.Contains(got, "GERMAN") || strings.Contains(got, " DL") || strings.Contains(got, " ML") {
		t.Fatalf("expected RHD DiscType-only full-disc name to omit language tag, got %q", got)
	}

	entry, err := buildUploadDryRunUnit3D(context.Background(), trackers.UploadRequest{
		Tracker: "RHD",
		Meta:    meta,
		TrackerConfig: config.TrackerConfig{
			APIKey: "test-key",
		},
		Assets: &trackers.DescriptionAssets{
			Description: "description",
			Final:       true,
		},
	})
	if err != nil {
		t.Fatalf("build RHD dry-run: %v", err)
	}
	if entry.ReleaseName != want {
		t.Fatalf("expected RHD dry-run release name %q, got %q", want, entry.ReleaseName)
	}
	if entry.Payload["name"] != want {
		t.Fatalf("expected RHD dry-run payload name %q, got %q", want, entry.Payload["name"])
	}
}

func TestResolveRHDTypeAndSourcePreservesExistingTypeOrdering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.PreparedMetadata
		want []string
	}{
		{
			name: "webdl",
			meta: api.PreparedMetadata{Type: "WEBDL"},
			want: []string{"WEB-DL"},
		},
		{
			name: "encode",
			meta: api.PreparedMetadata{Type: "ENCODE", Source: "Blu-ray"},
			want: []string{"Blu-ray"},
		},
		{
			name: "remux",
			meta: api.PreparedMetadata{Type: "REMUX", Source: "Blu-ray"},
			want: []string{"Blu-ray", "REMUX"},
		},
		{
			name: "disc type populated",
			meta: api.PreparedMetadata{
				Type:    "DISC",
				Region:  "GER",
				Release: api.ReleaseInfo{Source: "Blu-ray", Size: "BD50"},
			},
			want: []string{"COMPLETE", "GER", "Blu-ray", "BD50"},
		},
		{
			name: "empty type non disc",
			meta: api.PreparedMetadata{},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveRHDTypeAndSource(tt.meta)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestBuildUnit3DNameRHDDetectsMarkerTokensWithBroadDelimiters(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ReleaseName:    "Marker.Movie.2024.[INTERNAL].(UPSCALED).1080p.WEB-DL.DDP5.1.H.264-GRP",
		Type:           "WEBDL",
		Tag:            "-GRP",
		Audio:          "DDP5.1",
		VideoEncode:    "H.264",
		AudioLanguages: []string{"German"},
		Release: api.ReleaseInfo{
			Title:      "Marker Movie",
			Year:       2024,
			Resolution: "1080p",
		},
	}

	got := buildUnit3DName("RHD", meta, config.TrackerConfig{})
	want := "Marker Movie 2024 GERMAN 1080p UPSCALE WEB-DL DDP5.1 H.264 iNTERNAL-GRP"
	if got != want {
		t.Fatalf("expected RHD marker tokens with broad delimiters, got %q", got)
	}
}

func TestBuildUnit3DNameRHDIgnoresMarkerSubstringsAndGroupTag(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		ReleaseName:    "Marker.Movie.2024.Regradedness.Internalized.Lineage.1080p.WEB-DL.DDP5.1.H.264-LD",
		Type:           "WEBDL",
		Tag:            "-LD",
		Audio:          "DDP5.1",
		VideoEncode:    "H.264",
		AudioLanguages: []string{"English"},
		Release: api.ReleaseInfo{
			Title:      "Marker Movie",
			Year:       2024,
			Resolution: "1080p",
		},
	}

	got := buildUnit3DName("RHD", meta, config.TrackerConfig{})
	for _, marker := range []string{"REGRADED", "UPSCALE", "iNTERNAL", "DUBBED"} {
		if strings.Contains(got, marker) {
			t.Fatalf("expected marker substring/group tag not to emit %s, got %q", marker, got)
		}
	}
}

func TestBuildUnit3DNameRHDEmitsPreparedHDR(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{
		Type:           "WEBDL",
		Tag:            "-GRP",
		Audio:          "DDP5.1",
		HDR:            "DV HDR",
		VideoEncode:    "H.265",
		AudioLanguages: []string{"German"},
		Release: api.ReleaseInfo{
			Title:      "HDR Movie",
			Year:       2026,
			Resolution: "2160p",
		},
	}

	got := buildUnit3DName("RHD", meta, config.TrackerConfig{})
	want := "HDR Movie 2026 GERMAN 2160p WEB-DL DDP5.1 DV HDR H.265-GRP"
	if got != want {
		t.Fatalf("expected RHD name to include prepared HDR value %q, got %q", want, got)
	}
}

func TestResolveRHDLanguageCountsUniqueValidAudioLanguages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		language []string
		want     string
	}{
		{
			name:     "blank ignored",
			language: []string{"", "French", "   "},
			want:     "FRENCH",
		},
		{
			name:     "duplicate aliases ignored",
			language: []string{"English", "eng", "English"},
			want:     "ENGLISH",
		},
		{
			name:     "german aliases ignored",
			language: []string{"German", "deu", "de-DE"},
			want:     "GERMAN",
		},
		{
			name:     "dual real languages tagged",
			language: []string{"English", "French"},
			want:     "ENGLISH DL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveRHDLanguage(api.PreparedMetadata{AudioLanguages: tt.language})
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
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
