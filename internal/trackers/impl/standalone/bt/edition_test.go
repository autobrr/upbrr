// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestEditionLabelPreservesStructuredParts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		legacy     string
		structured api.UploadSubject
		want       string
	}{
		{"mixed", "Extended Collector's Open Matte", api.UploadSubject{
			Cut:          "Extended",
			Edition:      "Collector's",
			Presentation: "Open Matte",
		}, "Extended"},
		{"presentation only", "IMAX", api.UploadSubject{Presentation: "IMAX"}, "IMAX"},
		{"generic remaster", "Remastered Version", api.UploadSubject{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureRemaster}}, "Remastered"},
		{"4K remaster", "4K Remaster", api.UploadSubject{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature4KRemaster}}, "Remastered"},
		{"cut before remaster", "Director's Cut 4K Remaster", api.UploadSubject{
			Cut:             "Director's Cut",
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature4KRemaster},
		}, "Director's Cut"},
		{"presentation before remaster", "IMAX 4K Remaster", api.UploadSubject{
			Presentation:    "IMAX",
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature4KRemaster},
		}, "IMAX"},
		{"restoration is not remaster", "", api.UploadSubject{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature4KRestoration}}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			legacy := resolveEdition(api.UploadSubject{Edition: test.legacy})
			structured := resolveEdition(test.structured)
			if legacy != test.want || structured != legacy {
				t.Fatalf("legacy = %q, structured = %q, want %q", legacy, structured, test.want)
			}
		})
	}
}
