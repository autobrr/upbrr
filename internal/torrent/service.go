// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	slashpath "path" //nolint:depguard // Joins torrent-internal slash-delimited metainfo paths.
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/torrentmeta"
	"github.com/autobrr/upbrr/pkg/api"
)

type Service struct {
	logger  api.Logger
	tmpRoot string
}

func NewService(logger api.Logger, tmpRoot string) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Service{logger: logger, tmpRoot: strings.TrimSpace(tmpRoot)}
}

func (s *Service) Create(ctx context.Context, meta api.PreparedMetadata) (api.TorrentResult, error) {
	select {
	case <-ctx.Done():
		return api.TorrentResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	source := strings.TrimSpace(meta.SourcePath)
	if source == "" {
		return api.TorrentResult{}, internalerrors.ErrInvalidInput
	}
	meta.SourcePath = source

	s.logger.Debugf("torrent: preparing for %s", source)
	policy := resolveTrackerPolicy(meta)
	forceRehash := torrentOverrideEnabled(meta.TorrentOverrides.Rehash)
	reuseOnly := torrentOverrideEnabled(meta.TorrentOverrides.NoHash)
	if forceRehash && reuseOnly {
		s.logger.Debugf("torrent: ignoring nohash because rehash is enabled for %s", source)
		reuseOnly = false
	}
	s.logger.Debugf("torrent: reuse decision=scan source=%s force_rehash=%t reuse_only=%t", source, forceRehash, reuseOnly)
	emitTorrentProgress(ctx, meta, "running", "Checking reusable torrent")

	clientTorrent := strings.TrimSpace(meta.ClientTorrentPath)
	if !forceRehash && clientTorrent != "" {
		s.logger.Debugf("torrent: checking client-provided torrent %s", clientTorrent)
		info, err := os.Stat(clientTorrent)
		if err == nil && !info.IsDir() {
			if err := validateCandidateTorrent(clientTorrent, policy, meta, s.logger); err == nil {
				s.logger.Debugf("torrent: using client-provided torrent %s", clientTorrent)
				return resultFromExistingTorrent(ctx, meta, clientTorrent, "Using client-provided torrent")
			}
		}
	}

	// If user already provided a .torrent file, re-use it directly.
	if strings.EqualFold(filepath.Ext(source), ".torrent") {
		info, err := os.Stat(source)
		if err != nil {
			return api.TorrentResult{}, fmt.Errorf("torrent: path %q: %w", source, err)
		}
		if info.IsDir() {
			return api.TorrentResult{}, internalerrors.ErrInvalidInput
		}
		if err := validateCandidateTorrent(source, policy, meta, s.logger); err != nil {
			return api.TorrentResult{}, fmt.Errorf("torrent: provided torrent %q: %w", source, err)
		}
		s.logger.Debugf("torrent: using provided torrent %s", source)
		return resultFromExistingTorrent(ctx, meta, source, "Using provided torrent")
	}

	if !forceRehash && s.tmpRoot != "" {
		tmpTorrentPath, err := TempTorrentPath(s.tmpRoot, meta, source)
		if err != nil {
			return api.TorrentResult{}, err
		}
		s.logger.Debugf("torrent: checking temp torrent %s", tmpTorrentPath)
		if info, err := os.Stat(tmpTorrentPath); err == nil {
			if !info.IsDir() {
				if err := validateCandidateTorrent(tmpTorrentPath, policy, meta, s.logger); err == nil {
					s.logger.Debugf("torrent: reusing existing temp torrent %s", tmpTorrentPath)
					return resultFromExistingTorrent(ctx, meta, tmpTorrentPath, "Reusing existing torrent")
				}
			}
		}
	}

	candidate := source + ".torrent"
	if !forceRehash {
		s.logger.Debugf("torrent: checking adjacent torrent %s", candidate)
		if info, err := os.Stat(candidate); err == nil {
			if !info.IsDir() {
				if err := validateCandidateTorrent(candidate, policy, meta, s.logger); err == nil {
					s.logger.Debugf("torrent: reusing existing torrent %s", candidate)
					return resultFromExistingTorrent(ctx, meta, candidate, "Reusing existing torrent")
				}
			}
		}

		baseName := filepath.Base(source)
		if baseName != "" {
			sibling := filepath.Join(filepath.Dir(source), baseName+".torrent")
			if sibling != candidate {
				s.logger.Debugf("torrent: checking sibling torrent %s", sibling)
				if info, err := os.Stat(sibling); err == nil {
					if !info.IsDir() {
						if err := validateCandidateTorrent(sibling, policy, meta, s.logger); err == nil {
							s.logger.Debugf("torrent: reusing existing torrent %s", sibling)
							return resultFromExistingTorrent(ctx, meta, sibling, "Reusing existing torrent")
						}
					}
				}
			}
		}
	}

	if reuseOnly {
		return api.TorrentResult{}, fmt.Errorf("torrent: no reusable torrent found with nohash enabled: %w", internalerrors.ErrNotFound)
	}

	s.logger.Debugf("torrent: resolving create spec for %s", source)
	createSpec, err := resolveCreateSpec(meta, source, s.tmpRoot)
	if err != nil {
		return api.TorrentResult{}, err
	}
	s.logger.Debugf("torrent: create spec path=%s name=%q include_patterns=%d staged=%t", createSpec.path, createSpec.name, len(createSpec.includePatterns), createSpec.cleanupPath != "")
	if createSpec.cleanupPath != "" {
		defer func() {
			if err := os.RemoveAll(createSpec.cleanupPath); err != nil {
				s.logger.Warnf("torrent: failed to remove staging path path=%s err=%v", createSpec.cleanupPath, err)
			}
		}()
	}
	if _, err := os.Stat(createSpec.path); err != nil {
		if os.IsNotExist(err) {
			return api.TorrentResult{}, fmt.Errorf("torrent: path %q: %w", createSpec.path, internalerrors.ErrNotFound)
		}
		return api.TorrentResult{}, fmt.Errorf("torrent: path %q: %w", createSpec.path, err)
	}

	select {
	case <-ctx.Done():
		return api.TorrentResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if s.tmpRoot == "" {
		return api.TorrentResult{}, errors.New("torrent: tmp root is required")
	}
	outputPath, err := TempTorrentPath(s.tmpRoot, meta, source)
	if err != nil {
		return api.TorrentResult{}, err
	}
	pieceOptions := mkbrrPieceOptions{maxPieceExp: 27}
	if policy != nil {
		pieceOptions = policy.createOptions(meta)
	}
	s.logger.Infof("torrent: creating torrent output=%s max_piece_exp=%d piece_exp_set=%t", outputPath, pieceOptions.maxPieceExp, pieceOptions.pieceExp != nil)
	emitTorrentProgress(ctx, meta, "running", "Creating torrent with mkbrr")

	info, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:             createSpec.path,
		Name:             createSpec.name,
		OutputPath:       outputPath,
		IsPrivate:        true,
		MaxPieceLength:   &pieceOptions.maxPieceExp,
		PieceLengthExp:   pieceOptions.pieceExp,
		IncludePatterns:  createSpec.includePatterns,
		ProgressCallback: torrentProgressCallback(ctx, meta),
	})
	if err != nil {
		emitTorrentProgress(ctx, meta, "failed", "Torrent creation failed")
		return api.TorrentResult{}, fmt.Errorf("torrent: create %q: %w", createSpec.path, err)
	}
	if err := validateTorrentContent(info.Path, meta); err != nil {
		emitTorrentProgress(ctx, meta, "failed", "Torrent validation failed")
		return api.TorrentResult{}, fmt.Errorf("torrent: validate created torrent %q: %w", info.Path, err)
	}
	if err := setCreatedBy(info.Path, torrentmeta.MkbrrUploadCreatedBy); err != nil {
		emitTorrentProgress(ctx, meta, "failed", "Torrent metadata update failed")
		return api.TorrentResult{}, err
	}
	emitTorrentProgress(ctx, meta, "completed", "Torrent ready")
	s.logger.Infof("torrent: created torrent %s", info.Path)

	return api.TorrentResult{Path: info.Path, InfoHash: info.InfoHash}, nil
}

