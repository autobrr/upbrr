// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/autobrr/go-torrent/metainfo"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Create retains compatible base variants and assigns an exact artifact to each
// selected tracker. Existing pieces are preferred; NoHash never generates missing
// pieces. Selection prefers 1,000–2,000 pieces within each tracker's hard limits.
// Only missing required artifacts are generated; no optional variants are made.
func (s *Service) Create(ctx context.Context, meta api.TorrentSubject) (_ api.TorrentResult, err error) {
	if err := ctx.Err(); err != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: create canceled: %w", err)
	}
	meta.SourcePath = strings.TrimSpace(meta.SourcePath)
	if meta.SourcePath == "" {
		return api.TorrentResult{}, internalerrors.ErrInvalidInput
	}
	// Direct metainfo input has no local media from which to make another variant.
	if strings.EqualFold(filepath.Ext(meta.SourcePath), ".torrent") {
		return s.createSingle(ctx, meta, func(status, message string) {
			emitTorrentProgress(ctx, meta, status, message)
		})
	}
	logger := logging.FromContext(ctx, s.logger)
	rehash := torrentOverrideEnabled(meta.TorrentOverrides.Rehash)
	nohash := torrentOverrideEnabled(meta.TorrentOverrides.NoHash) && !rehash
	extractionCandidates, err := reusableTorrentCandidates(s.tmpRoot, meta)
	if err != nil {
		return api.TorrentResult{}, err
	}
	cache := make(map[string]error)
	if rehash {
		extractionCandidates = nil
	}
	// Layout, captured-byte identity, and requested recheck are evaluated once per
	// candidate; stale alternatives must not discard a verified reusable base.
	// A failed explicit recheck remains fatal when no verified candidate survives.
	extractionCandidates = slices.DeleteFunc(extractionCandidates, func(candidate string) bool {
		info, statErr := os.Stat(candidate)
		return statErr != nil || info.IsDir()
	})
	candidates := make([]string, 0, len(extractionCandidates))
	var recheckErr error
	openScopes := make([]string, 0, len(meta.Trackers)+1)
	failMessage := "Torrent preparation failed"
	openScope := func(tracker string) {
		if !slices.Contains(openScopes, tracker) {
			openScopes = append(openScopes, tracker)
		}
	}
	closeScope := func(tracker string, status string, message string) {
		index := slices.Index(openScopes, tracker)
		if index < 0 {
			return
		}
		openScopes = slices.Delete(openScopes, index, index+1)
		emitTorrentProgressForTracker(ctx, meta.SourcePath, tracker, status, message)
	}
	closeAllScopes := func(status string, message string) {
		for _, tracker := range openScopes {
			emitTorrentProgressForTracker(ctx, meta.SourcePath, tracker, status, message)
		}
		openScopes = openScopes[:0]
	}
	defer func() {
		if len(openScopes) == 0 {
			return
		}
		if ctx.Err() != nil {
			closeAllScopes("canceled", "Torrent preparation canceled")
			return
		}
		if meta.DeferWrapperConversion && errors.Is(err, internalerrors.ErrNotFound) {
			// The caller owns the open scopes across discovery and retry.
			return
		}
		closeAllScopes("failed", failMessage)
	}()
	if len(extractionCandidates) > 0 {
		emitTorrentProgress(ctx, meta, "running", "Checking reusable torrent")
		openScope(progressScopeTracker(meta))
	}
	for _, candidate := range extractionCandidates {
		if err := ctx.Err(); err != nil {
			return api.TorrentResult{}, fmt.Errorf("torrent: canceled while checking candidates: %w", err)
		}
		err := validateCandidateTorrentContent(ctx, candidate, meta, logger)
		cache[candidate] = err
		if errors.Is(err, internalerrors.ErrClientRecheckFailed) {
			recheckErr = err
			continue
		}
		if err == nil {
			candidates = append(candidates, candidate)
		}
	}
	result := api.TorrentResult{TrackerArtifacts: make(map[string]api.TorrentArtifact)}
	closeResultScopes := func(pendingScopes []string) {
		if len(openScopes) == 0 && len(pendingScopes) == 0 {
			openScope(progressScopeTracker(meta))
		}
		for _, tracker := range openScopes {
			if slices.Contains(pendingScopes, tracker) {
				continue
			}
			status, message := PreparationProgressOutcome(result, tracker)
			emitTorrentProgressForTracker(ctx, meta.SourcePath, tracker, status, message)
		}
		openScopes = openScopes[:0]
	}
	publications := &variantPublication{}
	defer func() { _ = publications.Discard() }()
	generated := make(map[string]bool)
	selected := make(map[string]string)
	extracted := make(map[string]bool)
	findReusable := func(path string, policy *trackerTorrentPolicy, subject api.TorrentSubject) (string, error) {
		if _, checked := cache[path]; subject.ForceRecheck && !checked {
			openScope(progressScopeTracker(subject))
		}
		return findReusableTorrent(ctx, path, policy, subject, cache, logger)
	}
	choose := func(subject api.TorrentSubject) (string, error) {
		policy := resolveTrackerPolicy(subject, s.registry)
		allowHash := !nohash && !hasTracker(meta.SkipIfRehashTrackers, subject.Trackers)
		type choice struct {
			path       string
			pieces     int64
			extraction *preparedSingleFileExtraction
			hashTail   bool
		}
		choices := make([]choice, 0, len(candidates)+len(extractionCandidates))
		for _, path := range candidates {
			if !allowHash && generated[path] {
				continue
			}
			if validateCandidateTorrentPolicy(path, policy, subject, logger) != nil {
				continue
			}
			mi, err := metainfo.LoadFromFile(path)
			if err != nil {
				continue
			}
			info, err := mi.UnmarshalInfo()
			if err == nil {
				choices = append(choices, choice{path: path, pieces: int64(info.NumPieces())})
			}
		}
		// A successful probe still ranks known metadata-only conversions against
		// its usable artifacts; missing lanes retain the exact-layout search retry.
		// A compatible full-disc choice already validates the inventory and cannot
		// be converted to a single file. With no choice, retain source errors
		// discovered by the extraction probe.
		considerExtraction := strings.TrimSpace(subject.DiscType) == "" || len(choices) == 0
		if s.tmpRoot != "" && considerExtraction && (!meta.DeferWrapperConversion || len(choices) > 0) {
			for _, path := range extractionCandidates {
				if extracted[path] {
					continue
				}
				prepared, err := prepareSingleFileExtraction(path, subject, policy, logger)
				if err != nil {
					return "", err
				}
				if prepared == nil || prepared.plan.tailBytes > 0 && !allowHash {
					continue
				}
				choices = append(choices, choice{
					path:       path,
					pieces:     prepared.pieceCount,
					extraction: prepared,
					hashTail:   prepared.plan.tailBytes > 0,
				})
			}
		}
		// Reuse existing pieces before hashing a partial tail. Equal scores retain
		// discovery order, with already usable artifacts before conversions.
		slices.SortStableFunc(choices, func(a, b choice) int {
			if a.hashTail != b.hashTail {
				if a.hashTail {
					return 1
				}
				return -1
			}
			return cmp.Compare(pieceCountDistance(a.pieces), pieceCountDistance(b.pieces))
		})
		for _, candidate := range choices {
			if candidate.extraction == nil {
				path, err := findReusable(candidate.path, policy, subject)
				if err != nil || path != "" {
					return path, err
				}
				continue
			}
			if candidate.hashTail {
				openScope(progressScopeTracker(subject))
			}
			conversion, err := candidate.extraction.publish(ctx, s.tmpRoot, subject)
			if err != nil {
				return "", err
			}
			converted := conversion.path
			if converted == "" {
				continue
			}
			if conversion.publication != nil {
				publications.files = append(publications.files, conversion.publication)
				if err := trackers.WriteUploadTorrent(converted, converted); err != nil {
					return "", fmt.Errorf("torrent: canonicalize extracted torrent: %w", err)
				}
				destination, err := s.variantPath(meta, converted)
				if err != nil {
					return "", err
				}
				conversion.publication.destination = destination
				generated[converted] = true
			}
			extracted[candidate.path] = true
			path, err := findReusable(converted, policy, subject)
			if path == "" && conversion.publication != nil {
				if discardErr := conversion.publication.Discard(); discardErr != nil {
					return "", errors.Join(err, discardErr)
				}
				publications.files = publications.files[:len(publications.files)-1]
				delete(generated, converted)
			}
			if errors.Is(err, internalerrors.ErrClientRecheckFailed) {
				recheckErr = err
				continue
			}
			if err != nil {
				return "", err
			}
			if path != "" {
				candidates = appendUniqueTorrentCandidate(candidates, path)
				return path, nil
			}
		}
		if len(candidates) == 0 && recheckErr != nil {
			return "", recheckErr
		}
		return "", nil
	}
	generate := func(subject api.TorrentSubject) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("torrent: canceled before generation: %w", err)
		}
		scope := progressScopeTracker(subject)
		subject.TorrentOverrides.NoHash = nil
		subject.SkipIfRehashTrackers = nil
		subject.DeferGenerationPublication = true
		created, err := s.createSingle(ctx, subject, func(status, message string) {
			if status == "running" {
				openScope(scope)
				emitTorrentProgressForTracker(ctx, subject.SourcePath, scope, status, message)
				return
			}
			closeScope(scope, status, message)
		})
		if err != nil {
			return "", err
		}
		publication, ok := created.PendingPublication.(*generatedTorrentPublication)
		if !ok {
			return "", errors.New("torrent: generated variant has no publication owner")
		}
		destination, err := s.variantPath(meta, created.Path)
		if err != nil {
			_ = publication.Discard()
			return "", err
		}
		publication.destination = destination
		publications.files = append(publications.files, publication)
		candidates = appendUniqueTorrentCandidate(candidates, created.Path)
		cache[created.Path] = nil // generation has already validated content and policy.
		generated[created.Path] = true
		return created.Path, nil
	}
	// Reuse the best compatible artifact for each requested tracker.
	for _, tracker := range normalizedTrackerNames(meta.Trackers) {
		skipGeneration := hasTracker(meta.SkipIfRehashTrackers, []string{tracker})
		if rehash && skipGeneration {
			result.SkippedTrackers = append(result.SkippedTrackers, tracker)
			continue
		}
		subject := meta
		subject.Trackers = []string{tracker}
		path, err := choose(subject)
		if err != nil {
			return api.TorrentResult{}, err
		}
		if (path == "" || generated[path]) && (nohash || skipGeneration) {
			result.SkippedTrackers = append(result.SkippedTrackers, tracker)
			continue
		}
		if path == "" {
			path, err = generate(subject)
			if err != nil {
				return api.TorrentResult{}, err
			}
		}
		selected[tracker] = path
	}
	if len(meta.Trackers) > 0 && len(selected) == 0 && !meta.DeferWrapperConversion {
		// Preserve available bases even when every lane is skipped, without
		// hashing media solely for lanes that cannot run.
		nohash = true
	}
	// The search probe must discover alternatives before accepting partial lanes.
	if meta.DeferWrapperConversion && (len(result.SkippedTrackers) != 0 || len(candidates) == 0) {
		if err := ctx.Err(); err != nil {
			return api.TorrentResult{}, fmt.Errorf("torrent: canceled after reusable variant probe: %w", err)
		}
		return api.TorrentResult{}, fmt.Errorf("torrent: reusable variants unavailable: %w", internalerrors.ErrNotFound)
	}
	var general string
	if len(selected) == 0 {
		generalSubject := meta
		generalSubject.Trackers = nil
		general, err = choose(generalSubject)
		if err != nil {
			return api.TorrentResult{}, err
		}
		if general == "" && !nohash {
			general, err = generate(generalSubject)
			if err != nil {
				return api.TorrentResult{}, err
			}
		}
	}
	if general == "" && len(selected) == 0 {
		if err := ctx.Err(); err != nil {
			return api.TorrentResult{}, fmt.Errorf("torrent: canceled before variant selection: %w", err)
		}
		if len(result.SkippedTrackers) > 0 && !meta.DeferWrapperConversion {
			closeResultScopes(nil)
			return result, nil
		}
		return api.TorrentResult{}, fmt.Errorf("torrent: no reusable torrent found with nohash enabled: %w", internalerrors.ErrNotFound)
	}
	// Snapshot each selected base once. Immutable names avoid a shared mutable base
	// being overwritten while different tracker operations personalize it.
	artifacts := make(map[string]api.TorrentArtifact)
	retain := func(path string) (api.TorrentArtifact, error) {
		if artifact, ok := artifacts[path]; ok {
			return artifact, nil
		}
		destination := path
		if generated[path] {
			for _, publication := range publications.files {
				if publication.stage == path {
					destination = publication.destination
					break
				}
			}
		} else {
			var err error
			destination, err = s.retainVariant(meta, path)
			if err != nil {
				return api.TorrentArtifact{}, err
			}
		}
		hashPath := destination
		if generated[path] {
			hashPath = path
		}
		hash, err := loadInfoHash(hashPath)
		if err != nil {
			return api.TorrentArtifact{}, err
		}
		data, err := os.ReadFile(hashPath)
		if err != nil {
			return api.TorrentArtifact{}, fmt.Errorf("torrent: read retained base: %w", err)
		}
		artifact := api.TorrentArtifact{
			Path:          destination,
			InfoHash:      hash,
			ContentSHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
		}
		artifacts[path] = artifact
		if !slices.ContainsFunc(result.BaseArtifacts, func(existing api.TorrentArtifact) bool { return existing.Path == destination }) {
			result.BaseArtifacts = append(result.BaseArtifacts, artifact)
		}
		return artifact, nil
	}
	if general != "" {
		if _, err := retain(general); err != nil {
			return api.TorrentResult{}, err
		}
	}
	for _, tracker := range normalizedTrackerNames(meta.Trackers) {
		path := selected[tracker]
		if path == "" {
			continue
		}
		artifact, err := retain(path)
		if err != nil {
			return api.TorrentResult{}, err
		}
		result.TrackerArtifacts[tracker] = artifact
		if generated[path] {
			result.RehashedTrackers = append(result.RehashedTrackers, tracker)
		}
		if result.Path == "" {
			result.Path = path
		}
	}
	if result.Path == "" {
		result.Path = general
	}
	if result.Path != "" {
		result.InfoHash, err = loadInfoHash(result.Path)
		if err != nil {
			return api.TorrentResult{}, err
		}
		if generated[result.Path] {
			result.Path = artifacts[result.Path].Path
		}
	}
	publications.path = result.Path
	if err := ctx.Err(); err != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: canceled before publication: %w", err)
	}
	if len(publications.files) > 0 {
		if meta.DeferGenerationPublication {
			result.PendingPublication = publications
			publications = &variantPublication{} // ownership transferred to the caller.
			closeResultScopes(PendingPreparationScopes(meta, result))
			return result, nil
		} else if _, err := publications.Publish(); err != nil {
			failMessage = "Torrent publication failed"
			return api.TorrentResult{}, err
		}
	}
	closeResultScopes(nil)
	return result, nil
}

