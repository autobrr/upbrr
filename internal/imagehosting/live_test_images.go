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

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const lostimgImagesEndpoint = "https://lostimg.cc/api/v1/images"

var errLiveImageBudget = errors.New("image hosting: live-test image upload budget exceeded")

// CleanupImageResult is a shareable outcome containing only a run-local opaque ID.
type CleanupImageResult struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// CleanupReport accounts for owned images and unresolved upload attempts without URLs.
// Unknown outcomes require provider reconciliation and are never retried automatically.
type CleanupReport struct {
	RunID   string               `json:"runId"`
	Deleted int                  `json:"deleted"`
	Pending int                  `json:"pending"`
	Unknown int                  `json:"unknown"`
	Failed  int                  `json:"failed"`
	Images  []CleanupImageResult `json:"images"`
}

// NewLiveTestServiceWithRegistry restricts remote uploads to journaled Lostimg
// requests. The caller must own the run lock and validate the private run directory.
// Normal host selection is preserved: unsupported cleanup lanes remain blocked.
// maxImages bounds all journaled upload attempts; zero disables remote uploads.
func NewLiveTestServiceWithRegistry(
	cfg config.Config, logger api.Logger, repo repository, registry *trackers.Registry, runID, journalPath string,
	maxImages int,
) (*Service, error) {
	if maxImages < 0 {
		return nil, errors.New("image hosting: live-test image upload budget must be nonnegative")
	}
	journal, err := newImageEffectJournal(runID, journalPath)
	if err != nil {
		return nil, err
	}
	service := NewServiceWithRegistry(cfg, logger, repo, registry)
	client := liveImageClient(service.client)
	service.uploaders = map[string]uploader{
		"lostimg": &lostimgUploader{
			apiKey:    cfg.ImageHosting.LostimgAPI,
			client:    client,
			journal:   journal,
			maxImages: maxImages,
		},
	}
	return service, nil
}

func liveImageClient(base *http.Client) *http.Client {
	client := httpclient.CloneWithTimeout(base, httpclient.UploadTimeout)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func (u *lostimgUploader) uploadLiveTestBatch(ctx context.Context, paths []string) ([]uploadResult, error) {
	imageEffectsMu.Lock()
	defer imageEffectsMu.Unlock()
	if strings.TrimSpace(u.apiKey) == "" || len(paths) == 0 {
		return nil, errors.New("image hosting: live-test Lostimg upload requires images and configured authentication")
	}
	state, err := u.journal.read()
	if err != nil {
		return nil, err
	}
	if state.cleanupStarted {
		return nil, errors.New("image hosting: live-test image cleanup has already started")
	}
	remaining := u.maxImages
	for _, attempt := range state.attempts {
		if len(attempt.sources) > remaining {
			return nil, errLiveImageBudget
		}
		remaining -= len(attempt.sources)
	}
	if len(paths) > remaining {
		return nil, errLiveImageBudget
	}
	hashes, err := imageSourceHashes(paths)
	if err != nil {
		return nil, err
	}
	for _, attempt := range state.attempts {
		if attempt.complete {
			continue
		}
		for _, hash := range hashes {
			if slices.Contains(attempt.sources, hash) {
				return nil, errors.New("image hosting: previous upload outcome requires reconciliation before retry")
			}
		}
	}
	var results []uploadResult
	for start := 0; start < len(paths); start += lostimgMaxBatchUploadImages {
		if err := ctx.Err(); err != nil {
			return results, fmt.Errorf("image hosting: live-test upload canceled: %w", err)
		}
		end := min(start+lostimgMaxBatchUploadImages, len(paths))
		record := u.journal.record("upload_pending", newImageEffectID())
		record.Sources = hashes[start:end]
		if err := u.journal.append(record); err != nil {
			return results, err
		}
		headers := map[string]string{"Authorization": "Bearer " + strings.TrimSpace(u.apiKey)}
		body, status, requestErr := postMultipartRepeatedFileField(ctx, liveImageClient(u.client), lostimgImagesEndpoint, "file[]", paths[start:end], headers)
		urls, valid := parseLiveImageURLs(body)
		returned := u.journal.record("uploaded", record.ID)
		returned.URLs = urls
		returned.Complete = requestErr == nil && status == http.StatusOK && valid && len(urls) == end-start
		if err := u.journal.append(returned); err != nil {
			return results, err
		}
		for _, raw := range urls {
			results = append(results, uploadResult{
				ImgURL: raw,
				RawURL: raw,
				WebURL: raw,
			})
		}
		if !returned.Complete {
			return results, errors.New("image hosting: live-test upload incomplete; retained image journal requires cleanup or reconciliation")
		}
	}
	return results, nil
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
	if strings.TrimSpace(cfg.ImageHosting.LostimgAPI) == "" {
		return report, errors.New("image hosting: live-test cleanup authentication is unavailable")
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
		if err := ctx.Err(); err != nil {
			return state.report(runID), fmt.Errorf("image hosting: live-test cleanup canceled: %w", err)
		}
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
		confirmed := deleteLiveImages(ctx, client, cfg.ImageHosting.LostimgAPI, urls)
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
		for index := range attempt.urls {
			image := s.images[fmt.Sprintf("%s_%d", attemptID, index)]
			switch image.State {
			case "deleted":
				report.Deleted++
			case "uploaded":
				report.Pending++
			default:
				image.State = "cleanup_unknown"
				image.Reason = "provider_unconfirmed"
				report.Unknown++
			}
			report.Images = append(report.Images, image)
		}
		missing := max(0, len(attempt.sources)-len(attempt.urls))
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
