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
	"net/textproto"
	"net/url"
	"os"
	"path" //nolint:depguard // Extracts response URL path basename, not local filesystem basename.
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	azMaxScreenshotUploads  = 15
	azScreenshotSourceMax   = 20 << 20
	azScreenshotBatchMax    = 100 << 20
	azScreenshotResponseMax = 64 << 10
	azImageUserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:141.0) Gecko/20100101 Firefox/141.0"
)

type preparedScreenshotUpload struct {
	data     []byte
	filename string
}

func buildFinalPayload(
	ctx context.Context,
	site siteDefinition,
	state sessionState,
	req trackers.PreparationInput,
	mediaCode string,
	task taskInfo,
	fileInfo string,
	screenshotIDs []string,
) (url.Values, error) {
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return nil, fmt.Errorf("trackers: %s release name: %w", site.Name, err)
	}
	langs := languageValues(req.Meta)
	tags, err := resolveTags(ctx, site, state, req)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("_token", state.token)
	values.Set("torrent_id", "")
	values.Set("type_id", categoryID(req.Meta))
	values.Set("file_name", releaseName)
	values.Set("description", buildDescriptionFromAssets(ctx, req))
	values.Set("qqfile", "")
	values.Set("rip_type_id", ripTypeID(req.Meta))
	values.Set("video_quality_id", videoQualityID(site, req.Meta))
	values.Set("video_resolution", resolutionValue(req.Meta))
	values.Set("movie_id", mediaCode)
	values.Set("media_info", fileInfo)
	values.Set("info_hash", task.InfoHash)
	values.Set("task_id", task.TaskID)
	if anonEnabled(req) {
		values.Set("anon_upload", "1")
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
		}
		if req.Meta.EpisodeInt > 0 {
			values.Set("tv_episode", strconv.Itoa(req.Meta.EpisodeInt))
		}
	}
	return values, nil
}

func resolveTags(ctx context.Context, site siteDefinition, state sessionState, req trackers.PreparationInput) ([]string, error) {
	seen := make(map[string]struct{})
	add := func(value string) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			seen[trimmed] = struct{}{}
		}
	}
	if req.Meta.PersonalRelease {
		add(site.PersonalReleaseTag)
	}
	if req.Runtime.Internal {
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

func prepareScreenshotUploads(
	ctx context.Context,
	site siteDefinition,
	state sessionState,
	req trackers.PreparationInput,
) ([]preparedScreenshotUpload, int, error) {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		return nil, 0, fmt.Errorf("trackers: %w", err)
	}
	minimum := azScreenshotMinimum(site, api.NewTrackerValidationSubject(req.Meta, req.Tracker))
	limit := minimum
	menuLimit := 0
	if req.Meta.TVPack || len(assets.MenuImages) > 0 {
		limit = azMaxScreenshotUploads
		menuLimit = limit - minimum
	}
	prepared := make([]preparedScreenshotUpload, 0, min(limit, len(assets.MenuImages)+len(assets.Screenshots)))
	failures := 0
	preparedBytes := 0
	appendImage := func(shot api.ScreenshotImage, menu bool) bool {
		imageBytes, filename, imageErr := screenshotBytes(ctx, state.client, shot)
		if imageErr != nil || len(imageBytes) == 0 || menu && http.DetectContentType(imageBytes) != "image/png" {
			failures++
			return false
		}
		if !fitsScreenshotBatch(preparedBytes, len(imageBytes)) {
			failures++
			return false
		}
		preparedBytes += len(imageBytes)
		prepared = append(prepared, preparedScreenshotUpload{data: imageBytes, filename: filename})
		return true
	}
	preparedMenus := 0
	for _, image := range assets.MenuImages {
		if preparedMenus >= menuLimit {
			break
		}
		if appendImage(image, true) {
			preparedMenus++
		}
	}
	for _, image := range assets.Screenshots {
		if len(prepared) >= limit {
			break
		}
		appendImage(image, false)
	}
	if len(prepared) < minimum {
		return nil, minimum, fmt.Errorf(
			"trackers: %s only %d of %d required screenshot sources could be read",
			site.Name,
			len(prepared),
			minimum,
		)
	}
	if failures > 0 && req.Logger != nil {
		req.Logger.Warnf(
			"trackers: %s screenshot source preparation partial prepared=%d failed=%d decision=continue",
			site.Name,
			len(prepared),
			failures,
		)
	}
	return prepared, minimum, nil
}

