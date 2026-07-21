// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func (h *historyModule) DeleteAll(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("core: delete all history releases canceled: %w", err)
	}
	if h == nil || h.repo == nil {
		return 0, errors.New("core: repository not initialized")
	}

	storedPaths, err := h.repo.ListStoredReleasePaths(ctx)
	if err != nil {
		return 0, fmt.Errorf("core: list stored release paths: %w", err)
	}

	deleted := 0
	for _, sourcePath := range storedPaths {
		if err := h.deleteStoredRelease(ctx, sourcePath); err != nil {
			return deleted, err
		}
		deleted++
	}

	return deleted, nil
}

func (h *historyModule) Delete(ctx context.Context, sourcePath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("core: delete history release canceled: %w", err)
	}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	if h == nil || h.repo == nil {
		return errors.New("core: repository not initialized")
	}
	return h.deleteStoredRelease(ctx, trimmed)
}

func (h *historyModule) deleteStoredRelease(ctx context.Context, sourcePath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("core: delete stored release canceled: %w", err)
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	if trimmedPath == "" {
		return internalerrors.ErrInvalidInput
	}
	if h == nil || h.repo == nil {
		return errors.New("core: repository not initialized")
	}

	tmpRoot, err := db.Subdir(h.dbPath, "tmp")
	if err != nil {
		return fmt.Errorf("core: delete history release: resolve tmp dir: %w", err)
	}
	cacheRoot, err := db.Subdir(h.dbPath, "cache")
	if err != nil {
		return fmt.Errorf("core: delete history release: resolve cache dir: %w", err)
	}
	nfoRoot, err := db.Subdir(h.dbPath, "nfo")
	if err != nil {
		return fmt.Errorf("core: delete history release: resolve nfo dir: %w", err)
	}

	cleanupPaths, err := h.releaseCleanupPaths(ctx, trimmedPath)
	if err != nil {
		return err
	}

	artifactPaths := make([]string, 0)
	tmpDirs := make(map[string]struct{})
	for _, cleanupPath := range cleanupPaths {
		pathArtifacts, pathTmpDirs, err := h.collectReleaseCleanupTargets(ctx, cleanupPath, tmpRoot)
		if err != nil {
			return err
		}
		artifactPaths = append(artifactPaths, pathArtifacts...)
		for dir := range pathTmpDirs {
			tmpDirs[dir] = struct{}{}
		}
	}

	addDirectoryChildTempDirs(h.fs, trimmedPath, tmpRoot, tmpDirs)

	fileRoots := []string{tmpRoot, cacheRoot, nfoRoot}
	for _, filePath := range artifactPaths {
		if _, err := ensureRemovableWithinRootsFS(h.fs, fileRoots, filePath, false); err != nil {
			return fmt.Errorf("core: delete history release validate file %q: %w", filePath, err)
		}
	}
	for dir := range tmpDirs {
		if _, err := ensureRemovableWithinRootFS(h.fs, tmpRoot, dir, true); err != nil {
			return fmt.Errorf("core: delete history release validate tmp dir %q: %w", dir, err)
		}
	}
	deletePrepared := func(workCtx context.Context, invalidate func(string)) error {
		for _, cleanupPath := range cleanupPaths {
			if err := h.repo.PurgeContentData(workCtx, cleanupPath); err != nil {
				return fmt.Errorf("core: delete history release: %w", err)
			}
			invalidate(cleanupPath)
		}
		for _, filePath := range artifactPaths {
			removed, err := removeIfWithinRootsFS(h.fs, fileRoots, filePath, false)
			if err != nil {
				return fmt.Errorf("core: delete history release remove file %q: %w", filePath, err)
			}
			if removed && h.logger != nil {
				h.logger.Debugf("core: delete history release removed file %s", filePath)
			}
		}
		for dir := range tmpDirs {
			removed, err := removeIfWithinRootFS(h.fs, tmpRoot, dir, true)
			if err != nil {
				return fmt.Errorf("core: delete history release remove tmp dir %q: %w", dir, err)
			}
			if removed && h.logger != nil {
				h.logger.Debugf("core: delete history release removed tmp dir %s", dir)
			}
		}
		if h.logger != nil {
			h.logger.Infof("core: delete history release completed path=%s", trimmedPath)
		}
		return nil
	}
	if h.preparedFacts != nil {
		for _, cleanupPath := range cleanupPaths {
			if err := h.preparedFacts.Purge(ctx, cleanupPath); err != nil {
				return fmt.Errorf("core: purge prepared release %q: %w", cleanupPath, err)
			}
		}
	}
	return deletePrepared(ctx, func(string) {})
}

func (h *historyModule) releaseCleanupPaths(ctx context.Context, sourcePath string) ([]string, error) {
	cleanupPaths := []string{sourcePath}
	if h.repo == nil {
		return cleanupPaths, nil
	}
	storedPaths, err := h.repo.ListStoredReleasePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("core: delete history release list stored paths: %w", err)
	}
	for _, storedPath := range storedPaths {
		if !releasePathRelated(h.fs, sourcePath, storedPath) {
			continue
		}
		cleanupPaths = append(cleanupPaths, storedPath)
	}
	return compactStrings(cleanupPaths), nil
}

func releasePathRelated(filesystem historyFilesystem, sourcePath string, storedPath string) bool {
	sourcePath = strings.TrimSpace(sourcePath)
	storedPath = strings.TrimSpace(storedPath)
	if sourcePath == "" || storedPath == "" {
		return false
	}
	if pathutil.SamePath(sourcePath, storedPath) {
		return true
	}
	info, err := filesystem.Stat(sourcePath)
	if err != nil || !info.IsDir() {
		return false
	}
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return false
	}
	absStored, err := filepath.Abs(storedPath)
	if err != nil {
		return false
	}
	return pathutil.IsWithinRoot(absSource, absStored)
}

