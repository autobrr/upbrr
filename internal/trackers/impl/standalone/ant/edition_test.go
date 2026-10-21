// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestEditionLabelPreservesStructuredParts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		legacy     string
		structured api.UploadSubject
		want       []string
	}{
		{"mixed", "Extended Collector's Open Matte", api.UploadSubject{
			Cut:          "Extended",
			Edition:      "Collector's",
			Presentation: "Open Matte",
		}, []string{"Extended"}},
		{"presentation only", "IMAX", api.UploadSubject{Presentation: "IMAX"}, []string{"IMAX"}},
		{"4K remaster", "4K Remaster", api.UploadSubject{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature4KRemaster}}, []string{"4KRemaster"}},
		{"duplicate 4K remaster", "4K Remaster", api.UploadSubject{
			Edition:         "4K Remaster",
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature4KRemaster, api.ReleaseFeature4KRemaster},
		}, []string{"4KRemaster"}},
		{"cut and remaster", "Director's Cut 4K Remaster", api.UploadSubject{
			Cut:             "Director's Cut",
			ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature4KRemaster},
		}, []string{"Directors", "4KRemaster"}},
		{"restoration is not remaster", "", api.UploadSubject{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeature4KRestoration}}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			legacy := resolveFlags(api.UploadSubject{Edition: test.legacy})
			structured := resolveFlags(test.structured)
			if !slices.Equal(legacy, test.want) || !slices.Equal(structured, legacy) {
				t.Fatalf("legacy = %v, structured = %v, want %v", legacy, structured, test.want)
			}
		})
	}
}

func TestPreparedRemasterPreservesDuplicateSlot(t *testing.T) {
	t.Parallel()
	legacy := api.UploadSubject{
		Type:       "ENCODE",
		Source:     "BluRay",
		VideoCodec: "AVC",
		Release:    api.ReleaseInfo{Resolution: "1080p"},
		Edition:    "4K Remaster",
	}
	structured := legacy
	structured.Edition = ""
	structured.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeature4KRemaster}
	want := "Encode/1080/SDR//4KREMASTER"
	if got := resolveTargetSlot(legacy); got != want {
		t.Fatalf("legacy slot = %q, want %q", got, want)
	}
	if got := resolveTargetSlot(structured); got != want {
		t.Fatalf("prepared feature slot = %q, want %q", got, want)
	}
}
