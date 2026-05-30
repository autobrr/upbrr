// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png" // register PNG decoder for screenshot metadata loading
	"io/fs"
	"net/url"
	"os"
	"path" //nolint:depguard // Extracts URL path components from screenshot URLs.
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/imagehost"
	"github.com/autobrr/upbrr/pkg/api"
)

type Service struct {
	cfg     config.Config
	logger  api.Logger
	tmpRoot string
	runner  Runner
	repo    api.MetadataRepository
}

func NewService(cfg config.Config, logger api.Logger, tmpRoot string, runner Runner) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	if runner == nil {
		runner = commandRunner{}
	}
	return &Service{cfg: cfg, logger: logger, tmpRoot: tmpRoot, runner: runner}
}

func NewServiceWithRepo(cfg config.Config, logger api.Logger, tmpRoot string, runner Runner, repo api.MetadataRepository) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	if runner == nil {
		runner = commandRunner{}
	}
	return &Service{cfg: cfg, logger: logger, tmpRoot: tmpRoot, runner: runner, repo: repo}
}

func (s *Service) Plan(ctx context.Context, meta api.PreparedMetadata, count int) (api.ScreenshotPlan, error) {
	select {
	case <-ctx.Done():
		return api.ScreenshotPlan{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	plan := api.ScreenshotPlan{
		SourcePath: meta.SourcePath,
		DiscType:   meta.DiscType,
	}

	if count <= 0 {
		count = meta.Options.Screens
	}
	if count <= 0 {
		count = s.cfg.ScreenshotHandling.Screens
	}
	if count <= 0 {
		return api.ScreenshotPlan{}, internalerrors.ErrInvalidInput
	}

	info, err := resolveVideoInfo(ctx, meta, s.tmpRoot)
	if err != nil {
		return api.ScreenshotPlan{}, err
	}
	plan.DurationSeconds = info.DurationSeconds
	plan.FrameRate = info.FrameRate
	plan.MetadataTimestamp = time.Now().UTC().Format(time.RFC3339)

	manualSelections := buildManualFrameSelections(meta.ScreenshotOverrides.ManualFrames, plan.FrameRate)
	if len(manualSelections) == 0 && (plan.DurationSeconds <= 0 || plan.FrameRate <= 0) {
		plan.RequiresManualFrames = true
		return plan, nil
	}

	total := count
	if strings.TrimSpace(meta.DiscType) != "" {
		total = count + 1
	}

	tmpDir, _, err := paths.ReleaseTempDir(s.tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return api.ScreenshotPlan{}, fmt.Errorf("screenshots: %w", err)
	}

	baselineSelections := manualSelections
	if len(baselineSelections) == 0 {
		baselineSelections = buildScreenshotSelections(total, plan.DurationSeconds, plan.FrameRate, meta)
	}
	base := screenshotBaseName(meta)
	baselineByIndex := screenshotSelectionsByIndex(baselineSelections)

	// Load existing screenshots from database that still exist on disk.
	var missingIndexTimestamps []float64
	var existingIndices map[int]struct{}
	if s.repo != nil {
		dbScreenshots, err := s.repo.ListScreenshotsByPath(ctx, meta.SourcePath)
		if err != nil {
			s.logger.Debugf("screenshots: failed to load existing screenshots: %v", err)
		} else {
			existingIndices = make(map[int]struct{}, len(dbScreenshots))
			kept := 0
			for _, shot := range dbScreenshots {
				pathValue := strings.TrimSpace(shot.ImagePath)
				if pathValue == "" || !isAllowedImageExt(pathValue) || !isPathWithinDir(tmpDir, pathValue) {
					continue
				}
				if info, statErr := os.Stat(pathValue); statErr != nil || info.IsDir() {
					continue
				}
				timestamp := shot.Timestamp
				if timestamp <= 0 {
					timestamp = parseScreenshotTimestamp(pathValue, base)
				}
				if index, ok := parseScreenshotIndexStrict(pathValue, base); ok {
					if selection, found := baselineByIndex[index]; found && screenshotTimestampMatchesSelection(timestamp, selection, plan.FrameRate) {
						existingIndices[index] = struct{}{}
					}
				} else {
					missingIndexTimestamps = append(missingIndexTimestamps, timestamp)
				}
				kept++
			}
			s.logger.Debugf("screenshots: found %d existing screenshots in database (%d on disk)", len(dbScreenshots), kept)
		}
	}

	if existingIndices == nil {
		existingIndices = make(map[int]struct{})
	}
	if len(missingIndexTimestamps) > 0 && len(baselineSelections) > 0 {
		sort.Slice(missingIndexTimestamps, func(i, j int) bool { return missingIndexTimestamps[i] < missingIndexTimestamps[j] })
		for _, existingTs := range missingIndexTimestamps {
			bestIndex := -1
			bestDiff := 0.0
			for _, candidate := range baselineSelections {
				if _, used := existingIndices[candidate.Index]; used {
					continue
				}
				diff := abs(candidate.TimestampSeconds - existingTs)
				if bestIndex < 0 || diff < bestDiff {
					bestIndex = candidate.Index
					bestDiff = diff
				}
			}
			if bestIndex >= 0 {
				if selection, found := baselineByIndex[bestIndex]; found && screenshotTimestampMatchesSelection(existingTs, selection, plan.FrameRate) {
					existingIndices[bestIndex] = struct{}{}
				}
			}
		}
	}

	var suggestions []api.ScreenshotSelection
	if len(baselineSelections) > 0 {
		suggestions = make([]api.ScreenshotSelection, 0, total)
		for _, candidate := range baselineSelections {
			if _, used := existingIndices[candidate.Index]; used {
				continue
			}
			suggestions = append(suggestions, candidate)
		}
	}

	plan.SuggestedSelections = suggestions

	plan.ExistingScreenshots = filterScreenshotsMatchingSelections(listExistingScreens(tmpDir, base), baselineSelections, plan.FrameRate)
	plan.TrackerImageLinks = buildTrackerImageLinks(meta, tmpDir)
	plan.ExistingTrackerScreenshots = filterUnlinkedTrackerScreens(
		listTrackerScreens(tmpDir, base),
		plan.TrackerImageLinks,
	)
	plan.FinalSelections = filterScreenshotsMatchingSelections(s.loadFinalSelections(ctx, meta, tmpDir), baselineSelections, plan.FrameRate)

	// Automatically include tracker images in final selections
	plan.FinalSelections = mergeTrackerImagesIntoFinalSelections(plan.FinalSelections, plan.TrackerImageLinks)

	return plan, nil
}

func (s *Service) Capture(ctx context.Context, meta api.PreparedMetadata, selections []api.ScreenshotSelection, purpose api.ScreenshotPurpose) (api.ScreenshotResult, error) {
	select {
	case <-ctx.Done():
		return api.ScreenshotResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if len(selections) == 0 {
		return api.ScreenshotResult{}, internalerrors.ErrInvalidInput
	}

	info, err := resolveVideoInfo(ctx, meta, s.tmpRoot)
	if err != nil {
		return api.ScreenshotResult{}, err
	}

	cmd, err := resolveFFmpeg()
	if err != nil {
		return api.ScreenshotResult{}, err
	}

	tmpDir, _, err := paths.ReleaseTempDir(s.tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return api.ScreenshotResult{}, fmt.Errorf("screenshots: %w", err)
	}

	result := api.ScreenshotResult{
		SourcePath: meta.SourcePath,
		Purpose:    purpose,
	}

	base := screenshotBaseName(meta)
	tonemap := shouldTonemap(meta, s.cfg)
	result.Tonemapped = tonemap

	frameInfo := map[float64]frameInfoResult{}
	if s.cfg.ScreenshotHandling.FrameOverlay {
		for _, selection := range selections {
			ts := selectionTimestamp(selection, info.FrameRate)
			if ts <= 0 {
				continue
			}
			if _, exists := frameInfo[ts]; exists {
				continue
			}
			infoResult, infoErr := getFrameInfo(ctx, s.runner, cmd, info.SourcePath, ts)
			if infoErr != nil {
				s.logger.Debugf("screenshots: frame info failed: %v", infoErr)
				continue
			}
			frameInfo[ts] = infoResult
		}
	}

	type captureJob struct {
		selection api.ScreenshotSelection
		timestamp float64
	}

	errors := make([]api.ScreenshotError, 0)
	jobs := make([]captureJob, 0, len(selections))
	for _, selection := range selections {
		ts := selectionTimestamp(selection, info.FrameRate)
		if ts <= 0 {
			errors = append(errors, api.ScreenshotError{Index: selection.Index, Message: "invalid timestamp"})
			continue
		}
		jobs = append(jobs, captureJob{selection: selection, timestamp: ts})
	}

	if len(jobs) == 0 {
		result.Errors = errors
		return result, nil
	}

	limit := len(jobs)
	if s.cfg.ScreenshotHandling.FFmpegLimit {
		if s.cfg.ScreenshotHandling.ProcessLimit > 0 {
			limit = s.cfg.ScreenshotHandling.ProcessLimit
		} else {
			limit = 1
		}
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > len(jobs) {
		limit = len(jobs)
	}

	jobCh := make(chan captureJob)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var usedLibplacebo atomic.Bool
	images := make([]api.ScreenshotImage, 0, len(jobs))

	worker := func() {
		defer wg.Done()
		for job := range jobCh {
			if ctx.Err() != nil {
				return
			}

			selection := job.selection
			ts := job.timestamp
			outputName := buildScreenshotFilename(base, selection.Index, ts, purpose)
			output := filepath.Join(tmpDir, outputName)
			capture := captureRequest{
				InputPath:     info.SourcePath,
				OutputPath:    output,
				Timestamp:     ts,
				FrameRate:     info.FrameRate,
				Resolution:    meta.Release.Resolution,
				UseLibplacebo: shouldUseLibplacebo(meta, s.cfg),
				ToneMap:       tonemap,
				Algorithm:     s.cfg.ScreenshotHandling.TonemapAlgorithm,
				Desat:         s.cfg.ScreenshotHandling.Desat,
				Compression:   s.cfg.ScreenshotHandling.FFmpegCompression,
				FrameOverlay:  s.cfg.ScreenshotHandling.FrameOverlay,
				OverlaySize:   s.cfg.ScreenshotHandling.OverlayTextSize,
				FrameInfo:     frameInfo[ts],
				SourceWidth:   info.Width,
				SourceHeight:  info.Height,
				WidthScale:    info.WidthScale,
				HeightScale:   info.HeightScale,
			}

			usedLib, captureErr := captureFrame(ctx, s.runner, cmd, capture)
			if captureErr != nil {
				mu.Lock()
				errors = append(errors, api.ScreenshotError{Index: selection.Index, Message: captureErr.Error()})
				mu.Unlock()
				continue
			}

			if usedLib {
				usedLibplacebo.Store(true)
			}

			img := api.ScreenshotImage{Index: selection.Index, TimestampSeconds: ts, Path: output}
			if stat, err := os.Stat(output); err == nil {
				img.SizeBytes = stat.Size()
			}
			if f, err := os.Open(output); err == nil {
				if cfg, _, err := image.DecodeConfig(f); err == nil {
					img.Width = cfg.Width
					img.Height = cfg.Height
				}
				_ = f.Close()
			}

			mu.Lock()
			images = append(images, img)
			mu.Unlock()
		}
	}

	wg.Add(limit)
	for i := 0; i < limit; i++ {
		go worker()
	}

	go func() {
		defer close(jobCh)
		for _, job := range jobs {
			select {
			case <-ctx.Done():
				return
			case jobCh <- job:
			}
		}
	}()

	wg.Wait()

	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("screenshots: generate canceled: %w", err)
	}

	sort.Slice(images, func(i, j int) bool { return images[i].Index < images[j].Index })
	sort.Slice(errors, func(i, j int) bool { return errors[i].Index < errors[j].Index })

	result.Images = images
	result.Errors = errors
	result.UsedLibplacebo = usedLibplacebo.Load()

	if s.repo != nil && len(images) > 0 && purpose != api.ScreenshotPurposePreview {
		for _, img := range images {
			screenshot := api.Screenshot{
				SourcePath:  meta.SourcePath,
				ImagePath:   img.Path,
				Timestamp:   img.TimestampSeconds,
				FrameNumber: int(img.TimestampSeconds * info.FrameRate),
				Width:       img.Width,
				Height:      img.Height,
				Purpose:     purpose,
				CapturedAt:  time.Now().UTC(),
			}
			if err := s.repo.SaveScreenshot(ctx, screenshot); err != nil {
				s.logger.Debugf("screenshots: failed to persist screenshot: %v", err)
			}
		}
	}
	return result, nil
}

func (s *Service) PreviewFrame(ctx context.Context, meta api.PreparedMetadata, timestampSeconds float64) (api.ScreenshotPreview, error) {
	select {
	case <-ctx.Done():
		return api.ScreenshotPreview{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	if timestampSeconds < 0 {
		return api.ScreenshotPreview{}, internalerrors.ErrInvalidInput
	}

	sourcePath, err := resolveVideoSource(ctx, meta, s.tmpRoot)
	if err != nil {
		return api.ScreenshotPreview{}, err
	}

	cmd, err := resolveFFmpeg()
	if err != nil {
		return api.ScreenshotPreview{}, err
	}

	payload, err := captureFrameBytes(ctx, s.runner, cmd, previewRequest{
		InputPath: sourcePath,
		Timestamp: timestampSeconds,
	})
	if err != nil {
		return api.ScreenshotPreview{}, err
	}

	preview := api.ScreenshotPreview{
		TimestampSeconds: timestampSeconds,
		ImageBytes:       payload,
		SizeBytes:        int64(len(payload)),
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(payload)); err == nil {
		preview.Width = cfg.Width
		preview.Height = cfg.Height
	}

	return preview, nil
}

func (s *Service) Delete(ctx context.Context, meta api.PreparedMetadata, imagePath string) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	trimmed := strings.TrimSpace(imagePath)
	if trimmed == "" {
		return internalerrors.ErrInvalidInput
	}
	if s.logger != nil {
		s.logger.Tracef("screenshots: delete requested path=%s", trimmed)
	}

	tmpDir, _, err := paths.ReleaseTempDir(s.tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return fmt.Errorf("screenshots: %w", err)
	}

	absTarget, err := filepath.Abs(trimmed)
	if err != nil {
		return fmt.Errorf("screenshots: resolve delete target path: %w", err)
	}
	absTmp, err := filepath.Abs(tmpDir)
	if err != nil {
		return fmt.Errorf("screenshots: resolve temp path: %w", err)
	}
	if absTarget != absTmp && !strings.HasPrefix(absTarget, absTmp+string(os.PathSeparator)) {
		return internalerrors.ErrInvalidInput
	}
	if s.logger != nil {
		s.logger.Tracef("screenshots: delete resolved abs_target=%s tmp_dir=%s", absTarget, absTmp)
	}

	if !isAllowedImageExt(absTarget) {
		return internalerrors.ErrInvalidInput
	}

	if err := os.Remove(absTarget); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("screenshots: remove captured image: %w", err)
		}
		if s.logger != nil {
			s.logger.Debugf("screenshots: image already missing: %s", absTarget)
		}
	} else if s.logger != nil {
		s.logger.Tracef("screenshots: image deleted from disk: %s", absTarget)
	}

	if s.repo != nil {
		if s.logger != nil {
			s.logger.Tracef("screenshots: deleting db records for %s", absTarget)
		}
		if err := retrySQLiteBusy(ctx, 3, func() error {
			return s.repo.DeleteScreenshot(ctx, absTarget)
		}); err != nil {
			s.logger.Debugf("screenshots: failed to delete screenshot record: %v", err)
		} else if s.logger != nil {
			s.logger.Tracef("screenshots: deleted screenshot record for %s", absTarget)
		}
		if err := retrySQLiteBusy(ctx, 3, func() error {
			return s.repo.DeleteFinalSelection(ctx, absTarget)
		}); err != nil {
			s.logger.Debugf("screenshots: failed to delete final selection: %v", err)
		} else if s.logger != nil {
			s.logger.Tracef("screenshots: deleted final selection for %s", absTarget)
		}
		s.removeTrackerImageReference(ctx, meta, tmpDir, absTarget)
	}

	return nil
}

func (s *Service) removeTrackerImageReference(ctx context.Context, meta api.PreparedMetadata, tmpDir string, absTarget string) {
	if s.repo == nil {
		return
	}
	if strings.TrimSpace(tmpDir) == "" || strings.TrimSpace(absTarget) == "" {
		return
	}
	fileStem := strings.ToLower(strings.TrimSuffix(filepath.Base(absTarget), filepath.Ext(absTarget)))
	records := meta.TrackerData
	if strings.TrimSpace(meta.SourcePath) != "" {
		stored, err := s.repo.ListTrackerMetadataByPath(ctx, meta.SourcePath)
		if err != nil {
			if s.logger != nil {
				s.logger.Debugf("screenshots: failed to load tracker metadata for delete: %v", err)
			}
		} else if len(stored) > 0 {
			records = stored
			if s.logger != nil {
				s.logger.Tracef("screenshots: using stored tracker metadata for delete records=%d", len(stored))
			}
		}
	}
	for _, record := range records {
		if len(record.ImageURLs) == 0 {
			continue
		}
		if s.logger != nil {
			s.logger.Tracef("screenshots: scan tracker images tracker=%s urls=%d", strings.TrimSpace(record.Tracker), len(record.ImageURLs))
		}
		trackerDir := sanitizeFilename(strings.ToLower(strings.TrimSpace(record.Tracker)))
		if trackerDir == "" {
			trackerDir = "tracker"
		}
		filtered := make([]string, 0, len(record.ImageURLs))
		removed := false
		for idx, urlValue := range record.ImageURLs {
			fileName := buildTrackerImageFilename(urlValue, idx)
			if fileName == "" {
				continue
			}
			candidate := filepath.Join(tmpDir, trackerDir, fileName)
			candidateAbs, err := filepath.Abs(candidate)
			if err == nil && pathutil.SamePath(candidateAbs, absTarget) {
				removed = true
				if s.logger != nil {
					s.logger.Tracef("screenshots: tracker image match tracker=%s file=%s", strings.TrimSpace(record.Tracker), candidateAbs)
				}
				continue
			}
			if fileStem != "" {
				baseStem := strings.ToLower(trackerImageBaseStem(urlValue))
				if baseStem != "" && (fileStem == baseStem || strings.HasPrefix(fileStem, baseStem+"_")) {
					removed = true
					if s.logger != nil {
						s.logger.Tracef("screenshots: tracker image stem match tracker=%s url=%s", strings.TrimSpace(record.Tracker), strings.TrimSpace(urlValue))
					}
					continue
				}
			}
			filtered = append(filtered, urlValue)
		}
		if !removed {
			continue
		}
		record.ImageURLs = filtered
		if strings.TrimSpace(record.SourcePath) == "" {
			record.SourcePath = meta.SourcePath
		}
		if err := retrySQLiteBusy(ctx, 3, func() error {
			return s.repo.SaveTrackerMetadata(ctx, record)
		}); err != nil {
			s.logger.Debugf("screenshots: failed to update tracker metadata: %v", err)
		} else if s.logger != nil {
			s.logger.Tracef("screenshots: updated tracker metadata tracker=%s remaining=%d", strings.TrimSpace(record.Tracker), len(record.ImageURLs))
		}
	}
}

func retrySQLiteBusy(ctx context.Context, attempts int, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("screenshots: sqlite retry canceled: %w", err)
		}
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isSQLiteBusyError(err) {
			return err
		}
		if ctx.Err() != nil {
			return fmt.Errorf("context canceled: %w", ctx.Err())
		}
		time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
	}
	return lastErr
}

