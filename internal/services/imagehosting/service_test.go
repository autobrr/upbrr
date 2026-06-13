// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type recordingRepo struct {
	savedHost   string
	savedPath   string
	savedImages []api.UploadedImageLink
	screens     []api.Screenshot
	selections  []api.ScreenshotFinalSelection
	uploads     []api.UploadedImageLink
}

func (r *recordingRepo) SaveUploadedImages(_ context.Context, path string, host string, images []api.UploadedImageLink) error {
	r.savedHost = host
	r.savedPath = path
	r.savedImages = images
	return nil
}

func (r *recordingRepo) GetByPath(context.Context, string) (api.FileMetadata, error) {
	return api.FileMetadata{}, nil
}
func (r *recordingRepo) Save(context.Context, api.FileMetadata) error { return nil }
func (r *recordingRepo) GetExternalIDs(context.Context, string) (api.ExternalIDs, error) {
	return api.ExternalIDs{}, nil
}
func (r *recordingRepo) SaveExternalIDs(context.Context, api.ExternalIDs) error { return nil }
func (r *recordingRepo) GetExternalMetadata(context.Context, string) (api.ExternalMetadata, error) {
	return api.ExternalMetadata{}, nil
}
func (r *recordingRepo) SaveExternalMetadata(context.Context, api.ExternalMetadata) error { return nil }
func (r *recordingRepo) GetDVDMediaInfo(context.Context, string) (api.DVDMediaInfo, error) {
	return api.DVDMediaInfo{}, nil
}
func (r *recordingRepo) SaveDVDMediaInfo(context.Context, api.DVDMediaInfo) error { return nil }
func (r *recordingRepo) GetReleaseNameOverrides(context.Context, string) (api.ReleaseNameOverrides, error) {
	return api.ReleaseNameOverrides{}, nil
}
func (r *recordingRepo) SaveReleaseNameOverrides(context.Context, string, api.ReleaseNameOverrides) error {
	return nil
}
func (r *recordingRepo) DeleteReleaseNameOverrides(context.Context, string) error { return nil }
func (r *recordingRepo) ListHistoryEntries(context.Context) ([]api.HistoryEntry, error) {
	return nil, nil
}
func (r *recordingRepo) ListUploadHistoryByPath(context.Context, string) ([]api.UploadRecord, error) {
	return nil, nil
}
func (r *recordingRepo) ListPendingUploads(context.Context) ([]api.UploadRecord, error) {
	return nil, nil
}
func (r *recordingRepo) CreateUploadRecord(context.Context, api.UploadRecord) error { return nil }
func (r *recordingRepo) UpdateLatestUploadRecordStatus(context.Context, string, string, string) error {
	return nil
}
func (r *recordingRepo) SaveTrackerRuleFailures(context.Context, string, string, []api.TrackerRuleFailure) error {
	return nil
}
func (r *recordingRepo) ListTrackerRuleFailuresByPath(context.Context, string) ([]api.TrackerRuleFailure, error) {
	return nil, nil
}
func (r *recordingRepo) GetTrackerTimestamp(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}
func (r *recordingRepo) SaveTrackerTimestamp(context.Context, api.TrackerTimestamp) error { return nil }
func (r *recordingRepo) SaveTrackerMetadata(context.Context, api.TrackerMetadata) error   { return nil }
func (r *recordingRepo) ListTrackerMetadataByPath(context.Context, string) ([]api.TrackerMetadata, error) {
	return nil, nil
}
func (r *recordingRepo) SaveScreenshot(context.Context, api.Screenshot) error { return nil }
func (r *recordingRepo) ListScreenshotsByPath(context.Context, string) ([]api.Screenshot, error) {
	return append([]api.Screenshot(nil), r.screens...), nil
}
func (r *recordingRepo) DeleteScreenshot(context.Context, string) error { return nil }
func (r *recordingRepo) SaveFinalSelections(context.Context, string, []api.ScreenshotFinalSelection) error {
	return nil
}
func (r *recordingRepo) ListFinalSelections(context.Context, string) ([]api.ScreenshotFinalSelection, error) {
	return append([]api.ScreenshotFinalSelection(nil), r.selections...), nil
}
func (r *recordingRepo) DeleteFinalSelection(context.Context, string) error { return nil }
func (r *recordingRepo) ReplaceScreenshotSlots(context.Context, string, []api.ScreenshotSlot) error {
	return nil
}
func (r *recordingRepo) ListScreenshotSlotsByPath(context.Context, string) ([]api.ScreenshotSlot, error) {
	return nil, nil
}
func (r *recordingRepo) UpsertScreenshotSlotVariants(context.Context, string, []api.ScreenshotSlotVariant) error {
	return nil
}
func (r *recordingRepo) ListUploadedImagesByPath(context.Context, string) ([]api.UploadedImageLink, error) {
	return append([]api.UploadedImageLink(nil), r.uploads...), nil
}
func (r *recordingRepo) DeleteUploadedImage(context.Context, string, string, string, string) error {
	return nil
}
func (r *recordingRepo) GetDescriptionOverride(context.Context, string, string) (api.DescriptionOverride, error) {
	return api.DescriptionOverride{}, nil
}
func (r *recordingRepo) ListDescriptionOverridesByPath(context.Context, string) ([]api.DescriptionOverride, error) {
	return nil, nil
}
func (r *recordingRepo) SaveDescriptionOverride(context.Context, api.DescriptionOverride) error {
	return nil
}
func (r *recordingRepo) DeleteDescriptionOverride(context.Context, string, string) error { return nil }
func (r *recordingRepo) GetPlaylistSelection(context.Context, string) (api.PlaylistSelection, error) {
	return api.PlaylistSelection{}, nil
}
func (r *recordingRepo) SavePlaylistSelection(context.Context, string, []string, bool) error {
	return nil
}
func (r *recordingRepo) DeletePlaylistSelection(context.Context, string) error { return nil }
func (r *recordingRepo) ListStoredReleasePaths(context.Context) ([]string, error) {
	return nil, nil
}
func (r *recordingRepo) PurgeContentData(context.Context, string) error { return nil }

