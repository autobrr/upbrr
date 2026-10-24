// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTonemappingDescriptionMatchesURLNotPath(t *testing.T) {
	images := []api.ScreenshotImage{{Path: "shared", RawURL: "https://images.example.invalid/old.png"}, {Path: "shared", RawURL: "https://images.example.invalid/new.png"}}
	uploads := []api.UploadedImageLink{{
		ImagePath:     "shared",
		RawURL:        images[0].RawURL,
		Tonemapping:   api.ScreenshotTonemapped,
		ContentSHA256: "old",
	}, {
		ImagePath:     "shared",
		RawURL:        images[1].RawURL,
		Tonemapping:   api.ScreenshotNotTonemapped,
		ContentSHA256: "new",
	}}
	applyScreenshotURLProvenance(images, uploads)
	if images[0].Tonemapping != api.ScreenshotTonemapped || images[1].Tonemapping != api.ScreenshotNotTonemapped {
		t.Fatalf("wrong URL evidence=%+v", images)
	}
	images[0].RawURL = "https://images.example.invalid/external.png"
	applyScreenshotURLProvenance(images, uploads)
	if images[0].Tonemapping != "" {
		t.Fatal("external URL inherited stale evidence")
	}
}

func TestNewUploadUnknownEvidenceOverridesSavedClaim(t *testing.T) {
	const url = "https://images.example.invalid/same.png"
	image := api.ScreenshotImage{
		Path:    "local.png",
		RawURL:  url,
		Purpose: api.ScreenshotPurposeFinal,
	}
	saved := api.UploadedImageLink{
		ImagePath:   image.Path,
		RawURL:      url,
		Tonemapping: api.ScreenshotTonemapped,
	}
	meta := api.UploadSubject{ExactMedia: &api.ExactMediaAssets{Screenshots: []api.ScreenshotImage{image}, ScreenshotUploads: []api.UploadedImageLink{saved}}}
	actual := []api.UploadedImageLink{{ImagePath: image.Path, RawURL: url}}
	assets := DescriptionAssets{}
	applyResolvedDescriptionScreenshots(t.Context(), "AITHER", meta, nil, nil, &assets, actual, []api.ScreenshotImage{image})
	if len(assets.Screenshots) != 1 || assets.Screenshots[0].Tonemapping != "" {
		t.Fatalf("new unknown evidence inherited saved claim: %+v", assets.Screenshots)
	}
	if meta.ExactMedia.ScreenshotUploads[0].Tonemapping != api.ScreenshotTonemapped {
		t.Fatal("shared exact media mutated")
	}
}
