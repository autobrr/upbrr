// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	hdbBaseURL    = "https://hdbits.org"
	hdbUploadPath = "/upload/upload"
)

var hdbSuccessURLPattern = regexp.MustCompile(`(?i)details\.php\?id=(\d+)&uploaded=\d+`)

type preparedUploadState struct {
	torrentPath string
	description string
	fields      map[string]string
	uploadURL   string
	cookies     []*http.Cookie
	body        []byte
	contentType string
	passkey     string
	client      *http.Client
}

func uploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string, httpClient *http.Client) (api.UploadSummary, error) {
	req.Intent = trackers.PreparationIntentUpload
	var nameFailure *trackers.PreparationFailure
	req, nameFailure = trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if nameFailure != nil {
		return api.UploadSummary{}, nameFailure
	}
	plan, failure := trackers.PrepareAdapter(ctx, req, nil, func(ctx context.Context, input trackers.PreparationInput) (trackers.PreparedOperation, error) {
		return prepareUploadAt(ctx, input, baseURL, httpClient)
	})
	if failure != nil {
		return api.UploadSummary{}, failure
	}
	summary, err := plan.Submit(ctx)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDB submit prepared upload: %w", err)
	}
	return summary, nil
}

func prepareUploadAt(
	ctx context.Context,
	req trackers.PreparationInput,
	baseURL string,
	httpClient *http.Client,
) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	select {
	case <-ctx.Done():
		return trackers.PreparedOperation{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	username := strings.TrimSpace(req.TrackerConfig.Username)
	passkey := strings.TrimSpace(req.TrackerConfig.Passkey)
	if username == "" || passkey == "" {
		return trackers.PreparedOperation{}, errors.New("trackers: HDB missing username/passkey")
	}

	category := hdbCategoryID(req.Meta)
	codec := hdbCodecID(req.Meta)
	medium := hdbMediumID(req.Meta)
	if category == 0 || codec == 0 || medium == 0 {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: HDB mapping failed category=%d codec=%d medium=%d", category, codec, medium)
	}

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: HDB description assets: %w", err)
		}
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	descriptionText := strings.TrimSpace(assets.Description)
	if !assets.Final {
		descriptionText, err = BuildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets.Description, assets.MenuImages, assets.Screenshots)
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}

	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: HDB prepared upload torrent: %w", err)
	}

	uploadURL := strings.TrimRight(baseURL, "/")
	uploadURL += hdbUploadPath

	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: HDB reviewed upload name: %w", err)
	}
	fields := buildUploadFields(req.Meta, req.Runtime.DescriptionConfig(), category, codec, medium, descriptionText, releaseName)
	preview := standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "HDB",
		ReleaseName:      releaseName,
		DescriptionGroup: "hdb",
		Description:      descriptionText,
		Endpoint:         uploadURL,
		Payload:          fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    torrentPath,
			Present: strings.TrimSpace(torrentPath) != "",
		}},
	})
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	cookies, err := resolveHDBCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	body, contentType, err := buildMultipartPayload(fields, torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 40 * time.Second}
	}
	state := preparedUploadState{
		torrentPath: torrentPath,
		description: descriptionText,
		fields:      fields,
		uploadURL:   uploadURL,
		cookies:     cookies,
		body:        body,
		contentType: contentType,
		passkey:     passkey,
		client:      client,
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state)
	}, nil), nil
}

