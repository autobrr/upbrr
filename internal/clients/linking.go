// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

// linkStagingResult records a link-staging decision and any cleanup needed if
// the subsequent client add fails. HashCheckRequired distinguishes an unsafe
// original-path fallback from ordinary unlinked mode. LayoutValidated and
// FileCount describe only staging derived from a local torrent artifact.
type linkStagingResult struct {
	SavePath          string
	Linked            bool
	HashCheckRequired bool
	LayoutValidated   bool
	FileCount         int
	Cleanup           *linkStagingCleanup
}

type linkStagingCleanup struct {
	TrackerDir       string
	Dest             string
	RemoveTrackerDir bool
}

func (c *linkStagingCleanup) Run() error {
	if c == nil {
		return nil
	}
	trackerDir, err := absLocalPath("link staging tracker dir", c.TrackerDir)
	if err != nil {
		return err
	}
	dest, err := absLocalPath("link staging destination", c.Dest)
	if err != nil {
		return err
	}
	if !pathutil.IsWithinRoot(trackerDir, dest) || pathutil.SamePath(trackerDir, dest) {
		return fmt.Errorf("link staging cleanup destination escapes tracker dir: %w", internalerrors.ErrInvalidInput)
	}
	if _, err := os.Lstat(dest); err == nil {
		if err := os.RemoveAll(dest); err != nil {
			return fmt.Errorf("remove staged destination: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat staged destination: %w", err)
	}
	if !c.RemoveTrackerDir {
		return nil
	}
	entries, err := os.ReadDir(trackerDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("list staged tracker dir: %w", err)
	}
	if len(entries) != 0 {
		return nil
	}
	if err := os.Remove(trackerDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove staged tracker dir: %w", err)
	}
	return nil
}

var createReflink = reflinkFile

// prepareLinkStaging creates a client-visible staging tree for the final local
// torrent artifact. It uses the torrent's metainfo layout, validates staged
// files before returning, and maps the containing directory to the qBittorrent
// host path. URL-only or invalid layouts fall back only when the client permits
// it; every original-path fallback requires qBittorrent to verify content.
// Cancellation during metainfo planning returns a wrapped context error instead
// of falling back.
func (s *Service) prepareLinkStaging(ctx context.Context, clientName string, client config.TorrentClientConfig, meta api.PreparedMetadata, torrent api.TorrentResult) (linkStagingResult, error) {
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
	linkTargets, err := linkedFolderCandidates(source, client.LinkedFolder, mode)
	if err != nil {
		return linkStagingResult{}, fmt.Errorf("clients: %s linking target: %w", clientName, err)
	}

	tracker := strings.TrimSpace(torrent.Tracker)
	trackerDirName, err := sanitizeTrackerLinkDirName(s.trackerLinkDirName(tracker))
	if err != nil {
		return linkStagingResult{}, fmt.Errorf("clients: %s invalid link staging directory for tracker %q: %w", clientName, strings.TrimSpace(tracker), err)
	}

	var torrentPlan torrentLinkPlan
	if torrentPath := strings.TrimSpace(torrent.Path); torrentPath != "" {
		s.logger.Debugf("clients: inspecting injected torrent layout client=%s tracker=%s mode=%s", clientName, tracker, mode)
		plan, err := buildTorrentLinkPlan(ctx, torrentPath, meta)
		if err != nil {
			if ctx.Err() != nil {
				return linkStagingResult{}, fmt.Errorf("clients: %s plan %s staging from injected torrent: %w", clientName, mode, err)
			}
			if client.FallbackAllowed() {
				s.logger.Warnf("clients: injected torrent layout validation failed client=%s tracker=%s mode=%s decision=original-path-fallback reason=%s", clientName, tracker, mode, redaction.RedactValue(err.Error(), nil))
				return linkStagingResult{HashCheckRequired: true}, nil
			}
			return linkStagingResult{}, fmt.Errorf("clients: %s plan %s staging from injected torrent: %w", clientName, mode, err)
		}
		torrentPlan = plan
		s.logger.Debugf("clients: injected torrent layout ready client=%s tracker=%s root=%q files=%d padding_files=%d multi_file=%t", clientName, tracker, plan.root, len(plan.files), plan.paddingFiles, plan.torrentIsMulti)
		for _, file := range plan.files {
			s.logger.Tracef("clients: injected torrent file mapped client=%s tracker=%s match=%s source=%s destination=%s", clientName, tracker, file.match, file.sourcePath, file.destRel)
		}
	} else {
		if !client.FallbackAllowed() {
			return linkStagingResult{}, fmt.Errorf("clients: %s cannot validate %s staging for URL-only torrent; provide a torrent file or enable allow_fallback for this client", clientName, mode)
		}
		s.logger.Warnf("clients: URL-only linked injection cannot inspect torrent layout client=%s tracker=%s mode=%s decision=original-path-fallback", clientName, tracker, mode)
		return linkStagingResult{HashCheckRequired: true}, nil
	}

	mappingPairs := pathMappingPairs(client.LocalPath, client.RemotePath)
	mappingRequired := len(mappingPairs) > 0
	mappingCandidateFound := false
	var lastErr error
	for _, linkTarget := range linkTargets {
		trackerDir := filepath.Join(linkTarget, trackerDirName)
		if !pathutil.IsWithinRoot(linkTarget, trackerDir) {
			lastErr = fmt.Errorf("clients: %s link staging directory escapes target: %w", clientName, internalerrors.ErrInvalidInput)
			continue
		}

		savePath := trackerDir
		mappingState := "not-configured"
		if mappingRequired {
			mappedPath, mapped := mappedRemotePath(trackerDir, client.LocalPath, client.RemotePath)
			if !mapped {
				lastErr = fmt.Errorf("clients: %s linked staging path is outside configured local_path roots: %w", clientName, internalerrors.ErrInvalidInput)
				s.logger.Debugf("clients: linked torrent path mapping skipped client=%s tracker=%s mode=%s state=no-local-root-match tracker_dir=%s", clientName, tracker, mode, trackerDir)
				continue
			}
			mappingCandidateFound = true
			mappingState = "matched"
			savePath = mappedPath
			s.logger.Debugf("clients: linked torrent path mapping ready client=%s tracker=%s mode=%s state=%s local=%s remote=%s", clientName, tracker, mode, mappingState, trackerDir, savePath)
		}

		trackerDirExisted, err := pathExists(trackerDir)
		if err != nil {
			lastErr = fmt.Errorf("clients: %s stat link staging directory: %w", clientName, err)
			continue
		}
		if err := os.MkdirAll(trackerDir, 0o700); err != nil {
			lastErr = fmt.Errorf("clients: %s create link target: %w", clientName, err)
			continue
		}

		dest := filepath.Join(trackerDir, torrentPlan.root)
		if !pathutil.IsWithinRoot(trackerDir, dest) {
			lastErr = fmt.Errorf("clients: %s link destination escapes target: %w", clientName, internalerrors.ErrInvalidInput)
			continue
		}
		destExisted, err := pathExists(dest)
		if err != nil {
			lastErr = fmt.Errorf("clients: %s stat link destination: %w", clientName, err)
			continue
		}
		linkErr := createTorrentLinkPlan(ctx, trackerDir, torrentPlan, mode)
		if linkErr != nil {
			if !destExisted {
				if cleanupErr := (&linkStagingCleanup{
					TrackerDir:       trackerDir,
					Dest:             dest,
					RemoveTrackerDir: !trackerDirExisted,
				}).Run(); cleanupErr != nil {
					s.logger.Warnf("clients: %s cleanup failed after staged link error target=%s: %v", clientName, linkTarget, cleanupErr)
				}
			}
			lastErr = fmt.Errorf("clients: %s %s target %s: %w", clientName, mode, linkTarget, linkErr)
			continue
		}

		var cleanup *linkStagingCleanup
		if !destExisted {
			cleanup = &linkStagingCleanup{
				TrackerDir:       trackerDir,
				Dest:             dest,
				RemoveTrackerDir: !trackerDirExisted,
			}
		}
		result := linkStagingResult{
			SavePath:        qbitSavePath(savePath),
			Linked:          true,
			LayoutValidated: true,
			FileCount:       len(torrentPlan.files),
			Cleanup:         cleanup,
		}
		s.logger.Infof("clients: linked torrent staging ready client=%s tracker=%s mode=%s files=%d layout_validated=%t path_mapping=%s save_path=%s", clientName, tracker, mode, result.FileCount, result.LayoutValidated, mappingState, result.SavePath)
		return result, nil
	}

	if mappingRequired && !mappingCandidateFound {
		s.logger.Warnf("clients: linked torrent path mapping blocked client=%s tracker=%s mode=%s decision=abort reason=no-local-root-match", clientName, tracker, mode)
		return linkStagingResult{}, lastErr
	}
	if client.FallbackAllowed() {
		s.logger.Warnf("clients: %s %s failed for %s, falling back to original qbit path: %v", clientName, mode, source, lastErr)
		return linkStagingResult{HashCheckRequired: true}, nil
	}
	return linkStagingResult{}, lastErr
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

// sourcePathForLinking returns an existing local source path suitable for link
// creation. It stats candidate paths because link staging must copy or link real
// filesystem content.
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

// sourcePathForQbitSavePath returns the prepared source path used for qbit
// savepath mapping. It is intentionally lexical and does not stat the source,
// because qbit injection can still be valid when source metadata is unavailable.
func sourcePathForQbitSavePath(meta api.PreparedMetadata) (string, error) {
	if len(meta.FileList) == 1 {
		if candidate := strings.TrimSpace(meta.FileList[0]); candidate != "" {
			return absLocalPath("qbit mapping source", candidate)
		}
	}
	source := strings.TrimSpace(meta.SourcePath)
	if source == "" {
		return "", internalerrors.ErrInvalidInput
	}
	return absLocalPath("qbit mapping source", source)
}

func linkedFolderCandidates[S ~[]string](source string, folders S, mode string) ([]string, error) {
	candidates := nonEmptyClientPaths(folders)
	if len(candidates) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}

	sourceAbs, err := absLocalPath("linking source", source)
	if err != nil {
		return nil, err
	}
	sourceVolume := filepath.VolumeName(sourceAbs)
	absCandidates := make([]string, 0, len(candidates))
	sameVolume := make([]string, 0, len(candidates))
	for _, folder := range candidates {
		folderAbs, err := absLocalPath("linked folder", folder)
		if err != nil {
			continue
		}
		absCandidates = append(absCandidates, folderAbs)
		if strings.EqualFold(filepath.VolumeName(folderAbs), sourceVolume) {
			sameVolume = append(sameVolume, folderAbs)
		}
	}
	if len(absCandidates) == 0 {
		return nil, internalerrors.ErrInvalidInput
	}
	if mode == "hardlink" || mode == "reflink" {
		if runtime.GOOS == "windows" {
			if len(sameVolume) == 0 {
				return nil, internalerrors.ErrNotFound
			}
			return sameVolume, nil
		}
		return absCandidates, nil
	}
	if runtime.GOOS == "windows" && len(sameVolume) > 0 {
		ordered := make([]string, 0, len(absCandidates))
		seen := make(map[string]struct{}, len(absCandidates))
		for _, folder := range sameVolume {
			key := strings.ToLower(folder)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			ordered = append(ordered, folder)
		}
		for _, folder := range absCandidates {
			key := strings.ToLower(folder)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			ordered = append(ordered, folder)
		}
		return ordered, nil
	}
	return absCandidates, nil
}

func sanitizeTrackerLinkDirName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", internalerrors.ErrInvalidInput
	}
	if filepath.IsAbs(trimmed) || filepath.VolumeName(trimmed) != "" {
		return "", internalerrors.ErrInvalidInput
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || filepath.Base(cleaned) != cleaned {
		return "", internalerrors.ErrInvalidInput
	}
	return cleaned, nil
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
	if destInfo, err := os.Lstat(dest); err == nil {
		matches, err := existingLinkDestinationMatchesSource(source, dest, sourceInfo, destInfo, mode)
		if err != nil {
			return err
		}
		if matches {
			return nil
		}
		return fmt.Errorf("existing link destination does not match source: %w", internalerrors.ErrInvalidInput)
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

func existingLinkDestinationMatchesSource(source string, dest string, sourceInfo os.FileInfo, destInfo os.FileInfo, mode string) (bool, error) {
	if mode == "symlink" {
		return existingSymlinkMatchesSource(source, dest, destInfo)
	}
	if destInfo.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	if sourceInfo.IsDir() != destInfo.IsDir() {
		return false, nil
	}
	if !sourceInfo.IsDir() {
		return existingLinkedFileMatchesSource(source, dest, sourceInfo, destInfo, mode)
	}
	return existingLinkedDirectoryMatchesSource(source, dest, mode)
}

func existingSymlinkMatchesSource(source string, dest string, destInfo os.FileInfo) (bool, error) {
	if destInfo.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}
	target, err := os.Readlink(dest)
	if err != nil {
		return false, fmt.Errorf("read existing symlink destination: %w", err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dest), target)
	}
	sourceAbs, err := absLocalPath("linking source", source)
	if err != nil {
		return false, err
	}
	targetAbs, err := absLocalPath("existing symlink target", target)
	if err != nil {
		return false, err
	}
	return pathutil.SamePath(sourceAbs, targetAbs), nil
}

