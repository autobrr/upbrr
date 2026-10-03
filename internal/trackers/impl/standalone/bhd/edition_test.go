// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestEditionLabelPreservesStructuredParts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		legacy      string
		structured  api.UploadSubject
		wantEdition string
		wantTags    []string
	}{
		{"mixed", "Extended Collector's Open Matte", api.UploadSubject{
			Cut:          "Extended",
			Edition:      "Collector's",
			Presentation: "Open Matte",
		}, "Collector", []string{"OpenMatte"}},
		{"cut only", "Extended", api.UploadSubject{Cut: "Extended"}, "Extended", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, subject := range []api.UploadSubject{{Edition: test.legacy}, test.structured} {
				tags := resolveTags(subject)
				custom, edition := resolveEdition(subject, tags)
				if custom || edition != test.wantEdition || !slices.Equal(tags, test.wantTags) {
					t.Fatalf("edition = %q, custom = %t, tags = %v; want %q, false, %v", edition, custom, tags, test.wantEdition, test.wantTags)
				}
			}
		})
	}
}
