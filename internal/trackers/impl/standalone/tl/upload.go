// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL       = "https://www.torrentleech.org"
	apiUploadURL  = baseURL + "/torrents/upload/apiupload"
	httpUploadURL = baseURL + "/torrents/upload/"
	torrentURL    = baseURL + "/torrent/"
	sourceFlag    = "TorrentLeech.org"
)

type uploadState struct {
	torrentPath string
	description string
	releaseName string
	fields      map[string]string
	files       []commonhttp.FileField
	endpoint    string
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	announces := announceList(req.TrackerConfig)
	if req.Intent == trackers.PreparationIntentUpload && len(announces) == 0 {
		return trackers.PreparedOperation{}, errors.New("trackers: TL required passkey-derived announce URL is missing")
	}
	state, client, err := prepareUploadState(ctx, req)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, state.files)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "TL")
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, client, body, contentType, announces, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	client *http.Client,
	body []byte,
	contentType string,
	announces []string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.endpoint, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TL build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TL upload request: %w", err)
	}
	defer resp.Body.Close()
	successCandidate := resp.StatusCode == http.StatusFound || (req.TrackerConfig.APIUpload && resp.StatusCode >= 200 && resp.StatusCode < 300)
	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(resp, successCandidate, commonhttp.DefaultResponsePreviewBytes)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TL read upload response: %w", err)
	}

	torrentID := ""
	if req.TrackerConfig.APIUpload {
		text := strings.TrimSpace(string(responseBody))
		if _, err := strconv.Atoi(text); err == nil {
			torrentID = text
		}
	} else if resp.StatusCode == http.StatusFound {
		torrentID = strings.TrimPrefix(strings.TrimSpace(resp.Header.Get("Location")), "/successfulupload?torrentID=")
	}
	if torrentID == "" {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "TL", "upload_failure", responsePreview, ".html")
		return api.UploadSummary{}, commonhttp.UploadHTTPError("TL", resp.StatusCode, responsePreview)
	}

	urlValue := torrentURL + torrentID
	announceURL := ""
	if len(announces) > 0 {
		announceURL = announces[0]
	}
	registeredPath := trackers.PersistReconstructedRegisteredTorrent(
		req.Logger, "TL", state.torrentPath, artifactPath, announceURL, sourceFlag,
	)
	return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
		Tracker:     "TL",
		TorrentID:   torrentID,
		TorrentURL:  urlValue,
		TorrentPath: registeredPath,
	}}}, nil
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "TL",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "tl",
		Description:      state.description,
		Endpoint:         state.endpoint,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "torrent",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, *http.Client, error) {
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
		return uploadState{}, nil, fmt.Errorf("trackers: TL release name: %w", nameErr)
	}
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		releaseName: releaseName,
	}

	if req.TrackerConfig.APIUpload {
		state.endpoint = apiUploadURL
		state.fields = map[string]string{
			"announcekey": announceKey(req.TrackerConfig),
			"category":    resolveCategory(req.Meta),
			"description": description,
			"name":        releaseName,
			"nonscene":    boolWord(!req.Meta.Scene, "on", "off"),
		}
		category, _ := req.Meta.Identity.RequireCategory()
		switch {
		case req.Meta.Anime && req.Meta.Identity.MALID > 0:
			state.fields["animeid"] = tlAnimeIDURL(req.Meta.Identity.MALID)
		case category == api.CanonicalCategoryMovie && req.Meta.Identity.IMDBID > 0:
			state.fields["imdb"] = providerid.IMDb(req.Meta.Identity.IMDBID).Prefixed()
		case category == api.CanonicalCategoryTV:
			state.fields["tvmazeid"] = strconv.Itoa(req.Meta.Identity.TVmazeID)
			if req.Meta.TVPack {
				state.fields["tvmazetype"] = "true"
			}
		}
		if req.TrackerConfig.Anon {
			state.fields["is_anonymous_upload"] = "on"
		}
		state.files = []commonhttp.FileField{{
			FieldName: "torrent",
			FileName:  releaseName + ".torrent",
			Path:      torrentPath,
		}}
		return state, &http.Client{Timeout: 30 * time.Second}, nil
	}

	state.endpoint = httpUploadURL
	state.fields = map[string]string{
		"name":                releaseName,
		"category":            resolveCategory(req.Meta),
		"nonscene":            boolWord(!req.Meta.Scene, "on", "off"),
		"imdbURL":             imdbURL(req.Meta),
		"tvMazeURL":           tvmazeURL(req.Meta),
		"igdbURL":             "",
		"torrentNFO":          "0",
		"torrentDesc":         "1",
		"nfotextbox":          "",
		"torrentComment":      "0",
		"uploaderComments":    "",
		"is_anonymous_upload": boolWord(req.TrackerConfig.Anon, "on", "off"),
	}
	if req.TrackerConfig.ImgRehost {
		for idx, shot := range screenshots(assets.Screenshots) {
			state.fields["screenshots["+strconv.Itoa(idx)+"]"] = shot
		}
	}
	state.files = []commonhttp.FileField{
		{
			FieldName: "torrent",
			FileName:  "torrent.torrent",
			Path:      torrentPath,
		},
		{
			FieldName: "nfo",
			FileName:  "description.txt",
			Content:   []byte(description),
		},
	}
	client, err := cookieClient(ctx, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	return state, client, nil
}

// tlAnimeIDURL formats TorrentLeech's animeid field. TL names the value as an
// AniList URL, while upbrr's canonical MALID carries the same anime identifier
// for this tracker payload.
func tlAnimeIDURL(malID int) string {
	return fmt.Sprintf("https://anilist.co/anime/%d", malID)
}
