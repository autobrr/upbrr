// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func ResolveTrackerTorrentArtifactPath(meta api.PreparedMetadata, dbPath string, tracker string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: tracker torrent path requires db path and source path")
	}

	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}

	name := strings.ToLower(strings.TrimSpace(tracker))
	name = strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(name)
	if name == "" {
		name = "tracker"
	}
	return filepath.Join(tmpDir, base+"."+name+".torrent"), nil
}

func ResolveUploadTorrentPath(meta api.PreparedMetadata, dbPath string) (string, error) {
	cleanPath, cleanPathOK := uploadTorrentCleanPath(meta, dbPath)
	candidates := []string{
		strings.TrimSpace(meta.TorrentPath),
		strings.TrimSpace(meta.ClientTorrentPath),
		strings.TrimSpace(meta.SourcePath),
	}
	for _, candidate := range candidates {
		if candidate == "" || !strings.EqualFold(filepath.Ext(candidate), ".torrent") {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if cleanPathOK {
				err := WriteUploadTorrent(candidate, cleanPath)
				if err == nil {
					return cleanPath, nil
				}
				if !isUploadTorrentLoadError(err) {
					return "", err
				}
			}
			return candidate, nil
		}
	}

	if strings.TrimSpace(dbPath) != "" && strings.TrimSpace(meta.SourcePath) != "" {
		tmpRoot, err := db.Subdir(dbPath, "tmp")
		if err == nil {
			tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
			if err == nil {
				guessed := filepath.Join(tmpDir, base+".torrent")
				if info, err := os.Stat(guessed); err == nil && !info.IsDir() {
					if err := WriteUploadTorrent(guessed, guessed); err != nil && !isUploadTorrentLoadError(err) {
						return "", err
					}
					return guessed, nil
				}
			}
		}
	}

	return "", errors.New("trackers: torrent file not found")
}

func uploadTorrentCleanPath(meta api.PreparedMetadata, dbPath string) (string, bool) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", false
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", false
	}
	tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", false
	}
	return filepath.Join(tmpDir, base+".torrent"), true
}

func isUploadTorrentLoadError(err error) bool {
	return errors.Is(err, errInvalidUploadTorrent)
}

var errInvalidUploadTorrent = errors.New("invalid upload torrent")

func WriteUploadTorrent(sourcePath string, outputPath string) error {
	torrentMeta, err := metainfo.LoadFromFile(sourcePath)
	if err != nil {
		return fmt.Errorf("trackers: load upload torrent: %w: %w", errInvalidUploadTorrent, err)
	}
	cleanTorrentMeta(torrentMeta)
	return writeTorrentMeta(*torrentMeta, outputPath, "upload torrent")
}

func WritePersonalizedTorrent(sourcePath string, outputPath string, announceURL string, comment string, source string) error {
	torrentMeta, err := metainfo.LoadFromFile(sourcePath)
	if err != nil {
		return fmt.Errorf("trackers: load torrent artifact: %w", err)
	}
	cleanTorrentMeta(torrentMeta)

	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("trackers: unmarshal torrent artifact info: %w", err)
	}
	info.Source = source
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return fmt.Errorf("trackers: marshal torrent artifact info: %w", err)
	}
	torrentMeta.InfoBytes = infoBytes

	if trimmedAnnounce := strings.TrimSpace(announceURL); trimmedAnnounce != "" {
		torrentMeta.Announce = trimmedAnnounce
		torrentMeta.AnnounceList = metainfo.AnnounceList{{trimmedAnnounce}}
	}
	torrentMeta.Comment = "upbrr"
	if trimmedComment := strings.TrimSpace(comment); trimmedComment != "" {
		torrentMeta.Comment = trimmedComment
	}

	return writeTorrentMeta(*torrentMeta, outputPath, "torrent artifact")
}

func cleanTorrentMeta(torrentMeta *metainfo.MetaInfo) {
	torrentMeta.Announce = ""
	torrentMeta.AnnounceList = nil
	torrentMeta.Nodes = nil
	torrentMeta.PieceLayers = nil
	torrentMeta.UrlList = nil
	torrentMeta.Comment = "upbrr"
	if strings.Contains(strings.ToLower(torrentMeta.CreatedBy), "upload assistant") {
		torrentMeta.CreatedBy = "upbrr"
	}
}

func writeTorrentMeta(torrentMeta metainfo.MetaInfo, outputPath string, context string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return fmt.Errorf("trackers: create %s dir: %w", context, err)
	}
	file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("trackers: create %s: %w", context, err)
	}
	defer file.Close()

	if err := torrentMeta.Write(file); err != nil {
		return fmt.Errorf("trackers: write %s: %w", context, err)
	}
	return nil
}
