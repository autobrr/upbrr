// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	arBaseURL    = "https://alpharatio.cc"
	arUploadURL  = arBaseURL + "/upload.php"
	arLoginURL   = arBaseURL + "/login.php"
	arBrowseURL  = arBaseURL + "/torrents.php"
	arAuthFile   = "AR_auth.txt"
	arUserAgent  = "upbrr"
	arSourceFlag = "AlphaRatio"
)

var (
	arLoginFailurePattern = regexp.MustCompile(`login\.php\?act=recover|Forgot your password`)
	arURLPattern          = regexp.MustCompile(`torrents\.php\?id=(\d+)(?:&torrentid=(\d+))?`)
	arDownloadPattern     = regexp.MustCompile(`torrents\.php\?action=download&id=(\d+)`)
)

type uploadState struct {
	torrentPath   string
	description   string
	fields        map[string]string
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, client, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: AR %s", state.blockedReason)
	}

	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath)
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, arUploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: AR request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", arUserAgent)

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: AR upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	groupID, torrentID := parseUploadIDs(finalURL, string(bodyBytes))
	if resp.StatusCode == http.StatusOK && groupID != "" {
		torrentURL := buildTorrentURL(groupID, torrentID)
		downloadURL := buildDownloadURL(torrentID, torrentURL)
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "AR")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, torrentURL, arSourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		id := firstNonEmpty(torrentID, groupID)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "AR",
				TorrentID:   id,
				DownloadURL: downloadURL,
				TorrentURL:  torrentURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	failurePath := ""
	if pathValue, pathErr := resolveFailurePath(req.Meta, req.AppConfig.MainSettings.DBPath); pathErr == nil {
		failurePath = pathValue
		_ = os.WriteFile(failurePath, bodyBytes, 0o600)
	}
	if failurePath != "" {
		return api.UploadSummary{}, fmt.Errorf("%w failure=%s", commonhttp.UploadHTTPErrorWithURL("AR", resp.StatusCode, finalURL, bodyBytes), failurePath)
	}
	return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("AR", resp.StatusCode, finalURL, bodyBytes)
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, _, err := prepareUploadState(ctx, req, true)
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
		Tracker:          "AR",
		Status:           status,
		Message:          message,
		ReleaseName:      resolveARName(req.Meta),
		DescriptionGroup: "ar",
		Description:      state.description,
		Endpoint:         arUploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, dryRun bool) (uploadState, *http.Client, error) {
	select {
	case <-ctx.Done():
		return uploadState{}, nil, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
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
	description := buildDescription(req.Meta, req.AppConfig.MainSettings.DBPath, assets)

	fields := map[string]string{
		"submit": "true",
		"type":   resolveTypeID(req.Meta),
		"title":  resolveARName(req.Meta),
		"tags":   resolveTags(req.Meta),
		"image":  resolvePoster(req.Meta),
		"desc":   description,
	}
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		fields:      fields,
	}
	if strings.TrimSpace(fields["image"]) == "" {
		state.blockedReason = "missing poster URL"
	}

	if dryRun {
		if authProblem := dryRunAuthProblem(ctx, req.TrackerConfig, req.AppConfig.MainSettings.DBPath); authProblem != "" && state.blockedReason == "" {
			state.blockedReason = authProblem
		}
		fields["auth"] = "dry-run-auth"
		return state, nil, nil
	}

	client, authKey, err := resolveSession(ctx, req.TrackerConfig, req.AppConfig.MainSettings.DBPath, req.Logger)
	if err != nil {
		return uploadState{}, nil, err
	}
	fields["auth"] = authKey
	return state, client, nil
}

