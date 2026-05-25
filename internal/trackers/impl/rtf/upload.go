// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package rtf

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	uploadURL  = "https://retroflix.club/api/upload"
	browseURL  = "https://retroflix.club/browse/t/"
	sourceFlag = "sunshine"
)

type uploadResponse struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
	Torrent struct {
		ID any `json:"id"`
	} `json:"torrent"`
}

type uploadState struct {
	torrentPath   string
	releaseName   string
	description   string
	payload       map[string]any
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: RTF %s", state.blockedReason)
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

	resp, err := (&http.Client{Timeout: 40 * time.Second}).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: RTF upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)

	var decoded uploadResponse
	_ = json.Unmarshal(responseBody, &decoded)
	if resp.StatusCode == http.StatusCreated && !decoded.Error {
		id := strings.TrimSpace(fmt.Sprint(decoded.Torrent.ID))
		if id == "" {
			return api.UploadSummary{}, errors.New("trackers: RTF upload succeeded but torrent id missing")
		}
		tURL := browseURL + id
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "RTF")
			if err != nil {
				return api.UploadSummary{}, err
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, err
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "RTF",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: "https://retroflix.club/api/torrent/" + id + "/download",
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "RTF", "upload_failure", responseBody, ".json")
	return api.UploadSummary{}, fmt.Errorf("trackers: RTF %s", firstNonEmpty(decoded.Message, fmt.Sprintf("upload failed with status %d", resp.StatusCode)))
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
		Tracker:          "RTF",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "rtf",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          payload,
		Files:            []api.TrackerDryRunFile{{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: RTF missing api_key")
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, err
	}
	var assets trackers.DescriptionAssets
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
		if err != nil {
			trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	description := buildDescription(assets)
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return uploadState{}, err
	}
	releaseName := firstNonEmpty(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename)
	payload := map[string]any{
		"name":        releaseName,
		"description": description,
		"mediaInfo":   commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath)),
		"nfo":         "",
		"url":         imdbURL(req.Meta),
		"descr":       description,
		"poster":      resolvePoster(req.Meta),
		"type":        resolveType(req.Meta),
		"screenshots": screenshots(assets.Screenshots),
		"isAnonymous": req.TrackerConfig.Anon,
		"file":        base64.StdEncoding.EncodeToString(torrentBytes),
	}
	return uploadState{
		torrentPath:   torrentPath,
		releaseName:   releaseName,
		description:   description,
		payload:       payload,
		blockedReason: validateEligibility(req.Meta),
	}, nil
}

func buildDescription(assets trackers.DescriptionAssets) string {
	return strings.TrimSpace(assets.Description)
}

func validateEligibility(meta api.PreparedMetadata) string {
	genres := strings.ToLower(genresText(meta) + "," + keywordsText(meta))
	for _, value := range []string{"xxx", "erotic", "porn", "adult", "orgy"} {
		if strings.Contains(genres, value) {
			return "adult content is not allowed"
		}
	}
	limit := time.Now().UTC().AddDate(-10, 0, 3)
	if t := releaseDate(meta); !t.IsZero() {
		if t.After(limit) {
			return "content must be at least 10 years old"
		}
		return ""
	}
	if year := resolveYear(meta); year > limit.Year() {
		return "content must be at least 10 years old"
	}
	return ""
}

func releaseDate(meta api.PreparedMetadata) time.Time {
	if meta.ExternalMetadata.TMDB == nil {
		return time.Time{}
	}
	for _, value := range []string{
		strings.TrimSpace(meta.ExternalMetadata.TMDB.ReleaseDate),
		strings.TrimSpace(meta.ExternalMetadata.TMDB.LastAirDate),
		strings.TrimSpace(meta.ExternalMetadata.TMDB.FirstAirDate),
	} {
		if value == "" {
			continue
		}
		if t, err := time.Parse("2006-01-02", value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func resolveYear(meta api.PreparedMetadata) int {
	if meta.Release.Year > 0 {
		return meta.Release.Year
	}
	if meta.ExternalMetadata.TMDB == nil {
		return 0
	}
	return meta.ExternalMetadata.TMDB.Year
}

func resolveType(meta api.PreparedMetadata) string {
	if !isTV(meta) {
		return "401"
	}
	return "402"
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

func imdbURL(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d/", meta.ExternalIDs.IMDBID)
}

func resolvePoster(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB == nil {
		return ""
	}
	return firstNonEmpty(meta.ExternalMetadata.TMDB.Poster)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}

func genresText(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return firstNonEmpty(meta.ExternalMetadata.TMDB.Genres, meta.Release.Genre)
	}
	return firstNonEmpty(meta.Release.Genre)
}

func keywordsText(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
}
