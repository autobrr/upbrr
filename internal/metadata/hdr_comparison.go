// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// InspectHDRComparison collects file-specific probe evidence without preparing
// a release or requesting provider metadata. The caller verifies source identity
// before and after this bounded inspection.
func (s *Service) InspectHDRComparison(ctx context.Context, path, fingerprint string) (api.HDRSourceEvidence, error) {
	root, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return api.HDRSourceEvidence{}, fmt.Errorf("HDR inspection temp root: %w", err)
	}
	return InspectHDRFile(ctx, s.mi, path, fingerprint, root)
}

// InspectHDRFile shares bounded per-file evidence inspection with standalone CLI analysis.
func InspectHDRFile(ctx context.Context, exporter mediainfo.Exporter, path, fingerprint, root string) (api.HDRSourceEvidence, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	key := sha256.Sum256([]byte(path + "\x00" + fingerprint))
	result, err := exporter.Export(ctx, mediainfo.Request{
		SourcePath:  path,
		VideoPath:   path,
		TempRoot:    root,
		ArtifactDir: filepath.Join(root, "hdr-mediainfo", hex.EncodeToString(key[:])),
		Release:     ParseReleaseInfo(path),
	})
	if err != nil {
		return api.HDRSourceEvidence{}, fmt.Errorf("HDR file inspection: %w", err)
	}
	file, err := os.Open(result.JSONPath)
	if err != nil {
		return api.HDRSourceEvidence{}, fmt.Errorf("open HDR file inspection: %w", err)
	}
	defer file.Close()
	const limit = 8 << 20
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return api.HDRSourceEvidence{}, fmt.Errorf("read HDR file inspection: %w", err)
	}
	if len(payload) > limit {
		return api.HDRSourceEvidence{}, errors.New("HDR file inspection exceeds report limit")
	}
	if err := ctx.Err(); err != nil {
		return api.HDRSourceEvidence{}, fmt.Errorf("HDR file inspection canceled: %w", err)
	}
	var doc mediafacts.MediaInfoDocument
	if err := json.Unmarshal(payload, &doc); err != nil {
		return api.HDRSourceEvidence{}, fmt.Errorf("decode HDR file inspection: %w", err)
	}
	return mediafacts.HDRSourceFromMediaInfo(doc), nil
}
