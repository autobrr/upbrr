// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepare(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	path := filepath.Join(base, "example")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	videoPath := filepath.Join(path, "example.mkv")
	if err := os.WriteFile(videoPath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write video failed: %v", err)
	}

	repo := &stubRepo{existing: db.FileMetadata{Path: path, InfoHash: "hash"}}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg))

	meta, err := service.Prepare(context.Background(), api.Request{
		Paths:          []string{path},
		Mode:           api.ModeCLI,
		Trackers:       []string{"blu", "bhd"},
		Options:        api.UploadOptions{Debug: true, Screens: 3},
		TrackersRemove: []string{"bhd"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if meta.SourcePath != path {
		t.Fatalf("unexpected source path: %s", meta.SourcePath)
	}
	if len(meta.Paths) != 1 {
		t.Fatalf("unexpected paths length: %d", len(meta.Paths))
	}
	if meta.Mode != api.ModeCLI {
		t.Fatalf("unexpected mode: %s", meta.Mode)
	}
	if len(meta.Trackers) != 2 {
		t.Fatalf("unexpected trackers length: %d", len(meta.Trackers))
	}
	if !meta.Options.Debug || meta.Options.Screens != 3 {
		t.Fatalf("unexpected options: %+v", meta.Options)
	}
	if len(meta.TrackersRemove) != 1 {
		t.Fatalf("unexpected trackers-remove length: %d", len(meta.TrackersRemove))
	}
	if len(meta.Paths) != 1 || meta.Paths[0] != path {
		t.Fatalf("unexpected paths: %v", meta.Paths)
	}
	if meta.StoredInfoHash != "hash" {
		t.Fatalf("unexpected stored info hash: %s", meta.StoredInfoHash)
	}
	if !meta.StoredDataFresh {
		t.Fatalf("expected stored metadata marked fresh")
	}
	if repo.saved.InfoHash != "hash" {
		t.Fatalf("expected persisted info hash, got %s", repo.saved.InfoHash)
	}
	if repo.saved.Path != path {
		t.Fatalf("expected repo save path, got %q", repo.saved.Path)
	}

	_, err = service.Prepare(context.Background(), api.Request{})
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input error, got: %v", err)
	}
}

func TestPrepareAppliesTorrentOverrides(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	path := filepath.Join(base, "example")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	videoPath := filepath.Join(path, "example.mkv")
	if err := os.WriteFile(videoPath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write video failed: %v", err)
	}

	repo := &stubRepo{}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg))
	infoHash := "abcdef0123456789abcdef0123456789abcdef01"

	meta, err := service.Prepare(context.Background(), api.Request{
		Paths: []string{path},
		Mode:  api.ModeCLI,
		TorrentOverrides: api.TorrentOverrides{
			InfoHash: &infoHash,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if meta.InfoHash != infoHash {
		t.Fatalf("expected infohash override, got %q", meta.InfoHash)
	}
}

func TestResolveServiceDarkroom(t *testing.T) {
	t.Parallel()

	service, longName, filename := resolveService(api.PreparedMetadata{
		SourcePath: `/releases/Example.Movie.2025.DARKROOM.WEB-DL.mkv`,
	})
	if service != "DARKROOM" {
		t.Fatalf("expected DARKROOM service, got %q", service)
	}
	if longName != "DARKROOM" {
		t.Fatalf("expected DARKROOM long name, got %q", longName)
	}
	if filename == "" {
		t.Fatalf("expected filename to be preserved")
	}
}

func TestPrepareBDMVMultiPlaylistUsesFullScanAndDerivesSummaries(t *testing.T) {
	base := t.TempDir()
	sourcePath := filepath.Join(base, "disc")
	bdmvPath := filepath.Join(sourcePath, "BDMV")
	if err := os.MkdirAll(filepath.Join(bdmvPath, "PLAYLIST"), 0o755); err != nil {
		t.Fatalf("mkdir playlist failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(bdmvPath, "STREAM"), 0o755); err != nil {
		t.Fatalf("mkdir stream failed: %v", err)
	}

	repo := &stubRepo{
		playlistSelection: db.PlaylistSelection{
			SelectedPlaylists: []string{"00002.MPLS", "00001.MPLS"},
		},
		playlistSelectionPath: filepath.ToSlash(filepath.Clean(filepath.Join(sourcePath, "BDMV"))),
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg), WithBDInfoService(bdinfo.New(api.NopLogger{})))

	originalDiscover := discoverBDMVPlaylists
	originalParse := parseBDMVPlaylist
	originalFullScan := executeFullBDInfoScan
	originalPlaylistScan := executePlaylistBDInfo
	originalParseOutput := parseBDInfoOutput
	t.Cleanup(func() {
		discoverBDMVPlaylists = originalDiscover
		parseBDMVPlaylist = originalParse
		executeFullBDInfoScan = originalFullScan
		executePlaylistBDInfo = originalPlaylistScan
		parseBDInfoOutput = originalParseOutput
	})

	discoverBDMVPlaylists = func(ctx context.Context, root string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
			{File: "00002.MPLS", Duration: 6000},
		}, nil
	}
	parseBDMVPlaylist = func(mplsPath string) (float64, []filesystem.PlaylistItem, error) {
		switch filepath.Base(strings.ToUpper(mplsPath)) {
		case "00001.MPLS":
			return 5400, []filesystem.PlaylistItem{
				{File: "00001.m2ts", Size: 100},
				{File: "00002.m2ts", Size: 150},
			}, nil
		case "00002.MPLS":
			return 6000, []filesystem.PlaylistItem{
				{File: "00002.m2ts", Size: 150},
				{File: "00003.m2ts", Size: 200},
			}, nil
		default:
			return 0, nil, errors.New("unexpected playlist")
		}
	}

	fullReport := strings.Join([]string{
		"header",
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
		"00003.m2ts        01:40:00     2,000,000,000",
		"CHAPTERS:",
		"QUICK SUMMARY:",
		"Playlist: 00002.MPLS",
		"Disc Label: DISC-TWO",
		"Length: 01:40:00.000",
		"********************",
		"FILES:",
		"[/code]",
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
		"00001.m2ts        01:30:00     1,000,000,000",
		"CHAPTERS:",
		"QUICK SUMMARY:",
		"Playlist: 00001.MPLS",
		"Disc Label: DISC-ONE",
		"Length: 01:30:00.000",
		"********************",
		"FILES:",
		"[/code]",
	}, "\n")

	fullScans := 0
	playlistScans := 0
	executeFullBDInfoScan = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, outputDir string) (bdinfo.ScanResult, error) {
		fullScans++
		return bdinfo.ScanResult{
			ReportPath: filepath.Join(outputDir, "BD_FULL.txt"),
			ReportText: fullReport,
		}, nil
	}
	executePlaylistBDInfo = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, playlistFile string, outputDir string) (string, error) {
		playlistScans++
		return "", errors.New("unexpected playlist scan")
	}
	parseBDInfoOutput = func(svc *bdinfo.Service, filePath string) (map[string]interface{}, error) {
		payload, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"summary": string(payload)}, nil
	}

	meta, err := service.Prepare(context.Background(), api.Request{
		Paths: []string{sourcePath},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}

	if fullScans != 1 {
		t.Fatalf("expected 1 full scan, got %d", fullScans)
	}
	if playlistScans != 0 {
		t.Fatalf("expected 0 playlist scans, got %d", playlistScans)
	}
	if got, want := meta.VideoPath, filepath.Join(bdmvPath, "STREAM", "00003.m2ts"); got != want {
		t.Fatalf("expected main file %q, got %q", want, got)
	}
	wantFiles := []string{
		filepath.Join(bdmvPath, "STREAM", "00001.m2ts"),
		filepath.Join(bdmvPath, "STREAM", "00002.m2ts"),
		filepath.Join(bdmvPath, "STREAM", "00003.m2ts"),
	}
	if !reflect.DeepEqual(sortedStrings(meta.FileList), sortedStrings(wantFiles)) {
		t.Fatalf("unexpected file list: %#v", meta.FileList)
	}
	if len(meta.SelectedBDMVPlaylists) != 2 || meta.SelectedBDMVPlaylists[0].File != "00002.MPLS" || meta.SelectedBDMVPlaylists[1].File != "00001.MPLS" {
		t.Fatalf("unexpected selected playlists: %#v", meta.SelectedBDMVPlaylists)
	}

	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, sourcePath)
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}

	assertFileContains(t, paths.BDMVSummaryPath(tmpDir, "00002.MPLS"), "Playlist: 00002.MPLS")
	assertFileContains(t, paths.BDMVSummaryPath(tmpDir, "00001.MPLS"), "Playlist: 00001.MPLS")
	assertFileContains(t, paths.BDMVExtSummaryPath(tmpDir, "00002.MPLS"), "extended summary two")
	assertFileContains(t, paths.BDMVExtSummaryPath(tmpDir, "00001.MPLS"), "extended summary one")
	assertFileContains(t, filepath.Join(tmpDir, "BD_FULL.txt"), "PLAYLIST: 00002.MPLS")
}

