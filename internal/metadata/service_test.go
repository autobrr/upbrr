// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

func testCollectionRequest(t *testing.T, request api.Request) preparationstate.Request {
	t.Helper()
	input, _ := api.MapPreparationRequest(request, api.PreparationIntentPreview)
	layout, _ := sourcelayout.Resolve(context.Background(), request.SourcePath)
	return preparationstate.Request{Input: input, Layout: layout}
}

func TestCollectSourceEvidenceKeepsCanonicalSource(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	path := filepath.Join(base, "example")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	videoPath := filepath.Join(path, "example.mkv")
	if err := os.WriteFile(videoPath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write video failed: %v", err)
	}

	repo := &stubRepo{existing: db.FileMetadata{Path: videoPath, InfoHash: "hash"}}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg))

	meta, err := service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{
		SourcePath:     path,
		Trackers:       []string{"blu", "bhd"},
		Options:        api.UploadOptions{
OnlyID: true,
 KeepImages: true,
 InteractionMode: api.InteractionModeUnattended,
},
		TrackersRemove: []string{"bhd"},
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if meta.SourcePath != path {
		t.Fatalf("unexpected source path: %s", meta.SourcePath)
	}
	if len(meta.Paths) != 1 {
		t.Fatalf("unexpected paths length: %d", len(meta.Paths))
	}
	if len(meta.EvidenceTrackers) != 0 {
		t.Fatalf("workflow trackers leaked into preparation evidence: %v", meta.EvidenceTrackers)
	}
	if !meta.Policy.OnlyID || !meta.Policy.KeepImages || meta.Policy.InteractionMode != api.InteractionModeUnattended {
		t.Fatalf("unexpected collection policy: %+v", meta.Policy)
	}
	if len(meta.Paths) != 1 || meta.Paths[0] != path {
		t.Fatalf("unexpected paths: %v", meta.Paths)
	}
	if repo.saved.Path != path {
		t.Fatalf("expected repo save path, got %q", repo.saved.Path)
	}

	_, err = service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{}))
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input error, got: %v", err)
	}
}