type fakeUploader struct {
	result uploadResult
	err    error
	mu     sync.Mutex
	calls  []string
}

func (u *fakeUploader) Upload(_ context.Context, imagePath string) (uploadResult, error) {
	u.mu.Lock()
	u.calls = append(u.calls, imagePath)
	u.mu.Unlock()
	return u.result, u.err
}

type selectiveUploader struct {
	mu      sync.Mutex
	results map[string]uploadResult
	errs    map[string]error
	calls   []string
}

func (u *selectiveUploader) Upload(_ context.Context, imagePath string) (uploadResult, error) {
	u.mu.Lock()
	u.calls = append(u.calls, imagePath)
	err := u.errs[imagePath]
	result := u.results[imagePath]
	u.mu.Unlock()
	if err != nil {
		return uploadResult{}, err
	}
	return result, nil
}

type blockingUploader struct {
	mu          sync.Mutex
	calls       []string
	firstCallCh chan struct{}
}

func (u *blockingUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	u.mu.Lock()
	u.calls = append(u.calls, imagePath)
	if len(u.calls) == 1 && u.firstCallCh != nil {
		close(u.firstCallCh)
		u.firstCallCh = nil
	}
	u.mu.Unlock()

	<-ctx.Done()
	return uploadResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
}

type fakeBatchUploader struct {
	err          error
	mu           sync.Mutex
	calls        []string
	batches      [][]string
	galleryNames []string
}

func (u *fakeBatchUploader) Upload(_ context.Context, imagePath string) (uploadResult, error) {
	u.mu.Lock()
	u.calls = append(u.calls, imagePath)
	u.mu.Unlock()
	if u.err != nil {
		return uploadResult{}, u.err
	}
	return uploadResult{}, nil
}

func (u *fakeBatchUploader) UploadBatch(_ context.Context, imagePaths []string) ([]uploadResult, error) {
	u.mu.Lock()
	u.batches = append(u.batches, append([]string{}, imagePaths...))
	u.mu.Unlock()
	return u.resultsFor(imagePaths)
}

func (u *fakeBatchUploader) UploadBatchWithName(_ context.Context, imagePaths []string, galleryName string) ([]uploadResult, error) {
	u.mu.Lock()
	u.batches = append(u.batches, append([]string{}, imagePaths...))
	u.galleryNames = append(u.galleryNames, galleryName)
	u.mu.Unlock()
	return u.resultsFor(imagePaths)
}

func (u *fakeBatchUploader) resultsFor(imagePaths []string) ([]uploadResult, error) {
	if u.err != nil {
		return nil, u.err
	}
	results := make([]uploadResult, 0, len(imagePaths))
	for idx := range imagePaths {
		suffix := strconv.Itoa(idx)
		results = append(results, uploadResult{
			ImgURL: "https://thumb/" + suffix,
			RawURL: "https://raw/" + suffix,
			WebURL: "https://web/" + suffix,
		})
	}
	return results, nil
}

func TestUploadImagesSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "shot-01.png")
	if err := os.WriteFile(imagePath, []byte("testdata"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	absPath, err := filepath.Abs(imagePath)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	uploaderStub := &fakeUploader{result: uploadResult{ImgURL: "https://img", RawURL: "https://raw", WebURL: "https://web"}}
	repo := &recordingRepo{}
	service := &Service{
		cfg:       config.Config{ScreenshotHandling: config.ScreenshotHandlingConfig{MaxConcurrentUploads: 2}},
		logger:    api.NopLogger{},
		repo:      repo,
		uploaders: map[string]uploader{"test": uploaderStub},
	}

	meta := api.PreparedMetadata{SourcePath: "source"}
	images := []api.ScreenshotImage{{Path: imagePath}}
	result, err := service.Upload(context.Background(), meta, "test", "tracker:HDB", images)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	if result[0].ImagePath != absPath {
		t.Fatalf("expected image path %q, got %q", absPath, result[0].ImagePath)
	}
	if result[0].Host != "test" {
		t.Fatalf("expected host test, got %q", result[0].Host)
	}
	if result[0].UsageScope != "tracker:HDB" {
		t.Fatalf("expected usage scope tracker:HDB, got %q", result[0].UsageScope)
	}
	if repo.savedHost != "test" || repo.savedPath != "source" {
		t.Fatalf("expected repo save for test/source, got %q/%q", repo.savedHost, repo.savedPath)
	}
	if len(repo.savedImages) != 1 {
		t.Fatalf("expected repo to save 1 image, got %d", len(repo.savedImages))
	}
	if repo.savedImages[0].UsageScope != "tracker:HDB" {
		t.Fatalf("expected repo usage scope tracker:HDB, got %q", repo.savedImages[0].UsageScope)
	}
}

func TestListCandidatesIncludesUploadedOnlyImages(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "tracker-artifact.png")
	if err := os.WriteFile(imagePath, []byte("testdata"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	uploadedAt := time.Now().UTC()
	repo := &recordingRepo{
		uploads: []api.UploadedImageLink{{
			SourcePath: "/tmp/source",
			ImagePath:  imagePath,
			Host:       "pixhost",
			UsageScope: "global",
			ImgURL:     "https://pixhost/img.png",
			RawURL:     "https://pixhost/raw.png",
			WebURL:     "https://pixhost/view",
			UploadedAt: uploadedAt,
			SizeBytes:  8,
		}},
	}
	service := &Service{logger: api.NopLogger{}, repo: repo}

	images, err := service.ListCandidates(context.Background(), api.PreparedMetadata{SourcePath: "/tmp/source"})
	if err != nil {
		t.Fatalf("ListCandidates returned error: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("expected uploaded-only image candidate, got %d", len(images))
	}
	if images[0].Path != imagePath || images[0].Host != "pixhost" || images[0].RawURL != "https://pixhost/raw.png" {
		t.Fatalf("expected uploaded-only candidate details, got %#v", images[0])
	}
	if !images[0].UploadedAt.Equal(uploadedAt) {
		t.Fatalf("expected uploaded timestamp, got %v", images[0].UploadedAt)
	}
}

func TestUploadImagesUnsupportedHost(t *testing.T) {
	service := &Service{logger: api.NopLogger{}, uploaders: map[string]uploader{}}
	meta := api.PreparedMetadata{SourcePath: "source"}
	_, err := service.Upload(context.Background(), meta, "missing", "global", []api.ScreenshotImage{{Path: "x.png"}})
	if err == nil {
		t.Fatal("expected error for unsupported host")
	}
}

func TestUploadImagesRejectsTrackerOwnedHostOutsideOwnerScope(t *testing.T) {
	service := &Service{
		logger:    api.NopLogger{},
		uploaders: map[string]uploader{"hdb": &fakeUploader{}},
	}
	tmp, err := os.CreateTemp(t.TempDir(), "imagehosting-owned-host-*.png")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	meta := api.PreparedMetadata{SourcePath: "source"}
	_, err = service.Upload(context.Background(), meta, "hdb", "global", []api.ScreenshotImage{{Path: tmpPath}})
	if err == nil {
		t.Fatal("expected tracker-owned host outside owner scope to fail")
	}
	if !strings.Contains(err.Error(), "scoped to tracker HDB") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUploadImagesMissingFile(t *testing.T) {
	uploaderStub := &fakeUploader{result: uploadResult{ImgURL: "https://img", RawURL: "https://raw", WebURL: "https://web"}}
	service := &Service{logger: api.NopLogger{}, uploaders: map[string]uploader{"test": uploaderStub}}
	meta := api.PreparedMetadata{SourcePath: "source"}
	_, err := service.Upload(context.Background(), meta, "test", "global", []api.ScreenshotImage{{Path: "missing.png"}})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if len(uploaderStub.calls) != 0 {
		t.Fatalf("expected uploader not called, got %d", len(uploaderStub.calls))
	}
}

func TestImgboxUploaderInRegistry(t *testing.T) {
	cfg := config.Config{}
	registry := newUploaderRegistry(cfg, nil)
	if _, ok := registry["imgbox"]; !ok {
		t.Fatal("imgbox not found in uploader registry")
	}
}

func TestUploadImagesUsesBatchUploader(t *testing.T) {
	tmpDir := t.TempDir()
	firstPath := filepath.Join(tmpDir, "shot-01.png")
	secondPath := filepath.Join(tmpDir, "shot-02.png")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("testdata"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
	}

	uploaderStub := &fakeBatchUploader{}
	service := &Service{
		cfg:       config.Config{},
		logger:    api.NopLogger{},
		repo:      &recordingRepo{},
		uploaders: map[string]uploader{"hdb": uploaderStub},
	}

	_, err := service.Upload(context.Background(), api.PreparedMetadata{
		SourcePath:  "source",
		ReleaseName: "Movie.2026.2160p.WEB-DL",
	}, "hdb", "tracker:HDB", []api.ScreenshotImage{
		{Path: firstPath},
		{Path: secondPath},
	})
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if len(uploaderStub.batches) != 1 {
		t.Fatalf("expected 1 batch call, got %d", len(uploaderStub.batches))
	}
	if len(uploaderStub.batches[0]) != 2 {
		t.Fatalf("expected 2 files in batch, got %d", len(uploaderStub.batches[0]))
	}
	if len(uploaderStub.calls) != 0 {
		t.Fatalf("expected single-image Upload not to be used, got %d calls", len(uploaderStub.calls))
	}
	if len(uploaderStub.galleryNames) != 1 {
		t.Fatalf("expected 1 gallery name call, got %d", len(uploaderStub.galleryNames))
	}
	if uploaderStub.galleryNames[0] != "Movie.2026.2160p.WEB-DL" {
		t.Fatalf("expected content gallery name, got %q", uploaderStub.galleryNames[0])
	}
}

func TestUploadImagesPersistsSuccessfulConcurrentUploadsOnPartialFailure(t *testing.T) {
	tmpDir := t.TempDir()
	firstPath := filepath.Join(tmpDir, "shot-01.png")
	secondPath := filepath.Join(tmpDir, "shot-02.png")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("testdata"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
	}

	uploaderStub := &selectiveUploader{
		results: map[string]uploadResult{
			firstPath: {ImgURL: "https://img/1", RawURL: "https://raw/1", WebURL: "https://web/1"},
		},
		errs: map[string]error{
			secondPath: errors.New("temporary network failure"),
		},
	}
	repo := &recordingRepo{}
	service := &Service{
		cfg: config.Config{
			ScreenshotHandling: config.ScreenshotHandlingConfig{MaxConcurrentUploads: 2},
		},
		logger:    api.NopLogger{},
		repo:      repo,
		uploaders: map[string]uploader{"test": uploaderStub},
	}

	result, err := service.Upload(context.Background(), api.PreparedMetadata{SourcePath: "source"}, "test", "global", []api.ScreenshotImage{
		{Path: firstPath},
		{Path: secondPath},
	})
	if err == nil {
		t.Fatal("expected partial failure error")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 successful upload result, got %d", len(result))
	}
	if len(repo.savedImages) != 1 {
		t.Fatalf("expected repo to persist 1 successful upload, got %d", len(repo.savedImages))
	}
	if repo.savedImages[0].ImagePath == secondPath {
		t.Fatalf("expected failed upload not to be persisted: %#v", repo.savedImages)
	}
}

func TestUploadImagesStopsDispatchingWhenContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	firstPath := filepath.Join(tmpDir, "shot-01.png")
	secondPath := filepath.Join(tmpDir, "shot-02.png")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("testdata"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
	}

	uploaderStub := &blockingUploader{firstCallCh: make(chan struct{})}
	service := &Service{
		cfg: config.Config{
			ScreenshotHandling: config.ScreenshotHandlingConfig{MaxConcurrentUploads: 1},
		},
		logger:    api.NopLogger{},
		uploaders: map[string]uploader{"test": uploaderStub},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstCallCh := uploaderStub.firstCallCh
	done := make(chan error, 1)
	go func() {
		_, err := service.Upload(ctx, api.PreparedMetadata{SourcePath: "source"}, "test", "global", []api.ScreenshotImage{
			{Path: firstPath},
			{Path: secondPath},
		})
		done <- err
	}()

	select {
	case <-firstCallCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first upload to start")
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upload to stop")
	}

	uploaderStub.mu.Lock()
	defer uploaderStub.mu.Unlock()
	if len(uploaderStub.calls) != 1 {
		t.Fatalf("expected only first upload to start, got %d calls", len(uploaderStub.calls))
	}
}
