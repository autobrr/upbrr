// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const lostimgImagesEndpoint = "https://lostimg.cc/api/v1/images"

var errLiveImageBudget = errors.New("image hosting: live-test image upload budget exceeded")
var errLiveImageReconciliation = errors.New("image hosting: live-test image upload outcome requires reconciliation")

// CleanupImageResult is a shareable outcome containing only a run-local opaque ID.
type CleanupImageResult struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// CleanupReport accounts for uploaded images and unresolved upload attempts without URLs.
// Unconfirmed deletions are retained with a reason and are never retried automatically;
// Unknown counts are reserved for upload outcomes that require reconciliation.
type CleanupReport struct {
	RunID    string               `json:"runId"`
	Deleted  int                  `json:"deleted"`
	Retained int                  `json:"retained"`
	Pending  int                  `json:"pending"`
	Unknown  int                  `json:"unknown"`
	Failed   int                  `json:"failed"`
	Images   []CleanupImageResult `json:"images"`
}

// NewLiveTestServiceWithRegistry wraps every configured production uploader in
// a durable live-test journal guard. The caller must own the run lock and
// validate the private run directory. Lostimg cleanup remains supported;
// successful uploads to other providers are retained and reported.
// maxImages bounds all journaled upload attempts; zero disables remote uploads.
func NewLiveTestServiceWithRegistry(
	cfg config.Config, logger api.Logger, repo repository, registry *trackers.Registry, runID, journalPath string,
	maxImages int,
) (*Service, error) {
	if maxImages < 0 || maxImages > api.MaxLiveTestImageUploads {
		return nil, fmt.Errorf("image hosting: live-test image upload budget must be between 0 and %d", api.MaxLiveTestImageUploads)
	}
	journal, err := newImageEffectJournal(runID, journalPath)
	if err != nil {
		return nil, err
	}
	service := NewServiceWithRegistry(cfg, logger, repo, registry)
	client := liveImageClient(service.client)
	service.client = client
	service.uploaders = newUploaderRegistry(cfg, client, registry)
	for provider, delegate := range service.uploaders {
		service.uploaders[provider] = wrapLiveTestUploader(provider, delegate, journal, maxImages)
	}
	return service, nil
}

