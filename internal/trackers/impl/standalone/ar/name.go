// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveARName(meta api.UploadSubject) string {
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName)
	}
	name := paths.ReleaseTempBaseFor(meta.SourcePath, meta.Release)
	if ext := strings.TrimSpace(filepath.Ext(name)); ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	return normalizeARName(name, meta.Tag)
}

func normalizeARName(name, tag string) string {
	replacer := strings.NewReplacer(
		"_", ".", " ", ".", "'", "", ":", "", "(", ".", ")", ".", "[", ".", "]", ".", "{", ".", "}", ".",
	)
	name = replacer.Replace(strings.TrimSpace(name))
	name = collapseDots(name)

	tagLower := strings.ToLower(strings.TrimSpace(tag))
	invalidTags := []string{"nogrp", "nogroup", "unknown", "-unk-"}
	if tagLower == "" || containsAny(tagLower, invalidTags) {
		for _, invalid := range invalidTags {
			name = regexp.MustCompile(`(?i)-?`+regexp.QuoteMeta(invalid)).ReplaceAllString(name, "")
		}
		name = strings.Trim(strings.TrimSpace(name), ".-")
		if name == "" {
			name = "release"
		}
		return name + "-NoGRP"
	}
	return name
}

func resolveARSearchName(meta api.UploadSubject) string {
	title := strings.TrimSpace(meta.Release.Title)
	if title == "" && meta.ProviderMetadata.TMDB != nil {
		title = strings.TrimSpace(meta.ProviderMetadata.TMDB.Title)
	}
	if title == "" && meta.ProviderMetadata.IMDB != nil {
		title = strings.TrimSpace(meta.ProviderMetadata.IMDB.Title)
	}
	if title == "" {
		title = strings.TrimSpace(meta.ReleaseName)
	}
	if title == "" {
		return ""
	}
	year := meta.Release.Year
	if year == 0 && meta.ProviderMetadata.TMDB != nil {
		year = meta.ProviderMetadata.TMDB.Year
	}
	if year == 0 && meta.ProviderMetadata.IMDB != nil {
		year = meta.ProviderMetadata.IMDB.Year
	}
	if year > 0 {
		return strings.TrimSpace(title + " " + strconv.Itoa(year))
	}
	return title
}
