// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://hd-space.org"
	uploadURL  = baseURL + "/index.php?page=upload"
	torrentURL = baseURL + "/index.php?page=torrent-details&id="
	sourceFlag = "HD-Space"
)

var idPattern = regexp.MustCompile(`download\.php\?id=([a-zA-Z0-9]+)`)

type uploadState struct {
	torrentPath string
	description string
	releaseName string
	fields      map[string]string
	nfo         *commonhttp.FileField
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
	if !supportsHDSResolution(trackers.ResolveRuleResolution(api.NewRuleSubject(req.Meta))) {
		return trackers.PreparedOperation{}, errors.New("trackers: HDS resolution must be at least 720p")
	}
	files := []commonhttp.FileField{{
		FieldName: "torrent",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}}
	if state.nfo != nil {
		files = append(files, *state.nfo)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	artifactPath := ""
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "HDS")
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
		return api.UploadSummary{}, fmt.Errorf("trackers: HDS request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	result, err := commonhttp.ExecuteUpload(httpclient.New(httpclient.DefaultTimeout), httpReq, commonhttp.UploadExecutionOptions{
		Tracker:       "HDS",
		SuccessStatus: func(status int) bool { return status >= 200 && status < 400 },
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDS execute upload: %w", err)
	}
	combined := result.FinalURL + "\n" + string(result.Body)
	match := idPattern.FindStringSubmatch(combined)
	if result.Success && len(match) >= 2 {
		id := strings.TrimSpace(match[1])
		tURL := torrentURL + id
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "HDS", state.torrentPath, artifactPath, announceURL, tURL, sourceFlag,
		)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "HDS",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: baseURL + "/download.php?id=" + id,
				TorrentPath: registeredPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "HDS", "upload_failure", result.Preview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("HDS", result.StatusCode, result.Preview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "HDS",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "hds",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
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
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: HDS reviewed upload name: %w", err)
	}
	fields := map[string]string{
		"category":      strconv.Itoa(resolveCategoryID(req.Meta)),
		"filename":      releaseName,
		"genre":         resolveGenres(req.Meta),
		"imdb":          resolveIMDbURL(req.Meta),
		"info":          description,
		"nuk_rea":       "",
		"nuk":           "false",
		"req":           "false",
		"submit":        "Send",
		"t3d":           boolString(req.Meta.Is3D != ""),
		"user_id":       "",
		"youtube_video": resolveYouTube(req.Meta),
		"anonymous":     boolString(req.TrackerConfig.Anon),
	}
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		releaseName: fields["filename"],
		fields:      fields,
	}
	if file, ok := resolveNFO(req.Meta); ok {
		state.nfo = &file
	}
	return state, cookies, nil
}

func resolveLogo(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func resolveKeywords(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords)
	}
	return ""
}

func resolveIMDbURL(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil && strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL) != "" {
		return strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL)
	}
	if meta.Identity.IMDBID > 0 {
		return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.Identity.IMDBID)
	}
	return ""
}

func resolveYouTube(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
	}
	return ""
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
