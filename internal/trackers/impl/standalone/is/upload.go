// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://immortalseed.me"
	uploadURL  = baseURL + "/upload.php"
	torrentURL = baseURL + "/details.php?hash="
	sourceFlag = "https://immortalseed.me"
)

var (
	sslPattern   = regexp.MustCompile(`details\.php\?hash=([a-zA-Z0-9]+)|download\.php\?id=([a-zA-Z0-9]+)`)
	successTexts = []string{"Download Torrent (SSL)", "Thank you for uploading"}
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	nfo           *commonhttp.FileField
	blockedReason string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, cookies, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: IS %s", state.blockedReason)
	}

	files := []commonhttp.FileField{{
		FieldName: "torrentfile",
		FileName:  metautil.FirstNonEmptyTrimmed(state.releaseName, filepath.Base(state.torrentPath)),
		Path:      state.torrentPath,
	}}
	if state.nfo != nil {
		files = append(files, *state.nfo)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "IS")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, cookies, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	cookies []*http.Cookie,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: IS request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: IS upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(
		resp,
		resp.StatusCode >= 200 && resp.StatusCode < 400,
		commonhttp.DefaultResponsePreviewBytes,
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: IS read upload response: %w", err)
	}
	id, success := successfulUploadResponse(finalURL, string(responseBody))
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && success {
		if id != "" {
			tURL := torrentURL + id
			registeredPath := trackers.PersistReconstructedRegisteredTorrent(
				req.Logger, "IS", state.torrentPath, artifactPath, announceURL, tURL, sourceFlag,
			)
			return api.UploadSummary{
				Uploaded: 1,
				UploadedTorrents: []api.UploadedTorrent{{
					Tracker:     "IS",
					TorrentID:   id,
					TorrentURL:  tURL,
					TorrentPath: registeredPath,
				}},
			}, nil
		}
		return api.UploadSummary{Uploaded: 1}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "IS", "upload_failure", responsePreview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("IS", resp.StatusCode, responsePreview)
}

func successfulUploadResponse(finalURL string, responseBody string) (string, bool) {
	match := sslPattern.FindStringSubmatch(finalURL + "\n" + responseBody)
	if len(match) >= 3 {
		if id := metautil.FirstNonEmptyTrimmed(match[1], match[2]); id != "" {
			return id, true
		}
	}
	for _, text := range successTexts {
		if strings.Contains(responseBody, text) {
			return "", true
		}
	}
	return "", false
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "IS",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "is",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "torrentfile",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, []*http.Cookie, error) {
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	cookies, err := loadCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: IS release name: %w", nameErr)
	}
	fields := map[string]string{
		"UseNFOasDescr": "no",
		"message":       buildMessage(req.Meta),
		"category":      strconv.Itoa(resolveCategoryID(req.Meta)),
		"subject":       releaseName,
		"nothingtopost": "1",
		"t_image_url":   resolvePoster(req.Meta),
		"submit":        "Upload Torrent",
		"anonymous":     yesNo(req.TrackerConfig.Anon),
	}
	if strings.EqualFold(categoryOf(req.Meta), "MOVIE") {
		fields["t_link"] = resolveIMDbURL(req.Meta)
	}
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		releaseName: fields["subject"],
		fields:      fields,
	}
	if strings.TrimSpace(fields["t_image_url"]) == "" {
		state.blockedReason = "missing poster URL"
	}
	if file, ok := resolveNFO(req.Meta); ok {
		state.nfo = &file
	}
	return state, cookies, nil
}

func buildMessage(meta api.UploadSubject) string {
	message := strings.TrimSpace(resolveOverview(meta))
	if trailer := resolveYouTube(meta); trailer != "" {
		if message != "" {
			message += "\n\n"
		}
		message += "[youtube]" + trailer + "[/youtube]"
	}
	return message
}

func resolveIMDbURL(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL)
	}
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).URL()
	}
	return ""
}

func resolveYouTube(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveKeywords(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
	}
	return ""
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