func buildDescription(meta api.PreparedMetadata, dbPath string, assets trackers.DescriptionAssets) string {
	var parts []string
	title := firstNonEmpty(strings.TrimSpace(meta.ReleaseName), strings.TrimSpace(meta.Release.Title), pathutil.Base(meta.SourcePath))
	parts = append(parts, fmt.Sprintf("[color=green][size=6]%s[/size][/color]", title))

	if links := buildDatabaseLinks(meta); links != "" {
		parts = append(parts, "[color=red][size=4]Links[/size][/color]\n"+links)
	}

	mediaLabel := "MEDIAINFO"
	var mediaText string
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		mediaLabel = "BDINFO"
		mediaText, _ = readBDSummary(meta, dbPath)
	case "DVD":
		mediaText = firstNonEmpty(strings.TrimSpace(meta.DVDVOBMediaInfoText), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
	default:
		mediaText = readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath))
	}
	if strings.TrimSpace(mediaText) != "" {
		parts = append(parts, fmt.Sprintf("[color=red][size=4]%s[/size][/color]\n[hide][code]%s[/code][/hide]", mediaLabel, strings.TrimSpace(mediaText)))
	}

	if overview := resolveOverview(meta); overview != "" {
		parts = append(parts, "[color=red][size=4]PLOT[/size][/color]\n"+overview)
	}
	if genres := resolveGenres(meta); genres != "" {
		parts = append(parts, "[color=red][size=4]Genres[/size][/color]\n"+genres)
	}
	if screenshots := buildScreenshotSection(assets.Screenshots); screenshots != "" {
		parts = append(parts, "[color=red][size=4]Screenshots[/size][/color]\n"+screenshots)
	}
	if youtube := resolveYouTube(meta); youtube != "" {
		parts = append(parts, "[color=red][size=4]Youtube[/size][/color]\n"+youtube)
	}
	if notes := cleanNotes(assets.Description); notes != "" {
		parts = append(parts, "[color=red][size=4]Notes[/size][/color]\n"+notes)
	}

	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func buildDatabaseLinks(meta api.PreparedMetadata) string {
	links := make([]string, 0, 5)
	if imdb := resolveIMDbURL(meta); imdb != "" {
		links = append(links, imdb)
	}
	category := strings.ToLower(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "" {
		category = "movie"
	}
	if id := meta.ExternalIDs.TMDBID; id > 0 {
		links = append(links, fmt.Sprintf("https://www.themoviedb.org/%s/%d", category, id))
	}
	if id := meta.ExternalIDs.TVDBID; id > 0 {
		links = append(links, fmt.Sprintf("https://www.thetvdb.com/?id=%d&tab=series", id))
	}
	if id := meta.ExternalIDs.TVmazeID; id > 0 {
		links = append(links, fmt.Sprintf("https://www.tvmaze.com/shows/%d", id))
	}
	if meta.MALID > 0 {
		links = append(links, fmt.Sprintf("https://myanimelist.net/anime/%d", meta.MALID))
	}
	return strings.Join(links, "\n")
}

func buildScreenshotSection(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := make([]string, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.RawURL) == "" || strings.TrimSpace(image.ImgURL) == "" {
			continue
		}
		parts = append(parts, "[url="+image.RawURL+"][img]"+image.ImgURL+"[/img][/url]")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[align=center]" + strings.Join(parts, "") + "[/align]"
}

func cleanNotes(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	replacer := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	trimmed = replacer.Replace(trimmed)
	sceneBlocks := []*regexp.Regexp{
		regexp.MustCompile(`(?is)\[center\]\[spoiler=Scene NFO:\].*?\[/center\]`),
		regexp.MustCompile(`(?is)\[center\]\[spoiler=FraMeSToR NFO:\].*?\[/center\]`),
		regexp.MustCompile(`(?is)\[color=red\]\[size=4\]Screenshots\[/size\]\[/color\]\s*\[align=center\].*?\[/align\]`),
		regexp.MustCompile(`(?is)\[(?:right|align=right)\]\s*\[url=https://github\.com/(?:Audionut|autobrr)/upbrr\].*?\[/url\]\s*\[/(?:right|align)\]`),
	}
	for _, pattern := range sceneBlocks {
		trimmed = pattern.ReplaceAllString(trimmed, "")
	}
	return strings.TrimSpace(trimmed)
}

func resolveTypeID(meta api.PreparedMetadata) string {
	genres := strings.ToLower(resolveGenres(meta) + " " + resolveKeywords(meta))
	adultKeywords := []string{"xxx", "erotic", "porn", "adult", "orgy"}
	if (strings.EqualFold(meta.Type, "DISC") || strings.EqualFold(meta.Type, "REMUX")) && strings.EqualFold(meta.Source, "Blu-ray") {
		return "14"
	}
	if meta.Anime {
		if isSD(meta.Release.Resolution) {
			return "15"
		}
		return "16"
	}
	if strings.EqualFold(meta.ExternalIDs.Category, "TV") {
		if meta.TVPack {
			if isSD(meta.Release.Resolution) {
				return "4"
			}
			if isHighTier(meta.Release.Resolution) {
				return "6"
			}
			return "5"
		}
		if isSD(meta.Release.Resolution) {
			return "0"
		}
		if isHighTier(meta.Release.Resolution) {
			return "2"
		}
		return "1"
	}
	if isSD(meta.Release.Resolution) {
		return "7"
	}
	for _, keyword := range adultKeywords {
		if strings.Contains(genres, keyword) {
			return "13"
		}
	}
	if isHighTier(meta.Release.Resolution) {
		return "9"
	}
	return "8"
}

func resolveARName(meta api.PreparedMetadata) string {
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName)
	}
	name := paths.ReleaseTempBase(meta, meta.SourcePath)
	if ext := strings.TrimSpace(filepath.Ext(name)); ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	replacer := strings.NewReplacer(
		"_", ".", " ", ".", "'", "", ":", "", "(", ".", ")", ".", "[", ".", "]", ".", "{", ".", "}", ".",
	)
	name = replacer.Replace(strings.TrimSpace(name))
	name = collapseDots(name)

	tagLower := strings.ToLower(strings.TrimSpace(meta.Tag))
	invalidTags := []string{"nogrp", "nogroup", "unknown", "-unk-"}
	if tagLower == "" || containsAny(tagLower, invalidTags) {
		for _, invalid := range invalidTags {
			name = regexp.MustCompile(`(?i)-?`+regexp.QuoteMeta(invalid)).ReplaceAllString(name, "")
		}
		name = strings.Trim(strings.TrimSpace(name), ".-")
		if name == "" {
			name = "release"
		}
		return name + "-NoGRP"
	}
	return name
}

