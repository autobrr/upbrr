// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package spd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL   = "https://speedapp.io"
	uploadURL = baseURL + "/api/upload"
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	payload       map[string]any
	questionnaire *api.TrackerQuestionnaire
	blockedReason string
}

type uploadResponse struct {
	Status      bool   `json:"status"`
	Error       bool   `json:"error"`
	DownloadURL string `json:"downloadUrl"`
	Torrent     struct {
		ID any `json:"id"`
	} `json:"torrent"`
}

type channelResult struct {
	ID  any    `json:"id"`
	Tag string `json:"tag"`
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD %s", state.blockedReason)
	}

	body, err := json.Marshal(state.payload)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD marshal upload payload: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, strings.NewReader(string(body)))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD build upload request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", strings.TrimSpace(req.TrackerConfig.APIKey))

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var decoded uploadResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "SPD", "upload_failure", responseBody, ".txt")
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return api.UploadSummary{}, commonhttp.UploadHTTPError("SPD", resp.StatusCode, responseBody)
		}
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD decode response: %w", err)
	}
	if resp.StatusCode == http.StatusOK && decoded.Status && !decoded.Error {
		torrentID := strings.TrimSpace(fmt.Sprint(decoded.Torrent.ID))
		artifactPath := ""
		if torrentID != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "SPD")
			if err == nil {
				if dlErr := downloadTrackerTorrent(ctx, baseURL+"/api/torrent/"+torrentID+"/download", req.TrackerConfig.APIKey, artifactPath); dlErr != nil {
					artifactPath = ""
				}
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "SPD",
				TorrentID:   torrentID,
				TorrentURL:  baseURL + "/browse/" + torrentID,
				DownloadURL: baseURL + "/api/torrent/" + torrentID + "/download",
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "SPD", "upload_failure", responseBody, ".json")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("SPD", resp.StatusCode, responseBody)
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
	payload := make(map[string]string, len(state.payload))
	for key, value := range state.payload {
		payload[key] = strings.TrimSpace(fmt.Sprint(value))
	}
	return api.TrackerDryRunEntry{
		Tracker:          "SPD",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "spd",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          payload,
		Questionnaire:    state.questionnaire,
		Files:            []api.TrackerDryRunFile{{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: SPD missing api_key")
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	channelID, blockedReason, questionnaire := resolveChannel(ctx, req)
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: SPD read torrent file: %w", err)
	}
	releaseName := normalizeName(metautil.FirstNonEmptyTrimmed(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename))
	payload := map[string]any{
		"bdInfo":           trackers.ReadBDinfoOrMediaInfo(req.AppConfig.MainSettings.DBPath, req.Meta),
		"coverPhotoUrl":    metautil.FirstNonEmptyTrimmed(req.Meta.ExternalMetadata.TMDB.Backdrop, req.Meta.ExternalMetadata.TMDB.Poster),
		"description":      genresText(req.Meta),
		"media_info":       commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath)),
		"name":             releaseName,
		"nfo":              "",
		"plot":             metautil.FirstNonEmptyTrimmed(req.Meta.EpisodeOverview, req.Meta.ExternalMetadata.TMDB.Overview),
		"poster":           metautil.FirstNonEmptyTrimmed(req.Meta.ExternalMetadata.TMDB.Poster),
		"technicalDetails": description,
		"screenshots":      combineUniqueScreenshots(assets.MenuImages, assets.Screenshots),
		"type":             resolveCategory(req.Meta),
		"url":              imdbURL(req.Meta),
		"channel":          channelID,
		"file":             base64.StdEncoding.EncodeToString(torrentBytes),
	}
	return uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		payload:       payload,
		questionnaire: questionnaire,
		blockedReason: blockedReason,
	}, nil
}

func resolveChannel(ctx context.Context, req trackers.UploadRequest) (string, string, *api.TrackerQuestionnaire) {
	answers := questionnaireAnswers(req.Meta)
	input := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["channel"]), strings.TrimSpace(req.TrackerConfig.Channel))
	if input == "" {
		return "1", "", nil
	}
	if digitsOnly(input) {
		return input, "", nil
	}
	id, err := lookupChannelID(ctx, req.TrackerConfig.APIKey, input)
	if err == nil && id != "" {
		return id, "", nil
	}
	return "", "answer the channel questionnaire with a valid channel id or tag", &api.TrackerQuestionnaire{
		Tracker: "SPD",
		Fields: []api.TrackerQuestionnaireField{{
			Key: "channel", Label: "Channel", Kind: "text", Value: input, Placeholder: "1 or channel tag", Required: true,
		}},
	}
}

func lookupChannelID(ctx context.Context, apiKey string, input string) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/channel?search="+url.QueryEscape(input), nil)
	if err != nil {
		return "", fmt.Errorf("trackers: SPD build channel lookup request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", strings.TrimSpace(apiKey))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: SPD channel lookup request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var decoded []channelResult
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("trackers: SPD unmarshal channel lookup response: %w", err)
	}
	for _, item := range decoded {
		if strings.EqualFold(strings.TrimSpace(item.Tag), strings.TrimSpace(input)) {
			return strings.TrimSpace(fmt.Sprint(item.ID)), nil
		}
	}
	return "", errors.New("channel not found")
}

