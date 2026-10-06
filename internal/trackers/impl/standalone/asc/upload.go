// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

var torrentIDPattern = regexp.MustCompile(`/torrents/(\d+)(?:[/?#]|$)`)

type uploadState struct {
	torrentPath     string
	description     string
	payload         uploadPayload
	screenshotPaths []string
	coverURL        string
	blockedReason   string
	questionnaire   *api.TrackerQuestionnaire
	releaseName     string
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
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC %s", state.blockedReason)
	}

	cookies, _, err := LoadCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC load cookies: %w", err)
	}
	client := httpclient.New(httpclient.DefaultTimeout)
	session, err := newSessionClient(client, cookies)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	if err := warmUploadSession(ctx, session); err != nil {
		return trackers.PreparedOperation{}, err
	}
	files := []commonhttp.FileField{{FieldName: "torrent", Path: state.torrentPath}}
	if state.coverURL != "" {
		cover, err := downloadCover(ctx, client, state.coverURL)
		if err != nil {
			return trackers.PreparedOperation{}, err
		}
		files = append(files, cover)
	}
	screenshots, err := loadScreenshotFiles(state.screenshotPaths)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	remotePaths, err := uploadScreenshots(ctx, session, screenshots)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	req.Logger.Infof("trackers: ASC screenshots uploaded tracker=ASC count=%d", len(remotePaths))
	body, contentType, err := commonhttp.BuildMultipartPayloadMulti(state.payload.multipartFields(remotePaths), files)
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: ASC build payload: %w", err)
	}
	artifactPath, _ := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "ASC")
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, session, state, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	client *http.Client,
	state uploadState,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	if err := warmUploadSession(ctx, client); err != nil {
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+uploadPath, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	setXHRHeaders(httpReq, client)
	result, err := commonhttp.ExecuteUpload(client, httpReq, commonhttp.UploadExecutionOptions{Tracker: "ASC"})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC upload: %w", err)
	}

	torrentID := parseUploadID(result.FinalURL)
	if result.Success && torrentID != "" {
		torrentURL := baseURL + torrentPath + torrentID
		req.Logger.Infof("trackers: ASC upload succeeded tracker=ASC torrent_id=%s", torrentID)
		registeredPath := persistRegisteredTorrent(ctx, req, client, state.torrentPath, torrentID, artifactPath, announceURL)
		maybeApprove(ctx, client, req, torrentID)
		if req.Runtime.Internal {
			req.Logger.Debugf("trackers: ASC internal flag skipped tracker=ASC reason=moderator_only")
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "ASC",
				TorrentID:   torrentID,
				TorrentURL:  torrentURL,
				TorrentPath: registeredPath,
			}},
		}, nil
	}

	if _, artifactErr := commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "ASC", "upload_failure", result.Preview, ".html"); artifactErr != nil {
		req.Logger.Warnf("trackers: ASC failure artifact write failed: %v", artifactErr)
	}
	if result.Success {
		return api.UploadSummary{}, fmt.Errorf("trackers: ASC upload response did not identify the torrent final_url=%s", result.FinalURL)
	}
	return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("ASC", result.StatusCode, result.FinalURL, result.Body)
}

// persistRegisteredTorrent stores the site's torrent, which may differ from
// the uploaded one, and falls back to local reconstruction.
func persistRegisteredTorrent(
	ctx context.Context,
	req trackers.PreparationInput,
	client *http.Client,
	uploadedPath string,
	torrentID string,
	artifactPath string,
	announceURL string,
) string {
	if artifactPath == "" {
		trackers.LogRegisteredTorrentUnavailable(req.Logger, "ASC")
		return ""
	}
	downloadReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+torrentPath+torrentID+"/download", nil)
	if err == nil {
		downloadReq.Header.Set("User-Agent", userAgent)
		if err = trackers.DownloadRegisteredTorrent(ctx, client, downloadReq, artifactPath); err == nil {
			return artifactPath
		}
	}
	req.Logger.Debugf("trackers: ASC registered torrent download failed tracker=ASC decision=reconstruct")
	return trackers.PersistReconstructedRegisteredTorrent(req.Logger, "ASC", uploadedPath, artifactPath, announceURL, sourceFlag)
}