func isSQLiteBusyError(err error) bool {
	return db.IsBusyError(err)
}

func (s *Service) SaveFinalSelections(ctx context.Context, meta api.PreparedMetadata, images []api.ScreenshotImage) error {
	if s.repo == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	tmpDir, _, err := paths.ReleaseTempDir(s.tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return fmt.Errorf("screenshots: %w", err)
	}

	selections := make([]api.ScreenshotFinalSelection, 0, len(images))
	for index, img := range images {
		pathValue := strings.TrimSpace(img.Path)
		if pathValue == "" {
			return internalerrors.ErrInvalidInput
		}
		if !isAllowedImageExt(pathValue) {
			return internalerrors.ErrInvalidInput
		}
		if !isPathWithinDir(tmpDir, pathValue) {
			return internalerrors.ErrInvalidInput
		}
		selections = append(selections, api.ScreenshotFinalSelection{
			SourcePath: meta.SourcePath,
			ImagePath:  pathValue,
			Order:      index,
			Source:     selectionSourceLabel(img),
			SelectedAt: time.Now().UTC(),
		})
	}

	if err := s.repo.SaveFinalSelections(ctx, meta.SourcePath, selections); err != nil {
		return fmt.Errorf("screenshots: save final selections: %w", err)
	}
	return nil
}

