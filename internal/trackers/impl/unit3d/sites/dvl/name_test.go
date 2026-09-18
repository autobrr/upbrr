// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
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
			name: "DVD encode omits edition without changing title",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "Uncut Shadows",
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "ENCODE",
				Source:      "PAL DVD",
				VideoEncode: " x264",
				Audio:       "DD 2.0",
				Edition:     "Uncut",
				Repack:      "REPACK",
				Tag:         "-GRP",
			},
			want: "Uncut Shadows 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip preserves edition title with DVD encode parity",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "Uncut Shadows",
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: " x264",
				Audio:       "DD 2.0",
				Edition:     "Uncut",
				Repack:      "REPACK",
				Tag:         "-GRP",
			},
			want: "Uncut Shadows 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip preserves repack title and edition alternate title",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "REPACK Shadows",
					Year:       2001,
					Resolution: "480p",
				},
				AlternateTitle: "AKA Uncut Nights",
				Type:           "DVDRIP",
				Source:         "PAL DVD",
				VideoEncode:    "x264",
				Audio:          "DD 2.0",
				Edition:        "Uncut",
				Repack:         "REPACK",
				Tag:            "-GRP",
			},
			want: "REPACK Shadows AKA Uncut Nights 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "TV DVDRip keeps codec after audio",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "TV",
					Title:      "Example Show",
					Resolution: "576p",
				},
				SeasonStr:   "S01",
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
				Tag:         "-GRP",
			},
			want: "Example Show S01 576p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip override keeps codec after audio",
			meta: api.UploadSubject{
				ReleaseName: "Example Show S01 576p DVDRip DD 2.0 x264-GRP",
				Release:     api.ReleaseInfo{Resolution: "576p"},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
			},
			want: "Example Show S01 576p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip typed as a DVD encode omits repack",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "REPACK3 Shadows",
					Year:       1994,
					Resolution: "480p",
				},
				Type:        "ENCODE",
				Source:      "DVD",
				VideoEncode: "x264",
				Audio:       "DD 5.1",
				Repack:      "REPACK3",
				Tag:         "-GRP",
			},
			want: "REPACK3 Shadows 1994 480p DVDRip DD 5.1 x264-GRP",
		},
		{
			name: "DVDRip typed as a DVD encode",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "Example Movie",
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "ENCODE",
				Source:      "DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
				Tag:         "-GRP",
			},
			want: "Example Movie 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "German DVDRip typed as a DVD encode",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "TV",
					Title:      "Example Show",
					Resolution: "480p",
				},
				SeasonStr:      "S04",
				EpisodeStr:     "E03",
				EpisodeTitle:   "Title",
				Type:           "ENCODE",
				Source:         "NTSC DVD",
				VideoEncode:    "x264",
				Audio:          "DD 2.0",
				AudioLanguages: []string{"German"},
				Tag:            "-GRP",
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
			name: "DVDRip title containing the encode value",
			meta: api.UploadSubject{
				ReleaseName: "x264 and x264 Tales 2001 PAL DVD x264 DVDRip DD 2.0-GRP",
				Release: api.ReleaseInfo{
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
			},
			want: "x264 and x264 Tales 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip title containing the audio value",
			meta: api.UploadSubject{
				ReleaseName: "DD 2.0 Tales 2001 PAL DVD x264 DVDRip DD 2.0-GRP",
				Release: api.ReleaseInfo{
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
			},
			want: "DD 2.0 Tales 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip title containing both encode and audio values",
			meta: api.UploadSubject{
				ReleaseName: "Example x264 and DD 2.0 Tales 2001 PAL DVD x264 DVDRip DD 2.0-GRP",
				Release: api.ReleaseInfo{
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
			},
			want: "Example x264 and DD 2.0 Tales 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVDRip title containing the encode and DVDRip sequence",
			meta: api.UploadSubject{
				ReleaseName: "Example x264 DVDRip Tales 2001 PAL DVD x264 DVDRip DD 2.0-GRP",
				Release: api.ReleaseInfo{
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "DVDRIP",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
			},
			want: "Example x264 DVDRip Tales 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVD encode title containing resolution and source",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "480p DVD Tales",
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "ENCODE",
				Source:      "DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
				Tag:         "-GRP",
			},
			want: "480p DVD Tales 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "BluRay encode title containing DVD resolution and source",
			meta: api.UploadSubject{
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "480p DVD Tales",
					Year:       2001,
					Resolution: "480p",
				},
				Type:        "ENCODE",
				Source:      "BluRay",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
				Tag:         "-GRP",
			},
			want: "480p DVD Tales 2001 480p BluRay DD 2.0 x264-GRP",
		},
		{
			name: "DVD full disc with a DVD-suffixed source",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 2001 R1 NTSC DVD9 DD 5.1-GRP",
				Release: api.ReleaseInfo{
					Year:       2001,
					Resolution: "480p",
					Size:       "DVD9",
				},
				Type:       "DISC",
				DiscType:   "DVD",
				Source:     "NTSC DVD",
				Region:     "R1",
				VideoCodec: "MPEG-2",
				Audio:      "DD 5.1",
			},
			want: "Example Movie 2001 R1 NTSC DVD9 DD 5.1-GRP",
		},
		{
			name: "DVD full disc without region or system",
			meta: api.UploadSubject{
				ReleaseName: "Example Movie 2001 DVD9 DD 5.1-GRP",
				Release: api.ReleaseInfo{
					Year:       2001,
					Resolution: "576p",
					Size:       "DVD9",
				},
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
				Release: api.ReleaseInfo{
					Year:       1997,
					Resolution: "576p",
					Size:       "DVD5",
				},
				Type:       "DISC",
				DiscType:   "DVD",
				Source:     "DVD",
				VideoCodec: "MPEG-2",
				Audio:      "DD 5.1",
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
				Release: api.ReleaseInfo{
					Category:   "MOVIE",
					Title:      "Example Movie",
					Year:       1984,
					Resolution: "480p",
				},
				Type:           "ENCODE",
				Source:         "DVD",
				VideoEncode:    "x264",
				Audio:          "DD 2.0",
				AudioLanguages: []string{"zxx"},
				Tag:            "-GRP",
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
			if tt.meta.ReleaseName == "" {
				tt.meta.ReleaseName = metadata.BuildReleaseName(api.ReleaseNameRequest{
					Category:     tt.meta.Release.Category,
					Type:         tt.meta.Type,
					Title:        tt.meta.Release.Title,
					AltTitle:     tt.meta.AlternateTitle,
					Year:         tt.meta.Release.Year,
					Resolution:   tt.meta.Release.Resolution,
					Audio:        tt.meta.Audio,
					Season:       tt.meta.SeasonStr,
					Episode:      tt.meta.EpisodeStr,
					EpisodeTitle: tt.meta.EpisodeTitle,
					Repack:       tt.meta.Repack,
					Tag:          tt.meta.Tag,
					Source:       tt.meta.Source,
					VideoCodec:   tt.meta.VideoCodec,
					VideoEncode:  tt.meta.VideoEncode,
					Edition:      tt.meta.Edition,
				}, nil).Name
			}
			if got := profile.BuildName(tt.meta, config.TrackerConfig{}); got != tt.want {
				t.Fatalf("name = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInsertAfterLast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		token  string
		suffix string
		want   string
	}{
		{
			name:   "last match before release group",
			input:  "DD 2.0 Tales DD 2.0-GRP",
			token:  "DD 2.0",
			suffix: "x264",
			want:   "DD 2.0 Tales DD 2.0 x264-GRP",
		},
		{
			name:   "token spans name",
			input:  "DD 2.0",
			token:  "DD 2.0",
			suffix: "x264",
			want:   "DD 2.0 x264",
		},
		{
			name:   "no match",
			input:  "Example FLAC 2.0-GRP",
			token:  "DD 2.0",
			suffix: "x264",
			want:   "Example FLAC 2.0-GRP",
		},
		{
			name:   "empty token",
			input:  "Example DD 2.0-GRP",
			suffix: "x264",
			want:   "Example DD 2.0-GRP",
		},
		{
			name:  "empty suffix",
			input: "Example DD 2.0-GRP",
			token: "DD 2.0",
			want:  "Example DD 2.0-GRP",
		},
		{
			name:   "skip trailing match without preceding boundary",
			input:  "Example DD 2.0 EDD 2.0-GRP",
			token:  "DD 2.0",
			suffix: "x264",
			want:   "Example DD 2.0 x264 EDD 2.0-GRP",
		},
		{
			name:   "skip trailing match without following boundary",
			input:  "Example DD 2.0 DD 2.00-GRP",
			token:  "DD 2.0",
			suffix: "x264",
			want:   "Example DD 2.0 x264 DD 2.00-GRP",
		},
		{
			name:   "skip partial match overlapping a whole token",
			input:  "DD DD DDX",
			token:  "DD DD",
			suffix: "x264",
			want:   "DD DD x264 DDX",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := insertAfterLast(tt.input, tt.token, tt.suffix); got != tt.want {
				t.Fatalf("name = %q, want %q", got, tt.want)
			}
		})
	}
}
