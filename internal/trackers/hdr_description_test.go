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