func TestLoadSelectedBDMVPlaylistsErrorsWhenRequestedPlaylistMissing(t *testing.T) {
	originalDiscover := discoverBDMVPlaylists
	t.Cleanup(func() {
		discoverBDMVPlaylists = originalDiscover
	})

	discoverBDMVPlaylists = func(ctx context.Context, root string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
		}, nil
	}

	_, err := loadSelectedBDMVPlaylists(context.Background(), `D:\Disc\BDMV`, []string{"00001.MPLS", "00002.MPLS"})
	if err == nil || !strings.Contains(err.Error(), "00002.MPLS") {
		t.Fatalf("expected missing playlist error for 00002.MPLS, got %v", err)
	}
}

func TestDiscoverBDMVSummaryCache(t *testing.T) {
	tmpDir := t.TempDir()
	writeBDMVSummaryFixture(t, tmpDir, "00002.MPLS", "extended summary two")
	writeBDMVSummaryFixture(t, tmpDir, "00001.MPLS", "extended summary one")

	cache, err := discoverBDMVSummaryCache(tmpDir)
	if err != nil {
		t.Fatalf("discover cache: %v", err)
	}
	if len(cache.Entries) != 2 {
		t.Fatalf("expected 2 cache entries, got %d", len(cache.Entries))
	}
	if got := cache.Entries["00002.MPLS"].ExtPath; got != paths.BDMVExtSummaryPath(tmpDir, "00002.MPLS") {
		t.Fatalf("expected playlist ext path, got %q", got)
	}
	if got := strings.TrimSpace(cache.Entries["00001.MPLS"].ExtSummary); got != "extended summary one" {
		t.Fatalf("unexpected ext summary: %q", got)
	}
}

