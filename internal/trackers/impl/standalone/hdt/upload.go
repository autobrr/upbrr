// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

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
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

var successPattern = regexp.MustCompile(`details\.php\?id=([a-zA-Z0-9]+)|Upload successful!`)
var detailsPattern = regexp.MustCompile(`details\.php\?id=([a-zA-Z0-9]+)`)

type uploadState struct {
	baseURL       string
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
	state, cookies, err := prepareUploadState(ctx, req, req.Intent != trackers.PreparationIntentUpload)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if trackers.ResolveRuleResolution(api.NewRuleSubject(req.Meta)) == "" {
		return trackers.PreparedOperation{}, errors.New("trackers: HDT missing resolution")
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: HDT %s", state.blockedReason)
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
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "HDT")
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.baseURL+"/upload.php", bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	result, err := commonhttp.ExecuteUpload(httpclient.New(httpclient.DefaultTimeout), httpReq, commonhttp.UploadExecutionOptions{
		Tracker:       "HDT",
		SuccessStatus: func(status int) bool { return status >= 200 && status < 400 },
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT execute upload: %w", err)
	}
	combined := result.FinalURL + "\n" + string(result.Body)
	id := ""
	if match := detailsPattern.FindStringSubmatch(combined); len(match) >= 2 {
		id = match[1]
	}
	if result.Success && successPattern.MatchString(combined) {
		tURL := result.FinalURL
		if id != "" && !strings.Contains(tURL, "details.php?id=") {
			tURL = state.baseURL + "/details.php?id=" + id
		}
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "HDT", state.torrentPath, artifactPath, announceURL, "hd-torrents.org",
		)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "HDT",
				TorrentID:   id,
				TorrentURL:  tURL,
				TorrentPath: registeredPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "HDT", "upload_failure", result.Preview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("HDT", result.StatusCode, result.Preview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "HDT",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "hdt",
		Description:      state.description,
		Endpoint:         state.baseURL + "/upload.php",
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput, dryRun bool) (uploadState, []*http.Cookie, error) {
	base := resolveBaseURL()
	cookies, err := loadCookies(ctx, req.Runtime.DBPath, base)
	if err != nil {
		return uploadState{}, nil, err
	}
	token := strings.Join([]string{"dry", "run", "token"}, "-")
	if !dryRun {
		token, err = fetchToken(ctx, base, cookies)
		if err != nil {
			return uploadState{}, nil, err
		}
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
	description := buildDescription(req, assets)
	releaseName, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: HDT release name: %w", nameErr)
	}
	fields := map[string]string{
		"filename":  releaseName,
		"category":  strconv.Itoa(resolveCategoryID(req.Meta)),
		"info":      description,
		"csrfToken": token,
		"season":    boolString(req.Meta.TVPack),
		"anonymous": boolString(req.TrackerConfig.Anon),
	}
	if req.Meta.Is3D != "" {
		fields["3d"] = "true"
	}
	hdr := strings.ToUpper(strings.TrimSpace(req.Meta.HDR))
	if strings.Contains(hdr, "HDR10+") {
		fields["HDR10"] = "true"
		fields["HDR10Plus"] = "true"
	} else if strings.Contains(hdr, "HDR") {
		fields["HDR10"] = "true"
	}
	if strings.Contains(hdr, "DV") {
		fields["DolbyVision"] = "true"
	}
	if imdb := resolveIMDbURL(req.Meta); imdb != "" {
		fields["infosite"] = imdb + "/"
	}
	state := uploadState{
		baseURL:     base,
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

func resolveBaseURL() string {
	return "https://hd-torrents.me"
}

func resolveLogo(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
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

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
