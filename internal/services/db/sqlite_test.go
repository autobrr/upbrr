// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

func readSchemaMigrationIDs(t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.Query(`SELECT id FROM schema_migrations ORDER BY id`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema_migrations: %v", err)
	}

	return ids
}

func TestSQLiteRepositoryCRUD(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()

	_, err = repo.GetByPath(ctx, "/missing")
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.Save(ctx, FileMetadata{
		Path:       "/media/file.mkv",
		InfoHash:   "abc",
		UpdatedAt:  now,
		DiscType:   "",
		VideoPath:  "/media/file.mkv",
		FileList:   []string{"/media/file.mkv"},
		SourceSize: 123,
		Scene:      true,
		SceneName:  "Example.Scene.Release",
		SceneIMDB:  1234567,
		Type:       "REMUX",
		Artist:     "Example Artist",
		Title:      "Example",
		Subtitle:   "Example Subtitle",
		Alt:        "Example.Alternate",
		Year:       2024,
		Month:      12,
		Day:        25,
		Source:     "BluRay",
		Resolution: "1080p",
		Codec:      []string{"H.264"},
		Audio:      []string{"DTS"},
		HDR:        []string{"HDR10"},
		Ext:        "mkv",
		Language:   []string{"EN"},
		Site:       "AMZN",
		Genre:      "Action",
		Channels:   "5.1",
		Collection: "Criterion.Collection",
		Region:     "GBR",
		Size:       "DVD5",
		Group:      "GROUP",
		Disc:       "Disc",
		Edition:    []string{"Extended"},
		Other:      []string{"Remastered"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.GetByPath(ctx, "/media/file.mkv")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.InfoHash != "abc" {
		t.Fatalf("unexpected info hash: %s", got.InfoHash)
	}
	if !got.Scene || got.SceneName == "" || got.SceneIMDB == 0 {
		t.Fatalf("unexpected scene metadata: %#v", got)
	}
	if got.SourceSize == 0 {
		t.Fatalf("unexpected source size: %d", got.SourceSize)
	}
	if got.Title == "" || got.Year == 0 || got.Type == "" {
		t.Fatalf("unexpected release metadata: %#v", got)
	}
	if got.Month == 0 || got.Day == 0 {
		t.Fatalf("unexpected release date metadata: %#v", got)
	}
	if got.Alt == "" {
		t.Fatalf("unexpected release alt metadata: %#v", got)
	}
	if got.Artist == "" || got.Subtitle == "" || got.Region == "" || got.Size == "" {
		t.Fatalf("unexpected release extended metadata: %#v", got)
	}
	if got.Resolution == "" || got.Ext == "" || got.Site == "" || got.Genre == "" || got.Channels == "" || got.Collection == "" {
		t.Fatalf("unexpected release extra metadata: %#v", got)
	}
	if len(got.Codec) == 0 || len(got.Audio) == 0 || len(got.HDR) == 0 || len(got.Language) == 0 {
		t.Fatalf("unexpected release list metadata: %#v", got)
	}

	pending, err := repo.ListPendingUploads(ctx)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending uploads, got %d", len(pending))
	}

	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "BLU", Status: "pending", SourcePath: "/tmp/file"}); err != nil {
		t.Fatalf("create upload record: %v", err)
	}

	pending, err = repo.ListPendingUploads(ctx)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending upload, got %d", len(pending))
	}
	if pending[0].SourcePath != "/tmp/file" {
		t.Fatalf("unexpected source path: %s", pending[0].SourcePath)
	}

	if _, err := repo.GetTrackerTimestamp(ctx, "BLU"); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected missing tracker timestamp, got %v", err)
	}
	stamp := time.Now().UTC().Truncate(time.Second)
	if err := repo.SaveTrackerTimestamp(ctx, TrackerTimestamp{Tracker: "BLU", UpdatedAt: stamp}); err != nil {
		t.Fatalf("save tracker timestamp: %v", err)
	}
	gotStamp, err := repo.GetTrackerTimestamp(ctx, "BLU")
	if err != nil {
		t.Fatalf("get tracker timestamp: %v", err)
	}
	if gotStamp.IsZero() {
		t.Fatalf("expected tracker timestamp, got zero")
	}

	if err := repo.SaveTrackerMetadata(ctx, TrackerMetadata{
		SourcePath:  "/media/file.mkv",
		Tracker:     "BLU",
		TrackerID:   "123",
		InfoHash:    "hash",
		TMDBID:      1,
		IMDBID:      2,
		TVDBID:      3,
		MALID:       4,
		Category:    "MOVIE",
		Description: "example",
		ImageURLs:   []string{"https://example.com/a.jpg"},
		Filename:    "example.mkv",
		Matched:     true,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("save tracker metadata: %v", err)
	}

	trackerData, err := repo.ListTrackerMetadataByPath(ctx, "/media/file.mkv")
	if err != nil {
		t.Fatalf("list tracker metadata: %v", err)
	}
	if len(trackerData) != 1 {
		t.Fatalf("expected 1 tracker metadata row, got %d", len(trackerData))
	}
	if trackerData[0].TrackerID != "123" || trackerData[0].Tracker != "BLU" {
		t.Fatalf("unexpected tracker metadata: %#v", trackerData[0])
	}

	idsStamp := time.Now().UTC().Truncate(time.Second)
	if err := repo.SaveExternalIDs(ctx, ExternalIDs{
		SourcePath:   "/media/file.mkv",
		TMDBID:       100,
		IMDBID:       200,
		TVDBID:       300,
		TVmazeID:     400,
		Category:     "MOVIE",
		SourceTMDB:   "tracker",
		SourceIMDB:   "mediainfo",
		SourceTVDB:   "tmdb",
		SourceTVmaze: "tvmaze",
		UpdatedAt:    idsStamp,
	}); err != nil {
		t.Fatalf("save external ids: %v", err)
	}

	ids, err := repo.GetExternalIDs(ctx, "/media/file.mkv")
	if err != nil {
		t.Fatalf("get external ids: %v", err)
	}
	if ids.TMDBID != 100 || ids.IMDBID != 200 || ids.TVDBID != 300 || ids.TVmazeID != 400 {
		t.Fatalf("unexpected external ids: %#v", ids)
	}

	if err := repo.SaveDVDMediaInfo(ctx, DVDMediaInfo{
		SourcePath:      "/media/file.mkv",
		IFOPath:         "/media/VIDEO_TS/VTS_01_0.IFO",
		VOBPath:         "/media/VIDEO_TS/VTS_01_1.VOB",
		VOBSet:          "01",
		Width:           720,
		Height:          480,
		FrameRate:       "29.970",
		ScanType:        "i",
		Resolution:      "480i",
		HighFrameRate:   false,
		MediaInfoJSON:   "/tmp/MediaInfo.json",
		MediaInfoText:   "/tmp/mediainfo.txt",
		VOBMediaInfoRaw: "raw text",
		UpdatedAt:       idsStamp,
	}); err != nil {
		t.Fatalf("save dvd mediainfo: %v", err)
	}

	dvdInfo, err := repo.GetDVDMediaInfo(ctx, "/media/file.mkv")
	if err != nil {
		t.Fatalf("get dvd mediainfo: %v", err)
	}
	if dvdInfo.IFOPath == "" || dvdInfo.VOBPath == "" || dvdInfo.Resolution != "480i" {
		t.Fatalf("unexpected dvd mediainfo: %#v", dvdInfo)
	}

	if err := repo.SaveExternalMetadata(ctx, ExternalMetadata{
		SourcePath: "/media/file.mkv",
		TMDB: &TMDBMetadata{
			TMDBID:           100,
			Title:            "Example",
			Year:             2024,
			TMDBType:         "Movie",
			Overview:         "Example overview",
			Poster:           "https://example.com/poster.jpg",
			OriginalLanguage: "en",
		},
		IMDB: &IMDBMetadata{
			IMDBID: 200,
			Title:  "Example",
			Year:   2024,
			Type:   "movie",
		},
		UpdatedAt: idsStamp,
	}); err != nil {
		t.Fatalf("save external metadata: %v", err)
	}

	metadata, err := repo.GetExternalMetadata(ctx, "/media/file.mkv")
	if err != nil {
		t.Fatalf("get external metadata: %v", err)
	}
	if metadata.TMDB == nil || metadata.TMDB.TMDBID != 100 || metadata.IMDB == nil || metadata.IMDB.IMDBID != 200 {
		t.Fatalf("unexpected external metadata: %#v", metadata)
	}

	if err := repo.SaveReleaseNameOverrides(ctx, "/media/file.mkv", ReleaseNameOverrides{
		Category:         stringPtr("MOVIE"),
		Type:             stringPtr("REMUX"),
		Source:           stringPtr("BluRay"),
		Tag:              stringPtr("-GROUP"),
		ManualYear:       intPtr(2025),
		UseSeasonEpisode: boolPtr(true),
		NoAKA:            boolPtr(true),
		Region:           stringPtr("A"),
	}); err != nil {
		t.Fatalf("save release overrides: %v", err)
	}

	gotOverrides, err := repo.GetReleaseNameOverrides(ctx, "/media/file.mkv")
	if err != nil {
		t.Fatalf("get release overrides: %v", err)
	}
	if gotOverrides.Category == nil || *gotOverrides.Category != "MOVIE" {
		t.Fatalf("unexpected category override: %#v", gotOverrides)
	}
	if gotOverrides.ManualYear == nil || *gotOverrides.ManualYear != 2025 {
		t.Fatalf("unexpected manual year override: %#v", gotOverrides)
	}
	if gotOverrides.NoAKA == nil || !*gotOverrides.NoAKA {
		t.Fatalf("unexpected no aka override: %#v", gotOverrides)
	}
	if gotOverrides.UseSeasonEpisode == nil || !*gotOverrides.UseSeasonEpisode {
		t.Fatalf("unexpected use season/episode override: %#v", gotOverrides)
	}

	if err := repo.DeleteReleaseNameOverrides(ctx, "/media/file.mkv"); err != nil {
		t.Fatalf("delete release overrides: %v", err)
	}
	if _, err := repo.GetReleaseNameOverrides(ctx, "/media/file.mkv"); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected release overrides not found, got %v", err)
	}
}

