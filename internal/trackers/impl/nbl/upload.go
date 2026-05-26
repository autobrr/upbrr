// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const nblUploadURL = "https://nebulance.io/api.php"

var nblTorrentIDPattern = regexp.MustCompile(`id=(\d+)`)

type uploadState struct {
	torrentPath string
	fields      map[string]string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}

	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath)
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, nblUploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: NBL request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: NBL upload request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: NBL read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return api.UploadSummary{}, fmt.Errorf("trackers: NBL upload failed status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	payload := map[string]any{}
	if len(bodyBytes) > 0 {
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			return api.UploadSummary{}, errors.New("trackers: NBL json decode error, the API is probably down")
		}
	}

	torrentURL, torrentID := extractUploadLinkAndID(payload)

	artifactPath := ""
	if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "NBL")
		if err != nil {
			return api.UploadSummary{}, err
		}
		if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, torrentURL, "NBL"); err != nil {
			return api.UploadSummary{}, err
		}
	}

	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "NBL",
			TorrentID:   torrentID,
			DownloadURL: torrentURL,
			TorrentURL:  torrentURL,
			TorrentPath: artifactPath,
		}},
	}, nil
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	return api.TrackerDryRunEntry{
		Tracker:          "NBL",
		Status:           "ready",
		Message:          "dry-run payload generated",
		ReleaseName:      resolveUploadName(req.Meta),
		DescriptionGroup: "nbl",
		Endpoint:         nblUploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, ctx.Err()
	default:
	}
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: NBL missing api_key")
	}

	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, err
	}
	mediaInfo, err := resolveMediaInfo(req.Meta)
	if err != nil {
		return uploadState{}, err
	}

	fields := map[string]string{
		"action":      "upload",
		"api_key":     strings.TrimSpace(req.TrackerConfig.APIKey),
		"tvmazeid":    strconv.Itoa(req.Meta.ExternalIDs.TVmazeID),
		"mediainfo":   mediaInfo,
		"category":    strconv.Itoa(resolveCategoryID(req.Meta)),
		"ignoredupes": "1",
	}

	return uploadState{torrentPath: torrentPath, fields: fields}, nil
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: NBL write multipart field %q: %w", key, err)
		}
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: NBL open torrent file: %w", err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file_input", "torrent.torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: NBL create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: NBL copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: NBL close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveMediaInfo(meta api.PreparedMetadata) (string, error) {
	if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
		return "", errors.New("trackers: NBL missing mediainfo text")
	}
	payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
	if err != nil {
		return "", fmt.Errorf("trackers: NBL read mediainfo: %w", err)
	}
	return string(payload), nil
}

func resolveCategoryID(meta api.PreparedMetadata) int {
	if meta.TVPack {
		return 3
	}
	return 1
}

func resolveUploadName(meta api.PreparedMetadata) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	if name := strings.TrimSpace(meta.ReleaseNameNoTag); name != "" {
		return name
	}
	if name := strings.TrimSpace(meta.Filename); name != "" {
		return name
	}
	return pathutil.Base(meta.SourcePath)
}

func nblString(value any) string {
	if value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func extractUploadLinkAndID(payload map[string]any) (string, string) {
	torrentURL := strings.TrimSpace(nblString(payload["link"]))
	if torrentURL == "" {
		if result, ok := payload["result"].(map[string]any); ok {
			torrentURL = strings.TrimSpace(nblString(result["link"]))
		}
	}

	torrentID := ""
	if matches := nblTorrentIDPattern.FindStringSubmatch(torrentURL); len(matches) > 1 {
		torrentID = strings.TrimSpace(matches[1])
	}

	return torrentURL, torrentID
}
