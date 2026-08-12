// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveUploadNameBTNRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "scene name preserved",
			meta: api.UploadSubject{
				Scene:       true,
				SceneName:   "Example.Show.S01E01.1080p.WEB-DL.x264-GRp",
				VideoEncode: "x264",
			},
			want: "Example.Show.S01E01.1080p.WEB-DL.x264-GRp",
		},
		{
			name: "daily exact date",
			meta: api.UploadSubject{
				ReleaseName:      "Example.Daily.S01E02.1080p.WEB-DL.H.264-GRP",
				DailyEpisodeDate: "2026-08-08",
				Tag:              "GRP",
			},
			want: "Example.Daily.2026.08.08.1080p.WEB-DL.H.264-GRP",
		},
		{
			name: "ordinary broadcast year omitted",
			meta: api.UploadSubject{
				ReleaseName: "Example.Show.2026.S01E01.1080p.WEB-DL.H.264-GRP",
				Release:     api.ReleaseInfo{Title: "Example Show", Resolution: "1080p"},
				Tag:         "GRP",
			},
			want: "Example.Show.S01E01.1080p.WEB-DL.H.264-GRP",
		},
		{
			name: "series title year retained",
			meta: api.UploadSubject{
				ReleaseName: "Example.Show.2026.S01E01.1080p.WEB-DL.H.264-GRP",
				Release:     api.ReleaseInfo{Title: "Example Show 2026", Resolution: "1080p"},
				Tag:         "GRP",
			},
			want: "Example.Show.2026.S01E01.1080p.WEB-DL.H.264-GRP",
		},
		{
			name: "literal sd removed",
			meta: api.UploadSubject{
				ReleaseName: "Example.Show.S01E01.SD.WEB-DL.H.264-GRP",
				Release:     api.ReleaseInfo{Resolution: "SD"},
				Tag:         "GRP",
			},
			want: "Example.Show.S01E01.WEB-DL.H.264-GRP",
		},
		{
			name: "concrete 480p retained",
			meta: api.UploadSubject{
				ReleaseName: "Example.Show.S01E01.480p.WEB-DL.H.264-GRP",
				Release:     api.ReleaseInfo{Resolution: "480p"},
				Tag:         "GRP",
			},
			want: "Example.Show.S01E01.480p.WEB-DL.H.264-GRP",
		},
		{
			name: "mixed group pack uses BTN",
			meta: api.UploadSubject{
				ReleaseName: "Example.Show.S01.1080p.WEB-DL.H.264-GRP",
				Release:     api.ReleaseInfo{Resolution: "1080p"},
				TVPack:      true,
				Tag:         "GRP",
				FileList: []string{
					"Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv",
					"Example.Show.S01E02.1080p.WEB-DL.H.264-OTHER.mkv",
				},
			},
			want: "Example.Show.S01.1080p.WEB-DL.H.264-BTN",
		},
		{
			name: "anime episode preserves file name",
			meta: api.UploadSubject{
				Anime:    true,
				Filename: "[GRP] Example Show - 01 [ABC123].mkv",
			},
			want: "[GRP] Example Show - 01 [ABC123]",
		},
		{
			name: "extension and invalid characters removed",
			meta: api.UploadSubject{
				ReleaseName: "Example's Show S01E01 1080p WEB-DL H.264-GRP.mkv",
				Release:     api.ReleaseInfo{Resolution: "1080p"},
				Tag:         "GRP",
			},
			want: "Examples.Show.S01E01.1080p.WEB-DL.H.264-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveUploadName(test.meta); got != test.want {
				t.Fatalf("upload name = %q, want %q", got, test.want)
			}
		})
	}
}
