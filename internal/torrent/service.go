// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package torrent validates reusable torrent metadata or creates private
// torrent artifacts for prepared source layouts.
package torrent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	slashpath "path" //nolint:depguard // Joins torrent-internal slash-delimited metainfo paths.
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/logging"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/redaction"
	torrentmeta "github.com/autobrr/upbrr/internal/torrent/metainfo"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Service selects or creates torrent artifacts under a configured temporary
// root. Callers must not mutate its registry concurrently with use.
type Service struct {
	logger   api.Logger
	tmpRoot  string
	registry *trackers.Registry
}

// NewService returns a service with legacy built-in PTP policy fallback.
// A nil logger is replaced with [api.NopLogger].
func NewService(logger api.Logger, tmpRoot string) *Service {
	return NewServiceWithRegistry(logger, tmpRoot, nil)
}

// NewServiceWithRegistry returns a service that combines registered tracker
// artifact policies. A nil registry leaves only legacy policy fallback
// available; tmpRoot is trimmed and may be empty until an operation needs a
// release-specific temporary path.
func NewServiceWithRegistry(logger api.Logger, tmpRoot string, registry *trackers.Registry) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Service{
		logger:   logger,
		tmpRoot:  strings.TrimSpace(tmpRoot),
		registry: registry,
	}
}

