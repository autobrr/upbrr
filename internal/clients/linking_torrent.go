// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

// torrentLinkPlan describes the qBittorrent-visible tree derived from the final
// torrent artifact, excluding padding files that require no source content.
type torrentLinkPlan struct {
	root           string
	files          []torrentLinkFile
	paddingFiles   int
	torrentIsMulti bool
}

// torrentLinkFile maps one non-padding metainfo file to its local source and
// exact path beneath qBittorrent's configured save directory.
type torrentLinkFile struct {
	sourcePath string
	destRel    string
	length     int64
	match      string
}

// sourceLinkCandidate tracks one regular source file while a torrent layout is
// matched. A candidate can satisfy at most one metainfo file.
type sourceLinkCandidate struct {
	path string
	rel  string
	name string
	size int64
	used bool
}

// buildTorrentLinkPlan parses the final torrent artifact and maps every
// non-padding metainfo file to one unique regular source file. Destination
// paths preserve the torrent root and internal file layout. It checks ctx
// around metainfo processing and during source discovery and matching, returning
// an error that wraps the context error when canceled.
func buildTorrentLinkPlan(ctx context.Context, torrentPath string, meta api.PreparedMetadata) (torrentLinkPlan, error) {
	if err := torrentLinkPlanContextError(ctx); err != nil {
		return torrentLinkPlan{}, err
	}
	torrentMeta, err := metainfo.LoadFromFile(torrentPath)
	if err := torrentLinkPlanContextError(ctx); err != nil {
		return torrentLinkPlan{}, err
	}
	if err != nil {
		return torrentLinkPlan{}, fmt.Errorf("load injected torrent metainfo: %w", err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err := torrentLinkPlanContextError(ctx); err != nil {
		return torrentLinkPlan{}, err
	}
	if err != nil {
		return torrentLinkPlan{}, fmt.Errorf("decode injected torrent info: %w", err)
	}
	root, err := safeTorrentLinkComponent(info.BestName())
	if err != nil {
		return torrentLinkPlan{}, fmt.Errorf("injected torrent root: %w", err)
	}
	candidates, err := sourceLinkCandidates(ctx, meta)
	if err != nil {
		return torrentLinkPlan{}, err
	}
	if len(candidates) == 0 {
		return torrentLinkPlan{}, errors.New("no regular source files available for injected torrent")
	}

	plan := torrentLinkPlan{root: root, torrentIsMulti: info.IsDir()}
	for _, torrentFile := range info.UpvertedFiles() {
		if err := torrentLinkPlanContextError(ctx); err != nil {
			return torrentLinkPlan{}, err
		}
		if strings.Contains(torrentFile.Attr, "p") {
			plan.paddingFiles++
			continue
		}
		torrentRel := root
		destRel := root
		if plan.torrentIsMulti {
			components, err := safeTorrentLinkPath(torrentFile.BestPath())
			if err != nil {
				return torrentLinkPlan{}, fmt.Errorf("injected torrent file path: %w", err)
			}
			torrentRel = filepath.Join(components...)
			destRel = filepath.Join(append([]string{root}, components...)...)
		}
		candidate, match, err := matchSourceLinkCandidate(ctx, candidates, torrentRel, torrentFile.Length)
		if err != nil {
			return torrentLinkPlan{}, fmt.Errorf("map injected torrent file %q: %w", filepath.ToSlash(torrentRel), err)
		}
		plan.files = append(plan.files, torrentLinkFile{
			sourcePath: candidate.path,
			destRel:    destRel,
			length:     torrentFile.Length,
			match:      match,
		})
	}
	if len(plan.files) == 0 {
		return torrentLinkPlan{}, errors.New("injected torrent has no non-padding files")
	}
	if err := torrentLinkPlanContextError(ctx); err != nil {
		return torrentLinkPlan{}, err
	}
	return plan, nil
}

// sourceLinkCandidates enumerates regular files below the prepared source path.
// Single-file sources produce one candidate; directory sources are walked
// recursively. It checks ctx around filesystem operations and on every walk
// callback, returning a wrapped context error when canceled.
func sourceLinkCandidates(ctx context.Context, meta api.PreparedMetadata) ([]sourceLinkCandidate, error) {
	if err := torrentLinkPlanContextError(ctx); err != nil {
		return nil, err
	}
	source := strings.TrimSpace(meta.SourcePath)
	if source == "" {
		return nil, errors.New("source path is required for torrent link staging")
	}
	sourceAbs, err := absLocalPath("torrent link source", source)
	if err != nil {
		return nil, err
	}
	if err := torrentLinkPlanContextError(ctx); err != nil {
		return nil, err
	}
	sourceInfo, err := os.Stat(sourceAbs)
	if err := torrentLinkPlanContextError(ctx); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("stat torrent link source: %w", err)
	}

	candidates := make([]sourceLinkCandidate, 0, max(1, len(meta.FileList)))
	seen := make(map[string]struct{})
	add := func(path string, rel string) error {
		if err := torrentLinkPlanContextError(ctx); err != nil {
			return err
		}
		pathAbs, err := absLocalPath("torrent link candidate", path)
		if err != nil {
			return err
		}
		info, err := os.Stat(pathAbs)
		if err := torrentLinkPlanContextError(ctx); err != nil {
			return err
		}
		if err != nil {
			return fmt.Errorf("stat torrent link candidate: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		key := pathAbs
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		candidates = append(candidates, sourceLinkCandidate{
			path: pathAbs,
			rel:  filepath.Clean(rel),
			name: filepath.Base(pathAbs),
			size: info.Size(),
		})
		return nil
	}

	if !sourceInfo.IsDir() {
		if err := add(sourceAbs, filepath.Base(sourceAbs)); err != nil {
			return nil, err
		}
		return candidates, nil
	}

	if err := filepath.WalkDir(sourceAbs, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := torrentLinkPlanContextError(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			return fmt.Errorf("walk torrent link source: %w", walkErr)
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sourceAbs, path)
		if err != nil {
			return fmt.Errorf("torrent link source relative path: %w", err)
		}
		return add(path, rel)
	}); err != nil {
		return nil, fmt.Errorf("walk torrent link candidates: %w", err)
	}
	if err := torrentLinkPlanContextError(ctx); err != nil {
		return nil, err
	}
	return candidates, nil
}

// matchSourceLinkCandidate selects one unused file by path and size, then name
// and size, then unique size. Ambiguous or absent matches return an error rather
// than risk staging the wrong content. Path and name matching follows host case
// semantics, and size-only matching cannot revive a rejected case-only match.
// It stops scanning and returns a wrapped context error when ctx is canceled.
func matchSourceLinkCandidate(ctx context.Context, candidates []sourceLinkCandidate, torrentRel string, length int64) (*sourceLinkCandidate, string, error) {
	return matchSourceLinkCandidateWithCaseFold(ctx, candidates, torrentRel, length, runtime.GOOS == "windows")
}

// matchSourceLinkCandidateWithCaseFold applies the matcher with explicit case
// semantics so host-independent tests can prove Windows and non-Windows rules.
func matchSourceLinkCandidateWithCaseFold(ctx context.Context, candidates []sourceLinkCandidate, torrentRel string, length int64, foldCase bool) (*sourceLinkCandidate, string, error) {
	torrentRel = filepath.Clean(torrentRel)
	torrentName := filepath.Base(torrentRel)
	checks := []struct {
		name  string
		match func(sourceLinkCandidate) bool
	}{
		{
			name: "path_size",
			match: func(candidate sourceLinkCandidate) bool {
				return candidate.size == length && sourceLinkPathEqual(filepath.Clean(candidate.rel), torrentRel, foldCase)
			},
		},
		{
			name: "name_size",
			match: func(candidate sourceLinkCandidate) bool {
				return candidate.size == length && sourceLinkPathEqual(candidate.name, torrentName, foldCase)
			},
		},
		{
			name: "unique_size",
			match: func(candidate sourceLinkCandidate) bool {
				return candidate.size == length && !sourceLinkCaseOnlyMismatch(candidate, torrentRel, torrentName, foldCase)
			},
		},
	}
	for _, check := range checks {
		if err := torrentLinkPlanContextError(ctx); err != nil {
			return nil, "", err
		}
		matched := -1
		for index := range candidates {
			if err := torrentLinkPlanContextError(ctx); err != nil {
				return nil, "", err
			}
			if candidates[index].used || !check.match(candidates[index]) {
				continue
			}
			if matched != -1 {
				matched = -2
				break
			}
			matched = index
		}
		if matched >= 0 {
			candidates[matched].used = true
			return &candidates[matched], check.name, nil
		}
		if matched == -2 {
			continue
		}
	}
	return nil, "", fmt.Errorf("no unique source match with length=%d", length)
}

// sourceLinkPathEqual compares a candidate path or name using the selected host
// case semantics.
func sourceLinkPathEqual(left string, right string, foldCase bool) bool {
	if foldCase {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// sourceLinkCaseOnlyMismatch reports whether a candidate differs only by case.
// Such candidates cannot bypass case-sensitive rejection via unique-size match.
func sourceLinkCaseOnlyMismatch(candidate sourceLinkCandidate, torrentRel string, torrentName string, foldCase bool) bool {
	if foldCase {
		return false
	}
	candidateRel := filepath.Clean(candidate.rel)
	relMismatch := candidateRel != torrentRel && strings.EqualFold(candidateRel, torrentRel)
	nameMismatch := candidate.name != torrentName && strings.EqualFold(candidate.name, torrentName)
	return relMismatch || nameMismatch
}

// torrentLinkPlanContextError returns nil while ctx is active or an error that
// wraps ctx.Err after cancellation.
func torrentLinkPlanContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("torrent link planning canceled: %w", err)
	}
	return nil
}

// safeTorrentLinkPath validates metainfo components before they become a local
// filesystem path. Empty, rooted, traversal, drive, and separator-bearing
// components are rejected.
func safeTorrentLinkPath(parts []string) ([]string, error) {
	if len(parts) == 0 {
		return nil, errors.New("empty torrent file path")
	}
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		component, err := safeTorrentLinkComponent(part)
		if err != nil {
			return nil, err
		}
		clean = append(clean, component)
	}
	return clean, nil
}

// safeTorrentLinkComponent validates one torrent name or path component for
// use beneath the guarded staging directory.
func safeTorrentLinkComponent(value string) (string, error) {
	if value == "" || value == "." || value == ".." || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || strings.ContainsAny(value, `/\`) {
		return "", fmt.Errorf("unsafe component %q", value)
	}
	return value, nil
}

// createTorrentLinkPlan materializes a metainfo-shaped staging tree and checks
// that every destination is a regular file with the declared length. On failure
// it removes links created by the current attempt while preserving pre-existing
// destinations. qBittorrent performs the subsequent piece hash check.
func createTorrentLinkPlan(ctx context.Context, trackerDir string, plan torrentLinkPlan, mode string) error {
	created := make([]string, 0, len(plan.files))
	for _, file := range plan.files {
		dest := filepath.Join(trackerDir, file.destRel)
		if !pathutil.IsWithinRoot(trackerDir, dest) {
			return rollbackTorrentLinkPlan(trackerDir, created, errors.New("torrent link destination escapes tracker directory"))
		}
		destExisted, err := pathExists(dest)
		if err != nil {
			return rollbackTorrentLinkPlan(trackerDir, created, fmt.Errorf("stat staged torrent file: %w", err))
		}
		if err := createLinkTree(ctx, file.sourcePath, dest, mode); err != nil {
			return rollbackTorrentLinkPlan(trackerDir, created, fmt.Errorf("create metainfo-shaped %s link: %w", mode, err))
		}
		if !destExisted {
			created = append(created, dest)
		}
		info, err := os.Stat(dest)
		if err != nil {
			return rollbackTorrentLinkPlan(trackerDir, created, fmt.Errorf("validate staged torrent file: %w", err))
		}
		if !info.Mode().IsRegular() || info.Size() != file.length {
			return rollbackTorrentLinkPlan(trackerDir, created, fmt.Errorf("staged torrent file size mismatch: expected=%d actual=%d", file.length, info.Size()))
		}
	}
	return nil
}

// rollbackTorrentLinkPlan removes created links in reverse order and combines
// cleanup failures with the original plan error.
func rollbackTorrentLinkPlan(trackerDir string, created []string, planErr error) error {
	var rollbackErrs []error
	for i := len(created) - 1; i >= 0; i-- {
		dest := created[i]
		if !pathutil.IsWithinRoot(trackerDir, dest) {
			rollbackErrs = append(rollbackErrs, errors.New("torrent link rollback destination escapes tracker directory"))
			continue
		}
		if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("remove staged torrent link: %w", err))
		}
	}
	if len(rollbackErrs) == 0 {
		return planErr
	}
	return fmt.Errorf("rollback failed: %w", errors.Join(append([]error{planErr}, rollbackErrs...)...))
}
