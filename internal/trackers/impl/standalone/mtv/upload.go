// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	mtvBaseURL      = "https://www.morethantv.me"
	mtvUploadPath   = "/upload.php"
	mtvIndexPath    = "/index.php"
	mtvUserAgentWeb = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
)

func uploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (api.UploadSummary, error) {
	req.Intent = trackers.PreparationIntentUpload
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if nameFailure != nil {
		return api.UploadSummary{}, nameFailure
	}
	plan, failure := trackers.PrepareAdapter(ctx, req, nil, func(ctx context.Context, input trackers.PreparationInput) (trackers.PreparedOperation, error) {
		return prepareUploadAt(ctx, input, baseURL)
	})
	if failure != nil {
		return api.UploadSummary{}, failure
	}
	summary, err := plan.Submit(ctx)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: MTV submit prepared upload: %w", err)
	}
	return summary, nil
}

func prepareUploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	select {
	case <-ctx.Done():
		return trackers.PreparedOperation{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	uploadURL := baseURL + mtvUploadPath

	assets := trackers.DescriptionAssets{}
	var err error
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.PreparedDescriptionAssets(req.Assets)
		if err != nil {
			trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	descText := strings.TrimSpace(assets.Description)
	if !assets.Final {
		descText, err = BuildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets.Description, assets.Screenshots)
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: MTV prepared upload torrent: %w", err)
	}

	previewFields, err := buildUploadFields(req, "[redacted]", descText)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "MTV",
		ReadyMessage:     "dry-run payload generated (auth placeholder used)",
		ReleaseName:      previewFields["title"],
		DescriptionGroup: "mtv",
		Description:      descText,
		Endpoint:         uploadURL,
		Payload:          previewFields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    torrentPath,
			Present: strings.TrimSpace(torrentPath) != "",
		}},
	})
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "MTV")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}

	cookies, err := loadMTVCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		cookies = nil
	}
	auth, client, err := resolveAuthKey(ctx, baseURL, cookies)
	if err != nil {
		if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
			if cookies == nil {
				return trackers.PreparedOperation{}, errors.New("trackers: MTV cookie invalid/missing and username/password not configured")
			}
			return trackers.PreparedOperation{}, err
		}
		var effectiveBaseURL string
		auth, client, cookies, effectiveBaseURL, err = loginAndResolveAuthKey(ctx, req.TrackerConfig, baseURL, api.TrackerAuthLoginRequest{})
		if err != nil {
			return trackers.PreparedOperation{}, err
		}
		if strings.TrimSpace(effectiveBaseURL) != "" && !strings.EqualFold(effectiveBaseURL, baseURL) {
			baseURL = effectiveBaseURL
			uploadURL = baseURL + mtvUploadPath
		}
		if persistErr := saveMTVCookies(ctx, req.Runtime.DBPath, cookies); persistErr != nil && req.Logger != nil {
			req.Logger.Warnf("trackers: MTV failed to persist cookies: %v", persistErr)
		}
	}

	fields, err := buildUploadFields(req, auth, descText)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	body, contentType, err := buildMultipartPayload(fields, torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(
			submitCtx,
			uploadURL,
			client,
			body,
			contentType,
			req.Logger,
			torrentPath,
			artifactPath,
			announceURL,
		)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	uploadURL string,
	client *http.Client,
	body []byte,
	contentType string,
	logger api.Logger,
	torrentPath string,
	artifactPath string,
	announceURL string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: MTV request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", mtvUserAgentWeb)

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: MTV upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusBadRequest {
		preview := string(bodyPreview)
		if strings.Contains(preview, "Request Header") || strings.Contains(preview, "Cookie Too Large") || strings.Contains(preview, "Header Too Large") {
			return api.UploadSummary{}, errors.New("trackers: MTV data error (request header/cookie too large)")
		}
	}

	if strings.Contains(finalURL, "torrents.php") {
		torrentID := ""
		if resp.Request != nil && resp.Request.URL != nil {
			torrentID = strings.TrimSpace(resp.Request.URL.Query().Get("id"))
		}
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			logger,
			"MTV",
			torrentPath,
			artifactPath,
			announceURL,
			finalURL,
			"MTV",
		)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "MTV",
				TorrentID:   torrentID,
				TorrentURL:  finalURL,
				TorrentPath: registeredPath,
			}},
		}, nil
	}

	return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("MTV", resp.StatusCode, finalURL, bodyPreview)
}
