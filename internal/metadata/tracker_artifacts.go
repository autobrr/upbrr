// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	unit3dImageTimeout  = 20 * time.Second
	unit3dMaxImageBytes = 20 * 1024 * 1024
	unit3dImageWorkers  = 6
)

func (s *Service) persistUnit3DArtifacts(ctx context.Context, meta api.PreparedMetadata, tracker string, result trackerdata.Result, keepImages bool) []string {
	if strings.TrimSpace(result.Description) == "" && (len(result.Validated) == 0 || !keepImages) {
		if s.logger != nil {
			s.logger.Debugf("metadata: unit3d artifacts skipped (no description/images)")
		}
		return nil
	}

	tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: unit3d tmp dir: %v", err)
		}
		return nil
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: unit3d tmp dir: %v", err)
		}
		return nil
	}

	trackerDir := sanitizeFilename(strings.ToLower(tracker))
	if trackerDir == "" {
		trackerDir = "tracker"
	}
	artifactDir := filepath.Join(tmpDir, trackerDir)
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: unit3d artifact dir: %v", err)
		}
		return nil
	}
	if s.logger != nil {
		s.logger.Debugf("metadata: unit3d artifacts dir=%s desc=%t images=%d keepImages=%t", artifactDir, strings.TrimSpace(result.Description) != "", len(result.Validated), keepImages)
	}

	if strings.TrimSpace(result.Description) != "" {
		name := sanitizeFilename(strings.ToLower(tracker)) + "_description.txt"
		path := filepath.Join(artifactDir, name)
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			if s.logger != nil {
				s.logger.Debugf("metadata: unit3d description exists path=%s", path)
			}
		} else if err := os.WriteFile(path, []byte(result.Description), 0o600); err != nil {
			if s.logger != nil {
				s.logger.Warnf("metadata: unit3d description save: %v", err)
			}
		} else if s.logger != nil {
			s.logger.Debugf("metadata: unit3d description saved path=%s", path)
		}
	}

	if !keepImages || len(result.Validated) == 0 {
		if s.logger != nil {
			s.logger.Debugf("metadata: unit3d images skipped keepImages=%t validated=%d", keepImages, len(result.Validated))
		}
		return nil
	}

	client := &http.Client{Timeout: unit3dImageTimeout}
	expectedHeight := parseResolutionHeight(meta.Release.Resolution)
	isDVD := strings.EqualFold(meta.DiscType, "DVD")

	type imageTask struct {
		index int
		url   string
	}

	tasks := make([]imageTask, 0, len(result.Validated))
	for i, image := range result.Validated {
		imgURL := strings.TrimSpace(image.RawURL)
		if imgURL == "" {
			imgURL = strings.TrimSpace(image.ImgURL)
		}
		if imgURL == "" {
			continue
		}
		tasks = append(tasks, imageTask{index: i, url: imgURL})
	}
	if len(tasks) == 0 {
		return nil
	}

	successfulByIndex := make([]string, len(result.Validated))
	jobs := make(chan imageTask)
	workerCount := unit3dImageWorkers
	if len(tasks) < workerCount {
		workerCount = len(tasks)
	}

	var wg sync.WaitGroup
	for workerIdx := 0; workerIdx < workerCount; workerIdx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				if ctx.Err() != nil {
					return
				}

				fileName := buildImageFilename(task.url, task.index)
				outPath := filepath.Join(artifactDir, fileName)
				if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
					if s.logger != nil {
						s.logger.Debugf("metadata: unit3d image exists path=%s", outPath)
					}
					successfulByIndex[task.index] = task.url
					continue
				}

				if err := downloadImage(ctx, client, task.url, outPath, expectedHeight, isDVD); err != nil {
					if s.logger != nil {
						s.logger.Warnf("metadata: unit3d image save: %v", err)
					}
					continue
				}
				if s.logger != nil {
					s.logger.Debugf("metadata: unit3d image saved path=%s", outPath)
				}
				successfulByIndex[task.index] = task.url
			}
		}()
	}

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return collectSuccessfulURLs(successfulByIndex)
		case jobs <- task:
		}
	}
	close(jobs)
	wg.Wait()

	return collectSuccessfulURLs(successfulByIndex)
}

func collectSuccessfulURLs(successfulByIndex []string) []string {
	successfulURLs := make([]string, 0, len(successfulByIndex))
	for _, imgURL := range successfulByIndex {
		if strings.TrimSpace(imgURL) == "" {
			continue
		}
		successfulURLs = append(successfulURLs, imgURL)
	}
	return successfulURLs
}

func buildImageFilename(rawURL string, index int) string {
	parsed, err := url.Parse(rawURL)
	base := ""
	if err == nil {
		base = path.Base(parsed.Path)
	}
	if base == "" || base == "." || base == "/" {
		base = "image"
	}
	base = sanitizeFilename(base)
	if !strings.Contains(base, ".") {
		base = fmt.Sprintf("%s_%02d", base, index+1)
	} else {
		parts := strings.Split(base, ".")
		ext := parts[len(parts)-1]
		base = fmt.Sprintf("%s_%02d.%s", strings.TrimSuffix(base, "."+ext), index+1, ext)
	}
	return base
}

func downloadImage(ctx context.Context, client *http.Client, rawURL string, outPath string, expectedHeight int, isDVD bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "image") {
		return fmt.Errorf("invalid content-type %q", contentType)
	}
	if resp.ContentLength > 0 && resp.ContentLength > unit3dMaxImageBytes {
		return fmt.Errorf("image exceeds max size (%d bytes)", resp.ContentLength)
	}
	limited := io.LimitReader(resp.Body, unit3dMaxImageBytes)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read image body: %w", err)
	}
	if len(payload) == 0 {
		return errors.New("empty image")
	}
	if resp.ContentLength > 0 && int64(len(payload)) < resp.ContentLength {
		return fmt.Errorf("incomplete image (%d of %d bytes)", len(payload), resp.ContentLength)
	}
	imgConfig, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("invalid image data: %w", err)
	}
	if expectedHeight > 0 {
		if err := validateImageResolution(imgConfig.Height, expectedHeight, isDVD); err != nil {
			return err
		}
	}
	if err := os.WriteFile(outPath, payload, 0o600); err != nil {
		return fmt.Errorf("metadata: write screenshot artifact: %w", err)
	}
	return nil
}

func parseResolutionHeight(resolution string) int {
	resMap := map[string]int{
		"8640p": 8640,
		"4320p": 4320,
		"2160p": 2160,
		"1440p": 1440,
		"1080p": 1080,
		"1080i": 1080,
		"720p":  720,
		"576p":  576,
		"576i":  576,
		"480p":  480,
		"480i":  480,
	}
	return resMap[strings.TrimSpace(resolution)]
}

func validateImageResolution(actualHeight int, expectedHeight int, isDVD bool) error {
	if expectedHeight <= 0 {
		return nil
	}
	lowerBound := int(float64(expectedHeight) * 0.70)
	upperBound := expectedHeight
	if isDVD {
		upperBound = int(float64(expectedHeight) * 1.30)
	}
	if actualHeight < lowerBound || actualHeight > upperBound {
		return fmt.Errorf("resolution %dp outside allowed range (%d-%d)", actualHeight, lowerBound, upperBound)
	}
	return nil
}

func sanitizeFilename(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "artifact"
	}
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return r
		default:
			return '_'
		}
	}, trimmed)
	if strings.TrimSpace(cleaned) == "" {
		return "artifact"
	}
	return cleaned
}
