// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://digitalcore.club"
	apiBaseURL = baseURL + "/api/v1/torrents"
	sourceFlag = "DigitalCore.club"
)

type uploadState struct {
	torrentPath   string
	releaseName   string
	description   string
	mediaInfo     string
	fields        map[string]string
	blockedReason string
}

type uploadResponse struct {
	ID      json.Number `json:"id"`
	Message string      `json:"message"`
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC %s", state.blockedReason)
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file",
		FileName:  state.releaseName + ".torrent",
		Path:      state.torrentPath,
	}})
	if err != nil {
		return api.UploadSummary{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+"/upload", strings.NewReader(string(body)))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC request build: %w", err)
	}
	httpReq.Body = io.NopCloser(strings.NewReader(string(body)))
	httpReq.ContentLength = int64(len(body))
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	httpReq.Header.Set("X-API-KEY", strings.TrimSpace(req.TrackerConfig.APIKey))

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC upload request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC read response body: %w", err)
	}

	if len(responseBody) == 0 {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC empty response body (status %d)", resp.StatusCode)
	}

	var decoded uploadResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		snippet := string(responseBody)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: DC decode response: %w | response: %s", err, snippet)
	}
	torrentID := strings.TrimSpace(fmt.Sprint(decoded.ID))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && torrentID != "" && torrentID != "<nil>" {
		torrentURL := baseURL + "/torrent/" + torrentID + "/"
		downloadURL := apiBaseURL + "/download/" + torrentID
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "DC")
			if err != nil {
				return api.UploadSummary{}, err
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, torrentURL, sourceFlag); err != nil {
				return api.UploadSummary{}, err
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "DC",
				TorrentID:   torrentID,
				TorrentURL:  torrentURL,
				DownloadURL: downloadURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	if _, artifactErr := commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "DC", "upload_failure", responseBody, ".json"); artifactErr != nil && req.Logger != nil {
		req.Logger.Warnf("trackers: DC failure artifact write failed: %v", artifactErr)
	}
	message := firstNonEmpty(strings.TrimSpace(decoded.Message), strings.TrimSpace(string(responseBody)), "upload failed")
	return api.UploadSummary{}, fmt.Errorf("trackers: DC %s", message)
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	status := "ready"
	message := "dry-run payload generated"
	if state.blockedReason != "" {
		status = "blocked"
		message = state.blockedReason
	}
	return api.TrackerDryRunEntry{
		Tracker:          "DC",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "dc",
		Description:      state.description,
		Endpoint:         apiBaseURL + "/upload",
		Payload:          cloneFields(state.fields),
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: DC missing api_key")
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, err
	}

	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	if announceURL != "" {
		artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "DC")
		if err == nil {
			if err := trackers.WritePersonalizedTorrent(torrentPath, artifactPath, announceURL, "Created by upbrr", sourceFlag); err == nil {
				torrentPath = artifactPath
			}
		}
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		assets = trackers.DescriptionAssets{}
	}
	description, err := buildDescription(req.Meta, assets)
	if err != nil {
		return uploadState{}, err
	}
	mediaInfo, err := resolveMediaInfo(req.Meta)
	if err != nil {
		return uploadState{}, err
	}
	releaseName := resolveUploadName(req.Meta)
	fields := map[string]string{
		"category":        strconv.Itoa(resolveCategoryID(req.Meta)),
		"imdbId":          resolveDCIMDbIDText(req.Meta),
		"nfo":             description,
		"mediainfo":       mediaInfo,
		"reqid":           "0",
		"section":         "new",
		"frileech":        "1",
		"anonymousUpload": resolveAnon(req),
		"p2p":             "0",
		"unrar":           "1",
	}
	state := uploadState{
		torrentPath: torrentPath,
		releaseName: releaseName,
		description: description,
		mediaInfo:   mediaInfo,
		fields:      fields,
	}
	if strings.TrimSpace(fields["imdbId"]) == "" {
		state.blockedReason = "missing IMDb ID"
	}
	return state, nil
}

func buildDescription(meta api.PreparedMetadata, assets trackers.DescriptionAssets) (string, error) {
	parts := make([]string, 0, 6)
	if overview := resolveOverview(meta); overview != "" && strings.EqualFold(categoryOf(meta), "TV") && strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]\n[center]"+overview+"[/center]")
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if media := resolveMediaDump(meta); media != "" {
			parts = append(parts, "[code]"+strings.TrimSpace(media)+"[/code]")
		}
	}
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}
	parts = append(parts, "[center][url=https://github.com/autobrr/upbrr]Created by upbrr[/url][/center]")
	return bbcode.FinalizeTrackerDescription("DC", strings.TrimSpace(strings.Join(parts, "\n\n"))), nil
}

func resolveCategoryID(meta api.PreparedMetadata) int {
	resolution := strings.ToLower(strings.TrimSpace(firstNonEmpty(meta.Release.Resolution, meta.ReleaseNameNoTag, meta.ReleaseName)))
	category := categoryOf(meta)
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if category == "TV" {
			return 14
		}
		if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "2160p") {
			return 38
		}
		return 3
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		if category == "TV" {
			return 11
		}
		return 1
	}
	if category == "TV" && meta.TVPack {
		return 12
	}
	if isSD(meta) {
		if category == "TV" {
			return 10
		}
		return 2
	}
	switch category {
	case "TV":
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 13
		case "1080p", "1080i":
			return 9
		default:
			return 8
		}
	default:
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 4
		case "1080p", "1080i":
			return 6
		default:
			_ = resolution
			return 5
		}
	}
}

func resolveUploadName(meta api.PreparedMetadata) string {
	name := strings.TrimSpace(meta.Filename)
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext)
}

func resolveMediaInfo(meta api.PreparedMetadata) (string, error) {
	if text := resolveMediaDump(meta); strings.TrimSpace(text) != "" {
		return text, nil
	}
	return "", errors.New("trackers: DC missing mediainfo")
}

func resolveMediaDump(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		for _, value := range []any{meta.BDInfo["summary"], meta.BDInfo["BD_SUMMARY_00"]} {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return firstNonEmpty(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
}

func resolveDCIMDbIDText(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbIDText) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbIDText)
	}
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
	}
	return ""
}

func resolveAnon(req trackers.UploadRequest) string {
	if req.TrackerConfig.Anon {
		return "1"
	}
	return "0"
}

func categoryOf(meta api.PreparedMetadata) string {
	if category := strings.ToUpper(strings.TrimSpace(meta.ExternalIDs.Category)); category != "" {
		return category
	}
	return strings.ToUpper(strings.TrimSpace(meta.MediaInfoCategory))
}

func resolveOverview(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil:
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Overview)
	case meta.ExternalMetadata.IMDB != nil:
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Plot)
	case meta.ExternalMetadata.TVmaze != nil:
		return strings.TrimSpace(meta.ExternalMetadata.TVmaze.Summary)
	default:
		return strings.TrimSpace(meta.EpisodeOverview)
	}
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.WebURL) == "" || strings.TrimSpace(image.RawURL) == "" {
			continue
		}
		parts = append(parts, "[url="+strings.TrimSpace(image.WebURL)+"][img=350]"+strings.TrimSpace(image.RawURL)+"[/img][/url]")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[center]" + strings.Join(parts, " ") + "[/center]"
}

func isSD(meta api.PreparedMetadata) bool {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	return resolution == "480p" || resolution == "576p" || resolution == ""
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