func TestApplySceneDetectionCopiesResultAfterRecoverableNFOFailure(t *testing.T) {
	t.Parallel()

	// Scene detection runs during fact derivation; applySceneDetection is the
	// plumbing that copies the result and surfaces a recoverable NFO side effect.
	logger := &recordingLogger{}
	service := NewService(&stubRepo{},
		WithSceneDetector(staticSceneDetector{
			result: SceneResult{
				IsScene:   true,
				SceneName: "Example.Release.2024.1080p-WEB",
				TMDBID:    42,
				IMDBID:    1234567,
				TVDBID:    333,
			},
			err: newSceneNFOError(errors.New("scene: write nfo: permission denied")),
		}),
		WithLogger(logger),
	)

	meta, err := service.applySceneDetection(context.Background(), preparationstate.State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.Scene || meta.SceneName != "Example.Release.2024.1080p-WEB" {
		t.Fatalf("expected scene result copied, got scene=%t name=%q", meta.Scene, meta.SceneName)
	}
	if meta.SceneTMDBID != 42 || meta.SceneIMDB != 1234567 || meta.SceneTVDBID != 333 {
		t.Fatalf("expected scene ids copied, got tmdb=%d imdb=%d tvdb=%d", meta.SceneTMDBID, meta.SceneIMDB, meta.SceneTVDBID)
	}
	if len(logger.warnings) != 1 || !strings.Contains(logger.warnings[0], "scene nfo side effect failed") {
		t.Fatalf("expected visible NFO warning, got %#v", logger.warnings)
	}
}

func TestApplySceneDetectionBackfillsIMDbID(t *testing.T) {
	t.Parallel()

	service := NewService(&stubRepo{},
		WithSceneDetector(staticSceneDetector{result: SceneResult{IsScene: true, IMDBID: 132245}}),
	)
	// No externally resolved ID: scene evidence backfills canonical identity.
	meta, err := service.applySceneDetection(context.Background(), preparationstate.State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Identity.IMDBID != 132245 {
		t.Fatalf("expected scene imdb backfilled, got %d", meta.Identity.IMDBID)
	}
	// An already-resolved id is not overwritten.
	meta, err = service.applySceneDetection(context.Background(), preparationstate.State{Identity: api.ExternalIdentity{IMDBID: 999}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Identity.IMDBID != 999 {
		t.Fatalf("expected resolved imdb preserved, got %d", meta.Identity.IMDBID)
	}
}

func TestApplySceneDetectionPropagatesCancellation(t *testing.T) {
	t.Parallel()

	// Context cancellation must abort the pipeline, not be swallowed as a soft
	// srrdb failure that continues into tracker rules.
	service := NewService(&stubRepo{},
		WithSceneDetector(staticSceneDetector{err: context.Canceled}),
	)
	if _, err := service.applySceneDetection(context.Background(), preparationstate.State{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation propagated, got %v", err)
	}
}

func TestPrepareCLIKeepFolderPreservesSingleFileDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	path := filepath.Join(base, "Example.Movie.2026.1080p.WEB-DL-GRP")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	videoPath := filepath.Join(path, "Example.Movie.2026.1080p.WEB-DL-GRP.mkv")
	if err := os.WriteFile(videoPath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write video failed: %v", err)
	}

	repo := &stubRepo{}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg))

	meta, err := service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{
		SourcePath: path,
		Options: api.UploadOptions{KeepFolder: true},
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if meta.SourcePath != path {
		t.Fatalf("expected folder source path, got %q", meta.SourcePath)
	}
	if meta.VideoPath != videoPath {
		t.Fatalf("expected selected video path %q, got %q", videoPath, meta.VideoPath)
	}
	if repo.saved.Path != path {
		t.Fatalf("expected repo save path to remain folder, got %q", repo.saved.Path)
	}
}

func TestPrepareCLITVPackPreservesDirectory(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	path := filepath.Join(base, "Example.Show.S01.1080p.WEB-DL-GRP")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	episode1 := filepath.Join(path, "Example.Show.S01E01.mkv")
	episode2 := filepath.Join(path, "Example.Show.S01E02.mkv")
	if err := os.WriteFile(episode1, []byte("episode 1"), 0o600); err != nil {
		t.Fatalf("write first episode failed: %v", err)
	}
	if err := os.WriteFile(episode2, []byte("episode 2 larger"), 0o600); err != nil {
		t.Fatalf("write second episode failed: %v", err)
	}

	repo := &stubRepo{}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg))

	meta, err := service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{
		SourcePath: path,
	}))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if meta.SourcePath != path {
		t.Fatalf("expected folder source path, got %q", meta.SourcePath)
	}
	if !meta.TVPack {
		t.Fatalf("expected TV pack metadata")
	}
	if len(meta.FileList) != 2 {
		t.Fatalf("expected both episode files, got %#v", meta.FileList)
	}
	if repo.saved.Path != path {
		t.Fatalf("expected repo save path to remain folder, got %q", repo.saved.Path)
	}
}

func TestResolveServiceDarkroom(t *testing.T) {
	t.Parallel()

	service, longName, filename := resolveService(preparationstate.State{
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

func TestResolveServiceMGMPlus(t *testing.T) {
	t.Parallel()

	service, longName, filename := resolveService(preparationstate.State{
		SourcePath: `D:\TV\Example.Spy.Show.S01.2160p.MGMP.WEB-DL.DDP5.1.H.265-GRP`,
	})
	if service != "MGMP" {
		t.Fatalf("expected MGMP service, got %q", service)
	}
	if longName != "MGM Plus" {
		t.Fatalf("expected MGM Plus long name, got %q", longName)
	}
	if filename == "" {
		t.Fatalf("expected filename to be preserved")
	}
}

func TestResolveServiceIgnoresTitleServiceTokens(t *testing.T) {
	t.Parallel()

	service, longName, filename := resolveService(preparationstate.State{
		SourcePath: `/releases/Example.Show.S01E01.Netflix.and.Chill.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv`,
	})
	if service != "" {
		t.Fatalf("expected no service from episode title, got %q", service)
	}
	if longName != "" {
		t.Fatalf("expected no long name from episode title, got %q", longName)
	}
	if filename == "" {
		t.Fatalf("expected filename to be preserved")
	}
}

func TestResolveServiceUsesAliasAdjacentToWebSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sourcePath string
	}{
		{
			name:       "web dl with separator",
			sourcePath: `/releases/Netflix.Documentary.2026.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP.mkv`,
		},
		{
			name:       "split web dl",
			sourcePath: `/releases/Netflix.Documentary.2026.1080p.AMZN WEB DL DDP5.1 H.264-GRP.mkv`,
		},
		{
			name:       "split web rip",
			sourcePath: `/releases/Netflix.Documentary.2026.1080p.AMZN WEB RIP DDP5.1 H.264-GRP.mkv`,
		},
		{
			name:       "compact webrip",
			sourcePath: `/releases/Netflix.Documentary.2026.1080p.AMZN.WEBRip.DDP5.1.H.264-GRP.mkv`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertResolvedService(t, tt.sourcePath, "AMZN", "Amazon Prime")
		})
	}
}

