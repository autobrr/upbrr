// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveTagsReleaseVersion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ value, want string }{
		{"PROPER", "PROPER"}, {"PROPER2", "PROPER"}, {"PROPER3", "PROPER"},
		{"REPACK", "REPACK"}, {"REPACK2", "REPACK"}, {"REPACK3", "REPACK"},
		{" proper ", "PROPER"}, {"RERIP", ""}, {"", ""}, {"PROPER4", ""},
	} {
		t.Run(test.value, func(t *testing.T) {
			tags := resolveTags(api.UploadSubject{
				Repack:  test.value,
				Edition: "Hybrid Director",
				Type:    "WEBDL",
			})
			want := []string{"WEBDL", "Hybrid"}
			if test.want != "" {
				want = append([]string{test.want}, want...)
			}
			if !slices.Equal(tags, want) {
				t.Fatalf("tags=%q want %q", tags, want)
			}
		})
	}
	tags := resolveTags(api.UploadSubject{Edition: "PROPER"})
	if slices.Contains(tags, "PROPER") {
		t.Fatalf("edition polluted version tags: %q", tags)
	}
}
