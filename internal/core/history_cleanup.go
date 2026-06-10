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
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func (c *Core) DeleteAllHistoryReleases(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("core: delete all history releases canceled: %w", err)
	}
	if c.repo == nil {
		return 0, errors.New("core: repository not initialized")
	}

	storedPaths, err := c.repo.ListStoredReleasePaths(ctx)
	if err != nil {
		return 0, fmt.Errorf("core: list stored release paths: %w", err)
	}

	deleted := 0
	for _, sourcePath := range storedPaths {
		if err := c.deleteStoredRelease(ctx, sourcePath); err != nil {
			return deleted, err
		}
		deleted++
	}

	return deleted, nil
}

func (c *Core) deleteStoredRelease(ctx context.Context, sourcePath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("core: delete stored release canceled: %w", err)
	}
	trimmedPath := strings.TrimSpace(sourcePath)
	if trimmedPath == "" {
		return internalerrors.ErrInvalidInput
	}
	if c.repo == nil {
		return errors.New("core: repository not initialized")
	}

	tmpRoot, err := db.Subdir(c.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return fmt.Errorf("core: delete history release: resolve tmp dir: %w", err)
	}
	cacheRoot, err := db.Subdir(c.cfg.MainSettings.DBPath, "cache")
	if err != nil {
		return fmt.Errorf("core: delete history release: resolve cache dir: %w", err)
	}
	nfoRoot, err := db.Subdir(c.cfg.MainSettings.DBPath, "nfo")
	if err != nil {
		return fmt.Errorf("core: delete history release: resolve nfo dir: %w", err)
	}

	cleanupPaths, err := c.releaseCleanupPaths(ctx, trimmedPath)
	if err != nil {
		return err
	}

	artifactPaths := make([]string, 0)
	tmpDirs := make(map[string]struct{})
	for _, cleanupPath := range cleanupPaths {
		pathArtifacts, pathTmpDirs, err := c.collectReleaseCleanupTargets(ctx, cleanupPath, tmpRoot)
		if err != nil {
			return err
		}
		artifactPaths = append(artifactPaths, pathArtifacts...)
		for dir := range pathTmpDirs {
			tmpDirs[dir] = struct{}{}
		}
	}

	addDirectoryChildTempDirs(trimmedPath, tmpRoot, tmpDirs)

	for _, cleanupPath := range cleanupPaths {
		if err := c.repo.PurgeContentData(ctx, cleanupPath); err != nil {
			return fmt.Errorf("core: delete history release: %w", err)
		}
	}

	fileRoots := []string{tmpRoot, cacheRoot, nfoRoot}
	for _, filePath := range artifactPaths {
		removed, err := removeIfWithinRoots(fileRoots, filePath, false)
		if err != nil && c.logger != nil {
			c.logger.Warnf("core: delete history release remove file failed %q: %v", filePath, err)
			continue
		}
		if removed && c.logger != nil {
			c.logger.Debugf("core: delete history release removed file %s", filePath)
		}
	}
	for dir := range tmpDirs {
		removed, err := removeIfWithinRoot(tmpRoot, dir, true)
		if err != nil && c.logger != nil {
			c.logger.Warnf("core: delete history release remove tmp dir failed %q: %v", dir, err)
			continue
		}
		if removed && c.logger != nil {
			c.logger.Debugf("core: delete history release removed tmp dir %s", dir)
		}
	}
	if c.logger != nil {
		c.logger.Infof("core: delete history release completed path=%s", trimmedPath)
	}

	return nil
}

func (c *Core) releaseCleanupPaths(ctx context.Context, sourcePath string) ([]string, error) {
	cleanupPaths := []string{sourcePath}
	if c.repo == nil {
		return cleanupPaths, nil
	}
	storedPaths, err := c.repo.ListStoredReleasePaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("core: delete history release list stored paths: %w", err)
	}
	for _, storedPath := range storedPaths {
		if !releasePathRelated(sourcePath, storedPath) {
			continue
		}
		cleanupPaths = append(cleanupPaths, storedPath)
	}
	return compactStrings(cleanupPaths), nil
}

