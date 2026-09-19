// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type cleanupRepo struct {
	stubRepo
	storedPaths    []string
	screenshots    []api.Screenshot
	uploaded       []api.UploadedImageLink
	finals         []api.ScreenshotFinalSelection
	slots          []api.ScreenshotSlot
	stored         api.FileMetadata
	getByPathErr   error
	workflowScopes []api.WorkflowScope
	protectedPaths []string
	purgedPaths    []string
	purgeCalls     int
	purgeErr       error
	purgeErrAt     int
	cancel         context.CancelFunc
}

type historyActiveInputRepoFake struct {
	slot   api.ActiveInputRecord
	record api.InputRecord
}

type historyDeletionBoundaryRepo struct {
	*db.SQLiteRepository
	afterCommit func()
}

func (r *historyDeletionBoundaryRepo) WithHistoryDeletion(ctx context.Context, callback func(context.Context) error) error {
	if err := r.SQLiteRepository.WithHistoryDeletion(ctx, callback); err != nil {
		return fmt.Errorf("history test deletion transaction: %w", err)
	}
	if r.afterCommit != nil {
		r.afterCommit()
	}
	return nil
}

func (f historyActiveInputRepoFake) LoadActiveInput(context.Context) (api.ActiveInputRecord, error) {
	return f.slot, nil
}

func (f historyActiveInputRepoFake) CompareAndSwapActiveInput(
	context.Context, api.ActiveInputRecord, api.ActiveInputRecord, time.Time,
) error {
	return internalerrors.ErrNotImplemented
}

func (f historyActiveInputRepoFake) CloseIdleActiveInput(context.Context, api.ActiveInputRecord, time.Time) error {
	return internalerrors.ErrNotImplemented
}

func (f historyActiveInputRepoFake) FinalizeActiveInput(
	context.Context, api.ActiveInputRecord, api.ActiveInputRecord, api.InputRecord, *api.ReleaseWorkflowStateRecord, time.Time,
) error {
	return internalerrors.ErrNotImplemented
}

func (f historyActiveInputRepoFake) RenewActiveInput(context.Context, string, uint64, time.Time, time.Time) error {
	return internalerrors.ErrNotImplemented
}

func (f historyActiveInputRepoFake) RelinquishActiveInput(context.Context, string, uint64, time.Time) error {
	return internalerrors.ErrNotImplemented
}

func (f historyActiveInputRepoFake) LoadInputRecord(context.Context, string) (api.InputRecord, error) {
	return f.record, nil
}

func (f historyActiveInputRepoFake) LoadInputRecordByID(context.Context, string) (api.InputRecord, error) {
	return f.record, nil
}

