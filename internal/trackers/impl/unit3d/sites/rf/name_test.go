// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rf

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildNameKeepsUnknownGroupElementBlank(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "missing group",
			meta: api.UploadSubject{
				ReleaseName: "Example Release 2026 1080p WEB-DL",
			},
			want: "Example Release 2026 1080p WEB-DL",
		},
		{
			name: "unknown parsed group",
			meta: api.UploadSubject{
				ReleaseNameNoTag: "Example Release S01E01 1080p WEB-DL",
				Tag:              "unknown",
			},
			want: "Example Release S01E01 1080p WEB-DL",
		},
		{
			name: "known source group remains",
			meta: api.UploadSubject{
				ReleaseName: "Example Release 2026 1080p WEB-DL-GRP",
				Tag:         "GRP",
			},
			want: "Example Release 2026 1080p WEB-DL-GRP",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildName(tc.meta, config.TrackerConfig{}); got != tc.want {
				t.Fatalf("buildName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProfileBuildNameVersion(t *testing.T) {
	t.Parallel()

	if got := Profile().Site.BuildNameVersion; got != "v2" {
		t.Fatalf("BuildNameVersion = %q, want %q", got, "v2")
	}
}
