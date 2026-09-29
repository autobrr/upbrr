// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder for tracker image validation
	"io"
	"net/http"
	"net/url"
	"os"
	"path" //nolint:depguard // Extracts URL path components from tracker image URLs.
	"path/filepath"
	"strings"
	"sync"
	"time"

	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
)

const (
	unit3dImageTimeout  = 20 * time.Second
	unit3dMaxImageBytes = 20 * 1024 * 1024
	unit3dImageWorkers  = 6
)

var newUnit3DArtifactImageHTTPClient = func() *http.Client {
	return trackerdata.Unit3DImageHTTPClient(&http.Client{Timeout: unit3dImageTimeout})
}

var validateTrackerArtifactImageURL = trackerdata.ValidateUnit3DImageURL

// persistTrackerArtifacts best-effort persists a tracker description and bounded,
// validated images beneath the release's private temporary directory. Existing
// non-empty files are reused, image downloads run concurrently, and the returned
// URL slice preserves input indexes with empty entries for failed downloads.
// Cancellation returns URLs completed before workers stop.
func (s *Service) persistTrackerArtifacts(
	ctx context.Context,
	meta preparationstate.State,
	tracker string,
	result trackerdata.Result,
	keepImages bool,
) []string {
	if strings.TrimSpace(result.Description) == "" && (len(result.Validated) == 0 || !keepImages) {
		if s.logger != nil {
			s.logger.Debugf("metadata: tracker artifacts skipped tracker=%s reason=no_description_or_images", tracker)
		}
		return nil
	}

	tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: tracker artifacts temp dir failed tracker=%s: %v", tracker, err)
		}
		return nil
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: tracker artifacts temp dir failed tracker=%s: %v", tracker, err)
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
			s.logger.Warnf("metadata: tracker artifact dir failed tracker=%s: %v", tracker, err)
		}
		return nil
	}
	if s.logger != nil {
		s.logger.Debugf(
			"metadata: tracker artifacts tracker=%s dir=%s desc=%t images=%d keepImages=%t",
			tracker,
			artifactDir,
			strings.TrimSpace(result.Description) != "",
			len(result.Validated),
			keepImages,
		)
	}

	if strings.TrimSpace(result.Description) != "" {
		name := sanitizeFilename(strings.ToLower(tracker)) + "_description.txt"
		path := filepath.Join(artifactDir, name)
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			if s.logger != nil {
				s.logger.Debugf("metadata: tracker description exists tracker=%s path=%s", tracker, path)
			}
		} else if err := os.WriteFile(path, []byte(result.Description), 0o600); err != nil {
			if s.logger != nil {
				s.logger.Warnf("metadata: tracker description save failed tracker=%s: %v", tracker, err)
			}
		} else if s.logger != nil {
			s.logger.Debugf("metadata: tracker description saved tracker=%s path=%s", tracker, path)
		}
	}

	if !keepImages || len(result.Validated) == 0 {
		if s.logger != nil {
			s.logger.Debugf("metadata: tracker images skipped tracker=%s keepImages=%t validated=%d", tracker, keepImages, len(result.Validated))
		}
		return nil
	}

	client := newUnit3DArtifactImageHTTPClient()
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
	if strings.EqualFold(tracker, "PTP") {
		client = trackers.PTPDescriptionImageHTTPClient(ctx, client, s.cfg, s.logger)
	}

	successfulByIndex := make([]string, len(result.Validated))
	jobs := make(chan imageTask)
	workerCount := min(len(tasks), unit3dImageWorkers)

	var wg sync.WaitGroup
	for range workerCount {
		wg.Go(func() {
			for task := range jobs {
				if ctx.Err() != nil {
					return
				}

				artifactURL := task.url
				if imagehost.IsWsrvProxyURL(task.url) {
					artifactURL = imagehost.DirectImageURL(task.url)
					if artifactURL == "" {
						if s.logger != nil {
							s.logger.Warnf("metadata: tracker image save failed tracker=%s index=%d reason=invalid_proxy_source", tracker, task.index+1)
						}
						continue
					}
				}
				fileName := buildImageFilename(artifactURL, task.index)
				outPath := filepath.Join(artifactDir, fileName)
				if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
					if s.logger != nil {
						s.logger.Debugf("metadata: tracker image exists tracker=%s index=%d path=%s", tracker, task.index+1, outPath)
					}
					successfulByIndex[task.index] = task.url
					continue
				}

				if reason := downloadImage(ctx, client, task.url, outPath, expectedHeight, isDVD); reason != "" {
					if s.logger != nil {
						s.logger.Warnf("metadata: tracker image save failed tracker=%s index=%d reason=%s", tracker, task.index+1, reason)
					}
					continue
				}
				if s.logger != nil {
					s.logger.Debugf("metadata: tracker image saved tracker=%s index=%d path=%s", tracker, task.index+1, outPath)
				}
				successfulByIndex[task.index] = task.url
			}
		})
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
	successfulURLs := make([]string, len(successfulByIndex))
	hasSuccessful := false
	for idx, imgURL := range successfulByIndex {
		trimmed := strings.TrimSpace(imgURL)
		if trimmed == "" {
			continue
		}
		successfulURLs[idx] = trimmed
		hasSuccessful = true
	}
	if !hasSuccessful {
		return nil
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
	digest := sha256.Sum256([]byte(rawURL))
	suffix := fmt.Sprintf("_%02d_%x", index+1, digest[:6])
	if !strings.Contains(base, ".") {
		base += suffix
	} else {
		parts := strings.Split(base, ".")
		ext := parts[len(parts)-1]
		base = strings.TrimSuffix(base, "."+ext) + suffix + "." + ext
	}
	return base
}

// downloadImage fetches the validated direct image source into a private
// artifact. It returns a URL-free failure reason for operator diagnostics.
func downloadImage(ctx context.Context, client *http.Client, rawURL string, outPath string, expectedHeight int, isDVD bool) string {
	requestURL := imagehost.DirectImageURL(rawURL)
	if requestURL == "" {
		return "invalid_proxy_source"
	}
	requestURL = trackers.PTPDescriptionImageDownloadURL(requestURL)
	if err := validateTrackerArtifactImageURL(ctx, requestURL); err != nil {
		return "invalid_or_nonpublic_url"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "invalid_request"
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "request_timeout"
		}
		return "request_failed"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("http_status_%d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" && !strings.Contains(contentType, "image") {
		return "non_image_content_type"
	}
	if resp.ContentLength > 0 && resp.ContentLength > unit3dMaxImageBytes {
		return "image_too_large"
	}
	limited := io.LimitReader(resp.Body, unit3dMaxImageBytes)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return "image_read_failed"
	}
	if len(payload) == 0 {
		return "empty_image"
	}
	if resp.ContentLength > 0 && int64(len(payload)) < resp.ContentLength {
		return "incomplete_image"
	}
	imgConfig, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		return "image_decode_failed"
	}
	if expectedHeight > 0 {
		if err := validateImageResolution(imgConfig.Height, expectedHeight, isDVD); err != nil {
			return fmt.Sprintf("resolution_mismatch_actual_%d_expected_%d", imgConfig.Height, expectedHeight)
		}
	}
	if err := os.WriteFile(outPath, payload, 0o600); err != nil {
		return "image_write_failed"
	}
	return ""
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