// PreparationProgressOutcome describes a settled scope in a successful Create result.
func PreparationProgressOutcome(result api.TorrentResult, tracker string) (status, message string) {
	tracker = strings.TrimSpace(tracker)
	if tracker == "" {
		if len(result.SkippedTrackers) > 0 && len(result.TrackerArtifacts) == 0 {
			return "skipped", "Torrent skipped: rehash required"
		}
		if len(result.TrackerArtifacts) > 0 {
			return "completed", "Reusable torrent check complete"
		}
	} else if slices.ContainsFunc(result.SkippedTrackers, func(skipped string) bool {
		return strings.EqualFold(strings.TrimSpace(skipped), tracker)
	}) {
		return "skipped", "Torrent skipped: rehash required"
	}
	return "completed", "Torrent ready"
}

// PendingPreparationScopes identifies the torrent scopes owned by deferred publication.
func PendingPreparationScopes(meta api.TorrentSubject, result api.TorrentResult) []string {
	if result.PendingPublication == nil {
		return nil
	}
	scopes := normalizedTrackerNames(result.RehashedTrackers)
	if len(scopes) == 0 && len(result.SkippedTrackers) == 0 {
		return []string{progressScopeTracker(meta)}
	}
	return scopes
}

// pieceCountDistance ranks reusable artifacts without making the preferred
// range a reason to hash again. Hard policy eligibility is checked separately.
func pieceCountDistance(count int64) int64 {
	return max(1000-count, count-2000, 0)
}