func TestOpenWithLoggerConfiguresConcurrentSQLiteSettings(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "shared.db")
	repo, err := OpenWithLogger(repoPath, nopLogger{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var journalMode string
	if err := repo.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("expected WAL journal mode, got %q", journalMode)
	}

	var busyTimeout int
	if err := repo.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != sqliteBusyTimeout {
		t.Fatalf("expected busy timeout %d, got %d", sqliteBusyTimeout, busyTimeout)
	}
}

func TestSQLiteRepositoryConcurrentMigrateAndAccessOnDisk(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "shared.db")
	repos := make([]*SQLiteRepository, 0, 3)
	for i := 0; i < 3; i++ {
		repo, err := OpenWithLogger(repoPath, nopLogger{})
		if err != nil {
			t.Fatalf("open repo %d: %v", i, err)
		}
		repos = append(repos, repo)
	}
	for _, repo := range repos {
		t.Cleanup(func() {
			_ = repo.Close()
		})
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	start := make(chan struct{})
	for idx, repo := range repos {
		wg.Add(1)
		go func(idx int, repo *SQLiteRepository) {
			defer wg.Done()
			<-start
			for attempt := 0; attempt < 3; attempt++ {
				if err := repo.Migrate(); err != nil {
					errCh <- fmt.Errorf("repo %d migrate attempt %d: %w", idx, attempt, err)
					return
				}
			}
			ctx := context.Background()
			for item := 0; item < 10; item++ {
				sourcePath := filepath.Join("C:\\shared", fmt.Sprintf("release-%d-%d.mkv", idx, item))
				if err := repo.Save(ctx, FileMetadata{
					Path:      sourcePath,
					Title:     fmt.Sprintf("Title %d-%d", idx, item),
					UpdatedAt: time.Now().UTC().Truncate(time.Second),
				}); err != nil {
					errCh <- fmt.Errorf("repo %d save %d: %w", idx, item, err)
					return
				}
				if _, err := repo.ListHistoryEntries(ctx); err != nil {
					errCh <- fmt.Errorf("repo %d list history %d: %w", idx, item, err)
					return
				}
			}
		}(idx, repo)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	for idx := range repos {
		for item := 0; item < 10; item++ {
			sourcePath := filepath.Join("C:\\shared", fmt.Sprintf("release-%d-%d.mkv", idx, item))
			if _, err := repos[0].GetByPath(ctx, sourcePath); err != nil {
				t.Fatalf("get %s: %v", sourcePath, err)
			}
		}
	}
}

func TestSQLiteRepositoryConcurrentReadsOnDisk(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "readers.db")
	writerRepo, err := OpenWithLogger(repoPath, nopLogger{})
	if err != nil {
		t.Fatalf("open writer repo: %v", err)
	}
	t.Cleanup(func() {
		_ = writerRepo.Close()
	})

	if err := writerRepo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 25; i++ {
		sourcePath := filepath.Join("C:\\reads", fmt.Sprintf("release-%d.mkv", i))
		if err := writerRepo.Save(ctx, FileMetadata{
			Path:      sourcePath,
			Title:     fmt.Sprintf("Title %d", i),
			UpdatedAt: time.Now().UTC().Truncate(time.Second),
		}); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	repos := make([]*SQLiteRepository, 0, 4)
	for i := 0; i < 4; i++ {
		repo, err := OpenWithLogger(repoPath, nopLogger{})
		if err != nil {
			t.Fatalf("open reader repo %d: %v", i, err)
		}
		repos = append(repos, repo)
	}
	for _, repo := range repos {
		t.Cleanup(func() {
			_ = repo.Close()
		})
	}

	start := make(chan struct{})
	errCh := make(chan error, len(repos))
	var wg sync.WaitGroup
	for idx, repo := range repos {
		wg.Add(1)
		go func(idx int, repo *SQLiteRepository) {
			defer wg.Done()
			<-start
			for item := 0; item < 25; item++ {
				sourcePath := filepath.Join("C:\\reads", fmt.Sprintf("release-%d.mkv", item))
				got, err := repo.GetByPath(ctx, sourcePath)
				if err != nil {
					errCh <- fmt.Errorf("reader %d get %d: %w", idx, item, err)
					return
				}
				if got.Path != sourcePath {
					errCh <- fmt.Errorf("reader %d get %d: unexpected path %q", idx, item, got.Path)
					return
				}
			}
		}(idx, repo)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteRepositoryReadsOverlapWithWriteTransaction(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "reader-writer.db")
	writerRepo, err := OpenWithLogger(repoPath, nopLogger{})
	if err != nil {
		t.Fatalf("open writer repo: %v", err)
	}
	t.Cleanup(func() {
		_ = writerRepo.Close()
	})

	readerRepoA, err := OpenWithLogger(repoPath, nopLogger{})
	if err != nil {
		t.Fatalf("open reader repo A: %v", err)
	}
	t.Cleanup(func() {
		_ = readerRepoA.Close()
	})

	readerRepoB, err := OpenWithLogger(repoPath, nopLogger{})
	if err != nil {
		t.Fatalf("open reader repo B: %v", err)
	}
	t.Cleanup(func() {
		_ = readerRepoB.Close()
	})

	if err := writerRepo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	sourcePath := filepath.Join("C:\\overlap", "release.mkv")
	if err := writerRepo.Save(ctx, FileMetadata{
		Path:      sourcePath,
		Title:     "Before",
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	conn, err := writerRepo.db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire writer conn: %v", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin immediate: %v", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
	}()

	if _, err := conn.ExecContext(ctx, `
		UPDATE file_metadata
		SET release_title = ?, updated_at = ?
		WHERE path = ?
	`, "DuringWrite", time.Now().UTC().Format(time.RFC3339Nano), sourcePath); err != nil {
		t.Fatalf("update within transaction: %v", err)
	}

	start := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	readers := []*SQLiteRepository{readerRepoA, readerRepoB}
	for idx, repo := range readers {
		wg.Add(1)
		go func(idx int, repo *SQLiteRepository) {
			defer wg.Done()
			<-start
			for attempt := 0; attempt < 20; attempt++ {
				got, err := repo.GetByPath(ctx, sourcePath)
				if err != nil {
					errCh <- fmt.Errorf("reader %d get attempt %d: %w", idx, attempt, err)
					return
				}
				if got.Title != "Before" {
					errCh <- fmt.Errorf("reader %d get attempt %d: expected committed title Before, got %q", idx, attempt, got.Title)
					return
				}
			}
		}(idx, repo)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetryBusyContextStopsOnCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	err := retryBusyContext(ctx, nil, "test", 3, func() error {
		attempts++
		return errors.New("database is locked")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if attempts != 0 {
		t.Fatalf("expected no attempts after cancellation, got %d", attempts)
	}
}

func TestIsBusyErrorUnwrapsSQLiteErrors(t *testing.T) {
	t.Parallel()

	repoPath := filepath.Join(t.TempDir(), "busy.db")

	lockingRepo, err := OpenWithLogger(repoPath, nopLogger{})
	if err != nil {
		t.Fatalf("open locking repo: %v", err)
	}
	t.Cleanup(func() {
		_ = lockingRepo.Close()
	})

	contendingRepo, err := OpenWithLogger(repoPath, nopLogger{})
	if err != nil {
		t.Fatalf("open contending repo: %v", err)
	}
	t.Cleanup(func() {
		_ = contendingRepo.Close()
	})

	if err := lockingRepo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := contendingRepo.db.Exec(`PRAGMA busy_timeout = 1`); err != nil {
		t.Fatalf("set contending busy_timeout: %v", err)
	}

	ctx := context.Background()
	conn, err := lockingRepo.db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire locking conn: %v", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin immediate: %v", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
	}()

	err = contendingRepo.Save(ctx, FileMetadata{
		Path:      filepath.Join("C:\\locked", "file.mkv"),
		Title:     "Locked",
		UpdatedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected busy error, got nil")
	}

	wrappedErr := fmt.Errorf("wrapped save: %w", err)
	if !IsBusyError(wrappedErr) {
		t.Fatalf("expected wrapped busy error, got %v", wrappedErr)
	}
}

func TestTrackerRuleFailuresCRUD(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	path := "/tmp/source"
	tracker := "AITHER"

	failures := []TrackerRuleFailure{{
		SourcePath: path,
		Tracker:    tracker,
		Rule:       "require_unique_id",
		Reason:     "missing MediaInfo Unique ID",
	}}
	if err := repo.SaveTrackerRuleFailures(ctx, path, tracker, failures); err != nil {
		t.Fatalf("save tracker rule failures: %v", err)
	}

	stored, err := repo.ListTrackerRuleFailuresByPath(ctx, path)
	if err != nil {
		t.Fatalf("list tracker rule failures: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected 1 tracker rule failure, got %d", len(stored))
	}
	if stored[0].Rule != "require_unique_id" || stored[0].Tracker != tracker {
		t.Fatalf("unexpected rule failure: %#v", stored[0])
	}

	if err := repo.SaveTrackerRuleFailures(ctx, path, tracker, nil); err != nil {
		t.Fatalf("clear tracker rule failures: %v", err)
	}
	stored, err = repo.ListTrackerRuleFailuresByPath(ctx, path)
	if err != nil {
		t.Fatalf("list tracker rule failures after clear: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("expected no tracker rule failures, got %d", len(stored))
	}
}

func TestSQLiteDescriptionOverrides(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	_, err = repo.GetDescriptionOverride(ctx, "/missing", "")
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}

	override := DescriptionOverride{SourcePath: "/media/file.mkv", Description: "Example desc"}
	if err := repo.SaveDescriptionOverride(ctx, override); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.GetDescriptionOverride(ctx, "/media/file.mkv", "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Description != "Example desc" {
		t.Fatalf("unexpected description: %q", got.Description)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatalf("expected updated_at to be set")
	}

	if err := repo.DeleteDescriptionOverride(ctx, "/media/file.mkv", ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.GetDescriptionOverride(ctx, "/media/file.mkv", "")
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestMigrateV7NormalizesCorruptLegacyDescriptionOverrides(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	dbConn := repo.db

	statements := []string{
		`PRAGMA user_version = 6`,
		`CREATE TABLE description_overrides (source_path TEXT, description TEXT, updated_at TEXT)`,
		`INSERT INTO description_overrides (source_path, description, updated_at) VALUES (NULL, 'broken', NULL)`,
		`INSERT INTO description_overrides (source_path, description, updated_at) VALUES ('   ', 'blank path', '')`,
		`INSERT INTO description_overrides (source_path, description, updated_at) VALUES ('/tmp/source', NULL, NULL)`,
		`INSERT INTO description_overrides (source_path, description, updated_at) VALUES ('/tmp/source', 'latest desc', '')`,
	}
	for _, statement := range statements {
		if _, err := dbConn.Exec(statement); err != nil {
			t.Fatalf("seed legacy schema: %v", err)
		}
	}

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	override, err := repo.GetDescriptionOverride(ctx, "/tmp/source", "")
	if err != nil {
		t.Fatalf("expected migrated override, got %v", err)
	}
	if override.Description != "latest desc" {
		t.Fatalf("expected duplicate legacy rows to collapse to latest description, got %q", override.Description)
	}
	if override.UpdatedAt.IsZero() {
		t.Fatalf("expected missing updated_at to be backfilled")
	}

	overrides, err := repo.ListDescriptionOverridesByPath(ctx, "/tmp/source")
	if err != nil {
		t.Fatalf("list overrides: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("expected one migrated override, got %d", len(overrides))
	}

	if _, err := repo.GetDescriptionOverride(ctx, "", ""); !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input for blank path lookup, got %v", err)
	}

	row := dbConn.QueryRow(`SELECT COUNT(*) FROM description_overrides`)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("count migrated overrides: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected only valid legacy rows to migrate, got %d", count)
	}

	var updatedAt string
	if err := dbConn.QueryRow(`SELECT updated_at FROM description_overrides WHERE source_path = ? AND group_key = ?`, "/tmp/source", "").Scan(&updatedAt); err != nil {
		t.Fatalf("read migrated updated_at: %v", err)
	}
	if strings.TrimSpace(updatedAt) == "" {
		t.Fatalf("expected non-empty updated_at after migration")
	}
}

func TestSQLiteMigrationBootstrapRecordsLedgerAndCompatibilityStamp(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	ids := readSchemaMigrationIDs(t, rawDB)
	if len(ids) != len(migrationRegistry) {
		t.Fatalf("expected %d recorded migrations, got %d", len(migrationRegistry), len(ids))
	}

	var userVersion int
	if err := rawDB.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion != legacyCompatibilitySchemaVersion {
		t.Fatalf("expected legacy user_version %d, got %d", legacyCompatibilitySchemaVersion, userVersion)
	}
}

func TestSQLiteMigrationLegacyV8BridgeIsIdempotent(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	ctx := context.Background()
	if err := createBaselineSchema(ctx, rawDB); err != nil {
		t.Fatalf("create baseline schema: %v", err)
	}
	if err := migrateAddDVDMediaInfo(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v2: %v", err)
	}
	if err := migrateAddReleaseOverrideUseSeasonEpisode(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v3: %v", err)
	}
	if err := migrateAddHistoryIndexes(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v4: %v", err)
	}
	if err := migrateBackfillUploadedImageUsageScope(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v5: %v", err)
	}
	if err := migrateAddScreenshotSlotTables(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v6: %v", err)
	}
	if err := migrateNormalizeDescriptionOverrides(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v7: %v", err)
	}
	if err := migrateAddTrackerCookies(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v8: %v", err)
	}
	if _, err := rawDB.Exec(`PRAGMA user_version = 8`); err != nil {
		t.Fatalf("set legacy user_version: %v", err)
	}

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := Migrate(rawDB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	ids := readSchemaMigrationIDs(t, rawDB)
	if len(ids) != len(migrationRegistry) {
		t.Fatalf("expected %d recorded migrations after bridge, got %d", len(migrationRegistry), len(ids))
	}

	var userVersion int
	if err := rawDB.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version after bridge: %v", err)
	}
	if userVersion != legacyCompatibilitySchemaVersion {
		t.Fatalf("expected legacy compatibility user_version %d after bridge, got %d", legacyCompatibilitySchemaVersion, userVersion)
	}
}

func TestSQLiteMigrationBridgesLegacyV8AndAppliesReleaseCategory(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	ctx := context.Background()
	if err := createBaselineSchema(ctx, rawDB); err != nil {
		t.Fatalf("create baseline schema: %v", err)
	}
	if err := migrateAddDVDMediaInfo(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v2: %v", err)
	}
	if err := migrateAddReleaseOverrideUseSeasonEpisode(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v3: %v", err)
	}
	if err := migrateAddHistoryIndexes(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v4: %v", err)
	}
	if err := migrateBackfillUploadedImageUsageScope(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v5: %v", err)
	}
	if err := migrateAddScreenshotSlotTables(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v6: %v", err)
	}
	if err := migrateNormalizeDescriptionOverrides(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy v7: %v", err)
	}
	if err := migrateAddTrackerCookies(ctx, rawDB); err != nil {
		t.Fatalf("apply legacy feat/encryption v8: %v", err)
	}
	if _, err := rawDB.Exec(`PRAGMA user_version = 8`); err != nil {
		t.Fatalf("set legacy user_version: %v", err)
	}

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("migrate bridged legacy v8 db: %v", err)
	}

	if ok, err := tableColumnExists(ctx, rawDB, "file_metadata", "release_category"); err != nil {
		t.Fatalf("check release_category column: %v", err)
	} else if !ok {
		t.Fatal("expected release_category column to be added after bridging legacy v8 db")
	}

	ids := readSchemaMigrationIDs(t, rawDB)
	expected := []string{"2026_04_add_tracker_cookies", "2026_04_add_release_category"}
	for _, id := range expected {
		found := false
		for _, got := range ids {
			if got == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected migration %q to be recorded after v8 bridge, got %v", id, ids)
		}
	}

	var userVersion int
	if err := rawDB.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read user_version after v8 bridge: %v", err)
	}
	if userVersion != legacyCompatibilitySchemaVersion {
		t.Fatalf("expected compatibility user_version %d after v8 bridge, got %d", legacyCompatibilitySchemaVersion, userVersion)
	}
}

func TestSQLiteMigrationKeepsLegacyRollbackCompatibilityStamp(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	var userVersion int
	if err := rawDB.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read compatibility user_version: %v", err)
	}
	if userVersion != legacyCompatibilitySchemaVersion {
		t.Fatalf("expected compatibility user_version %d, got %d", legacyCompatibilitySchemaVersion, userVersion)
	}

	// Simulate the old integer-version runner contract: a rollback binary should
	// classify this DB as already migrated past the non-idempotent v3 ALTER.
	if userVersion < 3 {
		t.Fatalf("rollback binary would misclassify db as pre-v3, got user_version %d", userVersion)
	}
	if _, err := rawDB.Exec(`ALTER TABLE release_overrides ADD COLUMN use_season_episode INTEGER`); err == nil {
		t.Fatalf("expected legacy v3 ALTER to be unsafe if rerun, but it unexpectedly succeeded")
	}
}

func TestSQLiteMigrationUnknownAppliedIDsAreTolerated(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO schema_migrations (id, applied_at) VALUES (?, ?)`, "branch_only_future_migration", "2026-04-18T00:00:00Z"); err != nil {
		t.Fatalf("insert unknown migration: %v", err)
	}

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("rerun migrate with unknown applied id: %v", err)
	}
}

func TestSQLiteMigrationBranchesCanApplyDisjointLedgerMigrations(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	registryA := []migrationStep{
		{id: baselineMigrationID, apply: createBaselineSchema},
		{id: "branch_a_feature", dependsOn: []string{baselineMigrationID}, apply: func(context.Context, migrationExecutor) error { return nil }},
	}
	registryB := []migrationStep{
		{id: baselineMigrationID, apply: createBaselineSchema},
		{id: "branch_b_feature", dependsOn: []string{baselineMigrationID}, apply: func(context.Context, migrationExecutor) error { return nil }},
	}

	if err := migrateContextWithRegistry(context.Background(), rawDB, registryA); err != nil {
		t.Fatalf("migrate branch A: %v", err)
	}
	if err := migrateContextWithRegistry(context.Background(), rawDB, registryB); err != nil {
		t.Fatalf("migrate branch B: %v", err)
	}
	if err := migrateContextWithRegistry(context.Background(), rawDB, registryA); err != nil {
		t.Fatalf("migrate branch A again: %v", err)
	}

	ids := readSchemaMigrationIDs(t, rawDB)
	expected := []string{baselineMigrationID, "branch_a_feature", "branch_b_feature"}
	if len(ids) != len(expected) {
		t.Fatalf("expected %d migrations after branch sharing, got %d (%v)", len(expected), len(ids), ids)
	}
	for _, id := range expected {
		found := false
		for _, got := range ids {
			if got == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected migration %q to be recorded, got %v", id, ids)
		}
	}
}

func TestSQLiteMigrationFailsWhenAppliedMigrationIsMissingDependency(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	if err := ensureSchemaMigrationsTable(context.Background(), rawDB); err != nil {
		t.Fatalf("ensure schema_migrations: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO schema_migrations (id, applied_at) VALUES (?, ?)`, "child", "2026-04-18T00:00:00Z"); err != nil {
		t.Fatalf("insert inconsistent applied migration: %v", err)
	}

	registry := []migrationStep{
		{id: baselineMigrationID, apply: createBaselineSchema},
		{id: "child", dependsOn: []string{baselineMigrationID}, apply: func(context.Context, migrationExecutor) error { return nil }},
	}

	err = migrateContextWithRegistry(context.Background(), rawDB, registry)
	if err == nil || !strings.Contains(err.Error(), `missing dependency`) {
		t.Fatalf("expected missing dependency error, got %v", err)
	}
}

func TestValidatedMigrationRegistryRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()

	duplicate := []migrationStep{
		{id: baselineMigrationID, apply: createBaselineSchema},
		{id: baselineMigrationID, apply: func(context.Context, migrationExecutor) error { return nil }},
	}
	if _, err := validatedMigrationRegistry(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate migration id") {
		t.Fatalf("expected duplicate migration id error, got %v", err)
	}

	missingDependency := []migrationStep{
		{id: baselineMigrationID, apply: createBaselineSchema},
		{id: "child", dependsOn: []string{"missing"}, apply: func(context.Context, migrationExecutor) error { return nil }},
	}
	if _, err := validatedMigrationRegistry(missingDependency); err == nil || !strings.Contains(err.Error(), "depends on unknown migration") {
		t.Fatalf("expected missing dependency definition error, got %v", err)
	}

	cycle := []migrationStep{
		{id: baselineMigrationID, dependsOn: []string{"child"}, apply: createBaselineSchema},
		{id: "child", dependsOn: []string{baselineMigrationID}, apply: func(context.Context, migrationExecutor) error { return nil }},
	}
	if _, err := validatedMigrationRegistry(cycle); err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("expected dependency cycle error, got %v", err)
	}
}
func TestSQLiteDescriptionOverrideGroupKeysAreCaseInsensitive(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	override := DescriptionOverride{
		SourcePath:  "/media/file.mkv",
		GroupKey:    " HDB|HDB|TRACKER:HDB ",
		Description: "Example desc",
	}
	if err := repo.SaveDescriptionOverride(ctx, override); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := repo.GetDescriptionOverride(ctx, "/media/file.mkv", "hdb|hdb|tracker:hdb")
	if err != nil {
		t.Fatalf("get lowercase: %v", err)
	}
	if got.GroupKey != "hdb|hdb|tracker:hdb" {
		t.Fatalf("expected normalized group key, got %q", got.GroupKey)
	}
	if got.Description != "Example desc" {
		t.Fatalf("unexpected description: %q", got.Description)
	}

	overrides, err := repo.ListDescriptionOverridesByPath(ctx, "/media/file.mkv")
	if err != nil {
		t.Fatalf("list overrides: %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("expected one override, got %d", len(overrides))
	}
	if overrides[0].GroupKey != "hdb|hdb|tracker:hdb" {
		t.Fatalf("expected listed group key normalized, got %q", overrides[0].GroupKey)
	}

	if err := repo.DeleteDescriptionOverride(ctx, "/media/file.mkv", "HDB|HDB|TRACKER:HDB"); err != nil {
		t.Fatalf("delete uppercase: %v", err)
	}
	if _, err := repo.GetDescriptionOverride(ctx, "/media/file.mkv", "hdb|hdb|tracker:hdb"); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestSQLitePurgeContentData(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	targetPath := "/media/file.mkv"
	otherPath := "/media/other.mkv"
	now := time.Now().UTC().Truncate(time.Second)

	if err := repo.Save(ctx, FileMetadata{Path: targetPath, InfoHash: "hash-a", UpdatedAt: now}); err != nil {
		t.Fatalf("save target metadata: %v", err)
	}
	if err := repo.Save(ctx, FileMetadata{Path: otherPath, InfoHash: "hash-b", UpdatedAt: now}); err != nil {
		t.Fatalf("save other metadata: %v", err)
	}
	if err := repo.SaveExternalIDs(ctx, ExternalIDs{SourcePath: targetPath, TMDBID: 100, UpdatedAt: now}); err != nil {
		t.Fatalf("save external ids: %v", err)
	}
	if err := repo.SaveExternalMetadata(ctx, ExternalMetadata{
		SourcePath: targetPath,
		TMDB:       &TMDBMetadata{TMDBID: 100, Title: "Example"},
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("save external metadata: %v", err)
	}
	if err := repo.SaveReleaseNameOverrides(ctx, targetPath, ReleaseNameOverrides{Category: stringPtr("MOVIE")}); err != nil {
		t.Fatalf("save release overrides: %v", err)
	}
	if err := repo.SaveDescriptionOverride(ctx, DescriptionOverride{SourcePath: targetPath, Description: "desc"}); err != nil {
		t.Fatalf("save description override: %v", err)
	}
	if err := repo.SavePlaylistSelection(ctx, targetPath, []string{"00001.mpls"}, false); err != nil {
		t.Fatalf("save playlist selection: %v", err)
	}
	if err := repo.SaveTrackerMetadata(ctx, TrackerMetadata{
		SourcePath: targetPath,
		Tracker:    "BLU",
		TrackerID:  "123",
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("save tracker metadata: %v", err)
	}
	if err := repo.SaveTrackerRuleFailures(ctx, targetPath, "BLU", []TrackerRuleFailure{{
		SourcePath: targetPath,
		Tracker:    "BLU",
		Rule:       "rule",
		Reason:     "reason",
		CreatedAt:  now,
	}}); err != nil {
		t.Fatalf("save rule failures: %v", err)
	}
	if err := repo.SaveScreenshot(ctx, Screenshot{
		SourcePath: targetPath,
		ImagePath:  "/tmp/target-01.png",
		Purpose:    ScreenshotPurpose("final"),
		CapturedAt: now,
	}); err != nil {
		t.Fatalf("save screenshot: %v", err)
	}
	if err := repo.SaveFinalSelections(ctx, targetPath, []ScreenshotFinalSelection{{
		ImagePath:  "/tmp/target-01.png",
		Order:      0,
		Source:     "existing",
		SelectedAt: now,
	}}); err != nil {
		t.Fatalf("save final selection: %v", err)
	}
	if err := repo.SaveUploadedImages(ctx, targetPath, "imgbox", []UploadedImageLink{{
		SourcePath: targetPath,
		ImagePath:  "/tmp/target-01.png",
		Host:       "imgbox",
		ImgURL:     "https://example.invalid/a.png",
		UploadedAt: now,
	}}); err != nil {
		t.Fatalf("save uploaded image: %v", err)
	}
	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "BLU", Status: "pending", SourcePath: targetPath}); err != nil {
		t.Fatalf("save upload record target: %v", err)
	}
	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "BLU", Status: "pending", SourcePath: otherPath}); err != nil {
		t.Fatalf("save upload record other: %v", err)
	}

	if err := repo.PurgeContentData(ctx, targetPath); err != nil {
		t.Fatalf("purge content: %v", err)
	}

	if _, err := repo.GetByPath(ctx, targetPath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected target metadata removed, got %v", err)
	}
	if _, err := repo.GetExternalIDs(ctx, targetPath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected external ids removed, got %v", err)
	}
	if _, err := repo.GetExternalMetadata(ctx, targetPath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected external metadata removed, got %v", err)
	}
	if _, err := repo.GetReleaseNameOverrides(ctx, targetPath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected release overrides removed, got %v", err)
	}
	if _, err := repo.GetDescriptionOverride(ctx, targetPath, ""); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected description override removed, got %v", err)
	}
	if _, err := repo.GetPlaylistSelection(ctx, targetPath); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected playlist selection removed, got %v", err)
	}
	if trackerData, err := repo.ListTrackerMetadataByPath(ctx, targetPath); err != nil || len(trackerData) != 0 {
		t.Fatalf("expected tracker metadata removed, got len=%d err=%v", len(trackerData), err)
	}
	if ruleFailures, err := repo.ListTrackerRuleFailuresByPath(ctx, targetPath); err != nil || len(ruleFailures) != 0 {
		t.Fatalf("expected tracker rule failures removed, got len=%d err=%v", len(ruleFailures), err)
	}
	if screenshots, err := repo.ListScreenshotsByPath(ctx, targetPath); err != nil || len(screenshots) != 0 {
		t.Fatalf("expected screenshots removed, got len=%d err=%v", len(screenshots), err)
	}
	if finals, err := repo.ListFinalSelections(ctx, targetPath); err != nil || len(finals) != 0 {
		t.Fatalf("expected final selections removed, got len=%d err=%v", len(finals), err)
	}
	if uploaded, err := repo.ListUploadedImagesByPath(ctx, targetPath); err != nil || len(uploaded) != 0 {
		t.Fatalf("expected uploaded images removed, got len=%d err=%v", len(uploaded), err)
	}

	if _, err := repo.GetByPath(ctx, otherPath); err != nil {
		t.Fatalf("expected other path untouched, got %v", err)
	}
	pending, err := repo.ListPendingUploads(ctx)
	if err != nil {
		t.Fatalf("list pending uploads: %v", err)
	}
	if len(pending) != 1 || pending[0].SourcePath != otherPath {
		t.Fatalf("expected only other upload record remaining, got %#v", pending)
	}
}

