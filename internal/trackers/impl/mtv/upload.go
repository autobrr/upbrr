// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP interoperability requires SHA-1.
	"encoding/base32"
	"encoding/binary"
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

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	descriptionmtv "github.com/autobrr/upbrr/internal/services/description/mtv"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	mtvBaseURL      = "https://www.morethantv.me"
	mtvUploadPath   = "/upload.php"
	mtvIndexPath    = "/index.php"
	mtvUserAgentWeb = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
)

var mtvAuthKeyPattern = regexp.MustCompile(`authkey=([a-zA-Z0-9]{32})`)
var mtvTokenPattern = regexp.MustCompile(`name="token"\s+value="([^"]{16,128})"`)

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	select {
	case <-ctx.Done():
		return api.UploadSummary{}, ctx.Err()
	default:
	}

	baseURL := strings.TrimRight(strings.TrimSpace(req.TrackerConfig.URL), "/")
	if baseURL == "" {
		baseURL = mtvBaseURL
	}
	uploadURL := baseURL + mtvUploadPath

	cookies, err := loadMTVCookies(ctx, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		cookies = nil
	}
	auth, client, err := resolveAuthKey(ctx, baseURL, cookies)
	if err != nil {
		if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
			if cookies == nil {
				return api.UploadSummary{}, errors.New("trackers: MTV cookie invalid/missing and username/password not configured")
			}
			return api.UploadSummary{}, err
		}
		auth, client, cookies, err = loginAndResolveAuthKey(ctx, req.TrackerConfig, baseURL)
		if err != nil {
			return api.UploadSummary{}, err
		}
		if persistErr := saveMTVCookies(ctx, req.AppConfig.MainSettings.DBPath, cookies); persistErr != nil && req.Logger != nil {
			req.Logger.Warnf("trackers: MTV failed to persist cookies: %v", persistErr)
		}
	}

	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
		if err != nil {
			trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	descText, err := descriptionmtv.BuildDescription(ctx, req.Meta, req.AppConfig, assets.Description, assets.Screenshots)
	if err != nil {
		return api.UploadSummary{}, err
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return api.UploadSummary{}, err
	}

	fields := buildUploadFields(req, auth, descText)
	body, contentType, err := buildMultipartPayload(fields, torrentPath)
	if err != nil {
		return api.UploadSummary{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: MTV request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", mtvUserAgentWeb)

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: MTV upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusBadRequest {
		preview := string(bodyPreview)
		if strings.Contains(preview, "Request Header") || strings.Contains(preview, "Cookie Too Large") || strings.Contains(preview, "Header Too Large") {
			return api.UploadSummary{}, errors.New("trackers: MTV data error (request header/cookie too large)")
		}
	}

	if strings.Contains(finalURL, "torrents.php") {
		return api.UploadSummary{Uploaded: 1}, nil
	}

	return api.UploadSummary{}, fmt.Errorf("trackers: MTV upload failed status=%d url=%s body=%s", resp.StatusCode, finalURL, strings.TrimSpace(string(bodyPreview)))
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	select {
	case <-ctx.Done():
		return api.TrackerDryRunEntry{}, ctx.Err()
	default:
	}

	var err error
	assets := trackers.DescriptionAssets{}
	if req.Assets != nil {
		assets = *req.Assets
	} else {
		assets, err = trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
		if err != nil {
			trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
			assets = trackers.DescriptionAssets{}
		}
	}
	descText, err := descriptionmtv.BuildDescription(ctx, req.Meta, req.AppConfig, assets.Description, assets.Screenshots)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	baseURL := strings.TrimRight(strings.TrimSpace(req.TrackerConfig.URL), "/")
	if baseURL == "" {
		baseURL = mtvBaseURL
	}
	uploadURL := baseURL + mtvUploadPath

	const dryRunAuthPlaceholder = "dry-run-auth"
	fields := buildUploadFields(req, dryRunAuthPlaceholder, descText)

	return api.TrackerDryRunEntry{
		Tracker:          "MTV",
		Status:           "ready",
		Message:          "dry-run payload generated (auth placeholder used)",
		ReleaseName:      resolveUploadName(req.Meta),
		DescriptionGroup: "mtv",
		Description:      descText,
		Endpoint:         uploadURL,
		Payload:          fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    torrentPath,
			Present: strings.TrimSpace(torrentPath) != "",
		}},
	}, nil
}

