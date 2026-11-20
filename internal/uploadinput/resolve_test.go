// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package uploadinput

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
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
	request := api.CreateReleaseWorkflowUploadRequest{
		Descriptions: api.ReleaseWorkflowUploadDescriptions{
			Overrides: []api.ReleaseWorkflowUploadDescriptionOverride{{
				GroupKey: "default",
				File:     &descriptionPath,
			}},
		},
		Media: api.ReleaseWorkflowUploadMedia{
			DVDMenus: api.ReleaseWorkflowUploadDVDMenus{MenuPaths: []string{imagePath}},
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
	media, err := readMediaPaths(resolved.Media.DVDMenus.MenuPaths, false)
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

func comparisonInputFolder(t *testing.T) string {
	t.Helper()
	folder := t.TempDir()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{"02", "10"} {
		for column, label := range []string{"source", "encode", "filtered"} {
			name := fmt.Sprintf("%s-%02d-%s.png", row, column+1, label)
			if err := os.WriteFile(filepath.Join(folder, name), imageBytes.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return folder
}

func TestComparisonInputUsesSourceGroupAndKeepsCompleteRows(t *testing.T) {
	folder := comparisonInputFolder(t)
	index := 2
	images, plan, err := readComparisonPaths([]string{folder}, &index)
	if err != nil || len(images) != 6 || len(plan.Rows) != 2 || len(plan.Sources) != 3 || plan.MainSourceID != "2" {
		t.Fatalf("import=%+v count=%d error=%v", plan, len(images), err)
	}
	if plan.Rows[0].ID != "2" || plan.Rows[1].ID != "10" || plan.Rows[0].Cells[1].SourceID != "2" {
		t.Fatalf("row/source identities changed: %+v", plan.Rows)
	}
	_, _, err = readComparisonPaths([]string{folder}, nil)
	var missing *ComparisonMainRequiredError
	if !errors.As(err, &missing) || len(missing.Sources) != 3 {
		t.Fatalf("missing main choice=%v", err)
	}
	index = 4
	if _, _, err := readComparisonPaths([]string{folder}, &index); err == nil {
		t.Fatal("nonexistent source group was accepted")
	}
}

func TestComparisonInputRejectsBadRowsBeforeWorkflowStarts(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, string)
	}{
		{"missing", func(t *testing.T, folder string) {
			if err := os.Remove(filepath.Join(folder, "02-02-encode.png")); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate", func(t *testing.T, folder string) {
			data, err := os.ReadFile(filepath.Join(folder, "02-02-encode.png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(folder, "02-2-encode.png"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"unreadable", func(t *testing.T, folder string) {
			if err := os.WriteFile(filepath.Join(folder, "02-02-encode.png"), []byte("not an image"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"skipped directory", func(t *testing.T, folder string) {
			if err := os.Mkdir(filepath.Join(folder, "additional-row"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"malformed", func(t *testing.T, folder string) {
			if err := os.Rename(filepath.Join(folder, "02-02-encode.png"), filepath.Join(folder, "custom.png")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			folder := comparisonInputFolder(t)
			test.change(t, folder)
			index := 2
			if _, _, err := readComparisonPaths([]string{folder}, &index); err == nil || strings.TrimSpace(err.Error()) == "" {
				t.Fatal("invalid images reached workflow staging")
			}
		})
	}
}