func TestSQLiteRepositoryListStoredReleasePathsIncludesOrphans(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.Save(ctx, FileMetadata{Path: "/media/a.mkv", UpdatedAt: now}); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "BLU", Status: "pending", SourcePath: "/media/orphan-upload.mkv"}); err != nil {
		t.Fatalf("create upload record: %v", err)
	}
	if err := repo.SaveScreenshot(ctx, Screenshot{
		SourcePath: "/media/orphan-shot.mkv",
		ImagePath:  "/tmp/orphan-shot.png",
		Purpose:    ScreenshotPurpose("final"),
		CapturedAt: now,
	}); err != nil {
		t.Fatalf("save screenshot: %v", err)
	}

	paths, err := repo.ListStoredReleasePaths(ctx)
	if err != nil {
		t.Fatalf("list stored release paths: %v", err)
	}

	expected := []string{"/media/a.mkv", "/media/orphan-shot.mkv", "/media/orphan-upload.mkv"}
	if len(paths) != len(expected) {
		t.Fatalf("expected %d stored paths, got %d (%#v)", len(expected), len(paths), paths)
	}
	for i, path := range expected {
		if paths[i] != path {
			t.Fatalf("expected path %d to be %q, got %q", i, path, paths[i])
		}
	}
}

func TestSQLiteRepositoryListPendingUploadsIncludesInternal(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "BLU", Status: "pending", SourcePath: "/tmp/a"}); err != nil {
		t.Fatalf("create pending record: %v", err)
	}
	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "AITHER", Status: "pending-internal", SourcePath: "/tmp/b"}); err != nil {
		t.Fatalf("create pending-internal record: %v", err)
	}
	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "HDB", Status: "uploaded", SourcePath: "/tmp/c"}); err != nil {
		t.Fatalf("create uploaded record: %v", err)
	}

	pending, err := repo.ListPendingUploads(ctx)
	if err != nil {
		t.Fatalf("list pending uploads: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending uploads, got %d", len(pending))
	}
}