func resolveAuthKey(ctx context.Context, baseURL string, cookies map[string]string) (string, *http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV create auth cookie jar: %w", err)
	}
	parsedBase, err := url.Parse(baseURL)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV parse base URL: %w", err)
	}
	jarCookies := make([]*http.Cookie, 0, len(cookies))
	for name, value := range cookies {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: value, Path: "/", Domain: parsedBase.Hostname()})
	}
	jar.SetCookies(parsedBase, jarCookies)

	client := &http.Client{Timeout: 20 * time.Second, Jar: jar}
	indexURL := strings.TrimRight(baseURL, "/") + mtvIndexPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV build auth request: %w", err)
	}
	req.Header.Set("User-Agent", mtvUserAgentWeb)

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV auth request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("trackers: MTV auth status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV read auth response: %w", err)
	}
	match := mtvAuthKeyPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", nil, errors.New("trackers: MTV auth key not found")
	}
	return strings.TrimSpace(match[1]), client, nil
}

func loadMTVCookies(ctx context.Context, dbPath string) (map[string]string, error) {
	return cookiepkg.LoadTrackerCookieMap(ctx, dbPath, "MTV")
}

func saveMTVCookies(ctx context.Context, dbPath string, values map[string]string) error {
	return cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "MTV", values)
}

func loginAndResolveAuthKey(ctx context.Context, cfg config.TrackerConfig, baseURL string) (string, *http.Client, map[string]string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", nil, nil, fmt.Errorf("trackers: MTV create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 25 * time.Second, Jar: jar}

	loginURL := strings.TrimRight(baseURL, "/") + "/login"
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return "", nil, nil, fmt.Errorf("trackers: MTV build login page request: %w", err)
	}
	loginReq.Header.Set("User-Agent", mtvUserAgentWeb)
	loginResp, err := client.Do(loginReq)
	if err != nil {
		return "", nil, nil, fmt.Errorf("trackers: MTV login page request: %w", err)
	}
	loginBody, _ := io.ReadAll(loginResp.Body)
	_ = loginResp.Body.Close()
	match := mtvTokenPattern.FindStringSubmatch(string(loginBody))
	if len(match) < 2 {
		return "", nil, nil, errors.New("trackers: MTV login token not found")
	}
	token := strings.TrimSpace(match[1])

	form := url.Values{}
	form.Set("username", strings.TrimSpace(cfg.Username))
	form.Set("password", strings.TrimSpace(cfg.Password))
	form.Set("keeploggedin", "1")
	form.Set("cinfo", "1920|1080|24|0")
	form.Set("submit", "login")
	form.Set("iplocked", "1")
	form.Set("token", token)

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", nil, nil, fmt.Errorf("trackers: MTV build login request: %w", err)
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("User-Agent", mtvUserAgentWeb)
	postResp, err := client.Do(postReq)
	if err != nil {
		return "", nil, nil, fmt.Errorf("trackers: MTV login request: %w", err)
	}
	body, _ := io.ReadAll(postResp.Body)
	_ = postResp.Body.Close()

	if postResp.Request != nil && postResp.Request.URL != nil && strings.Contains(postResp.Request.URL.Path, "/twofactor/login") {
		twoFactorTokenMatch := mtvTokenPattern.FindStringSubmatch(string(body))
		if len(twoFactorTokenMatch) < 2 {
			return "", nil, nil, errors.New("trackers: MTV 2FA token not found")
		}
		code, err := totpFromOTPURI(strings.TrimSpace(cfg.OTPURI), time.Now())
		if err != nil {
			return "", nil, nil, fmt.Errorf("trackers: MTV 2FA required but otp_uri invalid: %w", err)
		}
		twoFactorForm := url.Values{}
		twoFactorForm.Set("token", twoFactorTokenMatch[1])
		twoFactorForm.Set("code", code)
		twoFactorForm.Set("submit", "login")
		twoReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/twofactor/login", strings.NewReader(twoFactorForm.Encode()))
		if err != nil {
			return "", nil, nil, fmt.Errorf("trackers: MTV build 2FA login request: %w", err)
		}
		twoReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		twoReq.Header.Set("User-Agent", mtvUserAgentWeb)
		twoResp, err := client.Do(twoReq)
		if err != nil {
			return "", nil, nil, fmt.Errorf("trackers: MTV 2FA login request: %w", err)
		}
		_, _ = io.ReadAll(twoResp.Body)
		_ = twoResp.Body.Close()
	}

	auth, authedClient, err := resolveAuthKeyFromClient(ctx, baseURL, client)
	if err != nil {
		return "", nil, nil, err
	}
	cookieMap := cookiesFromJar(baseURL, authedClient.Jar)
	return auth, authedClient, cookieMap, nil
}