func screenshotBaseName(meta api.PreparedMetadata) string {
	base := paths.ReleaseTempBase(meta, meta.SourcePath)
	return sanitizeFilename(base)
}

func selectionTimestamp(selection api.ScreenshotSelection, frameRate float64) float64 {
	ts := selection.TimestampSeconds
	if ts <= 0 && selection.Frame > 0 && frameRate > 0 {
		ts = float64(selection.Frame) / frameRate
	}
	return ts
}

func screenshotSelectionsByIndex(selections []api.ScreenshotSelection) map[int]api.ScreenshotSelection {
	byIndex := make(map[int]api.ScreenshotSelection, len(selections))
	for _, selection := range selections {
		byIndex[selection.Index] = selection
	}
	return byIndex
}

func screenshotTimestampMatchesSelection(timestamp float64, selection api.ScreenshotSelection, frameRate float64) bool {
	expected := selectionTimestamp(selection, frameRate)
	if timestamp <= 0 || expected <= 0 {
		return false
	}
	tolerance := 0.5
	if frameRate > 0 {
		frameTolerance := 1 / frameRate
		if frameTolerance > tolerance {
			tolerance = frameTolerance
		}
	}
	return abs(timestamp-expected) <= tolerance
}

func filterScreenshotsMatchingSelections(images []api.ScreenshotImage, selections []api.ScreenshotSelection, frameRate float64) []api.ScreenshotImage {
	if len(images) == 0 || len(selections) == 0 {
		return nil
	}
	byIndex := screenshotSelectionsByIndex(selections)
	filtered := make([]api.ScreenshotImage, 0, len(images))
	for _, image := range images {
		selection, ok := byIndex[image.Index]
		if !ok || !screenshotTimestampMatchesSelection(image.TimestampSeconds, selection, frameRate) {
			continue
		}
		filtered = append(filtered, image)
	}
	return filtered
}

