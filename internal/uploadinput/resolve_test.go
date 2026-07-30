// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package uploadinput

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveMaterializesDescriptionFileAndValidatesManualImages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	descriptionPath := filepath.Join(root, "description.txt")
	imagePath := filepath.Join(root, "comparison.png")
	if err := os.WriteFile(descriptionPath, []byte("Synthetic description."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\nsynthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	primary := 1
	request := api.CreateReleaseWorkflowUploadRequest{
		Descriptions: api.ReleaseWorkflowUploadDescriptions{
			Overrides: []api.ReleaseWorkflowUploadDescriptionOverride{{
				GroupKey: "default",
				File:     &descriptionPath,
			}},
		},
		Media: api.ReleaseWorkflowUploadMedia{
			Screenshots: api.ReleaseWorkflowUploadScreenshots{
				ComparisonPaths:        []string{imagePath},
				ComparisonPrimaryIndex: &primary,
			},
		},
	}
	_, resolved, err := Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("resolve upload inputs: %v", err)
	}
	override := resolved.Descriptions.Overrides[0]
	if override.Inline == nil || *override.Inline != "Synthetic description." ||
		override.File != nil || override.URL != nil {
		t.Fatalf("resolved description = %#v", override)
	}
	media, err := readMediaPaths(resolved.Media.Screenshots.ComparisonPaths, &primary)
	if err != nil {
		t.Fatalf("read manual media: %v", err)
	}
	if len(media) != 1 || media[0].Name != "comparison.png" ||
		media[0].ContentType != "image/png" || len(media[0].Bytes) == 0 {
		t.Fatalf("resolved manual media = %#v", media)
	}
}

func TestDescriptionURLRejectsPrivateAddress(t *testing.T) {
	t.Parallel()

	if _, err := validateDescriptionURL(context.Background(), "http://127.0.0.1/private"); err == nil {
		t.Fatal("private description URL was accepted")
	}
}

func TestReadMediaPathsRejectsInvalidPrimaryIndex(t *testing.T) {
	t.Parallel()

	index := 2
	if _, err := readMediaPaths([]string{`C:\synthetic\one.png`}, &index); err == nil {
		t.Fatal("out-of-range primary index was accepted")
	}
}