func setCreatedBy(path string, createdBy string) error {
	torrentMeta, err := metainfo.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("torrent: load created torrent metadata: %w", err)
	}
	torrentMeta.CreatedBy = createdBy
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("torrent: open created torrent metadata: %w", err)
	}
	if err := torrentMeta.Write(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("torrent: write created torrent metadata: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("torrent: close created torrent metadata: %w", err)
	}
	return nil
}

func resultFromExistingTorrent(ctx context.Context, meta api.PreparedMetadata, path string, message string) (api.TorrentResult, error) {
	result, err := resultFromPath(path)
	if err != nil {
		return api.TorrentResult{}, err
	}
	emitTorrentProgress(ctx, meta, "completed", message)
	return result, nil
}

func emitTorrentProgress(ctx context.Context, meta api.PreparedMetadata, status string, message string) {
	emitTorrentHashProgress(ctx, meta, status, message, 0, 0, 0)
}

func torrentProgressCallback(ctx context.Context, meta api.PreparedMetadata) mkbrr.ProgressCallback {
	return func(completed, total int, hashRate float64) {
		if total <= 0 {
			emitTorrentHashProgress(ctx, meta, "running", "Preparing torrent pieces", completed, total, hashRate)
			return
		}
		status := "running"
		message := fmt.Sprintf("Hashing pieces... %d%% (%d/%d pieces)", progressPercent(completed, total), completed, total)
		if hashRate > 0 {
			message = fmt.Sprintf("Hashing pieces... [%.0f MiB/s] %d%% (%d/%d pieces)", hashRate, progressPercent(completed, total), completed, total)
		}
		if completed >= total {
			status = "completed"
			message = "Hashing complete"
		}
		emitTorrentHashProgress(ctx, meta, status, message, completed, total, hashRate)
	}
}

