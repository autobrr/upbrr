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
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://digitalcore.club"
	apiBaseURL = baseURL + "/api/v1/torrents"
	sourceFlag = "DigitalCore.club"
)

var (
	reDCChars = regexp.MustCompile(`[^a-zA-Z0-9 .\-]`)
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
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+"/upload", strings.NewReader(string(body)))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC request build: %w", err)
	}
	httpReq.Body = io.NopCloser(strings.NewReader(string(body)))
	httpReq.ContentLength = int64(len(body))
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	httpReq.Header.Set("X-Api-Key", strings.TrimSpace(req.TrackerConfig.APIKey))

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC upload request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC read response body: %w", err)
	}

	var decoded uploadResponse
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return api.UploadSummary{}, commonhttp.UploadHTTPError("DC", resp.StatusCode, responseBody)
			}
			return api.UploadSummary{}, fmt.Errorf("trackers: DC decode response: %w", err)
		}
	}

	if len(responseBody) == 0 {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC empty response body (status %d)", resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if _, artifactErr := commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "DC", "upload_failure", responseBody, ".json"); artifactErr != nil && req.Logger != nil {
			req.Logger.Warnf("trackers: DC failure artifact write failed: %v", artifactErr)
		}

		if msg := strings.TrimSpace(decoded.Message); msg != "" {
			return api.UploadSummary{}, fmt.Errorf("trackers: DC %s", msg)
		}

		snippet := strings.TrimSpace(string(responseBody))
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: DC upload failed with status %d: %s", resp.StatusCode, snippet)
	}

	torrentID := strings.TrimSpace(fmt.Sprint(decoded.ID))
	if torrentID != "" && torrentID != "<nil>" {
		torrentURL := baseURL + "/torrent/" + torrentID + "/"
		downloadURL := apiBaseURL + "/download/" + torrentID
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "DC")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, torrentURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
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

	message := metautil.FirstNonEmptyTrimmed(commonhttp.ExtractHTTPErrorDetail(responseBody), commonhttp.RedactErrorDetail(decoded.Message), commonhttp.RedactErrorDetail(string(responseBody)), "upload failed")
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
	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	if apiKey == "" {
		return uploadState{}, errors.New("trackers: DC missing api_key")
	}

	state := uploadState{}

	imdbID := strings.TrimSpace(trackers.ResolveIMDbIDText(req.Meta))
	if imdbID == "" {
		state.blockedReason = "missing imdb id"
	}

	categoryID := resolveCategoryID(req.Meta)
	if categoryID == 0 && state.blockedReason == "" {
		state.blockedReason = "unable to determine category"
	}

	releaseName := resolveUploadName(req.Meta)
	if releaseName == "" && state.blockedReason == "" {
		state.blockedReason = "unable to determine release name"
	}

	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}

	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	if announceURL != "" {
		artifactPath, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "DC")
		if err != nil {
			return uploadState{}, fmt.Errorf("resolve DC personalized torrent path: %w", err)
		}

		if err := trackers.WritePersonalizedTorrent(torrentPath, artifactPath, announceURL, "Created by upbrr", sourceFlag); err != nil {
			return uploadState{}, fmt.Errorf("write DC personalized torrent: %w", err)
		}
		torrentPath = artifactPath
	}

	mediaInfo, err := resolveMediaInfo(req.Meta)
	if err != nil && state.blockedReason == "" {
		state.blockedReason = "missing media info"
	}

	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}

	description := buildDescription(req, assets)
	if description == "" && state.blockedReason == "" {
		state.blockedReason = "missing description"
	}

	state.torrentPath = torrentPath
	state.releaseName = releaseName
	state.description = description
	state.mediaInfo = mediaInfo
	state.fields = map[string]string{
		"category":        strconv.Itoa(categoryID),
		"imdbId":          imdbID,
		"nfo":             description,
		"mediainfo":       mediaInfo,
		"reqid":           "0",
		"section":         "new",
		"frileech":        "1",
		"anonymousUpload": resolveAnon(req),
		"p2p":             "0",
		"unrar":           "1",
	}

	return state, nil
}