func maybeApprove(ctx context.Context, client *http.Client, req trackers.PreparationInput, torrentID string) {
	if !req.TrackerConfig.UploaderStatus {
		req.Logger.Debugf("trackers: ASC auto approval skipped tracker=ASC reason=uploader_status_disabled")
		return
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, baseURL+"/admin/torrents/"+url.PathEscape(torrentID)+"/approve", nil)
	if err != nil {
		req.Logger.Warnf("trackers: ASC auto approval failed tracker=ASC reason=request_build")
		return
	}
	setXHRHeaders(httpReq, client)
	resp, err := client.Do(httpReq)
	if err != nil {
		req.Logger.Warnf("trackers: ASC auto approval failed tracker=ASC reason=request")
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden:
		req.Logger.Debugf("trackers: ASC auto approval skipped tracker=ASC reason=not_staff")
	case resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices:
		req.Logger.Warnf("trackers: ASC auto approval failed tracker=ASC status=%d", resp.StatusCode)
	default:
		req.Logger.Debugf("trackers: ASC auto approval done tracker=ASC torrent_id=%s", torrentID)
	}
}

// downloadCover fetches the poster once during preparation so the submitted
// payload is fully captured.
func downloadCover(ctx context.Context, client *http.Client, coverURL string) (commonhttp.FileField, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, coverURL, nil)
	if err != nil {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover request build: %w", err)
	}
	httpReq.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover download returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC read cover: %w", err)
	}
	if len(data) > maxImageBytes {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover exceeds %d bytes", maxImageBytes)
	}
	contentType := http.DetectContentType(data)
	ext, ok := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
	}[contentType]
	if !ok {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover has unsupported type %s", contentType)
	}
	return commonhttp.FileField{
		FieldName: "cover",
		FileName:  "cover" + ext,
		Content:   data,
	}, nil
}

// resolveCoverURL picks the poster for the cover upload, capping TMDB
// originals to a size that stays under the site's image limit.
func resolveCoverURL(meta api.UploadSubject) string {
	poster := strings.TrimSpace(resolvePoster(meta))
	if poster == "" {
		return ""
	}
	if parsed, err := url.Parse(poster); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return strings.Replace(poster, "/t/p/original/", "/t/p/w780/", 1)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	files := make([]api.TrackerDryRunFile, 0, 2+len(state.screenshotPaths))
	files = append(files,
		api.TrackerDryRunFile{
			Field:   "torrent",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		},
		api.TrackerDryRunFile{
			Field:   "cover",
			Path:    state.coverURL,
			Present: state.coverURL != "",
		},
	)
	for _, screenshot := range state.screenshotPaths {
		files = append(files, api.TrackerDryRunFile{
			Field:   "screenshots[]",
			Path:    screenshot,
			Present: true,
		})
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "ASC",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "asc",
		Description:      state.description,
		Endpoint:         baseURL + uploadPath,
		Payload:          state.payload.previewFields(),
		Questionnaire:    state.questionnaire,
		Files:            files,
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, error) {
	torrentFile, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: ASC release name: %w", err)
	}
	description := buildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets)
	state := uploadState{
		torrentPath:     torrentFile,
		description:     description,
		payload:         buildPayload(req.Meta, req.TrackerConfig, description, releaseName, resolveMediaInfoReport(req.Meta, req.Runtime.DBPath)),
		screenshotPaths: selectScreenshots(assets),
		coverURL:        resolveCoverURL(req.Meta),
		releaseName:     releaseName,
		questionnaire:   buildQuestionnaire(req.Meta),
	}
	state.blockedReason = metautil.FirstNonEmptyTrimmed(
		authProblem(ctx, req.Runtime.DBPath),
		validatePayloadFields(req.Meta, state.payload, len(state.screenshotPaths), state.coverURL),
	)
	return state, nil
}

func parseUploadID(finalURL string) string {
	parsed, err := url.Parse(finalURL)
	if err != nil {
		return ""
	}
	if matches := torrentIDPattern.FindStringSubmatch(parsed.Path); len(matches) == 2 {
		return matches[1]
	}
	return ""
}