func liveImageClient(base *http.Client) *http.Client {
	client := httpclient.CloneWithTimeout(base, httpclient.UploadTimeout)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

type liveTestUploader struct {
	provider  string
	delegate  uploader
	journal   *imageEffectJournal
	maxImages int
	uploadMu  sync.Mutex
}

type liveTestBatchUploader struct {
	*liveTestUploader
	batch batchUploader
}

type liveTestNamedBatchUploader struct {
	*liveTestBatchUploader
	named namedBatchUploader
}

func wrapLiveTestUploader(provider string, delegate uploader, journal *imageEffectJournal, maxImages int) uploader {
	guard := &liveTestUploader{
		provider:  provider,
		delegate:  delegate,
		journal:   journal,
		maxImages: maxImages,
	}
	batch, supportsBatch := delegate.(batchUploader)
	if !supportsBatch {
		return guard
	}
	guardedBatch := &liveTestBatchUploader{liveTestUploader: guard, batch: batch}
	if named, ok := delegate.(namedBatchUploader); ok {
		return &liveTestNamedBatchUploader{liveTestBatchUploader: guardedBatch, named: named}
	}
	return guardedBatch
}

func (u *liveTestUploader) Upload(ctx context.Context, imagePath string) (uploadResult, error) {
	results, err := u.upload(ctx, []string{imagePath}, func() ([]uploadResult, error) {
		result, uploadErr := u.delegate.Upload(ctx, imagePath)
		if uploadErr != nil {
			return nil, fmt.Errorf("live-test image upload: %w", uploadErr)
		}
		return []uploadResult{result}, nil
	})
	if err != nil {
		return uploadResult{}, err
	}
	return results[0], nil
}

func (u *liveTestBatchUploader) UploadBatch(ctx context.Context, imagePaths []string) ([]uploadResult, error) {
	if u.provider == "lostimg" && u.journal.version == legacyImageJournalVersion && len(imagePaths) > lostimgMaxBatchUploadImages {
		return u.uploadLegacyLostimgBatch(ctx, imagePaths)
	}
	return u.upload(ctx, imagePaths, func() ([]uploadResult, error) { return u.batch.UploadBatch(ctx, imagePaths) })
}

func (u *liveTestBatchUploader) uploadLegacyLostimgBatch(ctx context.Context, imagePaths []string) ([]uploadResult, error) {
	u.uploadMu.Lock()
	defer u.uploadMu.Unlock()

	hashes, err := imageSourceHashes(imagePaths)
	if err != nil {
		return nil, err
	}
	if err := u.checkUpload(hashes); err != nil {
		if errors.Is(err, errLiveImageReconciliation) {
			return nil, liveImageUnknownOutcome(err)
		}
		return nil, err
	}
	results := make([]uploadResult, 0, len(imagePaths))
	for start := 0; start < len(imagePaths); start += lostimgMaxBatchUploadImages {
		chunk := imagePaths[start:min(start+lostimgMaxBatchUploadImages, len(imagePaths))]
		chunkResults, uploadErr := u.uploadLocked(ctx, chunk, func() ([]uploadResult, error) {
			return u.batch.UploadBatch(ctx, chunk)
		})
		results = append(results, chunkResults...)
		if uploadErr != nil {
			return results, uploadErr
		}
	}
	return results, nil
}

func (u *liveTestNamedBatchUploader) UploadBatchWithName(
	ctx context.Context,
	imagePaths []string,
	galleryName string,
) ([]uploadResult, error) {
	return u.upload(ctx, imagePaths, func() ([]uploadResult, error) {
		return u.named.UploadBatchWithName(ctx, imagePaths, galleryName)
	})
}

func (u *liveTestUploader) upload(
	ctx context.Context,
	paths []string,
	upload func() ([]uploadResult, error),
) ([]uploadResult, error) {
	u.uploadMu.Lock()
	defer u.uploadMu.Unlock()
	return u.uploadLocked(ctx, paths, upload)
}

func (u *liveTestUploader) uploadLocked(
	ctx context.Context,
	paths []string,
	upload func() ([]uploadResult, error),
) ([]uploadResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("image hosting: live-test upload canceled: %w", err)
	}
	if len(paths) == 0 {
		return nil, errors.New("image hosting: live-test upload requires images")
	}
	hashes, err := imageSourceHashes(paths)
	if err != nil {
		return nil, err
	}
	record, err := u.beginUpload(hashes)
	if err != nil {
		if errors.Is(err, errLiveImageReconciliation) {
			return nil, liveImageUnknownOutcome(err)
		}
		return nil, err
	}
	results, uploadErr := upload()
	urls, uploaded, valid := liveUploadResultURLs(results, len(paths), u.provider)
	returned := u.journal.recordForProvider("uploaded", record.ID, u.provider)
	returned.URLs = urls
	returned.Uploaded = uploaded
	if returned.Version == legacyImageJournalVersion {
		returned.Uploaded = 0
	}
	returned.Complete = uploadErr == nil && valid && len(results) == len(paths) && uploaded == len(paths)
	if err := u.finishUpload(returned); err != nil {
		return nil, liveImageUnknownOutcome(errors.Join(errLiveImageReconciliation, err))
	}
	if !returned.Complete {
		return results, liveImageUnknownOutcome(errors.Join(errLiveImageReconciliation, uploadErr))
	}
	return results, nil
}

func (u *liveTestUploader) checkUpload(hashes []string) error {
	imageEffectsMu.Lock()
	defer imageEffectsMu.Unlock()
	state, err := u.journal.read()
	if err != nil {
		return err
	}
	return u.validateUpload(state, hashes)
}

func (u *liveTestUploader) beginUpload(hashes []string) (imageEffectRecord, error) {
	imageEffectsMu.Lock()
	defer imageEffectsMu.Unlock()
	state, err := u.journal.read()
	if err != nil {
		return imageEffectRecord{}, err
	}
	if err := u.validateUpload(state, hashes); err != nil {
		return imageEffectRecord{}, err
	}
	record := u.journal.recordForProvider("upload_pending", newImageEffectID(), u.provider)
	record.Sources = slices.Clone(hashes)
	if err := u.journal.append(record); err != nil {
		return imageEffectRecord{}, err
	}
	return record, nil
}

func (u *liveTestUploader) validateUpload(state *imageEffectState, hashes []string) error {
	if state.cleanupStarted {
		return errors.New("image hosting: live-test image cleanup has already started")
	}
	if state.version == legacyImageJournalVersion && u.provider != "lostimg" {
		return errors.New("image hosting: legacy live-test journals support Lostimg uploads only")
	}
	remaining := u.maxImages
	for _, attempt := range state.attempts {
		if len(attempt.sources) > remaining {
			return errLiveImageBudget
		}
		remaining -= len(attempt.sources)
	}
	if len(hashes) > remaining {
		return errLiveImageBudget
	}
	for _, attempt := range state.attempts {
		if attempt.complete || attempt.provider != u.provider {
			continue
		}
		for _, hash := range hashes {
			if slices.Contains(attempt.sources, hash) {
				return errLiveImageReconciliation
			}
		}
	}
	return nil
}