func TestResolveServicePrefersLongestAdjacentAlias(t *testing.T) {
	t.Parallel()

	assertResolvedService(t, `/releases/Example.Movie.2026.1080p.Apple.TV+.WEB-DL.DDP5.1.H.264-GRP.mkv`, "ATVP", "Apple TV+")
}

func assertResolvedService(t *testing.T, sourcePath, wantService, wantLongName string) {
	t.Helper()

	service, longName, filename := resolveService(preparationstate.State{
		SourcePath: sourcePath,
	})
	if service != wantService {
		t.Fatalf("expected %s service, got %q", wantService, service)
	}
	if longName != wantLongName {
		t.Fatalf("expected %s long name, got %q", wantLongName, longName)
	}
	if filename == "" {
		t.Fatalf("expected filename to be preserved")
	}
}

func TestResolveServiceValue(t *testing.T) {
	t.Parallel()

	service, longName := resolveServiceValue("ITUNES")
	if service != "iT" {
		t.Fatalf("expected iT service, got %q", service)
	}
	if longName == "" {
		t.Fatalf("expected service long name")
	}

	service, _ = resolveServiceValue("AMAZON")
	if service != "AMZN" {
		t.Fatalf("expected AMZN service, got %q", service)
	}

	service, _ = resolveServiceValue("MGM+")
	if service != "MGMP" {
		t.Fatalf("expected MGMP service, got %q", service)
	}
}

