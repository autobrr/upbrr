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
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	defaultBaseURL = "https://retroflix.club"
	sourceFlag     = "sunshine"
)

var newHTTPClient = func() *http.Client { return &http.Client{Timeout: 40 * time.Second} }

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
	apiKey, err := resolveAPIKey(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}

	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: RTF %s", state.blockedReason)
	}

	body, err := json.Marshal(state.payload)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: RTF marshal upload payload: %w", err)
	}
	baseURL := resolveBaseURL(req.TrackerConfig)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(baseURL, "/api/upload"), strings.NewReader(string(body)))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: RTF create upload request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", apiKey)

	resp, err := newHTTPClient().Do(httpReq)
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
		tURL := joinURL(baseURL, "/browse/t/") + id
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "RTF")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "RTF",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: joinURL(baseURL, "/api/torrent/") + id + "/download",
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "RTF", "upload_failure", responseBody, ".json")
	return api.UploadSummary{}, fmt.Errorf("trackers: RTF %s", metautil.FirstNonEmptyTrimmed(commonhttp.ExtractHTTPErrorDetail(responseBody), commonhttp.RedactErrorDetail(decoded.Message), fmt.Sprintf("upload failed with status %d", resp.StatusCode)))
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
		Endpoint:         joinURL(resolveBaseURL(req.TrackerConfig), "/api/upload"),
		Payload:          payload,
		Files:            []api.TrackerDryRunFile{{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" && (strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "") {
		return uploadState{}, errors.New("trackers: RTF missing api_key or username/password")
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
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
		return uploadState{}, fmt.Errorf("trackers: RTF read torrent file: %w", err)
	}
	releaseName := metautil.FirstNonEmptyTrimmed(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename)
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

func resolveAPIKey(ctx context.Context, req trackers.UploadRequest) (string, error) {
	apiKey := strings.TrimSpace(req.TrackerConfig.APIKey)
	baseURL := resolveBaseURL(req.TrackerConfig)
	if apiKey != "" {
		valid, err := testAPIKey(ctx, baseURL, apiKey)
		if err == nil && valid {
			return apiKey, nil
		}
		if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
			if err != nil {
				return "", fmt.Errorf("trackers: RTF API key validation failed and username/password not configured: %w", err)
			}
			return "", errors.New("trackers: RTF API key invalid and username/password not configured")
		}
	}
	if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
		return "", errors.New("trackers: RTF missing api_key or username/password")
	}
	refreshed, err := refreshAPIKey(ctx, baseURL, req.TrackerConfig)
	if err != nil {
		return "", err
	}
	if err := persistRefreshedAPIKey(ctx, req.AppConfig.MainSettings.DBPath, refreshed); err != nil && req.Logger != nil {
		req.Logger.Warnf("trackers: RTF failed to persist refreshed API key: %v", err)
	}
	return refreshed, nil
}

// ResolveSessionForTrackerAuthLogin validates RTF API auth or refreshes the API
// key with configured credentials for tracker-auth checks.
func ResolveSessionForTrackerAuthLogin(ctx context.Context, cfg config.TrackerConfig, dbPath string, _ api.TrackerAuthLoginRequest) error {
	apiKey := strings.TrimSpace(cfg.APIKey)
	baseURL := resolveBaseURL(cfg)
	if apiKey != "" {
		valid, err := testAPIKey(ctx, baseURL, apiKey)
		if err == nil && valid {
			return nil
		}
		if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
			if err != nil {
				return fmt.Errorf("trackers: RTF API key validation failed and username/password not configured: %w", err)
			}
			return errors.New("trackers: RTF API key invalid and username/password not configured")
		}
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return errors.New("trackers: RTF missing api_key or username/password")
	}
	refreshed, err := refreshAPIKey(ctx, baseURL, cfg)
	if err != nil {
		return err
	}
	if err := persistRefreshedAPIKey(ctx, dbPath, refreshed); err != nil {
		return err
	}
	return nil
}

func testAPIKey(ctx context.Context, baseURL string, apiKey string) (bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(baseURL, "/api/test"), nil)
	if err != nil {
		return false, fmt.Errorf("trackers: RTF create API test request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", strings.TrimSpace(apiKey))
	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return false, fmt.Errorf("trackers: RTF API test request: %w", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func refreshAPIKey(ctx context.Context, baseURL string, cfg config.TrackerConfig) (string, error) {
	payload := map[string]string{
		"username": strings.TrimSpace(cfg.Username),
		"password": strings.TrimSpace(cfg.Password),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("trackers: RTF marshal API login payload: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(baseURL, "/api/login"), strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("trackers: RTF create API login request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := newHTTPClient().Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: RTF API login request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("trackers: RTF API login failed status=%d", resp.StatusCode)
	}
	var decoded struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return "", fmt.Errorf("trackers: RTF decode API login response: %w", err)
	}
	token := strings.TrimSpace(decoded.Token)
	if token == "" {
		return "", errors.New("trackers: RTF API login response missing token")
	}
	return token, nil
}

func persistRefreshedAPIKey(ctx context.Context, dbPath string, token string) error {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return errors.New("trackers: RTF persist refreshed API key: db path not configured")
	}
	repo, err := servicedb.OpenContext(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("trackers: RTF persist refreshed API key open db: %w", err)
	}
	defer repo.Close()
	cfg, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		return fmt.Errorf("trackers: RTF persist refreshed API key load config: %w", err)
	}
	if cfg.Trackers.Trackers == nil {
		cfg.Trackers.Trackers = map[string]config.TrackerConfig{}
	}
	trackerKey := "RTF"
	for key := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(key), "RTF") {
			trackerKey = key
			break
		}
	}
	trackerCfg := cfg.Trackers.Trackers[trackerKey]
	trackerCfg.APIKey = strings.TrimSpace(token)
	cfg.Trackers.Trackers[trackerKey] = trackerCfg
	if err := config.SaveToDatabase(ctx, cfg, repo); err != nil {
		return fmt.Errorf("trackers: RTF persist refreshed API key: %w", err)
	}
	return nil
}

func resolveBaseURL(cfg config.TrackerConfig) string {
	if value := strings.TrimRight(strings.TrimSpace(cfg.URL), "/"); value != "" {
		return value
	}
	return defaultBaseURL
}

func joinURL(baseURL string, path string) string {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(defaultBaseURL, "/") + path
	}
	ref, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil {
		return strings.TrimRight(baseURL, "/") + path
	}
	return parsed.ResolveReference(ref).String()
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
		if raw := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL)); raw != "" {
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
	return metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.Poster)
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}

func genresText(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.Genres, meta.Release.Genre)
	}
	return metautil.FirstNonEmptyTrimmed(meta.Release.Genre)
}

func keywordsText(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB == nil {
		return ""
	}
	return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
}