func (f historyActiveInputRepoFake) SaveInputRecord(context.Context, api.InputRecord) (api.InputRecord, error) {
	return api.InputRecord{}, internalerrors.ErrNotImplemented
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

func (r *cleanupRepo) ListHistoryProtectedSourcePaths(context.Context) ([]string, error) {
	return append([]string(nil), r.protectedPaths...), nil
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
	snapshot := api.HistoryCleanupSnapshot{
		ArtifactPaths:  artifactPaths,
		WorkflowScopes: append([]api.WorkflowScope(nil), r.workflowScopes...),
	}
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

func TestHistoryDeleteInvalidatesPreparedFactsBeforeTransactionReleases(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "Example.Release.2026.mkv")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	repository, err := db.Open(filepath.Join(baseDir, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	prepared, err := preparedrelease.New(repository, workflowMediaIdentityResolver{}, workflowMediaCollector{})
	if err != nil {
		t.Fatalf("create prepared release module: %v", err)
	}
	verified, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("verify first source: %v", err)
	}
	first, err := prepared.Prepare(t.Context(), api.PrepareInput{SourcePath: sourcePath, VerifiedSource: &verified})
	if err != nil {
		t.Fatalf("prepare first generation: %v", err)
	}
	if err := repository.Save(t.Context(), db.FileMetadata{Path: sourcePath, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("save history source: %v", err)
	}
	repo := &historyDeletionBoundaryRepo{SQLiteRepository: repository}
	var second api.PrepareResult
	repo.afterCommit = func() {
		verified, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: sourcePath})
		if err != nil {
			t.Fatalf("verify replacement source: %v", err)
		}
		second, err = prepared.Prepare(t.Context(), api.PrepareInput{SourcePath: sourcePath, VerifiedSource: &verified})
		if err != nil {
			t.Fatalf("prepare replacement generation: %v", err)
		}
	}
	history := newHistoryModule(repo, repository.DBPath(), api.NopLogger{})
	history.preparedFacts = prepared

	if err := history.Delete(t.Context(), sourcePath); err != nil {
		t.Fatalf("delete history release: %v", err)
	}
	if second.Release.Generation == 0 || first.Release.Generation == 0 {
		t.Fatalf("prepared generations first=%d replacement=%d", first.Release.Generation, second.Release.Generation)
	}
	if _, err := prepared.Export(t.Context(), api.ReleaseRef{SourcePath: sourcePath, Generation: second.Release.Generation}); err != nil {
		t.Fatalf("export replacement generation after history deletion: %v", err)
	}
}

func TestHistoryDeletionPreservesSharedArtifactsUntilLastReference(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"sequential", "all", "parent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			baseDir := t.TempDir()
			repo, err := db.Open(filepath.Join(baseDir, "history.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			sourceDir := filepath.Join(baseDir, "sources")
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				t.Fatal(err)
			}
			sources := []string{filepath.Join(sourceDir, "Example.A.mkv"), filepath.Join(sourceDir, "Example.B.mkv")}
			tmpRoot, err := db.Subdir(repo.DBPath(), "tmp")
			if err != nil {
				t.Fatal(err)
			}
			reuseRoots := make([]string, 0, len(sources))
			for _, source := range sources {
				reuseRoot, err := reusableMediaTempRoot(tmpRoot, source)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(reuseRoot, 0o700); err != nil {
					t.Fatal(err)
				}
				// A failed media commit can leave a completed copy without a database row.
				if err := os.WriteFile(filepath.Join(reuseRoot, "uncommitted.png"), []byte("copied image"), 0o600); err != nil {
					t.Fatal(err)
				}
				reuseRoots = append(reuseRoots, reuseRoot)
			}
			sharedDir := filepath.Join(tmpRoot, "shared-output")
			nestedDir := filepath.Join(sharedDir, "nested")
			if err := os.MkdirAll(nestedDir, 0o755); err != nil {
				t.Fatal(err)
			}
			shared := filepath.Join(nestedDir, "shared.png")
			private := filepath.Join(nestedDir, "private.png")
			untracked := filepath.Join(sharedDir, "generated.txt")
			for _, path := range append([]string{shared, private, untracked}, sources...) {
				if err := os.WriteFile(path, []byte("synthetic data"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			for _, source := range sources {
				if err := repo.Save(t.Context(), api.FileMetadata{
					Path:      source,
					InfoHash:  filepath.Base(source),
					UpdatedAt: now,
				}); err != nil {
					t.Fatal(err)
				}
				insertHistoryReusableArtifact(t, repo, source, shared, now)
			}
			insertHistoryReusableArtifact(t, repo, sources[0], private, now)
			history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
			switch mode {
			case "sequential":
				if err := history.Delete(t.Context(), sources[0]); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(shared); err != nil {
					t.Fatalf("retained source lost shared file: %v", err)
				}
				if _, err := os.Stat(private); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unshared file was retained: %v", err)
				}
				if _, err := os.Stat(reuseRoots[0]); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("uncommitted source copy was retained: %v", err)
				}
				if _, err := os.Stat(reuseRoots[1]); err != nil {
					t.Fatalf("unrelated source copy was removed: %v", err)
				}
				if _, err := os.Stat(untracked); err != nil {
					t.Fatalf("shared directory sibling was removed: %v", err)
				}
				retained, err := repo.LoadHistoryCleanupSnapshot(t.Context(), sources[1])
				if err != nil || len(retained.ArtifactPaths) != 1 || retained.ArtifactPaths[0] != shared {
					t.Fatalf("retained source association changed: %#v, %v", retained, err)
				}
				if err := history.Delete(t.Context(), sources[1]); err != nil {
					t.Fatal(err)
				}
			case "all":
				if deleted, err := history.DeleteAll(t.Context()); err != nil || deleted != 2 {
					t.Fatalf("delete all: count=%d, err=%v", deleted, err)
				}
			case "parent":
				if err := history.Delete(t.Context(), sourceDir); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(sharedDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("last-reference cleanup left generated directory: %v", err)
			}
			for _, reuseRoot := range reuseRoots {
				if _, err := os.Stat(reuseRoot); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("history cleanup left uncommitted copied files: %v", err)
				}
			}
			for _, source := range sources {
				if _, err := os.Stat(source); err != nil {
					t.Fatalf("source media was removed: %v", err)
				}
			}
		})
	}
}

