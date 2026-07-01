// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP interoperability requires SHA-1.
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	btnDefaultBaseURL = "https://backup.landof.tv"
	btnUploadPath     = "/upload.php"
	btnLoginPath      = "/login.php"
	btnAPIRPCURL      = "https://api.broadcasthe.net/"
)

var (
	btnInputPattern        = regexp.MustCompile(`(?is)<input[^>]*name=["']([^"']+)["'][^>]*value=["']([^"']*)["'][^>]*>`)
	btnTextAreaPattern     = regexp.MustCompile(`(?is)<textarea[^>]*name=["']album_desc["'][^>]*>(.*?)</textarea>`)
	btnSelectPattern       = regexp.MustCompile(`(?is)<select[^>]*name=["']([^"']+)["'][^>]*>(.*?)</select>`)
	btnSelectedOptionRegex = regexp.MustCompile(`(?is)<option[^>]*selected[^>]*value=["']([^"']+)["']`)
	btnOptionValueRegex    = regexp.MustCompile(`(?is)<option[^>]*value=["']([^"']+)["']`)
	btnSuccessURLPattern   = regexp.MustCompile(`torrents\.php\?id=(\d+)(?:&torrentid=(\d+))?`)
)

// ErrSubmitted2FARejected marks a BTN failure after a submitted manual 2FA code
// reached the tracker and was rejected.
var ErrSubmitted2FARejected = errors.New("trackers: BTN submitted 2FA rejected")

var (
	errBTNCookiesMissing          = errors.New("trackers: BTN cookies not configured")
	errBTNSessionConfirmedInvalid = errors.New("trackers: BTN stored session confirmed invalid")
)

type uploadContext struct {
	baseURL   string
	uploadURL string
	apiToken  string
	apiURL    string
	client    *http.Client
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	if err := validateBTNRequest(req); err != nil {
		return api.UploadSummary{}, err
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return api.UploadSummary{}, err
	}

	uploadCtx, err := newUploadContext(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	client, err := ensureBTNUploadSession(ctx, req.TrackerConfig, req.AppConfig.MainSettings.DBPath, uploadCtx)
	if err != nil {
		return api.UploadSummary{}, err
	}
	uploadCtx.client = client

	data, err := prepareUploadData(ctx, req, uploadCtx)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if req.Logger != nil {
		if message := btnTVPayloadMetadataMessage(req.Meta); message != "" {
			req.Logger.Warnf("trackers: BTN %s", message)
		}
	}

	body, contentType, err := commonhttp.BuildMultipartPayload(data, []commonhttp.FileField{{FieldName: "file_input", Path: torrentPath, FileName: "torrent.torrent"}})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadCtx.uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BTN request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := uploadCtx.client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BTN upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	matches := btnSuccessURLPattern.FindStringSubmatch(finalURL)
	if len(matches) < 2 {
		responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(resp, resp.StatusCode >= 200 && resp.StatusCode < 400, 2048)
		if err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: BTN read upload response: %w", err)
		}
		matches = btnSuccessURLPattern.FindStringSubmatch(string(responseBody))
		if len(matches) < 2 {
			failurePath, _ := commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "BTN", "upload-failure", responsePreview, ".html")
			if failurePath != "" {
				return api.UploadSummary{}, fmt.Errorf("%w failure=%s", commonhttp.UploadHTTPErrorWithURL("BTN", resp.StatusCode, finalURL, responsePreview), failurePath)
			}
			return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("BTN", resp.StatusCode, finalURL, responsePreview)
		}
	}

	groupID := strings.TrimSpace(matches[1])
	torrentID := strings.TrimSpace(matches[2])
	if torrentID == "" {
		torrentID = groupID
	}
	torrentURL := strings.TrimRight(uploadCtx.baseURL, "/") + "/torrents.php?id=" + url.QueryEscape(groupID)
	if torrentID != "" {
		torrentURL += "&torrentid=" + url.QueryEscape(torrentID)
	}

	trackerTorrentPath, err := resolveTrackerTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath, "BTN")
	if err != nil {
		return api.UploadSummary{}, err
	}
	if err := downloadTrackerTorrent(ctx, uploadCtx.client, uploadCtx.baseURL, torrentID, trackerTorrentPath); err != nil {
		if req.Logger != nil {
			req.Logger.Warnf("trackers: BTN torrent download fallback to API search: %v", err)
		}
		if err := resolveAndDownloadViaAPI(ctx, uploadCtx.apiURL, uploadCtx.apiToken, req, groupID, trackerTorrentPath); err != nil {
			return api.UploadSummary{}, err
		}
	}

	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "BTN",
			TorrentID:   torrentID,
			TorrentURL:  torrentURL,
			DownloadURL: torrentURL,
			TorrentPath: trackerTorrentPath,
		}},
	}, nil
}

