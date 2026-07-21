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
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const nblUploadURL = "https://nebulance.io/api.php"

var nblTorrentIDPattern = regexp.MustCompile(`id=(\d+)`)

type uploadState struct {
	torrentPath string
	releaseName string
	fields      map[string]string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "NBL")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, state, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	state uploadState,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, nblUploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: NBL request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	result, err := commonhttp.ExecuteUpload(httpclient.New(httpclient.DefaultTimeout), httpReq, commonhttp.UploadExecutionOptions{Tracker: "NBL"})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: NBL execute upload: %w", err)
	}
	if !result.Success {
		return api.UploadSummary{}, commonhttp.UploadHTTPError("NBL", result.StatusCode, result.Preview)
	}

	payload := map[string]any{}
	if len(result.Body) > 0 {
		if err := json.Unmarshal(result.Body, &payload); err != nil {
			return api.UploadSummary{}, errors.New("trackers: NBL json decode error, the API is probably down")
		}
	}

	torrentURL, torrentID := extractUploadLinkAndID(payload)

	if announceURL != "" {
		if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, torrentURL, "NBL"); err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
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

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "NBL",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "nbl",
		Endpoint:         nblUploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: NBL missing api_key")
	}

	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	mediaInfo, err := resolveMediaInfo(req.Meta)
	if err != nil {
		return uploadState{}, err
	}

	fields := map[string]string{
		"action":      "upload",
		"api_key":     strings.TrimSpace(req.TrackerConfig.APIKey),
		"tvmazeid":    strconv.Itoa(req.Meta.Identity.TVmazeID),
		"mediainfo":   mediaInfo,
		"category":    strconv.Itoa(resolveCategoryID(req.Meta)),
		"ignoredupes": "1",
	}

	return uploadState{
		torrentPath: torrentPath,
		releaseName: resolveUploadName(req.Meta),
		fields:      fields,
	}, nil
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

func resolveMediaInfo(meta api.UploadSubject) (string, error) {
	if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
		return "", errors.New("trackers: NBL missing mediainfo text")
	}
	payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
	if err != nil {
		return "", fmt.Errorf("trackers: NBL read mediainfo: %w", err)
	}
	return string(payload), nil
}

func resolveCategoryID(meta api.UploadSubject) int {
	if meta.TVPack {
		return 3
	}
	return 1
}

func resolveUploadName(meta api.UploadSubject) string {
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