func listExistingScreens(tmpDir, base string) []api.ScreenshotImage {
	pattern := filepath.Join(tmpDir, base+"-*.png")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}

	results := make([]api.ScreenshotImage, 0, len(matches))
	for _, match := range matches {
		name := strings.ToLower(filepath.Base(match))
		if base != "" && strings.HasPrefix(name, strings.ToLower(base)+"-preview-") {
			continue
		}
		stat, err := os.Stat(match)
		if err != nil {
			continue
		}
		ts := parseScreenshotTimestamp(match, base)
		results = append(results, api.ScreenshotImage{
			Index:            parseScreenshotIndex(match, base),
			TimestampSeconds: ts,
			Path:             match,
			SizeBytes:        stat.Size(),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		left := results[i]
		right := results[j]
		if left.TimestampSeconds > 0 || right.TimestampSeconds > 0 {
			if left.TimestampSeconds != right.TimestampSeconds {
				return left.TimestampSeconds < right.TimestampSeconds
			}
		}
		return left.Index < right.Index
	})
	return results
}

func listTrackerScreens(tmpDir, base string) []api.ScreenshotImage {
	results := make([]api.ScreenshotImage, 0)
	_ = filepath.WalkDir(tmpDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Dir(path) == tmpDir {
			return nil
		}
		if !isAllowedImageExt(path) {
			return nil
		}
		if base != "" {
			fileName := filepath.Base(path)
			if strings.HasPrefix(fileName, base+"-") && strings.HasSuffix(strings.ToLower(fileName), ".png") {
				return nil
			}
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		results = append(results, api.ScreenshotImage{Path: path, SizeBytes: info.Size()})
		return nil
	})

	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	return results
}

