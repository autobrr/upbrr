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
	"math"
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
// pieces. A general-purpose base and a base with pieces at most 16 MiB are retained
// automatically when available, or generated when hashing is permitted. Hard
// tracker limits and the explicit maximum take precedence over piece-count goals.
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
	if !meta.DeferWrapperConversion {
		for _, candidate := range slices.Clone(candidates) {
			converted, err := convertSingleFileWrapper(s.tmpRoot, candidate, meta)
			if err != nil {
				return api.TorrentResult{}, err
			}
			if converted != "" {
				candidates = appendUniqueTorrentCandidate(candidates, converted)
			}
		}
	}
	// Layout, captured-byte identity, and requested recheck are evaluated once per
	// candidate; stale alternatives must not discard a verified reusable base.
	// A failed explicit recheck remains fatal when no verified candidate survives.
	valid := make([]string, 0, len(candidates))
	var recheckErr error
	for _, candidate := range candidates {
		path, err := findReusableTorrent(ctx, []string{candidate}, nil, meta, cache, logger)
		if errors.Is(err, internalerrors.ErrClientRecheckFailed) {
			recheckErr = err
			continue
		}
		if err != nil {
			return api.TorrentResult{}, err
		}
		if path != "" {
			valid = append(valid, path)
		}
	}
	if len(valid) == 0 && recheckErr != nil {
		return api.TorrentResult{}, recheckErr
	}
	candidates = valid
	rankCandidates(candidates, meta)
	result := api.TorrentResult{TrackerArtifacts: make(map[string]api.TorrentArtifact)}
	publications := &variantPublication{}
	defer func() { _ = publications.Discard() }()
	generated := make(map[string]bool)
	selected := make(map[string]string)
	choose := func(subject api.TorrentSubject) (string, error) {
		policy := resolveTrackerPolicy(subject, s.registry)

		return findReusableTorrent(ctx, candidates, policy, subject, cache, logger)
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
	// Prefer reuse for tracker lanes before preparing optional global variants.
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
	rankCandidates(candidates, meta)
	// The search probe must discover alternatives before accepting partial lanes.
	if meta.DeferWrapperConversion && (len(result.SkippedTrackers) != 0 || len(candidates) == 0) {
		return api.TorrentResult{}, fmt.Errorf("torrent: reusable variants unavailable: %w", internalerrors.ErrNotFound)
	}
	generalSubject := meta
	generalSubject.Trackers = nil
	general, err := choose(generalSubject)
	if err != nil {
		return api.TorrentResult{}, err
	}
	if !nohash && (general == "" || !generalPieceCount(general, meta)) {
		general, err = generate(generalSubject)
		if err != nil {
			return api.TorrentResult{}, err
		}
	}
	compactSubject := generalSubject
	limit := 16
	if override := meta.TorrentOverrides.MaxPieceSizeMiB; override != nil {
		limit = min(limit, *override)
	}
	compactSubject.TorrentOverrides.MaxPieceSizeMiB = &limit
	compact, err := choose(compactSubject)
	if err != nil {
		return api.TorrentResult{}, err
	}
	if compact == "" && !nohash {
		compact, err = generate(compactSubject)
		if err != nil {
			return api.TorrentResult{}, err
		}
	}
	if meta.DeferWrapperConversion && (compact == "" || general == "" || !generalPieceCount(general, meta)) {
		return api.TorrentResult{}, fmt.Errorf("torrent: reusable base variants incomplete: %w", internalerrors.ErrNotFound)
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
	for _, path := range []string{general, compact} {
		if path != "" {
			if _, err := retain(path); err != nil {
				return api.TorrentResult{}, err
			}
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

// generalPieceCount treats 1,000–2,000 pieces as a preference, with mkbrr's
// supported minimum and the effective maximum defining attainable boundaries.
func generalPieceCount(path string, meta api.TorrentSubject) bool {
	return pieceCountDistance(path, meta) == 0
}

func pieceCountDistance(path string, meta api.TorrentSubject) int64 {
	mi, err := metainfo.LoadFromFile(path)
	if err != nil {
		return math.MaxInt64
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return math.MaxInt64
	}
	maxPiece := int64(128 << 20)
	if limit := meta.TorrentOverrides.MaxPieceSizeMiB; limit != nil {
		maxPiece = int64(*limit) << 20
	}
	count := int64(info.NumPieces())
	switch {
	case count < 1000:
		if info.PieceLength <= 64<<10 || info.TotalLength() < 1000*(64<<10) {
			return 0
		}
		return 1000 - count
	case count > 2000:
		if info.PieceLength >= maxPiece {
			return 0
		}
		return count - 2000
	default:
		return 0
	}
}

func rankCandidates(candidates []string, meta api.TorrentSubject) {
	scores := make(map[string]int64, len(candidates))
	for _, candidate := range candidates {
		scores[candidate] = pieceCountDistance(candidate, meta)
	}
	slices.SortStableFunc(candidates, func(a, b string) int { return cmp.Compare(scores[a], scores[b]) })
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