func emitTorrentHashProgress(ctx context.Context, meta api.PreparedMetadata, status string, message string, completed int, total int, hashRate float64) {
	tracker := ""
	if len(meta.Trackers) == 1 {
		tracker = meta.Trackers[0]
	}
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath:      meta.SourcePath,
		Tracker:         tracker,
		Task:            "torrent",
		Status:          status,
		Message:         message,
		CompletedPieces: completed,
		TotalPieces:     total,
		Percent:         progressPercent(completed, total),
		HashRateMiB:     hashRate,
		Timestamp:       time.Now().UTC().Format(time.RFC3339),
	})
}

func progressPercent(completed int, total int) int {
	if total <= 0 {
		return 0
	}
	percent := int(math.Round((float64(completed) / float64(total)) * 100))
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func torrentOverrideEnabled(value *bool) bool {
	return value != nil && *value
}

func validateCandidateTorrent(path string, policy *trackerTorrentPolicy, meta api.PreparedMetadata, logger api.Logger) error {
	if policy == nil {
		return validateTorrentContent(path, meta)
	}
	if err := policy.validateTorrent(path, meta); err != nil {
		if logger != nil {
			logger.Warnf("torrent: skipping non-compliant torrent %s: %v", path, err)
		}
		return err
	}
	if err := validateTorrentContent(path, meta); err != nil {
		if logger != nil {
			logger.Warnf("torrent: skipping mismatched torrent %s: %v", path, err)
		}
		return err
	}
	return nil
}

type createSpec struct {
	path            string
	name            string
	includePatterns []string
	cleanupPath     string
}

type contentFile struct {
	path   string
	length int64
}

type sourceContentFile struct {
	contentFile
}

func resolveCreateSpec(meta api.PreparedMetadata, source string, tmpRoot string) (createSpec, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return createSpec{}, internalerrors.ErrInvalidInput
	}
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return createSpec{}, fmt.Errorf("torrent: path %q: %w", source, internalerrors.ErrNotFound)
		}
		return createSpec{}, fmt.Errorf("torrent: path %q: %w", source, err)
	}
	if !info.IsDir() {
		return createSpec{path: source}, nil
	}

	if strings.TrimSpace(meta.DiscType) != "" {
		return createSpec{path: normalizeDiscSource(source)}, nil
	}

	wanted, err := wantedFilesWithin(source, meta.FileList)
	if err != nil {
		return createSpec{}, err
	}
	if len(wanted) == 1 {
		return createSpec{path: wanted[0]}, nil
	}
	if len(wanted) > 1 {
		if needsWantedFileStaging(source, wanted) {
			stagedRoot, cleanupPath, err := stageWantedFiles(tmpRoot, source, wanted)
			if err != nil {
				return createSpec{}, err
			}
			return createSpec{
				path:        stagedRoot,
				name:        filepath.Base(filepath.Clean(source)),
				cleanupPath: cleanupPath,
			}, nil
		}
		include, err := includePatternsForFiles(source, wanted)
		if err != nil {
			return createSpec{}, err
		}
		return createSpec{
			path:            source,
			name:            filepath.Base(filepath.Clean(source)),
			includePatterns: include,
		}, nil
	}

	return createSpec{path: source}, nil
}

