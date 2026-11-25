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
	"io"
	"io/fs"
	"math"
	"os"
	slashpath "path" //nolint:depguard // Joins torrent-internal slash-delimited metainfo paths.
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filelock"
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
func (s *Service) createSingle(
	ctx context.Context, meta api.TorrentSubject, reportProgress func(status, message string),
) (_ api.TorrentResult, err error) {
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

	failMessage := "Torrent preparation failed"
	defer func() {
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			reportProgress("canceled", "Torrent preparation canceled")
			return
		}
		reportProgress("failed", failMessage)
	}()

	// If user already provided a .torrent file, re-use it directly.
	if strings.EqualFold(filepath.Ext(source), ".torrent") {
		failMessage = "Provided torrent validation failed"
		reportProgress("running", "Validating provided torrent")
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
	reportProgress("running", "Preparing torrent")

	logger.Debugf("torrent: resolving create spec for %s", source)
	spec, err := resolveCreateSpec(ctx, meta, s.tmpRoot, false, logger)
	if err != nil {
		return api.TorrentResult{}, err
	}
	logger.Debugf(
		"torrent: create spec path=%s name=%q include_patterns=%d staged=%t",
		spec.path,
		spec.name,
		len(spec.includePatterns),
		spec.cleanupLock != nil,
	)
	if spec.cleanupLock != nil {
		defer spec.cleanup(ctx, logger)
	}
	if _, err := os.Stat(spec.path); err != nil {
		if os.IsNotExist(err) {
			return api.TorrentResult{}, fmt.Errorf("torrent: path %q: %w", spec.path, internalerrors.ErrNotFound)
		}
		return api.TorrentResult{}, fmt.Errorf("torrent: path %q: %w", spec.path, err)
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
	publication := &generatedTorrentPublication{
		stage:       stagePath,
		destination: outputPath,
		cancelErr:   ctx.Err,
	}
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
		failMessage = "Torrent policy validation failed"
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
	createFromSpec := func(targetSpec createSpec) (*mkbrr.TorrentInfo, error) {
		failMessage = "Torrent creation failed"
		createdInfo, createErr := mkbrr.CreateContext(ctx, mkbrr.CreateOptions{
			Path:             targetSpec.path,
			Name:             targetSpec.name,
			OutputPath:       stagePath,
			IsPrivate:        true,
			TrackerURLs:      trackerURLs,
			MaxPieceLength:   &pieceOptions.maxPieceExp,
			TargetPieceCount: target,
			PieceLengthExp:   pieceOptions.pieceExp,
			IncludePatterns:  targetSpec.includePatterns,
			ProgressCallback: torrentProgressCallback(ctx, meta),
		})
		if cancelErr := ctx.Err(); cancelErr != nil {
			return nil, fmt.Errorf("torrent: canceled after hashing: %w", cancelErr)
		}
		if createErr != nil {
			return nil, fmt.Errorf("torrent: create %q: %w", targetSpec.path, createErr)
		}
		failMessage = "Torrent validation failed"
		if err := validateTorrentContent(createdInfo.Path, meta); err != nil {
			return nil, fmt.Errorf("torrent: validate created torrent %q: %w", createdInfo.Path, err)
		}
		return createdInfo, nil
	}
	info, err := createFromSpec(spec)
	if err != nil && spec.fullDirBypass && ctx.Err() == nil {
		logger.Warnf("torrent: full-directory bypass failed during hashing; retrying with staging err=%s", redaction.RedactValue(err.Error(), nil))
		failMessage = "Torrent preparation failed"
		stagedSpec, stageErr := resolveCreateSpec(ctx, meta, s.tmpRoot, true, logger)
		if stageErr != nil {
			return api.TorrentResult{}, stageErr
		}
		defer stagedSpec.cleanup(ctx, logger)
		info, err = createFromSpec(stagedSpec)
	}
	if err != nil {
		return api.TorrentResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: canceled after validation: %w", err)
	}
	if policy != nil {
		trackerCount := len(normalizedTrackerNames(meta.Trackers))
		logger.Infof("torrent: tracker policy validation tracker=%s state=start decision=validate count=%d", policy.name, trackerCount)
		if err := policy.validateTorrent(info.Path, meta); err != nil {
			logger.Warnf("torrent: tracker policy validation tracker=%s state=completed decision=rejected count=%d", policy.name, trackerCount)
			failMessage = "Torrent policy validation failed"
			return api.TorrentResult{}, fmt.Errorf("torrent: validate created torrent policy %q: %w", info.Path, err)
		}
		logger.Infof("torrent: tracker policy validation tracker=%s state=completed decision=accepted count=%d", policy.name, trackerCount)
	}
	if err := setUploadMetadata(info.Path); err != nil {
		failMessage = "Torrent metadata update failed"
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
			failMessage = "Torrent publication failed"
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
	cancelErr   func() error
}

func (p *generatedTorrentPublication) checkCancellation() error {
	if p.cancelErr != nil {
		if err := p.cancelErr(); err != nil {
			return fmt.Errorf("torrent: canceled before publication: %w", err)
		}
	}
	return nil
}

func (p *generatedTorrentPublication) Publish() (string, error) {
	if p.stage == "" {
		return "", errors.New("torrent: generation staging file is no longer available")
	}
	if err := p.checkCancellation(); err != nil {
		return "", err
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

func emitTorrentProgressForTracker(ctx context.Context, sourcePath string, tracker string, status string, message string) {
	emitTorrentHashProgressForTracker(ctx, sourcePath, tracker, status, message, 0, 0, 0)
}

func progressScopeTracker(meta api.TorrentSubject) string {
	if len(meta.Trackers) == 1 {
		return strings.ToUpper(strings.TrimSpace(meta.Trackers[0]))
	}
	return ""
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
	emitTorrentHashProgressForTracker(ctx, meta.SourcePath, progressScopeTracker(meta), status, message, completed, total, hashRate)
}

func emitTorrentHashProgressForTracker(
	ctx context.Context,
	sourcePath string,
	tracker string,
	status string,
	message string,
	completed int,
	total int,
	hashRate float64,
) {
	api.EmitUploadProgress(ctx, api.UploadProgressUpdate{
		SourcePath:      sourcePath,
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
		retained, err := readRetainedTorrentEntries(os.DirFS(filepath.Dir(tmpTorrentPath)))
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

// readRetainedTorrentEntries tolerates an absent retained directory while
// preserving errors for a non-directory path or an unreadable directory.
func readRetainedTorrentEntries(root fs.FS) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(root, "base-torrents")
	if errors.Is(err, fs.ErrNotExist) {
		// Windows can report a file as missing; only tolerate an absent path
		// or a directory that appeared after the initial empty snapshot.
		if info, statErr := fs.Stat(root, "base-torrents"); errors.Is(statErr, fs.ErrNotExist) || statErr == nil && info.IsDir() {
			err = nil
		}
	}
	return entries, err
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

// findReusableTorrent returns candidate if it satisfies the provided
// tracker policy and source content. Content verification is cached by path.
func findReusableTorrent(
	ctx context.Context,
	candidate string,
	policy *trackerTorrentPolicy,
	meta api.TorrentSubject,
	validationCache map[string]error,
	logger api.Logger,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("torrent: canceled while checking candidates: %w", err)
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", nil
	}
	if err := validateCandidateTorrentPolicy(candidate, policy, meta, logger); err != nil {
		return "", nil
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
	if validationErr != nil {
		return "", nil
	}
	if logger != nil {
		logger.Debugf("torrent: reusable candidate validated path=%s tracker_policy=%t", candidate, policy != nil)
	}
	return candidate, nil
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
		logReusableCandidateRejection(logger, path, "tracker_policy", err)
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
	if err := validateReusablePieceCount(&info, policy, meta); err != nil {
		logReusableCandidateRejection(logger, path, "piece_count", err)
		return err
	}
	return nil
}

func validateCandidateTorrentContent(ctx context.Context, path string, meta api.TorrentSubject, logger api.Logger) error {
	if err := validateTorrentContent(path, meta); err != nil {
		logReusableCandidateRejection(logger, path, "content_layout", err)
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
		logReusableCandidateRejection(logger, path, "piece_hash", err)
		return fmt.Errorf("%w: %w", internalerrors.ErrClientRecheckFailed, err)
	}
	return nil
}

func logReusableCandidateRejection(logger api.Logger, path, stage string, err error) {
	if logger != nil {
		logger.Debugf("torrent: reusable candidate rejected path=%s stage=%s reason=%s", path, stage, redaction.RedactValue(err.Error(), nil))
	}
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
		return fmt.Errorf("torrent: verify candidate data: %w", err)
	}
	if result.BadPieces > 0 || result.MissingPieces > 0 || len(result.MissingFiles) > 0 || result.GoodPieces != result.TotalPieces {
		return fmt.Errorf(
			"torrent: candidate piece mismatch good=%d total=%d bad=%d missing=%d",
			result.GoodPieces,
			result.TotalPieces,
			result.BadPieces,
			result.MissingPieces,
		)
	}
	return nil
}

type createSpec struct {
	path            string
	name            string
	includePatterns []string
	cleanupLock     *os.File
	fullDirBypass   bool
}

func (c *createSpec) cleanup(ctx context.Context, logger api.Logger) {
	lock := c.cleanupLock
	if lock == nil {
		return
	}
	dir := filepath.Dir(lock.Name())
	coordPath := filepath.Join(filepath.Dir(dir), wantedStagingCoordLockName)
	coordLock, err := filelock.AcquireContext(ctx, coordPath)
	if ctx.Err() != nil && coordLock == nil {
		// Clean up immediately if possible. If the lock is held, leave the
		// marker for the next coordinated stale sweep.
		coordLock, err = filelock.Acquire(coordPath)
	}
	_ = lock.Close()
	c.cleanupLock = nil
	if err != nil {
		if logger != nil {
			logger.Warnf("torrent: failed to lock staging coordination path=%s err=%s", dir, redaction.RedactValue(err.Error(), nil))
		}
		return
	}
	defer coordLock.Close()
	if err := removeWantedStagingDir(context.WithoutCancel(ctx), dir); err != nil && logger != nil {
		logger.Warnf("torrent: failed to remove staging path path=%s err=%s", dir, redaction.RedactValue(err.Error(), nil))
	}
}

type contentFile struct {
	path   string
	length int64
}

type sourceContentFile struct {
	contentFile
}

func resolveCreateSpec(
	ctx context.Context,
	meta api.TorrentSubject,
	tmpRoot string,
	forceStaging bool,
	logger api.Logger,
) (createSpec, error) {
	if err := ctx.Err(); err != nil {
		return createSpec{}, fmt.Errorf("context canceled: %w", err)
	}
	source := strings.TrimSpace(meta.SourcePath)
	if source == "" {
		return createSpec{}, internalerrors.ErrInvalidInput
	}
	meta.SourcePath = source
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
			if !forceStaging && wantedMatchesFullDirectory(ctx, source, wanted) {
				return createSpec{
					path:          source,
					name:          filepath.Base(filepath.Clean(source)),
					fullDirBypass: true,
				}, nil
			}
			return stageWantedFiles(ctx, meta, tmpRoot, wanted, logger)
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

var errWantedDirectoryMismatch = errors.New("wanted files do not match full directory")

func wantedMatchesFullDirectory(ctx context.Context, root string, wanted []string) bool {
	if ctx.Err() != nil {
		return false
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	wantedSet := make(map[string]struct{}, len(wanted))
	for _, file := range wanted {
		rel, err := filepath.Rel(cleanRoot, file)
		if err != nil {
			return false
		}
		wantedSet[filepath.ToSlash(rel)] = struct{}{}
	}
	matched := 0
	err = walkNonIgnoredDirEntries(cleanRoot, ctx.Err, func(rel string, info os.FileInfo) error {
		if !info.Mode().IsRegular() {
			return errWantedDirectoryMismatch
		}
		if _, ok := wantedSet[filepath.ToSlash(rel)]; !ok {
			return errWantedDirectoryMismatch
		}
		matched++
		return nil
	})
	return err == nil && matched == len(wantedSet)
}

func walkNonIgnoredDirEntries(root string, checkStop func() error, visit func(rel string, info os.FileInfo) error) error {
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if checkStop != nil {
			if err := checkStop(); err != nil {
				return fmt.Errorf("walk stopped: %w", err)
			}
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return fmt.Errorf("relative path: %w", err)
				}
				if mkbrrIgnoredPath(rel, true) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}
		if mkbrrIgnoredPath(rel, false) {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("file info: %w", err)
		}
		return visit(rel, info)
	}); err != nil {
		return fmt.Errorf("walk directory: %w", err)
	}
	return nil
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
	seen := newSubmissionPathIndex(len(files))
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
		keys := pathutil.ResolvePathMatchKeys(cleanFile)
		if seen.match(keys) != 0 {
			continue
		}
		wanted = append(wanted, cleanFile)
		seen.add(keys, len(wanted))
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

type wantedStageOps struct {
	link    func(oldname, newname string) error
	symlink func(oldname, newname string) error
}

func defaultWantedStageOps() wantedStageOps {
	return wantedStageOps{
		link:    os.Link,
		symlink: os.Symlink,
	}
}

func stageWantedFiles(
	ctx context.Context,
	meta api.TorrentSubject,
	tmpRoot string,
	files []string,
	logger api.Logger,
) (createSpec, error) {
	return stageWantedFilesWithOps(ctx, meta, tmpRoot, files, logger, defaultWantedStageOps())
}

// stageWantedFilesWithOps stages the cleaned absolute paths returned by wantedFilesWithin.
func stageWantedFilesWithOps(
	ctx context.Context,
	meta api.TorrentSubject,
	tmpRoot string,
	files []string,
	logger api.Logger,
	ops wantedStageOps,
) (createSpec, error) {
	if err := ctx.Err(); err != nil {
		return createSpec{}, fmt.Errorf("torrent: canceled before staging wanted files: %w", err)
	}
	if strings.TrimSpace(tmpRoot) == "" {
		return createSpec{}, errors.New("torrent: tmp root is required for staged wanted files")
	}
	root := strings.TrimSpace(meta.SourcePath)
	meta.SourcePath = root
	rootName, err := safeTorrentRootName(root)
	if err != nil {
		return createSpec{}, err
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return createSpec{}, fmt.Errorf("torrent: resolve source root: %w", err)
	}
	releaseRoot, _, err := paths.ReleaseTempDirFor(tmpRoot, root, api.ReleaseInfo{})
	if err != nil {
		return createSpec{}, fmt.Errorf("torrent: resolve staging owner: %w", err)
	}
	stageParent, stageLock, err := createLockedWantedStagingDir(ctx, releaseRoot, cleanRoot, files, logger)
	if err != nil {
		return createSpec{}, err
	}
	spec := createSpec{
		path:        filepath.Join(stageParent, "payload"),
		name:        rootName,
		cleanupLock: stageLock,
	}
	succeeded := false
	defer func() {
		if !succeeded {
			spec.cleanup(ctx, logger)
		}
	}()
	emitTorrentProgress(ctx, meta, "running", "Staging torrent files")
	if logger != nil {
		logger.Debugf("torrent: staging wanted files count=%d dir=%s", len(files), stageParent)
	}
	copyFallbackLogged := false
	for index, absFile := range files {
		if err := ctx.Err(); err != nil {
			return createSpec{}, fmt.Errorf("torrent: canceled while staging wanted files: %w", err)
		}
		rel, err := filepath.Rel(cleanRoot, absFile)
		if err != nil {
			return createSpec{}, fmt.Errorf("torrent: wanted file relative path: %w", err)
		}
		dst := filepath.Join(spec.path, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return createSpec{}, fmt.Errorf("torrent: create wanted file staging parent: %w", err)
		}
		var linkErr error
		info, statErr := os.Lstat(absFile)
		switch {
		case statErr != nil:
			linkErr = fmt.Errorf("stat source before hardlink: %w", statErr)
		case info.Mode()&os.ModeSymlink != 0:
			linkErr = errors.New("skip hardlink for symlink source")
		case runtime.GOOS == "windows" && info.Mode()&0o200 == 0:
			linkErr = errors.New("skip hardlink for read-only source on windows")
		default:
			linkErr = ops.link(absFile, dst)
		}
		if linkErr == nil {
			continue
		}
		if errors.Is(linkErr, os.ErrExist) {
			return createSpec{}, fmt.Errorf("torrent: stage wanted file %q: destination collision: %w", absFile, linkErr)
		}
		symlinkErr := ops.symlink(absFile, dst)
		if symlinkErr == nil {
			continue
		}
		if errors.Is(symlinkErr, os.ErrExist) {
			return createSpec{}, fmt.Errorf("torrent: stage wanted file %q: destination collision: %w", absFile, symlinkErr)
		}
		if !copyFallbackLogged && logger != nil {
			logger.Warnf(
				"torrent: staging wanted files via copy fallback count=%d link_err=%s symlink_err=%s",
				len(files),
				redaction.RedactValue(linkErr.Error(), nil),
				redaction.RedactValue(symlinkErr.Error(), nil),
			)
			copyFallbackLogged = true
		}
		emitTorrentProgress(ctx, meta, "running", fmt.Sprintf("Staging torrent files (%d/%d)", index+1, len(files)))
		if copyErr := copyFileContext(ctx, absFile, dst); copyErr != nil {
			return createSpec{}, fmt.Errorf("torrent: stage wanted file %q: link: %w; symlink: %w; copy: %w", absFile, linkErr, symlinkErr, copyErr)
		}
	}
	succeeded = true
	return spec, nil
}

const (
	wantedStagingLockName      = ".staging.lock"
	wantedStagingCoordLockName = ".wanted-staging.lock"
)

func createLockedWantedStagingDir(ctx context.Context, releaseRoot string, cleanSource string, wanted []string, logger api.Logger) (string, *os.File, error) {
	// Each open handle contends on the same on-disk lock, including handles
	// owned by this process or opened through another spelling of releaseRoot.
	coordLock, err := filelock.AcquireContext(ctx, filepath.Join(releaseRoot, wantedStagingCoordLockName))
	if err != nil {
		return "", nil, fmt.Errorf("torrent: lock wanted file staging coordination: %w", err)
	}
	defer coordLock.Close()

	cleanupStaleWantedStagingDirs(ctx, releaseRoot, cleanSource, wanted, logger)
	if err := ctx.Err(); err != nil {
		return "", nil, fmt.Errorf("torrent: canceled while preparing wanted file staging: %w", err)
	}
	stageParent, err := os.MkdirTemp(releaseRoot, "wanted-files-*")
	if err != nil {
		return "", nil, fmt.Errorf("torrent: create wanted file staging dir: %w", err)
	}
	lock, err := filelock.Acquire(filepath.Join(stageParent, wantedStagingLockName))
	if err != nil {
		// The directory is still empty and under the coordination lock.
		_ = removeWantedStagingDir(context.WithoutCancel(ctx), stageParent)
		return "", nil, fmt.Errorf("torrent: lock wanted file staging dir: %w", err)
	}
	return stageParent, lock, nil
}

func cleanupStaleWantedStagingDirs(ctx context.Context, releaseRoot string, cleanSource string, wanted []string, logger api.Logger) {
	if ctx.Err() != nil || pathutil.PathsOverlap(releaseRoot, cleanSource) {
		return
	}
	entries, err := os.ReadDir(releaseRoot)
	if err != nil {
		return
	}
	var protected []os.FileInfo
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "wanted-files-") {
			continue
		}
		dir := filepath.Join(releaseRoot, entry.Name())
		lockPath := filepath.Join(dir, wantedStagingLockName)
		lockInfo, statErr := os.Lstat(lockPath)
		if statErr != nil || !lockInfo.Mode().IsRegular() {
			continue
		}
		lock, err := filelock.Acquire(lockPath)
		if err != nil {
			continue
		}
		_ = lock.Close()
		if protected == nil {
			protected = stagingSourceDirectories(ctx, cleanSource, wanted)
			if protected == nil {
				return
			}
		}
		if !staleStagingTreeDisjoint(ctx, dir, cleanSource, protected) {
			continue
		}
		if removeErr := removeWantedStagingDir(ctx, dir); removeErr != nil && logger != nil && ctx.Err() == nil {
			logger.Warnf("torrent: failed to remove stale staging path path=%s err=%s", dir, redaction.RedactValue(removeErr.Error(), nil))
		}
	}
}

// removeWantedStagingDir removes staged payload entries before removing
// .staging.lock so a partial cleanup failure never strands an unmarked
// wanted-files-* directory that future stale sweeps would skip.
func removeWantedStagingDir(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("remove wanted staging dir: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read staging dir %q: %w", dir, err)
	}
	var removeErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return errors.Join(removeErr, err)
		}
		if entry.Name() == wantedStagingLockName {
			continue
		}
		if err := removeStagedPayload(ctx, filepath.Join(dir, entry.Name())); err != nil {
			removeErr = errors.Join(removeErr, err)
		}
	}
	if removeErr != nil {
		return removeErr
	}
	lockPath := filepath.Join(dir, wantedStagingLockName)
	if err := removeStagedEntry(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staging lock %q: %w", lockPath, err)
	}
	if err := removeStagedEntry(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staging dir %q: %w", dir, err)
	}
	return nil
}

func copyFileContext(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("copy canceled: %w", err)
	}
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create destination file: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = destFile.Close()
			_ = os.Remove(dst)
		}
	}()

	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("copy canceled: %w", err)
		}
		n, readErr := sourceFile.Read(buf)
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("copy canceled: %w", err)
			}
			if _, writeErr := destFile.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("copy content: %w", writeErr)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("copy content: %w", readErr)
		}
	}
	if err := destFile.Close(); err != nil {
		return fmt.Errorf("close destination file: %w", err)
	}
	succeeded = true
	return nil
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
	if err != nil || !ok {
		return err
	}
	expectedName := filepath.Base(filepath.Clean(strings.TrimSpace(meta.SourcePath)))
	if scope == api.SubmissionContentScopeSingleFile {
		expectedName = expectedFiles[0].path
	}
	if info.BestName() != expectedName {
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
	if !sameContentSet(actual, sourceContentPaths(expectedFiles)) {
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
	err = walkNonIgnoredDirEntries(root, nil, func(rel string, info os.FileInfo) error {
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