func TestApplySceneDetectionAppliesServiceFromNFO(t *testing.T) {
	t.Parallel()

	service := NewService(&stubRepo{},
		WithSceneDetector(staticSceneDetector{result: SceneResult{
			IsScene:         true,
			Service:         "iT",
			ServiceLongName: "iTunes",
		}}),
	)

	meta, err := service.applySceneDetection(context.Background(), preparationstate.State{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Service != "iT" {
		t.Fatalf("expected iT service from scene nfo, got %q", meta.Service)
	}
	if meta.ServiceLongName != "iTunes" {
		t.Fatalf("expected iTunes long name from scene nfo, got %q", meta.ServiceLongName)
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
		playlistSelectionPath: filepath.ToSlash(filepath.Clean(sourcePath)),
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	mediaInfo := &recordingMediaInfo{}
	service := NewService(repo, WithMediaInfoExporter(mediaInfo), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg), WithBDInfoService(bdinfo.New(api.NopLogger{})))

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

	discoverBDMVPlaylists = func(_ context.Context, _ string) ([]filesystem.PlaylistInfo, error) {
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
	executeFullBDInfoScan = func(_ *bdinfo.Service, _ context.Context, _ string, outputDir string) (bdinfo.ScanResult, error) {
		fullScans++
		return bdinfo.ScanResult{
			ReportPath: filepath.Join(outputDir, "BD_FULL.txt"),
			ReportText: fullReport,
		}, nil
	}
	executePlaylistBDInfo = func(_ *bdinfo.Service, _ context.Context, _ string, _ string, _ string, _ bool) (string, error) {
		playlistScans++
		return "", errors.New("unexpected playlist scan")
	}
	parseBDInfoOutput = func(_ *bdinfo.Service, filePath string) (map[string]any, error) {
		payload, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read BDInfo output fixture: %w", err)
		}
		return map[string]any{"summary": string(payload)}, nil
	}

	meta, err := service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{
		SourcePath: sourcePath,
	}))
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}

	if fullScans != 1 {
		t.Fatalf("expected 1 full scan, got %d", fullScans)
	}
	if playlistScans != 0 {
		t.Fatalf("expected 0 playlist scans, got %d", playlistScans)
	}
	wantMainFile := filepath.Join(bdmvPath, "STREAM", "00003.m2ts")
	if got, want := meta.VideoPath, wantMainFile; got != want {
		t.Fatalf("expected main file %q, got %q", want, got)
	}
	if mediaInfo.request.VideoPath != wantMainFile {
		t.Fatalf("expected mediainfo target %q, got %q", wantMainFile, mediaInfo.request.VideoPath)
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

func TestResolveBDMVPlaylistSelectionRejectsUnknownDirectPlaylist(t *testing.T) {
	originalDiscover := discoverBDMVPlaylists
	t.Cleanup(func() {
		discoverBDMVPlaylists = originalDiscover
	})

	discoverBDMVPlaylists = func(_ context.Context, _ string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
		}, nil
	}

	_, err := (&Service{}).resolveBDMVPlaylistSelection(context.Background(), preparationstate.Request{
		Input: api.PrepareInput{Instructions: api.ReleaseFactInstructions{Playlist: api.PlaylistInstruction{
			Set:      true,
			Selected: []string{"00001.MPLS", "00002.MPLS"},
		}}},
		Layout: sourcelayout.Layout{SourcePath: `D:\Disc`, BDMVRoot: `D:\Disc\BDMV`},
	})
	var invalid *api.InvalidPlaylistSelectionError
	if !errors.As(err, &invalid) || invalid.Playlist != "00002.MPLS" {
		t.Fatalf("expected typed missing playlist error for 00002.MPLS, got %v", err)
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
		playlistSelectionPath: filepath.ToSlash(filepath.Clean(sourcePath)),
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

	discoverBDMVPlaylists = func(_ context.Context, _ string) ([]filesystem.PlaylistInfo, error) {
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
	executeFullBDInfoScan = func(_ *bdinfo.Service, _ context.Context, _ string, _ string) (bdinfo.ScanResult, error) {
		fullScans++
		return bdinfo.ScanResult{}, errors.New("unexpected full scan")
	}
	executePlaylistBDInfo = func(_ *bdinfo.Service, _ context.Context, _ string, _ string, _ string, _ bool) (string, error) {
		playlistScans++
		return "", errors.New("unexpected playlist scan")
	}
	parseBDInfoOutput = func(_ *bdinfo.Service, filePath string) (map[string]any, error) {
		payload, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read BDInfo output fixture: %w", err)
		}
		return map[string]any{"summary": string(payload)}, nil
	}

	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, preparationstate.State{}, sourcePath)
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp dir: %v", err)
	}
	writeBDMVSummaryFixture(t, tmpDir, "00001.MPLS", "extended summary one")
	writeBDMVSummaryFixture(t, tmpDir, "00002.MPLS", "extended summary two")

	meta, err := service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{
		SourcePath: sourcePath,
	}))
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
	got, ok := meta.BDInfo["summary"].(string)
	if !ok {
		t.Fatalf("expected BDInfo summary string, got %T", meta.BDInfo["summary"])
	}
	if !strings.Contains(got, "Playlist: 00002.MPLS") {
		t.Fatalf("expected cached canonical summary for first selected playlist, got %#v", meta.BDInfo)
	}
}

