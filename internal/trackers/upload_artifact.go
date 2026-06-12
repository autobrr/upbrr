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

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/torrentmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

type trackerUploadTorrentSpec struct {
	source          string
	defaultAnnounce string
	useMyAnnounce   bool
}

var trackerUploadTorrentSpecs = map[string]trackerUploadTorrentSpec{
	"ACM":   {source: "AsianCinema"},
	"ANT":   {source: "ANT"},
	"AR":    {source: "AlphaRatio"},
	"ASC":   {source: "ASC"},
	"AZ":    {source: "AvistaZ", defaultAnnounce: "https://tracker.avistaz.to/announce"},
	"BHD":   {source: "BHD"},
	"BHDTV": {source: "BIT-HDTV", useMyAnnounce: true},
	"BJS":   {source: "BJ"},
	"BT":    {source: "BT"},
	"CZ":    {source: "CinemaZ", defaultAnnounce: "https://tracker.cinemaz.to/announce"},
	"DC":    {source: "DigitalCore.club"},
	"FF":    {source: "FunFile"},
	"FL":    {source: "FL"},
	"GPW":   {source: "GreatPosterWall"},
	"HDB":   {source: "HDBits"},
	"HDS":   {source: "HD-Space"},
	"HDT":   {source: "hd-torrents.org"},
	"IS":    {source: "https://immortalseed.me"},
	"MTV":   {source: "MTV"},
	"NBL":   {source: "NBL"},
	"PHD":   {source: "PrivateHD", defaultAnnounce: "https://tracker.privatehd.to/announce"},
	"PTP":   {source: "PTP"},
	"PTS":   {source: "[www.ptskit.org] PTSKIT"},
	"RTF":   {source: "sunshine"},
	"THR":   {source: "[https://www.torrenthr.org] TorrentHR.org"},
	"TL":    {source: "TorrentLeech.org"},
	"TOS":   {source: "TheOldSchool"},
	"TVC":   {source: "TVCHAOS"},
}

func PrepareTrackerUploadTorrent(meta api.PreparedMetadata, dbPath string, tracker string, trackerConfig config.TrackerConfig) (api.PreparedMetadata, error) {
	source, announce, ok := trackerUploadTorrentFields(tracker, trackerConfig)
	if !ok {
		return meta, nil
	}

	basePath, err := ResolveUploadTorrentPath(meta, dbPath)
	if err != nil {
		if isUploadTorrentNotFound(err) {
			return meta, nil
		}
		return api.PreparedMetadata{}, err
	}
	artifactPath, err := ResolveTrackerTorrentArtifactPath(meta, dbPath, tracker)
	if err != nil {
		if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
			return meta, nil
		}
		return api.PreparedMetadata{}, err
	}
	if err := WritePersonalizedTorrent(basePath, artifactPath, announce, "", source); err != nil {
		return api.PreparedMetadata{}, err
	}
	meta.TorrentPath = artifactPath
	return meta, nil
}

func PrepareDryRunInjectionTorrent(meta api.PreparedMetadata, dbPath string, tracker string, trackerConfig config.TrackerConfig) (api.PreparedMetadata, error) {
	source, announce, ok := trackerUploadTorrentFields(tracker, trackerConfig)
	if !ok {
		source = strings.ToUpper(strings.TrimSpace(tracker))
		announce = strings.TrimSpace(trackerConfig.AnnounceURL)
		if announce == "" {
			announce = strings.TrimSpace(trackerConfig.MyAnnounceURL)
		}
		if source == "" && announce == "" {
			return meta, nil
		}
	}

	basePath, err := ResolveUploadTorrentPath(meta, dbPath)
	if err != nil {
		if isUploadTorrentNotFound(err) {
			return meta, nil
		}
		return api.PreparedMetadata{}, err
	}
	artifactPath, err := ResolveTrackerTorrentArtifactPath(meta, dbPath, tracker)
	if err != nil {
		if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
			return meta, nil
		}
		return api.PreparedMetadata{}, err
	}
	if err := WritePersonalizedTorrent(basePath, artifactPath, announce, "", source); err != nil {
		return api.PreparedMetadata{}, err
	}
	meta.TorrentPath = artifactPath
	return meta, nil
}

func trackerUploadTorrentFields(tracker string, trackerConfig config.TrackerConfig) (string, string, bool) {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	spec, ok := trackerUploadTorrentSpecs[name]
	if !ok {
		return "", "", false
	}
	announce := strings.TrimSpace(trackerConfig.AnnounceURL)
	if spec.useMyAnnounce {
		announce = strings.TrimSpace(trackerConfig.MyAnnounceURL)
	}
	if announce == "" {
		announce = spec.defaultAnnounce
	}
	source := strings.TrimSpace(spec.source)
	if source == "" && announce == "" {
		return "", "", false
	}
	return source, announce, true
}

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
	return filepath.Join(tmpDir, "["+name+"]."+base+".torrent"), nil
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

	return "", fmt.Errorf("trackers: %w", errUploadTorrentNotFound)
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
var errUploadTorrentNotFound = errors.New("torrent file not found")

