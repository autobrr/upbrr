// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	torrentmeta "github.com/autobrr/upbrr/internal/torrent/metainfo"
	"github.com/autobrr/upbrr/pkg/api"
)

// prepareTrackerUploadTorrentWithRegistry writes the exact release-scoped
// torrent artifact assigned to one tracker and returns a copied subject pointing
// at it. Every successful preparation receives its own artifact path, including
// trackers whose torrent bytes are otherwise identical.
func prepareTrackerUploadTorrentWithRegistry(
	meta api.UploadSubject,
	dbPath string,
	tracker string,
	trackerConfig config.TrackerConfig,
	registry *Registry,
) (api.UploadSubject, error) {
	source, announce, hasPolicy, err := trackerUploadTorrentFieldsWithRegistry(tracker, trackerConfig, registry)
	if err != nil {
		return api.UploadSubject{}, err
	}

	basePath, err := resolveUploadTorrentBasePath(meta, dbPath)
	if err != nil {
		// Policyless definitions are used by lightweight orchestration adapters and
		// tests that do not consume torrent files. Real upload adapters still reject
		// the missing exact path through PreparedUploadTorrentPath.
		if !hasPolicy && isUploadTorrentNotFound(err) {
			return meta, nil
		}
		return api.UploadSubject{}, fmt.Errorf("trackers: prepare %s upload torrent base: %w", normalizeTrackerName(tracker), err)
	}
	artifactPath, err := ResolveTrackerTorrentArtifactPath(meta, dbPath, tracker)
	if err != nil {
		return api.UploadSubject{}, fmt.Errorf("trackers: prepare %s upload torrent path: %w", normalizeTrackerName(tracker), err)
	}
	var rename func(string) string
	if renamer, ok := registry.LookupContentRenamer(tracker); ok {
		subject := meta
		rename = func(name string) string { return renamer(subject, name) }
	}
	if err := writePersonalizedTorrent(basePath, artifactPath, announce, source, rename); err != nil {
		return api.UploadSubject{}, fmt.Errorf("trackers: prepare %s upload torrent artifact: %w", normalizeTrackerName(tracker), err)
	}
	meta.TorrentPath = artifactPath
	return meta, nil
}

func trackerUploadTorrentFieldsWithRegistry(tracker string, trackerConfig config.TrackerConfig, registry *Registry) (string, string, bool, error) {
	if owned, ok := registry.LookupUploadArtifactPolicy(tracker); ok {
		source, announce, err := uploadArtifactFields(owned, trackerConfig)
		if err != nil {
			return "", "", true, fmt.Errorf("trackers: %s upload torrent policy: %w", normalizeTrackerName(tracker), err)
		}
		return source, announce, true, nil
	}
	source, announce := trackerUploadTorrentFields(tracker, trackerConfig)
	return source, announce, false, nil
}

func uploadArtifactFields(policy UploadArtifactPolicy, trackerConfig config.TrackerConfig) (string, string, error) {
	announce := strings.TrimSpace(trackerConfig.AnnounceURL)
	if policy.UseMyAnnounce {
		announce = strings.TrimSpace(trackerConfig.MyAnnounceURL)
	}
	if announce == "" {
		announce = policy.DefaultAnnounce
	}
	if policy.RequireAnnounce && announce == "" {
		return "", "", errors.New("required announce URL is missing")
	}
	source := strings.TrimSpace(policy.Source)
	return source, announce, nil
}

func trackerUploadTorrentFields(tracker string, trackerConfig config.TrackerConfig) (string, string) {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	announce := strings.TrimSpace(trackerConfig.AnnounceURL)
	if announce == "" {
		announce = strings.TrimSpace(trackerConfig.MyAnnounceURL)
	}
	return name, announce
}

// ResolveTrackerTorrentArtifactPath returns the local torrent artifact path for tracker.
func ResolveTrackerTorrentArtifactPath(meta api.UploadSubject, dbPath string, tracker string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: tracker torrent path requires db path and source path")
	}

	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
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

// resolveUploadTorrentBasePath selects TorrentPath, ClientTorrentPath, SourcePath,
// then the release-scoped default. A non-default candidate is copied to the
// release-scoped path with tracker fields removed when it can be decoded; an
// existing candidate already at that path is returned as-is.
func resolveUploadTorrentBasePath(meta api.UploadSubject, dbPath string) (string, error) {
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
				if strings.EqualFold(filepath.Clean(candidate), filepath.Clean(cleanPath)) {
					return cleanPath, nil
				}
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
			tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
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

// PreparedUploadTorrentPath returns the exact tracker artifact assigned by the
// preparation module. It never falls back to a client, source, or generic
// torrent path.
func PreparedUploadTorrentPath(meta api.UploadSubject) (string, error) {
	torrentPath := strings.TrimSpace(meta.TorrentPath)
	if torrentPath == "" {
		return "", errors.New("trackers: prepared tracker torrent is missing")
	}
	if !strings.EqualFold(filepath.Ext(torrentPath), ".torrent") {
		return "", errors.New("trackers: prepared tracker torrent has an invalid extension")
	}
	info, err := os.Stat(torrentPath)
	if err != nil {
		return "", fmt.Errorf("trackers: stat prepared tracker torrent: %w", err)
	}
	if info.IsDir() {
		return "", errors.New("trackers: prepared tracker torrent is not a file")
	}
	return torrentPath, nil
}

func uploadTorrentCleanPath(meta api.UploadSubject, dbPath string) (string, bool) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", false
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", false
	}
	tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
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

