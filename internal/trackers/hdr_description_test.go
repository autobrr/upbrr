// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCombinedHDRAudioStatsRemainInDescriptionsWithoutHosting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "waveform.png")
	for _, hosting := range []string{"stats only", "cached", "skipped", "required"} {
		t.Run(hosting, func(t *testing.T) {
			exact := &api.ExactMediaAssets{
				HDRAnalysis: &api.HDRAnalysisRef{ID: "hdr", Revision: 1},
				HDRStats: []api.HDRDescriptionStats{{
					ArtifactID: "stats",
					TargetID:   api.HDRTargetID("synthetic", ""),
					Label:      "Primary audio",
					Text:       "Peak -1 dB\n[spoiler]literal[/spoiler]",
				}},
			}
			if hosting != "stats only" {
				exact.HDRPlots = []api.HDRDescriptionPlot{{
					ArtifactID: "wave",
					Kind:       api.HDRArtifactAudioWaveform,
					TargetID:   api.HDRTargetID("synthetic", ""),
					Label:      "Waveform",
					Image: api.ScreenshotImage{
						Path:    path,
						Purpose: api.ScreenshotPurposeHDRAnalysis,
						Width:   1800,
						Height:  180,
					},
				}}
				exact.ImageHostUploadSkipped = hosting != "required"
			}
			if hosting == "cached" {
				exact.HDRUploadHosts = map[string]string{"HHD": "example-host"}
				exact.HDRUploads = []api.UploadedImageLink{{
					ImagePath: path,
					Host:      "example-host",
					RawURL:    "https://images.example.invalid/wave.png",
					Purpose:   api.ScreenshotPurposeHDRAnalysis,
				}}
			}
			if err := exact.Validate(); err != nil {
				t.Fatal(err)
			}
			assets, err := ResolveDescriptionAssets(t.Context(), "HHD", api.UploadSubject{
				SourcePath: filepath.Join(t.TempDir(), "Synthetic.HDR.mkv"), ExactMedia: exact,
			}, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
			if hosting == "required" {
				if err == nil {
					t.Fatal("required hosting was bypassed by textual statistics")
				}
				return
			}
			if err != nil || !strings.Contains(assets.Description, "[code]Peak -1 dB") || strings.Contains(assets.Description, "[spoiler]literal") ||
				!strings.Contains(assets.Description, "[spoiler=Audio Plots]") || strings.Contains(assets.Description, "[spoiler=HDR plots]") ||
				strings.Contains(assets.Description, "[img]") != (hosting == "cached") {
				t.Fatalf("combined audio description=%+v err=%v", assets, err)
			}
		})
	}
}