func TestDiscoverBDMVSummaryCacheIgnoresMalformedSummary(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(paths.BDMVSummaryPath(tmpDir, "00001.MPLS"), []byte("Disc Label: BROKEN\n"), 0o600); err != nil {
		t.Fatalf("write malformed summary: %v", err)
	}

	cache, err := discoverBDMVSummaryCache(tmpDir)
	if err != nil {
		t.Fatalf("discover cache: %v", err)
	}
	if len(cache.Entries) != 0 {
		t.Fatalf("expected malformed summary to be ignored, got %#v", cache.Entries)
	}
}

func TestDiscoverBDMVSummaryCacheErrorsOnDuplicatePlaylist(t *testing.T) {
	tmpDir := t.TempDir()
	writeBDMVSummaryFixture(t, tmpDir, "00002.MPLS", "extended summary two")
	if err := os.WriteFile(filepath.Join(tmpDir, "BD_SUMMARY_00002.txt"), []byte(strings.Join([]string{
		"Disc Title: Example",
		"Disc Label: BDMV",
		"Playlist: 00002.MPLS",
		"Length: 01:30:00.000",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write duplicate summary fixture: %v", err)
	}

	_, err := discoverBDMVSummaryCache(tmpDir)
	if err == nil || !strings.Contains(err.Error(), "duplicate cached playlist summary for 00002.MPLS") {
		t.Fatalf("expected duplicate cache error, got %v", err)
	}
}

func TestPrepareBDMVUsesCachedSummariesWithoutRescan(t *testing.T) {
	base := t.TempDir()
	sourcePath := filepath.Join(base, "disc")
	bdmvPath := filepath.Join(sourcePath, "BDMV")
	if err := os.MkdirAll(filepath.Join(bdmvPath, "PLAYLIST"), 0o755); err != nil {
		t.Fatalf("mkdir playlist failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(bdmvPath, "STREAM"), 0o755); err != nil {
		t.Fatalf("mkdir stream failed: %v", err)
	}

	repo := &stubRepo{
		playlistSelection: db.PlaylistSelection{
			SelectedPlaylists: []string{"00002.MPLS", "00001.MPLS"},
		},
		playlistSelectionPath: filepath.ToSlash(filepath.Clean(filepath.Join(sourcePath, "BDMV"))),
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg), WithBDInfoService(bdinfo.New(api.NopLogger{})))

	originalDiscover := discoverBDMVPlaylists
	originalParse := parseBDMVPlaylist
	originalFullScan := executeFullBDInfoScan
	originalPlaylistScan := executePlaylistBDInfo
	originalParseOutput := parseBDInfoOutput
	t.Cleanup(func() {
		discoverBDMVPlaylists = originalDiscover
		parseBDMVPlaylist = originalParse
		executeFullBDInfoScan = originalFullScan
		executePlaylistBDInfo = originalPlaylistScan
		parseBDInfoOutput = originalParseOutput
	})

	discoverBDMVPlaylists = func(ctx context.Context, root string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
			{File: "00002.MPLS", Duration: 6000},
		}, nil
	}
	parseBDMVPlaylist = func(mplsPath string) (float64, []filesystem.PlaylistItem, error) {
		switch filepath.Base(strings.ToUpper(mplsPath)) {
		case "00001.MPLS":
			return 5400, []filesystem.PlaylistItem{
				{File: "00001.m2ts", Size: 100},
				{File: "00002.m2ts", Size: 150},
			}, nil
		case "00002.MPLS":
			return 6000, []filesystem.PlaylistItem{
				{File: "00002.m2ts", Size: 150},
				{File: "00003.m2ts", Size: 200},
			}, nil
		default:
			return 0, nil, errors.New("unexpected playlist")
		}
	}
	fullScans := 0
	playlistScans := 0
	executeFullBDInfoScan = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, outputDir string) (bdinfo.ScanResult, error) {
		fullScans++
		return bdinfo.ScanResult{}, errors.New("unexpected full scan")
	}
	executePlaylistBDInfo = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, playlistFile string, outputDir string) (string, error) {
		playlistScans++
		return "", errors.New("unexpected playlist scan")
	}
	parseBDInfoOutput = func(svc *bdinfo.Service, filePath string) (map[string]interface{}, error) {
		payload, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"summary": string(payload)}, nil
	}

	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, api.PreparedMetadata{}, sourcePath)
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp dir: %v", err)
	}
	writeBDMVSummaryFixture(t, tmpDir, "00001.MPLS", "extended summary one")
	writeBDMVSummaryFixture(t, tmpDir, "00002.MPLS", "extended summary two")

	meta, err := service.Prepare(context.Background(), api.Request{
		Paths: []string{sourcePath},
		Mode:  api.ModeCLI,
	})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}

	if fullScans != 0 {
		t.Fatalf("expected no full scan, got %d", fullScans)
	}
	if playlistScans != 0 {
		t.Fatalf("expected no playlist scan, got %d", playlistScans)
	}
	assertFileContains(t, paths.BDMVSummaryPath(tmpDir, "00002.MPLS"), "Playlist: 00002.MPLS")
	assertFileContains(t, paths.BDMVSummaryPath(tmpDir, "00001.MPLS"), "Playlist: 00001.MPLS")
	assertFileContains(t, paths.BDMVExtSummaryPath(tmpDir, "00002.MPLS"), "extended summary two")
	if got := meta.BDInfo["summary"]; !strings.Contains(got.(string), "Playlist: 00002.MPLS") {
		t.Fatalf("expected cached canonical summary for first selected playlist, got %#v", meta.BDInfo)
	}
}