// buildUploadDryRun returns a BTN preview entry with the exact payload that
// would be submitted locally. TV payloads that would serialize missing
// canonical season or episode metadata as zero are returned as blocked so the
// operator sees the remediation before upload.
func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	if err := validateBTNRequest(req); err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	if err := validateBTNDryRunUploadAuth(ctx, req); err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	uploadCtx, err := newUploadContext(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	payload := map[string]string{
		"submit":       "true",
		"type":         resolveUploadType(req.Meta),
		"scenename":    resolveUploadName(req.Meta),
		"origin":       resolveOrigin(resolveUploadName(req.Meta)),
		"release_desc": strings.TrimSpace(req.Meta.DescriptionOverride),
		"tvdb":         "autofilled",
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	message := "dry-run payload generated"
	status := "ready"
	if metadataMessage := btnTVPayloadMetadataMessage(req.Meta); metadataMessage != "" {
		message += "; " + metadataMessage
		status = "blocked"
	}

	return api.TrackerDryRunEntry{
		Tracker:          "BTN",
		Status:           status,
		Message:          message,
		ReleaseName:      resolveUploadName(req.Meta),
		DescriptionGroup: "btn",
		Description:      payload["release_desc"],
		Endpoint:         uploadCtx.uploadURL,
		Payload:          payload,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    torrentPath,
			Present: strings.TrimSpace(torrentPath) != "",
		}},
	}, nil
}

// validateBTNDryRunUploadAuth checks only local auth prerequisites needed before
// an upload can authenticate. It does not perform remote login or persist cookies
// during dry-run, and it preserves storage/decrypt failures instead of treating
// them as missing cookies.
func validateBTNDryRunUploadAuth(ctx context.Context, req trackers.UploadRequest) error {
	values, cookieErr := loadBTNCookies(ctx, req.AppConfig.MainSettings.DBPath)
	if cookieErr == nil && len(values) > 0 {
		return nil
	}
	if cookieErr != nil && !errors.Is(cookieErr, errBTNCookiesMissing) {
		return cookieErr
	}
	if strings.TrimSpace(req.TrackerConfig.Username) == "" || strings.TrimSpace(req.TrackerConfig.Password) == "" {
		return errors.New("trackers: BTN cookie invalid/missing and username/password not configured")
	}
	return nil
}

func newUploadContext(ctx context.Context, req trackers.UploadRequest) (uploadContext, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return uploadContext{}, fmt.Errorf("trackers: BTN create cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 45 * time.Second, Jar: jar}
	baseURL := strings.TrimRight(strings.TrimSpace(req.TrackerConfig.URL), "/")
	if baseURL == "" {
		baseURL = btnDefaultBaseURL
	}
	uploadCtx := uploadContext{
		baseURL:   baseURL,
		uploadURL: baseURL + btnUploadPath,
		apiToken:  config.ResolveBTNAPIToken(req.AppConfig),
		apiURL:    resolveBTNAPIURL(req.TrackerConfig),
		client:    client,
	}
	loadBTNCookiesIntoJar(ctx, client, req.AppConfig.MainSettings.DBPath, baseURL)
	return uploadCtx, nil
}

// ensureBTNUploadSession validates imported BTN cookies before credential login.
// Credential login cookies are persisted only after the refreshed client reaches
// the upload page, so failed or incomplete logins do not replace stored auth.
func ensureBTNUploadSession(ctx context.Context, cfg config.TrackerConfig, dbPath string, uploadCtx uploadContext) (*http.Client, error) {
	values, cookieErr := loadBTNCookies(ctx, dbPath)
	if cookieErr == nil && len(values) > 0 {
		if client, err := newBTNClientWithCookies(uploadCtx.baseURL, values); err == nil {
			if err := validateBTNClientSession(ctx, client, uploadCtx.baseURL); err == nil {
				return client, nil
			} else if !errors.Is(err, errBTNSessionConfirmedInvalid) {
				return nil, err
			}
		}
	}
	if cookieErr != nil && !errors.Is(cookieErr, errBTNCookiesMissing) {
		return nil, cookieErr
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: BTN cookie invalid/missing and username/password not configured")
	}
	client, err := loginBTNSession(ctx, cfg, uploadCtx.baseURL, api.TrackerAuthLoginRequest{})
	if err != nil {
		return nil, err
	}
	if err := validateBTNClientSession(ctx, client, uploadCtx.baseURL); err != nil {
		return nil, err
	}
	if err := persistBTNCookies(ctx, dbPath, uploadCtx.baseURL, client.Jar); err != nil {
		return nil, fmt.Errorf("trackers: BTN persist cookies after successful login: %w", err)
	}
	return client, nil
}

// ResolveSessionForTrackerAuth validates BTN stored cookies or logs in with
// configured credentials. Credential login must produce reusable cookies before
// refreshed cookies are persisted.
func ResolveSessionForTrackerAuth(ctx context.Context, cfg config.TrackerConfig, dbPath string) error {
	return ResolveSessionForTrackerAuthLogin(ctx, cfg, dbPath, api.TrackerAuthLoginRequest{})
}

// ResolveSessionForTrackerAuthLogin validates BTN stored cookies or logs in
// with configured credentials. Refreshed cookies are persisted only after the
// upload page confirms the session. Manual login.Code is preferred over
// configured TOTP. Missing 2FA input preserves existing cookies; a rejected
// submitted code returns [ErrSubmitted2FARejected] before persistence.
func ResolveSessionForTrackerAuthLogin(ctx context.Context, cfg config.TrackerConfig, dbPath string, login api.TrackerAuthLoginRequest) error {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if baseURL == "" {
		baseURL = btnDefaultBaseURL
	}
	err := validateBTNStoredCookies(ctx, baseURL, dbPath)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errBTNCookiesMissing) && !errors.Is(err, errBTNSessionConfirmedInvalid) {
		return err
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		if errors.Is(err, errBTNSessionConfirmedInvalid) {
			return err
		}
		return errors.New("trackers: BTN cookie invalid/missing and username/password not configured")
	}
	client, err := loginBTNSession(ctx, cfg, baseURL, login)
	if err != nil {
		return err
	}
	if err := validateBTNClientSession(ctx, client, baseURL); err != nil {
		if strings.TrimSpace(login.Code) != "" && errors.Is(err, errBTNSessionConfirmedInvalid) {
			return fmt.Errorf("trackers: BTN submitted 2FA validation failed: %w", ErrSubmitted2FARejected)
		}
		return err
	}
	if err := persistBTNCookies(ctx, dbPath, baseURL, client.Jar); err != nil {
		return fmt.Errorf("trackers: BTN persist cookies after successful login: %w", err)
	}
	return nil
}

