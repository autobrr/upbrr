// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

func ReleaseTempDir(tmpRoot string, meta api.PreparedMetadata, source string) (string, string, error) {
	trimmed := strings.TrimSpace(tmpRoot)
	if trimmed == "" {
		return "", "", errors.New("paths: tmp root is required")
	}
	base := ReleaseTempBase(meta, source)
	contentDir := filepath.Join(trimmed, base)
	if err := os.MkdirAll(contentDir, 0o700); err != nil {
		return "", "", fmt.Errorf("paths: create tmp dir: %w", err)
	}
	return contentDir, base, nil
}

func ReleaseTempBase(meta api.PreparedMetadata, source string) string {
	base := pathutil.Base(source)
	if base != "" && base != string(filepath.Separator) && base != "." {
		return sanitizeName(base)
	}
	if name := releaseBaseName(meta.Release); name != "" {
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
	if strings.TrimSpace(release.Source) != "" {
		parts = append(parts, strings.TrimSpace(release.Source))
	}
	if trimmedType := strings.TrimSpace(release.Type); trimmedType != "" {
		if !strings.EqualFold(trimmedType, strings.TrimSpace(release.Source)) {
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
