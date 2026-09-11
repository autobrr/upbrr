// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildName(t *testing.T) {
	t.Parallel()

	profile := Profile().Site
	if profile.BuildNameVersion != "v1" || profile.BuildName == nil {
		t.Fatal("DVL requires its v1 name builder")
	}
	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "DVDRip omits edition",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 1999 Directors Cut PAL DVD x264 DVDRip DD 2.0-GRP",
				Release:     api.ReleaseInfo{Year: 1999, Resolution: "480p"},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: " x264",
				Audio:       "DD 2.0",
				Edition:     "Directors Cut",
			},
			want: "Example Movie 1999 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip typed as a DVD encode omits repack",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 1994 REPACK3 480p DVD DD 5.1 x264-GRP",
				Release:     api.ReleaseInfo{Year: 1994, Resolution: "480p"},
				Type:        "ENCODE",
				Source:      "DVD",
				VideoEncode: "x264",
				Audio:       "DD 5.1",
				Repack:      "REPACK3",
			},
			want: "Example Movie 1994 480p DVDRip DD 5.1 x264-GRP",
		},
		{
			name: "DVDRip typed as a DVD encode",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 2001 480p DVD DD 2.0 x264-GRP",
				Release:     api.ReleaseInfo{Year: 2001, Resolution: "480p"},
				Type:        "ENCODE",
				Source:      "DVDRiP",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
			},
			want: "Example Movie 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "German DVDRip typed as a DVD encode",
			meta: api.UploadSubject{
				ReleaseName:    "Example Show S04E03 Title 480p NTSC DVD DD 2.0 x264-GRP",
				Release:        api.ReleaseInfo{Resolution: "480p"},
				Type:           "ENCODE",
				Source:         "NTSC DVD",
				VideoEncode:    "x264",
				Audio:          "DD 2.0",
				AudioLanguages: []string{"German"},
			},
			want: "Example Show S04E03 Title GERMAN 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip title containing the source word",
			meta: api.UploadSubject{
				ReleaseName: "PAL DVD Massacre 2001 PAL DVD x264 DVDRip DD 2.0-GRP",
				Release:     api.ReleaseInfo{Year: 2001, Resolution: "480p"},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: " x264",
				Audio:       "DD 2.0",
			},
			want: "PAL DVD Massacre 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVD full disc with a DVD-suffixed source",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 2001 R1 NTSC DVD9 DD 5.1-GRP",
				Release:     api.ReleaseInfo{Year: 2001, Resolution: "480p", Size: "DVD9"},
				Type:        "DISC",
				DiscType:    "DVD",
				Source:      "NTSC DVD",
				Region:      "R1",
				VideoCodec:  "MPEG-2",
				Audio:       "DD 5.1",
			},
			want: "Example Movie 2001 R1 NTSC DVD9 DD 5.1-GRP",
		},
		{
			name: "DVD full disc without region or system",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 2001 DVD9 DD 5.1-GRP",
				Release:        api.ReleaseInfo{Year: 2001, Resolution: "576p", Size: "DVD9"},
				Type:           "DISC",
				DiscType:       "DVD",
				Source:         "DVD",
				VideoCodec:     "MPEG-2",
				Audio:          "DD 5.1",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Movie 2001 JAPANESE PAL DVD9 DD 5.1-GRP",
		},
		{
			name: "DVD full disc derives PAL from resolution",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 1997 DVD5 DD 5.1-GRP",
				Release:     api.ReleaseInfo{Year: 1997, Resolution: "576p", Size: "DVD5"},
				Type:        "DISC",
				DiscType:    "DVD",
				Source:      "DVD",
				VideoCodec:  "MPEG-2",
				Audio:       "DD 5.1",
			},
			want: "Example Movie 1997 PAL DVD5 DD 5.1-GRP",
		},
		{
			name: "Hi10P DVDRip",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 2001 PAL DVD Hi10P x264 DVDRip DD 2.0-GRP",
				Release:     api.ReleaseInfo{Year: 2001, Resolution: "480p"},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: "Hi10P x264",
				Audio:       "DD 2.0",
			},
			want: "Example Movie 2001 480p DVDRip DD 2.0 Hi10P x264-GRP",
		},
		{
			name: "DVD disc",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 2001 R1 NTSC DVD9 DD 5.1-GRP",
				Release: api.ReleaseInfo{
					Year:       2001,
					Resolution: "480p",
					Size:       "DVD9",
				},
				Type:       "DISC",
				DiscType:   "DVD",
				Source:     "NTSC",
				Region:     "R1",
				VideoCodec: "MPEG-2",
				Audio:      "DD 5.1",
			},
			want: "Example Movie 2001 R1 NTSC DVD9 DD 5.1-GRP",
		},
		{
			name: "bare DVD remux PAL",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 2001 DVD REMUX DD 5.1-GRP",
				Release:     api.ReleaseInfo{Year: 2001, Resolution: "576p"},
				Type:        "REMUX",
				Source:      "DVD",
				VideoCodec:  "MPEG-2",
				Audio:       "DD 5.1",
			},
			want: "Example Movie 2001 PAL DVD REMUX DD 5.1-GRP",
		},
		{
			name: "bare DVD remux NTSC",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 2001 DVD REMUX DD 5.1-GRP",
				Release:     api.ReleaseInfo{Year: 2001, Resolution: "480i"},
				Type:        "REMUX",
				Source:      "DVD",
				VideoCodec:  "MPEG-2",
				Audio:       "DD 5.1",
			},
			want: "Example Movie 2001 NTSC DVD REMUX DD 5.1-GRP",
		},
		{
			name: "foreign audio before encode resolution",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 2001 1080p BluRay DD 5.1 x264-GRP",
				Release:        api.ReleaseInfo{Year: 2001, Resolution: "1080p"},
				Type:           "ENCODE",
				Source:         "BluRay",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Movie 2001 JAPANESE 1080p BluRay DD 5.1 x264-GRP",
		},
		{
			name: "non-linguistic DVDRip audio omits marker",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 1984 480p DVD DD 2.0 x264-GRP",
				Release:        api.ReleaseInfo{Year: 1984, Resolution: "480p"},
				Type:           "ENCODE",
				Source:         "DVDRiP",
				VideoEncode:    "x264",
				Audio:          "DD 2.0",
				AudioLanguages: []string{"zxx"},
			},
			want: "Example Movie 1984 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "first linguistic audio supplies Norwegian Nynorsk marker",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 2001 1080p BluRay DD 5.1 x264-GRP",
				Release:        api.ReleaseInfo{Year: 2001, Resolution: "1080p"},
				Type:           "ENCODE",
				Source:         "BluRay",
				AudioLanguages: []string{"zxx", "no linguistic content", "und", "undetermined", "Norwegian Nynorsk"},
			},
			want: "Example Movie 2001 NORWEGIAN NYNORSK 1080p BluRay DD 5.1 x264-GRP",
		},
		{
			name: "BDMV omits foreign audio marker",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 2001 1080p BluRay AVC DD 5.1-GRP",
				Release:        api.ReleaseInfo{Year: 2001, Resolution: "1080p"},
				Type:           "DISC",
				DiscType:       "BDMV",
				Source:         "BluRay",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Movie 2001 1080p BluRay AVC DD 5.1-GRP",
		},
		{
			name: "English audio leaves name unchanged",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 2001 1080p BluRay DD 5.1 x264-GRP",
				Release:        api.ReleaseInfo{Year: 2001, Resolution: "1080p"},
				Type:           "ENCODE",
				Source:         "BluRay",
				AudioLanguages: []string{"Japanese", "English"},
			},
			want: "Example Movie 2001 1080p BluRay DD 5.1 x264-GRP",
		},
		{
			name: "foreign audio DVDRip",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 1999 NTSC DVD x264 DVDRip DD 2.0-GRP",
				Release:        api.ReleaseInfo{Year: 1999, Resolution: "480p"},
				Type:           "DVDRIP",
				Source:         "NTSC DVD",
				VideoEncode:    " x264",
				Audio:          "DD 2.0",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Movie 1999 JAPANESE 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "foreign audio DVD full disc",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 1977 USA NTSC DVD9 LPCM 2.0",
				Release: api.ReleaseInfo{
					Year:       1977,
					Resolution: "480p",
					Size:       "DVD9",
				},
				Type:           "DISC",
				DiscType:       "DVD",
				Source:         "NTSC",
				Region:         "USA",
				VideoCodec:     "MPEG-2",
				Audio:          "LPCM 2.0",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Movie 1977 JAPANESE USA NTSC DVD9 LPCM 2.0",
		},
		{
			name: "foreign audio after year for DVD remux",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 2001 PAL DVD REMUX DD 5.1-GRP",
				Release:        api.ReleaseInfo{Year: 2001, Resolution: "576p"},
				Type:           "REMUX",
				Source:         "PAL DVD",
				VideoCodec:     "MPEG-2",
				Audio:          "DD 5.1",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Movie 2001 JAPANESE PAL DVD REMUX DD 5.1-GRP",
		},
		{
			name: "DVD remux marker follows last year token",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 2001 2001 PAL DVD REMUX DD 5.1-GRP",
				Release:        api.ReleaseInfo{Year: 2001, Resolution: "576p"},
				Type:           "REMUX",
				Source:         "PAL DVD",
				VideoCodec:     "MPEG-2",
				Audio:          "DD 5.1",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Movie 2001 2001 JAPANESE PAL DVD REMUX DD 5.1-GRP",
		},
		{
			name: "DVL override name stays unchanged",
			meta: api.UploadSubject{
				ReleaseName:    "Example Movie 2001 JAPANESE PAL DVD REMUX DD 5.1-GRP",
				Release:        api.ReleaseInfo{Year: 2001, Resolution: "576p"},
				Type:           "REMUX",
				Source:         "DVD",
				VideoCodec:     "MPEG-2",
				Audio:          "DD 5.1",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Movie 2001 JAPANESE PAL DVD REMUX DD 5.1-GRP",
		},
		{
			name: "foreign audio before source for yearless DVD remux",
			meta: api.UploadSubject{
				ReleaseName:      "Example Movie PAL DVD REMUX DD 2.0-GRP",
				Release:          api.ReleaseInfo{Year: 2001, Resolution: "576p"},
				NamePresentation: api.ReleaseNamePresentation{OmitYear: true},
				Type:             "REMUX",
				Source:           "PAL DVD",
				VideoCodec:       "MPEG-2",
				Audio:            "DD 2.0",
				AudioLanguages:   []string{"Japanese"},
			},
			want: "Example Movie JAPANESE PAL DVD REMUX DD 2.0-GRP",
		},
		{
			name: "TV AKA before year with foreign audio",
			meta: api.UploadSubject{
				ReleaseName:    "Example Show 2024 AKA Alt Show S01 PAL DVD REMUX DD 2.0-GRP",
				Release:        api.ReleaseInfo{Year: 2024, Resolution: "576p"},
				Identity:       api.ExternalIdentity{Category: api.CanonicalCategoryTV},
				AlternateTitle: "AKA Alt Show",
				Type:           "REMUX",
				Source:         "PAL DVD",
				VideoCodec:     "MPEG-2",
				Audio:          "DD 2.0",
				AudioLanguages: []string{"Japanese"},
			},
			want: "Example Show AKA Alt Show 2024 JAPANESE S01 PAL DVD REMUX DD 2.0-GRP",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := profile.BuildName(tt.meta, config.TrackerConfig{}); got != tt.want {
				t.Fatalf("name = %q, want %q", got, tt.want)
			}
		})
	}
}