func TestPrepareBDMVPartialCacheRequiresConfirmation(t *testing.T) {
	base := t.TempDir()
	sourcePath := filepath.Join(base, "disc")
	bdmvPath := filepath.Join(sourcePath, "BDMV")
	if err := os.MkdirAll(filepath.Join(bdmvPath, "PLAYLIST"), 0o755); err != nil {
		t.Fatalf("mkdir playlist failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(bdmvPath, "STREAM"), 0o755); err != nil {
		t.Fatalf("mkdir stream failed: %v", err)
	}

	repo := &stubRepo{
		playlistSelection: db.PlaylistSelection{
			SelectedPlaylists: []string{"00002.MPLS", "00001.MPLS"},
		},
		playlistSelectionPath: filepath.ToSlash(filepath.Clean(filepath.Join(sourcePath, "BDMV"))),
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg), WithBDInfoService(bdinfo.New(api.NopLogger{})))

	originalDiscover := discoverBDMVPlaylists
	t.Cleanup(func() {
		discoverBDMVPlaylists = originalDiscover
	})
	discoverBDMVPlaylists = func(ctx context.Context, root string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
			{File: "00002.MPLS", Duration: 6000},
		}, nil
	}

	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, api.PreparedMetadata{}, sourcePath)
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp dir: %v", err)
	}
	writeBDMVSummaryFixture(t, tmpDir, "00001.MPLS", "extended summary one")

	_, err = service.Prepare(context.Background(), api.Request{
		Paths:   []string{sourcePath},
		Mode:    api.ModeCLI,
		Options: api.UploadOptions{InteractionMode: api.InteractionModeInteractive},
	})
	var rescanErr *api.BDMVRescanRequiredError
	if !errors.As(err, &rescanErr) {
		t.Fatalf("expected rescan confirmation error, got %v", err)
	}
	if !reflect.DeepEqual(rescanErr.MissingPlaylists, []string{"00002.MPLS"}) {
		t.Fatalf("unexpected missing playlists: %#v", rescanErr.MissingPlaylists)
	}
}

