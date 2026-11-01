// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/filesystem"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestKeepFolderEligibilityAndPolicySurviveRestart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		enabled    bool
		episode    api.EpisodeFacts
		disc       bool
		fileInput  bool
		extraVideo bool
		eligible   bool
	}{
		{
			name:     "movie enabled",
			enabled:  true,
			eligible: true,
		},
		{name: "movie disabled", eligible: true},
		{
			name:     "episode",
			enabled:  true,
			episode:  api.EpisodeFacts{Season: 1, Episode: 2},
			eligible: true,
		},
		{
			name:       "multiple videos",
			enabled:    true,
			extraVideo: true,
		},
		{
			name:    "season pack",
			enabled: true,
			episode: api.EpisodeFacts{Season: 1, Pack: true},
		},
		{
			name:    "disc",
			enabled: true,
			disc:    true,
		},
		{
			name:      "file input",
			enabled:   true,
			fileInput: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			video := filepath.Join(root, "Example.2026.mkv")
			files := []string{video, filepath.Join(root, "sample.mkv"), filepath.Join(root, "release.nfo"), filepath.Join(root, "notes.txt")}
			selected := []string{video}
			if tc.extraVideo {
				second := filepath.Join(root, "Second.mkv")
				files = append(files, second)
				selected = append(selected, second)
			}
			for _, file := range files {
				if err := os.WriteFile(file, []byte("synthetic fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			source := root
			if tc.fileInput {
				source = video
			}
			if tc.disc {
				if err := os.Mkdir(filepath.Join(root, "BDMV"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			collector := &keepFolderCollector{episode: tc.episode}
			store := newMemoryStore()
			module := newTestModule(t, store, collector)
			input := api.PrepareInput{SourcePath: source, Policy: api.PreparationPolicy{KeepFolder: tc.enabled}}
			var generation api.PreparedGeneration
			for _, stage := range []string{"initial", "restart"} {
				if stage == "restart" {
					module = newTestModule(t, store, collector)
				}
				prepared, err := module.Prepare(t.Context(), input)
				if err != nil {
					t.Fatalf("%s prepare: %v", stage, err)
				}
				if stage == "restart" && !tc.disc && prepared.Release.Generation != generation {
					t.Fatal("restart did not reuse the prepared generation")
				}
				generation = prepared.Release.Generation
				ref := api.ReleaseRef{SourcePath: source, Generation: generation}
				display, err := module.ResolveDisplay(t.Context(), ref)
				if err != nil {
					t.Fatal(err)
				}
				if display.CanKeepFolder != tc.eligible {
					t.Fatalf("%s CanKeepFolder = %v, want %v", stage, display.CanKeepFolder, tc.eligible)
				}
				subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
				if err != nil {
					t.Fatal(err)
				}
				if subject.KeepFolder != (tc.enabled && tc.eligible) {
					t.Fatalf("%s KeepFolder = %v, want %v", stage, subject.KeepFolder, tc.enabled && tc.eligible)
				}
				if !slices.Equal(subject.FileList, selected) {
					t.Fatalf("%s selected files = %v, want %v", stage, subject.FileList, selected)
				}
			}
		})
	}
}

type keepFolderCollector struct {
	base    recordingCollector
	episode api.EpisodeFacts
}

func (c *keepFolderCollector) Collect(ctx context.Context, request preparationstate.Request) (CollectedFacts, error) {
	facts, err := c.base.Collect(ctx, request)
	if err != nil {
		return CollectedFacts{}, err
	}
	facts.Episode = c.episode
	facts.Resources, err = c.HydratePrivateResources(ctx, request)
	return facts, err
}

func (*keepFolderCollector) HydratePrivateResources(ctx context.Context, request preparationstate.Request) (CollectedResources, error) {
	video, files, err := filesystem.CollectVideoFiles(ctx, request.Manifest.SourcePath, false)
	if err != nil {
		return CollectedResources{}, fmt.Errorf("select fixture video: %w", err)
	}
	return CollectedResources{
		SourcePath: request.Manifest.SourcePath,
		VideoPath:  video,
		FileList:   files,
	}, nil
}
