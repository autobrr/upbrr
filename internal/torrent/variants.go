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
func (s *Service) Create(ctx context.Context, meta api.TorrentSubject) (api.TorrentResult, error) {
	if err := ctx.Err(); err != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: create canceled: %w", err)
	}
	meta.SourcePath = strings.TrimSpace(meta.SourcePath)
	if meta.SourcePath == "" {
		return api.TorrentResult{}, internalerrors.ErrInvalidInput
	}
	// Direct metainfo input has no local media from which to make another variant.
	if strings.EqualFold(filepath.Ext(meta.SourcePath), ".torrent") {
		return s.createSingle(ctx, meta)
	}
	logger := logging.FromContext(ctx, s.logger)
	rehash := torrentOverrideEnabled(meta.TorrentOverrides.Rehash)
	nohash := torrentOverrideEnabled(meta.TorrentOverrides.NoHash) && !rehash
	candidates, err := reusableTorrentCandidates(s.tmpRoot, meta)
	if err != nil {
		return api.TorrentResult{}, err
	}
	cache := make(map[string]error)
	if rehash {
		candidates = nil
	}
	extractionCandidates := slices.Clone(candidates)
	// Layout, captured-byte identity, and requested recheck are evaluated once per
	// candidate; stale alternatives must not discard a verified reusable base.
	// A failed explicit recheck remains fatal when no verified candidate survives.
	valid := make([]string, 0, len(candidates))
	var recheckErr error
	for _, candidate := range candidates {
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
			valid = append(valid, candidate)
		}
	}
	candidates = valid
	result := api.TorrentResult{TrackerArtifacts: make(map[string]api.TorrentArtifact)}
	publications := &variantPublication{}
	defer func() { _ = publications.Discard() }()
	generated := make(map[string]bool)
	selected := make(map[string]string)
	extracted := make(map[string]bool)
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
		if s.tmpRoot != "" && (!meta.DeferWrapperConversion || len(choices) > 0) {
			for _, path := range extractionCandidates {
				if extracted[path] {
					continue
				}
				prepared, err := prepareSingleFileExtraction(path, subject, policy)
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
				path, err := findReusableTorrent(ctx, []string{candidate.path}, policy, subject, cache, logger)
				if err != nil || path != "" {
					return path, err
				}
				continue
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
			path, err := findReusableTorrent(ctx, []string{converted}, policy, subject, cache, logger)
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
		subject.TorrentOverrides.Rehash = new(true)
		subject.TorrentOverrides.NoHash = nil
		subject.SkipIfRehashTrackers = nil
		subject.DeferGenerationPublication = true
		created, err := s.createSingle(ctx, subject)
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
		if len(result.SkippedTrackers) > 0 && !meta.DeferWrapperConversion {
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
		} else if _, err := publications.Publish(); err != nil {
			return api.TorrentResult{}, err
		}
	}
	if result.Path != "" && result.PendingPublication == nil && !meta.DeferWrapperConversion {
		emitTorrentProgress(ctx, meta, "completed", "Torrent ready")
	}
	return result, nil
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
		if existing, err := os.ReadFile(file.destination); err == nil {
			staged, err := os.ReadFile(file.stage)
			if err != nil {
				return "", fmt.Errorf("torrent: retain base artifact: %w", err)
			}
			if !bytes.Equal(existing, staged) {
				return "", errors.New("torrent: generated base destination changed")
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