func uploadScreenshots(
	ctx context.Context,
	site siteDefinition,
	state sessionState,
	req trackers.PreparationInput,
	prepared []preparedScreenshotUpload,
	minimum int,
) ([]string, error) {
	results := make([]string, 0, len(prepared))
	failures := 0
	var firstErr error
	for _, image := range prepared {
		id, err := uploadScreenshot(ctx, site, state, image.data, image.filename)
		if err != nil {
			failures++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		results = append(results, id)
	}
	if len(results) < minimum {
		if firstErr != nil {
			return nil, fmt.Errorf(
				"trackers: %s image host returned %d of %d required screenshots: %w",
				site.Name,
				len(results),
				minimum,
				firstErr,
			)
		}
		return nil, fmt.Errorf("trackers: %s image host returned %d of %d required screenshots", site.Name, len(results), minimum)
	}
	if failures > 0 && req.Logger != nil {
		req.Logger.Warnf(
			"trackers: %s screenshot upload partial published=%d failed=%d minimum=%d decision=continue",
			site.Name,
			len(results),
			failures,
			minimum,
		)
	}
	return results, nil
}

func uploadScreenshot(ctx context.Context, site siteDefinition, state sessionState, imageBytes []byte, filename string) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "_token", value: state.token},
		{name: "qquuid", value: strconv.FormatInt(time.Now().UnixNano(), 10)},
		{name: "qqfilename", value: filename},
		{name: "qqtotalfilesize", value: strconv.Itoa(len(imageBytes))},
	} {
		if err := writer.WriteField(field.name, field.value); err != nil {
			return "", fmt.Errorf("trackers: %s write screenshot field %s: %w", site.Name, field.name, err)
		}
	}
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", multipart.FileContentDisposition("qqfile", filename))
	partHeader.Set("Content-Type", http.DetectContentType(imageBytes))
	part, err := writer.CreatePart(partHeader)
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
	req.Header.Set("User-Agent", azImageUserAgent)
	resp, err := state.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("trackers: %s screenshot upload request: %w", site.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, azScreenshotResponseMax+1))
	if err != nil {
		return "", fmt.Errorf("trackers: %s read screenshot upload response: %w", site.Name, err)
	}
	if len(responseBody) > azScreenshotResponseMax {
		return "", fmt.Errorf("trackers: %s screenshot upload response exceeded %d bytes", site.Name, azScreenshotResponseMax)
	}
	var payload struct {
		Success bool `json:"success"`
		ImageID any  `json:"imageId"`
		Error   any  `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return "", fmt.Errorf("trackers: %s decode screenshot upload response: %w", site.Name, err)
	}
	if !payload.Success {
		message := "tracker image host rejected screenshot"
		if payload.Error != nil {
			if detail := strings.TrimSpace(redaction.RedactValue(stringValue(payload.Error), nil)); detail != "" {
				message += ": " + detail
			}
		}
		return "", errors.New(message)
	}
	imageID := stringValue(payload.ImageID)
	if imageID == "" {
		return "", errors.New("tracker image host returned no image id")
	}
	return imageID, nil
}

func screenshotBytes(ctx context.Context, client *http.Client, shot api.ScreenshotImage) ([]byte, string, error) {
	var pathErr error
	if path := strings.TrimSpace(shot.Path); path != "" {
		data, err := readScreenshotFile(path)
		if err == nil {
			return data, filepath.Base(path), nil
		}
		pathErr = err
	}
	raw := strings.TrimSpace(shot.RawURL)
	if raw == "" {
		raw = strings.TrimSpace(shot.ImgURL)
	}
	if raw == "" {
		if pathErr != nil {
			return nil, "", pathErr
		}
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
	if resp.ContentLength > azScreenshotSourceMax {
		return nil, "", fmt.Errorf("trackers: screenshot source exceeded %d bytes", azScreenshotSourceMax)
	}
	data, err := readScreenshotSource(resp.Body, azScreenshotSourceMax)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: read screenshot response body: %w", err)
	}
	filename := path.Base(strings.TrimSpace(resp.Request.URL.Path))
	if filename == "" || filename == "." || filename == "/" {
		filename = "screenshot.png"
	}
	return data, filename, nil
}

func readScreenshotFile(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("trackers: open screenshot source: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("trackers: inspect screenshot source: %w", err)
	}
	if info.Size() > azScreenshotSourceMax {
		return nil, fmt.Errorf("trackers: screenshot source exceeded %d bytes", azScreenshotSourceMax)
	}
	data, err := readScreenshotSource(file, azScreenshotSourceMax)
	if err != nil {
		return nil, fmt.Errorf("trackers: read screenshot source: %w", err)
	}
	return data, nil
}

// readScreenshotSource rejects content larger than limit without retaining the
// complete oversized source.
func readScreenshotSource(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read source: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("source exceeded %d bytes", limit)
	}
	return data, nil
}

func fitsScreenshotBatch(current, next int) bool {
	return current <= azScreenshotBatchMax && next <= azScreenshotBatchMax-current
}

func keywordsFor(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
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
