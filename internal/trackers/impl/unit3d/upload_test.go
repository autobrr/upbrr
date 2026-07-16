// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// captureUnit3DLogger records warning messages from upload paths that may
// return before reaching the HTTP test server.
type captureUnit3DLogger struct {
	mu       sync.Mutex
	warnings []string
}

func (l *captureUnit3DLogger) Tracef(string, ...any) {}
func (l *captureUnit3DLogger) Debugf(string, ...any) {}
func (l *captureUnit3DLogger) Infof(string, ...any)  {}

func (l *captureUnit3DLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
}

func (l *captureUnit3DLogger) Errorf(string, ...any) {}

// containsWarning reports whether any captured warning contains value.
func (l *captureUnit3DLogger) containsWarning(value string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, warning := range l.warnings {
		if strings.Contains(warning, value) {
			return true
		}
	}
	return false
}

func TestDefinitionsRetainIndependentSiteProfiles(t *testing.T) {
	first := NewWithProfile(Profile{
		Name:         "FIRST",
		BannedGroups: []string{"GROUP-A"},
		Site: SiteProfile{
			ResolveTypeID: func(api.UploadSubject) string { return "1" },
		},
	})
	second := NewWithProfile(Profile{
		Name: "SECOND",
		Site: SiteProfile{
			ResolveTypeID: func(api.UploadSubject) string { return "2" },
		},
	})

	if got := first.profile.Site.ResolveTypeID(api.UploadSubject{}); got != "1" {
		t.Fatalf("first profile type ID = %q, want 1", got)
	}
	if got := second.profile.Site.ResolveTypeID(api.UploadSubject{}); got != "2" {
		t.Fatalf("second profile type ID = %q, want 2", got)
	}
	groups := first.BannedGroups()
	groups[0] = "CHANGED"
	if got := first.BannedGroups()[0]; got != "GROUP-A" {
		t.Fatalf("definition exposed mutable banned groups: %q", got)
	}
}

func TestResolveUnit3DCategory(t *testing.T) {
	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "external movie",
			meta: api.UploadSubject{Identity: api.ExternalIdentity{Category: "movie"}},
			want: "MOVIE",
		},
		{
			name: "external tv",
			meta: api.UploadSubject{Identity: api.ExternalIdentity{Category: "TV"}},
			want: "TV",
		},
		{
			name: "noncanonical tv alias",
			meta: api.UploadSubject{Identity: api.ExternalIdentity{Category: " tv-show "}},
			want: "",
		},
		{
			name: "canonical movie",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "movie"},
				Release:  api.ReleaseInfo{Category: "TV"},
			},
			want: "MOVIE",
		},
		{
			name: "external wins over release",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "MOVIE"},
				Release:  api.ReleaseInfo{Category: "TV"},
			},
			want: "MOVIE",
		},
		{
			name: "canonical identity wins over release",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "movie"},
				Release:  api.ReleaseInfo{Category: "episode"},
			},
			want: "MOVIE",
		},
		{
			name: "canonical tv identity",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "TV"},
			},
			want: "TV",
		},
		{
			name: "unknown external ignores release",
			meta: api.UploadSubject{
				Identity: api.ExternalIdentity{Category: "documentary"},
				Release:  api.ReleaseInfo{Category: "series"},
			},
			want: "",
		},
		{
			name: "release category is not canonical identity",
			meta: api.UploadSubject{Release: api.ReleaseInfo{Category: " series "}},
			want: "",
		},
		{
			name: "release movie category is not canonical identity",
			meta: api.UploadSubject{
				ReleaseName: "Example.Movie.2026.1080p.WEB-DL-GRP",
				Release:     api.ReleaseInfo{Category: "film"},
			},
			want: "",
		},
		{
			name: "structured episode fields are not identity",
			meta: api.UploadSubject{
				ReleaseName: "Show.1x01.1080p.WEB-DL-GRP",
				SeasonInt:   1,
				EpisodeInt:  1,
			},
			want: "",
		},
		{
			name: "canonical tv identity ignores release name inference",
			meta: api.UploadSubject{
				Identity:    api.ExternalIdentity{Category: "TV"},
				ReleaseName: "Show.S01E01.1080p.WEB-DL-GRP",
			},
			want: "TV",
		},
		{
			name: "whitespace identity remains unknown",
			meta: api.UploadSubject{
				Identity:   api.ExternalIdentity{Category: " \t "},
				SeasonInt:  1,
				EpisodeInt: 1,
			},
			want: "",
		},
		{
			name: "release name is not canonical identity",
			meta: api.UploadSubject{ReleaseName: "Show.S01E01.1080p.WEB-DL-GRP"},
			want: "",
		},
	}

	for _, tc := range tests {
		got := resolveUnit3DCategory(tc.meta)
		if got != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, got)
		}
	}
}