func resolveAuthKeyFromClient(ctx context.Context, baseURL string, client *http.Client) (string, *http.Client, error) {
	indexURL := strings.TrimRight(baseURL, "/") + mtvIndexPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV build auth request: %w", err)
	}
	req.Header.Set("User-Agent", mtvUserAgentWeb)
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV auth request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("trackers: MTV read auth response: %w", err)
	}
	match := mtvAuthKeyPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", nil, errors.New("trackers: MTV auth key not found")
	}
	return strings.TrimSpace(match[1]), client, nil
}

func cookiesFromJar(baseURL string, jar http.CookieJar) map[string]string {
	if jar == nil {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	for _, cookie := range jar.Cookies(parsed) {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		out[strings.TrimSpace(cookie.Name)] = strings.TrimSpace(cookie.Value)
	}
	return out
}

func totpFromOTPURI(otpURI string, now time.Time) (string, error) {
	trimmed := strings.TrimSpace(otpURI)
	if trimmed == "" {
		return "", errors.New("empty otp_uri")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: MTV parse otp_uri: %w", err)
	}
	query := parsed.Query()
	secret := strings.TrimSpace(query.Get("secret"))
	if secret == "" {
		return "", errors.New("missing secret query parameter")
	}
	interval := 30
	if period := strings.TrimSpace(query.Get("period")); period != "" {
		if value, err := strconv.Atoi(period); err == nil && value > 0 {
			interval = value
		}
	}
	counterTime := now.Unix() / int64(interval)
	if counterTime < 0 {
		return "", errors.New("totp counter before unix epoch")
	}
	counter := uint64(counterTime)

	decoder := base32.StdEncoding.WithPadding(base32.NoPadding)
	secretBytes, err := decoder.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, secretBytes)
	_, _ = mac.Write(buf)
	hash := mac.Sum(nil)
	offset := hash[len(hash)-1] & 0x0f
	code := (int(hash[offset])&0x7f)<<24 | int(hash[offset+1])<<16 | int(hash[offset+2])<<8 | int(hash[offset+3])
	return fmt.Sprintf("%06d", code%1000000), nil
}