// validateBTNStoredCookies checks persisted BTN cookies against the upload page.
// Confirmed logged-out evidence is returned distinctly so tracker auth can delete
// stale cookies; storage/decrypt failures and ambiguous remote/parser failures
// preserve stored cookies and block credential login.
func validateBTNStoredCookies(ctx context.Context, baseURL string, dbPath string) error {
	values, err := loadBTNCookies(ctx, dbPath)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return errBTNCookiesMissing
	}
	client, err := newBTNClientWithCookies(baseURL, values)
	if err != nil {
		return err
	}
	return validateBTNClientSession(ctx, client, baseURL)
}

// loginBTNSession performs the credential login step and leaves cookie
// persistence to callers after they validate the resulting upload session.
func loginBTNSession(ctx context.Context, cfg config.TrackerConfig, baseURL string, login api.TrackerAuthLoginRequest) (*http.Client, error) {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: BTN requires username/password")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 45 * time.Second, Jar: jar}
	values := url.Values{}
	values.Set("username", strings.TrimSpace(cfg.Username))
	values.Set("password", strings.TrimSpace(cfg.Password))
	values.Set("keeplogged", "1")
	if code, err := resolveBTN2FACode(cfg, login); err == nil && code != "" {
		values.Set("codenumber", code)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+btnLoginPath, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN login request: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if readErr != nil {
		return nil, fmt.Errorf("trackers: BTN read login response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		if strings.TrimSpace(login.Code) != "" && resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("trackers: BTN login failed status=%d: %w", resp.StatusCode, ErrSubmitted2FARejected)
		}
		return nil, fmt.Errorf("trackers: BTN login failed status=%d", resp.StatusCode)
	}
	bodyText := string(body)
	if btnLoginNeeds2FA(bodyText) {
		if strings.TrimSpace(login.Code) != "" {
			return nil, fmt.Errorf("trackers: BTN login failed: %w", ErrSubmitted2FARejected)
		}
		if _, err := resolveBTN2FACode(config.TrackerConfig{OTPURI: cfg.OTPURI}, api.TrackerAuthLoginRequest{}); err != nil {
			return nil, fmt.Errorf("trackers: BTN 2FA required: %w", err)
		}
	}
	if btnLoginFailed(bodyText) {
		if strings.TrimSpace(login.Code) != "" {
			return nil, fmt.Errorf("trackers: BTN login failed: %w", ErrSubmitted2FARejected)
		}
		return nil, errors.New("trackers: BTN login failed")
	}
	return client, nil
}

// validateBTNClientSession confirms the client can reach BTN's upload page.
// It treats explicit login redirects and logged-out markers as invalid session
// evidence while keeping layout misses and upstream failures transient.
func validateBTNClientSession(ctx context.Context, client *http.Client, baseURL string) error {
	if client == nil {
		return errors.New("trackers: BTN session client missing")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+btnUploadPath, nil)
	if err != nil {
		return fmt.Errorf("trackers: BTN upload auth request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trackers: BTN upload auth request: %w", err)
	}
	defer resp.Body.Close()
	finalPath := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalPath = strings.ToLower(resp.Request.URL.EscapedPath())
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if readErr != nil {
		return fmt.Errorf("trackers: BTN read upload auth response: %w", readErr)
	}
	bodyText := string(body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || strings.Contains(finalPath, "login") || btnLoggedOutPage(bodyText) {
		return fmt.Errorf("%w: login required", errBTNSessionConfirmedInvalid)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("trackers: BTN upload auth unavailable status=%d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("trackers: BTN upload auth failed status=%d", resp.StatusCode)
	}
	if !btnLooksLikeUploadPage(bodyText) {
		return errors.New("trackers: BTN upload auth page validation failed")
	}
	return nil
}

func prepareUploadData(ctx context.Context, req trackers.UploadRequest, uploadCtx uploadContext) (map[string]string, error) {
	autofillPayload := url.Values{}
	autofillPayload.Set("type", resolveUploadType(req.Meta))
	autofillPayload.Set("scene_yesno", "yes")
	autofillPayload.Set("autofill", resolveUploadName(req.Meta))
	autofillPayload.Set("tvdb", "Get Info")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadCtx.uploadURL, strings.NewReader(autofillPayload.Encode()))
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN autofill request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := uploadCtx.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN autofill request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("trackers: BTN autofill failed status=%d", resp.StatusCode)
	}
	htmlPayload, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN read autofill response: %w", err)
	}
	fields := extractAutofillFields(string(htmlPayload))
	if !validateAutofill(fields) {
		return nil, errors.New("trackers: BTN autofill validation failed")
	}

	description := strings.TrimSpace(req.Meta.DescriptionOverride)
	if description == "" {
		description = commonhttp.ReadOptionalFile(req.Meta.MediaInfoTextPath)
	}
	if description == "" {
		description = "No description provided."
	}

	format := mapContainer(req.Meta, fields)
	bitrate := mapCodec(req.Meta, fields)
	media := mapSource(req.Meta, fields)
	if format == "" || bitrate == "" || media == "" {
		return nil, fmt.Errorf("trackers: BTN dropdown mapping failed format=%q bitrate=%q media=%q", format, bitrate, media)
	}

	payload := map[string]string{
		"submit":       "true",
		"type":         resolveUploadType(req.Meta),
		"scenename":    applyBTNNameMapping(resolveUploadName(req.Meta), bitrate, media),
		"seriesid":     metautil.FirstNonEmptyTrimmed(fields["seriesid"]),
		"artist":       metautil.FirstNonEmptyTrimmed(fields["artist"]),
		"title":        metautil.FirstNonEmptyTrimmed(fields["title"]),
		"actors":       metautil.FirstNonEmptyTrimmed(fields["actors"]),
		"origin":       resolveOrigin(resolveUploadName(req.Meta)),
		"year":         metautil.FirstNonEmptyTrimmed(fields["year"]),
		"tags":         metautil.FirstNonEmptyTrimmed(fields["tags"], "action"),
		"image":        metautil.FirstNonEmptyTrimmed(fields["image"]),
		"album_desc":   buildAlbumDesc(req.Meta, fields),
		"format":       format,
		"bitrate":      bitrate,
		"media":        media,
		"resolution":   metautil.FirstNonEmptyTrimmed(fields["resolution"], "SD"),
		"release_desc": description,
		"tvdb":         "autofilled",
	}
	if req.Meta.ExternalMetadata.TVDB != nil && !strings.EqualFold(strings.TrimSpace(req.Meta.ExternalMetadata.TVDB.OriginalLanguage), "en") {
		payload["foreign"] = "on"
	}
	clean := make(map[string]string, len(payload))
	for key, value := range payload {
		if strings.TrimSpace(value) == "" {
			continue
		}
		clean[key] = value
	}
	return clean, nil
}

