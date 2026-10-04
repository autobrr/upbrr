// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package seasonep

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
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
			name: "year numbered season episode",
			path: "Example.Show.S2026E03.1080p.WEB-DL.mkv",
			want: Result{Season: 2026, Episode: 3},
		},
		{
			name: "year numbered season pack",
			path: "Example.Show.S2026.1080p.WEB-DL",
			want: Result{Season: 2026, TVPack: true},
		},
		{
			name: "year numbered alternate episode rejected",
			path: "Example.Show.2026x03.1080p.WEB-DL.mkv",
		},
		{
			name: "oversized season episode is not truncated",
			path: "Example.Show.S20260E03.1080p.mkv",
		},
		{
			name: "oversized season pack is not truncated",
			path: "Example.Show.S20260.1080p",
		},
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

func TestParseSeasonEpisodeInstruction(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		name  string
		parse func(string) (int, error)
		value string
		want  int
	}{
		{
			name:  "empty season clears",
			parse: ParseSeasonInstruction,
			value: "",
			want:  0,
		},
		{
			name:  "blank season clears",
			parse: ParseSeasonInstruction,
			value: "  ",
			want:  0,
		},
		{
			name:  "padded season",
			parse: ParseSeasonInstruction,
			value: "05",
			want:  5,
		},
		{
			name:  "prefixed season",
			parse: ParseSeasonInstruction,
			value: "S05",
			want:  5,
		},
		{
			name:  "lowercase prefixed season",
			parse: ParseSeasonInstruction,
			value: "s05",
			want:  5,
		},
		{
			name:  "year numbered season",
			parse: ParseSeasonInstruction,
			value: "2026",
			want:  2026,
		},
		{
			name:  "prefixed year numbered season",
			parse: ParseSeasonInstruction,
			value: "S2026",
			want:  2026,
		},
		{
			name:  "four digit season boundary",
			parse: ParseSeasonInstruction,
			value: "9999",
			want:  9999,
		},
		{
			name:  "two digit season",
			parse: ParseSeasonInstruction,
			value: "99",
			want:  99,
		},
		{
			name:  "empty episode clears",
			parse: ParseEpisodeInstruction,
			value: "",
			want:  0,
		},
		{
			name:  "bare episode",
			parse: ParseEpisodeInstruction,
			value: "7",
			want:  7,
		},
		{
			name:  "padded episode",
			parse: ParseEpisodeInstruction,
			value: "07",
			want:  7,
		},
		{
			name:  "prefixed episode",
			parse: ParseEpisodeInstruction,
			value: "E07",
			want:  7,
		},
		{
			name:  "lowercase prefixed episode",
			parse: ParseEpisodeInstruction,
			value: "e7",
			want:  7,
		},
		{
			name:  "three digit episode",
			parse: ParseEpisodeInstruction,
			value: "999",
			want:  999,
		},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.parse(tt.value)
			if err != nil || got != tt.want {
				t.Fatalf("parse(%q) = %d, %v, want %d", tt.value, got, err, tt.want)
			}
		})
	}

	rejected := []struct {
		name  string
		parse func(string) (int, error)
		value string
	}{
		{
			name:  "combined token",
			parse: ParseSeasonInstruction,
			value: "S01E05",
		},
		{
			name:  "season range",
			parse: ParseSeasonInstruction,
			value: "S01-S02",
		},
		{
			name:  "alt notation",
			parse: ParseSeasonInstruction,
			value: "1x05",
		},
		{
			name:  "zero season",
			parse: ParseSeasonInstruction,
			value: "0",
		},
		{
			name:  "zero prefixed season",
			parse: ParseSeasonInstruction,
			value: "S00",
		},
		{
			name:  "season overflow",
			parse: ParseSeasonInstruction,
			value: "10000",
		},
		{
			name:  "garbage season",
			parse: ParseSeasonInstruction,
			value: "abc",
		},
		{
			name:  "prefix only",
			parse: ParseSeasonInstruction,
			value: "S",
		},
		{
			name:  "negative season",
			parse: ParseSeasonInstruction,
			value: "-1",
		},
		{
			name:  "episode prefix on season",
			parse: ParseSeasonInstruction,
			value: "E05",
		},
		{
			name:  "inner space",
			parse: ParseSeasonInstruction,
			value: "S 05",
		},
		{
			name:  "episode range",
			parse: ParseEpisodeInstruction,
			value: "E01-E03",
		},
		{
			name:  "combined episodes",
			parse: ParseEpisodeInstruction,
			value: "E01E02",
		},
		{
			name:  "zero episode",
			parse: ParseEpisodeInstruction,
			value: "E00",
		},
		{
			name:  "episode overflow",
			parse: ParseEpisodeInstruction,
			value: "1000",
		},
		{
			name:  "season prefix on episode",
			parse: ParseEpisodeInstruction,
			value: "S05",
		},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.parse(tt.value)
			if err == nil {
				t.Fatalf("parse(%q) = %d, want rejection", tt.value, got)
			}
			if !errors.Is(err, internalerrors.ErrInvalidInput) {
				t.Fatalf("parse(%q) error = %v, want typed invalid input", tt.value, err)
			}
		})
	}
}

