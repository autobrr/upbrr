// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

var idPattern = regexp.MustCompile(`details\.php\?id=(\d+)`)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	extraFiles    []commonhttp.FileField
	blockedReason string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, cookies, err := prepareUploadState(ctx, req, req.Intent != trackers.PreparationIntentUpload)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: FF %s", state.blockedReason)
	}
	files := append([]commonhttp.FileField{{
		FieldName: "file",
		FileName:  state.releaseName + ".torrent",
		Path:      state.torrentPath,
	}}, state.extraFiles...)
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	artifactPath := ""
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "FF")
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
		return api.UploadSummary{}, fmt.Errorf("trackers: FF request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)
	client := httpclient.CloneWithTimeout(
		&http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		httpclient.DefaultTimeout,
	)
	result, err := commonhttp.ExecuteUpload(client, httpReq, commonhttp.UploadExecutionOptions{
		Tracker:       "FF",
		SuccessStatus: func(status int) bool { return status == http.StatusFound },
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FF execute upload: %w", err)
	}
	location := result.Header.Get("Location")
	match := idPattern.FindStringSubmatch(location)
	if result.Success && len(match) >= 2 {
		id := match[1]
		tURL := torrentURL + id
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "FF", state.torrentPath, artifactPath, announceURL, tURL, sourceFlag,
		)
		return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "FF",
			TorrentID:   id,
			TorrentURL:  tURL,
			TorrentPath: registeredPath,
		}}}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "FF", "upload_failure", result.Preview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("FF", result.StatusCode, result.Preview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "FF",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "ff",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput, dryRun bool) (uploadState, []*http.Cookie, error) {
	cookies, err := resolveCookies(ctx, req.Logger, req.TrackerConfig, req.Runtime.DBPath, dryRun)
	if err != nil {
		return uploadState{}, nil, err
	}
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(assets)
	fields := map[string]string{
		"MAX_FILE_SIZE": "10000000",
		"type":          resolveTypeID(req.Meta),
		"tags":          "",
		"descr":         description,
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(req.Meta)))
	switch {
	case req.Meta.Anime:
		fields["anime_type"] = resolveAnimeType(req.Meta)
		fields["anime_source"] = resolveAnimeSource(req.Meta)
		fields["anime_container"] = "mkv"
		fields["anime_v_res"] = req.Meta.Release.Resolution
		fields["anime_v_dar"] = "16_9"
		fields["anime_v_codec"] = resolveAnimeVCodec(req.Meta)
	case category == "MOVIE":
		fields["movie_type"] = resolveMovieType(req.Meta)
		fields["movie_source"] = resolveMovieSource(req.Meta)
		fields["movie_imdb"] = resolveIMDbURL(req.Meta)
		fields["pack"] = "0"
	default:
		fields["tv_type"] = resolveTVType(req.Meta)
		fields["tv_source"] = resolveTVSource(req.Meta)
		fields["tv_imdb"] = resolveIMDbURL(req.Meta)
		fields["pack"] = "0"
		if req.Meta.TVPack {
			fields["pack"] = "1"
		}
	}
	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: FF release name: %w", nameErr)
	}
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		releaseName: releaseName,
		fields:      fields,
		extraFiles:  resolveExtraFiles(ctx, req.Meta),
	}
	return state, cookies, nil
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
