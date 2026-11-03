// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestScreenshotInventoriesRetainDecodedEvidence(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "unknown"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			trackerDir := filepath.Join(root, "aither")
			if err := os.Mkdir(trackerDir, 0o700); err != nil {
				t.Fatal(err)
			}
			generatedPath := filepath.Join(root, "synthetic-1.png")
			importedPath := filepath.Join(trackerDir, "synthetic.jpg")
			payload := importedEvidenceBytes(t, format)
			for _, pathValue := range []string{generatedPath, importedPath} {
				if err := os.WriteFile(pathValue, payload, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			wantFormat, width, height := format, 19, 11
			if format == "unknown" {
				wantFormat, width, height = "", 0, 0
			}
			for name, images := range map[string][]api.ScreenshotImage{
				"generated": listExistingScreens(root, "synthetic"),
				"imported":  listTrackerScreens(root, "synthetic"),
			} {
				if len(images) != 1 {
					t.Fatalf("%s inventory count = %d", name, len(images))
				}
				got := images[0]
				if got.Format != wantFormat || got.Width != width || got.Height != height || got.SizeBytes != int64(len(payload)) {
					t.Errorf("%s evidence = format %q, %dx%d, %d bytes", name, got.Format, got.Width, got.Height, got.SizeBytes)
				}
			}
		})
	}
}

func TestImportedTrackerSelectionsRetainDecodedEvidenceWithoutSource(t *testing.T) {
	root := t.TempDir()
	trackerDir := filepath.Join(root, "aither")
	if err := os.Mkdir(trackerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := api.TrackerMetadata{Tracker: "AITHER", SourcePath: filepath.Join(root, "absent-source.mkv")}
	for index := range 8 {
		url := fmt.Sprintf("https://i.ibb.co/synthetic-%d.jpg", index)
		record.ImageURLs = append(record.ImageURLs, url)
		record.Description += "[img]" + url + "[/img]\n"
		pathValue := filepath.Join(trackerDir, buildTrackerImageFilename(url, index))
		if err := os.WriteFile(pathValue, importedEvidenceBytes(t, "png"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	links := buildTrackerImageLinks([]api.TrackerMetadata{record}, root, nil, 0, false)
	selected := mergeTrackerImagesIntoFinalSelections(nil, links)
	if len(selected) != 8 {
		t.Fatalf("imported image count = %d", len(selected))
	}
	selections := make([]api.ScreenshotFinalSelection, 0, len(selected))
	for index, shot := range selected {
		selections = append(selections, api.ScreenshotFinalSelection{ImagePath: shot.Path, Order: index})
	}
	service := &Service{repo: importedEvidenceRepository{selections: selections}}
	reloaded := service.loadFinalSelections(t.Context(), api.ScreenshotSubject{SourcePath: record.SourcePath}, root)
	if len(reloaded) != len(selected) {
		t.Fatalf("reloaded image count = %d", len(reloaded))
	}
	for _, images := range [][]api.ScreenshotImage{selected, reloaded} {
		for _, shot := range images {
			if shot.Format != "png" || shot.Width != 19 || shot.Height != 11 || shot.Purpose != api.ScreenshotPurposeFinal {
				t.Fatalf("imported evidence = format %q, %dx%d, purpose %q", shot.Format, shot.Width, shot.Height, shot.Purpose)
			}
		}
	}
}

type importedEvidenceRepository struct {
	repository
	selections []api.ScreenshotFinalSelection
}

func (r importedEvidenceRepository) ListFinalSelections(context.Context, api.PreparedMediaBinding) ([]api.ScreenshotFinalSelection, error) {
	return r.selections, nil
}

func importedEvidenceBytes(t *testing.T, format string) []byte {
	t.Helper()
	var payload bytes.Buffer
	var err error
	source := image.NewRGBA(image.Rect(0, 0, 19, 11))
	switch format {
	case "png":
		err = png.Encode(&payload, source)
	case "jpeg":
		err = jpeg.Encode(&payload, source, nil)
	default:
		return []byte("not an image")
	}
	if err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}