// createSingle generates one artifact under the supplied tracker policy. It also
// validates direct .torrent inputs, for which there is no local media to hash.
func (s *Service) createSingle(ctx context.Context, meta api.TorrentSubject) (api.TorrentResult, error) {
	logger := logging.FromContext(ctx, s.logger)

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

	logger.Debugf("torrent: preparing for %s", source)
	forceRehash := torrentOverrideEnabled(meta.TorrentOverrides.Rehash)
	reuseOnly := torrentOverrideEnabled(meta.TorrentOverrides.NoHash)
	if forceRehash && reuseOnly {
		logger.Debugf("torrent: ignoring nohash because rehash is enabled for %s", source)
		reuseOnly = false
	}
	logger.Debugf("torrent: reuse decision=scan source=%s force_rehash=%t reuse_only=%t", source, forceRehash, reuseOnly)
	emitTorrentProgress(ctx, meta, "running", "Checking reusable torrent")

	// If user already provided a .torrent file, re-use it directly.
	if strings.EqualFold(filepath.Ext(source), ".torrent") {
		info, err := os.Stat(source)
		if err != nil {
			return api.TorrentResult{}, fmt.Errorf("torrent: path %q: %w", source, err)
		}
		if info.IsDir() {
			return api.TorrentResult{}, internalerrors.ErrInvalidInput
		}
		if err := validateCandidateTorrent(ctx, source, resolveTrackerPolicy(meta, s.registry), meta, logger); err != nil {
			return api.TorrentResult{}, fmt.Errorf("torrent: provided torrent %q: %w", source, err)
		}
		logger.Debugf("torrent: using provided torrent %s", source)
		return resultFromExistingTorrent(ctx, meta, source, "Using provided torrent")
	}

	policy := resolveTrackerPolicy(meta, s.registry)

	logger.Debugf("torrent: resolving create spec for %s", source)
	createSpec, err := resolveCreateSpec(meta, source, s.tmpRoot)
	if err != nil {
		return api.TorrentResult{}, err
	}
	logger.Debugf(
		"torrent: create spec path=%s name=%q include_patterns=%d staged=%t",
		createSpec.path,
		createSpec.name,
		len(createSpec.includePatterns),
		createSpec.cleanupPath != "",
	)
	if createSpec.cleanupPath != "" {
		defer func() {
			if err := os.RemoveAll(createSpec.cleanupPath); err != nil {
				logger.Warnf("torrent: failed to remove staging path path=%s err=%s", createSpec.cleanupPath, redaction.RedactValue(err.Error(), nil))
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
	outputPath, err := TempTorrentPath(s.tmpRoot, source)
	if err != nil {
		return api.TorrentResult{}, err
	}
	staged, err := os.CreateTemp(filepath.Dir(outputPath), ".generating-*.torrent")
	if err != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: stage generated artifact: %w", err)
	}
	stagePath := staged.Name()
	if err := staged.Close(); err != nil {
		_ = os.Remove(stagePath)
		return api.TorrentResult{}, fmt.Errorf("torrent: close generation staging file: %w", err)
	}
	publication := &generatedTorrentPublication{stage: stagePath, destination: outputPath}
	owned := true
	defer func() {
		if owned {
			if err := publication.Discard(); err != nil {
				logger.Warnf("torrent: failed to discard generation staging file err=%s", redaction.RedactValue(err.Error(), nil))
			}
		}
	}()
	pieceOptions := policy.createOptions(meta)
	if err := policy.validateCreateOptions(meta, pieceOptions); err != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: resolve tracker piece size: %w", err)
	}
	logger.Infof("torrent: creating torrent output=%s max_piece_exp=%d piece_exp_set=%t", outputPath, pieceOptions.maxPieceExp, pieceOptions.pieceExp != nil)
	reason := "Hashing torrent: no compatible reusable artifact"
	if forceRehash {
		reason = "Hashing torrent: rehash requested"
	}
	emitTorrentProgress(ctx, meta, "running", reason)

	trackerURLs := []string(nil)
	if pieceOptions.profileURL != "" {
		trackerURLs = []string{pieceOptions.profileURL}
	}
	if cancelErr := ctx.Err(); cancelErr != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: canceled before hashing: %w", cancelErr)
	}
	var target *uint
	if pieceOptions.pieceExp == nil {
		target = new(uint(1000))
	}
	info, err := mkbrr.CreateContext(ctx, mkbrr.CreateOptions{
		Path:             createSpec.path,
		Name:             createSpec.name,
		OutputPath:       stagePath,
		IsPrivate:        true,
		TrackerURLs:      trackerURLs,
		MaxPieceLength:   &pieceOptions.maxPieceExp,
		TargetPieceCount: target,
		PieceLengthExp:   pieceOptions.pieceExp,
		IncludePatterns:  createSpec.includePatterns,
		ProgressCallback: torrentProgressCallback(ctx, meta),
	})
	if cancelErr := ctx.Err(); cancelErr != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: canceled after hashing: %w", cancelErr)
	}
	if err != nil {
		emitTorrentProgress(ctx, meta, "failed", "Torrent creation failed")
		return api.TorrentResult{}, fmt.Errorf("torrent: create %q: %w", createSpec.path, err)
	}
	if err := validateTorrentContent(info.Path, meta); err != nil {
		emitTorrentProgress(ctx, meta, "failed", "Torrent validation failed")
		return api.TorrentResult{}, fmt.Errorf("torrent: validate created torrent %q: %w", info.Path, err)
	}
	if policy != nil {
		trackerCount := len(normalizedTrackerNames(meta.Trackers))
		logger.Infof("torrent: tracker policy validation tracker=%s state=start decision=validate count=%d", policy.name, trackerCount)
		if err := policy.validateTorrent(info.Path, meta); err != nil {
			logger.Warnf("torrent: tracker policy validation tracker=%s state=completed decision=rejected count=%d", policy.name, trackerCount)
			emitTorrentProgress(ctx, meta, "failed", "Torrent policy validation failed")
			return api.TorrentResult{}, fmt.Errorf("torrent: validate created torrent policy %q: %w", info.Path, err)
		}
		logger.Infof("torrent: tracker policy validation tracker=%s state=completed decision=accepted count=%d", policy.name, trackerCount)
	}
	if err := setUploadMetadata(info.Path); err != nil {
		emitTorrentProgress(ctx, meta, "failed", "Torrent metadata update failed")
		return api.TorrentResult{}, err
	}
	if cancelErr := ctx.Err(); cancelErr != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: canceled before publication: %w", cancelErr)
	}
	resultPath := info.Path
	var pending api.TorrentPublication
	if meta.DeferGenerationPublication {
		pending = publication
	} else {
		resultPath, err = publication.Publish()
		if err != nil {
			return api.TorrentResult{}, err
		}
	}
	owned = false
	if pending != nil {
		emitTorrentProgress(ctx, meta, "running", "Checking source before publishing torrent")
	} else {
		emitTorrentProgress(ctx, meta, "completed", "Torrent ready")
	}
	logger.Infof("torrent: created torrent %s", resultPath)

	return api.TorrentResult{
		Path:               resultPath,
		InfoHash:           info.InfoHash,
		PendingPublication: pending,
		RehashedTrackers:   normalizedTrackerNames(meta.Trackers),
	}, nil
}