func extractAutofillFields(html string) map[string]string {
	fields := map[string]string{}
	for _, match := range btnInputPattern.FindAllStringSubmatch(html, -1) {
		if len(match) < 3 {
			continue
		}
		fields[strings.ToLower(strings.TrimSpace(match[1]))] = strings.TrimSpace(match[2])
	}
	if match := btnTextAreaPattern.FindStringSubmatch(html); len(match) > 1 {
		fields["album_desc"] = strings.TrimSpace(stripHTML(match[1]))
	}
	for _, selectMatch := range btnSelectPattern.FindAllStringSubmatch(html, -1) {
		if len(selectMatch) < 3 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(selectMatch[1]))
		body := selectMatch[2]
		if selected := btnSelectedOptionRegex.FindStringSubmatch(body); len(selected) > 1 {
			fields[name] = strings.TrimSpace(selected[1])
			continue
		}
		if first := btnOptionValueRegex.FindStringSubmatch(body); len(first) > 1 {
			fields[name] = strings.TrimSpace(first[1])
		}
	}
	return fields
}

func validateAutofill(fields map[string]string) bool {
	artist := strings.TrimSpace(fields["artist"])
	title := strings.TrimSpace(fields["title"])
	if artist == "" || title == "" {
		return false
	}
	if strings.EqualFold(artist, "autofill fail") || strings.EqualFold(title, "autofill fail") {
		return false
	}
	return true
}

func buildAlbumDesc(meta api.PreparedMetadata, fields map[string]string) string {
	if !strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.Category), "TV") {
		return metautil.FirstNonEmptyTrimmed(fields["album_desc"])
	}
	overview := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.EpisodeOverview), strings.TrimSpace(fields["album_desc"]))
	aired := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.TVDBAiredDate), strings.TrimSpace(meta.DailyEpisodeDate), "TBA")
	season, episode := meta.CanonicalSeasonEpisode()
	episodeTitle := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.EpisodeTitle), "TBA")
	return strings.TrimSpace(fmt.Sprintf("Episode Name: %s\nEpisode Title: %s\nSeason: %d\nEpisode: %d\nAired: %s\n\nEpisode overview: %s", episodeTitle, episodeTitle, season, episode, aired, overview))
}

func resolveUploadType(meta api.PreparedMetadata) string {
	if meta.TVPack {
		return "Season"
	}
	if meta.EpisodeInt > 0 {
		return "Episode"
	}
	return "Season"
}

