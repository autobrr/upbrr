// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadName(meta api.UploadSubject) string {
	return normalizeName(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename))
}

func normalizeName(input string) string {
	mapper := func(r rune) rune {
		if r > unicode.MaxASCII {
			return -1
		}
		if strings.ContainsRune(`\/*?"<>|`, r) {
			return -1
		}
		return r
	}
	return strings.Join(strings.Fields(strings.Map(mapper, strings.ReplaceAll(input, ":", " -"))), " ")
}

func resolveSearchName(meta api.UploadSubject) string {
	if meta.Identity.IMDBID != 0 {
		return resolveUploadName(meta)
	}
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	return resolveUploadName(meta)
}
