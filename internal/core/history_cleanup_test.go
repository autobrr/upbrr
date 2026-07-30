// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	purgeErr     error
	purgeErrAt   int
	cancel       context.CancelFunc
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
	if r.cancel != nil {
		r.cancel()
	}
	if r.purgeErrAt > 0 && r.purgeCalls == r.purgeErrAt {
		return r.purgeErr
	}
	if r.purgeErrAt > 0 {
		return nil
	}
	return r.purgeErr
}

func (r *cleanupRepo) LoadHistoryCleanupSnapshot(context.Context, string) (api.HistoryCleanupSnapshot, error) {
	artifactPaths := make([]string, 0, len(r.screenshots)+len(r.uploaded)+len(r.finals)+len(r.slots))
	for _, shot := range r.screenshots {
		artifactPaths = append(artifactPaths, shot.ImagePath)
	}
	for _, image := range r.uploaded {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}
	for _, image := range r.finals {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}
	for _, slot := range r.slots {
		artifactPaths = append(artifactPaths, slot.ImagePath)
		for _, variant := range slot.Variants {
			artifactPaths = append(artifactPaths, variant.ImagePath)
		}
	}
	snapshot := api.HistoryCleanupSnapshot{ArtifactPaths: artifactPaths}
	if r.getByPathErr == nil && r.stored.Path != "" {
		stored := r.stored
		snapshot.Metadata = &stored
	}
	return snapshot, nil
}

func TestHistoryDeleteRemovesStoredArtifacts(t *testing.T) {
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
		if err := os.WriteFile(target, []byte("test"), 0o600); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
	}

	repo := &cleanupRepo{
		screenshots: []api.Screenshot{{SourcePath: sourcePath, ImagePath: tmpFile}},
		uploaded: []api.UploadedImageLink{{
			SourcePath: sourcePath,
			ImagePath:  cacheFile,
			Host:       "imgbox",
		}},
		finals: []api.ScreenshotFinalSelection{{ImagePath: nfoFile}},
		slots: []api.ScreenshotSlot{{
			SourcePath: sourcePath,
			ImagePath:  slotFile,
			Variants:   []api.ScreenshotSlotVariant{{ImagePath: slotVariantFile}},
		}},
	}
	history := newHistoryModule(repo, dbPath, api.NopLogger{})

	if err := history.Delete(context.Background(), sourcePath); err != nil {
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

func TestHistoryDeleteRemovesDirectoryChildArtifacts(t *testing.T) {
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
	history := newHistoryModule(repo, dbPath, api.NopLogger{})

	if err := history.Delete(context.Background(), sourceDir); err != nil {
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

func TestHistoryDeleteKeepsDBRowsWhenArtifactRemovalFails(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ua.db")
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}

	sourcePath := filepath.Join(baseDir, "Example.Movie.2024.mkv")
	blockedPath := filepath.Join(tmpRoot, filepath.Base(sourcePath), "blocked.png")
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("mkdir blocked artifact dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "nested.txt"), []byte("test"), 0o600); err != nil {
		t.Fatalf("write nested artifact: %v", err)
	}

	repo := &cleanupRepo{
		screenshots: []api.Screenshot{{SourcePath: sourcePath, ImagePath: blockedPath}},
	}
	history := newHistoryModule(repo, dbPath, api.NopLogger{})

	if err := history.Delete(context.Background(), sourcePath); err == nil {
		t.Fatal("expected artifact removal failure")
	}
	if repo.purgeCalls != 0 {
		t.Fatalf("expected DB rows kept on artifact removal failure, got purge calls %#v", repo.purgedPaths)
	}
	if _, err := os.Stat(blockedPath); err != nil {
		t.Fatalf("expected blocked artifact path to remain, got %v", err)
	}
}

func TestHistoryDeleteAllPurgesEveryStoredPath(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	repo := &cleanupRepo{
		storedPaths: []string{
			filepath.Join(baseDir, "one.mkv"),
			filepath.Join(baseDir, "two.mkv"),
		},
		getByPathErr: internalerrors.ErrNotFound,
	}
	history := newHistoryModule(repo, filepath.Join(baseDir, "ua.db"), api.NopLogger{})

	deleted, err := history.DeleteAll(context.Background())
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

func TestHistoryDeleteDoesNotRemoveOutsideManagedRoots(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "ua.db")
	sourcePath := filepath.Join(baseDir, "Example.Release.2026.mkv")
	outsidePath := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outsidePath, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write outside artifact: %v", err)
	}
	repo := &cleanupRepo{screenshots: []api.Screenshot{{SourcePath: sourcePath, ImagePath: outsidePath}}}
	history := newHistoryModule(repo, dbPath, api.NopLogger{})

	if err := history.Delete(context.Background(), sourcePath); err != nil {
		t.Fatalf("delete history release: %v", err)
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Fatalf("outside artifact changed: %v", err)
	}
	if repo.purgeCalls != 1 {
		t.Fatalf("expected history row purge, got %d calls", repo.purgeCalls)
	}
}

func TestHistoryDeleteAllReturnsPartialCountOnPurgeError(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	wantErr := errors.New("purge failed")
	repo := &cleanupRepo{
		storedPaths:  []string{filepath.Join(baseDir, "one.mkv"), filepath.Join(baseDir, "two.mkv")},
		getByPathErr: internalerrors.ErrNotFound,
		purgeErr:     wantErr,
		purgeErrAt:   2,
	}
	history := newHistoryModule(repo, filepath.Join(baseDir, "ua.db"), api.NopLogger{})

	deleted, err := history.DeleteAll(context.Background())
	if deleted != 1 {
		t.Fatalf("expected partial delete count 1, got %d", deleted)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected purge error, got %v", err)
	}
	if repo.purgeCalls != 2 {
		t.Fatalf("expected stop at second purge, got %d calls", repo.purgeCalls)
	}
}

func TestHistoryDeleteAllHonorsCancellationBetweenReleases(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	repo := &cleanupRepo{
		storedPaths:  []string{filepath.Join(baseDir, "one.mkv"), filepath.Join(baseDir, "two.mkv")},
		getByPathErr: internalerrors.ErrNotFound,
		cancel:       cancel,
	}
	history := newHistoryModule(repo, filepath.Join(baseDir, "ua.db"), api.NopLogger{})

	deleted, err := history.DeleteAll(ctx)
	if deleted != 1 {
		t.Fatalf("expected one completed delete before cancellation, got %d", deleted)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if repo.purgeCalls != 1 {
		t.Fatalf("expected no second purge after cancellation, got %d calls", repo.purgeCalls)
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

	removed, err := removeIfWithinRootFS(osHistoryFilesystem{}, root, alias, true)
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