// btnTVPayloadMetadataMessage explains when BTN will send zero-valued TV season
// or episode fields because canonical metadata is absent. Parsed release values
// are reported only as ignored signals, and the message includes the operator
// action required by blocked dry-run entries.
func btnTVPayloadMetadataMessage(meta api.PreparedMetadata) string {
	if !strings.EqualFold(strings.TrimSpace(meta.ExternalIDs.Category), "TV") {
		return ""
	}
	missing := make([]string, 0, 2)
	ignored := make([]string, 0, 2)
	if meta.SeasonInt <= 0 {
		missing = append(missing, "season")
		if meta.Release.Season > 0 {
			ignored = append(ignored, "season")
		}
	}
	if meta.EpisodeInt <= 0 && !meta.TVPack {
		missing = append(missing, "episode")
		if meta.Release.Episode > 0 {
			ignored = append(ignored, "episode")
		}
	}
	if len(missing) == 0 {
		return ""
	}
	message := "canonical TV " + strings.Join(missing, "/") + " missing; tracker payload uses 0"
	if len(ignored) > 0 {
		message += " and ignores parsed " + strings.Join(ignored, "/") + " fallback"
	}
	message += "; refresh metadata or correct canonical season/episode before upload"
	return message
}

func resolveOrigin(releaseName string) string {
	name := strings.TrimSpace(releaseName)
	switch {
	case strings.HasSuffix(name, "-BTW"), strings.HasSuffix(name, "-NTb"), strings.HasSuffix(name, "-TVSmash"):
		return "Internal"
	case strings.HasSuffix(name, "-NOGRP"):
		return "None"
	default:
		return "P2P"
	}
}

func resolveUploadName(meta api.PreparedMetadata) string {
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	if name := strings.TrimSpace(meta.ReleaseNameNoTag); name != "" {
		return name
	}
	if name := strings.TrimSpace(meta.Filename); name != "" {
		return name
	}
	return pathutil.Base(meta.SourcePath)
}

func resolveTorrentPath(meta api.PreparedMetadata, dbPath string) (string, error) {
	candidates := []string{strings.TrimSpace(meta.TorrentPath), strings.TrimSpace(meta.ClientTorrentPath), strings.TrimSpace(meta.SourcePath)}
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
	return "", errors.New("trackers: BTN torrent file not found")
}

func resolveTrackerTorrentPath(meta api.PreparedMetadata, dbPath string, tracker string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: BTN tracker torrent path requires db path and source path")
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: BTN tmp root: %w", err)
	}
	tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: BTN tmp release dir: %w", err)
	}
	name := strings.ToLower(strings.TrimSpace(tracker))
	if name == "" {
		name = "tracker"
	}
	return filepath.Join(tmpDir, base+"."+name+".torrent"), nil
}