func buildDescription(req trackers.UploadRequest, assets trackers.DescriptionAssets) string {
	meta := req.Meta
	var parts []string

	// Custom Header
	if header := strings.TrimSpace(req.AppConfig.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information (Only BDInfo is added to the description, MediaInfo is sent as a form field)
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, _ := trackers.ReadBDInfo(req.AppConfig.MainSettings.DBPath, meta)
		if strings.TrimSpace(bdinfo) != "" {
			parts = append(parts, "[code]"+strings.TrimSpace(bdinfo)+"[/code]")
		}
	}

	// NFO
	if nfo := strings.TrimSpace(commonhttp.ReadOptionalFile(meta.SceneNFOPath)); nfo != "" {
		parts = append(parts, "[nfo]"+nfo+"[/nfo]")
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}

	// Combined Screenshots
	allShots := make([]api.ScreenshotImage, 0, len(assets.MenuImages)+len(assets.Screenshots))
	allShots = append(allShots, assets.MenuImages...)
	allShots = append(allShots, assets.Screenshots...)
	if shots := screenshotBlock(allShots); shots != "" {
		parts = append(parts, shots)
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(req.AppConfig.Description.TonemappedHeader); tonemapHeader != "" && descriptionunit3d.ShouldIncludeTonemappedHeader(meta, req.AppConfig, assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// Signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[center][url=%s]%s[/url][/center]", link, text))

	// Join and finalize
	description := strings.Join(parts, "\n\n")
	finalized := bbcode.FinalizeTrackerDescription("DC", description)

	// Debug saving
	if meta.Options.Debug {
		descriptionunit3d.SaveDescriptionDebug(meta, "DC", req.AppConfig.MainSettings.DBPath, finalized, req.Logger)
	}

	return finalized
}

func resolveCategoryID(meta api.PreparedMetadata) int {
	res := strings.TrimSpace(strings.ToLower(meta.Release.Resolution))
	discType := strings.ToLower(meta.DiscType)
	category := categoryOf(meta)

	if discType != "" {
		switch discType {
		case "bdmv":
			if category == "TV" {
				return 14
			}
			if category == "MOVIE" {
				if res == "2160p" {
					return 38
				}
				if res == "1080p" {
					return 3
				}
			}
		case "dvd", "hddvd":
			if category == "TV" {
				return 11
			}
			if category == "MOVIE" {
				return 1
			}
		}
	}

	if category == "TV" && meta.TVPack {
		return 12
	}

	if isSD(meta) {
		if category == "TV" {
			return 10
		}
		if category == "MOVIE" {
			return 2
		}
	}

	switch category {
	case "TV":
		switch res {
		case "2160p":
			return 13
		case "1080p", "1080i":
			return 9
		case "720p", "720i":
			return 8
		}
	case "MOVIE":
		switch res {
		case "2160p":
			return 4
		case "1080p", "1080i":
			return 6
		case "720p", "720i":
			return 5
		}
	}

	return 0
}

func resolveUploadName(meta api.PreparedMetadata) string {
	name := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(
		meta.ReleaseNameClean,
		meta.ReleaseName,
	))

	nameFilename := strings.TrimSpace(meta.Filename)
	if nameFilename != "" {
		ext := filepath.Ext(nameFilename)
		if n := strings.TrimSuffix(nameFilename, ext); n != "" {
			name = n
		}
	}

	name = sanitizeFilename(name)

	if meta.Scene {
		name += " [UNRAR]"
	}

	return name
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "DD+", "DDP")
	name = strings.ReplaceAll(name, "DTS:", "DTS-")
	name = strings.ReplaceAll(name, "HDR10+", "HDR10P")

	// Remove anything that is not alphanumeric, space, dot, or hyphen
	name = reDCChars.ReplaceAllString(name, "")
	return name
}

func resolveMediaInfo(meta api.PreparedMetadata) (string, error) {
	if text := metautil.FirstNonEmptyTrimmed(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText)); text != "" {
		return text, nil
	}
	return "", errors.New("trackers: DC missing mediainfo")
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
	return resolution == "480p" || resolution == "576p"
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
