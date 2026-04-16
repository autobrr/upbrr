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

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func upload(ctx context.Context, site siteDefinition, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := newSession(ctx, site, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return api.UploadSummary{}, err
	}
	media, err := lookupMediaCode(ctx, site, state, req.Meta)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if media.Missing || strings.TrimSpace(media.MediaCode) == "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: %s media missing from tracker database; add it on-site at %s/add/%s then retry", site.Name, site.BaseURL, categorySlug(req.Meta))
	}
	if requests, err := searchRequests(ctx, site, state, req.Meta); err == nil && len(requests) > 0 && req.Logger != nil {
		req.Logger.Infof("trackers: %s matched %d open request(s)", site.Name, len(requests))
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return api.UploadSummary{}, err
	}
	fileInfo, err := resolveMediaInfoText(req.Meta)
	if err != nil {
		return api.UploadSummary{}, err
	}
	task, err := createTask(ctx, site, state, req, media.MediaCode, fileInfo, torrentPath)
	if err != nil {
		return api.UploadSummary{}, err
	}
	screenshots, err := uploadScreenshots(ctx, site, state, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if len(screenshots) < 3 {
		return api.UploadSummary{}, fmt.Errorf("trackers: %s image host returned fewer than 3 screenshots", site.Name)
	}
	payload, err := buildFinalPayload(ctx, site, state, req, media.MediaCode, task, fileInfo, screenshots)
	if err != nil {
		return api.UploadSummary{}, err
	}
	resp, err := postForm(ctx, noRedirectClient(state.client), task.RedirectURL, payload, map[string]string{
		"Referer":    task.RedirectURL,
		"User-Agent": azCookieUserAgent,
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %s upload finalize: %w", site.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return api.UploadSummary{}, fmt.Errorf("trackers: %s upload failed status=%d body=%s", site.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	location := strings.TrimSpace(resp.Header.Get("Location"))
	torrentURL := absoluteURL(site.BaseURL, location)
	torrentID := extractPatternGroup(azTorrentIDPattern, torrentURL)
	if torrentID == "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: %s upload failed: missing torrent id", site.Name)
	}
	downloadURL := strings.Replace(torrentURL, "/torrent/", "/download/torrent/", 1)
	trackerTorrentPath, err := resolveTrackerTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath, site.Name)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if err := downloadTrackerTorrent(ctx, state.client, downloadURL, trackerTorrentPath); err != nil {
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

func buildUploadDryRun(ctx context.Context, site siteDefinition, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := newSession(ctx, site, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	media, err := lookupMediaCode(ctx, site, state, req.Meta)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	torrentPath, _ := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
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

func createTask(ctx context.Context, site siteDefinition, state sessionState, req trackers.UploadRequest, mediaCode, fileInfo, torrentPath string) (taskInfo, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range map[string]string{
		"_token":     state.token,
		"type_id":    categoryID(req.Meta),
		"movie_id":   mediaCode,
		"media_info": fileInfo,
	} {
		if err := writer.WriteField(key, value); err != nil {
			return taskInfo{}, err
		}
	}
	torrentBytes, infoHash, err := prepareTorrent(torrentPath, site.DefaultAnnounceURL, site.SourceFlag)
	if err != nil {
		return taskInfo{}, fmt.Errorf("trackers: %s prepare torrent: %w", site.Name, err)
	}
	part, err := writer.CreateFormFile("torrent_file", filepath.Base(torrentPath))
	if err != nil {
		return taskInfo{}, err
	}
	if _, err := io.Copy(part, bytes.NewReader(torrentBytes)); err != nil {
		return taskInfo{}, err
	}
	if err := writer.Close(); err != nil {
		return taskInfo{}, err
	}

	endpoint := site.BaseURL + "/upload/" + categorySlug(req.Meta)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return taskInfo{}, err
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
		return taskInfo{}, fmt.Errorf("trackers: %s task creation failed status=%d body=%s", site.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	location := strings.TrimSpace(resp.Header.Get("Location"))
	taskID := extractPatternGroup(azTaskIDPattern, absoluteURL(site.BaseURL, location))
	if taskID == "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return taskInfo{}, fmt.Errorf("trackers: %s task creation missing task id status=%d body=%s", site.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return taskInfo{
		TaskID:      taskID,
		InfoHash:    infoHash,
		RedirectURL: absoluteURL(site.BaseURL, location),
	}, nil
}

func resolveMediaInfoText(meta api.PreparedMetadata) (string, error) {
	if path := strings.TrimSpace(meta.MediaInfoTextPath); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return string(data), nil
		}
	}
	return "", errors.New("trackers: missing MediaInfo/BDInfo text")
}

func resolveTorrentPath(meta api.PreparedMetadata, dbPath string) (string, error) {
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
			tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
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

func resolveTrackerTorrentPath(meta api.PreparedMetadata, dbPath string, tracker string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", err
	}
	tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", err
	}
	return filepath.Join(tmpDir, fmt.Sprintf("[%s] %s.torrent", tracker, base)), nil
}

func postForm(ctx context.Context, client *http.Client, endpoint string, data url.Values, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return client.Do(req)
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
		return err
	}
	req.Header.Set("User-Agent", azCookieUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(targetPath, body, 0o600)
}

func prepareTorrent(torrentPath string, announceURL string, source string) ([]byte, string, error) {
	mi, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		return nil, "", err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, "", err
	}
	if source != "" {
		info.Source = source
		infoBytes, err := bencode.Marshal(info)
		if err != nil {
			return nil, "", err
		}
		mi.InfoBytes = infoBytes
	}
	if announceURL != "" {
		mi.Announce = announceURL
		mi.AnnounceList = metainfo.AnnounceList{{announceURL}}
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString(), nil
}
