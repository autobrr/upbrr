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
// paths preserve the torrent root and internal file layout.
func buildTorrentLinkPlan(torrentPath string, meta api.PreparedMetadata) (torrentLinkPlan, error) {
	torrentMeta, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		return torrentLinkPlan{}, fmt.Errorf("load injected torrent metainfo: %w", err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		return torrentLinkPlan{}, fmt.Errorf("decode injected torrent info: %w", err)
	}
	root, err := safeTorrentLinkComponent(info.BestName())
	if err != nil {
		return torrentLinkPlan{}, fmt.Errorf("injected torrent root: %w", err)
	}
	candidates, err := sourceLinkCandidates(meta)
	if err != nil {
		return torrentLinkPlan{}, err
	}
	if len(candidates) == 0 {
		return torrentLinkPlan{}, errors.New("no regular source files available for injected torrent")
	}

	plan := torrentLinkPlan{root: root, torrentIsMulti: info.IsDir()}
	for _, torrentFile := range info.UpvertedFiles() {
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
		candidate, match, err := matchSourceLinkCandidate(candidates, torrentRel, torrentFile.Length)
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
	return plan, nil
}

// sourceLinkCandidates enumerates regular files below the prepared source path.
// Single-file sources produce one candidate; directory sources are walked recursively.
func sourceLinkCandidates(meta api.PreparedMetadata) ([]sourceLinkCandidate, error) {
	source := strings.TrimSpace(meta.SourcePath)
	if source == "" {
		return nil, errors.New("source path is required for torrent link staging")
	}
	sourceAbs, err := absLocalPath("torrent link source", source)
	if err != nil {
		return nil, err
	}
	sourceInfo, err := os.Stat(sourceAbs)
	if err != nil {
		return nil, fmt.Errorf("stat torrent link source: %w", err)
	}

	candidates := make([]sourceLinkCandidate, 0, max(1, len(meta.FileList)))
	seen := make(map[string]struct{})
	add := func(path string, rel string) error {
		pathAbs, err := absLocalPath("torrent link candidate", path)
		if err != nil {
			return err
		}
		info, err := os.Stat(pathAbs)
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
	return candidates, nil
}

// matchSourceLinkCandidate selects one unused file by path and size, then name
// and size, then unique size. Ambiguous or absent matches return an error rather
// than risk staging the wrong content.
func matchSourceLinkCandidate(candidates []sourceLinkCandidate, torrentRel string, length int64) (*sourceLinkCandidate, string, error) {
	checks := []struct {
		name  string
		match func(sourceLinkCandidate) bool
	}{
		{
			name: "path_size",
			match: func(candidate sourceLinkCandidate) bool {
				return candidate.size == length && strings.EqualFold(filepath.Clean(candidate.rel), filepath.Clean(torrentRel))
			},
		},
		{
			name: "name_size",
			match: func(candidate sourceLinkCandidate) bool {
				return candidate.size == length && strings.EqualFold(candidate.name, filepath.Base(torrentRel))
			},
		},
		{
			name: "unique_size",
			match: func(candidate sourceLinkCandidate) bool {
				return candidate.size == length
			},
		},
	}
	for _, check := range checks {
		matched := -1
		for index := range candidates {
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
// that every destination is a regular file with the declared length. qBittorrent
// performs the subsequent piece hash check during injection.
func createTorrentLinkPlan(ctx context.Context, trackerDir string, plan torrentLinkPlan, mode string) error {
	for _, file := range plan.files {
		dest := filepath.Join(trackerDir, file.destRel)
		if !pathutil.IsWithinRoot(trackerDir, dest) {
			return errors.New("torrent link destination escapes tracker directory")
		}
		if err := createLinkTree(ctx, file.sourcePath, dest, mode); err != nil {
			return fmt.Errorf("create metainfo-shaped %s link: %w", mode, err)
		}
		info, err := os.Stat(dest)
		if err != nil {
			return fmt.Errorf("validate staged torrent file: %w", err)
		}
		if !info.Mode().IsRegular() || info.Size() != file.length {
			return fmt.Errorf("staged torrent file size mismatch: expected=%d actual=%d", file.length, info.Size())
		}
	}
	return nil
}
