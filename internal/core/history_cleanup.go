// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
	err := h.repo.WithHistoryDeletion(ctx, func(workCtx context.Context) error {
		return h.deleteStoredReleaseInTransaction(workCtx, sourcePath)
	})
	if err != nil {
		return fmt.Errorf("core: delete history release transaction: %w", err)
	}
	if h.logger != nil {
		h.logger.Infof("core: delete history release completed path=%s", sourcePath)
	}
	return nil
}

// cleanupOrphanedHistory completes old History deletions before accepting new
// input. Selection and cleanup share the same writer reservation, including
// private artifact removal while the workflow's durable scope is still known.
func (h *historyModule) cleanupOrphanedHistory(ctx context.Context, repo *db.SQLiteRepository) error {
	var sources, workflows int
	err := h.repo.WithHistoryDeletion(ctx, func(workCtx context.Context) error {
		paths, scopes, err := repo.ListOrphanedHistoryPaths(workCtx)
		if err != nil {
			return fmt.Errorf("core: list orphaned history: %w", err)
		}
		workflows = len(scopes)
		if len(paths)+workflows > 0 && h.logger != nil {
			h.logger.Infof("history: orphan cleanup started sources=%d empty_workflows=%d", len(paths), workflows)
		}
		for _, sourcePath := range paths {
			if err := h.deleteStoredReleaseInTransaction(workCtx, sourcePath); err != nil {
				if errors.Is(err, api.ErrActiveInputBusy) {
					// Legacy temp names can collide across otherwise unrelated
					// sources. Leave that orphan for a later idle startup.
					if h.logger != nil {
						h.logger.Debugf("history: orphan cleanup decision=defer reason=active_input")
					}
					continue
				}
				return err
			}
			sources++
		}
		if err := h.deletePrivateWorkflowScopes(scopes); err != nil {
			return err
		}
		for _, scope := range scopes {
			if err := repo.DeleteReleaseWorkflowState(workCtx, scope.OwnerID, scope.WorkflowID); err != nil {
				return fmt.Errorf("core: delete orphaned workflow: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("core: orphaned history cleanup transaction: %w", err)
	}
	if sources+workflows > 0 && h.logger != nil {
		h.logger.Infof("history: orphan cleanup completed sources=%d empty_workflows=%d", sources, workflows)
	}
	return nil
}

func (h *historyModule) deleteStoredReleaseInTransaction(ctx context.Context, sourcePath string) error {
	cleanupPaths, err := h.releaseCleanupPaths(ctx, sourcePath)
	if err != nil {
		return err
	}
	if err := h.deleteStoredReleaseData(ctx, sourcePath, cleanupPaths); err != nil {
		return err
	}
	if h.preparedFacts != nil {
		for _, cleanupPath := range cleanupPaths {
			h.preparedFacts.Invalidate(cleanupPath)
		}
	}
	return nil
}

func (h *historyModule) deleteStoredReleaseData(ctx context.Context, sourcePath string, cleanupPaths []string) error {
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

	if err := h.ensureHistoryDeletionInactive(ctx, cleanupPaths); err != nil {
		return err
	}

	artifactPaths := make([]string, 0)
	tmpDirs := make(map[string]struct{})
	workflowScopes := make([]api.WorkflowScope, 0)
	for _, cleanupPath := range cleanupPaths {
		pathArtifacts, pathTmpDirs, pathWorkflowScopes, err := h.collectReleaseCleanupTargets(ctx, cleanupPath, tmpRoot)
		if err != nil {
			return err
		}
		artifactPaths = append(artifactPaths, pathArtifacts...)
		workflowScopes = append(workflowScopes, pathWorkflowScopes...)
		for dir := range pathTmpDirs {
			tmpDirs[dir] = struct{}{}
		}
	}

	addDirectoryChildTempDirs(h.fs, trimmedPath, tmpRoot, tmpDirs)

	fileRoots := []string{tmpRoot, cacheRoot, nfoRoot}
	removableArtifacts := make([]string, 0, len(artifactPaths))
	for _, filePath := range artifactPaths {
		removable, err := ensureRemovableWithinRootsFS(h.fs, fileRoots, filePath, false)
		if err != nil {
			return fmt.Errorf("core: delete history release validate file %q: %w", filePath, err)
		}
		if removable {
			removableArtifacts = append(removableArtifacts, filePath)
		}
	}
	removableTmpDirs := make([]string, 0, len(tmpDirs))
	for dir := range tmpDirs {
		removable, err := ensureRemovableWithinRootFS(h.fs, tmpRoot, dir, true)
		if err != nil {
			return fmt.Errorf("core: delete history release validate tmp dir %q: %w", dir, err)
		}
		if removable {
			removableTmpDirs = append(removableTmpDirs, dir)
		}
	}
	storedSourcePaths, err := h.repo.ListStoredReleasePaths(ctx)
	if err != nil {
		return fmt.Errorf("core: delete history release list stored source paths: %w", err)
	}
	protectedSourcePaths, err := h.repo.ListHistoryProtectedSourcePaths(ctx)
	if err != nil {
		return fmt.Errorf("core: delete history release protected source paths: %w", err)
	}
	if err := ensureHistoryTempTargetsUnprotected(tmpRoot, artifactPaths, tmpDirs, protectedSourcePaths); err != nil {
		return err
	}
	knownSourcePaths := compactStrings(append(append(append([]string{trimmedPath}, cleanupPaths...), storedSourcePaths...), protectedSourcePaths...))
	if err := ensureHistoryCleanupTargetsDoNotContainSources(removableArtifacts, removableTmpDirs, knownSourcePaths); err != nil {
		return err
	}
	for _, cleanupPath := range cleanupPaths {
		if err := h.repo.PurgeContentData(ctx, cleanupPath); err != nil {
			return fmt.Errorf("core: delete history release: %w", err)
		}
	}
	retainedArtifacts, err := h.repo.ListStoredHistoryArtifactPaths(ctx)
	if err != nil {
		return fmt.Errorf("core: delete history release retained artifacts: %w", err)
	}
	if err := h.deletePrivateWorkflowScopes(workflowScopes); err != nil {
		return err
	}
	for _, filePath := range artifactPaths {
		if historyArtifactRetained(filePath, retainedArtifacts) {
			continue
		}
		removed, err := removeIfWithinRootsFS(h.fs, fileRoots, filePath, false)
		if err != nil {
			return fmt.Errorf("core: delete history release remove file %q: %w", filePath, err)
		}
		if removed && h.logger != nil {
			h.logger.Debugf("core: delete history release removed file %s", filePath)
		}
	}
	for dir := range tmpDirs {
		removed, err := removeHistoryTempDirFS(h.fs, tmpRoot, dir, retainedArtifacts)
		if err != nil {
			return fmt.Errorf("core: delete history release remove tmp dir %q: %w", dir, err)
		}
		if removed && h.logger != nil {
			h.logger.Debugf("core: delete history release removed tmp dir %s", dir)
		}
	}
	return nil
}

func historyArtifactRetained(target string, retainedArtifacts []string) bool {
	for _, retained := range retainedArtifacts {
		if pathutil.SamePath(target, retained) {
			return true
		}
	}
	return false
}

// removeHistoryTempDirFS preserves a shared directory until its last artifact
// reference is gone. Explicit artifact cleanup still removes unshared files.
func removeHistoryTempDirFS(filesystem historyFilesystem, root, target string, retainedArtifacts []string) (bool, error) {
	for _, retained := range retainedArtifacts {
		if pathutil.SamePath(target, retained) || pathutil.IsWithinRoot(target, retained) {
			return false, nil
		}
	}
	return removeIfWithinRootFS(filesystem, root, target, true)
}

// ensureHistoryDeletionInactive rejects removal of a current input's display
// history. A parent-source deletion also protects stored child sources.
func (h *historyModule) ensureHistoryDeletionInactive(ctx context.Context, cleanupPaths []string) error {
	if h.activeInputs == nil {
		return nil
	}
	slot, err := h.activeInputs.LoadActiveInput(ctx)
	if err != nil {
		return fmt.Errorf("core: delete history release active input: %w", err)
	}
	if slot.State == api.ActiveInputEmpty {
		return nil
	}
	activePaths := make([]string, 0, 2)
	if strings.TrimSpace(slot.InputID) != "" {
		record, recordErr := h.activeInputs.LoadInputRecordByID(ctx, slot.InputID)
		if recordErr != nil {
			return api.ErrActiveInputBusy
		}
		activePaths = append(activePaths, record.CanonicalPath)
	}
	activePaths = append(activePaths, slot.RequestedPath)
	for _, activePath := range activePaths {
		for _, cleanupPath := range cleanupPaths {
			if releasePathRelated(h.fs, cleanupPath, activePath) || releasePathRelated(h.fs, activePath, cleanupPath) {
				return api.ErrActiveInputBusy
			}
		}
	}
	return nil
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
	// A missing source can still own persisted child releases. Their path
	// containment remains valid after the original directory has been removed.
	if (err != nil && !errors.Is(err, os.ErrNotExist)) || (err == nil && !info.IsDir()) {
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

func (h *historyModule) collectReleaseCleanupTargets(
	ctx context.Context,
	sourcePath string,
	tmpRoot string,
) ([]string, map[string]struct{}, []api.WorkflowScope, error) {
	artifactPaths := make([]string, 0)

	snapshot, err := h.repo.LoadHistoryCleanupSnapshot(ctx, sourcePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("core: delete history release load cleanup snapshot: %w", err)
	}
	artifactPaths = append(artifactPaths, snapshot.ArtifactPaths...)

	artifactPaths = compactStrings(artifactPaths)
	tmpDirs := make(map[string]struct{})
	fallbackBase := paths.ReleaseTempBaseFor(sourcePath, api.ReleaseInfo{})
	tmpDirs[filepath.Join(tmpRoot, fallbackBase)] = struct{}{}
	reuseRoot, err := reusableMediaTempRoot(tmpRoot, sourcePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("core: delete history release resolve reusable media root: %w", err)
	}
	tmpDirs[reuseRoot] = struct{}{}

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

	return artifactPaths, tmpDirs, append([]api.WorkflowScope(nil), snapshot.WorkflowScopes...), nil
}

func (h *historyModule) deletePrivateWorkflowScopes(scopes []api.WorkflowScope) error {
	if h.privateVault == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		ownerID := strings.TrimSpace(scope.OwnerID)
		workflowID := api.WorkflowID(strings.TrimSpace(string(scope.WorkflowID)))
		if ownerID == "" || workflowID == "" {
			return errors.New("core: delete history release invalid workflow scope")
		}
		key := ownerID + "\x00" + string(workflowID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := h.privateVault.DeleteWorkflow(ownerID, workflowID); err != nil {
			return fmt.Errorf("core: delete history release private workflow scope: %w", err)
		}
	}
	return nil
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

func ensureHistoryTempTargetsUnprotected(
	tmpRoot string,
	artifactPaths []string,
	tmpDirs map[string]struct{},
	protectedSourcePaths []string,
) error {
	protectedRoots := make([]string, 0, len(protectedSourcePaths)*2)
	for _, sourcePath := range compactStrings(protectedSourcePaths) {
		legacyBase := strings.TrimSpace(paths.ReleaseTempBaseFor(sourcePath, api.ReleaseInfo{}))
		if legacyBase != "" {
			protectedRoots = append(protectedRoots, filepath.Join(tmpRoot, legacyBase))
		}
		reuseRoot, err := reusableMediaTempRoot(tmpRoot, sourcePath)
		if err != nil {
			return fmt.Errorf("core: delete history release resolve protected reusable media root: %w", err)
		}
		protectedRoots = append(protectedRoots, reuseRoot)
	}
	for _, target := range append(append([]string(nil), artifactPaths...), slices.Collect(maps.Keys(tmpDirs))...) {
		for _, protectedRoot := range protectedRoots {
			if historyPathsIntersect(target, protectedRoot) {
				return api.ErrActiveInputBusy
			}
		}
	}
	return nil
}

func ensureHistoryCleanupTargetsDoNotContainSources(files, tmpDirs, sourcePaths []string) error {
	for _, target := range append(append([]string(nil), files...), tmpDirs...) {
		for _, sourcePath := range sourcePaths {
			if pathutil.SamePath(target, sourcePath) || pathutil.IsWithinRoot(target, sourcePath) {
				return api.ErrActiveInputBusy
			}
		}
	}
	return nil
}

func historyPathsIntersect(left, right string) bool {
	return pathutil.SamePath(left, right) || pathutil.IsWithinRoot(left, right) || pathutil.IsWithinRoot(right, left)
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
