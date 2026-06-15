// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type mockUploadAssistantRepo struct {
	stubRepo
	savedSelections []db.ScreenshotFinalSelection
	savedUploads    map[string][]db.UploadedImageLink
}

func (m *mockUploadAssistantRepo) SaveFinalSelections(_ context.Context, _ string, selections []db.ScreenshotFinalSelection) error {
	m.savedSelections = selections
	return nil
}

func (m *mockUploadAssistantRepo) SaveUploadedImages(_ context.Context, _ string, host string, images []db.UploadedImageLink) error {
	if m.savedUploads == nil {
		m.savedUploads = make(map[string][]db.UploadedImageLink)
	}
	m.savedUploads[host] = images
	return nil
}

func (m *mockUploadAssistantRepo) Save(_ context.Context, _ db.FileMetadata) error {
	return nil
}

func (m *mockUploadAssistantRepo) GetByPath(_ context.Context, _ string) (db.FileMetadata, error) {
	return db.FileMetadata{}, os.ErrNotExist
}

func TestImportUploadAssistantScreenshots(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Create a mock PreparedMetadata
	meta := api.PreparedMetadata{
		SourcePath: filepath.Join(tmpDir, "Example.Movie.mkv"),
	}

	// 2. Set up DB path and temp folder
	dbPath := filepath.Join(tmpDir, "db.sqlite")
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{
			DBPath: dbPath,
		},
	}

	// 3. Resolve the Release Temp Directory
	releaseTempDir, _, err := paths.ReleaseTempDir(filepath.Join(tmpDir, "tmp"), meta, meta.SourcePath)
	if err != nil {
		t.Fatalf("failed to create release temp dir: %v", err)
	}

	// 4. Write mock image_data.json and menu_images.json
	imageDataJSON := `{
		"image_list": [
			{
				"img_url": "https://thumbs2.imgbox.com/aa/bb/123_t.png",
				"raw_url": "https://images2.imgbox.com/aa/bb/123_o.png",
				"web_url": "https://imgbox.com/123"
			}
		],
		"image_sizes": {
			"https://images2.imgbox.com/aa/bb/123_o.png": 123456
		},
		"tonemapped": false
	}`
	if err := os.WriteFile(filepath.Join(releaseTempDir, "image_data.json"), []byte(imageDataJSON), 0o600); err != nil {
		t.Fatalf("failed to write image_data.json: %v", err)
	}

	menuImagesJSON := `{
		"menu_images": [
			{
				"img_url": "https://thumbs2.imgbox.com/cc/dd/456_t.png",
				"raw_url": "https://images2.imgbox.com/cc/dd/456_o.png",
				"web_url": "https://imgbox.com/456"
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(releaseTempDir, "menu_images.json"), []byte(menuImagesJSON), 0o600); err != nil {
		t.Fatalf("failed to write menu_images.json: %v", err)
	}

	// 5. Initialize metadata Service with mock repo
	repo := &mockUploadAssistantRepo{}
	service := NewService(repo, WithConfig(cfg))

	// 6. Run the import
	err = service.importUploadAssistantScreenshots(context.Background(), meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// 7. Validate results
	if len(repo.savedSelections) != 2 {
		t.Fatalf("expected 2 saved selections, got %d", len(repo.savedSelections))
	}

	// First selection (generated)
	sel1 := repo.savedSelections[0]
	if sel1.Source != "generated" {
		t.Fatalf("expected first selection source to be 'generated', got %q", sel1.Source)
	}
	if !strings.HasSuffix(sel1.ImagePath, "123_o.png") {
		t.Fatalf("unexpected first selection image path: %q", sel1.ImagePath)
	}

	// Second selection (menu)
	sel2 := repo.savedSelections[1]
	if sel2.Source != string(api.ScreenshotPurposeMenu) {
		t.Fatalf("expected second selection source to be 'menu', got %q", sel2.Source)
	}
	if !strings.HasSuffix(sel2.ImagePath, "456_o.png") {
		t.Fatalf("unexpected second selection image path: %q", sel2.ImagePath)
	}

	// Uploads
	if len(repo.savedUploads) != 1 {
		t.Fatalf("expected uploads under 1 host, got %d", len(repo.savedUploads))
	}
	imgboxUploads, ok := repo.savedUploads["imgbox"]
	if !ok || len(imgboxUploads) != 2 {
		t.Fatalf("expected 2 uploads under imgbox, got: %+v", repo.savedUploads)
	}

	up1 := imgboxUploads[0]
	if up1.SizeBytes != 123456 {
		t.Fatalf("expected first upload size to be 123456, got %d", up1.SizeBytes)
	}
	if up1.RawURL != "https://images2.imgbox.com/aa/bb/123_o.png" {
		t.Fatalf("unexpected first upload raw url: %s", up1.RawURL)
	}
}