func setUploadMetadata(path string) error {
	torrentMeta, err := metainfo.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("torrent: load created torrent metadata: %w", err)
	}
	torrentMeta.CreatedBy = torrentmeta.MkbrrUploadCreatedBy
	torrentMeta.Comment = torrentmeta.UploadComment
	torrentMeta.Announce = ""
	torrentMeta.AnnounceList = nil
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

type generatedTorrentPublication struct {
	stage       string
	destination string
}

func (p *generatedTorrentPublication) Publish() (string, error) {
	if p.stage == "" {
		return "", errors.New("torrent: generation staging file is no longer available")
	}
	if err := os.Rename(p.stage, p.destination); err != nil {
		return "", fmt.Errorf("torrent: publish generated artifact: %w", err)
	}
	p.stage = ""
	return p.destination, nil
}

func (p *generatedTorrentPublication) Discard() error {
	if p.stage == "" {
		return nil
	}
	if err := os.Remove(p.stage); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("torrent: discard generated artifact: %w", err)
	}
	p.stage = ""
	return nil
}

func resultFromExistingTorrent(ctx context.Context, meta api.TorrentSubject, path string, message string) (api.TorrentResult, error) {
	if err := ctx.Err(); err != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: canceled before reuse: %w", err)
	}
	result, err := resultFromPath(path)
	if err != nil {
		return api.TorrentResult{}, err
	}
	emitTorrentProgress(ctx, meta, "completed", message)
	return result, nil
}

func emitTorrentProgress(ctx context.Context, meta api.TorrentSubject, status string, message string) {
	emitTorrentHashProgress(ctx, meta, status, message, 0, 0, 0)
}

func torrentProgressCallback(ctx context.Context, meta api.TorrentSubject) mkbrr.ProgressCallback {
	return func(completed, total int, hashRate float64) {
		if total <= 0 {
			emitTorrentHashProgress(ctx, meta, "running", "Preparing torrent pieces", completed, total, hashRate)
			return
		}
		message := fmt.Sprintf("Hashing pieces... %d%% (%d/%d pieces)", progressPercent(completed, total), completed, total)
		if hashRate > 0 {
			message = fmt.Sprintf("Hashing pieces... [%.0f MiB/s] %d%% (%d/%d pieces)", hashRate, progressPercent(completed, total), completed, total)
		}
		if completed >= total {
			message = "Hashing complete"
		}
		emitTorrentHashProgress(ctx, meta, "running", message, completed, total, hashRate)
	}
}

