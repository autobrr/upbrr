// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

type linkStagingResult struct {
	SavePath string
	Linked   bool
}

var createReflink = reflinkFile

func (s *Service) prepareLinkStaging(ctx context.Context, clientName string, client config.TorrentClientConfig, meta api.PreparedMetadata, tracker string) (linkStagingResult, error) {
	mode := client.LinkingMode()
	if mode == "" {
		s.logger.Tracef("clients: %s link staging disabled", clientName)
		return linkStagingResult{}, nil
	}
	if mode != "symlink" && mode != "hardlink" && mode != "reflink" {
		return linkStagingResult{}, fmt.Errorf("clients: %s linking must be symlink, hardlink, reflink, or empty", clientName)
	}

	source, err := sourcePathForLinking(meta)
	if err != nil {
		return linkStagingResult{}, fmt.Errorf("clients: %s linking source: %w", clientName, err)
	}
	linkTarget, err := selectLinkedFolder(source, client.LinkedFolder, mode)
	if err != nil {
		return linkStagingResult{}, fmt.Errorf("clients: %s linking target: %w", clientName, err)
	}

	trackerDirName := s.trackerLinkDirName(tracker)
	trackerDir := filepath.Join(linkTarget, trackerDirName)
	if err := os.MkdirAll(trackerDir, 0o700); err != nil {
		return linkStagingResult{}, fmt.Errorf("clients: %s create link target: %w", clientName, err)
	}

	dest := filepath.Join(trackerDir, filepath.Base(source))
	if !pathutil.IsWithinRoot(trackerDir, dest) {
		return linkStagingResult{}, fmt.Errorf("clients: %s link destination escapes target: %w", clientName, internalerrors.ErrInvalidInput)
	}
	if err := createLinkTree(ctx, source, dest, mode); err != nil {
		if client.FallbackAllowed() {
			s.logger.Warnf("clients: %s %s failed for %s, falling back to original qbit path: %v", clientName, mode, source, err)
			return linkStagingResult{}, nil
		}
		return linkStagingResult{}, fmt.Errorf("clients: %s %s: %w", clientName, mode, err)
	}

	savePath := mapLocalPathToRemote(trackerDir, client.LocalPath, client.RemotePath)
	result := linkStagingResult{SavePath: qbitSavePath(savePath), Linked: true}
	s.logger.Debugf("clients: %s linked content tracker=%s mode=%s save_path=%s", clientName, strings.TrimSpace(tracker), mode, result.SavePath)
	return result, nil
}

func (s *Service) trackerLinkDirName(tracker string) string {
	trimmed := strings.TrimSpace(tracker)
	if trimmed == "" {
		return "UNKNOWN"
	}
	for name, cfg := range s.cfg.Trackers.Trackers {
		if !strings.EqualFold(strings.TrimSpace(name), trimmed) {
			continue
		}
		if linkDirName := strings.TrimSpace(cfg.LinkDirName); linkDirName != "" {
			return linkDirName
		}
		return strings.TrimSpace(name)
	}
	return trimmed
}

func sourcePathForLinking(meta api.PreparedMetadata) (string, error) {
	if len(meta.FileList) == 1 {
		if candidate := strings.TrimSpace(meta.FileList[0]); candidate != "" {
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() {
				return absLocalPath("linking candidate", candidate)
			}
		}
	}
	source := strings.TrimSpace(meta.SourcePath)
	if source == "" {
		return "", internalerrors.ErrInvalidInput
	}
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return "", internalerrors.ErrNotFound
		}
		return "", fmt.Errorf("stat linking source: %w", err)
	}
	if !info.IsDir() {
		return absLocalPath("linking source", source)
	}
	return absLocalPath("linking source", source)
}

func selectLinkedFolder[S ~[]string](source string, folders S, mode string) (string, error) {
	candidates := nonEmptyClientPaths(folders)
	if len(candidates) == 0 {
		return "", internalerrors.ErrInvalidInput
	}

	sourceAbs, err := absLocalPath("linking source", source)
	if err != nil {
		return "", err
	}
	sourceVolume := filepath.VolumeName(sourceAbs)
	if runtime.GOOS == "windows" || mode == "hardlink" || mode == "reflink" {
		for _, folder := range candidates {
			folderAbs, err := filepath.Abs(folder)
			if err != nil {
				continue
			}
			if strings.EqualFold(filepath.VolumeName(folderAbs), sourceVolume) {
				return folderAbs, nil
			}
		}
		if mode == "symlink" {
			return absLocalPath("linked folder", candidates[0])
		}
		return "", internalerrors.ErrNotFound
	}
	return absLocalPath("linked folder", candidates[0])
}

