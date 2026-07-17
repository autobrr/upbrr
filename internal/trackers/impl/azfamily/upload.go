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

func prepareUpload(ctx context.Context, site siteDefinition, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if req.Intent != trackers.PreparationIntentUpload {
		preview, err := buildUploadDryRun(ctx, site, req)
		if err != nil {
			return trackers.PreparedOperation{}, err
		}
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	state, err := newSession(ctx, site, req.Runtime.DBPath, req.Logger)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	media, err := lookupMediaCode(ctx, site, state, req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	if media.Missing || strings.TrimSpace(media.MediaCode) == "" {
		return trackers.PreparedOperation{}, fmt.Errorf(
			"trackers: %s media missing from tracker database; add it on-site at %s/add/%s then retry",
			site.Name,
			site.BaseURL,
			categorySlug(req.Meta),
		)
	}
	if requests, err := searchRequests(ctx, site, state, req.Meta); err == nil && len(requests) > 0 && req.Logger != nil {
		req.Logger.Infof("trackers: %s matched %d open request(s)", site.Name, len(requests))
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	fileInfo, err := resolveMediaInfoText(req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	task, err := createTask(ctx, site, state, req, media.MediaCode, fileInfo, torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	screenshots, err := uploadScreenshots(ctx, site, state, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	if len(screenshots) < 3 {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %s image host returned fewer than 3 screenshots", site.Name)
	}
	payload, err := buildFinalPayload(ctx, site, state, req, media.MediaCode, task, fileInfo, screenshots)
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
		ReleaseName:      editName(site, req.Meta),
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
		return submitPreparedUpload(submitCtx, site, state.client, task.RedirectURL, payload, trackerTorrentPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	site siteDefinition,
	client *http.Client,
	redirectURL string,
	payload url.Values,
	trackerTorrentPath string,
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
	if err := downloadTrackerTorrent(ctx, client, downloadURL, trackerTorrentPath); err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %s personalized torrent download: %w", site.Name, err)
	}
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     site.Name,
			TorrentID:   torrentID,
			DownloadURL: downloadURL,
			TorrentURL:  torrentURL,
			TorrentPath: trackerTorrentPath,
		}},
	}, nil
}

func buildUploadDryRun(ctx context.Context, site siteDefinition, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	state, err := newSession(ctx, site, req.Runtime.DBPath, req.Logger)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	media, err := lookupMediaCode(ctx, site, state, req.Meta)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	torrentPath, _ := resolveTorrentPath(req.Meta, req.Runtime.DBPath)
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
		ReleaseName:      editName(site, req.Meta),
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

func resolveMediaInfoText(meta api.UploadSubject) (string, error) {
	if path := strings.TrimSpace(meta.MediaInfoTextPath); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return string(data), nil
		}
	}
	return "", errors.New("trackers: missing MediaInfo/BDInfo text")
}

func resolveTorrentPath(meta api.UploadSubject, dbPath string) (string, error) {
	for _, candidate := range []string{strings.TrimSpace(meta.TorrentPath), strings.TrimSpace(meta.ClientTorrentPath)} {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if strings.TrimSpace(dbPath) != "" && strings.TrimSpace(meta.SourcePath) != "" {
		tmpRoot, err := db.Subdir(dbPath, "tmp")
		if err == nil {
			tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
			if err == nil {
				guessed := filepath.Join(tmpDir, base+".torrent")
				if info, err := os.Stat(guessed); err == nil && !info.IsDir() {
					return guessed, nil
				}
			}
		}
	}
	return "", errors.New("trackers: torrent file not found")
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
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trackers: personalized torrent request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("trackers: read personalized torrent response: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("trackers: create personalized torrent dir: %w", err)
	}
	if err := os.WriteFile(targetPath, body, 0o600); err != nil {
		return fmt.Errorf("trackers: write personalized torrent: %w", err)
	}
	return nil
}