func buildUploadFields(req trackers.UploadRequest, auth string, description string) map[string]string {
	meta := req.Meta
	anon := "1"
	if !req.TrackerConfig.Anon {
		anon = "0"
	}
	return map[string]string{
		"image":               "",
		"title":               resolveUploadName(meta),
		"category":            resolveCategoryID(meta),
		"Resolution":          resolveResolutionID(meta),
		"source":              resolveSourceID(meta),
		"origin":              resolveOriginID(meta),
		"taglist":             resolveTags(meta),
		"desc":                strings.TrimSpace(description),
		"groupDesc":           resolveGroupDescription(meta),
		"ignoredupes":         "1",
		"genre_tags":          "---",
		"autocomplete_toggle": "on",
		"fontfont":            "-1",
		"fontsize":            "-1",
		"auth":                auth,
		"anonymous":           anon,
		"submit":              "true",
	}
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", err
		}
	}
	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: MTV open torrent file: %w", err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile("file_input", "[MTV].torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: MTV copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveTorrentPath(meta api.PreparedMetadata, dbPath string) (string, error) {
	candidates := []string{
		strings.TrimSpace(meta.TorrentPath),
		strings.TrimSpace(meta.ClientTorrentPath),
		strings.TrimSpace(meta.SourcePath),
	}
	for _, candidate := range candidates {
		if candidate == "" || !strings.EqualFold(filepath.Ext(candidate), ".torrent") {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if strings.TrimSpace(dbPath) != "" && strings.TrimSpace(meta.SourcePath) != "" {
		tmpRoot, err := db.Subdir(dbPath, "tmp")
		if err == nil {
			tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
			if err == nil {
				guessed := filepath.Join(tmpDir, base+".torrent")
				if info, err := os.Stat(guessed); err == nil && !info.IsDir() {
					return guessed, nil
				}
			}
		}
	}
	return "", errors.New("trackers: MTV torrent file not found")
}

func resolveUploadName(meta api.PreparedMetadata) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return cleanName(name)
	}
	if name := strings.TrimSpace(meta.ReleaseNameNoTag); name != "" {
		return cleanName(name)
	}
	if name := strings.TrimSpace(meta.Filename); name != "" {
		return cleanName(name)
	}
	return cleanName(pathutil.Base(meta.SourcePath))
}

func cleanName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, " ", ".")
	value = strings.ReplaceAll(value, "..", ".")
	return strings.TrimSpace(value)
}

func resolveResolution(meta api.PreparedMetadata) string {
	if value := strings.TrimSpace(meta.Release.Resolution); value != "" {
		return value
	}
	if strings.TrimSpace(meta.UHD) != "" {
		return "2160p"
	}
	return ""
}

func resolveResolutionID(meta api.PreparedMetadata) string {
	res := strings.ToLower(strings.TrimSpace(resolveResolution(meta)))
	values := map[string]string{
		"8640p": "0", "4320p": "4000", "2160p": "2160", "1440p": "1440", "1080p": "1080", "1080i": "1080", "720p": "720", "576p": "0", "576i": "0", "480p": "480", "480i": "480",
	}
	if value, ok := values[res]; ok {
		return value
	}
	return "10"
}

func isSD(meta api.PreparedMetadata) bool {
	res := strings.ToLower(resolveResolution(meta))
	return strings.Contains(res, "480") || strings.Contains(res, "576")
}

