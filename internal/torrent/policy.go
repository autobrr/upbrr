// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"fmt"
	"math"
	"strings"

	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// trackerTorrentPolicy contains the metainfo limits enforced during both
// candidate reuse and torrent creation for selected trackers.
type trackerTorrentPolicy struct {
	name                string
	maxPieceExp         uint
	maxTorrentBytes     int64
	pieceSizeProfileURL string
	profileMaxPieceExp  uint
	ptp                 bool
}

// resolveTrackerPolicy combines registered artifact limits and uses the most
// restrictive mkbrr piece-size profile selected for this source size.
func resolveTrackerPolicy(meta api.TorrentSubject, registry *trackers.Registry) *trackerTorrentPolicy {
	var policy *trackerTorrentPolicy
	for _, name := range meta.Trackers {
		trackerName := strings.ToUpper(strings.TrimSpace(name))
		artifact, ok := registry.LookupArtifactPolicy(name)
		if !ok {
			continue
		}
		if policy == nil {
			policy = &trackerTorrentPolicy{}
		}
		if policy.name == "" {
			policy.name = trackerName
		} else {
			policy.name += "+" + trackerName
		}
		policy.ptp = policy.ptp || trackerName == "PTP"
		maxPieceExp, _ := pieceExpForMiB(artifact.MaxPieceSizeMiB)
		if maxPieceExp > 0 && (policy.maxPieceExp == 0 || maxPieceExp < policy.maxPieceExp) {
			policy.maxPieceExp = maxPieceExp
		}
		if artifact.MaxTorrentBytes > 0 && (policy.maxTorrentBytes == 0 || artifact.MaxTorrentBytes < policy.maxTorrentBytes) {
			policy.maxTorrentBytes = artifact.MaxTorrentBytes
		}
		profileURL := strings.TrimSpace(artifact.PieceSizeProfileURL)
		if profileURL == "" {
			continue
		}
		profileExp := mkbrr.GetRecommendedPieceLengthExp(profileURL, uint64(max(meta.SourceSize, 0)))
		if meta.SourceSize <= 0 {
			profileExp = maxPieceExp
		}
		if policy.pieceSizeProfileURL == "" || profileExp > 0 && (policy.profileMaxPieceExp == 0 || profileExp < policy.profileMaxPieceExp) {
			policy.pieceSizeProfileURL = profileURL
			policy.profileMaxPieceExp = profileExp
		}
	}
	if policy != nil {
		return policy
	}
	if hasTracker(meta.Trackers, []string{"PTP"}) {
		return &trackerTorrentPolicy{
			name:                "PTP",
			maxPieceExp:         24,
			pieceSizeProfileURL: "https://passthepopcorn.me",
			ptp:                 true,
		}
	}
	return nil
}

func (p *trackerTorrentPolicy) createOptions(meta api.TorrentSubject) mkbrrPieceOptions {
	if p == nil {
		return applyTorrentOverridePieceOptions(meta, mkbrrPieceOptions{maxPieceExp: 27})
	}
	options := mkbrrPieceOptions{maxPieceExp: p.maxPieceExp, profileURL: p.pieceSizeProfileURL}
	if exp, ok := p.requiredPieceExp(meta); ok {
		options.pieceExp = &exp
	}
	return applyTorrentOverridePieceOptions(meta, options)
}

func (p *trackerTorrentPolicy) requiredPieceExp(meta api.TorrentSubject) (uint, bool) {
	if p == nil || p.pieceSizeProfileURL == "" || meta.SourceSize <= 0 {
		return 0, false
	}
	exp := mkbrr.GetRecommendedPieceLengthExp(p.pieceSizeProfileURL, uint64(meta.SourceSize))
	if exp == 0 {
		return 0, false
	}
	if p.maxPieceExp > 0 && exp > p.maxPieceExp {
		exp = p.maxPieceExp
	}
	return exp, true
}

func (p *trackerTorrentPolicy) validateCreateOptions(meta api.TorrentSubject, options mkbrrPieceOptions) error {
	if p == nil || !p.ptp || meta.SourceSize <= 0 {
		return nil
	}
	minExp, _ := ptpPieceExpRange(meta.SourceSize)
	if options.maxPieceExp > 0 && options.maxPieceExp < minExp {
		return fmt.Errorf("%s piece-size policies conflict: max exponent %d is below PTP minimum %d", p.name, options.maxPieceExp, minExp)
	}
	if options.pieceExp != nil && *options.pieceExp < minExp {
		return fmt.Errorf("%s piece-size profile exponent %d is below PTP minimum %d", p.name, *options.pieceExp, minExp)
	}
	return nil
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
	if !p.ptp {
		return nil
	}
	sourceSize := meta.SourceSize
	if sourceSize <= 0 {
		sourceSize = info.TotalLength()
	}
	minExp, maxExp := ptpPieceExpRange(sourceSize)
	minLength, maxLength := int64(1)<<minExp, int64(1)<<maxExp
	if info.PieceLength <= 0 || info.PieceLength&(info.PieceLength-1) != 0 || info.PieceLength < minLength || info.PieceLength > maxLength {
		return fmt.Errorf("%s piece size %d is outside PTP range %d-%d", p.name, info.PieceLength, minLength, maxLength)
	}
	return nil
}

func ptpPieceExpRange(size int64) (uint, uint) {
	// PTP's green chart cells cover 500-2000 pieces, with a 32/64 KiB floor and 16 MiB cap.
	bytes := float64(max(size, 1))
	minExp := min(max(int(math.Ceil(math.Log2(bytes/2000))), 15), 24)
	maxExp := min(max(int(math.Floor(math.Log2(bytes/500))), 16), 24)
	return uint(minExp), uint(maxExp)
}

type mkbrrPieceOptions struct {
	maxPieceExp uint
	pieceExp    *uint
	profileURL  string
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
	if options.pieceExp != nil && *options.pieceExp > options.maxPieceExp {
		pieceExp := options.maxPieceExp
		options.pieceExp = &pieceExp
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
