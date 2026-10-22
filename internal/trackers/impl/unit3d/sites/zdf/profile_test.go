// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package zdf

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestZDFCategoryMapping(t *testing.T) {
	tests := []struct {
		name     string
		subject  api.UploadSubject
		expected string
	}{
		{
			name: "Anime",
			subject: api.UploadSubject{
				Anime: true,
			},
			expected: "43",
		},
		{
			name: "TV pack",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				TVPack: true,
			},
			expected: "28",
		},
		{
			name: "TV 4K",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				Release: api.ReleaseInfo{
					Resolution: "2160p",
				},
			},
			expected: "22",
		},
		{
			name: "TV BluRay Remux",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				Release: api.ReleaseInfo{
					Type: "REMUX",
				},
				Source: "BluRay",
			},
			expected: "23",
		},
		{
			name: "TV x265 Encode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				Release: api.ReleaseInfo{
					Type:       "ENCODE",
					Resolution: "1080p",
				},
				VideoCodec: "HEVC",
			},
			expected: "31",
		},
		{
			name: "TV 1080p HEVC BluRay encode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				Source: "BluRay",
				Release: api.ReleaseInfo{
					Type:       "ENCODE",
					Resolution: "1080p",
				},
				VideoCodec: "HEVC",
			},
			expected: "31",
		},
		{
			name: "TV x264 Encode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				Release: api.ReleaseInfo{
					Type:       "ENCODE",
					Resolution: "1080p",
				},
				VideoCodec: "AVC",
			},
			expected: "30",
		},
		{
			name: "TV SD 480p",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				Release: api.ReleaseInfo{
					Resolution: "480p",
				},
			},
			expected: "21",
		},
		{
			name: "TV SD 480p Encode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				Release: api.ReleaseInfo{
					Type:       "ENCODE",
					Resolution: "480p",
				},
				VideoCodec: "AVC",
			},
			expected: "21",
		},
		{
			name: "TV WEB Default",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
			},
			expected: "59",
		},
		{
			name: "Movie 4K",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Release: api.ReleaseInfo{
					Resolution: "2160p",
				},
			},
			expected: "10",
		},
		{
			name: "Movie Full Disc",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				DiscType: "BDMV",
			},
			expected: "12",
		},
		{
			name: "Movie BluRay Remux",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Release: api.ReleaseInfo{
					Type: "REMUX",
				},
				Source: "BluRay",
			},
			expected: "11",
		},
		{
			name: "Movie x265 Encode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Release: api.ReleaseInfo{
					Type:       "ENCODE",
					Resolution: "1080p",
				},
				VideoCodec: "HEVC",
			},
			expected: "19",
		},
		{
			name: "Movie 1080p HEVC BluRay encode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Source: "BluRay",
				Release: api.ReleaseInfo{
					Type:       "ENCODE",
					Resolution: "1080p",
				},
				VideoCodec: "HEVC",
			},
			expected: "19",
		},
		{
			name: "Movie x264 Encode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Release: api.ReleaseInfo{
					Type:       "ENCODE",
					Resolution: "1080p",
				},
				VideoCodec: "AVC",
			},
			expected: "18",
		},
		{
			name: "Movie SD 480p",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Release: api.ReleaseInfo{
					Resolution: "480p",
				},
			},
			expected: "9",
		},
		{
			name: "Movie SD 480p Encode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Release: api.ReleaseInfo{
					Type:       "ENCODE",
					Resolution: "480p",
				},
				VideoCodec: "AVC",
			},
			expected: "9",
		},
		{
			name: "Movie WEB Default",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
			},
			expected: "58",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := categoryID(tt.subject)
			if got != tt.expected {
				t.Errorf("categoryID() = %v, want %v", got, tt.expected)
			}
		})
	}
}