func downloadTrackerTorrent(ctx context.Context, client *http.Client, baseURL string, torrentID string, outputPath string) error {
	if strings.TrimSpace(torrentID) == "" {
		return errors.New("trackers: BTN torrent_id missing")
	}
	downloadURL := strings.TrimRight(baseURL, "/") + "/torrents.php?action=download&id=" + url.QueryEscape(strings.TrimSpace(torrentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("trackers: BTN torrent download request build: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trackers: BTN torrent download request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("trackers: BTN read torrent response: %w", err)
	}
	if len(body) == 0 || body[0] != 'd' {
		return errors.New("not a torrent payload")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return fmt.Errorf("trackers: BTN create torrent output dir: %w", err)
	}
	if err := os.WriteFile(outputPath, body, 0o600); err != nil {
		return fmt.Errorf("trackers: BTN write torrent output: %w", err)
	}
	return nil
}

func resolveAndDownloadViaAPI(ctx context.Context, apiURL string, apiToken string, req trackers.UploadRequest, groupID string, outputPath string) error {
	if strings.TrimSpace(apiToken) == "" {
		return errors.New("trackers: BTN api token missing for torrent resolution")
	}
	if strings.TrimSpace(apiURL) == "" {
		apiURL = btnAPIRPCURL
	}
	releaseName := resolveUploadName(req.Meta)
	filter := map[string]any{"searchstr": releaseName}
	if strings.TrimSpace(groupID) != "" {
		filter["group"] = groupID
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "ua-btn-upload",
		"method":  "getTorrentsSearch",
		"params":  []any{apiToken, filter, 50},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("trackers: BTN API search encode: %w", err)
	}
	apiReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("trackers: BTN API search request build: %w", err)
	}
	apiReq.Header.Set("Content-Type", "application/json")
	apiResp, err := (&http.Client{Timeout: 30 * time.Second}).Do(apiReq)
	if err != nil {
		return fmt.Errorf("trackers: BTN API search request: %w", err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode < 200 || apiResp.StatusCode >= 300 {
		return fmt.Errorf("trackers: BTN API search failed status=%d", apiResp.StatusCode)
	}
	var response struct {
		Result struct {
			Torrents map[string]map[string]any `json:"torrents"`
		} `json:"result"`
	}
	if err := json.NewDecoder(apiResp.Body).Decode(&response); err != nil {
		return fmt.Errorf("trackers: BTN decode torrent search response: %w", err)
	}
	selectedID := ""
	for id, torrentData := range response.Result.Torrents {
		if strings.TrimSpace(groupID) != "" {
			torrentGroup := metautil.FirstNonEmptyTrimmed(fmt.Sprint(torrentData["GroupID"]), fmt.Sprint(torrentData["groupId"]))
			if strings.TrimSpace(torrentGroup) != strings.TrimSpace(groupID) {
				continue
			}
		}
		selectedID = strings.TrimSpace(id)
		break
	}
	if selectedID == "" {
		return errors.New("trackers: BTN API did not return a matching torrent id")
	}

	downloadPayload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "ua-btn-download",
		"method":  "getTorrentById",
		"params":  []any{apiToken, selectedID},
	}
	downloadEncoded, err := json.Marshal(downloadPayload)
	if err != nil {
		return fmt.Errorf("trackers: BTN API download encode: %w", err)
	}
	downloadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(downloadEncoded))
	if err != nil {
		return fmt.Errorf("trackers: BTN API download request build: %w", err)
	}
	downloadReq.Header.Set("Content-Type", "application/json")
	downloadResp, err := (&http.Client{Timeout: 30 * time.Second}).Do(downloadReq)
	if err != nil {
		return fmt.Errorf("trackers: BTN API download request: %w", err)
	}
	defer downloadResp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(downloadResp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("trackers: BTN API read torrent response: %w", err)
	}
	if len(body) == 0 || body[0] != 'd' {
		return errors.New("trackers: BTN API did not return torrent payload")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return fmt.Errorf("trackers: BTN API create torrent output dir: %w", err)
	}
	if err := os.WriteFile(outputPath, body, 0o600); err != nil {
		return fmt.Errorf("trackers: BTN API write torrent output: %w", err)
	}
	return nil
}

// loadBTNCookiesIntoJar best-effort seeds an upload client with persisted BTN
// cookies. Missing or unreadable cookies leave the caller's client unchanged.
func loadBTNCookiesIntoJar(ctx context.Context, client *http.Client, dbPath string, baseURL string) {
	if client == nil || client.Jar == nil {
		return
	}
	values, err := loadBTNCookies(ctx, dbPath)
	if err != nil {
		return
	}
	setBTNCookies(client.Jar, baseURL, values)
}

// loadBTNCookies reads persisted BTN cookies and maps only typed not-found
// results to the BTN missing-cookie sentinel. Storage, parse, and decrypt errors
// are returned with tracker context so callers can avoid replacing valid state.
func loadBTNCookies(ctx context.Context, dbPath string) (map[string]string, error) {
	values, err := cookies.LoadTrackerCookieMap(ctx, dbPath, "BTN")
	if err != nil {
		if errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
			return nil, errBTNCookiesMissing
		}
		return nil, fmt.Errorf("trackers: %w", err)
	}
	return values, nil
}

// newBTNClientWithCookies creates a short-lived BTN client with a fresh cookie
// jar populated from the supplied stored cookie map.
func newBTNClientWithCookies(baseURL string, values map[string]string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN create session cookie jar: %w", err)
	}
	setBTNCookies(jar, baseURL, values)
	return &http.Client{Timeout: 45 * time.Second, Jar: jar}, nil
}

// setBTNCookies mirrors stored BTN cookie values into jar for baseURL. Invalid
// base URLs or nil jars are ignored because callers treat cookie seeding as
// best-effort before explicit session validation.
func setBTNCookies(jar http.CookieJar, baseURL string, values map[string]string) {
	if jar == nil {
		return
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return
	}
	jarCookies := make([]*http.Cookie, 0, len(values))
	for name, value := range values {
		// #nosec G124 -- Outbound tracker jar cookie mirrors configured BTN session values.
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: value, Domain: parsed.Hostname(), Path: "/"})
	}
	jar.SetCookies(parsed, jarCookies)
}

// persistBTNCookies saves cookies extracted from a caller-validated BTN client jar.
func persistBTNCookies(ctx context.Context, dbPath string, baseURL string, jar http.CookieJar) error {
	values, err := btnCookiesFromJar(baseURL, jar)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return errors.New("trackers: BTN login returned no usable cookies")
	}
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", values); err != nil {
		return fmt.Errorf("trackers: BTN save cookies: %w", err)
	}
	return nil
}

// btnCookiesFromJar extracts non-empty BTN cookie names and values for baseURL
// after a caller has validated that the jar represents a usable session.
func btnCookiesFromJar(baseURL string, jar http.CookieJar) (map[string]string, error) {
	if jar == nil {
		return nil, errors.New("trackers: BTN login returned no cookie jar")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("trackers: BTN parse cookie URL: %w", err)
	}
	out := make(map[string]string)
	for _, cookie := range jar.Cookies(parsed) {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" {
			continue
		}
		out[strings.TrimSpace(cookie.Name)] = cookie.Value
	}
	return out, nil
}

// resolveBTN2FACode returns a manually submitted code before falling back to
// the configured TOTP URI.
func resolveBTN2FACode(cfg config.TrackerConfig, login api.TrackerAuthLoginRequest) (string, error) {
	if code := strings.TrimSpace(login.Code); code != "" {
		return code, nil
	}
	return resolve2FACode(strings.TrimSpace(cfg.OTPURI))
}