func (s *Service) loadFinalSelections(ctx context.Context, meta api.PreparedMetadata, tmpDir string) []api.ScreenshotImage {
	if s.repo == nil {
		return nil
	}
	selections, err := s.repo.ListFinalSelections(ctx, meta.SourcePath)
	if err != nil {
		s.logger.Debugf("screenshots: failed to load final selections: %v", err)
		return nil
	}
	if len(selections) == 0 {
		return nil
	}
	images := make([]api.ScreenshotImage, 0, len(selections))
	for _, selection := range selections {
		pathValue := strings.TrimSpace(selection.ImagePath)
		if pathValue == "" {
			continue
		}
		if !isAllowedImageExt(pathValue) {
			continue
		}
		if !isPathWithinDir(tmpDir, pathValue) {
			continue
		}
		img, ok := buildScreenshotImage(pathValue, preservedScreenshotIndex(selection.Order))
		if !ok {
			continue
		}
		images = append(images, img)
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Index < images[j].Index })
	return images
}

func buildScreenshotImage(pathValue string, index int) (api.ScreenshotImage, bool) {
	stat, err := os.Stat(pathValue)
	if err != nil {
		return api.ScreenshotImage{}, false
	}
	basePrefix := screenshotBaseFromFilename(pathValue)
	ts := parseScreenshotTimestamp(pathValue, basePrefix)
	img := api.ScreenshotImage{
		Index:            index,
		TimestampSeconds: ts,
		Path:             pathValue,
		SizeBytes:        stat.Size(),
	}
	if f, err := os.Open(pathValue); err == nil {
		if cfg, _, err := image.DecodeConfig(f); err == nil {
			img.Width = cfg.Width
			img.Height = cfg.Height
		}
		_ = f.Close()
	}
	return img, true
}