func (s *Service) variantPath(meta api.TorrentSubject, path string) (string, error) {
	base, err := TempTorrentPath(s.tmpRoot, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	dir := filepath.Join(filepath.Dir(base), "base-torrents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	return filepath.Join(dir, fmt.Sprintf("%x.torrent", sha256.Sum256(data))), nil
}

var retainedVariantMu sync.Mutex

func (s *Service) retainVariant(meta api.TorrentSubject, path string) (string, error) {
	base, err := TempTorrentPath(s.tmpRoot, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	stage, err := os.CreateTemp(filepath.Dir(base), ".base-*.torrent")
	if err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	stagePath := stage.Name()
	defer func() { _ = stage.Close(); _ = os.Remove(stagePath) }()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	if err := validateClientCandidateBytes(path, data, meta); err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	if _, err := stage.Write(data); err != nil {
		return "", fmt.Errorf("torrent: stage retained base: %w", err)
	}
	if err := stage.Close(); err != nil {
		return "", fmt.Errorf("torrent: close retained base: %w", err)
	}
	if err := validateTorrentContent(stagePath, meta); err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	if err := trackers.WriteUploadTorrent(stagePath, stagePath); err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	destination, err := s.variantPath(meta, stagePath)
	if err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	retainedVariantMu.Lock()
	defer retainedVariantMu.Unlock()
	if existing, err := os.ReadFile(destination); err == nil {
		sanitized, err := os.ReadFile(stagePath)
		if err != nil {
			return "", fmt.Errorf("torrent: retain base artifact: %w", err)
		}
		if !bytes.Equal(existing, sanitized) {
			return "", errors.New("torrent: retained base identity changed")
		}
		return destination, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	if err := os.Rename(stagePath, destination); err != nil {
		return "", fmt.Errorf("torrent: retain base artifact: %w", err)
	}
	return destination, nil
}

type variantPublication struct {
	files []*generatedTorrentPublication
	path  string
}

func (p *variantPublication) Publish() (string, error) {
	// Share the retained-base publication lock so identical writers never replace
	// an artifact that another operation may already have open on Windows.
	retainedVariantMu.Lock()
	defer retainedVariantMu.Unlock()
	for _, file := range p.files {
		if err := file.checkCancellation(); err != nil {
			return "", err
		}
		if existing, err := os.ReadFile(file.destination); err == nil {
			staged, err := os.ReadFile(file.stage)
			if err != nil {
				return "", fmt.Errorf("torrent: retain base artifact: %w", err)
			}
			if !bytes.Equal(existing, staged) {
				return "", errors.New("torrent: generated base destination changed")
			}
			if err := file.checkCancellation(); err != nil {
				return "", err
			}
			if err := file.Discard(); err != nil {
				return "", fmt.Errorf("torrent: retain base artifact: %w", err)
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("torrent: retain base artifact: %w", err)
		}
		if _, err := file.Publish(); err != nil {
			return "", fmt.Errorf("torrent: retain base artifact: %w", err)
		}
	}
	return p.path, nil
}
func (p *variantPublication) Discard() error {
	var result error
	for _, file := range p.files {
		result = errors.Join(result, file.Discard())
	}
	return result
}
