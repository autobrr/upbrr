// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

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
		}, "Extended Edition / Collector's Open Matte"},
		{"presentation only", "Open Matte", api.UploadSubject{Presentation: "Open Matte"}, "Open Matte"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			legacy := resolveRemasterTitle(api.UploadSubject{Edition: test.legacy})
			structured := resolveRemasterTitle(test.structured)
			if legacy != test.want || structured != legacy {
				t.Fatalf("legacy = %q, structured = %q, want %q", legacy, structured, test.want)
			}
		})
	}
}