func existingLinkedFileMatchesSource(source string, dest string, sourceInfo os.FileInfo, destInfo os.FileInfo, mode string) (bool, error) {
	if !sourceInfo.Mode().IsRegular() || !destInfo.Mode().IsRegular() {
		return false, nil
	}
	if mode == "hardlink" {
		return os.SameFile(sourceInfo, destInfo), nil
	}
	if sourceInfo.Size() != destInfo.Size() {
		return false, nil
	}
	return regularFilesEqual(source, dest)
}

func existingLinkedDirectoryMatchesSource(source string, dest string, mode string) (bool, error) {
	if err := filepath.WalkDir(source, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk existing link source: %w", walkErr)
		}
		rel, err := filepath.Rel(source, sourcePath)
		if err != nil {
			return fmt.Errorf("relative existing link source path: %w", err)
		}
		destPath := filepath.Join(dest, rel)
		if !pathutil.IsWithinRoot(dest, destPath) {
			return fmt.Errorf("existing link destination escapes target: %w", internalerrors.ErrInvalidInput)
		}
		destInfo, err := os.Lstat(destPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fs.ErrNotExist
			}
			return fmt.Errorf("stat existing link destination entry: %w", err)
		}
		if entry.IsDir() {
			if !destInfo.IsDir() {
				return fs.ErrInvalid
			}
			return nil
		}
		sourceInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect existing link source entry: %w", err)
		}
		if !sourceInfo.Mode().IsRegular() {
			return nil
		}
		matches, err := existingLinkedFileMatchesSource(sourcePath, destPath, sourceInfo, destInfo, mode)
		if err != nil {
			return err
		}
		if !matches {
			return fs.ErrInvalid
		}
		return nil
	}); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid) {
			return false, nil
		}
		return false, fmt.Errorf("walk existing link source tree: %w", err)
	}
	if err := filepath.WalkDir(dest, func(destPath string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk existing link destination: %w", walkErr)
		}
		rel, err := filepath.Rel(dest, destPath)
		if err != nil {
			return fmt.Errorf("relative existing link destination path: %w", err)
		}
		sourcePath := filepath.Join(source, rel)
		if !pathutil.IsWithinRoot(source, sourcePath) {
			return fmt.Errorf("existing link source escapes root: %w", internalerrors.ErrInvalidInput)
		}
		if _, err := os.Lstat(sourcePath); err != nil {
			if os.IsNotExist(err) {
				return fs.ErrNotExist
			}
			return fmt.Errorf("stat existing link source entry: %w", err)
		}
		return nil
	}); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("walk existing link destination tree: %w", err)
	}
	return true, nil
}