func normalizeDiscSource(source string) string {
	return filepath.Clean(source)
}

func mkbrrIgnoredPath(rel string, isDir bool) bool {
	lowerRel := strings.ToLower(filepath.ToSlash(rel))
	if lowerRel == "@eadir" || strings.HasPrefix(lowerRel, "@eadir/") ||
		strings.HasSuffix(lowerRel, "/@eadir") || strings.Contains(lowerRel, "/@eadir/") {
		return true
	}
	if isDir {
		return false
	}
	return strings.HasSuffix(lowerRel, ".torrent") ||
		strings.HasSuffix(lowerRel, ".ds_store") ||
		strings.HasSuffix(lowerRel, "thumbs.db") ||
		strings.HasSuffix(lowerRel, "desktop.ini") ||
		strings.HasSuffix(lowerRel, "zone.identifier")
}

func wantedFilesWithin(root string, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("torrent: resolve source root: %w", err)
	}
	wanted := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		trimmed := strings.TrimSpace(file)
		if trimmed == "" {
			continue
		}
		absFile, err := filepath.Abs(trimmed)
		if err != nil {
			return nil, fmt.Errorf("torrent: resolve wanted file: %w", err)
		}
		if err := ensureWithinRoot(cleanRoot, absFile); err != nil {
			return nil, err
		}
		info, err := os.Stat(absFile)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("torrent: wanted file %q: %w", absFile, internalerrors.ErrNotFound)
			}
			return nil, fmt.Errorf("torrent: stat wanted file %q: %w", absFile, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("torrent: wanted file %q is not a regular file", absFile)
		}
		cleanFile := filepath.Clean(absFile)
		if _, ok := seen[cleanFile]; ok {
			continue
		}
		seen[cleanFile] = struct{}{}
		wanted = append(wanted, cleanFile)
	}
	sort.Strings(wanted)
	return wanted, nil
}

func includePatternsForFiles(root string, files []string) ([]string, error) {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("torrent: resolve source root: %w", err)
	}
	include := make([]string, 0, len(files))
	for _, file := range files {
		absFile, err := filepath.Abs(file)
		if err != nil {
			return nil, fmt.Errorf("torrent: resolve wanted file: %w", err)
		}
		if err := ensureWithinRoot(cleanRoot, absFile); err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(cleanRoot, absFile)
		if err != nil {
			return nil, fmt.Errorf("torrent: wanted file relative path: %w", err)
		}
		slashRel := filepath.ToSlash(rel)
		if strings.Contains(slashRel, ",") {
			return nil, fmt.Errorf("torrent: wanted file %q requires staging", slashRel)
		}
		include = append(include, globLiteral(slashRel))
	}
	return include, nil
}

func needsWantedFileStaging(root string, files []string) bool {
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	for _, file := range files {
		absFile, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(cleanRoot, absFile)
		if err == nil && strings.Contains(filepath.ToSlash(rel), ",") {
			return true
		}
	}
	return false
}

func stageWantedFiles(tmpRoot string, root string, files []string) (string, string, error) {
	if strings.TrimSpace(tmpRoot) == "" {
		return "", "", errors.New("torrent: tmp root is required for staged wanted files")
	}
	rootName, err := safeTorrentRootName(root)
	if err != nil {
		return "", "", err
	}
	stageParent, err := os.MkdirTemp(tmpRoot, "wanted-files-*")
	if err != nil {
		return "", "", fmt.Errorf("torrent: create wanted file staging dir: %w", err)
	}
	stagedRoot := filepath.Join(stageParent, rootName)
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		_ = os.RemoveAll(stageParent)
		return "", "", fmt.Errorf("torrent: resolve source root: %w", err)
	}
	for _, file := range files {
		absFile, err := filepath.Abs(file)
		if err != nil {
			_ = os.RemoveAll(stageParent)
			return "", "", fmt.Errorf("torrent: resolve wanted file: %w", err)
		}
		rel, err := filepath.Rel(cleanRoot, absFile)
		if err != nil {
			_ = os.RemoveAll(stageParent)
			return "", "", fmt.Errorf("torrent: wanted file relative path: %w", err)
		}
		dst := filepath.Join(stagedRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			_ = os.RemoveAll(stageParent)
			return "", "", fmt.Errorf("torrent: create wanted file staging parent: %w", err)
		}
		if err := os.Link(absFile, dst); err != nil {
			if copyErr := filesystem.CopyFile(absFile, dst); copyErr != nil {
				_ = os.RemoveAll(stageParent)
				return "", "", fmt.Errorf("torrent: stage wanted file %q: link: %w; copy: %w", absFile, err, copyErr)
			}
		}
	}
	return stagedRoot, stageParent, nil
}

