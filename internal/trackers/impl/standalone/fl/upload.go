// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package fl

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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

var (
	validatorPattern = regexp.MustCompile(`name="validator"\s+value="([^"]+)"`)
	successPattern   = regexp.MustCompile(`details\.php\?id=(\d+)&uploaded=`)
	newHTTPClient    = httpclient.New
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	questionnaire *api.TrackerQuestionnaire
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
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: FL %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file",
		FileName:  resolveTorrentFileName(req.Meta, state.releaseName) + ".torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: FL create cookie jar: %w", err)
	}
	base, _ := url.Parse(baseURL)
	jar.SetCookies(base, cookies)
	client := httpclient.CloneWithTimeout(&http.Client{Jar: jar}, httpclient.DefaultTimeout)
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, client, body, contentType)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	client *http.Client,
	body []byte,
	contentType string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FL request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	result, err := commonhttp.ExecuteUpload(client, httpReq, commonhttp.UploadExecutionOptions{
		Tracker:       "FL",
		SuccessStatus: func(status int) bool { return status >= 200 && status < 400 },
	})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FL execute upload: %w", err)
	}
	match := successPattern.FindStringSubmatch(result.FinalURL)
	if len(match) >= 2 {
		id := match[1]
		artifactPath := ""
		resolvedPath, pathErr := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "FL")
		if pathErr == nil {
			if downloadErr := downloadPersonalizedTorrent(ctx, client, id, resolvedPath); downloadErr == nil {
				artifactPath = resolvedPath
			} else {
				trackers.LogRegisteredTorrentUnavailable(req.Logger, "FL")
			}
		} else {
			trackers.LogRegisteredTorrentUnavailable(req.Logger, "FL")
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "FL",
				TorrentID:   id,
				TorrentURL:  baseURL + "/details.php?id=" + id,
				DownloadURL: downloadURL + id,
				TorrentPath: artifactPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "FL", "upload_failure", result.Preview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("FL", result.StatusCode, result.FinalURL, result.Preview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "FL",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "fl",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          state.fields,
		Questionnaire:    state.questionnaire,
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
	name, nameErr := req.ReviewedUploadName()
	if nameErr != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: FL release name: %w", nameErr)
	}
	fields := map[string]string{
		"name":  name,
		"type":  strconv.Itoa(resolveCategoryID(req.Meta)),
		"descr": description,
		"nfo":   resolveMedia(req.Meta),
	}
	if req.Meta.Identity.IMDBID > 0 {
		fields["imdbid"] = providerid.IMDb(req.Meta.Identity.IMDBID).Digits()
		fields["description"] = resolveGenres(req.Meta)
	}
	if strings.TrimSpace(req.TrackerConfig.UploaderName) != "" && !req.TrackerConfig.Anon {
		fields["epenis"] = strings.TrimSpace(req.TrackerConfig.UploaderName)
	}
	if hasRomanianAudio(req.Meta) {
		fields["materialro"] = "on"
	}
	if strings.EqualFold(strings.TrimSpace(req.Meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(req.Meta.Type), "REMUX") || req.Meta.TVPack {
		fields["freeleech"] = "on"
	}
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   name,
		fields:        fields,
		questionnaire: buildQuestionnaire(req.Meta, name),
	}
	if strings.TrimSpace(name) == "" {
		state.blockedReason = "missing release name"
	}
	return state, cookies, nil
}

func downloadPersonalizedTorrent(ctx context.Context, client *http.Client, id string, outputPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL+id, nil)
	if err != nil {
		return fmt.Errorf("trackers: FL torrent download request build: %w", err)
	}
	if err := trackers.DownloadRegisteredTorrent(ctx, client, req, outputPath); err != nil {
		return fmt.Errorf("trackers: FL registered torrent: %w", err)
	}
	return nil
}

func resolveTorrentFileName(meta api.UploadSubject, releaseName string) string {
	if meta.Anime && strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-")), "SubsPlease") {
		return releaseName
	}
	return strings.TrimSuffix(strings.TrimSpace(meta.Filename), filepath.Ext(strings.TrimSpace(meta.Filename)))
}
