// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path" //nolint:depguard // Extracts URL path components from tracker image URLs.
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/logging"

	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	unit3dImageTimeout = 20 * time.Second
	unit3dImageWorkers = 6
)

var newUnit3DArtifactImageHTTPClient = func() *http.Client {
	return trackerdata.Unit3DImageHTTPClient(&http.Client{Timeout: unit3dImageTimeout})
}

// persistTrackerArtifacts best-effort persists a tracker description and bounded,
// validated images beneath the release's private temporary directory. Existing
// non-empty files are reused, image downloads run concurrently, and the returned
// URL slice preserves input indexes with empty entries for failed downloads.
// Current accepted-but-unavailable downloads return separate reference receipts.
// Cancellation returns URLs completed before workers stop, but no receipts.
func (s *Service) persistTrackerArtifacts(
	ctx context.Context,
	meta preparationstate.State,
	tracker string,
	result trackerdata.Result,
	keepImages bool,
) ([]string, []api.ImportedScreenshotReference) {
	logger := logging.FromContext(ctx, s.logger)

	if strings.TrimSpace(result.Description) == "" && (len(result.Validated) == 0 || !keepImages) {
		logger.Debugf("metadata: tracker artifacts skipped tracker=%s reason=no_description_or_images", tracker)
		return nil, nil
	}

	tmpRoot, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		logger.Warnf("metadata: tracker artifacts temp dir failed tracker=%s: %v", tracker, err)
		return nil, nil
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		logger.Warnf("metadata: tracker artifacts temp dir failed tracker=%s: %v", tracker, err)
		return nil, nil
	}

	trackerDir := sanitizeFilename(strings.ToLower(tracker))
	if trackerDir == "" {
		trackerDir = "tracker"
	}
	artifactDir := filepath.Join(tmpDir, trackerDir)
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		logger.Warnf("metadata: tracker artifact dir failed tracker=%s: %v", tracker, err)
		return nil, nil
	}
	logger.Debugf(
		"metadata: tracker artifacts tracker=%s dir=%s desc=%t images=%d keepImages=%t",
		tracker,
		artifactDir,
		strings.TrimSpace(result.Description) != "",
		len(result.Validated),
		keepImages,
	)

	if strings.TrimSpace(result.Description) != "" {
		name := sanitizeFilename(strings.ToLower(tracker)) + "_description.txt"
		path := filepath.Join(artifactDir, name)
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			logger.Debugf("metadata: tracker description exists tracker=%s path=%s", tracker, path)
		} else if err := os.WriteFile(path, []byte(result.Description), 0o600); err != nil {
			logger.Warnf("metadata: tracker description save failed tracker=%s: %v", tracker, err)
		} else {
			logger.Debugf("metadata: tracker description saved tracker=%s path=%s", tracker, path)
		}
	}

	if !keepImages || len(result.Validated) == 0 {
		logger.Debugf("metadata: tracker images skipped tracker=%s keepImages=%t validated=%d", tracker, keepImages, len(result.Validated))
		return nil, nil
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
		return nil, nil
	}
	if strings.EqualFold(tracker, "PTP") {
		client = trackers.PTPDescriptionImageHTTPClient(ctx, client, s.cfg, logger)
	}

	client = trackerdata.Unit3DImageHTTPClient(client)
	allowReferences := trackerdata.Unit3DImageClientSupportsReferences(client)
	referencesByIndex := make([]string, len(result.Validated))
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
						logger.Warnf("metadata: tracker image save failed tracker=%s index=%d reason=invalid_proxy_source", tracker, task.index+1)
						continue
					}
				}
				fileName := buildImageFilename(artifactURL, task.index)
				outPath := filepath.Join(artifactDir, fileName)
				if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
					logger.Debugf("metadata: tracker image exists tracker=%s index=%d path=%s", tracker, task.index+1, outPath)
					successfulByIndex[task.index] = task.url
					continue
				}

				reference, err := attemptTrackerImageDownload(ctx, client, tracker, task.url, outPath, expectedHeight, isDVD, allowReferences)
				referencesByIndex[task.index] = reference
				if err != nil {
					logger.Warnf("metadata: tracker image save failed tracker=%s index=%d reason=%s", tracker, task.index+1, err.Error())
					continue
				}
				logger.Debugf("metadata: tracker image saved tracker=%s index=%d path=%s", tracker, task.index+1, outPath)
				successfulByIndex[task.index] = task.url
			}
		})
	}

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return collectSuccessfulURLs(successfulByIndex), nil
		case jobs <- task:
		}
	}
	close(jobs)
	wg.Wait()

	var references []api.ImportedScreenshotReference
	if ctx.Err() == nil {
		for _, rawURL := range referencesByIndex {
			if rawURL != "" {
				references = append(references, api.ImportedScreenshotReference{RawURL: rawURL})
			}
		}
	}
	return collectSuccessfulURLs(successfulByIndex), references
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
