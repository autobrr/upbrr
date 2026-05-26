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
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func (c *Core) DeleteAllHistoryReleases(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
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
		return err
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

	artifactPaths, tmpDirs, err := c.collectReleaseCleanupTargets(ctx, trimmedPath, tmpRoot)
	if err != nil {
		return err
	}

	if err := c.repo.PurgeContentData(ctx, trimmedPath); err != nil {
		return fmt.Errorf("core: delete history release: %w", err)
	}

	fileRoots := []string{tmpRoot, cacheRoot, nfoRoot}
	for _, filePath := range artifactPaths {
		if _, err := removeIfWithinRoots(fileRoots, filePath, false); err != nil && c.logger != nil {
			c.logger.Warnf("core: delete history release remove file failed %q: %v", filePath, err)
		}
	}
	for dir := range tmpDirs {
		if _, err := removeIfWithinRoot(tmpRoot, dir, true); err != nil && c.logger != nil {
			c.logger.Warnf("core: delete history release remove tmp dir failed %q: %v", dir, err)
		}
	}
	if c.logger != nil {
		c.logger.Infof("core: delete history release completed path=%s", trimmedPath)
	}

	return nil
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
	if !pathWithinRoot(absTmpRoot, absCandidate) {
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
		return false, err
	}
	absTarget, err := filepath.Abs(trimmed)
	if err != nil {
		return false, err
	}
	if absTarget == absRoot {
		return false, nil
	}
	if !pathWithinRoot(absRoot, absTarget) {
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

func pathWithinRoot(root string, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)
}