func (u *liveTestUploader) finishUpload(record imageEffectRecord) error {
	imageEffectsMu.Lock()
	defer imageEffectsMu.Unlock()
	state, err := u.journal.read()
	if err != nil {
		return err
	}
	if state.cleanupStarted {
		return errLiveImageReconciliation
	}
	if err := state.apply(record); err != nil {
		return err
	}
	return u.journal.append(record)
}

func liveUploadResultURLs(results []uploadResult, expected int, provider string) ([]string, int, bool) {
	urls := make([]string, 0, len(results)*3)
	seen := make(map[string]struct{}, len(results)*3)
	uploaded := 0
	valid := len(results) <= expected
	for _, result := range results {
		resultValid := false
		resultURLs := make(map[string]struct{}, 3)
		for _, raw := range []string{result.ImgURL, result.RawURL, result.WebURL} {
			if raw == "" {
				continue
			}
			if !validEffectURL(raw) {
				valid = false
				continue
			}
			resultValid = true
			if _, exists := resultURLs[raw]; exists {
				continue
			}
			resultURLs[raw] = struct{}{}
			if _, exists := seen[raw]; exists && provider != "lostimg" {
				continue
			}
			seen[raw] = struct{}{}
			urls = append(urls, raw)
		}
		if resultValid {
			uploaded++
		} else {
			valid = false
		}
	}
	if uploaded > expected {
		uploaded = expected
		valid = false
	}
	return urls, uploaded, valid
}

func liveImageUnknownOutcome(cause error) error {
	return api.NewOperationError(api.OperationFailure{
		Code:      api.OperationFailureUnknownOutcome,
		Operation: api.OperationKindImageHosting,
		Message:   "A live-test image upload outcome is uncertain. Reconcile the private run journal before retrying.",
		Recovery:  api.OperationRecoveryConfirm,
	}, cause)
}

func imageSourceHashes(paths []string) ([]string, error) {
	hashes := make([]string, 0, len(paths))
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return nil, errors.New("image hosting: cannot read live-test source image")
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return nil, errors.New("image hosting: cannot hash live-test source image")
		}
		hashes = append(hashes, hex.EncodeToString(hash.Sum(nil)))
	}
	return hashes, nil
}

// Retain validated ownership fields even if another response field is invalid.
// The boolean requires the documented success shape, without contradictory fields.
func parseLiveImageURLs(body []byte) ([]string, bool) {
	var response struct {
		URL   string   `json:"url"`
		URLs  []string `json:"urls"`
		Error string   `json:"error"`
	}
	decodeErr := json.Unmarshal(body, &response)
	valid := decodeErr == nil && response.Error == "" && (response.URL == "" || len(response.URLs) == 0)
	rawURLs := response.URLs
	if response.URL != "" {
		rawURLs = append(rawURLs, response.URL)
	}
	urls := make([]string, 0, len(rawURLs))
	seen := make(map[string]bool)
	for _, raw := range rawURLs {
		if !validEffectURL(raw) || seen[raw] {
			valid = false
			continue
		}
		seen[raw] = true
		urls = append(urls, raw)
	}
	return urls, valid && len(urls) > 0
}

// CleanupLiveTestImages deletes only exact returned URLs backed by this run's
// validated journal. The caller owns the cross-process run lock and private path
// validation, and supplies a fresh bounded context after stopping the workflow.
func CleanupLiveTestImages(ctx context.Context, cfg config.Config, runID, journalPath string) (CleanupReport, error) {
	return cleanupLiveTestImages(ctx, cfg, runID, journalPath, liveImageClient(nil))
}

