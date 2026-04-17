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

	"github.com/autobrr/upbrr/internal/services/bbcode"
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
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, strings.NewReader(string(body)))
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", strings.TrimSpace(req.TrackerConfig.APIKey))

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: SPD upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)

	var decoded uploadResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "SPD", "upload_failure", responseBody, ".txt")
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
	return api.UploadSummary{}, fmt.Errorf("trackers: SPD upload failed status=%d", resp.StatusCode)
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
		return uploadState{}, err
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description, err := buildDescription(req.Meta, assets)
	if err != nil {
		return uploadState{}, err
	}
	channelID, blockedReason, questionnaire := resolveChannel(ctx, req)
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return uploadState{}, err
	}
	payload := map[string]any{
		"bdInfo":           resolveBDInfo(req.Meta),
		"coverPhotoUrl":    firstNonEmpty(req.Meta.ExternalMetadata.TMDB.Backdrop, req.Meta.ExternalMetadata.TMDB.Poster),
		"description":      genresText(req.Meta),
		"media_info":       commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath)),
		"name":             normalizeName(firstNonEmpty(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename)),
		"nfo":              "",
		"plot":             firstNonEmpty(req.Meta.EpisodeOverview, req.Meta.ExternalMetadata.TMDB.Overview),
		"poster":           firstNonEmpty(req.Meta.ExternalMetadata.TMDB.Poster),
		"technicalDetails": description,
		"screenshots":      screenshots(assets.Screenshots),
		"type":             resolveCategory(req.Meta),
		"url":              imdbURL(req.Meta),
		"channel":          channelID,
		"file":             base64.StdEncoding.EncodeToString(torrentBytes),
	}
	return uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   payload["name"].(string),
		payload:       payload,
		questionnaire: questionnaire,
		blockedReason: blockedReason,
	}, nil
}

func resolveChannel(ctx context.Context, req trackers.UploadRequest) (string, string, *api.TrackerQuestionnaire) {
	answers := questionnaireAnswers(req.Meta)
	input := firstNonEmpty(strings.TrimSpace(answers["channel"]), strings.TrimSpace(req.TrackerConfig.Channel))
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
		return "", err
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", strings.TrimSpace(apiKey))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var decoded []channelResult
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", err
	}
	for _, item := range decoded {
		if strings.EqualFold(strings.TrimSpace(item.Tag), strings.TrimSpace(input)) {
			return strings.TrimSpace(fmt.Sprint(item.ID)), nil
		}
	}
	return "", errors.New("channel not found")
}

func buildDescription(meta api.PreparedMetadata, assets trackers.DescriptionAssets) (string, error) {
	parts := make([]string, 0, 4)
	if title := strings.TrimSpace(meta.EpisodeTitle); title != "" {
		parts = append(parts, "[center]"+title+"[/center]")
	}
	if overview := strings.TrimSpace(meta.EpisodeOverview); overview != "" {
		parts = append(parts, "[center]"+overview+"[/center]")
	}
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, base)
	}
	parts = append(parts, "[url=https://github.com/autobrr/upbrr]upbrr[/url]")
	return bbcode.FinalizeTrackerDescription("SPD", strings.TrimSpace(strings.Join(parts, "\n\n"))), nil
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

func screenshots(images []api.ScreenshotImage) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		if raw := strings.TrimSpace(firstNonEmpty(image.RawURL, image.ImgURL)); raw != "" {
			out = append(out, raw)
		}
	}
	return out
}

func resolveBDInfo(meta api.PreparedMetadata) string {
	if strings.EqualFold(meta.DiscType, "BDMV") {
		return commonhttp.ReadOptionalFile(strings.TrimSpace(meta.MediaInfoTextPath))
	}
	return ""
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
		return err
	}
	httpReq.Header.Set("Authorization", strings.TrimSpace(apiKey))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return err
	}
	return os.WriteFile(output, body, 0o600)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func genresText(meta api.PreparedMetadata) string {
	return firstNonEmpty(meta.ExternalMetadata.TMDB.Genres, meta.Release.Genre)
}

func keywordsText(meta api.PreparedMetadata) string {
	return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}
