// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDBMediumIDTypePrecedence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		meta api.DuplicateSubject
		want int
	}{
		{
			name: "metadata type takes precedence",
			meta: api.DuplicateSubject{
				Type:                 "WEBDL",
				ReleaseNameOverrides: api.ReleaseNameOverrides{Type: new("WEBRIP")},
				Release:              api.ReleaseInfo{Type: "REMUX"},
			},
			want: 6,
		},
		{
			name: "falls back to override type",
			meta: api.DuplicateSubject{ReleaseNameOverrides: api.ReleaseNameOverrides{Type: new("WEBRIP")}, Release: api.ReleaseInfo{Type: "REMUX"}},
			want: 3,
		},
		{
			name: "falls back to release type",
			meta: api.DuplicateSubject{Release: api.ReleaseInfo{Type: "REMUX"}},
			want: 5,
		},
		{
			name: "disc type takes precedence",
			meta: api.DuplicateSubject{
				DiscType:             "BDMV",
				Type:                 "WEBDL",
				ReleaseNameOverrides: api.ReleaseNameOverrides{Type: new("WEBRIP")},
				Release:              api.ReleaseInfo{Type: "REMUX"},
			},
			want: 1,
		},
		{
			name: "hdtv with encode settings maps to encode medium",
			meta: api.DuplicateSubject{Type: "HDTV", HasEncodeSettings: true},
			want: 3,
		},
		{
			name: "category type infers encode medium from metadata",
			meta: api.DuplicateSubject{
				Type:       "movie",
				Source:     "BluRay",
				VideoCodec: "AVC",
				Release:    api.ReleaseInfo{Resolution: "1080p"},
			},
			want: 3,
		},
		{
			name: "category type uses source hint for webdl medium",
			meta: api.DuplicateSubject{Type: "movie", Source: "Web-DL"},
			want: 6,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hdbDupeMediumID(tc.meta); got != tc.want {
				t.Fatalf("hdbDupeMediumID() = %d, want %d", got, tc.want)
			}
		})
	}
}
