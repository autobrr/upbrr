// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/pkg/api"
)

// Serialize publication so concurrent conversions never replace identical
// artifacts while another operation is reading them.
var convertedTorrentPublicationMu sync.Mutex

// ConvertSingleFileWrapperInfo returns single-file info bytes when only a one-file
// folder wrapper differs from the source. A nil result means conversion is unsafe
// or unnecessary. It preserves pieces without reading media bytes or writing files.
func ConvertSingleFileWrapperInfo(infoBytes []byte, subject api.TorrentSubject) ([]byte, error) {
	inventory, ok, err := ResolveSubmissionContentInventory(subject)
	if err != nil || !ok || inventory.Scope != api.SubmissionContentScopeSingleFile || len(inventory.Files) != 1 {
		return nil, err
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	info, err := mi.UnmarshalInfo()
	if err != nil || len(info.Files) != 1 || ValidatePieceMetadata(info) != nil {
		return nil, nil
	}
	file := info.Files[0]
	parts := file.BestPath()
	if len(parts) == 0 || parts[len(parts)-1] != inventory.Files[0].TorrentPath || file.Length != inventory.Files[0].Size {
		return nil, nil
	}
	var dictionary map[string]any
	if err := bencode.Unmarshal(mi.InfoBytes, &dictionary); err != nil {
		return nil, nil
	}
	files, ok := dictionary["files"].([]any)
	if !ok || len(files) != 1 {
		return nil, nil
	}
	fileFields, ok := files[0].(map[string]any)
	if !ok {
		return nil, nil
	}
	for key := range fileFields {
		if key != "length" && key != "path" && key != "path.utf-8" {
			return nil, nil
		}
	}
	delete(dictionary, "files")
	dictionary["length"] = file.Length
	dictionary["name"] = inventory.Files[0].TorrentPath
	if _, ok := dictionary["name.utf-8"]; ok {
		dictionary["name.utf-8"] = inventory.Files[0].TorrentPath
	}
	converted, err := bencode.Marshal(dictionary)
	if err != nil {
		return nil, fmt.Errorf("torrent: encode single-file wrapper: %w", err)
	}
	return converted, nil
}

// convertSingleFileWrapper preserves a complete one-file piece sequence when
// only its folder wrapper differs from the requested single-file layout.
func convertSingleFileWrapper(tmpRoot, path string, subject api.TorrentSubject) (string, error) {
	if strings.TrimSpace(tmpRoot) == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil || validateClientCandidateBytes(path, data, subject) != nil {
		return "", nil
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return "", nil
	}
	converted, err := ConvertSingleFileWrapperInfo(mi.InfoBytes, subject)
	if err != nil || len(converted) == 0 {
		return "", err
	}
	mi.InfoBytes = converted
	var output bytes.Buffer
	if err := mi.Write(&output); err != nil {
		return "", fmt.Errorf("torrent: encode converted metainfo: %w", err)
	}
	base, err := TempTorrentPath(tmpRoot, subject.SourcePath)
	if err != nil {
		return "", err
	}
	path = filepath.Join(filepath.Dir(base), fmt.Sprintf("converted-%x.torrent", sha256.Sum256(output.Bytes())))
	convertedTorrentPublicationMu.Lock()
	defer convertedTorrentPublicationMu.Unlock()
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, output.Bytes()) {
		return path, nil
	}
	fileHandle, err := os.CreateTemp(filepath.Dir(path), ".converted-*.tmp")
	if err != nil {
		return "", fmt.Errorf("torrent: stage converted metainfo: %w", err)
	}
	defer func() { _ = os.Remove(fileHandle.Name()) }()
	if _, err := fileHandle.Write(output.Bytes()); err != nil {
		_ = fileHandle.Close()
		return "", fmt.Errorf("torrent: write converted metainfo: %w", err)
	}
	if err := fileHandle.Close(); err != nil {
		return "", fmt.Errorf("torrent: close converted metainfo: %w", err)
	}
	if err := os.Rename(fileHandle.Name(), path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, output.Bytes()) {
			return path, nil
		}
		return "", fmt.Errorf("torrent: publish converted metainfo: %w", err)
	}
	return path, nil
}
