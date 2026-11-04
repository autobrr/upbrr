// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestE2EArtifactsUseReleaseOwner(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.Release.mkv")
	if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	torrent, err := (e2eTorrentService{dbPath: filepath.Join(state, "db.sqlite")}).Create(t.Context(), api.TorrentSubject{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := paths.ReleaseTempPathFor(filepath.Join(state, "tmp"), source, api.ReleaseInfo{})
	if err != nil || !pathing.IsWithinRoot(owner, torrent.Path) {
		t.Fatalf("torrent outside release owner: %s %v", torrent.Path, err)
	}
	shotPath := filepath.Join(t.TempDir(), "screenshot.png")
	if err := os.WriteFile(shotPath, []byte("image fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	shot, err := e2eManagedScreenshot(shotPath, filepath.Join(state, "tmp"), source, "scope", 1)
	if err != nil || !pathing.IsWithinRoot(owner, shot.Path) || filepath.Base(filepath.Dir(shot.Path)) != filepath.Base(source)+"-scope" {
		t.Fatalf("screenshot outside release scope: %s %v", shot.Path, err)
	}
	screenshots := e2eScreenshotService{tmpRoot: filepath.Join(state, "tmp")}
	subject := api.ScreenshotSubject{SourcePath: source}
	if err := screenshots.Delete(t.Context(), subject, torrent.Path); err == nil {
		t.Fatal("screenshot deletion accepted a sibling torrent")
	}
	if _, err := os.Stat(torrent.Path); err != nil {
		t.Fatalf("sibling torrent changed: %v", err)
	}
	if err := screenshots.Delete(t.Context(), subject, shot.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shot.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("screenshot was not deleted: %v", err)
	}
	t.Setenv(e2eEnabledEnv, "1")
	t.Setenv(e2eShotPathEnv, shotPath)
	t.Setenv("UPBRR_E2E_SAVED_DESCRIPTION_IMAGE", "1")
	services := api.ServiceSet{}
	if err := maybeApplyE2EServices(t.Context(), &services,
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(state, "db.sqlite")}},
		api.RepositoryCapabilities{}, nil, api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	plan, err := services.Screenshots.Plan(t.Context(), subject, 1)
	if err != nil || len(plan.FinalSelections) != 1 || len(plan.TrackerImageLinks) != 1 {
		t.Fatalf("saved-image fixture plan = %#v err=%v", plan, err)
	}
	if err := services.Screenshots.Delete(t.Context(), subject, plan.FinalSelections[0].Path); err != nil {
		t.Fatal(err)
	}
	plan, err = services.Screenshots.Plan(t.Context(), subject, 1)
	if err != nil || len(plan.FinalSelections) != 0 || len(plan.TrackerImageLinks) != 0 {
		t.Fatalf("planning recreated a deleted saved image: plan=%#v err=%v", plan, err)
	}
	// Reconstruct the service as a process restart would, retaining only disk state.
	services = api.ServiceSet{}
	if err := maybeApplyE2EServices(t.Context(), &services,
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(state, "db.sqlite")}},
		api.RepositoryCapabilities{}, nil, api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	plan, err = services.Screenshots.Plan(t.Context(), subject, 1)
	if err != nil || len(plan.FinalSelections) != 0 || len(plan.TrackerImageLinks) != 0 {
		t.Fatalf("restart recreated a deleted saved image: plan=%#v err=%v", plan, err)
	}
}

func TestE2EFailedScreenshotDeletionRetainsSeed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "Example.Release.mkv")
	fixture := filepath.Join(root, "fixture.png")
	if err := os.WriteFile(fixture, []byte("fixture image"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmpRoot := filepath.Join(root, "tmp")
	image, err := e2eManagedScreenshot(fixture, tmpRoot, source, "existing-description", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(image.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(image.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(image.Path, "retained"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	service := e2eScreenshotService{tmpRoot: tmpRoot}
	if err := service.Delete(t.Context(), api.ScreenshotSubject{SourcePath: source}, image.Path); err == nil {
		t.Fatal("deletion unexpectedly removed a nonempty directory")
	}
	if _, err := os.Stat(image.Path + ".deleted"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed deletion retained its marker: %v", err)
	}
	if err := os.RemoveAll(image.Path); err != nil {
		t.Fatal(err)
	}
	restored, err := e2eManagedScreenshot(fixture, tmpRoot, source, "existing-description", 0)
	if err != nil || restored.Path != image.Path {
		t.Fatalf("failed deletion hid the seed on reconstruction: image=%#v err=%v", restored, err)
	}
}

func TestE2EMetadataUsesNestedManifestVideos(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "Episodes")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	files := []string{
		filepath.Join(nested, "Example.S00E01.mkv"),
		filepath.Join(nested, "Example.S01E01.mkv"),
	}
	entries := make([]api.SourceManifestEntry, 0, len(files)+3)
	entries = append(entries, api.SourceManifestEntry{Path: nested, Type: api.SourceEntryTypeDirectory})
	for _, name := range append(slices.Clone(files), filepath.Join(nested, "Example.S02E01.nfo"), filepath.Join(nested, "Example.S03E01.sample.mkv")) {
		if err := os.WriteFile(name, []byte("synthetic fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, api.SourceManifestEntry{Path: name, Type: api.SourceEntryTypeFile})
	}
	meta, err := (e2eMetadataService{}).CollectPreparationEvidence(context.Background(), preparationstate.Request{
		Input:    api.PrepareInput{SourcePath: root},
		Manifest: api.SourceManifest{SourcePath: root, Entries: entries},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(meta.FileList, files) || meta.VideoPath != files[0] {
		t.Fatalf("file evidence = %v, video = %q; want %v", meta.FileList, meta.VideoPath, files)
	}
}

func TestE2EMetadataRetainsSingleFileAndExplicitSample(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Example.wav", "Example.!sample.mkv"} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(file, []byte("synthetic fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			source := file
			if filepath.Ext(file) == ".mkv" {
				source = filepath.Dir(file)
			}
			meta, err := (e2eMetadataService{}).CollectPreparationEvidence(context.Background(), preparationstate.Request{
				Input:    api.PrepareInput{SourcePath: source},
				Manifest: api.SourceManifest{SourcePath: source, Entries: []api.SourceManifestEntry{{Path: file, Type: api.SourceEntryTypeFile}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(meta.FileList, []string{file}) || meta.VideoPath != file {
				t.Fatalf("file evidence = %v, video = %q; want %q", meta.FileList, meta.VideoPath, file)
			}
		})
	}
}

func TestE2EMetadataCollectsCompleteEnglishProgrammeEvidence(t *testing.T) {
	for _, test := range []struct {
		name          string
		mediaKind     string
		audioAnalysis string
		codec         string
		channels      int
	}{
		{
			name:      "movie",
			mediaKind: "movie",
			codec:     "AC-3",
			channels:  6,
		},
		{
			name:      "tv",
			mediaKind: "tv",
			codec:     "AC-3",
			channels:  6,
		},
		{
			name:          "audio analysis",
			mediaKind:     "movie",
			audioAnalysis: "1",
			codec:         "PCM",
			channels:      2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(e2eMediaKindEnv, test.mediaKind)
			t.Setenv(e2eAudioAnalysisEnv, test.audioAnalysis)
			t.Setenv(e2eNamingModeEnv, "")
			collector, err := preparedrelease.NewEvidenceCollector(e2eMetadataService{})
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "Example.2026.mkv")
			facts, err := collector.Collect(t.Context(), preparationstate.Request{
				Input:    api.PrepareInput{SourcePath: source},
				Manifest: api.SourceManifest{SourcePath: source},
			})
			if err != nil {
				t.Fatal(err)
			}
			language := facts.Media.LanguageFacts
			if language.AudioStatus != api.MetadataEvidenceStatusComplete || !language.TrackCoverageComplete ||
				!slices.Equal(language.ProgrammeLanguages, []string{"English"}) || !language.HasOriginalAudio() {
				t.Errorf("fixture programme evidence = %#v", language)
			}
			if !slices.Equal(language.OriginalLanguages, []string{"English"}) ||
				facts.Media.OriginalLanguageProvenance != api.FactProvenanceAutomatic {
				t.Errorf("fixture original language = %v, provenance = %s", language.OriginalLanguages, facts.Media.OriginalLanguageProvenance)
			}
			if len(language.Tracks) != 1 {
				t.Fatalf("fixture tracks = %#v", language.Tracks)
			}
			track := language.Tracks[0]
			if track.Kind != api.MediaTrackAudio || track.Role != api.AudioRoleProgramme || !track.Default ||
				track.ID != language.PrimaryAudioTrackID || track.Codec != test.codec || track.Channels != test.channels {
				t.Errorf("fixture primary audio = %#v", track)
			}
		})
	}
}
