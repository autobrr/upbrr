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
