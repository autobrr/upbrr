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
