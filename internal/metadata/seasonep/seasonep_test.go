// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package seasonep

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
)

func TestExtract(t *testing.T) {
	t.Parallel()

	tvPackRoot := t.TempDir()
	tvPackDir := filepath.Join(tvPackRoot, "Show.S01.1080p")
	if err := os.MkdirAll(tvPackDir, 0o755); err != nil {
		t.Fatalf("mkdir tv pack dir: %v", err)
	}
	first := filepath.Join(tvPackDir, "Show.S01E01.mkv")
	second := filepath.Join(tvPackDir, "Show.S01E02.mkv")
	if err := os.WriteFile(first, []byte("1"), 0o600); err != nil {
		t.Fatalf("write first episode: %v", err)
	}
	if err := os.WriteFile(second, []byte("2"), 0o600); err != nil {
		t.Fatalf("write second episode: %v", err)
	}

	multiTokenRoot := t.TempDir()
	multiTokenDir := filepath.Join(multiTokenRoot, "Show.S01")
	if err := os.MkdirAll(multiTokenDir, 0o755); err != nil {
		t.Fatalf("mkdir multi token dir: %v", err)
	}
	multiFirst := filepath.Join(multiTokenDir, "Show.S01E01+E02.mkv")
	multiSecond := filepath.Join(multiTokenDir, "Show.S01E03+E04.mkv")
	if err := os.WriteFile(multiFirst, []byte("1"), 0o600); err != nil {
		t.Fatalf("write first multi episode: %v", err)
	}
	if err := os.WriteFile(multiSecond, []byte("2"), 0o600); err != nil {
		t.Fatalf("write second multi episode: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		meta    preparationstate.State
		want    Result
		wantMul []int
	}{
		{
			name: "standard tv",
			path: "Show.Name.S01E05.1080p.WEB-DL.mkv",
			want: Result{Season: 1, Episode: 5},
		},
		{
			name: "multi episode",
			path: "Show.S01E01E02E03.1080p.mkv",
			want: Result{Season: 1, Episode: 1},
			wantMul: []int{
				1, 2, 3,
			},
		},
		{
			name: "season pack",
			path: tvPackDir,
			meta: preparationstate.State{
				VideoPath: first,
				FileList:  []string{first, second},
			},
			want: Result{Season: 1, TVPack: true},
		},
		{
			name: "season pack keeps pack semantics with multi-token video path",
			path: multiTokenDir,
			meta: preparationstate.State{
				VideoPath: multiFirst,
				FileList:  []string{multiFirst, multiSecond},
			},
			want: Result{Season: 1, TVPack: true},
		},
		{
			name: "daily show",
			path: "Show.2024.01.15.1080p.mkv",
			want: Result{DailyDate: "2024-01-15"},
		},
		{
			name: "anime absolute",
			path: "[SubsPlease] Anime - 43 (1080p).mkv",
			want: Result{Episode: 43, AbsoluteEpisode: 43},
		},
		{
			name: "anime absolute revision",
			path: "[SubsPlease] Anime - 43v2 (1080p).mkv",
			want: Result{Episode: 43, AbsoluteEpisode: 43},
		},
		{
			name: "anime absolute four digit with arbitrary progressive resolution",
			path: "[Group] Long Anime 1001 (1440p).mkv",
			want: Result{Episode: 1001, AbsoluteEpisode: 1001},
		},
		{
			name: "anime season episode",
			path: "[Group] Anime S02E05.mkv",
			want: Result{Season: 2, Episode: 5},
		},
		{
			name: "no match",
			path: "Movie.2024.1080p.BluRay.mkv",
			want: Result{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Extract(tt.path, tt.meta)
			if got.Season != tt.want.Season ||
				got.Episode != tt.want.Episode ||
				got.TVPack != tt.want.TVPack ||
				got.DailyDate != tt.want.DailyDate ||
				got.AbsoluteEpisode != tt.want.AbsoluteEpisode {
				t.Fatalf("unexpected result: got=%+v want=%+v", got, tt.want)
			}
			if !reflect.DeepEqual(got.MultiEpisode, tt.wantMul) {
				t.Fatalf("unexpected multi episodes: got=%v want=%v", got.MultiEpisode, tt.wantMul)
			}
		})
	}
}

func TestFormatSeasonEpisode(t *testing.T) {
	t.Parallel()
	if got := FormatSeason(3); got != "S03" {
		t.Fatalf("expected S03, got %q", got)
	}
	if got := FormatEpisode(9); got != "E09" {
		t.Fatalf("expected E09, got %q", got)
	}
	if got := FormatSeason(0); got != "" {
		t.Fatalf("expected empty season, got %q", got)
	}
}