func TestBuildUnit3DDataUsesCanonicalCategory(t *testing.T) {
	tvReq := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Show.S02E03.Episode.Title.1080p.WEB-DL-GRP",
			Identity: api.ExternalIdentity{
				Category: api.CanonicalCategoryTV,
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

	movieReq := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Movie.2025.1080p.WEB-DL-GRP",
			Identity: api.ExternalIdentity{
				Category: api.CanonicalCategoryMovie,
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

func TestBuildUnit3DDataMovieOmitsTVOnlyFields(t *testing.T) {
	req := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Movie.2025.1080p.WEB-DL.DD5.1.H264-GRP",
			Identity: api.ExternalIdentity{
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

func TestBuildUnit3DDataTVIncludesTVOnlyFields(t *testing.T) {
	req := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Show.S02E03.1080p.WEB-DL.DD5.1.H264-GRP",
			Identity: api.ExternalIdentity{
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

func TestBuildUnit3DDataTVOmitsParsedReleaseSeasonEpisodeFallback(t *testing.T) {
	req := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Daily.Show.2025.07.01.1080p.WEB-DL-GRP",
			Identity: api.ExternalIdentity{
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

	if got := data["season_number"]; got != "0" {
		t.Fatalf("expected season_number=0, got %q", got)
	}
	if got := data["episode_number"]; got != "0" {
		t.Fatalf("expected episode_number=0, got %q", got)
	}
	if got := data["tvdb"]; got != "789" {
		t.Fatalf("expected tvdb=789, got %q", got)
	}
	if got := unit3DTVPayloadMetadataMessage(req.Meta, data); got != "canonical TV season/episode missing; tracker payload uses 0 and ignores parsed season/episode fallback; refresh metadata or correct canonical season/episode before upload" {
		t.Fatalf("unexpected metadata message %q", got)
	}
}

func TestBuildUnit3DDryRunBlocksMissingCanonicalTVSeasonEpisode(t *testing.T) {
	tempDir := t.TempDir()
	mediaInfoPath := filepath.Join(tempDir, "mediainfo.txt")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nComplete name: show"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	torrentPath := filepath.Join(tempDir, "show.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce13:https://x.ee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := buildUploadDryRunUnit3D(context.Background(), trackers.PreparationInput{
		Tracker: "AITHER",
		TrackerConfig: config.TrackerConfig{
			APIKey: "test-key",
		},
		Meta: api.UploadSubject{
			ReleaseName:       "Daily.Show.2025.07.01.1080p.WEB-DL-GRP",
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Assessments:       api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
			Identity: api.ExternalIdentity{
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
		Assets: &trackers.DescriptionAssets{
			Description: "description",
			Final:       true,
		},
	})
	if err != nil {
		t.Fatalf("build Unit3D dry-run: %v", err)
	}
	if entry.Status != "blocked" {
		t.Fatalf("expected canonical TV metadata gap to block dry-run, got %#v", entry)
	}
	if !strings.Contains(entry.Message, "canonical TV season/episode missing; tracker payload uses 0 and ignores parsed season/episode fallback; refresh metadata or correct canonical season/episode before upload") {
		t.Fatalf("expected canonical metadata message, got %q", entry.Message)
	}
}

func TestUploadUnit3DBlocksMissingCanonicalTVSeasonEpisode(t *testing.T) {
	tempDir := t.TempDir()
	mediaInfoPath := filepath.Join(tempDir, "mediainfo.txt")
	if err := os.WriteFile(mediaInfoPath, []byte("General\nComplete name: show"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	torrentPath := filepath.Join(tempDir, "show.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce13:https://x.ee"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	var requestCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	logger := &captureUnit3DLogger{}
	_, err := uploadUnit3D(context.Background(), trackers.PreparationInput{
		Tracker: "AITHER",
		TrackerConfig: config.TrackerConfig{
			URL:    server.URL,
			APIKey: "test-key",
		},
		Logger: logger,
		Meta: api.UploadSubject{
			ReleaseName:       "Daily.Show.2025.07.01.1080p.WEB-DL-GRP",
			TorrentPath:       torrentPath,
			MediaInfoTextPath: mediaInfoPath,
			Assessments:       api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
			Identity: api.ExternalIdentity{
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
		Assets: &trackers.DescriptionAssets{
			Description: "description",
			Final:       true,
		},
	})
	if err == nil {
		t.Fatal("expected canonical TV metadata gap to block upload")
	}
	want := "canonical TV season/episode missing; tracker payload uses 0 and ignores parsed season/episode fallback; refresh metadata or correct canonical season/episode before upload"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected canonical metadata error, got %v", err)
	}
	if !logger.containsWarning(want) {
		t.Fatal("expected canonical metadata warning")
	}
	if requestCalls.Load() != 0 {
		t.Fatalf("expected upload to fail before remote calls, got %d calls", requestCalls.Load())
	}
}

func TestBuildUnit3DDataFailsOnUnknownType(t *testing.T) {
	req := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Movie.2025.1080p.UNKNOWN-GRP",
			Identity: api.ExternalIdentity{
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
	meta := api.UploadSubject{
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
	meta := api.UploadSubject{
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
	meta := api.UploadSubject{
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
	req := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Example.Movie.2026.2160p.WEB-DL.DDP5.1.H.265-GRP",
			Identity:    api.ExternalIdentity{
Category: "movie",
 TMDBID: 765432,
 IMDBID: 1234567,
},
			Type:        "movie",
			Source:      "WEB-DL",
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

func TestBuildUnit3DDataUsesCanonicalCategoryWithParsedReleaseFacts(t *testing.T) {
	tvReq := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Show.1x01.Episode.Title.1080p.WEB-DL-GRP",
			Identity: api.ExternalIdentity{
				Category: api.CanonicalCategoryTV,
				TVDBID:   789,
			},
			Type:       "WEBDL",
			SeasonInt:  1,
			EpisodeInt: 1,
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

	movieReq := trackers.PreparationInput{
		Tracker: "AITHER",
		Meta: api.UploadSubject{
			ReleaseName: "Example.Movie.2026.1080p.WEB-DL-GRP",
			Identity: api.ExternalIdentity{
				Category: api.CanonicalCategoryMovie,
				TVDBID:   789,
			},
			Type: "WEBDL",
			Release: api.ReleaseInfo{
				Category:   "MOVIE",
				Year:       2026,
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

func TestBuildUnit3DDataOmitsLegacyModQAliasForA4K(t *testing.T) {
	req := trackers.PreparationInput{
		Tracker: "A4K",
		TrackerConfig: config.TrackerConfig{
			ModQ: true,
		},
		Meta: api.UploadSubject{
			ReleaseName: "Movie.2025.2160p.WEB-DL.DD5.1.H264-GRP",
			Identity:    api.ExternalIdentity{Category: "MOVIE"},
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