func safeTorrentRootName(root string) (string, error) {
	cleanRoot := filepath.Clean(root)
	rootName := filepath.Base(cleanRoot)
	if rootName == "" || rootName == "." || rootName == string(filepath.Separator) || filepath.IsAbs(rootName) {
		return "", fmt.Errorf("torrent: invalid source root name %q", root)
	}
	return rootName, nil
}

func ensureWithinRoot(root string, child string) error {
	root = filepath.Clean(root)
	child = filepath.Clean(child)
	if pathutil.SamePath(root, child) || !pathutil.IsWithinRoot(root, child) {
		return fmt.Errorf("torrent: wanted file %q is outside source root %q", child, root)
	}
	return nil
}

func globLiteral(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '[':
			builder.WriteString("[[]")
		case '*', '?', '{', '}':
			builder.WriteRune('[')
			builder.WriteRune(r)
			builder.WriteRune(']')
		default:
			builder.WriteRune(r)
		}
	}
	return "{" + builder.String() + "}"
}

func validateTorrentContent(path string, meta api.PreparedMetadata) error {
	expectedFiles, ok, err := expectedTorrentFiles(meta)
	if err != nil {
		return err
	}
	expectedName, nameOK, err := expectedTorrentName(meta)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	expected := sourceContentPaths(expectedFiles)
	torrentMeta, err := metainfo.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("torrent: read candidate metadata: %w", err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("torrent: unmarshal candidate info: %w", err)
	}
	if nameOK && info.BestName() != expectedName {
		return errors.New("torrent: candidate name mismatch")
	}
	actual := torrentContentPaths(info)
	if len(actual) == 0 {
		return errors.New("torrent: candidate has no files")
	}
	if !sameContentSet(actual, expected) {
		return errors.New("torrent: candidate content mismatch")
	}
	return nil
}

func expectedTorrentFiles(meta api.PreparedMetadata) ([]sourceContentFile, bool, error) {
	source := strings.TrimSpace(meta.SourcePath)
	if source == "" || strings.EqualFold(filepath.Ext(source), ".torrent") {
		return nil, false, nil
	}
	if strings.TrimSpace(meta.DiscType) != "" {
		root := normalizeDiscSource(source)
		expected, err := diskContentFiles(root)
		return expected, true, err
	}
	info, err := os.Stat(source)
	if err == nil && !info.IsDir() {
		return []sourceContentFile{{
			contentFile: contentFile{path: filepath.Base(source), length: info.Size()},
		}}, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("torrent: stat source %q: %w", source, err)
	}
	if len(meta.FileList) == 0 {
		expected, err := diskContentFiles(source)
		return expected, true, err
	}
	wanted, err := wantedFilesWithin(source, meta.FileList)
	if err != nil {
		return nil, false, err
	}
	if len(wanted) == 0 {
		return nil, false, errors.New("torrent: no valid wanted files")
	}
	if len(wanted) == 1 {
		info, err := os.Stat(wanted[0])
		if err != nil {
			return nil, false, fmt.Errorf("torrent: stat wanted file %q: %w", wanted[0], err)
		}
		return []sourceContentFile{{
			contentFile: contentFile{path: filepath.Base(wanted[0]), length: info.Size()},
		}}, true, nil
	}
	expected := make([]sourceContentFile, 0, len(wanted))
	root, err := filepath.Abs(source)
	if err != nil {
		return nil, false, fmt.Errorf("torrent: resolve source root: %w", err)
	}
	for _, file := range wanted {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return nil, false, fmt.Errorf("torrent: wanted file relative path: %w", err)
		}
		info, err := os.Stat(file)
		if err != nil {
			return nil, false, fmt.Errorf("torrent: stat wanted file %q: %w", file, err)
		}
		expected = append(expected, sourceContentFile{
			contentFile: contentFile{path: filepath.ToSlash(rel), length: info.Size()},
		})
	}
	return expected, true, nil
}

