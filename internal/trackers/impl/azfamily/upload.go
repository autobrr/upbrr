// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

// prepareUpload performs authenticated lookup for every intent. Upload intent
// also creates the remote task and uploads screenshots during preparation, then
// captures the finalized form, client session, redirect, and local torrent
// target for one later submission.
func prepareUpload(ctx context.Context, site siteDefinition, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if req.Intent != trackers.PreparationIntentUpload {
		preview, err := buildUploadDryRun(ctx, site, req)
		if err != nil {
			return trackers.PreparedOperation{}, err
		}
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	state, err := newSession(ctx, site, req.Runtime.DBPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	media, err := lookupMediaCode(ctx, site, state, req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	mediaMissing := media.Missing || strings.TrimSpace(media.MediaCode) == ""
	if mediaMissing {
		if req.Meta.Options.InteractionMode == api.InteractionModeUnattended {
			if req.Logger != nil {
				req.Logger.Warnf("trackers: %s media missing decision=skip mode=unattended", site.Name)
			}
			return trackers.PreparedOperation{}, trackers.NewPreparationFailure(
				site.Name,
				trackers.PreparationFailureCodeSkipped,
				"Media is missing from the tracker database. Tracker skipped in unattended mode.",
				nil,
			)
		}
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %s prepared upload torrent: %w", site.Name, err)
	}
	fileInfo, err := resolveMediaInfoText(req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	if mediaMissing {
		return prepareMissingMediaOperation(ctx, site, state, req, torrentPath, fileInfo)
	}
	preparedScreenshots, screenshotMinimum, err := prepareScreenshotUploads(ctx, site, state, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	return prepareResolvedUpload(
		ctx,
		site,
		state,
		req,
		media.MediaCode,
		torrentPath,
		fileInfo,
		preparedScreenshots,
		screenshotMinimum,
	)
}

func prepareMissingMediaOperation(
	ctx context.Context,
	site siteDefinition,
	state sessionState,
	req trackers.PreparationInput,
	torrentPath string,
	fileInfo string,
) (trackers.PreparedOperation, error) {
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %s release name: %w", site.Name, err)
	}
	preview := api.TrackerDryRunEntry{
		Tracker:          site.Name,
		Status:           "ready",
		Message:          "media database addition requires confirmation",
		ReleaseName:      releaseName,
		DescriptionGroup: "azfamily",
		Description:      buildDescriptionFromAssets(ctx, req),
		Endpoint:         site.BaseURL + "/upload/" + categorySlug(req.Meta),
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent_file",
			Path:    torrentPath,
			Present: true,
		}},
		RequiredActions: []api.RequiredAction{{
			Kind:   api.RequiredActionResolveTrackerPreparation,
			Prompt: site.Name + " media is missing from the tracker database. Add it now and continue?",
			Options: []api.RequiredActionOption{
				{Value: "confirm", Label: "Add media and continue"},
				{Value: "resolve", Label: "Skip " + site.Name},
			},
		}},
	}
	return trackers.NewPreparedOperationWithDecisionResolver(
		preview,
		func(context.Context) (api.UploadSummary, error) {
			return api.UploadSummary{}, errors.New("tracker media database decision is unresolved")
		},
		nil,
		func(resolveCtx context.Context, confirmed bool) (trackers.PreparedOperation, error) {
			if !confirmed {
				return trackers.PreparedOperation{}, trackers.NewPreparationFailure(
					site.Name,
					trackers.PreparationFailureCodeSkipped,
					"Media is missing from the tracker database. Tracker skipped.",
					nil,
				)
			}
			preparedScreenshots, screenshotMinimum, prepareErr := prepareScreenshotUploads(resolveCtx, site, state, req)
			if prepareErr != nil {
				return trackers.PreparedOperation{}, prepareErr
			}
			mediaCode, addErr := addMissingMedia(resolveCtx, site, state, req.Meta, req.Logger)
			if addErr != nil {
				return trackers.PreparedOperation{}, addErr
			}
			return prepareResolvedUpload(
				resolveCtx,
				site,
				state,
				req,
				mediaCode,
				torrentPath,
				fileInfo,
				preparedScreenshots,
				screenshotMinimum,
			)
		},
	), nil
}

func prepareResolvedUpload(
	ctx context.Context,
	site siteDefinition,
	state sessionState,
	req trackers.PreparationInput,
	mediaCode string,
	torrentPath string,
	fileInfo string,
	preparedScreenshots []preparedScreenshotUpload,
	screenshotMinimum int,
) (trackers.PreparedOperation, error) {
	if requests, err := searchRequests(ctx, site, state, req.Meta); err == nil && len(requests) > 0 && req.Logger != nil {
		req.Logger.Infof("trackers: %s matched %d open request(s)", site.Name, len(requests))
	}
	// ponytail: image-host failure can leave the required step-one task behind; add rollback when AZ-family exposes task deletion.
	task, err := createTask(ctx, site, state, req, mediaCode, fileInfo, torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	screenshots, err := uploadScreenshots(ctx, site, state, req, preparedScreenshots, screenshotMinimum)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	payload, err := buildFinalPayload(ctx, site, state, req, mediaCode, task, fileInfo, screenshots)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	trackerTorrentPath, err := resolveTrackerTorrentPath(req.Meta, req.Runtime.DBPath, site.Name)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := api.TrackerDryRunEntry{
		Tracker:          site.Name,
		Status:           "ready",
		Message:          "upload payload prepared",
		ReleaseName:      payload.Get("file_name"),
		DescriptionGroup: "azfamily",
		Description:      payload.Get("description"),
		Endpoint:         site.BaseURL + "/upload/" + categorySlug(req.Meta),
		Payload:          valuesToMap(payload),
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent_file",
			Path:    torrentPath,
			Present: true,
		}},
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, site, state.client, task.RedirectURL, payload, trackerTorrentPath, req.Logger)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	site siteDefinition,
	client *http.Client,
	redirectURL string,
	payload url.Values,
	trackerTorrentPath string,
	logger api.Logger,
) (api.UploadSummary, error) {
	resp, err := postForm(ctx, noRedirectClient(client), redirectURL, payload, map[string]string{
		"Referer":    redirectURL,
		"User-Agent": azCookieUserAgent,
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %s upload finalize: %w", site.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return api.UploadSummary{}, commonhttp.UploadHTTPError(site.Name, resp.StatusCode, body)
	}

	location := strings.TrimSpace(resp.Header.Get("Location"))
	torrentURL := absoluteURL(site.BaseURL, location)
	torrentID := extractPatternGroup(azTorrentIDPattern, torrentURL)
	if torrentID == "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: %s upload failed: missing torrent id", site.Name)
	}
	downloadURL := strings.Replace(torrentURL, "/torrent/", "/download/torrent/", 1)
	persistedPath := ""
	if err := downloadTrackerTorrent(ctx, client, downloadURL, trackerTorrentPath); err != nil {
		trackers.LogRegisteredTorrentUnavailable(logger, site.Name)
	} else {
		persistedPath = trackerTorrentPath
	}
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     site.Name,
			TorrentID:   torrentID,
			DownloadURL: downloadURL,
			TorrentURL:  torrentURL,
			TorrentPath: persistedPath,
		}},
	}, nil
}