func isUploadTorrentNotFound(err error) bool {
	return errors.Is(err, errUploadTorrentNotFound)
}

func WriteUploadTorrent(sourcePath string, outputPath string) error {
	torrentMeta, err := metainfo.LoadFromFile(sourcePath)
	if err != nil {
		return fmt.Errorf("trackers: load upload torrent: %w: %w", errInvalidUploadTorrent, err)
	}
	cleanTorrentMeta(torrentMeta)
	if err := rewriteTorrentInfoSource(torrentMeta, "", "upload torrent"); err != nil {
		return err
	}
	return writeTorrentMeta(*torrentMeta, outputPath, "upload torrent")
}

func WritePersonalizedTorrent(sourcePath string, outputPath string, announceURL string, comment string, source string) error {
	torrentMeta, err := metainfo.LoadFromFile(sourcePath)
	if err != nil {
		return fmt.Errorf("trackers: load torrent artifact: %w", err)
	}
	cleanTorrentMeta(torrentMeta)

	if err := rewriteTorrentInfoSource(torrentMeta, source, "torrent artifact"); err != nil {
		return err
	}

	if trimmedAnnounce := strings.TrimSpace(announceURL); trimmedAnnounce != "" {
		torrentMeta.Announce = trimmedAnnounce
		torrentMeta.AnnounceList = metainfo.AnnounceList{{trimmedAnnounce}}
	}
	torrentMeta.Comment = torrentmeta.UploadCommentFallback
	if trimmedComment := strings.TrimSpace(comment); trimmedComment != "" {
		torrentMeta.Comment = trimmedComment
	}
	torrentMeta.CreatedBy = torrentmeta.UploadCreatedBy

	return writeTorrentMeta(*torrentMeta, outputPath, "torrent artifact")
}

func rewriteTorrentInfoSource(torrentMeta *metainfo.MetaInfo, source string, context string) error {
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("trackers: unmarshal %s info: %w", context, err)
	}
	info.Source = strings.TrimSpace(source)
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return fmt.Errorf("trackers: marshal %s info: %w", context, err)
	}
	torrentMeta.InfoBytes = infoBytes
	return nil
}

func cleanTorrentMeta(torrentMeta *metainfo.MetaInfo) {
	torrentMeta.Announce = ""
	torrentMeta.AnnounceList = nil
	torrentMeta.Nodes = nil
	torrentMeta.UrlList = nil
	torrentMeta.Comment = torrentmeta.UploadCommentFallback
	torrentMeta.CreatedBy = torrentmeta.UploadCreatedBy
}

func writeTorrentMeta(torrentMeta metainfo.MetaInfo, outputPath string, context string) error {
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("trackers: create %s dir: %w", context, err)
	}
	file, err := os.CreateTemp(dir, filepath.Base(outputPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("trackers: create temp %s: %w", context, err)
	}
	tmpPath := file.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("trackers: chmod temp %s: %w", context, err)
	}
	if err := torrentMeta.Write(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("trackers: write %s: %w", context, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("trackers: close temp %s: %w", context, err)
	}
	if err := replaceStagedTorrent(tmpPath, outputPath); err != nil {
		return fmt.Errorf("trackers: replace %s: %w", context, err)
	}
	removeTemp = false
	return nil
}

func replaceStagedTorrent(tmpPath string, outputPath string) error {
	info, err := os.Stat(outputPath)
	if err != nil {
		if os.IsNotExist(err) {
			if renameErr := os.Rename(tmpPath, outputPath); renameErr != nil {
				return fmt.Errorf("rename staged torrent into place: %w", renameErr)
			}
			return nil
		}
		return fmt.Errorf("stat output torrent: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", outputPath)
	}

	backupPath, err := reserveTorrentBackupPath(filepath.Dir(outputPath), filepath.Base(outputPath)+".backup-*")
	if err != nil {
		return err
	}
	if err := os.Rename(outputPath, backupPath); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("backup existing torrent: %w", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		restoreErr := os.Rename(backupPath, outputPath)
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore original torrent: %w", restoreErr))
		}
		return fmt.Errorf("replace existing torrent: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("remove replaced torrent backup: %w", err)
	}
	return nil
}

func reserveTorrentBackupPath(dir string, pattern string) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temp torrent backup marker: %w", err)
	}
	path := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(path)
	if closeErr != nil || removeErr != nil {
		return "", errors.Join(closeErr, removeErr)
	}
	return path, nil
}
