// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path" //nolint:depguard // Extracts base name from URL path
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/imagehost"
	"github.com/autobrr/upbrr/pkg/api"
)

type uaImageData struct {
	ImageList  []uaImageEntry   `json:"image_list"`
	ImageSizes map[string]int64 `json:"image_sizes"`
	Tonemapped bool             `json:"tonemapped"`
}

type uaMenuImages struct {
	MenuImages []uaImageEntry `json:"menu_images"`
}

type uaImageEntry struct {
	ImgURL string `json:"img_url"`
	RawURL string `json:"raw_url"`
	WebURL string `json:"web_url"`
}

func (s *Service) importUploadAssistantScreenshots(ctx context.Context, meta api.PreparedMetadata) error {
	if s.repo == nil {
		return errors.New("metadata: repository not configured")
	}

	logger := s.logger
	if logger == nil {
		logger = api.NopLogger{}
	}

	tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return fmt.Errorf("metadata: get tmp subdir: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return fmt.Errorf("metadata: get release temp dir: %w", err)
	}

	imageDataPath := filepath.Join(tmpDir, "image_data.json")
	menuImagesPath := filepath.Join(tmpDir, "menu_images.json")

	var imageData *uaImageData
	if payload, err := os.ReadFile(imageDataPath); err == nil {
		var data uaImageData
		if err := json.Unmarshal(payload, &data); err == nil {
			imageData = &data
		} else {
			logger.Warnf("metadata: failed to parse image_data.json: %v", err)
		}
	}

	var menuImages *uaMenuImages
	if payload, err := os.ReadFile(menuImagesPath); err == nil {
		var data uaMenuImages
		if err := json.Unmarshal(payload, &data); err == nil {
			menuImages = &data
		} else {
			logger.Warnf("metadata: failed to parse menu_images.json: %v", err)
		}
	}

	if imageData == nil && menuImages == nil {
		return nil
	}

	logger.Debugf("metadata: importing Upload-Assistant screenshots for %s", meta.SourcePath)

	var selections []api.ScreenshotFinalSelection
	var uploads []api.UploadedImageLink
	now := time.Now().UTC()

	usedNames := make(map[string]bool)
	getUniqueFilename := func(filename string) string {
		if !usedNames[filename] {
			usedNames[filename] = true
			return filename
		}
		ext := filepath.Ext(filename)
		base := strings.TrimSuffix(filename, ext)
		counter := 2
		for {
			candidate := fmt.Sprintf("%s_%d%s", base, counter, ext)
			if !usedNames[candidate] {
				usedNames[candidate] = true
				return candidate
			}
			counter++
		}
	}

	// Parse main screenshots
	if imageData != nil && len(imageData.ImageList) > 0 {
		for idx, entry := range imageData.ImageList {
			rawURL := strings.TrimSpace(entry.RawURL)
			if rawURL == "" {
				rawURL = strings.TrimSpace(entry.ImgURL)
			}
			if rawURL == "" {
				continue
			}

			// Extract filename for synthetic local path
			filename := extractFilenameFromURL(rawURL, "screenshot.png")
			filename = getUniqueFilename(filename)
			syntheticPath := filepath.Join(tmpDir, filename)

			size := int64(0)
			if imageData.ImageSizes != nil {
				size = imageData.ImageSizes[rawURL]
				if size == 0 {
					size = imageData.ImageSizes[entry.ImgURL]
				}
			}

			host := strings.ToLower(strings.TrimSpace(imagehost.ExtractHost(rawURL)))
			if host == "" {
				host = "unknown"
			}

			selections = append(selections, api.ScreenshotFinalSelection{
				SourcePath: meta.SourcePath,
				ImagePath:  syntheticPath,
				Order:      idx,
				Source:     "generated",
				SelectedAt: now,
			})

			uploads = append(uploads, api.UploadedImageLink{
				SourcePath: meta.SourcePath,
				ImagePath:  syntheticPath,
				Host:       host,
				UsageScope: "global",
				ImgURL:     strings.TrimSpace(entry.ImgURL),
				RawURL:     strings.TrimSpace(entry.RawURL),
				WebURL:     strings.TrimSpace(entry.WebURL),
				SizeBytes:  size,
				UploadedAt: now,
			})
		}
	}

	// Parse menu screenshots
	if menuImages != nil && len(menuImages.MenuImages) > 0 {
		startOrder := len(selections)
		for idx, entry := range menuImages.MenuImages {
			rawURL := strings.TrimSpace(entry.RawURL)
			if rawURL == "" {
				rawURL = strings.TrimSpace(entry.ImgURL)
			}
			if rawURL == "" {
				continue
			}

			filename := extractFilenameFromURL(rawURL, "menu_screenshot.png")
			filename = getUniqueFilename(filename)
			syntheticPath := filepath.Join(tmpDir, filename)

			host := strings.ToLower(strings.TrimSpace(imagehost.ExtractHost(rawURL)))
			if host == "" {
				host = "unknown"
			}

			selections = append(selections, api.ScreenshotFinalSelection{
				SourcePath: meta.SourcePath,
				ImagePath:  syntheticPath,
				Order:      startOrder + idx,
				Source:     string(api.ScreenshotPurposeMenu),
				SelectedAt: now,
			})

			uploads = append(uploads, api.UploadedImageLink{
				SourcePath: meta.SourcePath,
				ImagePath:  syntheticPath,
				Host:       host,
				UsageScope: "global",
				ImgURL:     strings.TrimSpace(entry.ImgURL),
				RawURL:     strings.TrimSpace(entry.RawURL),
				WebURL:     strings.TrimSpace(entry.WebURL),
				SizeBytes:  0,
				UploadedAt: now,
			})
		}
	}

	if len(selections) > 0 {
		if err := s.repo.SaveFinalSelections(ctx, meta.SourcePath, selections); err != nil {
			logger.Warnf("metadata: failed to save final selections from Upload-Assistant: %v", err)
		}
	}

	if len(uploads) > 0 {
		// Group by host as SaveUploadedImages expects a single host name
		uploadsByHost := make(map[string][]api.UploadedImageLink)
		for _, upload := range uploads {
			uploadsByHost[upload.Host] = append(uploadsByHost[upload.Host], upload)
		}
		for host, list := range uploadsByHost {
			if err := s.repo.SaveUploadedImages(ctx, meta.SourcePath, host, list); err != nil {
				logger.Warnf("metadata: failed to save uploaded images for host %s from Upload-Assistant: %v", host, err)
			}
		}
	}

	return nil
}

// extractFilenameFromURL extracts a safe filename from a URL, falling back to defaultName.
func extractFilenameFromURL(rawURL, defaultName string) string {
	filename := defaultName
	if parsedURL, err := url.Parse(rawURL); err == nil {
		if base := path.Base(parsedURL.Path); base != "" && base != "." && base != "/" && base != ".." && !strings.ContainsAny(base, `/\`) {
			filename = base
		}
	}
	return filename
}
