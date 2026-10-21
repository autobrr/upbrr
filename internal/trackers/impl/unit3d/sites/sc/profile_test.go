// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sc

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSCTypeMapping(t *testing.T) {
	tests := []struct {
		name     string
		subject  api.UploadSubject
		expected string
	}{
		{
			name: "TV pack",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				TVPack: true,
			},
			expected: "21",
		},
		{
			name: "TV episode",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryTV,
				},
				TVPack: false,
			},
			expected: "25",
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
			expected: "20",
		},
		{
			name: "Movie HD",
			subject: api.UploadSubject{
				Identity: api.ExternalIdentity{
					Category: api.CanonicalCategoryMovie,
				},
				Release: api.ReleaseInfo{
					Resolution: "1080p",
				},
			},
			expected: "12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := typeID(tt.subject)
			if got != tt.expected {
				t.Errorf("typeID() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSCBannedGroups(t *testing.T) {
	groups := BannedGroups()
	if len(groups) == 0 {
		t.Fatal("expected non-empty banned groups list")
	}
	if !slices.Contains(groups, "YTS") {
		t.Error("expected YTS in banned groups")
	}
	if !slices.Contains(groups, "RARBG") {
		t.Error("expected RARBG in banned groups")
	}
}