func resolveCategory(meta api.PreparedMetadata) string {
	category := strings.ToUpper(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "" {
		category = strings.ToUpper(strings.TrimSpace(meta.MediaInfoCategory))
	}
	return category
}

func resolveCategoryID(meta api.PreparedMetadata) string {
	category := resolveCategory(meta)
	if category == "MOVIE" {
		if isSD(meta) {
			return "2"
		}
		return "1"
	}
	if category == "TV" {
		if meta.TVPack {
			if isSD(meta) {
				return "6"
			}
			return "5"
		}
		if isSD(meta) {
			return "4"
		}
		return "3"
	}
	return "0"
}

func resolveType(meta api.PreparedMetadata) string {
	value := strings.ToUpper(strings.TrimSpace(meta.Type))
	if value == "" {
		value = strings.ToUpper(strings.TrimSpace(meta.Release.Type))
	}
	return strings.ReplaceAll(strings.ReplaceAll(value, "-", ""), " ", "")
}

func resolveSourceID(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return "1"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || resolveType(meta) == "REMUX" {
		return "7"
	}
	mapping := map[string]string{
		"DISC": "1", "WEBDL": "9", "WEBRIP": "10", "HDTV": "1", "SDTV": "2", "TVRIP": "3", "DVD": "4", "DVDRIP": "5", "BDRIP": "8", "VHS": "6", "MIXED": "11", "ENCODE": "7",
	}
	if value, ok := mapping[resolveType(meta)]; ok {
		return value
	}
	return "0"
}

func resolveOriginID(meta api.PreparedMetadata) string {
	if meta.PersonalRelease {
		return "4"
	}
	if meta.Scene {
		return "2"
	}
	return "3"
}

func resolveTags(meta api.PreparedMetadata) string {
	tags := make([]string, 0, 12)
	resolution := strings.ToLower(strings.TrimSpace(resolveResolution(meta)))
	if resolution != "" {
		tags = append(tags, resolution)
	}
	switch {
	case isSD(meta):
		tags = append(tags, "sd")
	case resolution == "2160p" || resolution == "4320p":
		tags = append(tags, "uhd")
	default:
		tags = append(tags, "hd")
	}
	if service := strings.TrimSpace(meta.ServiceLongName); service != "" {
		svc := strings.ToLower(strings.ReplaceAll(service, " ", "."))
		svc = strings.ReplaceAll(svc, "+", "plus")
		tags = append(tags, svc+".source")
	}
	switch category := resolveCategory(meta); category {
	case "TV":
		switch {
		case meta.TVPack && isSD(meta):
			tags = append(tags, "sd.season")
		case meta.TVPack:
			tags = append(tags, "hd.season")
		case isSD(meta):
			tags = append(tags, "sd.episode")
		default:
			tags = append(tags, "hd.episode")
		}
	case "MOVIE":
		if isSD(meta) {
			tags = append(tags, "sd.movie")
		} else {
			tags = append(tags, "hd.movie")
		}
	}
	audio := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(meta.Audio, "+", "p"), "-", "."), " ", "."))
	for _, token := range []string{"dd", "ddp", "aac", "truehd", "mp3", "mp2", "dts", "dts.hd", "dts.x"} {
		if strings.Contains(audio, token) {
			tags = append(tags, token+".audio")
			break
		}
	}
	if strings.Contains(strings.ToLower(meta.Audio), "atmos") {
		tags = append(tags, "atmos.audio")
	}
	codec := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(meta.VideoCodec), "avc", "h264"), "hevc", "h265")
	codec = strings.ReplaceAll(codec, "-", "")
	if strings.TrimSpace(codec) != "" {
		tags = append(tags, strings.TrimSpace(codec))
	}
	if tag := strings.TrimSpace(meta.Tag); tag != "" {
		tags = append(tags, strings.TrimPrefix(strings.ReplaceAll(tag, " ", "."), "-")+".release")
	} else {
		tags = append(tags, "NOGRP.release")
	}
	if meta.Scene {
		tags = append(tags, "scene.group.release")
	} else {
		tags = append(tags, "p2p.group.release")
	}
	return strings.Join(tags, " ")
}

func resolveGroupDescription(meta api.PreparedMetadata) string {
	parts := make([]string, 0, 5)
	if meta.ExternalMetadata.IMDB != nil {
		if imdbURL := strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbURL); imdbURL != "" {
			parts = append(parts, imdbURL)
		}
	}
	if meta.ExternalIDs.TMDBID != 0 {
		category := strings.ToLower(strings.TrimSpace(resolveCategory(meta)))
		if category == "" {
			category = "movie"
		}
		parts = append(parts, "https://www.themoviedb.org/"+category+"/"+strconv.Itoa(meta.ExternalIDs.TMDBID))
	}
	if meta.ExternalIDs.TVDBID != 0 {
		parts = append(parts, "https://www.thetvdb.com/?id="+strconv.Itoa(meta.ExternalIDs.TVDBID))
	}
	if meta.ExternalIDs.TVmazeID != 0 {
		parts = append(parts, "https://www.tvmaze.com/shows/"+strconv.Itoa(meta.ExternalIDs.TVmazeID))
	}
	if meta.MALID != 0 {
		parts = append(parts, "https://myanimelist.net/anime/"+strconv.Itoa(meta.MALID))
	}
	return strings.Join(parts, "\n")
}
