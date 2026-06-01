// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path" //nolint:depguard // Extracts response URL path basename, not local filesystem basename.
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func buildFinalPayload(ctx context.Context, site siteDefinition, state sessionState, req trackers.UploadRequest, mediaCode string, task taskInfo, fileInfo string, screenshotIDs []string) (url.Values, error) {
	langs := languageValues(site.Name, req.Meta)
	tags, err := resolveTags(ctx, site, state, req)
	if err != nil {
		return nil, err
	}

	if reason := validateMetadata(site, req.Meta); reason != "" {
		return nil, errors.New(reason)
	}

	categoryID := categoryID(req.Meta)
	fileName := editName(site, req.Meta)
	ripTypeID := ripTypeID(site, req.Meta)
	videoQualityID := videoQualityID(site, req.Meta)
	videoResolution := resolutionValue(req.Meta)

	values := url.Values{}
	values.Set("_token", state.token)
	values.Set("torrent_id", "")
	values.Set("type_id", categoryID)
	values.Set("file_name", fileName)
	values.Set("description", buildDescriptionFromAssets(ctx, req))
	values.Set("qqfile", "")
	values.Set("rip_type_id", ripTypeID)
	values.Set("video_quality_id", videoQualityID)
	values.Set("video_resolution", videoResolution)
	values.Set("movie_id", mediaCode)
	values.Set("media_info", fileInfo)
	values.Set("info_hash", task.InfoHash)
	values.Set("task_id", task.TaskID)
	if anonEnabled(req) {
		values.Set("anon_upload", "1")
	} else {
		values.Set("anon_upload", "")
	}
	for _, value := range langs.Audio {
		values.Add("languages[]", value)
	}
	for _, value := range langs.Subtitles {
		values.Add("subtitles[]", value)
	}
	for _, value := range tags {
		values.Add("tags[]", value)
	}
	for _, value := range screenshotIDs {
		values.Add("screenshots[]", value)
	}
	if isTV(req.Meta) {
		if req.Meta.TVPack {
			values.Set("tv_collection", "2")
		} else {
			values.Set("tv_collection", "1")
		}
		if req.Meta.SeasonInt > 0 {
			values.Set("tv_season", strconv.Itoa(req.Meta.SeasonInt))
		} else {
			values.Set("tv_season", "")
		}
		if req.Meta.EpisodeInt > 0 {
			values.Set("tv_episode", strconv.Itoa(req.Meta.EpisodeInt))
		} else {
			values.Set("tv_episode", "")
		}
	}
	return values, nil
}

func resolveTags(ctx context.Context, site siteDefinition, state sessionState, req trackers.UploadRequest) ([]string, error) {
	seen := make(map[string]struct{})
	add := func(value string) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			seen[trimmed] = struct{}{}
		}
	}
	if req.Meta.PersonalRelease {
		add(site.PersonalReleaseTag)
	}
	if trackers.IsInternalGroup(req.AppConfig, site.Name, req.Meta) {
		add(site.InternalTagID)
	}
	for _, keyword := range splitKeywords(keywordsFor(req.Meta)) {
		tagID, err := fetchTagID(ctx, site, state, keyword)
		if err != nil {
			return nil, err
		}
		add(tagID)
	}
	return sortedKeys(seen), nil
}

func fetchTagID(ctx context.Context, site siteDefinition, state sessionState, word string) (string, error) {
	if strings.TrimSpace(word) == "" {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, site.BaseURL+"/ajax/tags?term="+url.QueryEscape(word), nil)
	if err != nil {
		return "", fmt.Errorf("trackers: %s tag lookup request build: %w", site.Name, err)
	}
	req.Header.Set("Referer", site.BaseURL+"/upload")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", azCookieUserAgent)
	resp, err := state.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("trackers: %s tag lookup request: %w", site.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("trackers: %s decode tag lookup response: %w", site.Name, err)
	}
	for _, item := range payload.Data {
		if strings.EqualFold(stringValue(item["tag"]), word) {
			return stringValue(item["id"]), nil
		}
	}
	return "", nil
}

