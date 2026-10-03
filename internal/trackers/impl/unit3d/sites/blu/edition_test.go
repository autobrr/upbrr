// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package blu

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCategoryIDPreservesStructuredEditionParts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		edition string
		want    string
	}{
		{"Collector's", "1"},
		{"FANRES", "3"},
	} {
		t.Run(test.edition, func(t *testing.T) {
			legacy := api.UploadSubject{
				Identity:       api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				AudioLanguages: []string{"English"},
				Edition:        "Extended " + test.edition + " Open Matte",
			}
			structured := legacy
			structured.Cut, structured.Edition, structured.Presentation = "Extended", test.edition, "Open Matte"
			if old, got := categoryID(legacy), categoryID(structured); old != test.want || got != old {
				t.Fatalf("legacy = %q, structured = %q, want %q", old, got, test.want)
			}
		})
	}
}
