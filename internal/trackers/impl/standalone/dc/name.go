// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadName(meta api.UploadSubject) string {
	name := metautil.FirstNonEmptyTrimmed(
		strings.TrimSpace(meta.SceneName),
		strings.TrimSpace(meta.ReleaseNameClean),
		strings.TrimSpace(meta.ReleaseName),
		strings.TrimSpace(meta.Filename),
	)
	if name == "" {
		name = "release"
	}
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName) + " [UNRAR]"
	}
	name = strings.NewReplacer("DD+", "DDP", "DTS:", "DTS-", "HDR10+", "HDR10P").Replace(name)
	out := strings.Builder{}
	for _, r := range name {
		if r > 127 {
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' || r == '.' || r == '-' {
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(out.String())
}
