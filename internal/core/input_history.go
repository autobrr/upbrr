// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// GetInputHistory returns retained correction and track evidence without
// triggering provider requests or media inspection.
func (c *Core) GetInputHistory(ctx context.Context, source string) (api.InputHistory, error) {
	if c == nil || c.history == nil || c.history.repo == nil {
		return api.InputHistory{}, errors.New("core: history is not initialized")
	}
	if strings.TrimSpace(source) == "" {
		return api.InputHistory{}, internalerrors.ErrInvalidInput
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return api.InputHistory{}, fmt.Errorf("core: normalize input history path: %w", err)
	}
	record, err := c.history.repo.LoadHistoryRecord(ctx, filepath.Clean(source))
	if err != nil {
		return api.InputHistory{}, fmt.Errorf("core: load input history: %w", err)
	}
	result := api.InputHistory{Corrections: record.Corrections}
	if record.PreparedRelease != nil {
		prepared, err := record.PreparedRelease.Clone()
		if err != nil {
			return api.InputHistory{}, fmt.Errorf("core: clone input history: %w", err)
		}
		result.Release = api.ReleaseRef{SourcePath: prepared.Source.SourcePath, Generation: prepared.Generation}
		result.SourceFingerprint = prepared.Compatibility.SourceFingerprint
		result.Tracks = prepared.Media.Tracks
	}
	return result, nil
}