func TestPrepareBDMVPartialCacheRescansWhenConfirmed(t *testing.T) {
	base := t.TempDir()
	sourcePath := filepath.Join(base, "disc")
	bdmvPath := filepath.Join(sourcePath, "BDMV")
	if err := os.MkdirAll(filepath.Join(bdmvPath, "PLAYLIST"), 0o755); err != nil {
		t.Fatalf("mkdir playlist failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(bdmvPath, "STREAM"), 0o755); err != nil {
		t.Fatalf("mkdir stream failed: %v", err)
	}

	repo := &stubRepo{
		playlistSelection: db.PlaylistSelection{
			SelectedPlaylists: []string{"00002.MPLS", "00001.MPLS"},
		},
		playlistSelectionPath: filepath.ToSlash(filepath.Clean(filepath.Join(sourcePath, "BDMV"))),
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg), WithBDInfoService(bdinfo.New(api.NopLogger{})))

	originalDiscover := discoverBDMVPlaylists
	originalParse := parseBDMVPlaylist
	originalFullScan := executeFullBDInfoScan
	originalParseOutput := parseBDInfoOutput
	t.Cleanup(func() {
		discoverBDMVPlaylists = originalDiscover
		parseBDMVPlaylist = originalParse
		executeFullBDInfoScan = originalFullScan
		parseBDInfoOutput = originalParseOutput
	})
	discoverBDMVPlaylists = func(ctx context.Context, root string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
			{File: "00002.MPLS", Duration: 6000},
		}, nil
	}
	parseBDMVPlaylist = func(mplsPath string) (float64, []filesystem.PlaylistItem, error) {
		return 6000, []filesystem.PlaylistItem{{File: "00003.m2ts", Size: 200}}, nil
	}
	fullReport := strings.Join([]string{
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
		"00003.m2ts        01:40:00     2,000,000,000",
		"CHAPTERS:",
		"QUICK SUMMARY:",
		"Playlist: 00002.MPLS",
		"Disc Label: DISC-TWO",
		"********************",
		"FILES:",
		"[/code]",
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
		"00001.m2ts        01:30:00     1,000,000,000",
		"CHAPTERS:",
		"QUICK SUMMARY:",
		"Playlist: 00001.MPLS",
		"Disc Label: DISC-ONE",
		"********************",
		"FILES:",
		"[/code]",
	}, "\n")
	fullScans := 0
	executeFullBDInfoScan = func(svc *bdinfo.Service, ctx context.Context, bdmvPath string, outputDir string) (bdinfo.ScanResult, error) {
		fullScans++
		return bdinfo.ScanResult{
			ReportPath: filepath.Join(outputDir, "BD_FULL.txt"),
			ReportText: fullReport,
		}, nil
	}
	parseBDInfoOutput = func(svc *bdinfo.Service, filePath string) (map[string]interface{}, error) {
		payload, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"summary": string(payload)}, nil
	}

	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, api.PreparedMetadata{}, sourcePath)
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp dir: %v", err)
	}
	writeBDMVSummaryFixture(t, tmpDir, "00001.MPLS", "extended summary one")

	_, err = service.Prepare(context.Background(), api.Request{
		Paths:             []string{sourcePath},
		Mode:              api.ModeCLI,
		ConfirmBDMVRescan: true,
		Options:           api.UploadOptions{InteractionMode: api.InteractionModeInteractive},
	})
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if fullScans != 1 {
		t.Fatalf("expected 1 full scan after confirmation, got %d", fullScans)
	}
	assertFileContains(t, paths.BDMVSummaryPath(tmpDir, "00002.MPLS"), "Playlist: 00002.MPLS")
}