func releasePathRelated(sourcePath string, storedPath string) bool {
	sourcePath = strings.TrimSpace(sourcePath)
	storedPath = strings.TrimSpace(storedPath)
	if sourcePath == "" || storedPath == "" {
		return false
	}
	if pathutil.SamePath(sourcePath, storedPath) {
		return true
	}
	info, err := os.Stat(sourcePath)
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

func (c *Core) collectReleaseCleanupTargets(ctx context.Context, sourcePath string, tmpRoot string) ([]string, map[string]struct{}, error) {
	artifactPaths := make([]string, 0)

	shots, err := c.repo.ListScreenshotsByPath(ctx, sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("core: delete history release list screenshots: %w", err)
	}
	for _, shot := range shots {
		artifactPaths = append(artifactPaths, shot.ImagePath)
	}

	uploaded, err := c.repo.ListUploadedImagesByPath(ctx, sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("core: delete history release list uploaded images: %w", err)
	}
	for _, image := range uploaded {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}

	finals, err := c.repo.ListFinalSelections(ctx, sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("core: delete history release list final selections: %w", err)
	}
	for _, image := range finals {
		artifactPaths = append(artifactPaths, image.ImagePath)
	}

	slots, err := c.repo.ListScreenshotSlotsByPath(ctx, sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("core: delete history release list screenshot slots: %w", err)
	}
	for _, slot := range slots {
		artifactPaths = append(artifactPaths, slot.ImagePath)
		for _, variant := range slot.Variants {
			artifactPaths = append(artifactPaths, variant.ImagePath)
		}
	}

	artifactPaths = compactStrings(artifactPaths)
	tmpDirs := make(map[string]struct{})
	fallbackBase := paths.ReleaseTempBase(api.PreparedMetadata{}, sourcePath)
	tmpDirs[filepath.Join(tmpRoot, fallbackBase)] = struct{}{}

	stored, err := c.repo.GetByPath(ctx, sourcePath)
	if err == nil {
		releaseBase := paths.ReleaseTempBase(api.PreparedMetadata{
			Release: api.ReleaseInfo{
				Title:    stored.Title,
				Alt:      stored.Alt,
				Year:     stored.Year,
				Category: string(stored.Category),
				Source:   stored.Source,
				Type:     stored.Type,
				Group:    stored.Group,
			},
		}, sourcePath)
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

func addDirectoryChildTempDirs(sourcePath string, tmpRoot string, tmpDirs map[string]struct{}) {
	info, err := os.Stat(sourcePath)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(sourcePath)
	if err != nil {
		return
	}
	for _, entry := range entries {
		childPath := filepath.Join(sourcePath, entry.Name())
		base := paths.ReleaseTempBase(api.PreparedMetadata{}, childPath)
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

func removeIfWithinRoot(root string, target string, recursive bool) (bool, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return false, nil
	}
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return false, fmt.Errorf("cleanup history artifact: resolve root path: %w", err)
	}
	absTarget, err := filepath.Abs(trimmed)
	if err != nil {
		return false, fmt.Errorf("cleanup history artifact: resolve target path: %w", err)
	}
	if pathutil.SamePath(absRoot, absTarget) {
		return false, nil
	}
	if !pathutil.IsWithinRoot(absRoot, absTarget) {
		return false, nil
	}
	if recursive {
		if _, err := os.Stat(absTarget); err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("cleanup history artifact: stat target: %w", err)
		}
		if err := os.RemoveAll(absTarget); err != nil {
			return false, fmt.Errorf("cleanup history artifact: remove target tree: %w", err)
		}
		return true, nil
	}
	if err := os.Remove(absTarget); err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("cleanup history artifact: remove target: %w", err)
	}
	if _, err := os.Stat(absTarget); err == nil {
		return false, nil
	}
	return true, nil
}

func removeIfWithinRoots(roots []string, target string, recursive bool) (bool, error) {
	for _, root := range roots {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			continue
		}
		removed, err := removeIfWithinRoot(trimmed, target, recursive)
		if err != nil {
			return false, err
		}
		if removed {
			return true, nil
		}
	}
	return false, nil
}
