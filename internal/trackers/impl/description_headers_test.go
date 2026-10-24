// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDescriptionHeadersSurvivePreparationWithAnalysis(t *testing.T) {
	registry := MustNewRegistry()
	for _, tracker := range []string{"AITHER", "STC", "ACM", "OE", "GPW", "HDT", "HDS", "TL", "DC", "BT", "BJS", "SPD"} {
		t.Run(tracker, func(t *testing.T) {
			host := "pixhost"
			if tracker == "OE" || tracker == "STC" || tracker == "DC" {
				host = "imgbb"
			}
			root := t.TempDir()
			media := &api.ExactMediaAssets{
				HDRAnalysis:    &api.HDRAnalysisRef{ID: "analysis", Revision: 1},
				HDRUploadHosts: map[string]string{tracker: host},
				HDRPlots: []api.HDRDescriptionPlot{{
					TargetID: api.HDRTargetID("source", ""),
					Label:    "Native metadata",
					Image: api.ScreenshotImage{
						Path:    filepath.Join(root, "hdr.png"),
						Purpose: api.ScreenshotPurposeHDRAnalysis,
						Width:   api.HDRAnalysisWidth,
						Height:  api.HDRAnalysisHeight,
					},
				}},
				HDRUploads: []api.UploadedImageLink{{
					ImagePath:  filepath.Join(root, "hdr.png"),
					Host:       host,
					UsageScope: "global",
					RawURL:     "https://images.example.invalid/hdr.png",
					Purpose:    api.ScreenshotPurposeHDRAnalysis,
				}},
				HDRStats: []api.HDRDescriptionStats{{
					ArtifactID: "stats",
					TargetID:   api.HDRTargetID("source", ""),
					Label:      "Primary audio",
					Text:       "Peak -1 dB",
				}},
			}
			for index := range 3 {
				path := filepath.Join(root, fmt.Sprintf("screen-%d.png", index))
				url := fmt.Sprintf("https://images.example.invalid/screen-%d.png", index)
				media.Screenshots = append(media.Screenshots, api.ScreenshotImage{Path: path, Purpose: api.ScreenshotPurposeFinal})
				media.ScreenshotUploads = append(media.ScreenshotUploads, api.UploadedImageLink{
					Tonemapping: api.ScreenshotTonemapped,
					ImagePath:   path,
					Host:        host,
					UsageScope:  "global",
					ImgURL:      url,
					RawURL:      url,
					WebURL:      url,
				})
			}
			meta := api.UploadSubject{
				SourcePath:          filepath.Join(root, "Synthetic.HDR.mkv"),
				HDR:                 "HDR10",
				ExactMedia:          media,
				DescriptionOverride: "Release notes",
				ImageHostOverrides:  api.ImageHostOverrides{SkipUpload: new(true)},
			}
			if tracker == "OE" {
				meta.Type, meta.VideoCodec, meta.Tag = "ENCODE", "AV1", "SM737"
				meta.TrackerQuestionnaireAnswers = map[string]map[string]string{
					"OE": {"encoding_settings": "SVT-AV1 preset=4 crf=20", "source_notes": "Synthetic BluRay source"},
				}
			}
			for _, mode := range []string{"tonemapped", "disabled", "SDR", "unknown", "not tonemapped", "mixed", "plots only"} {
				t.Run(mode, func(t *testing.T) {
					subject := meta
					subject.ExactMedia = media.Clone()
					if mode == "unknown" || mode == "not tonemapped" {
						for i := range subject.ExactMedia.ScreenshotUploads {
							subject.ExactMedia.ScreenshotUploads[i].Tonemapping = ""
							if mode == "not tonemapped" {
								subject.ExactMedia.ScreenshotUploads[i].Tonemapping = api.ScreenshotNotTonemapped
							}
						}
					}
					if mode == "mixed" {
						subject.ExactMedia.ScreenshotUploads[0].Tonemapping = ""
					}
					if mode == "plots only" {
						subject.ExactMedia = media.Clone()
						subject.ExactMedia.Screenshots, subject.ExactMedia.ScreenshotUploads = nil, nil
					}
					if mode == "SDR" {
						subject.HDR = ""
					}
					cfg := config.Config{
						ImageHosting:       config.ImageHostingConfig{Host1: host},
						ScreenshotHandling: config.ScreenshotHandlingConfig{ToneMap: mode != "disabled"},
						Description:        config.DescriptionSettingsConfig{ScreenshotHeader: "SCREENSHOT HEADER", TonemappedHeader: "TONEMAPPED HEADER"},
					}
					service := trackers.NewServiceWithRegistry(cfg, api.NopLogger{}, descriptionPreviewPersistence{}, registry)
					preview, err := service.BuildPreparation(t.Context(), api.NewDescriptionSubject(subject), []string{tracker})
					if tracker == "OE" && mode == "plots only" {
						if err != nil || len(preview.ContentFailures) != 1 ||
							!strings.Contains(preview.ContentFailures[0].Message, "at least 3 unique renderable screenshots") {
							t.Fatalf("OE screenshot requirement changed: %+v %v", preview, err)
						}
						return
					}
					if err != nil || len(preview.ContentFailures) != 0 || len(preview.Descriptions) != 1 {
						t.Fatalf("preview=%+v err=%v", preview, err)
					}
					got := preview.Descriptions[0].RawDescription
					if mode == "mixed" && !strings.Contains(got, "2 tonemapped, 0 not tonemapped, 1 with unknown processing") {
						t.Fatalf("mixed evidence missing: %s", got)
					}
					if strings.Contains(got, "TONEMAPPED HEADER") != (mode == "tonemapped" || mode == "disabled" || mode == "SDR") {
						t.Fatalf("incorrect tonemap notice: %s", got)
					}
					switch tracker {
					case "AITHER", "STC", "ACM", "OE", "GPW", "HDT", "HDS", "TL":
						if mode != "plots only" && (strings.Count(got, "SCREENSHOT HEADER") != 1 || !strings.Contains(got, "screen-0.png")) {
							t.Fatalf("screenshot header missing: %s", got)
						}
					}
					switch tracker {
					case "HDS", "TL", "DC", "SPD":
						// These dialects remove named spoiler titles. Evidence must remain.
						if !strings.Contains(got, "Peak -1 dB") || !strings.Contains(got, "hdr.png") {
							t.Fatalf("analysis evidence missing: %s", got)
						}
					default:
						if !strings.Contains(got, "Audio Plots") || !strings.Contains(got, "HDR plots") {
							t.Fatalf("analysis titles missing: %s", got)
						}
					}
					switch tracker {
					case "AITHER", "STC", "ACM", "OE":
						if mode == "plots only" && strings.Contains(got, "SCREENSHOT HEADER") {
							t.Fatalf("plots mislabeled as screenshots: %s", got)
						}
						if mode == "tonemapped" && strings.Index(got, "TONEMAPPED HEADER") < strings.Index(got, "HDR plots") {
							t.Fatalf("tonemap header labels plots instead of screenshots: %s", got)
						}
					}
				})
			}
		})
	}
}
