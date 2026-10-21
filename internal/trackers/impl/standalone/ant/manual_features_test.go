// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestExplicitPresentationNoneOverridesOpaqueEdition(t *testing.T) {
	subject := api.UploadSubject{Edition: "Custom IMAX Open Matte 4K Remaster wording", ReleaseNameOverrides: api.ReleaseNameOverrides{Presentation: new(""), ReleaseFeatures: new([]api.ReleaseFeature{})}}
	got := strings.Join(resolveFlags(subject), " ")
	if strings.Contains(got, "IMAX") || strings.Contains(got, "Open Matte") || strings.Contains(got, "Remaster") {
		t.Fatalf("None restored opaque markers: %q", got)
	}
}
