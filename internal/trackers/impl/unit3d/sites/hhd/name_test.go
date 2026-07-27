// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hhd

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNamePreservesHHDHDRVocabulary(t *testing.T) {
	t.Parallel()

	const want = "Example Release 2026 2160p WEB-DL PQ10 H.265-GRP"
	if got := buildName(api.UploadSubject{ReleaseName: want}, config.TrackerConfig{}); got != want {
		t.Fatalf("HHD name = %q", got)
	}
}

