// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/paths"
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

	s.logger.Debugf("torrent: preparing for %s", meta.SourcePath)
	policy := resolveTrackerPolicy(meta)
	forceRehash := torrentOverrideEnabled(meta.TorrentOverrides.Rehash)
	reuseOnly := torrentOverrideEnabled(meta.TorrentOverrides.NoHash)

	clientTorrent := strings.TrimSpace(meta.ClientTorrentPath)
	if !forceRehash && clientTorrent != "" {
		info, err := os.Stat(clientTorrent)
		if err == nil && !info.IsDir() {
			if err := validateCandidateTorrent(clientTorrent, policy, meta, s.logger); err == nil {
				s.logger.Debugf("torrent: using client-provided torrent %s", clientTorrent)
				return resultFromPath(clientTorrent)
			}
		}
	}

	source := strings.TrimSpace(meta.SourcePath)
	if source == "" {
		return api.TorrentResult{}, internalerrors.ErrInvalidInput
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
		return resultFromPath(source)
	}

	if !forceRehash && s.tmpRoot != "" {
		tmpTorrentPath, err := TempTorrentPath(s.tmpRoot, meta, source)
		if err != nil {
			return api.TorrentResult{}, err
		}
		if info, err := os.Stat(tmpTorrentPath); err == nil {
			if !info.IsDir() {
				if err := validateCandidateTorrent(tmpTorrentPath, policy, meta, s.logger); err == nil {
					s.logger.Debugf("torrent: reusing existing temp torrent %s", tmpTorrentPath)
					return resultFromPath(tmpTorrentPath)
				}
			}
		}
	}

	candidate := source + ".torrent"
	if !forceRehash {
		if info, err := os.Stat(candidate); err == nil {
			if !info.IsDir() {
				if err := validateCandidateTorrent(candidate, policy, meta, s.logger); err == nil {
					s.logger.Debugf("torrent: reusing existing torrent %s", candidate)
					return resultFromPath(candidate)
				}
			}
		}

		baseName := filepath.Base(source)
		if baseName != "" {
			sibling := filepath.Join(filepath.Dir(source), baseName+".torrent")
			if sibling != candidate {
				if info, err := os.Stat(sibling); err == nil {
					if !info.IsDir() {
						if err := validateCandidateTorrent(sibling, policy, meta, s.logger); err == nil {
							s.logger.Debugf("torrent: reusing existing torrent %s", sibling)
							return resultFromPath(sibling)
						}
					}
				}
			}
		}
	}

	if reuseOnly {
		return api.TorrentResult{}, fmt.Errorf("torrent: no reusable torrent found with nohash enabled: %w", internalerrors.ErrNotFound)
	}

	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return api.TorrentResult{}, fmt.Errorf("torrent: path %q: %w", source, internalerrors.ErrNotFound)
		}
		return api.TorrentResult{}, fmt.Errorf("torrent: path %q: %w", source, err)
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
	s.logger.Debugf("torrent: creating torrent with max piece exponent %d", pieceOptions.maxPieceExp)

	info, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:           source,
		OutputPath:     outputPath,
		IsPrivate:      true,
		MaxPieceLength: &pieceOptions.maxPieceExp,
		PieceLengthExp: pieceOptions.pieceExp,
	})
	if err != nil {
		return api.TorrentResult{}, fmt.Errorf("torrent: create %q: %w", source, err)
	}
	s.logger.Debugf("torrent: created torrent %s", info.Path)

	return api.TorrentResult{Path: info.Path, InfoHash: info.InfoHash}, nil
}

func torrentOverrideEnabled(value *bool) bool {
	return value != nil && *value
}

func validateCandidateTorrent(path string, policy *trackerTorrentPolicy, meta api.PreparedMetadata, logger api.Logger) error {
	if policy == nil {
		return nil
	}
	if err := policy.validateTorrent(path, meta); err != nil {
		if logger != nil {
			logger.Warnf("torrent: skipping non-compliant torrent %s: %v", path, err)
		}
		return err
	}
	return nil
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