func submitPreparedUpload(ctx context.Context, req trackers.PreparationInput, state preparedUploadState) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.uploadURL, bytes.NewReader(state.body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDB request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", state.contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	for _, cookie := range state.cookies {
		httpReq.AddCookie(cookie)
	}

	resp, err := state.client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDB upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	matches := hdbSuccessURLPattern.FindStringSubmatch(finalURL)
	if len(matches) < 2 {
		bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("HDB", resp.StatusCode, finalURL, bodyPreview)
	}

	torrentID := strings.TrimSpace(matches[1])
	downloadURL := buildHDBDownloadURL(state.uploadURL, req.Meta, torrentID, state.passkey)
	trackerTorrentPath := ""
	if torrentID != "" {
		resolvedPath, pathErr := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "HDB")
		if pathErr == nil {
			downloadErr := downloadPersonalizedTorrent(
				ctx,
				state.client,
				downloadURL,
				resolvedPath,
				state.cookies,
			)
			if downloadErr == nil {
				trackerTorrentPath = resolvedPath
			} else {
				trackers.LogRegisteredTorrentUnavailable(req.Logger, "HDB")
			}
		} else {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "HDB")
		}
	}

	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "HDB",
			TorrentID:   torrentID,
			DownloadURL: downloadURL,
			TorrentURL:  finalURL,
			TorrentPath: trackerTorrentPath,
		}},
	}, nil
}

func buildUploadFields(
	meta api.UploadSubject,
	appConfig config.Config,
	categoryID int,
	codecID int,
	mediumID int,
	description string,
	releaseName string,
) map[string]string {
	fields := map[string]string{
		"name":     releaseName,
		"category": strconv.Itoa(categoryID),
		"codec":    strconv.Itoa(codecID),
		"medium":   strconv.Itoa(mediumID),
		"origin":   "0",
		"descr":    strings.TrimSpace(description),
		"techinfo": "",
	}

	if trackers.IsInternalGroup(appConfig, "HDB", meta) {
		fields["origin"] = "1"
	}

	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if strings.TrimSpace(meta.MediaInfoTextPath) != "" {
			content, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
			if err == nil {
				fields["techinfo"] = strings.TrimSpace(string(content))
			}
		}
	}

	if isHDBTVCategory(meta) && meta.Identity.TVDBID != 0 {
		fields["tvdb"] = strconv.Itoa(meta.Identity.TVDBID)
	}
	if imdb := resolveIMDbURL(meta); imdb != "" {
		fields["imdb"] = imdb
	} else {
		fields["imdb"] = "0"
	}
	if isHDBTVCategory(meta) {
		season := meta.SeasonInt
		episode := meta.EpisodeInt
		if season <= 0 {
			season = 1
		}
		if episode <= 0 {
			episode = 1
		}
		fields["tvdb_season"] = strconv.Itoa(season)
		fields["tvdb_episode"] = strconv.Itoa(episode)
	}

	return fields
}

func resolveIMDbURL(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil {
		if value := strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL); value != "" {
			if !strings.HasSuffix(value, "/") {
				return value + "/"
			}
			return value
		}
	}
	if meta.Identity.IMDBID != 0 {
		return providerid.IMDb(meta.Identity.IMDBID).URL() + "/"
	}
	return ""
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: HDB write multipart field %q: %w", key, err)
		}
	}

	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: HDB open torrent file: %w", err)
	}
	defer file.Close()

	part, err := writer.CreateFormFile("file", strings.ReplaceAll(fields["name"], " ", ".")+".torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: HDB create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: HDB copy torrent file: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: HDB close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func downloadPersonalizedTorrent(
	ctx context.Context,
	client *http.Client,
	downloadURL string,
	torrentPath string,
	cookies []*http.Cookie,
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("trackers: HDB create personalized torrent request: %w", err)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	if err := trackers.DownloadRegisteredTorrent(ctx, client, req, torrentPath); err != nil {
		return fmt.Errorf("trackers: HDB registered torrent: %w", err)
	}
	return nil
}

func buildHDBDownloadURL(uploadURL string, meta api.UploadSubject, torrentID string, passkey string) string {
	if strings.TrimSpace(torrentID) == "" || strings.TrimSpace(passkey) == "" {
		return ""
	}
	base := strings.TrimSuffix(uploadURL, hdbUploadPath)
	filePart := pathutil.Base(meta.SourcePath)
	if filePart == "" || filePart == "." || filePart == string(filepath.Separator) {
		filePart = "download"
	}
	return fmt.Sprintf(
		"%s/download.php/%s?id=%s&passkey=%s",
		strings.TrimRight(base, "/"),
		url.PathEscape(filePart),
		url.QueryEscape(torrentID),
		url.QueryEscape(passkey),
	)
}