func createLinkTree(ctx context.Context, source string, dest string, mode string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled: %w", err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return internalerrors.ErrNotFound
		}
		return fmt.Errorf("stat link source: %w", err)
	}
	if _, err := os.Lstat(dest); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat link destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("create link destination directory: %w", err)
	}

	if mode == "symlink" {
		return symlink(source, dest, sourceInfo.IsDir())
	}
	if mode == "reflink" {
		if !sourceInfo.IsDir() {
			if err := createReflink(source, dest); err != nil {
				return fmt.Errorf("create reflink: %w", err)
			}
			return nil
		}
		return reflinkDirectory(ctx, source, dest)
	}
	if !sourceInfo.IsDir() {
		if err := os.Link(source, dest); err != nil {
			return fmt.Errorf("create hardlink: %w", err)
		}
		return nil
	}
	return hardlinkDirectory(ctx, source, dest)
}

func reflinkDirectory(ctx context.Context, source string, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return fmt.Errorf("create reflink destination root: %w", err)
	}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk reflink source: %w", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context canceled: %w", err)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("relative reflink path: %w", err)
		}
		target := filepath.Join(dest, rel)
		if !pathutil.IsWithinRoot(dest, target) {
			return fmt.Errorf("reflink target escapes destination: %w", internalerrors.ErrInvalidInput)
		}
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("create reflink subdirectory: %w", err)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect reflink source entry: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if _, err := os.Lstat(target); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat reflink target: %w", err)
		}
		if err := createReflink(path, target); err != nil {
			return fmt.Errorf("create reflink for directory entry: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk reflink source tree: %w", err)
	}
	return nil
}

func hardlinkDirectory(ctx context.Context, source string, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return fmt.Errorf("create hardlink destination root: %w", err)
	}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk hardlink source: %w", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context canceled: %w", err)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("relative hardlink path: %w", err)
		}
		target := filepath.Join(dest, rel)
		if !pathutil.IsWithinRoot(dest, target) {
			return fmt.Errorf("link target escapes destination: %w", internalerrors.ErrInvalidInput)
		}
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("create hardlink subdirectory: %w", err)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect hardlink source entry: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if _, err := os.Lstat(target); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat hardlink target: %w", err)
		}
		if err := os.Link(path, target); err != nil { //nolint:gosec // Hardlink staging intentionally links files from a user-selected source tree into a guarded destination.
			return fmt.Errorf("create hardlink for directory entry: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk hardlink source tree: %w", err)
	}
	return nil
}

func symlink(source string, dest string, _ bool) error {
	if err := os.Symlink(source, dest); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	return nil
}

func mapLocalPathToRemote[S ~[]string](value string, localPaths S, remotePaths S) string {
	savePath := strings.TrimSpace(value)
	if savePath == "" {
		return ""
	}
	locals := nonEmptyClientPaths(localPaths)
	remotes := nonEmptyClientPaths(remotePaths)
	for idx, localPath := range locals {
		if idx >= len(remotes) {
			break
		}
		remotePath := remotes[idx]
		rel, ok := relativePathUnderRoot(localPath, savePath)
		if !ok {
			continue
		}
		if rel == "." {
			return remotePath
		}
		//pathpolicy:allow Joins configured qBittorrent remote save path with staged relative suffix before API slash normalization.
		return filepath.Join(remotePath, rel)
	}
	return savePath
}

func relativePathUnderRoot(root string, target string) (string, bool) {
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", false
	}
	targetAbs, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", false
	}
	if !pathutil.IsWithinRoot(rootAbs, targetAbs) {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", false
	}
	return rel, true
}

func qbitSavePath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	normalized := filepath.ToSlash(filepath.Clean(trimmed))
	if !strings.HasSuffix(normalized, "/") {
		normalized += "/"
	}
	return normalized
}

func nonEmptyClientPaths[S ~[]string](values S) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func absLocalPath(label string, value string) (string, error) {
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("%s absolute path: %w", label, err)
	}
	return abs, nil
}
