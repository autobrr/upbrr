// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"

	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
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
	ID      any    `json:"id"`
	Message string `json:"message"`
}

func upload(ctx context.Context, req trackers.PreparationInput) (api.UploadSummary, error) {
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

	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(
		resp,
		resp.StatusCode >= 200 && resp.StatusCode < 300,
		commonhttp.DefaultResponsePreviewBytes,
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: DC read upload response: %w", err)
	}
	var decoded uploadResponse
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &decoded); err != nil {
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return api.UploadSummary{}, commonhttp.UploadHTTPError("DC", resp.StatusCode, responsePreview)
			}
			return api.UploadSummary{}, fmt.Errorf("trackers: DC decode response: %w", err)
		}
	}
	torrentID := strings.TrimSpace(fmt.Sprint(decoded.ID))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && torrentID != "" && torrentID != "<nil>" {
		torrentURL := baseURL + "/torrent/" + torrentID + "/"
		downloadURL := apiBaseURL + "/download/" + torrentID
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "DC")
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

	if _, artifactErr := commonhttp.WriteFailureArtifact(
		req.Meta,
		req.Runtime.DBPath,
		"DC",
		"upload_failure",
		responsePreview,
		".json",
	); artifactErr != nil &&
		req.Logger != nil {
		req.Logger.Warnf("trackers: DC failure artifact write failed: %v", artifactErr)
	}
	message := metautil.FirstNonEmptyTrimmed(
		commonhttp.ExtractHTTPErrorDetail(responsePreview),
		commonhttp.RedactErrorDetail(decoded.Message),
		commonhttp.RedactErrorDetail(string(responsePreview)),
		"upload failed",
	)
	return api.UploadSummary{}, fmt.Errorf("trackers: DC %s", message)
}

func buildUploadDryRun(ctx context.Context, req trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
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

func prepareUploadState(_ context.Context, req trackers.PreparationInput) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: DC missing api_key")
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	mediaInfo, err := resolveMediaInfo(req.Meta)
	if err != nil {
		return uploadState{}, err
	}
	releaseName := resolveUploadName(req.Meta)
	fields := map[string]string{
		"category":        strconv.Itoa(resolveCategoryID(req.Meta)),
		"imdbId":          resolveIMDbID(req.Meta),
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

func buildDescription(req trackers.PreparationInput, assets trackers.DescriptionAssets) string {
	meta := req.Meta
	var parts []string

	// Custom Header
	if header := strings.TrimSpace(req.Runtime.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information
	if media := resolveMedia(req, meta); media != "" {
		parts = append(parts, strings.TrimSpace(media))
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
	if tonemapHeader := strings.TrimSpace(
		req.Runtime.Description.TonemappedHeader,
	); tonemapHeader != "" &&
		descriptionunit3d.ShouldIncludeTonemappedHeader(api.NewDescriptionSubject(meta), req.Runtime.DescriptionConfig(), assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// Signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[center][url=%s]%s[/url][/center]", link, text))

	// Join and finalize
	description := strings.Join(parts, "\n\n")
	finalized := finalizeDescription(description)

	// Explicit dry runs retain the local diagnostic description artifact.
	if req.Intent == trackers.PreparationIntentDryRun {
		descriptionunit3d.SaveDescriptionDebug(api.NewDescriptionSubject(meta), "DC", req.Runtime.DBPath, finalized, req.Logger)
	}

	return finalized
}

func resolveCategoryID(meta api.UploadSubject) int {
	resolution := strings.ToLower(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.Release.Resolution, meta.ReleaseNameNoTag, meta.ReleaseName)))
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

func resolveUploadName(meta api.UploadSubject) string {
	name := metautil.FirstNonEmptyTrimmed(
		strings.TrimSpace(meta.SceneName),
		strings.TrimSpace(meta.ReleaseNameClean),
		strings.TrimSpace(meta.ReleaseName),
		strings.TrimSpace(meta.Filename),
	)
	if name == "" {
		name = "release"
	}
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName) + " [UNRAR]"
	}
	name = strings.NewReplacer("DD+", "DDP", "DTS:", "DTS-", "HDR10+", "HDR10P").Replace(name)
	out := strings.Builder{}
	for _, r := range name {
		if r > 127 {
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' || r == '.' || r == '-' {
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(out.String())
}

func resolveMediaInfo(meta api.UploadSubject) (string, error) {
	if text := metautil.FirstNonEmptyTrimmed(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText)); text != "" {
		return text, nil
	}
	return "", errors.New("trackers: DC missing mediainfo")
}

func resolveMedia(req trackers.PreparationInput, meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, _ := trackers.ReadBDInfo(req.Runtime.DBPath, meta)
		return strings.TrimSpace(bdinfo)
	}
	return metautil.FirstNonEmptyTrimmed(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
}

func resolveIMDbID(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return "tt" + strconv.Itoa(meta.Identity.IMDBID)
	}
	return ""
}

func resolveAnon(req trackers.PreparationInput) string {
	if req.TrackerConfig.Anon {
		return "1"
	}
	return "0"
}

func categoryOf(meta api.UploadSubject) string {
	category, err := meta.Identity.RequireCategory()
	if err != nil {
		return ""
	}
	return strings.ToUpper(string(category))
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

func isSD(meta api.UploadSubject) bool {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	return resolution == "480p" || resolution == "576p" || resolution == ""
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}