func resolveTags(meta api.PreparedMetadata) string {
	values := make([]string, 0, 8)
	if meta.ExternalIDs.IMDBID > 0 {
		values = append(values, "tt"+strconv.Itoa(meta.ExternalIDs.IMDBID))
	}
	for _, value := range strings.Split(resolveGenres(meta), ",") {
		for _, sub := range strings.Split(value, "&") {
			tag := strings.TrimSpace(collapseDots(sub))
			if tag == "" {
				continue
			}
			values = append(values, tag)
		}
	}
	return strings.Join(uniqueStrings(values), ", ")
}

func resolvePoster(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster)
	}
	if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover)
	}
	if meta.ExternalMetadata.TVDB != nil && strings.TrimSpace(meta.ExternalMetadata.TVDB.Poster) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.TVDB.Poster)
	}
	if meta.ExternalMetadata.TVmaze != nil && strings.TrimSpace(meta.ExternalMetadata.TVmaze.Poster) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.TVmaze.Poster)
	}
	return ""
}

func resolveOverview(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.Overview) != "":
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Overview)
	case meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.Plot) != "":
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Plot)
	case meta.ExternalMetadata.TVDB != nil && strings.TrimSpace(meta.ExternalMetadata.TVDB.Overview) != "":
		return strings.TrimSpace(meta.ExternalMetadata.TVDB.Overview)
	case meta.ExternalMetadata.TVmaze != nil && strings.TrimSpace(meta.ExternalMetadata.TVmaze.Summary) != "":
		return strings.TrimSpace(meta.ExternalMetadata.TVmaze.Summary)
	default:
		return strings.TrimSpace(meta.EpisodeOverview)
	}
}

func resolveGenres(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres) != "":
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres)
	case meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres) != "":
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres)
	case meta.ExternalMetadata.TVDB != nil && strings.TrimSpace(meta.ExternalMetadata.TVDB.Genres) != "":
		return strings.TrimSpace(meta.ExternalMetadata.TVDB.Genres)
	case meta.ExternalMetadata.TVmaze != nil && strings.TrimSpace(meta.ExternalMetadata.TVmaze.Genres) != "":
		return strings.TrimSpace(meta.ExternalMetadata.TVmaze.Genres)
	default:
		return strings.TrimSpace(meta.Release.Genre)
	}
}