func isPathWithinDir(root string, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	if pathutil.SamePath(rootAbs, targetAbs) {
		return true
	}
	return pathutil.IsWithinRoot(rootAbs, targetAbs)
}

func selectionSourceLabel(img api.ScreenshotImage) string {
	if img.TimestampSeconds > 0 {
		return "generated"
	}
	return "existing"
}

func filterUnlinkedTrackerScreens(images []api.ScreenshotImage, links []api.ScreenshotLinkedImage) []api.ScreenshotImage {
	if len(images) == 0 || len(links) == 0 {
		return images
	}
	linked := make(map[string]struct{}, len(links))
	for _, link := range links {
		if strings.TrimSpace(link.Path) == "" {
			continue
		}
		linked[link.Path] = struct{}{}
	}
	filtered := make([]api.ScreenshotImage, 0, len(images))
	for _, img := range images {
		if _, ok := linked[img.Path]; ok {
			continue
		}
		filtered = append(filtered, img)
	}
	return filtered
}

func mergeTrackerImagesIntoFinalSelections(finalSelections []api.ScreenshotImage, trackerLinks []api.ScreenshotLinkedImage) []api.ScreenshotImage {
	if len(trackerLinks) == 0 {
		return reindexScreenshotImages(finalSelections)
	}

	// Build set of already selected paths
	existingPaths := make(map[string]struct{}, len(finalSelections))
	for _, img := range finalSelections {
		existingPaths[img.Path] = struct{}{}
	}

	// Add tracker images that aren't already in final selections
	merged := make([]api.ScreenshotImage, len(finalSelections), len(finalSelections)+len(trackerLinks))
	copy(merged, finalSelections)

	for _, link := range trackerLinks {
		if strings.TrimSpace(link.Path) == "" {
			continue
		}
		if _, exists := existingPaths[link.Path]; exists {
			continue
		}
		img, ok := buildScreenshotImage(link.Path, len(merged))
		if !ok {
			continue
		}
		merged = append(merged, img)
		existingPaths[link.Path] = struct{}{}
	}

	return reindexScreenshotImages(merged)
}