func buildUploadDryRun(ctx context.Context, site siteDefinition, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	state, err := newSession(ctx, site, req.Runtime.DBPath)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	media, err := lookupMediaCode(ctx, site, state, req.Meta)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	torrentPath, _ := trackers.PreparedUploadTorrentPath(req.Meta)
	if media.Missing || strings.TrimSpace(media.MediaCode) == "" {
		return api.TrackerDryRunEntry{
			Tracker: site.Name,
			Status:  "blocked",
			Message: fmt.Sprintf("media missing from tracker database; add it on-site at %s/add/%s", site.BaseURL, categorySlug(req.Meta)),
			Files: []api.TrackerDryRunFile{{
				Field:   "torrent_file",
				Path:    torrentPath,
				Present: strings.TrimSpace(torrentPath) != "",
			}},
		}, nil
	}
	fileInfo, err := resolveMediaInfoText(req.Meta)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	payload, err := buildFinalPayload(ctx, site, state, req, media.MediaCode, taskInfo{
		TaskID:      "dry-run-task",
		InfoHash:    "dry-run-info-hash",
		RedirectURL: site.BaseURL + "/upload/" + categorySlug(req.Meta) + "/dry-run",
	}, fileInfo, []string{"dry-run-image-1", "dry-run-image-2", "dry-run-image-3"})
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	return api.TrackerDryRunEntry{
		Tracker:          site.Name,
		Status:           "ready",
		Message:          "dry-run payload generated",
		ReleaseName:      payload.Get("file_name"),
		DescriptionGroup: "azfamily",
		Description:      payload.Get("description"),
		Endpoint:         site.BaseURL + "/upload/" + categorySlug(req.Meta),
		Payload:          valuesToMap(payload),
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent_file",
			Path:    torrentPath,
			Present: strings.TrimSpace(torrentPath) != "",
		}},
	}, nil
}

