// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package yus

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNamePreservesYUSHDRVocabulary(t *testing.T) {
	t.Parallel()

	const want = "Example Release 2026 2160p WEB-DL HLG H.265-GRP"
	if got := buildName(api.UploadSubject{ReleaseName: want}, config.TrackerConfig{}); got != want {
		t.Fatalf("YUS name = %q", got)
	}
}