func insertHistoryReusableArtifact(t *testing.T, repo *db.SQLiteRepository, source, image string, now time.Time) {
	t.Helper()
	if _, err := repo.RawDB().ExecContext(t.Context(), `
		INSERT INTO media_reusable_assets (
			source_path, prepared_media_fingerprint, prepared_generation, compatibility_key,
			capture_fingerprint, content_sha256, kind, purpose, image_path, captured_at
		) VALUES (?, 'media', 1, 'compatible', 'capture', 'content', 'screenshot', 'final', ?, ?)
	`, source, image, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryDeleteRemovesDirectoryChildArtifacts(t *testing.T) {
	t.Parallel()
	for _, sourceExists := range []bool{true, false} {
		t.Run(fmt.Sprintf("source_exists_%t", sourceExists), func(t *testing.T) {
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
			if !sourceExists {
				if err := os.Remove(childPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(sourceDir); err != nil {
					t.Fatal(err)
				}
			}
			siblingPath := sourceDir + ".other"

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
				storedPaths:  []string{childPath, siblingPath},
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
		})
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

func TestHistoryDeleteRejectsActiveChildSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	child := filepath.Join(root, "child.mkv")
	if err := os.WriteFile(child, []byte("source"), 0o600); err != nil {
		t.Fatalf("write child source: %v", err)
	}
	workflowID := api.WorkflowID("active-child-workflow")
	repo := &cleanupRepo{
		storedPaths:    []string{root, child},
		workflowScopes: []api.WorkflowScope{{OwnerID: "history-owner", WorkflowID: workflowID}},
	}
	history := newHistoryModule(repo, filepath.Join(root, "upbrr.db"), api.NopLogger{})
	vault, err := releaseworkflow.NewPrivateArtifactVault(t.TempDir())
	if err != nil {
		t.Fatalf("new private artifact vault: %v", err)
	}
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	if err := vault.Put(
		"history-owner",
		workflowID,
		"preview:active-child",
		releaseworkflow.MediaPreviewContent{Bytes: []byte("active"), ContentType: "image/png"},
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("put active private artifact: %v", err)
	}
	history.privateVault = vault
	history.activeInputs = historyActiveInputRepoFake{
		slot:   api.ActiveInputRecord{State: api.ActiveInputActive, InputID: "active-child"},
		record: api.InputRecord{ID: "active-child", CanonicalPath: child},
	}
	if err := history.Delete(t.Context(), root); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("delete active child history = %v", err)
	}
	if len(repo.purgedPaths) != 0 {
		t.Fatalf("purged active history paths = %#v", repo.purgedPaths)
	}
	if _, err := vault.Get("history-owner", workflowID, "preview:active-child", now); err != nil {
		t.Fatalf("active workflow private artifact was removed: %v", err)
	}
}

func TestHistoryDeleteProtectsActiveLegacyTempCollision(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name       string
		activeName string
		blocked    bool
	}{
		{
			name:       "same sanitized basename",
			activeName: "Example.Release.2026.mkv",
			blocked:    true,
		},
		{name: "different basename", activeName: "Other.Release.2026.mkv"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			baseDir := t.TempDir()
			repo, err := db.Open(filepath.Join(baseDir, "history.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Second)
			historySource := filepath.Join(baseDir, "history", "Example.Release.2026.mkv")
			activeSource := filepath.Join(baseDir, "active", scenario.activeName)
			for _, sourcePath := range []string{historySource, activeSource} {
				if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := repo.Save(ctx, db.FileMetadata{Path: historySource, UpdatedAt: now}); err != nil {
				t.Fatalf("save history source: %v", err)
			}
			activateHistoryInput(t, repo, activeSource, now)
			tmpRoot, err := db.Subdir(repo.DBPath(), "tmp")
			if err != nil {
				t.Fatal(err)
			}
			capturePath := filepath.Join(tmpRoot, paths.ReleaseTempBaseFor(activeSource, api.ReleaseInfo{}), "capture.png")
			if err := os.MkdirAll(filepath.Dir(capturePath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(capturePath, []byte("unpersisted capture"), 0o600); err != nil {
				t.Fatal(err)
			}

			history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
			err = history.Delete(ctx, historySource)
			if scenario.blocked {
				if !errors.Is(err, api.ErrActiveInputBusy) {
					t.Fatalf("delete colliding history source = %v", err)
				}
				if _, err := repo.GetByPath(ctx, historySource); err != nil {
					t.Fatalf("history row after blocked delete: %v", err)
				}
			} else if err != nil {
				t.Fatalf("delete noncolliding history source: %v", err)
			} else if _, err := repo.GetByPath(ctx, historySource); !errors.Is(err, internalerrors.ErrNotFound) {
				t.Fatalf("history row after delete = %v", err)
			}
			if _, err := os.Stat(capturePath); err != nil {
				t.Fatalf("active unpersisted capture changed: %v", err)
			}
			active, err := repo.LoadActiveInput(ctx)
			if err != nil {
				t.Fatalf("load active input: %v", err)
			}
			if active.State != api.ActiveInputActive || active.InputID != "history-active-input" {
				t.Fatalf("active input changed: %#v", active)
			}
		})
	}
}

func TestHistoryDeleteProtectsSourceUnderTmpRoot(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name      string
		create    func(*testing.T, string)
		sourceRel string
	}{
		{
			name:      "file",
			sourceRel: "managed-source.mkv",
			create: func(t *testing.T, sourcePath string) {
				t.Helper()
				if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "directory",
			sourceRel: "managed-source",
			create: func(t *testing.T, sourcePath string) {
				t.Helper()
				if err := os.MkdirAll(sourcePath, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sourcePath, "video.mkv"), []byte("source"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			repo, err := db.Open(filepath.Join(t.TempDir(), "history.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			tmpRoot, err := db.Subdir(repo.DBPath(), "tmp")
			if err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(tmpRoot, scenario.sourceRel)
			scenario.create(t, sourcePath)
			if err := repo.Save(t.Context(), db.FileMetadata{Path: sourcePath, UpdatedAt: time.Now().UTC()}); err != nil {
				t.Fatalf("save history source: %v", err)
			}

			history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
			if err := history.Delete(t.Context(), sourcePath); !errors.Is(err, api.ErrActiveInputBusy) {
				t.Fatalf("delete managed source = %v", err)
			}
			if _, err := os.Stat(sourcePath); err != nil {
				t.Fatalf("source after blocked delete: %v", err)
			}
			if _, err := repo.GetByPath(t.Context(), sourcePath); err != nil {
				t.Fatalf("history row after blocked delete: %v", err)
			}
		})
	}
}

func TestHistoryDeleteProtectsOtherStoredSourceUnderTmpRoot(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	repo, err := db.Open(filepath.Join(baseDir, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	tmpRoot, err := db.Subdir(repo.DBPath(), "tmp")
	if err != nil {
		t.Fatal(err)
	}
	historySource := filepath.Join(baseDir, "history", "Example.Release.2026.mkv")
	if err := os.MkdirAll(filepath.Dir(historySource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historySource, []byte("history source"), 0o600); err != nil {
		t.Fatal(err)
	}
	storedSource := filepath.Join(tmpRoot, paths.ReleaseTempBaseFor(historySource, api.ReleaseInfo{}))
	if err := os.WriteFile(storedSource, []byte("stored source"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, sourcePath := range []string{historySource, storedSource} {
		if err := repo.Save(t.Context(), db.FileMetadata{Path: sourcePath, UpdatedAt: now}); err != nil {
			t.Fatalf("save history source %q: %v", sourcePath, err)
		}
	}

	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	if err := history.Delete(t.Context(), historySource); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("delete history source = %v", err)
	}
	if _, err := os.Stat(storedSource); err != nil {
		t.Fatalf("stored source after blocked delete: %v", err)
	}
	for _, sourcePath := range []string{historySource, storedSource} {
		if _, err := repo.GetByPath(t.Context(), sourcePath); err != nil {
			t.Fatalf("history row %q after blocked delete: %v", sourcePath, err)
		}
	}
}

func TestHistoryDeleteLeavesOutsideManagedArtifact(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "Example.Release.2026.mkv")
	artifactPath := filepath.Join(baseDir, "external", "capture.png")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("external artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := &cleanupRepo{
		storedPaths:  []string{sourcePath},
		getByPathErr: internalerrors.ErrNotFound,
		screenshots:  []api.Screenshot{{SourcePath: sourcePath, ImagePath: artifactPath}},
	}
	history := newHistoryModule(repo, filepath.Join(baseDir, "history.sqlite"), api.NopLogger{})

	if err := history.Delete(t.Context(), sourcePath); err != nil {
		t.Fatalf("delete history release: %v", err)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("outside managed artifact changed: %v", err)
	}
	if repo.purgeCalls != 1 {
		t.Fatalf("purge calls=%d, want 1", repo.purgeCalls)
	}
}

func activateHistoryInput(t *testing.T, repo *db.SQLiteRepository, sourcePath string, now time.Time) {
	t.Helper()
	workflow := historySubmissionWorkflowState(t, sourcePath, now)
	workflow.OwnerID = "history-active-owner"
	workflow.WorkflowID = "history-active-workflow"
	workflow.Status = api.WorkflowStatusActive
	workflow.CreationKey = "history-active-create"
	workflow.CreationFingerprint = "history-active-fingerprint"
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), workflow); err != nil {
		t.Fatalf("create active workflow: %v", err)
	}
	empty, err := repo.LoadActiveInput(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	opening := api.ActiveInputRecord{
		State:          api.ActiveInputOpening,
		Revision:       empty.Revision + 1,
		Fence:          empty.Fence + 1,
		OwnerID:        workflow.OwnerID,
		CoordinatorID:  "history-active-coordinator",
		LeaseExpiresAt: now.Add(time.Hour),
		ReservationID:  "history-active-reservation",
		RequestedPath:  sourcePath,
		IdempotencyKey: "history-active-open",
	}
	if err := repo.CompareAndSwapActiveInput(t.Context(), empty, opening, now); err != nil {
		t.Fatalf("open active input: %v", err)
	}
	input := api.InputRecord{
		ID:            "history-active-input",
		CanonicalPath: sourcePath,
		SourceVersion: "history-active-source",
		Manifest:      []byte(`{}`),
		UpdatedAt:     now,
	}
	active := opening
	active.State, active.Revision = api.ActiveInputActive, opening.Revision+1
	active.InputID, active.SourceVersion, active.WorkflowID = input.ID, input.SourceVersion, workflow.WorkflowID
	active.ReservationID, active.RequestedPath = "", ""
	if err := repo.FinalizeActiveInput(t.Context(), opening, active, input, nil, now); err != nil {
		t.Fatalf("activate input: %v", err)
	}
}

func TestHistoryDeleteRemovesExactPrivateWorkflowArtifacts(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "Example.Release.2026.mkv")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	repo, err := db.Open(filepath.Join(baseDir, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	workflow := historySubmissionWorkflowState(t, sourcePath, now)
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), workflow); err != nil {
		t.Fatalf("create history workflow: %v", err)
	}
	if err := repo.Save(t.Context(), db.FileMetadata{Path: sourcePath, UpdatedAt: now}); err != nil {
		t.Fatalf("save display history: %v", err)
	}
	targetScope := api.WorkflowScope{OwnerID: workflow.OwnerID, WorkflowID: workflow.WorkflowID}
	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	vault, err := releaseworkflow.NewPrivateArtifactVault(t.TempDir())
	if err != nil {
		t.Fatalf("new private artifact vault: %v", err)
	}
	targetResource := "preview:delete"
	unrelatedWorkflow := api.WorkflowID("history-preserve")
	if err := vault.Put(
		targetScope.OwnerID,
		targetScope.WorkflowID,
		targetResource,
		releaseworkflow.MediaPreviewContent{Bytes: []byte("delete"), ContentType: "image/png"},
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("put target private artifact: %v", err)
	}
	if err := vault.Put(
		targetScope.OwnerID,
		unrelatedWorkflow,
		"preview:preserve",
		releaseworkflow.MediaPreviewContent{Bytes: []byte("preserve"), ContentType: "image/png"},
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("put unrelated private artifact: %v", err)
	}
	// The exact target file paths are verified by the vault regression. History
	// verifies the source-to-scope handoff and retained workflow preservation.
	history.privateVault = vault
	if err := history.Delete(t.Context(), sourcePath); err != nil {
		t.Fatalf("delete history: %v", err)
	}
	if _, err := repo.LoadReleaseWorkflowState(t.Context(), workflow.OwnerID, workflow.WorkflowID); !errors.Is(err, api.ErrReleaseWorkflowStateNotFound) {
		t.Fatalf("history workflow after deletion = %v", err)
	}
	if _, err := vault.Get(targetScope.OwnerID, targetScope.WorkflowID, targetResource, now); !errors.Is(err, releaseworkflow.ErrPrivateResourceUnavailable) {
		t.Fatalf("target private workflow artifact error = %v", err)
	}
	if _, err := vault.Get(targetScope.OwnerID, unrelatedWorkflow, "preview:preserve", now); err != nil {
		t.Fatalf("unrelated private workflow artifact was removed: %v", err)
	}
}

func TestHistoryDeleteKeepsRowsWhenPrivateVaultCleanupFails(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	sourcePath := filepath.Join(baseDir, "Example.Release.2026.mkv")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	repo, err := db.Open(filepath.Join(baseDir, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	workflow := historySubmissionWorkflowState(t, sourcePath, now)
	if _, _, err := repo.CreateReleaseWorkflowState(t.Context(), workflow); err != nil {
		t.Fatalf("create history workflow: %v", err)
	}
	if err := repo.Save(t.Context(), db.FileMetadata{Path: sourcePath, UpdatedAt: now}); err != nil {
		t.Fatalf("save display history: %v", err)
	}
	scope := api.WorkflowScope{OwnerID: workflow.OwnerID, WorkflowID: workflow.WorkflowID}
	history := newHistoryModule(repo, repo.DBPath(), api.NopLogger{})
	vaultRoot := t.TempDir()
	vault, err := releaseworkflow.NewPrivateArtifactVault(vaultRoot)
	if err != nil {
		t.Fatalf("new private artifact vault: %v", err)
	}
	if err := vault.Put(
		scope.OwnerID,
		scope.WorkflowID,
		"preview:retry",
		releaseworkflow.MediaPreviewContent{Bytes: []byte("retry"), ContentType: "image/png"},
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("put private artifact: %v", err)
	}
	if err := os.RemoveAll(vaultRoot); err != nil {
		t.Fatalf("remove vault root: %v", err)
	}
	history.privateVault = vault
	if err := history.Delete(t.Context(), sourcePath); err == nil {
		t.Fatal("expected private vault cleanup error")
	}
	if _, err := repo.LoadReleaseWorkflowState(t.Context(), workflow.OwnerID, workflow.WorkflowID); err != nil {
		t.Fatalf("private cleanup failure purged history workflow: %v", err)
	}
	if _, err := repo.GetByPath(t.Context(), sourcePath); err != nil {
		t.Fatalf("private cleanup failure purged display history: %v", err)
	}
	if _, err := vault.Get(scope.OwnerID, scope.WorkflowID, "preview:retry", now); err != nil {
		t.Fatalf("private cleanup failure discarded retryable artifact: %v", err)
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