func reindexScreenshotImages(images []api.ScreenshotImage) []api.ScreenshotImage {
	for idx := range images {
		images[idx].Index = idx
	}
	return images
}

func buildTrackerImageLinks(meta api.PreparedMetadata, tmpDir string) []api.ScreenshotLinkedImage {
	if strings.TrimSpace(tmpDir) == "" {
		return nil
	}
	if len(meta.TrackerData) == 0 {
		return nil
	}
	results := make([]api.ScreenshotLinkedImage, 0)
	for _, record := range meta.TrackerData {
		tracker := strings.TrimSpace(record.Tracker)
		if tracker == "" {
			continue
		}
		trackerDir := sanitizeFilename(strings.ToLower(tracker))
		if trackerDir == "" {
			trackerDir = "tracker"
		}
		for index, rawURL := range record.ImageURLs {
			trimmed := strings.TrimSpace(rawURL)
			if trimmed == "" {
				continue
			}
			fileName := buildTrackerImageFilename(trimmed, index)
			if fileName == "" {
				continue
			}
			fullPath := filepath.Join(tmpDir, trackerDir, fileName)
			if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
				host := imagehost.ExtractHost(trimmed)
				results = append(results, api.ScreenshotLinkedImage{
					Tracker: tracker,
					URL:     trimmed,
					Path:    fullPath,
					Host:    host,
				})
			}
		}
	}
	return results
}