func buildDescription(req trackers.UploadRequest, assets trackers.DescriptionAssets) string {
	meta := req.Meta
	var parts []string

	// Avoid unnecessary descriptions
	if strings.TrimSpace(assets.Description) != "" || strings.TrimSpace(meta.EpisodeOverview) != "" {
		// Custom Header
		if header := strings.TrimSpace(req.AppConfig.Description.CustomDescriptionHeader); header != "" {
			parts = append(parts, header)
		}

		// Logo
		if logo, logoSize := descriptionunit3d.ResolveLogo(meta, req.AppConfig); logo != "" {
			parts = append(parts, fmt.Sprintf("[center][img=%d]https://image.tmdb.org/t/p/w300/%s[/img][/center]", logoSize, logo))
		}

		// TV Details
		if strings.TrimSpace(meta.EpisodeOverview) != "" {
			parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
			parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
		}

		// User Description
		if strings.TrimSpace(assets.Description) != "" {
			parts = append(parts, strings.TrimSpace(assets.Description))
		}
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(req.AppConfig.Description.TonemappedHeader); tonemapHeader != "" && descriptionunit3d.ShouldIncludeTonemappedHeader(meta, req.AppConfig, assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// custom user signature
	if signature := strings.TrimSpace(req.AppConfig.Description.CustomSignature); signature != "" {
		parts = append(parts, signature)
	}

	// Signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[url=%s]%s[/url]", link, text))

	// Join and finalize
	description := strings.Join(parts, "\n\n")
	finalized := bbcode.FinalizeTrackerDescription("SPD", description)

	// Debug saving
	if meta.Options.Debug {
		descriptionunit3d.SaveDescriptionDebug(meta, "SPD", req.AppConfig.MainSettings.DBPath, finalized, req.Logger)
	}

	return finalized
}

func resolveCategory(meta api.PreparedMetadata) string {
	romanian := hasRomanian(meta)
	if containsWord(genresText(meta), "documentary") || containsWord(keywordsText(meta), "documentary") {
		if romanian {
			return "63"
		}
		return "9"
	}
	if meta.Anime {
		return "3"
	}
	if isTV(meta) {
		if meta.TVPack {
			if romanian {
				return "66"
			}
			return "41"
		}
		if isSD(meta.Release.Resolution) {
			if romanian {
				return "46"
			}
			return "45"
		}
		if romanian {
			return "44"
		}
		return "43"
	}
	if meta.Release.Resolution == "2160p" && !strings.EqualFold(meta.Type, "DISC") {
		if romanian {
			return "57"
		}
		return "61"
	}
	if strings.EqualFold(meta.Type, "DISC") {
		if romanian {
			return "24"
		}
		return "17"
	}
	if romanian {
		return "29"
	}
	return "8"
}

func hasRomanian(meta api.PreparedMetadata) bool {
	for _, value := range append([]string{}, append(meta.AudioLanguages, meta.SubtitleLanguages...)...) {
		if strings.EqualFold(strings.TrimSpace(value), "romanian") {
			return true
		}
	}
	for _, code := range meta.ExternalMetadata.TMDB.OriginCountry {
		if strings.EqualFold(strings.TrimSpace(code), "RO") {
			return true
		}
	}
	return false
}

func combineUniqueScreenshots(menu []api.ScreenshotImage, normal []api.ScreenshotImage) []string {
	var out []string
	seen := make(map[string]struct{})

	for _, image := range menu {
		u := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL))
		if u != "" && !isSeen(seen, u) {
			out = append(out, u)
			seen[u] = struct{}{}
		}
	}
	for _, image := range normal {
		u := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL))
		if u != "" && !isSeen(seen, u) {
			out = append(out, u)
			seen[u] = struct{}{}
		}
	}
	return out
}

func isSeen(seen map[string]struct{}, url string) bool {
	_, ok := seen[url]
	return ok
}

func normalizeName(input string) string {
	mapper := func(r rune) rune {
		if r > unicode.MaxASCII {
			return -1
		}
		if strings.ContainsRune(`\/*?"<>|`, r) {
			return -1
		}
		return r
	}
	return strings.Join(strings.Fields(strings.Map(mapper, strings.ReplaceAll(input, ":", " -"))), " ")
}

func imdbURL(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.ExternalIDs.IMDBID)
}

func downloadTrackerTorrent(ctx context.Context, urlValue string, apiKey string, output string) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, urlValue, nil)
	if err != nil {
		return fmt.Errorf("trackers: SPD build torrent download request: %w", err)
	}
	httpReq.Header.Set("Authorization", strings.TrimSpace(apiKey))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(httpReq)
	if err != nil {
		return fmt.Errorf("trackers: SPD torrent download request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("trackers: SPD read torrent response: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return fmt.Errorf("trackers: SPD create torrent output dir: %w", err)
	}
	if err := os.WriteFile(output, body, 0o600); err != nil {
		return fmt.Errorf("trackers: SPD write torrent output: %w", err)
	}
	return nil
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["SPD"]
}

func digitsOnly(value string) bool {
	_, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil
}

func containsWord(a string, b string) bool {
	return strings.Contains(strings.ToLower(a), strings.ToLower(b))
}

func isSD(res string) bool {
	return strings.HasPrefix(strings.TrimSpace(res), "480") || strings.HasPrefix(strings.TrimSpace(res), "576") || strings.HasPrefix(strings.TrimSpace(res), "540")
}

func genresText(meta api.PreparedMetadata) string {
	return metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.Genres, meta.Release.Genre)
}

func keywordsText(meta api.PreparedMetadata) string {
	return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}
