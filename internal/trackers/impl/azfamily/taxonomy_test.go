// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolutionUsesResolvedFacts(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{ReleaseName: "Example.Movie.2026.2160p.WEB-DL-GRP"}
	for _, site := range []string{"AZ", "CZ", "PHD"} {
		if got := videoQualityID(siteDefinition{Name: site}, meta); got != "0" {
			t.Fatalf("%s missing resolution quality = %q", site, got)
		}
		meta.Release.Resolution = "720p"
		if got := videoQualityID(siteDefinition{Name: site}, meta); got != "2" {
			t.Fatalf("%s resolved resolution quality = %q", site, got)
		}
		meta.Release.Resolution = ""
	}
	if got := resolutionValue(meta); got != "" {
		t.Fatalf("missing resolution = %q", got)
	}
	meta.Release.Resolution = "720p"
	if got := resolutionValue(meta); got != "720p" {
		t.Fatalf("resolved resolution = %q", got)
	}
	meta.DiscType = "BDMV"
	if got := resolutionValue(meta); got != "1280x720" {
		t.Fatalf("resolved disc resolution = %q", got)
	}
}