// btnLoginNeeds2FA recognizes BTN login responses that render the manual 2FA
// challenge field instead of accepting the submitted credentials.
func btnLoginNeeds2FA(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "name=\"codenumber\"") ||
		strings.Contains(lower, "name='codenumber'")
}

// btnLoginFailed recognizes explicit BTN credential or submitted-code failures
// in successful HTTP responses.
func btnLoginFailed(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "invalid login") ||
		strings.Contains(lower, "incorrect password") ||
		strings.Contains(lower, "invalid code") ||
		strings.Contains(lower, "incorrect code") ||
		strings.Contains(lower, "login failed")
}

// btnLoggedOutPage recognizes upload-page responses that prove the session is
// logged out and safe to classify as confirmed-invalid auth.
func btnLoggedOutPage(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "<form") && (strings.Contains(lower, "password") || strings.Contains(lower, "login.php")) ||
		strings.Contains(lower, "you must be logged in") ||
		strings.Contains(lower, "please log in")
}

// btnLooksLikeUploadPage recognizes enough upload-page structure to confirm a
// BTN session without depending on one exact page layout.
func btnLooksLikeUploadPage(body string) bool {
	lower := strings.ToLower(body)
	hasForm := strings.Contains(lower, "<form")
	hasUploadAction := strings.Contains(lower, "action=\"/upload.php") ||
		strings.Contains(lower, "action='/upload.php") ||
		strings.Contains(lower, "action=\"upload.php") ||
		strings.Contains(lower, "action='upload.php")
	hasFileInput := strings.Contains(lower, "name=\"file_input\"") ||
		strings.Contains(lower, "name='file_input'")
	hasAutofill := strings.Contains(lower, "name=\"autofill\"") ||
		strings.Contains(lower, "name='autofill'")
	return hasForm && (hasFileInput || (hasUploadAction && hasAutofill))
}

func resolve2FACode(otpURI string) (string, error) {
	trimmed := strings.TrimSpace(otpURI)
	if trimmed == "" {
		return "", errors.New("otp_uri not configured")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: BTN parse otp_uri: %w", err)
	}
	secret := strings.TrimSpace(parsed.Query().Get("secret"))
	if secret == "" {
		return "", errors.New("otp_uri missing secret")
	}
	period := 30
	if value := strings.TrimSpace(parsed.Query().Get("period")); value != "" {
		if parsedValue, parseErr := strconv.Atoi(value); parseErr == nil && parsedValue > 0 {
			period = parsedValue
		}
	}
	decoder := base32.StdEncoding.WithPadding(base32.NoPadding)
	secretBytes, err := decoder.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("trackers: BTN decode otp secret: %w", err)
	}
	counterTime := time.Now().Unix() / int64(period)
	if counterTime < 0 {
		return "", errors.New("totp counter before unix epoch")
	}
	counter := uint64(counterTime)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, secretBytes)
	_, _ = mac.Write(buf)
	hash := mac.Sum(nil)
	offset := hash[len(hash)-1] & 0x0f
	code := (int(hash[offset])&0x7f)<<24 | int(hash[offset+1])<<16 | int(hash[offset+2])<<8 | int(hash[offset+3])
	return fmt.Sprintf("%06d", code%1000000), nil
}

func resolveBTNAPIURL(cfg config.TrackerConfig) string {
	if cfg.Unknown != nil {
		if raw, ok := cfg.Unknown["api_url"]; ok {
			if value := strings.TrimSpace(fmt.Sprint(raw)); value != "" {
				return value
			}
		}
	}
	return btnAPIRPCURL
}

func stripHTML(value string) string {
	replacer := strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n")
	cleaned := replacer.Replace(value)
	cleaned = regexp.MustCompile(`(?s)<[^>]*>`).ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

func mapContainer(meta api.PreparedMetadata, fields map[string]string) string {
	allowed := map[string]struct{}{"AVI": {}, "MKV": {}, "VOB": {}, "MPEG": {}, "MP4": {}, "ISO": {}, "WMV": {}, "TS": {}, "M4V": {}, "M2TS": {}, "Mixed": {}}
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	mapped := map[string]string{"avi": "AVI", "mkv": "MKV", "vob": "VOB", "mpg": "MPEG", "mpeg": "MPEG", "mp4": "MP4", "iso": "ISO", "wmv": "WMV", "ts": "TS", "m4v": "M4V", "m2ts": "M2TS"}[container]
	if mapped == "" && strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		mapped = "M2TS"
	}
	if mapped == "" && strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		mapped = "VOB"
	}
	for _, candidate := range []string{mapped, fields["format"], "Mixed"} {
		if _, ok := allowed[candidate]; ok {
			return candidate
		}
	}
	return ""
}