func uploadScreenshots(ctx context.Context, site siteDefinition, state sessionState, req trackers.UploadRequest) ([]string, error) {
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	limit := 3
	if req.Meta.TVPack {
		limit = 15
	}
	results := make([]string, 0, limit)
	for _, shot := range assets.Screenshots {
		if len(results) >= limit {
			break
		}
		imageBytes, filename, err := screenshotBytes(ctx, state.client, shot)
		if err != nil {
			if req.Logger != nil {
				req.Logger.Warnf("trackers: %s failed to get screenshot bytes: %v", site.Name, err)
			}
			continue
		}
		id, err := uploadScreenshot(ctx, site, state, imageBytes, filename)
		if err != nil {
			if req.Logger != nil {
				req.Logger.Warnf("trackers: %s failed to upload screenshot: %v", site.Name, err)
			}
			continue
		}
		if id != "" {
			results = append(results, id)
		}
	}
	return results, nil
}

func uploadScreenshot(ctx context.Context, site siteDefinition, state sessionState, imageBytes []byte, filename string) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("_token", state.token)
	_ = writer.WriteField("qquuid", strconv.FormatInt(time.Now().UnixNano(), 10))
	_ = writer.WriteField("qqfilename", filename)
	_ = writer.WriteField("qqtotalfilesize", strconv.Itoa(len(imageBytes)))
	part, err := writer.CreateFormFile("qqfile", filename)
	if err != nil {
		return "", fmt.Errorf("trackers: %s create screenshot form file: %w", site.Name, err)
	}
	if _, err := part.Write(imageBytes); err != nil {
		return "", fmt.Errorf("trackers: %s write screenshot upload part: %w", site.Name, err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("trackers: %s close screenshot multipart writer: %w", site.Name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, site.BaseURL+"/ajax/image/upload", body)
	if err != nil {
		return "", fmt.Errorf("trackers: %s screenshot upload request build: %w", site.Name, err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Referer", site.BaseURL)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", site.BaseURL)
	req.Header.Set("User-Agent", azCookieUserAgent)
	resp, err := state.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("trackers: %s screenshot upload request: %w", site.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var payload struct {
		Success bool `json:"success"`
		ImageID any  `json:"imageId"`
		Error   any  `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("trackers: %s decode screenshot upload response: %w", site.Name, err)
	}
	if !payload.Success {
		return "", fmt.Errorf("%s", stringValue(payload.Error))
	}
	return stringValue(payload.ImageID), nil
}

func screenshotBytes(ctx context.Context, client *http.Client, shot api.ScreenshotImage) ([]byte, string, error) {
	if path := strings.TrimSpace(shot.Path); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return data, filepath.Base(path), nil
		}
	}
	raw := strings.TrimSpace(shot.RawURL)
	if raw == "" {
		raw = strings.TrimSpace(shot.ImgURL)
	}
	if raw == "" {
		return nil, "", errors.New("no screenshot source")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: screenshot download request build: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: screenshot download request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: read screenshot response body: %w", err)
	}
	filename := path.Base(strings.TrimSpace(resp.Request.URL.Path))
	if filename == "" || filename == "." || filename == "/" {
		filename = "screenshot.png"
	}
	return data, filename, nil
}

func keywordsFor(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
	}
	return ""
}

func splitKeywords(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		trimmed := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(part)), " "))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func validateMetadata(site siteDefinition, meta api.PreparedMetadata) string {
	if categoryID(meta) == "" {
		return "failed to determine category"
	}
	if len(languageValues(site.Name, meta).Audio) == 0 {
		return "failed to determine audio language (site requires at least one audio language)"
	}
	if editName(site, meta) == "" {
		return "failed to determine file name (e.g., 'Movie Title 2025 1080p BluRay REMUX-GROUP')"
	}
	if rtID := ripTypeID(site, meta); rtID == "" || rtID == "0" {
		return "failed to determine rip type (e.g., BluRay, WEB-DL)"
	}
	if vqID := videoQualityID(site, meta); vqID == "" || vqID == "0" {
		return "failed to determine video quality (e.g., 1080p, 2160p)"
	}
	if resolutionValue(meta) == "" {
		return "failed to determine video resolution (e.g., 1920x1080)"
	}
	return ""
}