func createTask(
	ctx context.Context,
	site siteDefinition,
	state sessionState,
	req trackers.PreparationInput,
	mediaCode, fileInfo, torrentPath string,
) (taskInfo, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range map[string]string{
		"_token":     state.token,
		"type_id":    categoryID(req.Meta),
		"movie_id":   mediaCode,
		"media_info": fileInfo,
	} {
		if err := writer.WriteField(key, value); err != nil {
			return taskInfo{}, fmt.Errorf("trackers: %s write multipart field %q: %w", site.Name, key, err)
		}
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		return taskInfo{}, fmt.Errorf("trackers: %s open torrent file: %w", site.Name, err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile("torrent_file", filepath.Base(torrentPath))
	if err != nil {
		return taskInfo{}, fmt.Errorf("trackers: %s create torrent form file: %w", site.Name, err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return taskInfo{}, fmt.Errorf("trackers: %s copy torrent file: %w", site.Name, err)
	}
	if err := writer.Close(); err != nil {
		return taskInfo{}, fmt.Errorf("trackers: %s close multipart writer: %w", site.Name, err)
	}

	endpoint := site.BaseURL + "/upload/" + categorySlug(req.Meta)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return taskInfo{}, fmt.Errorf("trackers: %s task creation request build: %w", site.Name, err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("Referer", endpoint)
	httpReq.Header.Set("User-Agent", azCookieUserAgent)
	resp, err := noRedirectClient(state.client).Do(httpReq)
	if err != nil {
		return taskInfo{}, fmt.Errorf("trackers: %s task creation: %w", site.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return taskInfo{}, fmt.Errorf("trackers: %s task creation failed: %w", site.Name, commonhttp.UploadHTTPError(site.Name, resp.StatusCode, body))
	}
	location := strings.TrimSpace(resp.Header.Get("Location"))
	taskID := extractPatternGroup(azTaskIDPattern, absoluteURL(site.BaseURL, location))
	if taskID == "" {
		return taskInfo{}, fmt.Errorf("trackers: %s task creation missing task id", site.Name)
	}
	return taskInfo{
		TaskID:      taskID,
		InfoHash:    strings.TrimSpace(req.Meta.InfoHash),
		RedirectURL: absoluteURL(site.BaseURL, location),
	}, nil
}

func resolveTrackerTorrentPath(meta api.UploadSubject, dbPath string, tracker string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, fmt.Sprintf("[%s] %s.torrent", tracker, base)), nil
}

func postForm(ctx context.Context, client *http.Client, endpoint string, data url.Values, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: form post request build: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackers: form post request: %w", err)
	}
	return resp, nil
}

func noRedirectClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &http.Client{
		Transport:     base.Transport,
		Jar:           base.Jar,
		Timeout:       base.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func downloadTrackerTorrent(ctx context.Context, client *http.Client, downloadURL, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("trackers: personalized torrent request build: %w", err)
	}
	req.Header.Set("User-Agent", azCookieUserAgent)
	if err := trackers.DownloadRegisteredTorrent(ctx, client, req, targetPath); err != nil {
		return fmt.Errorf("trackers: personalized registered torrent: %w", err)
	}
	return nil
}