func regularFilesEqual(source string, dest string) (bool, error) {
	sourceFile, err := os.Open(source)
	if err != nil {
		return false, fmt.Errorf("open source file for existing link validation: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Open(dest)
	if err != nil {
		return false, fmt.Errorf("open destination file for existing link validation: %w", err)
	}
	defer destFile.Close()

	sourceBuf := make([]byte, 32*1024)
	destBuf := make([]byte, 32*1024)
	for {
		sourceN, sourceErr := sourceFile.Read(sourceBuf)
		destN, destErr := destFile.Read(destBuf)
		if sourceN != destN || !bytes.Equal(sourceBuf[:sourceN], destBuf[:destN]) {
			return false, nil
		}
		if errors.Is(sourceErr, io.EOF) && errors.Is(destErr, io.EOF) {
			return true, nil
		}
		if sourceErr != nil && !errors.Is(sourceErr, io.EOF) {
			return false, fmt.Errorf("read source file for existing link validation: %w", sourceErr)
		}
		if destErr != nil && !errors.Is(destErr, io.EOF) {
			return false, fmt.Errorf("read destination file for existing link validation: %w", destErr)
		}
		if errors.Is(sourceErr, io.EOF) != errors.Is(destErr, io.EOF) {
			return false, nil
		}
	}
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

// mappedRemotePath reports whether value is under a usable configured local
// root and, when it is, returns the most-specific paired remote root plus
// relative suffix. Blank entries are ignored without shifting later pairs.
func mappedRemotePath[S ~[]string](value string, localPaths S, remotePaths S) (string, bool) {
	savePath := strings.TrimSpace(value)
	if savePath == "" {
		return "", false
	}
	bestSpecificity := -1
	bestMapped := ""
	for _, pair := range pathMappingPairs(localPaths, remotePaths) {
		rel, ok := relativePathUnderRoot(pair.local, savePath)
		if !ok {
			continue
		}
		localRoot, err := filepath.Abs(filepath.Clean(pair.local))
		if err != nil || len(localRoot) <= bestSpecificity {
			continue
		}
		bestSpecificity = len(localRoot)
		if rel == "." {
			bestMapped = pair.remote
			continue
		}
		bestMapped = filepath.Join(pair.remote, rel)
	}
	return bestMapped, bestSpecificity >= 0
}

// mappedQbitSavePathForSource maps the prepared source path to the qBittorrent
// save path parent that can contain the torrent's top-level content. It returns
// mapped=false without touching source metadata when no usable path pairs exist
// or no configured pair matches the lexical source path.
func mappedQbitSavePathForSource[S ~[]string](meta api.PreparedMetadata, localPaths S, remotePaths S) (string, bool, error) {
	if len(pathMappingPairs(localPaths, remotePaths)) == 0 {
		return "", false, nil
	}
	source, err := sourcePathForQbitSavePath(meta)
	if err != nil {
		return "", false, err
	}
	mappedSource, ok := mappedRemotePath(source, localPaths, remotePaths)
	if !ok {
		return "", false, nil
	}
	return qbitSavePath(filepath.Dir(mappedSource)), true, nil
}

// pathMappingPair is one trimmed local_path/remote_path mapping that kept its
// original positional relationship from the client configuration.
type pathMappingPair struct {
	local  string
	remote string
}

// pathMappingPairs trims configured path entries while preserving positional
// local_path/remote_path pairing; entries with either side blank are ignored.
func pathMappingPairs[S ~[]string](localPaths S, remotePaths S) []pathMappingPair {
	limit := min(len(remotePaths), len(localPaths))
	pairs := make([]pathMappingPair, 0, limit)
	for idx := range limit {
		localPath := strings.TrimSpace(localPaths[idx])
		remotePath := strings.TrimSpace(remotePaths[idx])
		if localPath == "" || remotePath == "" {
			continue
		}
		pairs = append(pairs, pathMappingPair{local: localPath, remote: remotePath})
	}
	return pairs
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

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat path: %w", err)
	}
	return false, nil
}

func absLocalPath(label string, value string) (string, error) {
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("%s absolute path: %w", label, err)
	}
	return abs, nil
}
