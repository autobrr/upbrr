// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"testing"
	"time"
)

func TestScreenshotEvidenceFingerprintScopesCurrentHostedSelection(t *testing.T) {
	for _, scope := range []string{"", "global", " Global ", "tracker:lst", " TRACKER: LsT ", "tracker: "} {
		t.Run(scope, func(t *testing.T) {
			exact := &ExactMediaAssets{
				Screenshots: []ScreenshotImage{{
					Path:    " selected.png ",
					Purpose: ScreenshotPurposeFinal,
					Format:  "png",
				}},
				ScreenshotUploads: []UploadedImageLink{{
					ImagePath:  "selected.png",
					UsageScope: scope,
					RawURL:     "https://images.example/first.png",
				}},
			}
			before := validationScreenshotFingerprint(exact, "LST")
			exact.ScreenshotUploads[0].RawURL = "https://images.example/replaced.png"
			if before == validationScreenshotFingerprint(exact, "LST") {
				t.Fatal("usable hosted replacement did not invalidate review")
			}
			before = validationScreenshotFingerprint(exact, "LST")
			exact.ScreenshotUploads[0].UploadedAt = time.Now()
			exact.Screenshots[0].UploadedAt = time.Now()
			exact.DVDMenus = []DVDMenuCaptureImage{{ScreenshotImage: ScreenshotImage{Path: "menu.png"}}}
			exact.HDRPlots = []HDRDescriptionPlot{{ArtifactID: "plot"}}
			exact.ScreenshotUploads = append(exact.ScreenshotUploads, UploadedImageLink{
				ImagePath:  "selected.png",
				UsageScope: "tracker:OTHER",
				RawURL:     "https://images.example/other.png",
			})
			if before != validationScreenshotFingerprint(exact, "LST") {
				t.Fatal("unrelated media or upload bookkeeping changed review identity")
			}
			exact.Screenshots[0].TimestampSeconds++
			if before == validationScreenshotFingerprint(exact, "LST") {
				t.Fatal("different captured frame did not invalidate review")
			}
			exact.Screenshots[0].TimestampSeconds--
			exact.Screenshots[0].Format = "jpeg"
			if before == validationScreenshotFingerprint(exact, "LST") {
				t.Fatal("changed selected image did not invalidate review")
			}
		})
	}
}

func TestScreenshotEvidenceFingerprintDistinguishesCaptureOrderAndAssociations(t *testing.T) {
	exact := &ExactMediaAssets{
		Screenshots: []ScreenshotImage{
			{
				Path:             "first.png",
				Index:            1,
				TimestampSeconds: 30,
				Purpose:          ScreenshotPurposeFinal,
				Format:           "png",
			},
			{
				Path:             "second.png",
				Index:            1,
				TimestampSeconds: 60,
				Purpose:          ScreenshotPurposeFinal,
				Format:           "png",
			},
		},
		ScreenshotUploads: []UploadedImageLink{
			{ImagePath: "first.png", RawURL: "https://images.example/first.png"},
			{ImagePath: "second.png", RawURL: "https://images.example/second.png"},
		},
	}
	before := validationScreenshotFingerprint(exact, "LST")
	exact.Screenshots[0], exact.Screenshots[1] = exact.Screenshots[1], exact.Screenshots[0]
	if before == validationScreenshotFingerprint(exact, "LST") {
		t.Fatal("selected order changed without invalidating review")
	}
	exact.Screenshots[0], exact.Screenshots[1] = exact.Screenshots[1], exact.Screenshots[0]
	exact.ScreenshotUploads[0].ImagePath, exact.ScreenshotUploads[1].ImagePath = exact.ScreenshotUploads[1].ImagePath, exact.ScreenshotUploads[0].ImagePath
	if before == validationScreenshotFingerprint(exact, "LST") {
		t.Fatal("host association changed without invalidating review")
	}
	exact.ScreenshotUploads[0].ImagePath, exact.ScreenshotUploads[1].ImagePath = "relocated/first.png", "relocated/second.png"
	exact.Screenshots[0].Path, exact.Screenshots[1].Path = "relocated/first.png", "relocated/second.png"
	if before != validationScreenshotFingerprint(exact, "LST") {
		t.Fatal("storage relocation changed unchanged screenshot review")
	}
}