func TestPrepareBDMVDirectPlaylistInvokesBDInfoForParentAndRoot(t *testing.T) {
	base := t.TempDir()
	sourcePath := filepath.Join(base, "disc")
	bdmvPath := filepath.Join(sourcePath, "BDMV")
	if err := os.MkdirAll(filepath.Join(bdmvPath, "PLAYLIST"), 0o755); err != nil {
		t.Fatalf("mkdir playlist failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(bdmvPath, "STREAM"), 0o755); err != nil {
		t.Fatalf("mkdir stream failed: %v", err)
	}

	repo := &stubRepo{}
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

	discoverBDMVPlaylists = func(_ context.Context, _ string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
		}, nil
	}
	parseBDMVPlaylist = func(_ string) (float64, []filesystem.PlaylistItem, error) {
		return 5400, []filesystem.PlaylistItem{
			{File: "00001.m2ts", Size: 100},
		}, nil
	}

	fullScans := 0
	playlistScans := 0
	var scannedRoots []string
	var scannedPlaylists []string
	dummyFullReport := strings.Join([]string{
		"DISC INFO:",
		"DISC LABEL: DISC-ONE",
		"FILES:",
		"-------------",
		"00001.m2ts        01:30:00     1,000,000,000",
		"CHAPTERS:",
		"[code]",
		"table one",
		"[/code]",
		"[code]",
		"extended summary one",
		"[/code]",
		"QUICK SUMMARY:",
		"Playlist: 00001.MPLS",
		"Disc Label: DISC-ONE",
		"Length: 01:30:00.000",
	}, "\n") + "\n"

	executeFullBDInfoScan = func(_ *bdinfo.Service, _ context.Context, _ string, _ string) (bdinfo.ScanResult, error) {
		fullScans++
		return bdinfo.ScanResult{}, errors.New("unexpected full scan")
	}
	executePlaylistBDInfo = func(_ *bdinfo.Service, _ context.Context, root string, playlist string, outputPath string, summaryOnly bool) (string, error) {
		playlistScans++
		scannedRoots = append(scannedRoots, root)
		scannedPlaylists = append(scannedPlaylists, playlist)
		if summaryOnly {
			return "", errors.New("expected full scan, got summaryOnly = true")
		}
		// Simulate writing the full report to the outputPath
		if err := os.WriteFile(outputPath, []byte(dummyFullReport), 0o600); err != nil {
			return "", fmt.Errorf("write dummy full report: %w", err)
		}
		return outputPath, nil
	}
	parseBDInfoOutput = func(_ *bdinfo.Service, filePath string) (map[string]any, error) {
		payload, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read BDInfo output fixture: %w", err)
		}
		return map[string]any{"summary": string(payload)}, nil
	}

	for _, requestedSource := range []string{sourcePath, bdmvPath} {
		meta, err := service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{
			SourcePath: requestedSource,
			PlaylistInstruction: api.PlaylistInstruction{
				Set:      true,
				Selected: []string{"00001.MPLS"},
			},
		}))
		if err != nil {
			t.Fatalf("prepare %q: %v", requestedSource, err)
		}
		if !sameFilesystemPath(meta.SourcePath, requestedSource) {
			t.Fatalf("prepared source = %q, want %q", meta.SourcePath, requestedSource)
		}
		tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
		if err != nil {
			t.Fatalf("tmp root: %v", err)
		}
		tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, requestedSource)
		if err != nil {
			t.Fatalf("tmp dir: %v", err)
		}
		assertFileContains(t, paths.BDMVSummaryPath(tmpDir, "00001.MPLS"), "Playlist: 00001.MPLS")
		assertFileContains(t, paths.BDMVExtSummaryPath(tmpDir, "00001.MPLS"), "extended summary one")
		assertFileContains(t, paths.BDMVFullSummaryPath(tmpDir, "00001.MPLS"), "QUICK SUMMARY:")

		got, ok := meta.BDInfo["summary"].(string)
		if !ok {
			t.Fatalf("expected BDInfo summary string, got %T", meta.BDInfo["summary"])
		}
		if !strings.Contains(got, "Playlist: 00001.MPLS") {
			t.Fatalf("unexpected summary in BDInfo: %v", got)
		}
	}

	if fullScans != 0 {
		t.Fatalf("expected no full scan, got %d", fullScans)
	}
	if playlistScans != 2 {
		t.Fatalf("expected exactly 2 playlist scans, got %d", playlistScans)
	}
	for index := range scannedRoots {
		if !sameFilesystemPath(scannedRoots[index], bdmvPath) || scannedPlaylists[index] != "00001.MPLS" {
			t.Fatalf("BDInfo call %d root=%q playlist=%q", index, scannedRoots[index], scannedPlaylists[index])
		}
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
		playlistSelectionPath: filepath.ToSlash(filepath.Clean(sourcePath)),
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}
	service := NewService(repo, WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}), WithConfig(cfg), WithBDInfoService(bdinfo.New(api.NopLogger{})))

	originalDiscover := discoverBDMVPlaylists
	t.Cleanup(func() {
		discoverBDMVPlaylists = originalDiscover
	})
	discoverBDMVPlaylists = func(_ context.Context, _ string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
			{File: "00002.MPLS", Duration: 6000},
		}, nil
	}

	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, preparationstate.State{}, sourcePath)
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp dir: %v", err)
	}
	writeBDMVSummaryFixture(t, tmpDir, "00001.MPLS", "extended summary one")

	_, err = service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{
		SourcePath: sourcePath,
		Options: api.UploadOptions{InteractionMode: api.InteractionModeInteractive},
	}))
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
		playlistSelectionPath: filepath.ToSlash(filepath.Clean(sourcePath)),
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
	discoverBDMVPlaylists = func(_ context.Context, _ string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{
			{File: "00001.MPLS", Duration: 5400},
			{File: "00002.MPLS", Duration: 6000},
		}, nil
	}
	parseBDMVPlaylist = func(_ string) (float64, []filesystem.PlaylistItem, error) {
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
	executeFullBDInfoScan = func(_ *bdinfo.Service, _ context.Context, _ string, outputDir string) (bdinfo.ScanResult, error) {
		fullScans++
		return bdinfo.ScanResult{
			ReportPath: filepath.Join(outputDir, "BD_FULL.txt"),
			ReportText: fullReport,
		}, nil
	}
	parseBDInfoOutput = func(_ *bdinfo.Service, filePath string) (map[string]any, error) {
		payload, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read BDInfo output fixture: %w", err)
		}
		return map[string]any{"summary": string(payload)}, nil
	}

	tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, preparationstate.State{}, sourcePath)
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir tmp dir: %v", err)
	}
	writeBDMVSummaryFixture(t, tmpDir, "00001.MPLS", "extended summary one")

	_, err = service.collectSourceEvidence(context.Background(), testCollectionRequest(t, api.Request{
		SourcePath:        sourcePath,
		ConfirmBDMVRescan: true,
		Options:           api.UploadOptions{InteractionMode: api.InteractionModeInteractive},
	}))
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

