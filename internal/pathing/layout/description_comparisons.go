// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package layout

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
)

// DescriptionComparisonTempRoot returns the cache owned by one canonical source
// path. Its reserved @ prefix cannot collide with legacy release basenames.
// It does not create directories, so History can resolve missing or orphaned data.
func DescriptionComparisonTempRoot(tmpRoot, sourcePath string) (string, error) {
	if strings.TrimSpace(tmpRoot) == "" || strings.TrimSpace(sourcePath) == "" {
		return "", errors.New("paths: comparison cache root and source are required")
	}
	sourceKey, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return "", fmt.Errorf("paths: comparison cache source: %w", err)
	}
	if runtime.GOOS == "windows" {
		sourceKey = strings.ToLower(sourceKey)
	}
	digest := sha256.Sum256([]byte(sourceKey))
	dir := filepath.Join(tmpRoot, fmt.Sprintf("@description-comparisons-%x", digest))
	if !pathutil.IsWithinRoot(tmpRoot, dir) {
		return "", errors.New("paths: comparison cache escapes temp root")
	}
	return dir, nil
}