func resolveKeywords(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
	}
	return ""
}

func resolveYouTube(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveIMDbURL(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbURL) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbURL)
	}
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.ExternalIDs.IMDBID)
	}
	return ""
}

func dryRunAuthProblem(ctx context.Context, cfg config.TrackerConfig, dbPath string) string {
	if _, err := os.Stat(authPath(dbPath)); err == nil {
		return ""
	}
	if cookies, err := loadCookies(ctx, dbPath); err == nil && len(cookies) > 0 {
		return ""
	}
	if strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != "" {
		return ""
	}
	return "missing valid AR cookies/auth and username/password are not configured"
}

func resolveSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, logger api.Logger) (*http.Client, string, error) {
	if logger == nil {
		logger = api.NopLogger{}
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: AR create cookie jar: %w", err)
	}
	base, _ := url.Parse(arBaseURL + "/")
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	if cookies, err := loadCookies(ctx, dbPath); err == nil && len(cookies) > 0 {
		jar.SetCookies(base, cookies)
		authKey, valid, authErr := validateSession(ctx, client, dbPath)
		if authErr == nil && valid {
			return client, authKey, nil
		}
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, "", errors.New("trackers: AR cookie invalid/missing and username/password not configured")
	}
	authKey, err := login(ctx, client, cfg, dbPath)
	if err != nil {
		return nil, "", err
	}
	if err := persistLoginCookies(ctx, dbPath, logger, jar.Cookies(base)); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(authPath(dbPath), []byte(authKey), 0o600); err != nil {
		return nil, "", fmt.Errorf("trackers: AR write auth key: %w", err)
	}
	return client, authKey, nil
}

func persistLoginCookies(ctx context.Context, dbPath string, logger api.Logger, values []*http.Cookie) error {
	if err := saveCookies(ctx, dbPath, values); err != nil {
		if errors.Is(err, cookiepkg.ErrAuthHelperUnavailable) {
			if logger == nil {
				logger = api.NopLogger{}
			}
			logger.Warnf("trackers: AR failed to persist cookies; continuing with plaintext fallback: %v", err)
			return nil
		}
		return err
	}
	return nil
}