type recordingMediaInfo struct {
	request mediainfo.Request
}

func (r *recordingMediaInfo) Export(_ context.Context, req mediainfo.Request) (mediainfo.Result, error) {
	r.request = req
	return mediainfo.Result{}, nil
}

type recordingLogger struct {
	api.NopLogger
	warnings []string
}

func (r *recordingLogger) Warnf(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

type stubSceneDetector struct{}

func (stubSceneDetector) Detect(context.Context, preparationstate.State) (SceneResult, error) {
	return SceneResult{}, nil
}

type staticSceneDetector struct {
	result SceneResult
	err    error
}

func (s staticSceneDetector) Detect(context.Context, preparationstate.State) (SceneResult, error) {
	return s.result, s.err
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

func (s *stubRepo) GetExternalIdentity(context.Context, string) (db.Identity, error) {
	return db.Identity{}, internalerrors.ErrNotFound
}

func (s *stubRepo) SaveExternalIdentity(context.Context, db.Identity) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) GetExternalMetadata(context.Context, string) (db.ProviderMetadata, error) {
	return db.ProviderMetadata{}, internalerrors.ErrNotFound
}

func (s *stubRepo) SaveExternalMetadata(context.Context, db.ProviderMetadata) error {
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

func (s *stubRepo) GetDescriptionOverride(context.Context, string, string) (db.DescriptionOverride, error) {
	return db.DescriptionOverride{}, internalerrors.ErrNotFound
}
func (s *stubRepo) ListDescriptionOverridesByPath(context.Context, string) ([]db.DescriptionOverride, error) {
	return nil, internalerrors.ErrNotFound
}

func (s *stubRepo) SaveDescriptionOverride(context.Context, db.DescriptionOverride) error {
	return internalerrors.ErrNotImplemented
}

func (s *stubRepo) DeleteDescriptionOverride(context.Context, string, string) error {
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

func sameFilesystemPath(left string, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
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

func TestSafeWriteFileRejectsCrossPlatformTraversal(t *testing.T) {
	root := t.TempDir()
	if err := safeWriteFile(root, filepath.Join(root, "ok.txt"), []byte("ok")); err != nil {
		t.Fatalf("expected safe write to succeed: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "posix absolute", path: "/outside.txt"},
		{name: "windows rooted", path: `\outside.txt`},
		{name: "windows drive absolute", path: `C:\outside.txt`},
		{name: "windows drive relative", path: `C:outside.txt`},
		{name: "windows unc", path: `\\server\share\outside.txt`},
		{name: "parent escape", path: filepath.Join(root, "..", "outside.txt")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := safeWriteFile(root, tt.path, []byte("bad")); err == nil {
				t.Fatalf("expected traversal error for %q", tt.path)
			}
		})
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
	fullPath := paths.BDMVFullSummaryPath(tmpDir, playlist)
	fullReport := strings.Join([]string{
		"QUICK SUMMARY:",
		"Playlist: " + playlist,
		"[code]",
		"extended summary info",
		"[/code]",
		"[code]",
		extSummary,
		"[/code]",
	}, "\n") + "\n"
	if err := os.WriteFile(fullPath, []byte(fullReport), 0o600); err != nil {
		t.Fatalf("write full summary fixture: %v", err)
	}
}
