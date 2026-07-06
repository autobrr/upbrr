// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDetectSeasonPackGroupTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		meta       api.PreparedMetadata
		wantMixed  bool
		wantGroups []string
	}{
		{
			name: "mixed season pack groups",
			meta: api.PreparedMetadata{
				TVPack: true,
				FileList: []string{
					"Example.Show.S01E01.1080p.WEB-DL.x264-GRP.mkv",
					"Example.Show.S01E02.1080p.WEB-DL.x264-ALT.mkv",
				},
			},
			wantMixed:  true,
			wantGroups: []string{"ALT", "GRP"},
		},
		{
			name: "same season pack groups",
			meta: api.PreparedMetadata{
				TVPack: true,
				FileList: []string{
					"Example.Show.S01E01.1080p.WEB-DL.x264-GRP.mkv",
					"Example.Show.S01E02.1080p.WEB-DL.x264-GRP.mkv",
				},
			},
			wantMixed: false,
		},
		{
			name: "mixed groups ignored outside season packs",
			meta: api.PreparedMetadata{
				FileList: []string{
					"Example.Show.S01E01.1080p.WEB-DL.x264-GRP.mkv",
					"Example.Show.S01E02.1080p.WEB-DL.x264-ALT.mkv",
				},
			},
			wantMixed: false,
		},
		{
			name: "unknown groups ignored",
			meta: api.PreparedMetadata{
				TVPack: true,
				FileList: []string{
					"Example.Show.S01E01.1080p.WEB-DL.x264-GRP.mkv",
					"Example.Show.S01E02.1080p.WEB-DL.x264-NOGRP.mkv",
				},
			},
			wantMixed: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := DetectSeasonPackGroupTags(tc.meta)
			if got.Mixed != tc.wantMixed {
				t.Fatalf("expected mixed=%t, got %t", tc.wantMixed, got.Mixed)
			}
			if !reflect.DeepEqual(got.Groups, tc.wantGroups) {
				t.Fatalf("expected groups %#v, got %#v", tc.wantGroups, got.Groups)
			}
			if tc.wantMixed && !strings.Contains(got.Notice, "trackers with mixed-origin support will use Mixed") {
				t.Fatalf("expected mixed-origin notice, got %q", got.Notice)
			}
		})
	}
}
