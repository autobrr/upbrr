// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"github.com/autobrr/upbrr/internal/config"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTonemappedHeaderDoesNotInferLegacyImageProcessing(t *testing.T) {
	if TonemappingNotice("Tonemapped", []api.ScreenshotImage{{ImgURL: "https://images.example.invalid/legacy.png"}}) != "" {
		t.Fatal("unknown legacy screenshot was labelled tonemapped from current configuration")
	}
}

func TestTonemappingNoticeUsesSelectedRenderableEvidence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		states []api.ScreenshotTonemapping
		want   string
	}{
		{"tonemapped", []api.ScreenshotTonemapping{api.ScreenshotTonemapped}, "CUSTOM"},
		{"not tonemapped", []api.ScreenshotTonemapping{api.ScreenshotNotTonemapped}, ""},
		{"unknown", []api.ScreenshotTonemapping{""}, ""},
		{"mixed", []api.ScreenshotTonemapping{api.ScreenshotTonemapped, api.ScreenshotNotTonemapped, ""}, "Screenshots: 1 tonemapped, 1 not tonemapped, 1 with unknown processing."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			images := make([]api.ScreenshotImage, 0, len(tt.states)+1)
			for _, state := range tt.states {
				images = append(images, api.ScreenshotImage{RawURL: "https://images.example.invalid/image.png", Tonemapping: state})
			}
			images = append(images, api.ScreenshotImage{}) // Not rendered, must not affect notice.
			if got := TonemappingNotice("CUSTOM", images); got != tt.want {
				t.Fatalf("notice=%q, want %q", got, tt.want)
			}
			if got := TonemappingNotice("", images); got != "" {
				t.Fatalf("disabled header=%q", got)
			}
		})
	}
}

func TestTonemappingNoticeCountsSelectedImagesAlreadyInKeptDescription(t *testing.T) {
	for _, status := range []api.ScreenshotTonemapping{"", api.ScreenshotNotTonemapped} {
		t.Run(string(status), func(t *testing.T) {
			const keptURL = "https://images.example.invalid/kept.png"
			kept := "User caption\n[img]" + keptURL + "[/img]"
			images := []api.ScreenshotImage{{RawURL: keptURL, Tonemapping: status}, {RawURL: "https://images.example.invalid/new.png", Tonemapping: api.ScreenshotTonemapped}}
			cfg := config.Config{}
			cfg.Description.TonemappedHeader = "ALL TONEMAPPED"
			got, err := ComposeDescription(t.Context(), api.DescriptionSubject{}, cfg, api.NopLogger{}, kept, nil, images)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got, "ALL TONEMAPPED") || !strings.Contains(got, "Screenshots: 1 tonemapped,") || strings.Count(got, keptURL) != 1 || !strings.Contains(got, "User caption") {
				t.Fatalf("incorrect kept-image notice or duplicate: %s", got)
			}
		})
	}
}