func buildTrackerImageFilename(rawURL string, index int) string {
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
		return fmt.Sprintf("%s_%02d", base, index+1)
	}
	parts := strings.Split(base, ".")
	ext := parts[len(parts)-1]
	return fmt.Sprintf("%s_%02d.%s", strings.TrimSuffix(base, "."+ext), index+1, ext)
}

func trackerImageBaseStem(rawURL string) string {
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
		return base
	}
	parts := strings.Split(base, ".")
	ext := parts[len(parts)-1]
	return strings.TrimSuffix(base, "."+ext)
}

func parseScreenshotIndex(path string, base string) int {
	name := strings.TrimSuffix(filepath.Base(path), ".png")
	if base != "" {
		prefix := base + "-"
		name = strings.TrimPrefix(name, prefix)
	}
	name = strings.TrimPrefix(name, "preview-")
	parts := strings.SplitN(name, "-", 2)
	if len(parts) == 0 {
		return 0
	}
	parsed, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return parsed
}

func parseScreenshotIndexStrict(path string, base string) (int, bool) {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if base != "" {
		prefix := base + "-"
		if !strings.HasPrefix(name, prefix) {
			return 0, false
		}
		name = strings.TrimPrefix(name, prefix)
	}
	name = strings.TrimPrefix(name, "preview-")
	parts := strings.SplitN(name, "-", 2)
	if len(parts) == 0 || parts[0] == "" {
		return 0, false
	}
	for _, ch := range parts[0] {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func buildScreenshotFilename(base string, index int, timestampSeconds float64, purpose api.ScreenshotPurpose) string {
	ms := int64(timestampSeconds * 1000)
	if ms < 0 {
		ms = 0
	}
	stamp := fmt.Sprintf("ss_%08d", ms)
	if purpose == api.ScreenshotPurposePreview {
		return fmt.Sprintf("%s-preview-%02d-%s-%d.png", base, index, stamp, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%02d-%s.png", base, index, stamp)
}

func parseScreenshotTimestamp(path string, base string) float64 {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if base != "" {
		prefix := base + "-"
		name = strings.TrimPrefix(name, prefix)
	}
	name = strings.TrimPrefix(name, "preview-")
	parts := strings.Split(name, "-")
	for _, part := range parts {
		if !strings.HasPrefix(part, "ss_") {
			continue
		}
		value := strings.TrimPrefix(part, "ss_")
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		return float64(parsed) / 1000.0
	}
	return 0
}

func screenshotBaseFromFilename(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if strings.Contains(name, "-preview-") {
		return strings.SplitN(name, "-preview-", 2)[0]
	}
	if strings.Contains(name, "-") {
		return strings.SplitN(name, "-", 2)[0]
	}
	return name
}

func isAllowedImageExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp":
		return true
	default:
		return false
	}
}
