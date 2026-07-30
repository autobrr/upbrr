// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package thr

import (
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

var thrReleaseNamePattern = regexp.MustCompile(`[^0-9a-zA-Z. '\-\[\]]+`)

func resolveName(meta api.UploadSubject) string {
	base := strings.ReplaceAll(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename), "DD+", "DDP")
	return thrReleaseNamePattern.ReplaceAllString(base, " ")
}