func mapCodec(meta api.PreparedMetadata, fields map[string]string) string {
	allowed := map[string]struct{}{"XViD": {}, "MPEG2": {}, "DiVX": {}, "DVDR": {}, "VC-1": {}, "H.264": {}, "H.265": {}, "WMV": {}, "BD": {}, "x264-Hi10P": {}, "VP9": {}, "Mixed": {}}
	videoEncode := strings.ToLower(strings.TrimSpace(meta.VideoEncode))
	videoCodec := strings.ToLower(strings.TrimSpace(meta.VideoCodec))
	bitDepth := strings.TrimSpace(meta.BitDepth)
	mapped := ""
	if (strings.Contains(videoEncode, "hi10") || bitDepth == "10") && (strings.Contains(videoEncode, "x264") || strings.Contains(videoCodec, "avc") || strings.Contains(videoCodec, "h.264")) {
		mapped = "x264-Hi10P"
	}
	if mapped == "" {
		lookup := map[string]string{"xvid": "XViD", "divx": "DiVX", "mpeg-2": "MPEG2", "mpeg2": "MPEG2", "vc-1": "VC-1", "wmv": "WMV", "vp9": "VP9", "avc": "H.264", "h.264": "H.264", "h264": "H.264", "x264": "H.264", "hevc": "H.265", "h.265": "H.265", "h265": "H.265", "x265": "H.265"}
		for _, value := range []string{videoEncode, videoCodec} {
			for needle, resolved := range lookup {
				if strings.Contains(value, needle) {
					mapped = resolved
					break
				}
			}
			if mapped != "" {
				break
			}
		}
	}
	for _, candidate := range []string{mapped, fields["bitrate"], "Mixed"} {
		if _, ok := allowed[candidate]; ok {
			return candidate
		}
	}
	return ""
}

func mapSource(meta api.PreparedMetadata, fields map[string]string) string {
	allowed := map[string]struct{}{"HDTV": {}, "PDTV": {}, "DSR": {}, "DVDRip": {}, "TVRip": {}, "VHSRip": {}, "Bluray": {}, "BDRip": {}, "BRRip": {}, "DVD5": {}, "DVD9": {}, "HDDVD": {}, "WEB-DL": {}, "WEBRip": {}, "BD5": {}, "BD9": {}, "BD25": {}, "BD50": {}, "Mixed": {}, "Unknown": {}}
	source := strings.ToLower(strings.TrimSpace(meta.Source))
	typeName := strings.ToUpper(strings.TrimSpace(meta.Type))
	resolution := strings.ToUpper(strings.TrimSpace(meta.Release.Resolution))
	var mapped string
	switch {
	case strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD"):
		mapped = "DVD9"
	case strings.EqualFold(strings.TrimSpace(meta.DiscType), "HDDVD"):
		mapped = "HDDVD"
	case typeName == "WEBDL":
		mapped = "WEB-DL"
	case typeName == "WEBRIP":
		mapped = "WEBRip"
	case typeName == "HDTV" || source == "hdtv":
		mapped = "HDTV"
	case typeName == "DVDRIP":
		mapped = "DVDRip"
	case resolution == "SD" && (source == "bluray" || source == "blu-ray"):
		mapped = "BDRip"
	default:
		mapped = map[string]string{"bluray": "Bluray", "blu-ray": "Bluray", "bdrip": "BDRip", "brrip": "BRRip", "dvd5": "DVD5", "dvd9": "DVD9", "web-dl": "WEB-DL", "webrip": "WEBRip", "pdtv": "PDTV", "dsr": "DSR", "tvrip": "TVRip", "vhsrip": "VHSRip", "bd5": "BD5", "bd9": "BD9", "bd25": "BD25", "bd50": "BD50"}[source]
	}
	for _, candidate := range []string{mapped, fields["media"], "Unknown"} {
		if _, ok := allowed[candidate]; ok {
			return candidate
		}
	}
	return ""
}

func applyBTNNameMapping(releaseName string, mappedCodec string, mappedSource string) string {
	updated := releaseName
	if mappedSource != "" {
		sourcePattern := regexp.MustCompile(`(?i)\b(bluray|blu-ray|bdrip|brrip|web-dl|webrip|hdtv|dvdrip|hddvd|dvd5|dvd9|bd5|bd9|bd25|bd50)\b`)
		updated = sourcePattern.ReplaceAllString(updated, mappedSource)
	}
	if mappedCodec != "" {
		codecPatterns := map[string]*regexp.Regexp{
			"H.264":      regexp.MustCompile(`(?i)\b(x264|h\.264|h264|avc)\b`),
			"H.265":      regexp.MustCompile(`(?i)\b(x265|h\.265|h265|hevc)\b`),
			"x264-Hi10P": regexp.MustCompile(`(?i)\b(x264-hi10p|hi10p)\b`),
			"XViD":       regexp.MustCompile(`(?i)\b(xvid)\b`),
			"DiVX":       regexp.MustCompile(`(?i)\b(divx)\b`),
			"MPEG2":      regexp.MustCompile(`(?i)\b(mpeg-2|mpeg2)\b`),
			"VC-1":       regexp.MustCompile(`(?i)\b(vc-1)\b`),
			"WMV":        regexp.MustCompile(`(?i)\b(wmv)\b`),
			"VP9":        regexp.MustCompile(`(?i)\b(vp9)\b`),
		}
		if pattern, ok := codecPatterns[mappedCodec]; ok {
			updated = pattern.ReplaceAllString(updated, mappedCodec)
		}
	}
	return updated
}
