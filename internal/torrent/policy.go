// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"fmt"
	"strings"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type pieceSizeRange struct {
	maxSize  uint64
	pieceExp uint
}

// trackerTorrentPolicy contains the metainfo limits enforced during both
// candidate reuse and torrent creation for one tracker.
type trackerTorrentPolicy struct {
	name            string
	maxPieceExp     uint
	pieceSizeChart  []pieceSizeRange
	maxTorrentBytes int64
}

// resolveTrackerPolicy uses the first registered artifact policy in subject
// tracker order. When none match, the legacy PTP size chart applies if PTP is
// present anywhere in the subject tracker list.
func resolveTrackerPolicy(meta api.TorrentSubject, registry *trackers.Registry) *trackerTorrentPolicy {
	for _, name := range meta.Trackers {
		artifact, ok := registry.LookupArtifactPolicy(name)
		if !ok {
			continue
		}
		maxPieceExp, _ := pieceExpForMiB(artifact.MaxPieceSizeMiB)
		return &trackerTorrentPolicy{
			name:            strings.ToUpper(strings.TrimSpace(name)),
			maxPieceExp:     maxPieceExp,
			maxTorrentBytes: artifact.MaxTorrentBytes,
		}
	}
	if hasTracker(meta.Trackers, []string{"PTP"}) {
		return &trackerTorrentPolicy{
			name:        "PTP",
			maxPieceExp: 24,
			pieceSizeChart: []pieceSizeRange{
				{maxSize: 58 << 20, pieceExp: 16},
				{maxSize: 122 << 20, pieceExp: 17},
				{maxSize: 213 << 20, pieceExp: 18},
				{maxSize: 444 << 20, pieceExp: 19},
				{maxSize: 922 << 20, pieceExp: 20},
				{maxSize: 3977 << 20, pieceExp: 21},
				{maxSize: 6861 << 20, pieceExp: 22},
				{maxSize: 14234 << 20, pieceExp: 23},
				{maxSize: ^uint64(0), pieceExp: 24},
			},
		}
	}
	return nil
}

func (p *trackerTorrentPolicy) createOptions(meta api.TorrentSubject) mkbrrPieceOptions {
	if p == nil {
		return applyTorrentOverridePieceOptions(meta, mkbrrPieceOptions{maxPieceExp: 27})
	}
	options := mkbrrPieceOptions{maxPieceExp: p.maxPieceExp}
	if len(p.pieceSizeChart) == 0 {
		return applyTorrentOverridePieceOptions(meta, options)
	}
	if exp, ok := p.requiredPieceExp(meta); ok {
		options.pieceExp = &exp
	}
	return applyTorrentOverridePieceOptions(meta, options)
}

func (p *trackerTorrentPolicy) requiredPieceExp(meta api.TorrentSubject) (uint, bool) {
	if p == nil || len(p.pieceSizeChart) == 0 {
		return 0, false
	}
	if meta.SourceSize <= 0 {
		return 0, false
	}
	size := uint64(meta.SourceSize)
	for _, entry := range p.pieceSizeChart {
		if size <= entry.maxSize {
			return entry.pieceExp, true
		}
	}
	return 0, false
}

func (p *trackerTorrentPolicy) validateTorrent(path string, meta api.TorrentSubject) error {
	if p == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	if err := validateTorrentFileSize(path, p); err != nil {
		return err
	}
	torrentMeta, err := metainfo.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("load torrent %q: %w", path, err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("decode torrent %q: %w", path, err)
	}
	if p.maxPieceExp > 0 {
		maxPieceLength := int64(1) << p.maxPieceExp
		if info.PieceLength > maxPieceLength {
			return fmt.Errorf("%s piece size %d exceeds max %d", p.name, info.PieceLength, maxPieceLength)
		}
	}
	requiredExp, ok := p.requiredPieceExp(meta)
	if !ok {
		return nil
	}
	requiredLength := int64(1) << requiredExp
	if info.PieceLength != requiredLength {
		return fmt.Errorf("%s requires piece size %d, got %d", p.name, requiredLength, info.PieceLength)
	}
	return nil
}

type mkbrrPieceOptions struct {
	maxPieceExp uint
	pieceExp    *uint
}

func applyTorrentOverridePieceOptions(meta api.TorrentSubject, options mkbrrPieceOptions) mkbrrPieceOptions {
	if meta.TorrentOverrides.MaxPieceSizeMiB == nil {
		return options
	}

	overrideExp, ok := pieceExpForMiB(*meta.TorrentOverrides.MaxPieceSizeMiB)
	if !ok {
		return options
	}
	if options.maxPieceExp == 0 || overrideExp < options.maxPieceExp {
		options.maxPieceExp = overrideExp
	}
	return options
}

func pieceExpForMiB(sizeMiB int) (uint, bool) {
	switch sizeMiB {
	case 1:
		return 20, true
	case 2:
		return 21, true
	case 4:
		return 22, true
	case 8:
		return 23, true
	case 16:
		return 24, true
	case 32:
		return 25, true
	case 64:
		return 26, true
	case 128:
		return 27, true
	default:
		return 0, false
	}
}