// WriteUploadTorrent validates sourcePath, removes announce, node, URL-list, and
// source fields, applies canonical creator/comment values, and atomically writes
// outputPath with mode 0600.
func WriteUploadTorrent(sourcePath string, outputPath string) error {
	torrentMeta, err := metainfo.LoadFromFile(sourcePath)
	if err != nil {
		return fmt.Errorf("trackers: load upload torrent: %w: %w", errInvalidUploadTorrent, err)
	}
	cleanTorrentMeta(torrentMeta)
	if err := rewriteTorrentInfo(torrentMeta, "", nil, "upload torrent"); err != nil {
		return err
	}
	return writeTorrentMeta(*torrentMeta, outputPath, "upload torrent")
}

// WritePersonalizedTorrent strips inherited tracker fields, applies the supplied
// announce and source values plus canonical upload metadata, and atomically writes
// outputPath with mode 0600.
func WritePersonalizedTorrent(sourcePath string, outputPath string, announceURL string, source string) error {
	return writePersonalizedTorrent(sourcePath, outputPath, announceURL, source, nil)
}

// writePersonalizedTorrent is WritePersonalizedTorrent plus an optional rename
// applied to the torrent root name and every file path component.
func writePersonalizedTorrent(sourcePath string, outputPath string, announceURL string, source string, rename func(string) string) error {
	torrentMeta, err := metainfo.LoadFromFile(sourcePath)
	if err != nil {
		return fmt.Errorf("trackers: load torrent artifact: %w", err)
	}
	cleanTorrentMeta(torrentMeta)

	if err := rewriteTorrentInfo(torrentMeta, source, rename, "torrent artifact"); err != nil {
		return err
	}

	if trimmedAnnounce := strings.TrimSpace(announceURL); trimmedAnnounce != "" {
		torrentMeta.Announce = trimmedAnnounce
		torrentMeta.AnnounceList = metainfo.AnnounceList{{trimmedAnnounce}}
	}

	return writeTorrentMeta(*torrentMeta, outputPath, "torrent artifact")
}

// rewriteTorrentInfo sets the info source and, when rename is non-nil, renames
// the root name and each file path component. Names are not part of the piece
// hashes, so content and piece layout are untouched. Hybrid v2 torrents also
// carry a file tree that is not rewritten, so they are rejected only when the
// rename actually changes a name; an already-compliant hybrid torrent is
// accepted unchanged.
func rewriteTorrentInfo(torrentMeta *metainfo.MetaInfo, source string, rename func(string) string, context string) error {
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("trackers: unmarshal %s info: %w", context, err)
	}
	info.Source = strings.TrimSpace(source)
	if rename != nil {
		changed, err := renameTorrentInfoContent(&info, rename)
		if err != nil {
			return fmt.Errorf("trackers: rename %s content: %w", context, err)
		}
		if changed && info.HasV2() {
			return fmt.Errorf("trackers: rename %s content: v2 torrents are not supported", context)
		}
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return fmt.Errorf("trackers: marshal %s info: %w", context, err)
	}
	torrentMeta.InfoBytes = infoBytes
	return nil
}

// renameTorrentInfoContent applies rename to the root name and every file path
// component of the info dictionary, including the name.utf-8 and path.utf-8
// variants, and reports whether any name changed. Results are memoised because
// folder names repeat across every file. It rejects any result that is not a
// single legal path component, so a renamer can never alter the torrent's
// directory layout.
func renameTorrentInfoContent(info *metainfo.Info, rename func(string) string) (bool, error) {
	changed := false
	cache := make(map[string]string)
	renameComponent := func(name string) (string, error) {
		renamed, ok := cache[name]
		if !ok {
			renamed = rename(name)
			if renamed == "" || renamed == "." || renamed == ".." || strings.ContainsAny(renamed, `/\`) {
				return "", fmt.Errorf("invalid renamed path component %q", renamed)
			}
			cache[name] = renamed
		}
		changed = changed || renamed != name
		return renamed, nil
	}
	renameParts := func(parts []string) ([]string, error) {
		out := make([]string, len(parts))
		for i, part := range parts {
			renamed, err := renameComponent(part)
			if err != nil {
				return nil, err
			}
			out[i] = renamed
		}
		return out, nil
	}

	var err error
	if info.Name, err = renameComponent(info.Name); err != nil {
		return false, err
	}
	if info.NameUtf8 != "" {
		if info.NameUtf8, err = renameComponent(info.NameUtf8); err != nil {
			return false, err
		}
	}
	for i := range info.Files {
		file := &info.Files[i]
		if file.Path, err = renameParts(file.Path); err != nil {
			return false, err
		}
		if file.PathUtf8, err = renameParts(file.PathUtf8); err != nil {
			return false, err
		}
	}
	return changed, nil
}

func cleanTorrentMeta(torrentMeta *metainfo.MetaInfo) {
	createdBy := torrentmeta.CanonicalCreatedBy(torrentMeta.CreatedBy)
	torrentMeta.Announce = ""
	torrentMeta.AnnounceList = nil
	torrentMeta.Nodes = nil
	torrentMeta.UrlList = nil
	torrentMeta.Comment = torrentmeta.UploadComment
	torrentMeta.CreatedBy = createdBy
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