func TestHDRAndStandaloneAudioShareOnlyTheAudioDescriptionSection(t *testing.T) {
	exact := &api.ExactMediaAssets{
		AudioAnalysis:  &api.AudioAnalysisRef{ID: "audio", Revision: 1},
		AudioTracks:    []api.AudioDescriptionTrack{{Ordinal: 1, Stats: "Standalone statistics"}},
		HDRAnalysis:    &api.HDRAnalysisRef{ID: "hdr", Revision: 1},
		HDRUploadHosts: map[string]string{"HHD": "hdr-host"},
	}
	for index, kind := range []api.HDRArtifactKind{api.HDRArtifactHDR10PlusPlot, api.HDRArtifactAudioWaveform, api.HDRArtifactAudioSpectrogram} {
		id := string(kind)
		width, height := 1800, 180
		format, track := api.HDRNativeFormat(""), "main"
		if index == 0 {
			width, height = api.HDRAnalysisWidth, api.HDRAnalysisHeight
			format, track = api.HDRNativeHDR10Plus, ""
		}
		path := filepath.Join(t.TempDir(), id+".png")
		exact.HDRPlots = append(exact.HDRPlots, api.HDRDescriptionPlot{
			ArtifactID:             api.PublicResourceID(id),
			TargetID:               api.HDRTargetID("synthetic", ""),
			Label:                  id,
			Kind:                   kind,
			Format:                 format,
			AudioTrackID:           track,
			MeasurementFingerprint: strings.Repeat("a", 64),
			RenderFingerprint:      strings.Repeat("b", 64),
			Image: api.ScreenshotImage{
				Path:    path,
				Purpose: api.ScreenshotPurposeHDRAnalysis,
				Width:   width,
				Height:  height,
			},
		})
		exact.HDRUploads = append(exact.HDRUploads, api.UploadedImageLink{
			ImagePath: path,
			Host:      "hdr-host",
			RawURL:    "https://images.example.invalid/" + id + ".png",
			Purpose:   api.ScreenshotPurposeHDRAnalysis,
		})
	}
	exact.HDRStats = []api.HDRDescriptionStats{{
		ArtifactID:             "stats",
		TargetID:               api.HDRTargetID("synthetic", ""),
		Label:                  "Audio track 1",
		Text:                   "Combined statistics",
		AudioTrackID:           "main",
		MeasurementFingerprint: strings.Repeat("a", 64),
		RenderFingerprint:      strings.Repeat("b", 64),
	}}
	if err := exact.Validate(); err != nil {
		t.Fatal(err)
	}
	subject := api.UploadSubject{
		SourcePath:          filepath.Join(t.TempDir(), "Synthetic.HDR.mkv"),
		ExactMedia:          exact,
		DescriptionOverride: "Notes\n[spoiler=source_audio]old audio[/spoiler]\n[spoiler=source_hdr]old HDR[/spoiler]",
	}
	assets, err := ResolveDescriptionAssets(t.Context(), "HHD", subject, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	audio := sourceAudioBlockPattern.FindString(assets.Description)
	hdr := sourceHDRBlockPattern.FindString(assets.Description)
	for _, text := range []string{"Standalone statistics", "Combined statistics", "audio_waveform.png", "audio_spectrogram.png"} {
		if !strings.Contains(audio, text) || strings.Contains(hdr, text) {
			t.Fatalf("audio evidence escaped audio section: %s", assets.Description)
		}
	}
	if strings.Count(assets.Description, "[spoiler=Audio Plots]") != 1 || strings.Count(assets.Description, "[spoiler=HDR plots]") != 1 ||
		strings.Contains(assets.Description, "old audio") || strings.Contains(assets.Description, "old HDR") ||
		!strings.Contains(hdr, "hdr10plus_plot.png") || strings.Contains(audio, "hdr10plus_plot.png") {
		t.Fatalf("owned sections were not replaced separately: %s", assets.Description)
	}
	// Final editor text needs no fresh hosts and must not be moved between sections.
	subject.DescriptionGroupsFinal = true
	subject.DescriptionGroups = []api.DescriptionBuilderGroup{{Trackers: []string{"HHD"}, RawDescription: subject.DescriptionOverride}}
	exact.HDRUploadHosts = nil
	final, err := ResolveDescriptionAssets(t.Context(), "HHD", subject, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil || final.Description != subject.DescriptionOverride {
		t.Fatalf("final description changed: %+v error=%v", final, err)
	}
	// Removing audio from the exact selection also removes its previous owned block.
	subject.DescriptionGroupsFinal, subject.DescriptionGroups = false, nil
	subject.DescriptionOverride = assets.Description
	exact.AudioAnalysis, exact.AudioTracks, exact.HDRStats = nil, nil, nil
	exact.HDRPlots, exact.HDRUploads = exact.HDRPlots[:1], exact.HDRUploads[:1]
	exact.HDRUploadHosts = map[string]string{"HHD": "hdr-host"}
	videoOnly, err := ResolveDescriptionAssets(t.Context(), "HHD", subject, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil || sourceAudioBlockPattern.MatchString(videoOnly.Description) || !strings.Contains(videoOnly.Description, "hdr10plus_plot.png") {
		t.Fatalf("removed audio selection survived: %+v error=%v", videoOnly, err)
	}
}

func TestExplicitImageHostSkipKeepsCachedAnalysisImagesAndAudioStats(t *testing.T) {
	for _, cachedFor := range []string{"", "HHD", "OTHER"} {
		t.Run("cachedFor="+cachedFor, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			audioPath, hdrPath := filepath.Join(root, "audio.png"), filepath.Join(root, "hdr.png")
			exact := &api.ExactMediaAssets{
				ImageHostUploadSkipped: true,
				AudioAnalysis:          &api.AudioAnalysisRef{ID: "audio", Revision: 1},
				AudioTracks: []api.AudioDescriptionTrack{{
					Ordinal: 1,
					Stats:   "Peak -1 dB",
					Images:  []api.ScreenshotImage{{Path: audioPath, Purpose: api.ScreenshotPurposeAudioAnalysis}},
				}},
				HDRAnalysis: &api.HDRAnalysisRef{ID: "hdr", Revision: 1},
				HDRPlots: []api.HDRDescriptionPlot{{
					TargetID: api.HDRTargetID("source", ""),
					Label:    "Video 1",
					Image: api.ScreenshotImage{
						Path:    hdrPath,
						Purpose: api.ScreenshotPurposeHDRAnalysis,
						Width:   api.HDRAnalysisWidth,
						Height:  api.HDRAnalysisHeight,
					},
				}},
			}
			if cachedFor != "" {
				exact.AudioUploads = []api.UploadedImageLink{{
					ImagePath: audioPath,
					Host:      "imgbb",
					RawURL:    "https://images.example.invalid/audio.png",
					Purpose:   api.ScreenshotPurposeAudioAnalysis,
				}}
				exact.AudioUploadHosts = map[string]string{cachedFor: "imgbb"}
				exact.HDRUploads = []api.UploadedImageLink{{
					ImagePath: hdrPath,
					Host:      "imgbb",
					RawURL:    "https://images.example.invalid/hdr.png",
					Purpose:   api.ScreenshotPurposeHDRAnalysis,
				}}
				exact.HDRUploadHosts = map[string]string{cachedFor: "imgbb"}
			}
			cached := cachedFor == "HHD"
			assets, err := ResolveDescriptionAssets(t.Context(), "HHD", api.UploadSubject{
				SourcePath: filepath.Join(root, "Example.mkv"), ExactMedia: exact,
			}, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
			if err != nil || !strings.Contains(assets.Description, "Peak -1 dB") ||
				strings.Contains(assets.Description, "[img]") != cached {
				t.Fatalf("skipped analysis description=%+v err=%v", assets, err)
			}
			if cached && (!strings.Contains(assets.Description, "/audio.png") || !strings.Contains(assets.Description, "/hdr.png")) {
				t.Fatalf("skip lost cached analysis images: %+v", assets)
			}
		})
	}
}

func TestHDRPreparedDescriptionCarriesFullSizePreviewAuthority(t *testing.T) {
	const full = "https://images.example.invalid/full.png"
	for _, preview := range []string{"https://images.example.invalid/preview.png", full, ""} {
		t.Run(preview, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.Register(stubPreparationDefinition{
				name:        "ONE",
				group:       "default",
				description: "[img]" + full + "[/img]",
			}); err != nil {
				t.Fatal(err)
			}
			image := filepath.Join(t.TempDir(), "hdr.png")
			service := NewServiceWithRegistry(config.Config{}, nil, nil, registry)
			prepared, err := service.BuildPreparation(t.Context(), api.NewDescriptionSubject(api.UploadSubject{
				SourcePath: filepath.Join(t.TempDir(), "Synthetic.HDR.mkv"),
				ExactMedia: &api.ExactMediaAssets{
					HDRAnalysis: &api.HDRAnalysisRef{ID: "hdr", Revision: 1},
					HDRPlots: []api.HDRDescriptionPlot{{
						TargetID: api.HDRTargetID("source", ""),
						Label:    "Video 1",
						Image: api.ScreenshotImage{
							Path:    image,
							Purpose: api.ScreenshotPurposeHDRAnalysis,
							Width:   3000,
							Height:  1200,
						},
					}},
					HDRUploadHosts: map[string]string{"ONE": "example-host"},
					HDRUploads: []api.UploadedImageLink{{
						ImagePath:  image,
						Host:       "example-host",
						UsageScope: "global",
						Purpose:    api.ScreenshotPurposeHDRAnalysis,
						RawURL:     full,
						ImgURL:     preview,
					}},
				},
			}), []string{"ONE"})
			if err != nil || len(prepared.Descriptions) != 1 {
				t.Fatalf("prepared=%#v err=%v", prepared, err)
			}
			want := preview
			if want == "" {
				want = full
			}
			entry := prepared.Descriptions[0]
			if entry.ImagePreviews[full] != want || !strings.Contains(entry.DescriptionHTML, `src="`+want+`"`) ||
				entry.RawDescription != "[img]"+full+"[/img]" {
				t.Fatalf("HDR lightbox authority lost: %#v", entry)
			}
		})
	}
}

func TestHDRDescriptionUsesExactPurposeHostAndPreservesComparisons(t *testing.T) {
	nested := "[spoiler=Comparisons]\n[spoiler=source_hdr]comparison image[/spoiler]\n[/spoiler]"
	original := "Custom notes\n" + nested + "\n[spoiler=source_hdr]old image[/spoiler]"
	path := "synthetic-hdr.png"
	exact := &api.ExactMediaAssets{
		HDRAnalysis: &api.HDRAnalysisRef{ID: "analysis", Revision: 2},
		HDRPlots: []api.HDRDescriptionPlot{{
			TargetID: api.HDRTargetID("source", ""),
			Label:    "Video [1]",
			Image: api.ScreenshotImage{
				Path:    path,
				Purpose: api.ScreenshotPurposeHDRAnalysis,
				Width:   3000,
				Height:  1200,
			},
		}},
		HDRUploadHosts: map[string]string{"HHD": "pixhost"},
		HDRUploads: []api.UploadedImageLink{{
			ImagePath:  path,
			Host:       "pixhost",
			UsageScope: "global",
			RawURL:     "https://image.example.invalid/hdr.png",
			Purpose:    api.ScreenshotPurposeHDRAnalysis,
		}},
	}
	subject := api.UploadSubject{
		SourcePath:          "Synthetic.HDR.mkv",
		DescriptionOverride: original,
		ExactMedia:          exact,
	}
	repo := &stubRepo{}
	registry := descriptionAssetsTestRegistry(t)
	assets, err := ResolveDescriptionAssets(t.Context(), "HHD", subject, repo, api.NopLogger{}, registry)
	if err != nil || !strings.Contains(assets.Description, nested) || strings.Contains(assets.Description, "old image") ||
		!strings.Contains(assets.Description, "https://image.example.invalid/hdr.png") ||
		len(assets.Screenshots) != 0 ||
		len(assets.MenuImages) != 0 {
		t.Fatalf("HDR description=%#v %v", assets, err)
	}
	exact.HDRUploads[0].Purpose = api.ScreenshotPurposeAudioAnalysis
	if _, err := ResolveDescriptionAssets(t.Context(), "HHD", subject, repo, api.NopLogger{}, registry); err == nil {
		t.Fatal("audio upload satisfied HDR")
	}
	exact.HDRUploads[0].Purpose = api.ScreenshotPurposeHDRAnalysis
	subject.DescriptionGroupsFinal = true
	subject.DescriptionGroups = []api.DescriptionBuilderGroup{{Trackers: []string{"HHD"}, RawDescription: original}}
	// Final editor text is authoritative even when no HDR host mapping is available.
	exact.HDRUploadHosts = nil
	assets, err = ResolveDescriptionAssets(t.Context(), "HHD", subject, repo, api.NopLogger{}, registry)
	if err != nil || assets.Description != original {
		t.Fatalf("final HDR text changed=%#v %v", assets, err)
	}
}