func emitTorrentHashProgress(ctx context.Context, meta api.TorrentSubject, status string, message string, completed int, total int, hashRate float64) {
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

// reusableTorrentCandidates returns unique candidate paths in reuse priority
// order: client-provided, managed temporary, then source-adjacent.
func reusableTorrentCandidates(tmpRoot string, meta api.TorrentSubject) ([]string, error) {
	candidates := make([]string, 0, 3)
	candidates = appendUniqueTorrentCandidate(candidates, meta.ClientTorrentPath)
	for _, candidate := range meta.ClientTorrentCandidates {
		candidates = appendUniqueTorrentCandidate(candidates, candidate.Path)
	}
	if strings.TrimSpace(tmpRoot) != "" {
		tmpTorrentPath, err := TempTorrentPath(tmpRoot, meta.SourcePath)
		if err != nil {
			return nil, err
		}
		candidates = appendUniqueTorrentCandidate(candidates, tmpTorrentPath)
		retainedDir := filepath.Join(filepath.Dir(tmpTorrentPath), "base-torrents")
		retained, err := os.ReadDir(retainedDir)
		if errors.Is(err, os.ErrNotExist) {
			// Windows can report a regular file as missing during directory reads.
			if _, statErr := os.Stat(retainedDir); errors.Is(statErr, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil {
			return nil, fmt.Errorf("torrent: list retained bases: %w", err)
		}
		for _, candidate := range retained {
			if !candidate.IsDir() && strings.HasSuffix(candidate.Name(), ".torrent") {
				candidates = appendUniqueTorrentCandidate(candidates, filepath.Join(retainedDir, candidate.Name()))
			}
		}
	}
	candidates = appendUniqueTorrentCandidate(candidates, meta.SourcePath+".torrent")
	baseName := filepath.Base(meta.SourcePath)
	if baseName != "" {
		candidates = appendUniqueTorrentCandidate(candidates, filepath.Join(filepath.Dir(meta.SourcePath), baseName+".torrent"))
	}
	return candidates, nil
}

func appendUniqueTorrentCandidate(candidates []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return candidates
	}
	for _, existing := range candidates {
		if strings.EqualFold(filepath.Clean(existing), filepath.Clean(candidate)) {
			return candidates
		}
	}
	return append(candidates, candidate)
}

// findReusableTorrent returns the first candidate that satisfies the provided
// tracker policy and source content. Content verification is cached by path.
func findReusableTorrent(
	ctx context.Context,
	candidates []string,
	policy *trackerTorrentPolicy,
	meta api.TorrentSubject,
	validationCache map[string]error,
	logger api.Logger,
) (string, error) {
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("torrent: canceled while checking candidates: %w", err)
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if err := validateCandidateTorrentPolicy(candidate, policy, meta, logger); err != nil {
			continue
		}
		validationErr, checked := validationCache[candidate]
		if !checked {
			validationErr = validateCandidateTorrentContent(ctx, candidate, meta, logger)
			if err := ctx.Err(); err != nil {
				return "", fmt.Errorf("torrent: canceled after checking candidate: %w", err)
			}
			validationCache[candidate] = validationErr
			if errors.Is(validationErr, internalerrors.ErrClientRecheckFailed) {
				return "", validationErr
			}
		}
		if validationErr == nil {
			if logger != nil {
				logger.Debugf("torrent: reusable candidate validated path=%s tracker_policy=%t", candidate, policy != nil)
			}
			return candidate, nil
		}
	}
	return "", nil
}

func normalizedTrackerNames(trackers []string) []string {
	result := make([]string, 0, len(trackers))
	seen := make(map[string]struct{}, len(trackers))
	for _, tracker := range trackers {
		tracker = strings.ToUpper(strings.TrimSpace(tracker))
		if tracker == "" {
			continue
		}
		if _, ok := seen[tracker]; ok {
			continue
		}
		seen[tracker] = struct{}{}
		result = append(result, tracker)
	}
	return result
}

// validateCandidateTorrent checks an existing torrent against the active
// tracker policy and prepared source layout. Expected candidate rejection is
// logged at debug level so discovery can continue without operator warnings.
func validateCandidateTorrent(ctx context.Context, path string, policy *trackerTorrentPolicy, meta api.TorrentSubject, logger api.Logger) error {
	if err := validateCandidateTorrentPolicy(path, policy, meta, logger); err != nil {
		return err
	}
	return validateCandidateTorrentContent(ctx, path, meta, logger)
}

func validateCandidateTorrentPolicy(path string, policy *trackerTorrentPolicy, meta api.TorrentSubject, logger api.Logger) error {
	if err := policy.validateTorrent(path, meta); err != nil {
		if logger != nil {
			logger.Debugf("torrent: reusable candidate rejected path=%s stage=tracker_policy reason=%s", path, redaction.RedactValue(err.Error(), nil))
		}
		return err
	}
	mi, err := metainfo.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("load reusable torrent: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("decode reusable torrent: %w", err)
	}
	return validateReusablePieceCount(&info, policy, meta)
}

func validateCandidateTorrentContent(ctx context.Context, path string, meta api.TorrentSubject, logger api.Logger) error {
	if err := validateTorrentContent(path, meta); err != nil {
		if logger != nil {
			logger.Debugf("torrent: reusable candidate rejected path=%s stage=content_layout reason=%s", path, redaction.RedactValue(err.Error(), nil))
		}
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("torrent: read client artifact: %w", err)
	}
	if err := validateClientCandidateBytes(path, data, meta); err != nil {
		return err
	}
	if !meta.ForceRecheck {
		return nil
	}
	if err := verifyCandidateTorrentData(ctx, path, meta); err != nil {
		if logger != nil {
			logger.Debugf("torrent: reusable candidate rejected path=%s stage=piece_hash reason=%s", path, redaction.RedactValue(err.Error(), nil))
		}
		return fmt.Errorf("%w: %w", internalerrors.ErrClientRecheckFailed, err)
	}
	return nil
}

func validateClientCandidateBytes(path string, data []byte, meta api.TorrentSubject) error {
	expectedHash := ""
	if pathutil.SamePath(path, meta.ClientTorrentPath) {
		expectedHash = strings.TrimSpace(meta.ClientTorrentInfoHash)
	}
	for _, candidate := range meta.ClientTorrentCandidates {
		if pathutil.SamePath(path, candidate.Path) {
			expectedHash = strings.TrimSpace(candidate.InfoHash)
			if expectedHash == "" {
				return errors.New("torrent: client artifact infohash missing")
			}
			if candidate.ContentSHA256 != "" {
				if !strings.EqualFold(fmt.Sprintf("%x", sha256.Sum256(data)), candidate.ContentSHA256) {
					return errors.New("torrent: client artifact bytes changed")
				}
			}
			break
		}
	}
	if expectedHash != "" {
		candidate, err := metainfo.Load(bytes.NewReader(data))
		if err != nil || !strings.EqualFold(candidate.HashInfoBytes().String(), expectedHash) {
			return errors.New("torrent: client artifact infohash changed")
		}
	}
	return nil
}

func verifyCandidateTorrentData(ctx context.Context, path string, meta api.TorrentSubject) error {
	contentPath := strings.TrimSpace(meta.SourcePath)
	if contentPath == "" || strings.EqualFold(filepath.Ext(contentPath), ".torrent") {
		return errors.New("torrent: local source data is required for recheck")
	}
	if strings.TrimSpace(meta.DiscType) != "" {
		contentPath = normalizeDiscSource(contentPath)
	}
	inventory, ok, err := ResolveSubmissionContentInventory(meta)
	if err != nil {
		return err
	}
	if ok && inventory.Scope == api.SubmissionContentScopeSingleFile && len(inventory.Files) == 1 {
		contentPath = inventory.Files[0].LocalPath
	}
	if cancelErr := ctx.Err(); cancelErr != nil {
		return fmt.Errorf("torrent: canceled before verification: %w", cancelErr)
	}
	result, err := mkbrr.VerifyDataContext(ctx, mkbrr.VerifyOptions{
		TorrentPath: path,
		ContentPath: contentPath,
		Quiet:       true,
		ProgressCallback: func(completed, total int, rate float64) {
			emitTorrentHashProgress(ctx, meta, "running", "Verifying torrent pieces", completed, total, rate)
		},
	})
	if cancelErr := ctx.Err(); cancelErr != nil {
		return fmt.Errorf("torrent: canceled after verification: %w", cancelErr)
	}
	if err != nil {
		emitTorrentProgress(ctx, meta, "failed", "Torrent piece verification failed")
		return fmt.Errorf("torrent: verify candidate data: %w", err)
	}
	if result.BadPieces > 0 || result.MissingPieces > 0 || len(result.MissingFiles) > 0 || result.GoodPieces != result.TotalPieces {
		emitTorrentProgress(ctx, meta, "failed", "Torrent piece verification failed")
		return fmt.Errorf(
			"torrent: candidate piece mismatch good=%d total=%d bad=%d missing=%d",
			result.GoodPieces,
			result.TotalPieces,
			result.BadPieces,
			result.MissingPieces,
		)
	}
	emitTorrentProgress(ctx, meta, "completed", "Torrent pieces verified")
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

func resolveCreateSpec(meta api.TorrentSubject, source string, tmpRoot string) (createSpec, error) {
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
	if len(wanted) == 1 && !meta.KeepFolder {
		return createSpec{path: wanted[0]}, nil
	}
	if len(wanted) > 0 {
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
		if slices.ContainsFunc(wanted, func(path string) bool { return pathutil.SamePath(path, cleanFile) }) {
			continue
		}
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
	releaseRoot, _, err := paths.ReleaseTempDirFor(tmpRoot, root, api.ReleaseInfo{})
	if err != nil {
		return "", "", fmt.Errorf("torrent: resolve staging owner: %w", err)
	}
	stageParent, err := os.MkdirTemp(releaseRoot, "wanted-files-*")
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

func validateTorrentContent(path string, meta api.TorrentSubject) error {
	torrentMeta, err := metainfo.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("torrent: read candidate metadata: %w", err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("torrent: unmarshal candidate info: %w", err)
	}
	return ValidateTorrentLayout(info, meta)
}

// ValidateTorrentLayout checks piece metadata and the exact requested source layout.
func ValidateTorrentLayout(info metainfo.Info, meta api.TorrentSubject) error {
	if err := ValidatePieceMetadata(info); err != nil {
		return err
	}
	expectedFiles, scope, ok, err := expectedTorrentFiles(meta)
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
	if nameOK && info.BestName() != expectedName {
		return errors.New("torrent: candidate name mismatch")
	}
	if (scope == api.SubmissionContentScopeSingleFile && len(info.Files) != 0) ||
		(scope == api.SubmissionContentScopeFileSet && len(info.Files) == 0) {
		return errors.New("torrent: candidate folder layout mismatch")
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

// ValidatePieceMetadata checks that a v1 piece table spans safe declared files.
func ValidatePieceMetadata(info metainfo.Info) error {
	if !info.HasV1() || info.HasV2() || info.PieceLength <= 0 || len(info.Pieces)%metainfo.HashSize != 0 {
		return errors.New("torrent: unsupported or invalid piece metadata")
	}
	if !safeTorrentComponent(info.Name) {
		return errors.New("torrent: unsafe name metadata")
	}
	if info.NameUtf8 != "" && info.NameUtf8 != info.Name {
		return errors.New("torrent: ambiguous name metadata")
	}
	length := info.Length
	if len(info.Files) > 0 {
		length = 0
		for _, file := range info.Files {
			if file.Length < 0 || length > math.MaxInt64-file.Length || len(file.Path) == 0 ||
				(len(file.PathUtf8) > 0 && !slices.Equal(file.Path, file.PathUtf8)) {
				return errors.New("torrent: invalid file metadata")
			}
			for _, component := range file.Path {
				if !safeTorrentComponent(component) {
					return errors.New("torrent: unsafe file path metadata")
				}
			}
			length += file.Length
		}
	}
	if length < 0 || (length == 0 && len(info.Pieces) != 0) ||
		(length > 0 && int64(len(info.Pieces)/metainfo.HashSize) != 1+(length-1)/info.PieceLength) {
		return errors.New("torrent: incomplete piece metadata")
	}
	return nil
}

func safeTorrentComponent(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\:\x00")
}

func expectedTorrentFiles(meta api.TorrentSubject) ([]sourceContentFile, api.SubmissionContentScope, bool, error) {
	inventory, ok, err := ResolveSubmissionContentInventory(meta)
	if err != nil || !ok {
		return nil, "", ok, err
	}
	expected := make([]sourceContentFile, 0, len(inventory.Files))
	for _, file := range inventory.Files {
		expected = append(expected, sourceContentFile{path: file.TorrentPath, length: file.Size})
	}
	return expected, inventory.Scope, true, nil
}

func expectedTorrentName(meta api.TorrentSubject) (string, bool, error) {
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
	if len(wanted) == 1 && !meta.KeepFolder {
		return filepath.Base(wanted[0]), true, nil
	}
	return filepath.Base(filepath.Clean(source)), true, nil
}

func torrentContentPaths(info metainfo.Info) []contentFile {
	if !info.HasV1() {
		return nil
	}
	files := info.Files
	if len(files) == 0 {
		files = []metainfo.FileInfo{{Length: info.Length}}
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
			path: filepath.Base(root), length: info.Size(),
		}}, nil
	}
	paths := make([]sourceContentFile, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
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
			return fmt.Errorf("disc file info: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		paths = append(paths, sourceContentFile{
			path: filepath.ToSlash(rel), length: info.Size(),
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

// TempTorrentPath creates the release-specific directory below tmpRoot with
// mode 0700 when missing and returns its deterministic .torrent path.
func TempTorrentPath(tmpRoot string, source string) (string, error) {
	contentDir, base, err := paths.ReleaseTempDirFor(tmpRoot, source, api.ReleaseInfo{})
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
