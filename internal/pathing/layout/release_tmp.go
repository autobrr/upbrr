// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package layout defines deterministic application filesystem layouts.
package layout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// ReleaseTempDir returns the release-specific temporary directory and its base name.
func ReleaseTempDir(tmpRoot string, meta preparationstate.State, source string) (string, string, error) {
	return ReleaseTempDirFor(tmpRoot, source, meta.Release)
}

// ReleaseTempDirFor returns the release-specific temporary directory without
// coupling callers to the legacy universal prepared-metadata shape.
func ReleaseTempDirFor(tmpRoot string, source string, release api.ReleaseInfo) (string, string, error) {
	trimmed := strings.TrimSpace(tmpRoot)
	if trimmed == "" {
		return "", "", errors.New("paths: tmp root is required")
	}
	base := ReleaseTempBaseFor(source, release)
	contentDir := filepath.Join(trimmed, base)
	if err := os.MkdirAll(contentDir, 0o700); err != nil {
		return "", "", fmt.Errorf("paths: create tmp dir: %w", err)
	}
	return contentDir, base, nil
}

// ReleaseTempBase returns the stable directory name used for release temporary files.
func ReleaseTempBase(meta preparationstate.State, source string) string {
	return ReleaseTempBaseFor(source, meta.Release)
}

// ReleaseTempBaseFor returns the stable temporary-directory name from the
// source path and optional parsed release identity.
func ReleaseTempBaseFor(source string, release api.ReleaseInfo) string {
	base := pathutil.Base(source)
	if base != "" && base != string(filepath.Separator) && base != "." {
		return sanitizeName(base)
	}
	if name := releaseBaseName(release); name != "" {
		return sanitizeName(name)
	}
	return "content"
}

func releaseBaseName(release api.ReleaseInfo) string {
	title := strings.TrimSpace(release.Title)
	if title == "" {
		title = strings.TrimSpace(release.Alt)
	}
	if title == "" {
		return ""
	}
	parts := []string{title}
	if release.Year > 0 {
		parts = append(parts, strconv.Itoa(release.Year))
	}
	trimmedSource := strings.TrimSpace(release.Source)
	if trimmedSource != "" {
		parts = append(parts, trimmedSource)
	}
	if trimmedType := strings.TrimSpace(release.Type); trimmedType != "" {
		if !strings.EqualFold(trimmedType, trimmedSource) {
			parts = append(parts, trimmedType)
		}
	}
	name := strings.Join(parts, ".")
	if strings.TrimSpace(release.Group) != "" {
		name = name + "-" + strings.TrimSpace(release.Group)
	}
	return name
}

func sanitizeName(base string) string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '_'
		}
	}, base)
	if strings.TrimSpace(sanitized) == "" {
		return "content"
	}
	return sanitized
}
