// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// SubmissionContentFile is one local file selected by the exact torrent
// creation contract. LocalPath remains private to the operation; TorrentPath
// is the normalized metainfo-relative layout path.
type SubmissionContentFile struct {
	LocalPath   string
	TorrentPath string
	Size        int64
}

// SubmissionContentInventory is the canonical local file selection consumed by
// torrent content validation and submitted-content identity construction.
type SubmissionContentInventory struct {
	Scope api.SubmissionContentScope
	Files []SubmissionContentFile
}

// ResolveSubmissionContentInventory resolves the same effective file inventory
// used to validate torrent content. A false result means the TorrentSubject
// names only a .torrent artifact and has no local submitted-content scope.
func ResolveSubmissionContentInventory(meta api.TorrentSubject) (SubmissionContentInventory, bool, error) {
	source := strings.TrimSpace(meta.SourcePath)
	if source == "" || strings.EqualFold(filepath.Ext(source), ".torrent") {
		return SubmissionContentInventory{}, false, nil
	}
	if strings.TrimSpace(meta.DiscType) != "" {
		inventory, err := submissionContentInventoryFromDisk(normalizeDiscSource(source), api.SubmissionContentScopeFullDisc)
		return inventory, err == nil, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return SubmissionContentInventory{}, false, fmt.Errorf("torrent: stat source %q: %w", source, err)
	}
	if !info.IsDir() {
		return SubmissionContentInventory{
			Scope: api.SubmissionContentScopeSingleFile,
			Files: []SubmissionContentFile{{
				LocalPath:   filepath.Clean(source),
				TorrentPath: filepath.Base(source),
				Size:        info.Size(),
			}},
		}, true, nil
	}
	if len(meta.FileList) == 0 {
		inventory, err := submissionContentInventoryFromDisk(source, api.SubmissionContentScopeFileSet)
		return inventory, err == nil, err
	}
	wanted, err := wantedFilesWithin(source, meta.FileList)
	if err != nil {
		return SubmissionContentInventory{}, false, err
	}
	if len(wanted) == 0 {
		return SubmissionContentInventory{}, false, errors.New("torrent: no valid wanted files")
	}
	if len(wanted) == 1 {
		info, err := os.Stat(wanted[0])
		if err != nil {
			return SubmissionContentInventory{}, false, fmt.Errorf("torrent: stat wanted file %q: %w", wanted[0], err)
		}
		return SubmissionContentInventory{
			Scope: api.SubmissionContentScopeSingleFile,
			Files: []SubmissionContentFile{{
				LocalPath:   wanted[0],
				TorrentPath: filepath.Base(wanted[0]),
				Size:        info.Size(),
			}},
		}, true, nil
	}
	root, err := filepath.Abs(source)
	if err != nil {
		return SubmissionContentInventory{}, false, fmt.Errorf("torrent: resolve source root: %w", err)
	}
	files := make([]SubmissionContentFile, 0, len(wanted))
	for _, file := range wanted {
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return SubmissionContentInventory{}, false, fmt.Errorf("torrent: wanted file relative path: %w", err)
		}
		info, err := os.Stat(file)
		if err != nil {
			return SubmissionContentInventory{}, false, fmt.Errorf("torrent: stat wanted file %q: %w", file, err)
		}
		files = append(files, SubmissionContentFile{
			LocalPath:   file,
			TorrentPath: filepath.ToSlash(relative),
			Size:        info.Size(),
		})
	}
	return SubmissionContentInventory{Scope: api.SubmissionContentScopeFileSet, Files: files}, true, nil
}

// SubmissionContentIdentity derives a path-free submitted-inventory identity
// from the exact selected inventory and fresh bounded source samples.
func SubmissionContentIdentity(
	inventory SubmissionContentInventory,
	verified api.SourceContentIdentity,
) (api.SubmissionContentIdentity, error) {
	if verified.Version != api.SourceContentIdentityVersion || strings.TrimSpace(verified.Digest) == "" {
		return api.SubmissionContentIdentity{}, errors.New("torrent: sampled source content identity is required")
	}
	if len(inventory.Files) == 0 {
		return api.SubmissionContentIdentity{}, errors.New("torrent: submitted content inventory is empty")
	}
	files := make([]api.SubmissionContentFile, 0, len(inventory.Files))
	for index, file := range inventory.Files {
		if strings.TrimSpace(file.LocalPath) == "" || file.Size < 0 {
			return api.SubmissionContentIdentity{}, fmt.Errorf("torrent: submitted content inventory file %d is invalid", index)
		}
		for previous := range inventory.Files[:index] {
			if pathutil.SamePath(inventory.Files[previous].LocalPath, file.LocalPath) {
				return api.SubmissionContentIdentity{}, fmt.Errorf("torrent: submitted content inventory aliases %q", file.LocalPath)
			}
		}
		matched, err := verifiedSourceFile(file, verified.Files)
		if err != nil {
			return api.SubmissionContentIdentity{}, err
		}
		files = append(files, api.SubmissionContentFile{
			RelativePath: file.TorrentPath,
			Size:         file.Size,
			SHA256:       matched.SHA256,
		})
	}
	identity, err := api.NewSubmissionContentIdentity(inventory.Scope, files)
	if err != nil {
		return api.SubmissionContentIdentity{}, fmt.Errorf("torrent: derive submitted sample identity: %w", err)
	}
	return identity, nil
}

func submissionContentInventoryFromDisk(
	root string,
	scope api.SubmissionContentScope,
) (SubmissionContentInventory, error) {
	entries, err := diskContentFiles(root)
	if err != nil {
		return SubmissionContentInventory{}, err
	}
	if len(entries) == 0 {
		return SubmissionContentInventory{}, errors.New("torrent: submitted content inventory is empty")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return SubmissionContentInventory{}, fmt.Errorf("torrent: resolve submitted content root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return SubmissionContentInventory{}, fmt.Errorf("torrent: stat submitted content root %q: %w", root, err)
	}
	files := make([]SubmissionContentFile, 0, len(entries))
	for _, entry := range entries {
		localPath := root
		if info.IsDir() {
			localPath = filepath.Join(root, filepath.FromSlash(entry.path))
		}
		files = append(files, SubmissionContentFile{
			LocalPath:   localPath,
			TorrentPath: entry.path,
			Size:        entry.length,
		})
	}
	return SubmissionContentInventory{Scope: scope, Files: files}, nil
}

func verifiedSourceFile(file SubmissionContentFile, verified []api.VerifiedSourceFile) (api.VerifiedSourceFile, error) {
	var matched api.VerifiedSourceFile
	for _, candidate := range verified {
		if !pathutil.SamePath(file.LocalPath, candidate.LocalPath) {
			continue
		}
		if matched.LocalPath != "" {
			return api.VerifiedSourceFile{}, fmt.Errorf("torrent: sampled source identity aliases %q", file.LocalPath)
		}
		matched = candidate
	}
	if matched.LocalPath == "" {
		return api.VerifiedSourceFile{}, fmt.Errorf("torrent: submitted file %q is missing fresh sample evidence", file.LocalPath)
	}
	if matched.Size != file.Size {
		return api.VerifiedSourceFile{}, fmt.Errorf("torrent: submitted file %q differs from fresh sample evidence", file.LocalPath)
	}
	return matched, nil
}
