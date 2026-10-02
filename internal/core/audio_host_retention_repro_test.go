// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/imagehosting"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/db/dbfixture"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// persistedAudioHostFake models a successful upload and its durable provenance.
// The service's real upload tests separately verify provenance stamping.
type persistedAudioHostFake struct {
	descriptionAudioHostFake
	repo     *db.SQLiteRepository
	cfg      config.Config
	registry *trackers.Registry
}

func (f *persistedAudioHostFake) Upload(ctx context.Context, subject api.ImageHostingSubject, host, scope string, images []api.ScreenshotImage) ([]api.UploadedImageLink, error) {
	links, err := f.descriptionAudioHostFake.Upload(ctx, subject, host, scope, images)
	if err != nil {
		return nil, err
	}
	fingerprint, err := imagehosting.AudioAccountScope(f.cfg, f.registry, host)
	if err != nil {
		return nil, fmt.Errorf("fixture account fingerprint: %w", err)
	}
	for i := range links {
		links[i].AccountScope = fingerprint
	}
	if err := f.repo.SaveUploadedImages(ctx, subject.MediaBinding, host, links); err != nil {
		return nil, fmt.Errorf("fixture persist upload: %w", err)
	}
	return links, nil
}

func TestRestoredAudioAnalysisReusesHostedImages(t *testing.T) {
	for _, name := range []string{"same account", "changed account", "legacy", "changed host", "different scope", "persistence failure"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			priorRoot := filepath.Join(root, "prior")
			if err := os.MkdirAll(priorRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			path := writeAudioAnalysisPNGForTest(t, priorRoot, "waveform.png", 10, 6)
			oldRelease := api.ReleaseRef{SourcePath: filepath.Join(root, "Example.Release.2026.mkv"), Generation: 1}
			release := oldRelease
			release.Generation++
			prior := api.AudioAnalysisResult{
				Release:             oldRelease,
				ResourceID:          "resource-1",
				ManifestFingerprint: "manifest-1",
				ProfileVersion:      api.AudioAnalysisProfileVersion,
				Selection:           api.AudioAnalysisSelectionPrimary,
				TrackIDs:            []string{"track-1"},
				Variants:            []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
				Status:              api.StageStatusCompleted,
				Tracks: []api.AudioAnalysisTrackResult{{
					TrackID: "track-1",
					Ordinal: 1,
					Status:  api.StageStatusCompleted,
					Artifacts: []api.AudioAnalysisArtifact{{
						ID:      "old-waveform",
						Variant: api.AudioAnalysisWaveform,
						Status:  api.StageStatusCompleted,
						Width:   10,
						Height:  6,
					}},
				}},
			}
			resource := retainWorkflowAudioAnalysisResourceForTest(t, root, priorRoot, map[api.PublicResourceID]string{"old-waveform": path})
			dbPath := filepath.Join(t.TempDir(), "retention.sqlite")
			dbfixture.WriteMigrated(t, dbPath)
			repo, err := db.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			oldBinding := api.PreparedMediaBinding{
				SourcePath:               release.SourcePath,
				PreparedGeneration:       1,
				PreparedMediaFingerprint: "prior-fingerprint",
			}
			binding := api.PreparedMediaBinding{
				SourcePath:               release.SourcePath,
				PreparedGeneration:       2,
				PreparedMediaFingerprint: "current-fingerprint",
			}
			cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "imgbb", ImgBBAPI: "example-first-account"}}
			registry := mediaImageHostRegistry(t)
			host := &persistedAudioHostFake{
				repo:     repo,
				cfg:      cfg,
				registry: registry,
			}
			descriptions := workflowDescriptionBuilder{media: &mediaModule{
				cfg:      cfg,
				logger:   api.NopLogger{},
				registry: registry,
				images:   host,
				repo:     repo,
			}}
			subject := api.UploadSubject{MediaBinding: oldBinding, ExactMedia: &api.ExactMediaAssets{
				AudioAnalysis: &api.AudioAnalysisRef{ID: "analysis", Revision: 1},
				AudioTracks:   []api.AudioDescriptionTrack{{Ordinal: 1, Images: []api.ScreenshotImage{{Path: path, Purpose: api.ScreenshotPurposeAudioAnalysis}}}},
			}}
			if err := descriptions.uploadAudioDescriptionImages(t.Context(), subject, []string{"ONE"}); err != nil {
				t.Fatal(err)
			}
			if len(host.images) != 1 {
				t.Fatalf("initial uploads = %d", len(host.images))
			}
			if name == "legacy" {
				if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE uploaded_images SET account_scope = ''`); err != nil {
					t.Fatal(err)
				}
			}
			if name == "different scope" {
				if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE uploaded_images SET usage_scope = 'tracker:OTHER'`); err != nil {
					t.Fatal(err)
				}
			}
			builder := workflowAudioAnalysisBuilder{
				root:    root,
				uploads: repo,
				service: &audioAnalysisServiceFake{},
				resolver: audioAnalysisResolverFake{subject: api.AudioAnalysisSubject{
					Release:             release,
					MediaBinding:        binding,
					SourcePath:          release.SourcePath,
					VideoPath:           "source.mkv",
					ResourceID:          "resource-1",
					ManifestFingerprint: "manifest-1",
					PrimaryTrackID:      "track-1",
					Tracks: []api.MediaTrackFacts{{
						ID:         "track-1",
						Kind:       api.MediaTrackAudio,
						Ordinal:    1,
						Channels:   2,
						SampleRate: 48000,
					}},
				}},
			}
			if name == "persistence failure" {
				if _, err := repo.RawDB().ExecContext(t.Context(), `CREATE TRIGGER fail_audio_clone BEFORE INSERT ON uploaded_images WHEN NEW.prepared_generation = 2 BEGIN SELECT RAISE(ABORT, 'fixture failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			restored, private, err := builder.RestoreCompatible(t.Context(), release, prior, resource, "reopened")
			if name == "persistence failure" {
				if err == nil {
					t.Fatal("copy failure was ignored")
				}
				resolver, ok := builder.resolver.(audioAnalysisResolverFake)
				if !ok {
					t.Fatal("fixture resolver type changed")
				}
				cloneRoot, rootErr := audioAnalysisAttemptRoot(root, resolver.subject, "reopened")
				if rootErr != nil {
					t.Fatal(rootErr)
				}
				if _, statErr := os.Stat(cloneRoot); !os.IsNotExist(statErr) {
					t.Fatalf("failed clone remains: %v", statErr)
				}
				if _, statErr := os.Stat(path); statErr != nil {
					t.Fatalf("original artifact lost: %v", statErr)
				}
				links, listErr := repo.ListUploadedImagesByPath(t.Context(), binding)
				if listErr != nil || len(links) != 0 {
					t.Fatalf("failed clone retained records: %#v %v", links, listErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			restoredResource, ok := private.(workflowAudioAnalysisResource)
			if !ok {
				t.Fatalf("restored resource type = %T", private)
			}
			restoredPath := restoredResource.paths[restored.Tracks[0].Artifacts[0].ID]
			if restoredPath == path {
				t.Fatal("fixture did not exercise the restored artifact path")
			}
			if name == "changed account" {
				cfg.ImageHosting.ImgBBAPI = "example-second-account"
				host.cfg = cfg
				descriptions.media.cfg = cfg
			}
			if name == "changed host" {
				cfg.ImageHosting.Host1 = "pixhost"
				host.cfg = cfg
				descriptions.media.cfg = cfg
			}
			subject.MediaBinding = binding
			subject.ExactMedia.AudioTracks[0].Images[0].Path = restoredPath
			if err := descriptions.uploadAudioDescriptionImages(t.Context(), subject, []string{"ONE"}); err != nil {
				t.Fatal(err)
			}
			want := 1
			if name != "same account" {
				want = 2
			}
			if len(host.images) != want {
				t.Fatalf("uploads after restore = %d, want %d", len(host.images), want)
			}
			if err := descriptions.uploadAudioDescriptionImages(t.Context(), subject, []string{"ONE"}); err != nil {
				t.Fatal(err)
			}
			if len(host.images) != want {
				t.Fatalf("repeated description uploaded again: %d", len(host.images))
			}
			retried, retryPrivate, err := builder.Build(t.Context(), release, audioAnalysisInstructionsForBuilderTest(release, []string{"track-1"}, api.AudioAnalysisWaveform), "retry", time.Now(), &restored, restoredResource)
			if err != nil {
				t.Fatal(err)
			}
			retryResource, ok := retryPrivate.(workflowAudioAnalysisResource)
			if !ok {
				t.Fatalf("retry resource type = %T", retryPrivate)
			}
			subject.ExactMedia.AudioTracks[0].Images[0].Path = retryResource.paths[retried.Tracks[0].Artifacts[0].ID]
			if err := descriptions.uploadAudioDescriptionImages(t.Context(), subject, []string{"ONE"}); err != nil {
				t.Fatal(err)
			}
			if len(host.images) != want {
				t.Fatalf("compatible retry uploaded again: %d, want %d", len(host.images), want)
			}
			original, err := repo.ListUploadedImagesByPath(t.Context(), oldBinding)
			if err != nil || len(original) != 1 || original[0].ImagePath != path {
				t.Fatalf("original upload changed: %#v, %v", original, err)
			}
		})
	}
}