type stubRepo struct {
	saved                 db.FileMetadata
	existing              db.FileMetadata
	playlistSelection     db.PlaylistSelection
	playlistSelectionPath string
}

type stubMediaInfo struct{}

func (stubMediaInfo) Export(context.Context, mediainfo.Request) (mediainfo.Result, error) {
	return mediainfo.Result{}, nil
}

type stubSceneDetector struct{}

func (stubSceneDetector) Detect(context.Context, api.PreparedMetadata) (SceneResult, error) {
	return SceneResult{}, nil
}

func (s *stubRepo) GetByPath(context.Context, string) (db.FileMetadata, error) {
	if s.existing.Path != "" {
		return s.existing, nil
	}
	return db.FileMetadata{}, internalerrors.ErrNotFound
}

func (s *stubRepo) Save(_ context.Context, metadata db.FileMetadata) error {
	metadata.UpdatedAt = time.Now().UTC()
	s.saved = metadata
	return nil
}

func (s *stubRepo) GetExternalIDs(context.Context, string) (db.ExternalIDs, error) {
	return db.ExternalIDs{}, internalerrors.ErrNotFound
}

func (s *stubRepo) SaveExternalIDs(context.Context, db.ExternalIDs) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) GetExternalMetadata(context.Context, string) (db.ExternalMetadata, error) {
	return db.ExternalMetadata{}, internalerrors.ErrNotFound
}

func (s *stubRepo) SaveExternalMetadata(context.Context, db.ExternalMetadata) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) GetDVDMediaInfo(context.Context, string) (db.DVDMediaInfo, error) {
	return db.DVDMediaInfo{}, internalerrors.ErrNotFound
}

func (s *stubRepo) SaveDVDMediaInfo(context.Context, db.DVDMediaInfo) error {
	return nil
}

func (s *stubRepo) GetReleaseNameOverrides(context.Context, string) (db.ReleaseNameOverrides, error) {
	return db.ReleaseNameOverrides{}, internalerrors.ErrNotFound
}

func (s *stubRepo) SaveReleaseNameOverrides(context.Context, string, db.ReleaseNameOverrides) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) DeleteReleaseNameOverrides(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) GetDescriptionOverride(context.Context, string) (db.DescriptionOverride, error) {
	return db.DescriptionOverride{}, internalerrors.ErrNotFound
}

