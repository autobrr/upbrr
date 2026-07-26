// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

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
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	uploadURL  = "https://www.bit-hdtv.com/takeupload.php"
	sourceFlag = "BIT-HDTV"
)

type uploadState struct {
	torrentPath  string
	releaseName  string
	screenBlock  string
	inlineDescr  string
	mediaDump    string
	fields       map[string]string
	artifactPath string
}

type uploadResponse struct {
	Status  string `json:"status"`
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		View string `json:"view"`
	} `json:"data"`
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
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
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, body, contentType)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	body []byte,
	contentType string,
) (api.UploadSummary, error) {
	response, responseBody, err := sendUpload(ctx, body, contentType)
	if err != nil {
		return api.UploadSummary{}, err
	}
	viewURL := strings.TrimSpace(response.Data.View)
	if viewURL == "" {
		artifactPath, artifactErr := writeFailureArtifact(req, responseBody, "upload_failure")
		if artifactErr != nil && req.Logger != nil {
			req.Logger.Warnf("trackers: BHDTV failure artifact write failed: %v", artifactErr)
		}
		message := metautil.FirstNonEmptyTrimmed(
			commonhttp.ExtractHTTPErrorDetail(responseBody),
			commonhttp.RedactErrorDetail(response.Message),
			commonhttp.RedactErrorDetail(response.Status),
			"upload response did not include a view URL",
		)
		if artifactPath != "" {
			message += " (" + artifactPath + ")"
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: BHDTV %s", message)
	}

	if strings.TrimSpace(req.TrackerConfig.MyAnnounceURL) != "" {
		artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "BHDTV")
		if err != nil {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "BHDTV")
		} else {
			state.artifactPath = trackers.PersistReconstructedRegisteredTorrent(
				req.Logger,
				"BHDTV",
				state.torrentPath,
				artifactPath,
				req.TrackerConfig.MyAnnounceURL,
				viewURL,
				sourceFlag,
			)
		}
	}

	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "BHDTV",
			TorrentURL:  viewURL,
			TorrentPath: state.artifactPath,
		}},
	}, nil
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "BHDTV",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "bhdtv",
		Description:      state.screenBlock,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
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
		return uploadState{}, errors.New("trackers: BHDTV missing api_key")
	}

	descriptionAssets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		descriptionAssets = trackers.DescriptionAssets{}
	}

	screenBlock := buildDescription(descriptionAssets)

	mediaDump, err := resolveMediaDump(req.Meta)
	if err != nil {
		return uploadState{}, err
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}

	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, fmt.Errorf("trackers: BHDTV release name: %w", nameErr)
	}
	fields := map[string]string{
		"api_key":    strings.TrimSpace(req.TrackerConfig.APIKey),
		"name":       releaseName,
		"mediainfo":  mediaDump,
		"cat":        resolveCategoryID(req.Meta),
		"subcat":     resolveSubcategoryID(req.Meta),
		"resolution": resolveResolutionID(req.Meta),
		"sdescr":     " ",
		"descr":      resolveInlineDescription(req.Meta),
		"screen":     screenBlock,
		"url":        resolveReferenceURL(req.Meta),
		"format":     "json",
	}

	return uploadState{
		torrentPath: torrentPath,
		releaseName: fields["name"],
		screenBlock: screenBlock,
		inlineDescr: fields["descr"],
		mediaDump:   mediaDump,
		fields:      fields,
	}, nil
}

func sendUpload(ctx context.Context, body []byte, contentType string) (uploadResponse, []byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHDTV request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHDTV upload request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return uploadResponse{}, nil, fmt.Errorf("trackers: BHDTV read response: %w", err)
	}

	var decoded uploadResponse
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			return uploadResponse{}, responseBody, fmt.Errorf("trackers: BHDTV decode response: %w", err)
		}
	}
	return decoded, responseBody, nil
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: BHDTV write multipart field %q: %w", key, err)
		}
	}

	file, err := os.Open(strings.TrimSpace(torrentPath))
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHDTV open torrent file: %w", err)
	}
	defer file.Close()

	part, err := writer.CreateFormFile("file", filepath.Base(torrentPath))
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHDTV create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: BHDTV copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: BHDTV close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveReferenceURL(meta api.UploadSubject) string {
	if categoryOf(meta) == "TV" && meta.Identity.TVmazeID != 0 {
		return fmt.Sprintf("https://www.tvmaze.com/shows/%d", meta.Identity.TVmazeID)
	}
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL)
	}
	return ""
}

func writeFailureArtifact(req trackers.PreparationInput, payload []byte, name string) (string, error) {
	if strings.TrimSpace(req.Runtime.DBPath) == "" || strings.TrimSpace(req.Meta.SourcePath) == "" {
		return "", nil
	}
	tmpRoot, err := db.Subdir(req.Runtime.DBPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, req.Meta.SourcePath, req.Meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	ext := ".txt"
	if bytes.Contains(bytes.ToLower(payload), []byte("<html")) {
		ext = ".html"
	}
	path := filepath.Join(tmpDir, "[BHDTV]"+name+ext)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("trackers: BHDTV create failure artifact dir: %w", err)
	}
	payload = []byte(redaction.RedactValue(string(payload), nil))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", fmt.Errorf("trackers: BHDTV write failure artifact: %w", err)
	}
	return path, nil
}