func cleanupLiveTestImages(ctx context.Context, cfg config.Config, runID, journalPath string, client *http.Client) (CleanupReport, error) {
	imageEffectsMu.Lock()
	defer imageEffectsMu.Unlock()
	journal := &imageEffectJournal{path: journalPath, runID: runID}
	state, err := journal.read()
	if err != nil {
		return CleanupReport{}, err
	}
	report := state.report(runID)
	if report.Pending == 0 {
		return report, cleanupReportError(report)
	}
	// Any prior outcome for an exact URL applies to duplicate returned references.
	// This prevents repeating a possibly completed delete across attempts or chunks.
	prior := make(map[string]string)
	var pending []string
	for _, image := range report.Images {
		switch image.State {
		case "uploaded":
			pending = append(pending, image.ID)
		case "deleted":
			prior[state.urls[image.ID]] = "deleted"
		case "cleanup_unknown":
			raw := state.urls[image.ID]
			if raw != "" && prior[raw] != "deleted" {
				prior[raw] = "cleanup_unknown"
			}
		}
	}
	for start := 0; start < len(pending); start += lostimgMaxBatchUploadImages {
		ids := pending[start:min(start+lostimgMaxBatchUploadImages, len(pending))]
		record := journal.record("cleanup_pending", newImageEffectID())
		var urls []string
		for _, id := range ids {
			record.Images = append(record.Images, CleanupImageResult{ID: id, State: "cleanup_pending"})
			raw := state.urls[id]
			if prior[raw] == "" && !slices.Contains(urls, raw) {
				urls = append(urls, raw)
			}
		}
		if err := journal.append(record); err != nil {
			return state.report(runID), err
		}
		if err := state.apply(record); err != nil {
			return state.report(runID), err
		}
		confirmed := make(map[string]bool)
		if strings.TrimSpace(cfg.ImageHosting.LostimgAPI) != "" && ctx.Err() == nil {
			confirmed = deleteLiveImages(ctx, client, cfg.ImageHosting.LostimgAPI, urls)
		}
		for _, raw := range urls {
			prior[raw] = "cleanup_unknown"
			if confirmed[raw] {
				prior[raw] = "deleted"
			}
		}
		result := journal.record("cleanup_result", record.ID)
		for _, id := range ids {
			outcome := CleanupImageResult{ID: id, State: prior[state.urls[id]]}
			if outcome.State != "deleted" {
				outcome.State = "cleanup_unknown"
				outcome.Reason = "provider_unconfirmed"
			}
			result.Images = append(result.Images, outcome)
		}
		if err := journal.append(result); err != nil {
			return state.report(runID), err
		}
		if err := state.apply(result); err != nil {
			return state.report(runID), err
		}
	}
	report = state.report(runID)
	return report, cleanupReportError(report)
}

func deleteLiveImages(ctx context.Context, client *http.Client, apiKey string, urls []string) map[string]bool {
	confirmed := make(map[string]bool)
	if len(urls) == 0 {
		return confirmed
	}
	body, err := json.Marshal(struct {
		URLs []string `json:"urls"`
	}{URLs: urls})
	if err != nil {
		return confirmed
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, lostimgImagesEndpoint, bytes.NewReader(body))
	if err != nil {
		return confirmed
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	request.Header.Set("Content-Type", "application/json")
	response, err := liveImageClient(client).Do(request)
	if err != nil {
		closeResponseBody(response)
		return confirmed
	}
	data, readErr := readLimitedAndCloseResponseBody(response)
	if readErr != nil || response.StatusCode != http.StatusOK {
		return confirmed
	}
	returned, valid := parseLiveImageURLs(data)
	if !valid {
		return confirmed
	}
	for _, raw := range returned {
		if !slices.Contains(urls, raw) {
			return confirmed
		}
	}
	for _, raw := range returned {
		confirmed[raw] = true
	}
	return confirmed
}

func (s *imageEffectState) report(runID string) CleanupReport {
	report := CleanupReport{RunID: runID, Images: []CleanupImageResult{}}
	for _, attemptID := range s.order {
		attempt := s.attempts[attemptID]
		for index := range attempt.uploaded {
			image := s.images[fmt.Sprintf("%s_%d", attemptID, index)]
			switch image.State {
			case "deleted":
				report.Deleted++
			case "retained":
				report.Retained++
			case "uploaded":
				report.Pending++
			case "cleanup_pending", "cleanup_unknown":
				image.State = "retained"
				image.Reason = "deletion_unconfirmed"
				report.Retained++
			default:
				image.State = "cleanup_unknown"
				image.Reason = "provider_unconfirmed"
				report.Unknown++
			}
			report.Images = append(report.Images, image)
		}
		missing := max(0, len(attempt.sources)-attempt.uploaded)
		// Contradictory/malformed replies may hide effects even when URL counts match.
		if !attempt.complete && missing == 0 {
			missing = 1
		}
		for index := range missing {
			report.Unknown++
			report.Images = append(report.Images, CleanupImageResult{
				ID:     fmt.Sprintf("%s_unknown_%d", attemptID, index),
				State:  "cleanup_unknown",
				Reason: "upload_unconfirmed",
			})
		}
	}
	return report
}

func cleanupReportError(report CleanupReport) error {
	if report.Pending+report.Unknown+report.Failed != 0 {
		return errors.New("image hosting: live-test image cleanup remains unresolved")
	}
	return nil
}