func TestSQLiteUploadedImagesPersistUsageScope(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := repo.SaveUploadedImages(ctx, "/tmp/source", "hdb", []UploadedImageLink{{
		SourcePath: "/tmp/source",
		ImagePath:  "/tmp/a.png",
		Host:       "hdb",
		UsageScope: "tracker:HDB",
		ImgURL:     "https://hdb/a.png",
		RawURL:     "https://hdb/a.png",
		WebURL:     "https://hdb/a",
		UploadedAt: now,
	}}); err != nil {
		t.Fatalf("save uploaded image: %v", err)
	}

	images, err := repo.ListUploadedImagesByPath(ctx, "/tmp/source")
	if err != nil {
		t.Fatalf("list uploaded images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("expected 1 uploaded image, got %d", len(images))
	}
	if images[0].UsageScope != "tracker:HDB" {
		t.Fatalf("expected tracker:HDB usage scope, got %q", images[0].UsageScope)
	}
}

func TestSQLiteMigrationBackfillsUploadedImageUsageScope(t *testing.T) {
	t.Parallel()

	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() {
		_ = rawDB.Close()
	})

	statements := []string{
		`CREATE TABLE uploaded_images (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_path TEXT NOT NULL,
			image_path TEXT NOT NULL,
			host TEXT NOT NULL,
			img_url TEXT NOT NULL DEFAULT "",
			raw_url TEXT NOT NULL DEFAULT "",
			web_url TEXT NOT NULL DEFAULT "",
			size_bytes INTEGER NOT NULL DEFAULT 0,
			uploaded_at TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_uploaded_images_unique ON uploaded_images (source_path, host, image_path)`,
		`INSERT INTO uploaded_images (source_path, image_path, host, img_url, raw_url, web_url, size_bytes, uploaded_at)
		 VALUES ("/tmp/source", "/tmp/a.png", "imgbb", "https://imgbb/a.png", "https://imgbb/a.png", "https://imgbb/a", 10, "2026-03-28T00:00:00Z")`,
		`PRAGMA user_version = 4`,
	}
	for _, statement := range statements {
		if _, err := rawDB.Exec(statement); err != nil {
			t.Fatalf("exec setup statement: %v", err)
		}
	}

	if err := Migrate(rawDB); err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}

	repo := &SQLiteRepository{db: rawDB}
	images, err := repo.ListUploadedImagesByPath(context.Background(), "/tmp/source")
	if err != nil {
		t.Fatalf("list uploaded images: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("expected 1 uploaded image, got %d", len(images))
	}
	if images[0].UsageScope != "global" {
		t.Fatalf("expected global usage scope after migration, got %q", images[0].UsageScope)
	}
}

