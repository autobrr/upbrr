// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import "testing"

func TestRemoveEpisodeTitleFromReleaseName(t *testing.T) {
	tests := []struct {
		name         string
		releaseName  string
		episodeTitle string
		want         string
	}{
		{
			name:         "removes dotted token sequence",
			releaseName:  "Example.Show.S01E01.Episode.One.1080p.WEB-DL.x265-GRP",
			episodeTitle: "Episode One",
			want:         "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
		},
		{
			name:         "leaves substring inside larger word",
			releaseName:  "Example.Show.S01E01.Someone.1080p.WEB-DL.x265-GRP",
			episodeTitle: "One",
			want:         "Example.Show.S01E01.Someone.1080p.WEB-DL.x265-GRP",
		},
		{
			name:         "matches punctuation in title as token separators",
			releaseName:  "Example.Show.S01E01.Who.Are.You.1080p.WEB-DL.x265-GRP",
			episodeTitle: "Who Are You?",
			want:         "Example.Show.S01E01.1080p.WEB-DL.x265-GRP",
		},
		{
			name:         "preserves group separator",
			releaseName:  "Example.Show.S01E01.Episode.One-GRP",
			episodeTitle: "Episode One",
			want:         "Example.Show.S01E01-GRP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RemoveEpisodeTitleFromReleaseName(tt.releaseName, tt.episodeTitle); got != tt.want {
				t.Fatalf("RemoveEpisodeTitleFromReleaseName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReleaseNameContainsEpisodeTitle(t *testing.T) {
	if !ReleaseNameContainsEpisodeTitle("Example.Show.S01E01.Episode.One.1080p-GRP", "Episode One") {
		t.Fatalf("expected token sequence match")
	}
	if ReleaseNameContainsEpisodeTitle("Example.Show.S01E01.Someone.1080p-GRP", "One") {
		t.Fatalf("expected substring inside larger word not to match")
	}
}
