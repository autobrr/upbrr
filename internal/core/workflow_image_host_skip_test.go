// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/imagehosting"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type skippedHostingDescriptionDefinition struct {
	assets             *trackers.DescriptionAssets
	uploadAdapterImage bool
}

func TestWorkflowDescriptionExplicitSkipReusesCachedAnalysisFromProducer(t *testing.T) {
	for _, cache := range []string{"compatible", "missing", "wrong account", "wrong purpose", "wrong generation"} {
		t.Run(cache, func(t *testing.T) {
			t.Parallel()
			extractor, resolver, sidecar := hdrFixture(t)
			release := resolver.subject.Release
			record, retained, err := extractor.retainExtraction(t.Context(), release, sidecar, "skip-metadata")
			if err != nil {
				t.Fatal(err)
			}
			analysis, paths, err := extractor.Build(t.Context(), releaseworkflow.HDRPreparation{Release: release},
				api.HDRAnalysisInstructions{
					ProfileVersion: api.HDRAnalysisProfileVersion,
					Release:        release,
					TargetIDs:      []string{sidecar.Identity.TargetID},
				},
				"skip-plot", time.Now(), nil, nil,
				map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{record.ID: record},
				map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource{record.ID: retained}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			analysis.ID, analysis.Revision = "skip-hdr", 1
			hdrPath, err := paths.LocalArtifactPath(analysis, analysis.Targets[0].Artifact.ID)
			if err != nil {
				t.Fatal(err)
			}
			audioPath := filepath.Join(t.TempDir(), "audio.png")
			resources := releaseworkflow.DescriptionResources{
				Media: workflowMediaPrivateArtifacts{},
				AudioAnalysis: api.AudioAnalysisResult{
					ID:       "skip-audio",
					Revision: 1,
					Tracks: []api.AudioAnalysisTrackResult{{
						Ordinal: 1,
						Artifacts: []api.AudioAnalysisArtifact{
							{
								ID:      "waveform",
								Variant: api.AudioAnalysisWaveform,
								Status:  api.StageStatusCompleted,
								Width:   1200,
								Height:  400,
							},
							{
								ID:      "stats",
								Variant: api.AudioAnalysisStats,
								Status:  api.StageStatusCompleted,
								Text:    "Peak -1 dB",
							},
						},
					}},
				},
				AudioPaths: descriptionAudioPathsFake{path: audioPath},
				HDR: func(context.Context) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
					return analysis, paths, nil
				},
			}
			database, err := db.Open(filepath.Join(t.TempDir(), "uploads.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			if err := database.Migrate(); err != nil {
				t.Fatal(err)
			}
			binding := api.PreparedMediaBinding{
				SourcePath:               release.SourcePath,
				PreparedGeneration:       release.Generation,
				PreparedMediaFingerprint: "skip-analysis",
			}
			cfg := config.Config{ImageHosting: config.ImageHostingConfig{Host1: "pixhost", Host2: "onlyimage"}}
			registry := mediaImageHostRegistry(t)
			if cache != "missing" {
				for _, image := range []api.ScreenshotImage{
					{Path: audioPath, Purpose: api.ScreenshotPurposeAudioAnalysis},
					{Path: hdrPath, Purpose: api.ScreenshotPurposeHDRAnalysis},
				} {
					account, err := imagehosting.AnalysisAccountScope(cfg, registry, "pixhost", image.Purpose)
					if err != nil {
						t.Fatal(err)
					}
					storedBinding := binding
					purpose := image.Purpose
					switch cache {
					case "wrong account":
						account = "obsolete-account"
					case "wrong purpose":
						purpose = api.ScreenshotPurposeFinal
					case "wrong generation":
						storedBinding.PreparedGeneration++
					}
					if err := database.SaveUploadedImages(t.Context(), storedBinding, "pixhost", []api.UploadedImageLink{{
						ImagePath:    image.Path,
						Purpose:      purpose,
						AccountScope: account,
						UsageScope:   "global",
						RawURL:       "https://images.example.invalid/" + string(image.Purpose) + ".png",
					}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			host := &descriptionAudioHostFake{}
			preview := &workflowDescriptionServiceFake{}
			builder := workflowDescriptionBuilder{
				resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
					SourcePath:   release.SourcePath,
					MediaBinding: binding,
					Trackers:     []string{"ONE", "TWO"},
				}},
				trackers: preview,
				media: &mediaModule{
					cfg:      cfg,
					logger:   api.NopLogger{},
					registry: registry,
					images:   host,
					repo:     database,
				},
			}
			projections := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
				{TrackerID: "ONE", Artifacts: api.TrackerArtifactRequirements{Description: true}},
				{TrackerID: "TWO", Artifacts: api.TrackerArtifactRequirements{Description: true}},
			}}
			_, err = builder.Build(t.Context(), release, projections, api.MediaArtifactSet{ImageHostUploadSkipped: true}, resources, api.DescriptionInstructions{}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(host.images) != 0 {
				t.Fatalf("explicit skip uploaded %d analysis images", len(host.images))
			}
			exact := preview.subject.ExactMedia
			cached := cache == "compatible"
			if (len(exact.AudioUploads) == 1 && len(exact.HDRUploads) == 1) != cached ||
				(!cached && (len(exact.AudioUploads) != 0 || len(exact.HDRUploads) != 0)) {
				t.Fatalf("analysis cache was lost or incompatible cache reused: %+v", exact)
			}
			for _, tracker := range []string{"ONE", "TWO"} {
				assets, err := trackers.ResolveDescriptionAssets(t.Context(), tracker, api.UploadSubject{
					SourcePath:   release.SourcePath,
					MediaBinding: binding,
					ExactMedia:   exact,
				}, database, api.NopLogger{}, registry)
				wantGraphs := cached && tracker == "ONE"
				if err != nil || !strings.Contains(assets.Description, "Peak -1 dB") ||
					strings.Contains(assets.Description, "[img]") != wantGraphs {
					t.Fatalf("tracker=%s wrong cached analysis description: %+v err=%v", tracker, assets, err)
				}
				if wantGraphs && (!strings.Contains(assets.Description, "/audio_analysis.png") || !strings.Contains(assets.Description, "/hdr_analysis.png")) {
					t.Fatalf("cached audio/HDR was omitted: %+v", assets)
				}
			}
		})
	}
}

func TestWorkflowDescriptionExplicitSkipPreventsAnalysisImageUploads(t *testing.T) {
	t.Parallel()
	host := &countingWorkflowImageHost{}
	builder := workflowDescriptionBuilder{media: &mediaModule{
		cfg:      config.Config{ImageHosting: config.ImageHostingConfig{Host1: "imgbb"}},
		logger:   api.NopLogger{},
		registry: mediaImageHostRegistry(t),
		images:   host,
	}}
	subject := api.UploadSubject{ExactMedia: &api.ExactMediaAssets{
		ImageHostUploadSkipped: true,
		AudioAnalysis:          &api.AudioAnalysisRef{ID: "audio", Revision: 1},
		AudioTracks:            []api.AudioDescriptionTrack{{Ordinal: 1, Images: []api.ScreenshotImage{{Path: "waveform.png", Purpose: api.ScreenshotPurposeAudioAnalysis}}}},
		HDRAnalysis:            &api.HDRAnalysisRef{ID: "hdr", Revision: 1},
		HDRPlots: []api.HDRDescriptionPlot{{
			TargetID: api.HDRTargetID("source", ""),
			Image: api.ScreenshotImage{
				Path:    "hdr.png",
				Purpose: api.ScreenshotPurposeHDRAnalysis,
				Width:   api.HDRAnalysisWidth,
				Height:  api.HDRAnalysisHeight,
			},
		}},
	}}
	if err := builder.uploadAudioDescriptionImages(t.Context(), subject, []string{"ONE"}); err != nil {
		t.Fatal(err)
	}
	if err := builder.uploadHDRDescriptionImages(t.Context(), subject, []string{"ONE"}); err != nil {
		t.Fatal(err)
	}
	if host.uploads != 0 {
		t.Fatalf("explicit skip performed %d analysis image uploads", host.uploads)
	}
}

func (*skippedHostingDescriptionDefinition) Name() string { return "ALPHA" }

func (*skippedHostingDescriptionDefinition) UsesMenuImages() bool { return true }

func (d *skippedHostingDescriptionDefinition) Prepare(ctx context.Context, input trackers.PreparationInput) (trackers.TrackerPlan, *trackers.PreparationFailure) {
	d.assets = input.Assets
	if d.uploadAdapterImage && input.UploadImages != nil {
		if _, err := input.UploadImages(ctx, []api.ScreenshotImage{{Path: "poster.png"}}); err != nil {
			return trackers.TrackerPlan{}, trackers.NewPreparationFailure("ALPHA", "image_upload", "Adapter image failed.", err)
		}
	}
	return trackers.NewDescriptionPlan("ALPHA", trackers.DescriptionResult{Description: "Example description.", Group: "alpha"}), nil
}

func TestWorkflowDescriptionExplicitSkipPreventsContentPreparationUploads(t *testing.T) {
	for _, channel := range []string{"screenshot", "DVD menu", "cached screenshot", "adapter image"} {
		t.Run(channel, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			database, err := db.Open(filepath.Join(root, "workflow.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			if err := database.Migrate(); err != nil {
				t.Fatal(err)
			}
			imagePath := filepath.Join(root, "image.png")
			if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
				t.Fatal(err)
			}
			definition := &skippedHostingDescriptionDefinition{uploadAdapterImage: channel == "adapter image"}
			registry := trackers.NewRegistry()
			if err := registry.RegisterDescriptor(trackers.Descriptor{
				Name:              "ALPHA",
				Definition:        definition,
				BaseURL:           "https://alpha.example.invalid",
				UploadContentMode: trackers.UploadContentModeDescription,
				ImageHost:         &trackers.ImageHostPolicy{AllowedHosts: []string{"imgbb"}},
			}); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: database.DBPath()},
				ImageHosting: config.ImageHostingConfig{Host1: "imgbb"},
			}
			host := &countingWorkflowImageHost{rawURL: "https://images.example.invalid/image.png"}
			sourcePath := filepath.Join(root, "Example.mkv")
			builder := workflowDescriptionBuilder{
				resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
					SourcePath: sourcePath,
					MediaBinding: api.PreparedMediaBinding{
						SourcePath:               sourcePath,
						PreparedGeneration:       1,
						PreparedMediaFingerprint: "skip-media",
					},
				}},
				trackers: trackers.NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, database, registry, host),
			}
			media := api.MediaArtifactSet{
				ImageRequirementsPrepared: true,
				ImageHostUploadSkipped:    true,
				Artifacts: []api.MediaArtifact{{
					ID:       "image",
					Kind:     api.MediaArtifactScreenshot,
					Purpose:  api.ScreenshotPurposeFinal,
					Selected: true,
				}},
			}
			private := workflowMediaPrivateArtifacts{Screenshots: []api.ScreenshotImage{{Path: imagePath, Purpose: api.ScreenshotPurposeFinal}}}
			if channel == "DVD menu" {
				media.Artifacts[0].Kind, media.Artifacts[0].Purpose = api.MediaArtifactDVDMenu, api.ScreenshotPurposeMenu
				private.Screenshots = nil
				private.DVDMenus = []api.DVDMenuCaptureImage{{Path: imagePath, Purpose: api.ScreenshotPurposeMenu}}
			}
			if channel == "cached screenshot" {
				media.Artifacts = append(media.Artifacts, api.MediaArtifact{
					ID:       "hosted",
					Kind:     api.MediaArtifactHostedImage,
					Purpose:  api.ScreenshotPurposeFinal,
					Selected: true,
					Source:   "image",
					Host:     "imgbb",
					URL:      host.rawURL,
				})
				private.HostedImages = map[api.PublicResourceID]api.UploadedImageLink{"hosted": {
					ImagePath:  imagePath,
					Host:       "imgbb",
					UsageScope: "global",
					RawURL:     host.rawURL,
				}}
				private.HostedSources = map[api.PublicResourceID]api.PublicResourceID{"hosted": "image"}
			}
			result, err := builder.Build(t.Context(), api.ReleaseRef{SourcePath: sourcePath, Generation: 1},
				api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
					TrackerID: "ALPHA", Artifacts: api.TrackerArtifactRequirements{Description: true},
				}}}, media, private, api.DescriptionInstructions{}, time.Now())
			if err != nil || result.Status != api.StageStatusCompleted || definition.assets == nil {
				t.Fatalf("skipped hosting blocked content preparation: result=%+v err=%v", result, err)
			}
			if host.uploads != 0 {
				t.Fatalf("explicit skip performed %d image uploads: %+v", host.uploads, host.calls)
			}
			if channel == "cached screenshot" && (len(definition.assets.Screenshots) != 1 ||
				!strings.Contains(definition.assets.Screenshots[0].RawURL, "images.example.invalid")) {
				t.Fatalf("skip lost usable cached screenshot: %+v", definition.assets)
			}
		})
	}
}
