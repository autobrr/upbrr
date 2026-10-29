// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func prepareDryRun(ctx context.Context, input trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	input.Intent = trackers.PreparationIntentDryRun
	plan, failure := New().Prepare(ctx, input)
	if failure != nil {
		return api.TrackerDryRunEntry{}, failure
	}
	return plan.DryRun(), nil
}

func TestAudioAnalysisPrecedesScreenshots(t *testing.T) {
	t.Parallel()
	got := buildDescription(api.UploadSubject{ReleaseName: "Example.Release.2026"}, "", trackers.DescriptionAssets{
		Description: "Notes\n\n[spoiler=Audio Plots]\n[img]https://images.example.invalid/audio.png[/img]\n[/spoiler]",
		Screenshots: []api.ScreenshotImage{{RawURL: "https://images.example.invalid/shot.png", ImgURL: "https://images.example.invalid/shot-thumb.png"}},
	})
	if strings.Index(got, "Notes") >= strings.Index(got, "audio.png") || strings.Index(got, "audio.png") >= strings.Index(got, "shot-thumb.png") {
		t.Fatalf("audio analysis placement = %q", got)
	}
}

func TestBuildDescriptionRemovesKnownSignatures(t *testing.T) {
	for _, footer := range []string{"[right]Created by Upload Assistant[/right]", "[img]https://files.catbox.moe/5izwmx.svg[/img]"} {
		for _, notes := range []string{"", "[b]Release notes[/b]\n"} {
			input := notes + footer
			got := buildDescription(api.UploadSubject{ReleaseName: "Example"}, "", trackers.DescriptionAssets{Description: input})
			if strings.Contains(got, "Upload Assistant") || strings.Contains(got, "5izwmx.svg") || notes != "" && !strings.Contains(got, "[b]Release notes[/b]") {
				t.Fatalf("unexpected cleaned description %q", got)
			}
			if final := buildDescription(api.UploadSubject{}, "", trackers.DescriptionAssets{Description: input, Final: true}); final != input {
				t.Fatalf("final description changed: %q", final)
			}
		}
	}
}

func TestBuildDescriptionConvertsCenterTags(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single section",
			input: "[center]Example description[/center]",
			want:  "[align=center]Example description[/align]",
		},
		{
			name: "multiple sections and unrelated formatting",
			input: "[center][b]First & second[/b]\nLine two[/center]\n" +
				"[align=center][i]Already aligned[/i][/align]\n" +
				"[left]Left[/left][right]Right[/right]\n" +
				"[center][url=https://images.example.invalid/view][img]https://images.example.invalid/image.png[/img][/url][/center]",
			want: "[align=center][b]First & second[/b]\nLine two[/align]\n" +
				"[align=center][i]Already aligned[/i][/align]\n" +
				"[left]Left[/left][right]Right[/right]\n" +
				"[align=center][url=https://images.example.invalid/view][img]https://images.example.invalid/image.png[/img][/url][/align]",
		},
		{
			name: "literal code and pre blocks",
			input: "[center]Before [CoDe][center]Literal\n[/center][/CoDe][/center]\n" +
				"[pre][center]Literal pre[/center][/pre]\n[center]After[/center]",
			want: "[align=center]Before [CoDe][center]Literal\n[/center][/CoDe][/align]\n" +
				"[pre][center]Literal pre[/center][/pre]\n[align=center]After[/align]",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, final := range []bool{false, true} {
				want := tt.want
				if !final {
					want = "[color=green][size=6]Example.Release.2026[/size][/color]\n\n[color=red][size=4]Notes[/size][/color]\n" + want
				}
				got := buildDescription(api.UploadSubject{ReleaseName: "Example.Release.2026"}, "", trackers.DescriptionAssets{
					Description: tt.input,
					Final:       final,
				})
				if got != want {
					t.Errorf("final=%t: description = %q, want %q", final, got, want)
				}
			}
		})
	}
}

func TestBuildDescriptionPreservesLiteralMediaInfo(t *testing.T) {
	t.Parallel()

	const mediaText = "Complete name: [center]Example[/center].mkv\nComment: [center]literal[/center]"
	mediaPath := filepath.Join(t.TempDir(), "mediainfo.txt")
	if err := os.WriteFile(mediaPath, []byte(mediaText), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildDescription(api.UploadSubject{
		ReleaseName:       "Example.Release.2026",
		MediaInfoTextPath: mediaPath,
	}, "", trackers.DescriptionAssets{Description: "[center]Release notes[/center]"})
	want := "[color=green][size=6]Example.Release.2026[/size][/color]\n\n" +
		"[color=red][size=4]MEDIAINFO[/size][/color]\n[hide][code]" + mediaText + "[/code][/hide]\n\n" +
		"[color=red][size=4]Notes[/size][/color]\n[align=center]Release notes[/align]"
	if got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
}

func TestResolveTypeID(t *testing.T) {
	t.Parallel()

	if got := resolveTypeID(api.UploadSubject{
		Type:   "REMUX",
		Source: "Blu-ray",
	}); got != "14" {
		t.Fatalf("expected bluray remux type 14, got %q", got)
	}

	if got := resolveTypeID(api.UploadSubject{
		Anime:   true,
		Release: api.ReleaseInfo{Resolution: "1080p"},
	}); got != "16" {
		t.Fatalf("expected anime hd type 16, got %q", got)
	}

	if got := resolveTypeID(api.UploadSubject{
		TVPack:   true,
		Identity: api.ExternalIdentity{Category: "TV"},
		Release:  api.ReleaseInfo{Resolution: "480p"},
	}); got != "4" {
		t.Fatalf("expected tv pack sd type 4, got %q", got)
	}

	if got := resolveTypeID(api.UploadSubject{
		Identity: api.ExternalIdentity{Category: "MOVIE"},
		Release:  api.ReleaseInfo{Resolution: "1080p"},
		ProviderMetadata: api.SourceScopedMetadata{
			TMDB: &api.TMDBMetadata{Genres: "Drama", Keywords: "adult"},
		},
	}); got != "13" {
		t.Fatalf("expected adult movie type 13, got %q", got)
	}
}

func TestDefinitionBuildUploadDryRunBlockedWithoutPoster(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "Movie.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	confirmedName := "Movie"

	entry, err := prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker:             "AR",
		RequestedUploadName: &confirmedName,
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "Movie.mkv"),
			TorrentPath: torrentPath,
			Release:     api.ReleaseInfo{Title: "Movie", Resolution: "1080p"},
			Identity:    api.ExternalIdentity{Category: "MOVIE"},
		},
		TrackerConfig: config.TrackerConfig{},
		Runtime:       trackers.PreparationRuntimeFromConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")}}),
		Logger:        api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", entry.Status)
	}
	if entry.Message != "missing poster URL" {
		t.Fatalf("expected poster block message, got %q", entry.Message)
	}
}