func validateSession(ctx context.Context, client *http.Client, dbPath string) (string, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, arBrowseURL, nil)
	if err != nil {
		return "", false, fmt.Errorf("trackers: AR session validation request build: %w", err)
	}
	httpReq.Header.Set("User-Agent", arUserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", false, fmt.Errorf("trackers: AR session validation request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, fmt.Errorf("trackers: AR read session response: %w", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK || arLoginFailurePattern.MatchString(body) {
		return "", false, nil
	}
	if authKey := readAuthKey(dbPath); authKey != "" {
		return authKey, true, nil
	}
	authKey := extractAuthKey(body)
	if authKey == "" {
		return "", false, nil
	}
	if err := os.WriteFile(authPath(dbPath), []byte(authKey), 0o600); err != nil {
		return "", false, fmt.Errorf("trackers: AR write auth key: %w", err)
	}
	return authKey, true, nil
}

func login(ctx context.Context, client *http.Client, cfg config.TrackerConfig, dbPath string) (string, error) {
	values := url.Values{
		"username":   {strings.TrimSpace(cfg.Username)},
		"password":   {strings.TrimSpace(cfg.Password)},
		"keeplogged": {"1"},
		"login":      {"Login"},
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, arLoginURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", fmt.Errorf("trackers: AR login request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", arUserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: AR login request: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("trackers: AR read login response: %w", err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK || arLoginFailurePattern.MatchString(body) {
		return "", errors.New("trackers: AR login failed")
	}
	authKey := extractAuthKey(body)
	if authKey != "" {
		return authKey, nil
	}
	authKey, valid, err := validateSession(ctx, client, dbPath)
	if err != nil {
		return "", err
	}
	if !valid || authKey == "" {
		return "", errors.New("trackers: AR auth key not found after login")
	}
	return authKey, nil
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	file, err := os.Open(strings.TrimSpace(torrentPath))
	if err != nil {
		return nil, "", fmt.Errorf("trackers: AR open torrent file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", fmt.Errorf("trackers: AR write multipart field %q: %w", key, err)
		}
	}
	part, err := writer.CreateFormFile("file_input", filepath.Base(torrentPath))
	if err != nil {
		return nil, "", fmt.Errorf("trackers: AR create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", fmt.Errorf("trackers: AR copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: AR close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func parseUploadIDs(finalURL string, body string) (string, string) {
	if matches := arURLPattern.FindStringSubmatch(finalURL); len(matches) >= 2 {
		return strings.TrimSpace(matches[1]), strings.TrimSpace(matchAt(matches, 2))
	}
	if matches := arURLPattern.FindStringSubmatch(body); len(matches) >= 2 {
		return strings.TrimSpace(matches[1]), strings.TrimSpace(matchAt(matches, 2))
	}
	torrentID := ""
	if matches := arDownloadPattern.FindStringSubmatch(body); len(matches) == 2 {
		torrentID = strings.TrimSpace(matches[1])
	}
	return "", torrentID
}

func buildTorrentURL(groupID string, torrentID string) string {
	if groupID == "" {
		return ""
	}
	if torrentID != "" {
		return arBaseURL + "/torrents.php?id=" + url.QueryEscape(groupID) + "&torrentid=" + url.QueryEscape(torrentID)
	}
	return arBaseURL + "/torrents.php?id=" + url.QueryEscape(groupID)
}

func buildDownloadURL(torrentID string, fallback string) string {
	if torrentID == "" {
		return fallback
	}
	return arBaseURL + "/torrents.php?action=download&id=" + url.QueryEscape(torrentID)
}

func readBDSummary(meta api.PreparedMetadata, dbPath string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return readTextFile(paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta)))
}

func resolveFailurePath(meta api.PreparedMetadata, dbPath string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, "[AR]upload_failure.html"), nil
}

func authPath(dbPath string) string {
	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	path, err := db.CookiePath(dbPath, arAuthFile)
	if err != nil {
		return ""
	}
	return path
}

func readAuthKey(dbPath string) string {
	payload, err := os.ReadFile(authPath(dbPath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(payload))
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	return wrapTrackerResult(cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, "AR", "alpharatio.cc"))
}

func saveCookies(ctx context.Context, dbPath string, values []*http.Cookie) error {
	return wrapTrackerError(cookiepkg.SaveTrackerHTTPCookies(ctx, dbPath, "AR", values))
}

func extractAuthKey(body string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(body))
	for {
		//nolint:exhaustive // We only inspect tokens relevant to extracting the auth key.
		switch tokenizer.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if !strings.EqualFold(token.Data, "a") {
				continue
			}
			href := ""
			for _, attr := range token.Attr {
				if strings.EqualFold(attr.Key, "href") {
					href = attr.Val
					break
				}
			}
			if href == "" || !strings.Contains(href, "auth=") {
				continue
			}
			parsed, err := url.Parse(href)
			if err != nil {
				continue
			}
			authKey := strings.TrimSpace(parsed.Query().Get("auth"))
			if authKey != "" {
				return authKey
			}
		}
	}
}

func readTextFile(path string) (string, error) {
	payload, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("trackers: AR read text file: %w", err)
	}
	return string(payload), nil
}

func readTextFileNoErr(path string) string {
	payload, err := readTextFile(path)
	if err != nil {
		return ""
	}
	return payload
}

func isSD(resolution string) bool {
	value := strings.ToLower(strings.TrimSpace(resolution))
	return strings.Contains(value, "576") || strings.Contains(value, "480")
}

func isHighTier(resolution string) bool {
	value := strings.ToLower(strings.TrimSpace(resolution))
	return strings.Contains(value, "2160") || strings.Contains(value, "4320") || strings.Contains(value, "8640")
}

func collapseDots(value string) string {
	cleaned := strings.TrimSpace(value)
	for strings.Contains(cleaned, "..") {
		cleaned = strings.ReplaceAll(cleaned, "..", ".")
	}
	return strings.Trim(cleaned, ".")
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func matchAt(values []string, idx int) string {
	if idx < len(values) {
		return values[idx]
	}
	return ""
}