func (h *historyModule) collectReleaseCleanupTargets(ctx context.Context, sourcePath string, tmpRoot string) ([]string, map[string]struct{}, error) {
	artifactPaths := make([]string, 0)

	snapshot, err := h.repo.LoadHistoryCleanupSnapshot(ctx, sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("core: delete history release load cleanup snapshot: %w", err)
	}
	artifactPaths = append(artifactPaths, snapshot.ArtifactPaths...)

	artifactPaths = compactStrings(artifactPaths)
	tmpDirs := make(map[string]struct{})
	fallbackBase := paths.ReleaseTempBaseFor(sourcePath, api.ReleaseInfo{})
	tmpDirs[filepath.Join(tmpRoot, fallbackBase)] = struct{}{}

	if snapshot.Metadata != nil {
		stored := *snapshot.Metadata
		releaseBase := paths.ReleaseTempBaseFor(sourcePath, api.ReleaseInfo{
			Title:    stored.Title,
			Alt:      stored.Alt,
			Year:     stored.Year,
			Category: string(stored.Category),
			Source:   stored.Source,
			Type:     stored.Type,
			Group:    stored.Group,
		})
		tmpDirs[filepath.Join(tmpRoot, releaseBase)] = struct{}{}
	}
	for _, filePath := range artifactPaths {
		contentRoot, ok := resolveContentTmpRoot(tmpRoot, filePath)
		if !ok {
			continue
		}
		tmpDirs[contentRoot] = struct{}{}
	}

	return artifactPaths, tmpDirs, nil
}

func addDirectoryChildTempDirs(filesystem historyFilesystem, sourcePath string, tmpRoot string, tmpDirs map[string]struct{}) {
	info, err := filesystem.Stat(sourcePath)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := filesystem.ReadDir(sourcePath)
	if err != nil {
		return
	}
	for _, entry := range entries {
		childPath := filepath.Join(sourcePath, entry.Name())
		base := paths.ReleaseTempBaseFor(childPath, api.ReleaseInfo{})
		if strings.TrimSpace(base) == "" {
			continue
		}
		tmpDirs[filepath.Join(tmpRoot, base)] = struct{}{}
	}
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		compacted = append(compacted, trimmed)
	}
	return compacted
}

func resolveContentTmpRoot(tmpRoot string, candidate string) (string, bool) {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return "", false
	}
	absCandidate, err := filepath.Abs(trimmed)
	if err != nil {
		return "", false
	}
	absTmpRoot, err := filepath.Abs(strings.TrimSpace(tmpRoot))
	if err != nil {
		return "", false
	}
	if !pathutil.IsWithinRoot(absTmpRoot, absCandidate) {
		return "", false
	}
	rel, err := filepath.Rel(absTmpRoot, absCandidate)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 1 || strings.TrimSpace(parts[0]) == "" || parts[0] == "." {
		return "", false
	}
	return filepath.Join(absTmpRoot, parts[0]), true
}

func removeIfWithinRootFS(filesystem historyFilesystem, root string, target string, recursive bool) (bool, error) {
	absTarget, shouldRemove, err := inspectCleanupTargetFS(filesystem, root, target, recursive)
	if err != nil {
		return false, err
	}
	if !shouldRemove {
		return false, nil
	}
	if recursive {
		if err := filesystem.RemoveAll(absTarget); err != nil {
			return false, fmt.Errorf("cleanup history artifact: remove target tree: %w", err)
		}
		return true, nil
	}
	if err := filesystem.Remove(absTarget); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("cleanup history artifact: remove target: %w", err)
	}
	if _, err := filesystem.Stat(absTarget); err == nil {
		return false, nil
	}
	return true, nil
}

func ensureRemovableWithinRootFS(filesystem historyFilesystem, root string, target string, recursive bool) (bool, error) {
	_, shouldRemove, err := inspectCleanupTargetFS(filesystem, root, target, recursive)
	return shouldRemove, err
}

func inspectCleanupTargetFS(filesystem historyFilesystem, root string, target string, recursive bool) (string, bool, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", false, nil
	}
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", false, fmt.Errorf("cleanup history artifact: resolve root path: %w", err)
	}
	absTarget, err := filepath.Abs(trimmed)
	if err != nil {
		return "", false, fmt.Errorf("cleanup history artifact: resolve target path: %w", err)
	}
	if pathutil.SamePath(absRoot, absTarget) {
		return "", false, nil
	}
	if !pathutil.IsWithinRoot(absRoot, absTarget) {
		return "", false, nil
	}
	info, err := filesystem.Stat(absTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return absTarget, false, nil
		}
		return "", false, fmt.Errorf("cleanup history artifact: stat target: %w", err)
	}
	if !recursive && info.IsDir() {
		return "", false, fmt.Errorf("cleanup history artifact: target is directory: %s", absTarget)
	}
	return absTarget, true, nil
}

func removeIfWithinRootsFS(filesystem historyFilesystem, roots []string, target string, recursive bool) (bool, error) {
	for _, root := range roots {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			continue
		}
		removed, err := removeIfWithinRootFS(filesystem, trimmed, target, recursive)
		if err != nil {
			return false, err
		}
		if removed {
			return true, nil
		}
	}
	return false, nil
}

func ensureRemovableWithinRootsFS(filesystem historyFilesystem, roots []string, target string, recursive bool) (bool, error) {
	for _, root := range roots {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			continue
		}
		removable, err := ensureRemovableWithinRootFS(filesystem, trimmed, target, recursive)
		if err != nil {
			return false, err
		}
		if removable {
			return true, nil
		}
	}
	return false, nil
}
