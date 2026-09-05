// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseNamePolicyVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().ReleaseNamePolicy.ID; got != "standalone/tvc/v2" {
		t.Fatalf("TVC release-name policy = %q", got)
	}
}

func TestResolveNameUsesOnlyAvailableTVPresentationFacts(t *testing.T) {
	t.Parallel()

	base := api.UploadSubject{
		Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Release:    api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
		Type:       "WEBDL",
		VideoCodec: "H.264",
	}

	daily := base
	daily.DailyEpisodeDate = "2026-02-03"
	if got := resolveName(daily); !strings.Contains(got, "Example Show 2026-02-03") || strings.Contains(got, "S01E01") || strings.Contains(got, "(0)") {
		t.Fatalf("daily name = %q", got)
	}

	if got := resolveName(base); strings.Contains(got, "S01E01") || strings.Contains(got, "(0)") {
		t.Fatalf("cleared-facts name = %q", got)
	}
}