func TestSQLiteRepositoryUpdateLatestUploadRecordStatus(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "BLU", Status: "pending", SourcePath: "/tmp/file"}); err != nil {
		t.Fatalf("create first upload record: %v", err)
	}
	if err := repo.CreateUploadRecord(ctx, UploadRecord{Tracker: "BLU", Status: "pending", SourcePath: "/tmp/file"}); err != nil {
		t.Fatalf("create second upload record: %v", err)
	}

	if err := repo.UpdateLatestUploadRecordStatus(ctx, "/tmp/file", "BLU", "uploaded"); err != nil {
		t.Fatalf("update latest upload record status: %v", err)
	}

	history, err := repo.ListUploadHistoryByPath(ctx, "/tmp/file")
	if err != nil {
		t.Fatalf("list upload history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 upload records, got %d", len(history))
	}
	if history[0].Status != "uploaded" {
		t.Fatalf("expected latest upload status to be uploaded, got %q", history[0].Status)
	}
	if history[1].Status != "pending" {
		t.Fatalf("expected older upload status to remain pending, got %q", history[1].Status)
	}
}

func TestSQLiteRepositoryUpdateLatestUploadRecordStatusNotFound(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	err = repo.UpdateLatestUploadRecordStatus(context.Background(), "/missing", "BLU", "uploaded")
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestSQLiteRepositoryScreenshotSlotsRoundTrip(t *testing.T) {
	t.Parallel()

	repo, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	slots := []ScreenshotSlot{
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           0,
			SourceKind:          "description",
			OriginalKey:         "https://imgbb.com/original-a.png",
			OriginalURL:         "https://imgbb.com/original-a.png",
			OriginalHost:        "imgbb",
			ImagePath:           "/tmp/a.png",
			FromDescription:     true,
			Tracker:             "AITHER",
			SectionKind:         "wrapped",
			RenderInScreenshots: true,
			Variants: []ScreenshotSlotVariant{
				{
					SourcePath: "/tmp/source",
					SlotOrder:  0,
					Host:       "imgbb",
					UsageScope: "global",
					ImagePath:  "/tmp/a.png",
					ImgURL:     "https://imgbb.com/a.png",
					RawURL:     "https://imgbb.com/a.png",
					WebURL:     "https://imgbb.com/a",
					UploadedAt: time.Date(2026, 3, 28, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			SourcePath:          "/tmp/source",
			SlotOrder:           1,
			SourceKind:          "description",
			OriginalKey:         "https://ptpimg.me/original-b.png",
			OriginalURL:         "https://ptpimg.me/original-b.png",
			OriginalHost:        "ptpimg",
			SectionKind:         "comparison",
			RenderInScreenshots: true,
		},
	}

	if err := repo.ReplaceScreenshotSlots(ctx, "/tmp/source", slots); err != nil {
		t.Fatalf("replace screenshot slots: %v", err)
	}
	if err := repo.UpsertScreenshotSlotVariants(ctx, "/tmp/source", []ScreenshotSlotVariant{{
		SourcePath: "/tmp/source",
		SlotOrder:  1,
		Host:       "hdb",
		UsageScope: "tracker:HDB",
		ImgURL:     "https://t.hdbits.org/b.jpg",
		RawURL:     "https://img.hdbits.org/b.jpg",
		WebURL:     "https://img.hdbits.org/b",
		UploadedAt: time.Date(2026, 3, 28, 1, 0, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatalf("upsert screenshot slot variants: %v", err)
	}

	loaded, err := repo.ListScreenshotSlotsByPath(ctx, "/tmp/source")
	if err != nil {
		t.Fatalf("list screenshot slots: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(loaded))
	}
	if loaded[0].OriginalURL != "https://imgbb.com/original-a.png" || loaded[1].OriginalHost != "ptpimg" {
		t.Fatalf("unexpected loaded slots: %#v", loaded)
	}
	if len(loaded[0].Variants) != 1 || loaded[0].Variants[0].UsageScope != "global" {
		t.Fatalf("expected first slot global variant, got %#v", loaded[0].Variants)
	}
	if len(loaded[1].Variants) != 1 || loaded[1].Variants[0].UsageScope != "tracker:HDB" {
		t.Fatalf("expected second slot tracker-scoped variant, got %#v", loaded[1].Variants)
	}
}

func stringPtr(value string) *string {
	copy := value
	return &copy
}

func intPtr(value int) *int {
	copy := value
	return &copy
}

func boolPtr(value bool) *bool {
	copy := value
	return &copy
}