func (s *stubRepo) SaveDescriptionOverride(context.Context, db.DescriptionOverride) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) DeleteDescriptionOverride(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListHistoryEntries(context.Context) ([]db.HistoryEntry, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListUploadHistoryByPath(context.Context, string) ([]db.UploadRecord, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListPendingUploads(context.Context) ([]db.UploadRecord, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) CreateUploadRecord(context.Context, db.UploadRecord) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) UpdateLatestUploadRecordStatus(context.Context, string, string, string) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) SaveTrackerRuleFailures(context.Context, string, string, []db.TrackerRuleFailure) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListTrackerRuleFailuresByPath(context.Context, string) ([]db.TrackerRuleFailure, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) GetTrackerTimestamp(context.Context, string) (time.Time, error) {
	return time.Time{}, internalerrors.ErrNotImplemented
}

func (s *stubRepo) SaveTrackerTimestamp(context.Context, db.TrackerTimestamp) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) SaveTrackerMetadata(context.Context, db.TrackerMetadata) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListTrackerMetadataByPath(context.Context, string) ([]db.TrackerMetadata, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) SaveScreenshot(context.Context, db.Screenshot) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListScreenshotsByPath(context.Context, string) ([]db.Screenshot, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) DeleteScreenshot(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) SaveFinalSelections(context.Context, string, []db.ScreenshotFinalSelection) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListFinalSelections(context.Context, string) ([]db.ScreenshotFinalSelection, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) DeleteFinalSelection(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}
func (s *stubRepo) ReplaceScreenshotSlots(context.Context, string, []db.ScreenshotSlot) error {
	return internalerrors.ErrNotImplemented
}
func (s *stubRepo) ListScreenshotSlotsByPath(context.Context, string) ([]db.ScreenshotSlot, error) {
	return nil, internalerrors.ErrNotImplemented
}
func (s *stubRepo) UpsertScreenshotSlotVariants(context.Context, string, []db.ScreenshotSlotVariant) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) SaveUploadedImages(context.Context, string, string, []db.UploadedImageLink) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListUploadedImagesByPath(context.Context, string) ([]db.UploadedImageLink, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) DeleteUploadedImage(context.Context, string, string, string) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) GetPlaylistSelection(_ context.Context, path string) (db.PlaylistSelection, error) {
	if len(s.playlistSelection.SelectedPlaylists) > 0 && (s.playlistSelectionPath == "" || s.playlistSelectionPath == path) {
		return s.playlistSelection, nil
	}
	return db.PlaylistSelection{}, internalerrors.ErrNotImplemented
}

func (s *stubRepo) SavePlaylistSelection(context.Context, string, []string, bool) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) DeletePlaylistSelection(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) ListStoredReleasePaths(context.Context) ([]string, error) {
	return nil, internalerrors.ErrNotImplemented
}

func (s *stubRepo) PurgeContentData(context.Context, string) error {
	return internalerrors.ErrNotImplemented
}

func sortedStrings(values []string) []string {
	cloned := append([]string(nil), values...)
	slices.Sort(cloned)
	return cloned
}

func assertFileContains(t *testing.T, path string, want string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(payload), want) {
		t.Fatalf("expected %s to contain %q, got %q", path, want, string(payload))
	}
}

func writeBDMVSummaryFixture(t *testing.T, tmpDir string, playlist string, extSummary string) {
	t.Helper()
	summaryPath := paths.BDMVSummaryPath(tmpDir, playlist)
	summary := strings.Join([]string{
		"Disc Title: Example",
		"Disc Label: BDMV",
		"Playlist: " + playlist,
		"Length: 01:30:00.000",
	}, "\n") + "\n"
	if err := os.WriteFile(summaryPath, []byte(summary), 0o600); err != nil {
		t.Fatalf("write summary fixture: %v", err)
	}
	extPath := paths.BDMVExtSummaryPath(tmpDir, playlist)
	if err := os.WriteFile(extPath, []byte(extSummary+"\n"), 0o600); err != nil {
		t.Fatalf("write ext fixture: %v", err)
	}
}
