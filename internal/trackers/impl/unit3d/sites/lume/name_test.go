// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameUsesLumeStructuredHDRVocabulary(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		ReleaseName: "Example Release 2026 Hybrid 2160p WEB-DL DV PQ10 Hi10P H.265-GRP",
		HDR:         "DV PQ10",
		HDRFacts: api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatPQ10},
			Origin:  api.HDREvidenceMediaInfo,
			Status:  api.HDREvidenceComplete,
		},
	}
	got := buildName(meta, config.TrackerConfig{})
	if strings.Contains(got, "Hybrid") || strings.Contains(got, "Hi10P") || !strings.Contains(got, "DV HDR") {
		t.Fatalf("LUME name = %q", got)
	}
}

