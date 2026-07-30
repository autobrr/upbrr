// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://greatposterwall.com"
	torrentURL = baseURL + "/torrents.php?torrentid="
	sourceFlag = "GreatPosterWall"
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	groupID       string
	questionnaire *api.TrackerQuestionnaire
	blockedReason string
}

type apiResponse struct {
	Status   any    `json:"status"`
	Response any    `json:"response"`
	Error    string `json:"error"`
	Message  string `json:"message"`
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
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: GPW %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file_input",
		FileName:  "GPW.placeholder.torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	uploadEndpoint := baseURL + "/api.php?api_key=" + req.TrackerConfig.APIKey + "&action=upload"
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "GPW")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, body, contentType, uploadEndpoint, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	body []byte,
	contentType string,
	uploadEndpoint string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		uploadEndpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: GPW upload request build: %s", commonhttp.RedactErrorDetail(err.Error()))
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: GPW upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(
		resp,
		resp.StatusCode >= 200 && resp.StatusCode < 300,
		commonhttp.DefaultResponsePreviewBytes,
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: GPW read upload response: %w", err)
	}
	var decoded apiResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return api.UploadSummary{}, commonhttp.UploadHTTPError("GPW", resp.StatusCode, responsePreview)
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: GPW decode response: %w", err)
	}
	id := extractTorrentID(decoded.Response)
	status := strings.ToLower(strings.TrimSpace(fmt.Sprint(decoded.Status)))
	if (status == "success" || status == "ok" || status == "200") && id != "" {
		tURL := torrentURL + id
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "GPW", state.torrentPath, artifactPath, announceURL, tURL, sourceFlag,
		)
		return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "GPW",
			TorrentID:   id,
			TorrentURL:  tURL,
			TorrentPath: registeredPath,
		}}}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "GPW", "upload_failure", responsePreview, ".json")
	return api.UploadSummary{}, fmt.Errorf(
		"trackers: GPW %s",
		metautil.FirstNonEmptyTrimmed(
			commonhttp.ExtractHTTPErrorDetail(responsePreview),
			commonhttp.RedactErrorDetail(decoded.Error),
			commonhttp.RedactErrorDetail(decoded.Message),
			"upload failed",
		),
	)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	message := "dry-run payload generated"
	if state.groupID == "" {
		message += " for new group"
	} else {
		message += " for existing group"
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "GPW",
		ReadyMessage:     message,
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "gpw",
		Description:      state.description,
		Endpoint:         baseURL + "/api.php?action=upload",
		Payload:          state.fields,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: GPW missing api_key")
	}
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	groupID, _ := lookupGroupID(ctx, req.TrackerConfig.APIKey, req.Meta)
	answers := standalone.QuestionnaireAnswers(req.Meta, "GPW")
	fields := buildFields(req, req.TrackerConfig, description, groupID, answers)
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: GPW reviewed upload name: %w", err)
	}
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		fields:        fields,
		groupID:       groupID,
		questionnaire: buildQuestionnaire(req.Meta, groupID, answers),
	}
	if reason := validateFields(groupID, fields); reason != "" {
		state.blockedReason = reason
	}
	return state, nil
}

func lookupGroupID(ctx context.Context, apiKey string, meta api.UploadSubject) (string, error) {
	if meta.Identity.IMDBID == 0 {
		return "", nil
	}
	url := fmt.Sprintf("%s/api.php?api_key=%s&action=torrent&req=group&imdbID=tt%07d", baseURL, apiKey, meta.Identity.IMDBID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("trackers: GPW torrent URL lookup request build: %s", commonhttp.RedactErrorDetail(err.Error()))
	}
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
	if err != nil {
		return "", fmt.Errorf("trackers: GPW torrent URL lookup request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var decoded apiResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", nil
	}
	if responseMap, ok := decoded.Response.(map[string]any); ok {
		if value, ok := responseMap["ID"]; ok {
			return strings.TrimSpace(fmt.Sprint(value)), nil
		}
	}
	return "", nil
}

func buildFields(
	req trackers.PreparationInput,
	trackerCfg config.TrackerConfig,
	description string,
	groupID string,
	answers map[string]string,
) map[string]string {
	meta := req.Meta
	fields := map[string]string{
		"codec_other":               "",
		"codec":                     resolveCodec(meta),
		"container_other":           "",
		"container":                 resolveContainer(meta),
		"mediainfo[]":               trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, meta),
		"movie_edition_information": onOff(strings.TrimSpace(meta.Edition) != ""),
		"processing_other":          "",
		"processing":                resolveProcessing(meta),
		"release_desc":              description,
		"remaster_custom_title":     "",
		"remaster_title":            strings.TrimSpace(meta.Edition),
		"remaster_year":             "",
		"resolution_height":         "",
		"resolution_width":          "",
		"resolution":                resolveResolution(meta),
		"source_other":              "",
		"source":                    resolveSource(meta),
		"submit":                    "true",
		"subtitle_type":             resolveSubtitleType(meta),
		"subtitles[]":               strings.Join(resolveSubtitles(meta), ","),
	}
	if groupID != "" {
		fields["groupid"] = groupID
	} else {
		fields["data_source"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["data_source"]), "imdb")
		fields["identifier"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["identifier"]), resolveIdentifier(meta))
		fields["desc"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["desc"]), resolveOverview(meta))
		fields["image"] = strings.TrimSpace(answers["poster_url"])
		fields["maindesc"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["main_desc"]), resolveOverview(meta))
		fields["name"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["title"]), meta.Release.Title)
		fields["releasetype"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["release_type"]), resolveMovieType(meta))
		fields["subname"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["subname"]), meta.Release.Title)
		fields["tags"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["tags"]), resolveTags(meta))
		fields["year"] = strconv.Itoa(resolveYear(meta))
		fields["artists[]"] = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["director_name"]), resolveDirectorName(meta))
		fields["importance[]"] = "1"
		fields["artist_ids[]"] = strings.TrimSpace(answers["director_imdb"])
		fields["artist_subs[]"] = strings.TrimSpace(answers["director_chinese"])
		fields["characters[]"] = ""
		fields["main_artist_number"] = "1"
	}
	maps.Copy(fields, resolveMediaFlags(meta))
	if meta.Scene {
		fields["scene"] = "on"
	}
	if meta.PersonalRelease {
		if isDiscUpload(meta) {
			fields["buy"] = "on"
		} else {
			fields["diy"] = "on"
		}
	}
	if trackerCfg.Exclusive {
		fields["jinzhuan"] = "on"
	}
	return fields
}

func isDiscUpload(meta api.UploadSubject) bool {
	return strings.TrimSpace(meta.DiscType) != "" || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC")
}

func extractTorrentID(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if torrentID, ok := typed["torrent_id"]; ok {
			return strings.TrimSpace(fmt.Sprint(torrentID))
		}
	case []any:
		if len(typed) > 0 {
			if first, ok := typed[0].(map[string]any); ok {
				if torrentID, ok := first["torrent_id"]; ok {
					return strings.TrimSpace(fmt.Sprint(torrentID))
				}
			}
		}
	}
	return ""
}

func resolveIdentifier(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.Identity.IMDBID)
	}
	if meta.Identity.TMDBID > 0 {
		return strconv.Itoa(meta.Identity.TMDBID)
	}
	return ""
}

func resolveYear(meta api.UploadSubject) int {
	if meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Year > 0 {
		return meta.ProviderMetadata.TMDB.Year
	}
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0 {
		return meta.ProviderMetadata.IMDB.Year
	}
	return meta.Release.Year
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return ""
}