func TestExtractSeasonTokenWidths(t *testing.T) {
	t.Parallel()
	for _, token := range []string{"768x576", "1920x800", "1x05", "01x05", "2026x03", "S1E03", "S123E03", "S12345E03", "S1", "S123", "Season 1", "Series 123", "S1E03E04", "S123E03E04"} {
		t.Run(token, func(t *testing.T) {
			got := Extract("Example.Show."+token+".1080p.mkv", preparationstate.State{})
			if !reflect.DeepEqual(got, Result{}) {
				t.Fatalf("rejected token %q produced %+v", token, got)
			}
		})
	}
	for _, token := range []string{"Season 01", "Series 2026"} {
		t.Run(token, func(t *testing.T) {
			got := Extract("Example.Show."+token+".1080p", preparationstate.State{})
			if got.Season == 0 || !got.TVPack {
				t.Fatalf("valid season pack %q produced %+v", token, got)
			}
		})
	}
	got := Extract("Example.Show.S2026E03E04.1080p.mkv", preparationstate.State{})
	if got.Season != 2026 || !reflect.DeepEqual(got.MultiEpisode, []int{3, 4}) {
		t.Fatalf("year-numbered multi episode produced %+v", got)
	}
}

func TestParseSeasonInstructionRejectsInvalidWidths(t *testing.T) {
	t.Parallel()
	for _, token := range []string{"1", "S1", "123", "S123", "000", "S001"} {
		if _, err := ParseSeasonInstruction(token); !errors.Is(err, internalerrors.ErrInvalidInput) {
			t.Errorf("ParseSeasonInstruction(%q) error = %v, want invalid input", token, err)
		}
	}
}

func TestExtractIgnoredTokensPreservePackAndEpisodeEvidence(t *testing.T) {
	t.Parallel()
	files := []string{"Example.Show.S02E03.mkv", "Example.Show.S02E04.mkv"}
	for _, token := range []string{"1x05", "S1E03", "768x576"} {
		t.Run(token, func(t *testing.T) {
			got := Extract("Example.Show."+token, preparationstate.State{VideoPath: files[0], FileList: files})
			if got.Season != 2 || got.Episode != 0 || !got.TVPack {
				t.Fatalf("rejected primary token overrode pack evidence: %+v", got)
			}
			got = Extract("Example.Show."+token+".S02E04.mkv", preparationstate.State{})
			if got.Season != 2 || got.Episode != 4 || got.TVPack {
				t.Fatalf("rejected token overrode valid episode: %+v", got)
			}
		})
	}
}

func TestFormatSeasonInstructionRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		token string
		want  string
	}{
		{"99", "S99"}, {"0100", "S0100"}, {"0999", "S0999"}, {"1000", "S1000"},
	} {
		t.Run(tc.token, func(t *testing.T) {
			season, err := ParseSeasonInstruction(tc.token)
			if err != nil {
				t.Fatal(err)
			}
			formatted := FormatSeason(season)
			if formatted != tc.want {
				t.Fatalf("FormatSeason(%d) = %q, want %q", season, formatted, tc.want)
			}
			if got, err := ParseSeasonInstruction(formatted); err != nil || got != season {
				t.Fatalf("season did not round-trip: %d, %v", got, err)
			}
		})
	}
}