func expectedTorrentName(meta api.PreparedMetadata) (string, bool, error) {
	source := strings.TrimSpace(meta.SourcePath)
	if source == "" || strings.EqualFold(filepath.Ext(source), ".torrent") {
		return "", false, nil
	}
	if strings.TrimSpace(meta.DiscType) != "" {
		return filepath.Base(filepath.Clean(normalizeDiscSource(source))), true, nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", false, fmt.Errorf("torrent: stat source %q: %w", source, err)
	}
	if !info.IsDir() {
		return filepath.Base(source), true, nil
	}
	if len(meta.FileList) == 0 {
		return filepath.Base(filepath.Clean(source)), true, nil
	}
	wanted, err := wantedFilesWithin(source, meta.FileList)
	if err != nil {
		return "", false, err
	}
	if len(wanted) == 0 {
		return "", false, errors.New("torrent: no valid wanted files")
	}
	if len(wanted) == 1 {
		return filepath.Base(wanted[0]), true, nil
	}
	return filepath.Base(filepath.Clean(source)), true, nil
}

func torrentContentPaths(info metainfo.Info) []contentFile {
	files := info.UpvertedFiles()
	if len(files) == 0 {
		return nil
	}
	result := make([]contentFile, 0, len(files))
	for _, file := range files {
		result = append(result, contentFile{path: torrentContentPath(info, file), length: file.Length})
	}
	return result
}

func diskContentFiles(root string) ([]sourceContentFile, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("torrent: stat disc root %q: %w", root, err)
	}
	if !info.IsDir() {
		return []sourceContentFile{{
			contentFile: contentFile{path: filepath.Base(root), length: info.Size()},
		}}, nil
	}
	paths := make([]sourceContentFile, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			if path != root {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return fmt.Errorf("disc relative path: %w", err)
				}
				if mkbrrIgnoredPath(rel, true) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("disc relative path: %w", err)
		}
		if mkbrrIgnoredPath(rel, false) {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("disc file info: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		paths = append(paths, sourceContentFile{
			contentFile: contentFile{path: filepath.ToSlash(rel), length: info.Size()},
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("torrent: walk disc root %q: %w", root, err)
	}
	sortSourceContentFiles(paths)
	return paths, nil
}

func sourceContentPaths(files []sourceContentFile) []contentFile {
	paths := make([]contentFile, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.contentFile)
	}
	return paths
}

func torrentContentPath(info metainfo.Info, file metainfo.FileInfo) string {
	parts := file.BestPath()
	if len(parts) == 0 {
		return info.BestName()
	}
	return slashpath.Join(parts...)
}

func sameContentSet(left []contentFile, right []contentFile) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]contentFile{}, left...)
	rightCopy := append([]contentFile{}, right...)
	sortContentFiles(leftCopy)
	sortContentFiles(rightCopy)
	for idx := range leftCopy {
		if leftCopy[idx].path != rightCopy[idx].path || leftCopy[idx].length != rightCopy[idx].length {
			return false
		}
	}
	return true
}

func sortSourceContentFiles(files []sourceContentFile) {
	sort.Slice(files, func(left int, right int) bool {
		if files[left].path == files[right].path {
			return files[left].length < files[right].length
		}
		return files[left].path < files[right].path
	})
}

func sortContentFiles(files []contentFile) {
	sort.Slice(files, func(left int, right int) bool {
		if files[left].path == files[right].path {
			return files[left].length < files[right].length
		}
		return files[left].path < files[right].path
	})
}

func TempTorrentPath(tmpRoot string, meta api.PreparedMetadata, source string) (string, error) {
	contentDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, source)
	if err != nil {
		return "", fmt.Errorf("torrent: tmp dir: %w", err)
	}
	return filepath.Join(contentDir, base+".torrent"), nil
}

func resultFromPath(path string) (api.TorrentResult, error) {
	infoHash, err := loadInfoHash(path)
	if err != nil {
		return api.TorrentResult{}, err
	}
	return api.TorrentResult{Path: path, InfoHash: infoHash}, nil
}

func loadInfoHash(path string) (string, error) {
	meta, err := metainfo.LoadFromFile(path)
	if err != nil {
		return "", fmt.Errorf("torrent: read %q: %w", path, err)
	}
	return meta.HashInfoBytes().String(), nil
}

func hasTracker(trackers []string, targets []string) bool {
	if len(trackers) == 0 || len(targets) == 0 {
		return false
	}
	for _, tracker := range trackers {
		for _, target := range targets {
			if strings.EqualFold(tracker, target) {
				return true
			}
		}
	}
	return false
}
