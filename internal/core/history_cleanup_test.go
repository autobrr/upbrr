// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type cleanupRepo struct {
	stubRepo
	storedPaths  []string
	screenshots  []api.Screenshot
	uploaded     []api.UploadedImageLink
	finals       []api.ScreenshotFinalSelection
	slots        []api.ScreenshotSlot
	stored       api.FileMetadata
	getByPathErr error
	purgedPaths  []string
	purgeCalls   int
}

func (r *cleanupRepo) GetByPath(context.Context, string) (api.FileMetadata, error) {
	if r.getByPathErr != nil {
		return api.FileMetadata{}, r.getByPathErr
	}
	if r.stored.Path == "" {
		return api.FileMetadata{}, internalerrors.ErrNotFound
	}
	return r.stored, nil
}

func (r *cleanupRepo) ListScreenshotsByPath(context.Context, string) ([]api.Screenshot, error) {
	return r.screenshots, nil
}

func (r *cleanupRepo) ListUploadedImagesByPath(context.Context, string) ([]api.UploadedImageLink, error) {
	return r.uploaded, nil
}

func (r *cleanupRepo) ListFinalSelections(context.Context, string) ([]api.ScreenshotFinalSelection, error) {
	return r.finals, nil
}

func (r *cleanupRepo) ListScreenshotSlotsByPath(context.Context, string) ([]api.ScreenshotSlot, error) {
	return r.slots, nil
}

func (r *cleanupRepo) ListStoredReleasePaths(context.Context) ([]string, error) {
	return r.storedPaths, nil
}

func (r *cleanupRepo) PurgeContentData(_ context.Context, path string) error {
	r.purgeCalls++
	r.purgedPaths = append(r.purgedPaths, path)
	return nil
}

func TestCoreDeleteHistoryReleaseRemovesStoredArtifacts(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ua.db")
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	cacheRoot, err := db.Subdir(dbPath, "cache")
	if err != nil {
		t.Fatalf("cache root: %v", err)
	}
	nfoRoot, err := db.Subdir(dbPath, "nfo")
	if err != nil {
		t.Fatalf("nfo root: %v", err)
	}

	sourcePath := filepath.Join(baseDir, "Example.Movie.2024.mkv")
	tmpFile := filepath.Join(tmpRoot, filepath.Base(sourcePath), "shot-01.png")
	cacheFile := filepath.Join(cacheRoot, "uploaded-01.png")
	nfoFile := filepath.Join(nfoRoot, "release.nfo")
	slotFile := filepath.Join(tmpRoot, filepath.Base(sourcePath), "slot-01.png")
	slotVariantFile := filepath.Join(tmpRoot, filepath.Base(sourcePath), "slot-variant-01.png")
	for _, target := range []string{tmpFile, cacheFile, nfoFile, slotFile, slotVariantFile} {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", target, err)
		}
		if err := os.WriteFile(target, []byte("test"), 0o644); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
	}

	repo := &cleanupRepo{
		screenshots: []api.Screenshot{{SourcePath: sourcePath, ImagePath: tmpFile}},
		uploaded:    []api.UploadedImageLink{{SourcePath: sourcePath, ImagePath: cacheFile, Host: "imgbox"}},
		finals:      []api.ScreenshotFinalSelection{{ImagePath: nfoFile}},
		slots: []api.ScreenshotSlot{{
			SourcePath: sourcePath,
			ImagePath:  slotFile,
			Variants:   []api.ScreenshotSlotVariant{{ImagePath: slotVariantFile}},
		}},
	}
	coreSvc := &Core{
		cfg:    config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		logger: api.NopLogger{},
		repo:   repo,
	}

	if err := coreSvc.DeleteHistoryRelease(context.Background(), sourcePath); err != nil {
		t.Fatalf("delete history release: %v", err)
	}
	if repo.purgeCalls != 1 || len(repo.purgedPaths) != 1 || repo.purgedPaths[0] != sourcePath {
		t.Fatalf("unexpected purge calls: %#v", repo.purgedPaths)
	}
	for _, target := range []string{tmpFile, cacheFile, nfoFile, slotFile, slotVariantFile} {
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %s removed, got err=%v", target, err)
		}
	}
	if _, err := os.Stat(filepath.Join(tmpRoot, filepath.Base(sourcePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected tmp content dir removed, got err=%v", err)
	}
}

func TestCoreDeleteHistoryReleaseRemovesDirectoryChildArtifacts(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ua.db")
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}

	sourceDir := filepath.Join(baseDir, "Example.Movie.2024")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	childPath := filepath.Join(sourceDir, "Example.Movie.2024.mkv")
	if err := os.WriteFile(childPath, []byte("video"), 0o600); err != nil {
		t.Fatalf("write child source: %v", err)
	}

	sourceTmpDir := filepath.Join(tmpRoot, filepath.Base(sourceDir))
	childTmpDir := filepath.Join(tmpRoot, filepath.Base(childPath))
	for _, dir := range []string{sourceTmpDir, childTmpDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir tmp dir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "mediainfo.txt"), []byte("test"), 0o600); err != nil {
			t.Fatalf("write tmp artifact: %v", err)
		}
	}

	repo := &cleanupRepo{
		storedPaths:  []string{childPath},
		getByPathErr: internalerrors.ErrNotFound,
	}
	coreSvc := &Core{
		cfg:    config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
		logger: api.NopLogger{},
		repo:   repo,
	}

	if err := coreSvc.DeleteHistoryRelease(context.Background(), sourceDir); err != nil {
		t.Fatalf("delete history release: %v", err)
	}
	if len(repo.purgedPaths) != 2 || repo.purgedPaths[0] != sourceDir || repo.purgedPaths[1] != childPath {
		t.Fatalf("expected source and child DB purge, got %#v", repo.purgedPaths)
	}
	for _, dir := range []string{sourceTmpDir, childTmpDir} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected tmp dir %s removed, got err=%v", dir, err)
		}
	}
}

func TestCoreDeleteAllHistoryReleasesPurgesEveryStoredPath(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	repo := &cleanupRepo{
		storedPaths: []string{
			filepath.Join(baseDir, "one.mkv"),
			filepath.Join(baseDir, "two.mkv"),
		},
		getByPathErr: internalerrors.ErrNotFound,
	}
	coreSvc := &Core{
		cfg:    config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(baseDir, "ua.db")}},
		logger: api.NopLogger{},
		repo:   repo,
	}

	deleted, err := coreSvc.DeleteAllHistoryReleases(context.Background())
	if err != nil {
		t.Fatalf("delete all history releases: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted releases, got %d", deleted)
	}
	if len(repo.purgedPaths) != 2 {
		t.Fatalf("expected 2 purged paths, got %#v", repo.purgedPaths)
	}
	if repo.purgedPaths[0] != repo.storedPaths[0] || repo.purgedPaths[1] != repo.storedPaths[1] {
		t.Fatalf("unexpected purged paths: %#v", repo.purgedPaths)
	}
}

func TestRemoveIfWithinRootKeepsAliasedRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sentinel := filepath.Join(root, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	alias := filepath.Join(root, "self")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable on this host: %v", err)
	}

	removed, err := removeIfWithinRoot(root, alias, true)
	if err != nil {
		t.Fatalf("remove aliased root: %v", err)
	}
	if removed {
		t.Fatalf("expected aliased root to be kept")
	}
	if _, err := os.Lstat(alias); err != nil {
		t.Fatalf("expected alias to remain: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("expected root contents to remain: %v", err)
	}
}
