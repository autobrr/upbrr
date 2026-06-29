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
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

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
	btnCountryMap          = map[string]string{
		"se": "1", "swe": "1", "sweden": "1",
		"us": "2", "usa": "2", "united states": "2", "united states of america": "2",
		"ru": "3", "rus": "3", "russia": "3",
		"fi": "4", "fin": "4", "finland": "4",
		"ca": "5", "can": "5", "canada": "5",
		"fr": "6", "fra": "6", "france": "6",
		"de": "7", "deu": "7", "germany": "7",
		"cn": "8", "chn": "8", "china": "8",
		"it": "9", "ita": "9", "italy": "9",
		"dk": "10", "dnk": "10", "denmark": "10",
		"no": "11", "nor": "11", "norway": "11",
		"gb": "12", "uk": "12", "gbr": "12", "united kingdom": "12",
		"ie": "13", "irl": "13", "ireland": "13",
		"pl": "14", "pol": "14", "poland": "14",
		"nl": "15", "nld": "15", "netherlands": "15",
		"be": "16", "bel": "16", "belgium": "16",
		"jp": "17", "jpn": "17", "japan": "17",
		"br": "18", "bra": "18", "brazil": "18",
		"ar": "19", "arg": "19", "argentina": "19",
		"au": "20", "aus": "20", "australia": "20",
		"nz": "21", "nzl": "21", "new zealand": "21",
		"es": "22", "esp": "22", "spain": "22",
		"pt": "23", "prt": "23", "portugal": "23",
		"mx": "24", "mex": "24", "mexico": "24",
		"sg": "25", "sgp": "25", "singapore": "25",
		"za": "26", "zaf": "26", "south africa": "26",
		"kr": "27", "kor": "27", "south korea": "27",
		"jm": "28", "jam": "28", "jamaica": "28",
		"lu": "29", "lux": "29", "luxembourg": "29",
		"hk": "30", "hkg": "30", "hong kong": "30",
		"bz": "31", "blz": "31", "belize": "31",
		"dz": "32", "dza": "32", "algeria": "32",
		"ao": "33", "ago": "33", "angola": "33",
		"at": "34", "aut": "34", "austria": "34",
		"yu": "35", "yug": "35", "yugoslavia": "35",
		"ws": "36", "wsm": "36", "western samoa": "36",
		"my": "37", "mys": "37", "malaysia": "37",
		"do": "38", "dom": "38", "dominican republic": "38",
		"gr": "39", "grc": "39", "greece": "39",
		"gt": "40", "gtm": "40", "guatemala": "40",
		"il": "41", "isr": "41", "israel": "41",
		"pk": "42", "pak": "42", "pakistan": "42",
		"cz": "43", "cze": "43", "czech republic": "43",
		"rs": "44", "srb": "44", "serbia": "44",
		"sc": "45", "syc": "45", "seychelles": "45",
		"tw": "46", "twn": "46", "taiwan": "46",
		"pr": "47", "pri": "47", "puerto rico": "47",
		"cl": "48", "chl": "48", "chile": "48",
		"cu": "49", "cub": "49", "cuba": "49",
		"cg": "50", "cog": "50", "congo": "50",
		"af": "51", "afg": "51", "afghanistan": "51",
		"tr": "52", "tur": "52", "turkey": "52",
		"uz": "53", "uzb": "53", "uzbekistan": "53",
		"ch": "54", "che": "54", "switzerland": "54",
		"ki": "55", "kir": "55", "kiribati": "55",
		"ph": "56", "phl": "56", "philippines": "56",
		"bf": "57", "bfa": "57", "burkina faso": "57",
		"ng": "58", "nga": "58", "nigeria": "58",
		"is": "59", "isl": "59", "iceland": "59",
		"nr": "60", "nru": "60", "nauru": "60",
		"si": "61", "svn": "61", "slovenia": "61",
		"al": "62", "alb": "62", "albania": "62",
		"tm": "63", "tkm": "63", "turkmenistan": "63",
		"ba": "64", "bih": "64", "bosnia herzegovina": "64",
		"ad": "65", "and": "65", "andorra": "65",
		"lt": "66", "ltu": "66", "lithuania": "66",
		"in": "67", "ind": "67", "india": "67",
		"an": "68", "ant": "68", "netherlands antilles": "68",
		"ua": "69", "ukr": "69", "ukraine": "69",
		"ve": "70", "ven": "70", "venezuela": "70",
		"hu": "71", "hun": "71", "hungary": "71",
		"ro": "72", "rou": "72", "romania": "72",
		"vu": "73", "vut": "73", "vanuatu": "73",
		"vn": "74", "vnm": "74", "vietnam": "74",
		"tt": "75", "tto": "75", "trinidad": "75",
		"hn": "76", "hnd": "76", "honduras": "76",
		"kg": "77", "kgz": "77", "kyrgyzstan": "77",
		"ec": "78", "ecu": "78", "ecuador": "78",
		"bs": "79", "bhs": "79", "bahamas": "79",
		"pe": "80", "per": "80", "peru": "80",
		"kh": "81", "khm": "81", "cambodia": "81",
		"bb": "82", "brb": "82", "barbados": "82",
		"bd": "83", "bgd": "83", "bangladesh": "83",
		"la": "84", "lao": "84", "laos": "84",
		"uy": "85", "ury": "85", "uruguay": "85",
		"ag": "86", "atg": "86", "antigua barbuda": "86",
		"py": "87", "pry": "87", "paraguay": "87",
		"su": "88", "sun": "88", "soviet": "88",
		"th": "89", "tha": "89", "thailand": "89",
		"sn": "90", "sen": "90", "senegal": "90",
		"tg": "91", "tgo": "91", "togo": "91",
		"kp": "92", "prk": "92", "north korea": "92",
		"hr": "93", "hrv": "93", "croatia": "93",
		"ee": "94", "est": "94", "estonia": "94",
		"co": "95", "col": "95", "colombia": "95",
		"lb": "96", "lbn": "96", "lebanon": "96",
		"lv": "97", "lva": "97", "latvia": "97",
		"cr": "98", "cri": "98", "costa rica": "98",
		"eg": "99", "egy": "99", "egypt": "99",
		"bg": "100", "bgr": "100", "bulgaria": "100",
		"mk": "103", "mkd": "103", "macedonia": "103",
		"kw": "104", "kwt": "104", "kuwait": "104",
		"lk": "105", "lka": "105", "sri lanka": "105",
		"ir": "106", "irn": "106", "iran": "106",
		"sa": "108", "sau": "108", "saudi arabia": "108",
		"sk": "110", "svk": "110", "slovakia": "110",
		"id": "111", "idn": "111", "indonesia": "111",
		"bn": "113", "brn": "113", "brunei": "113",
	}
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

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BTN read upload response: %w", err)
	}
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	matches := btnSuccessURLPattern.FindStringSubmatch(finalURL)
	if len(matches) < 2 {
		matches = btnSuccessURLPattern.FindStringSubmatch(string(responseBody))
	}
	if len(matches) < 2 {
		failurePath, _ := commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "BTN", "upload-failure", responseBody, ".html")
		if failurePath != "" {
			return api.UploadSummary{}, fmt.Errorf("%w failure=%s", commonhttp.UploadHTTPErrorWithURL("BTN", resp.StatusCode, finalURL, responseBody), failurePath)
		}
		return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("BTN", resp.StatusCode, finalURL, responseBody)
	}

	groupID := strings.TrimSpace(matches[1])
	torrentID := strings.TrimSpace(matches[2])
	torrentURL := strings.TrimRight(uploadCtx.baseURL, "/") + "/torrents.php?id=" + url.QueryEscape(groupID)
	if torrentID != "" {
		torrentURL += "&torrentid=" + url.QueryEscape(torrentID)
	}

	trackerTorrentPath, err := resolveTrackerTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath, "BTN")
	if err != nil {
		return api.UploadSummary{}, err
	}

	if torrentID != "" {
		if err := downloadTrackerTorrent(ctx, uploadCtx.client, uploadCtx.baseURL, torrentID, trackerTorrentPath); err != nil {
			if req.Logger != nil {
				req.Logger.Warnf("trackers: BTN torrent download fallback to API search: %s", commonhttp.RedactErrorDetail(err.Error()))
			}
			if err := resolveAndDownloadViaAPI(ctx, uploadCtx.apiURL, uploadCtx.apiToken, req, groupID, trackerTorrentPath); err != nil {
				return api.UploadSummary{}, err
			}
		}
	} else {
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
	if resolveFastTorrent(req.TrackerConfig) {
		payload["fasttorrent"] = "on"
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}

	return api.TrackerDryRunEntry{
		Tracker:          "BTN",
		Status:           "ready",
		Message:          "dry-run payload generated",
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
	uploadType := resolveUploadType(req.Meta)
	autofillPayload.Set("type", uploadType)
	autofillPayload.Set("tvdb", "Get Info")

	if req.Meta.ExternalMetadata.TVDB != nil && req.Meta.ExternalMetadata.TVDB.TVDBID > 0 {
		autofillPayload.Set("scene_yesno", "No")
		autofillPayload.Set("auto_series", strconv.Itoa(req.Meta.ExternalMetadata.TVDB.TVDBID))

		if uploadType == "Episode" {
			seasonPart := fmt.Sprintf("S%02d", req.Meta.Release.Season)
			episodePart := ""
			if req.Meta.Release.Episode > 0 {
				episodePart = fmt.Sprintf("E%02d", req.Meta.Release.Episode)
			}
			autofillPayload.Set("auto_title", seasonPart+episodePart)
		} else {
			autofillPayload.Set("auto_season", strconv.Itoa(req.Meta.Release.Season))
		}
	} else {
		autofillPayload.Set("scene_yesno", "Yes")
		autofillPayload.Set("autofill", resolveUploadName(req.Meta))
	}

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
	if !validateAutofill(fields, uploadType) {
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
		"resolution":   mapResolution(req.Meta),
		"release_desc": description,
		"tvdb":         "autofilled",
	}
	if resolveFastTorrent(req.TrackerConfig) {
		payload["fasttorrent"] = "on"
	}
	if req.Meta.ExternalMetadata.TVDB != nil && !strings.EqualFold(strings.TrimSpace(req.Meta.ExternalMetadata.TVDB.OriginalLanguage), "en") {
		payload["foreign"] = "on"
		if countryID := resolveCountryID(req.Meta); countryID != "" {
			payload["country"] = countryID
		}
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

func validateAutofill(fields map[string]string, uploadType string) bool {
	artist := strings.TrimSpace(fields["artist"])
	title := strings.TrimSpace(fields["title"])
	if artist == "" {
		return false
	}
	if uploadType == "Episode" && title == "" {
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
	season := meta.SeasonInt
	episode := meta.EpisodeInt
	if season <= 0 {
		season = meta.Release.Season
	}
	if episode <= 0 {
		episode = meta.Release.Episode
	}
	episodeTitle := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.EpisodeTitle), "TBA")
	return strings.TrimSpace(fmt.Sprintf("Episode Name: %s\nEpisode Title: %s\nSeason: %d\nEpisode: %d\nAired: %s\n\nEpisode overview: %s", episodeTitle, episodeTitle, season, episode, aired, overview))
}

func resolveUploadType(meta api.PreparedMetadata) string {
	if meta.TVPack {
		return "Season"
	}
	if meta.EpisodeInt > 0 || meta.Release.Episode > 0 {
		return "Episode"
	}
	return "Season"
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

func stripEpisodeTitle(name string, episodeTitle string) string {
	if episodeTitle == "" || name == "" {
		return name
	}
	// uncleaned episodeTitle is embedded directly into ReleaseName.
	return strings.ReplaceAll(name, episodeTitle, "")
}

func resolveUploadName(meta api.PreparedMetadata) string {
	var name string
	if n := strings.TrimSpace(meta.ReleaseName); n != "" {
		name = n
	} else if n := strings.TrimSpace(meta.ReleaseNameNoTag); n != "" {
		name = n
	} else if n := strings.TrimSpace(meta.Filename); n != "" {
		name = n
	} else {
		name = pathutil.Base(meta.SourcePath)
	}
	name = stripEpisodeTitle(name, meta.EpisodeTitle)
	name = cleanAndNormalizeBTNName(name)
	return applyBTNNoGroupSuffix(name, meta)
}

func applyBTNNoGroupSuffix(name string, meta api.PreparedMetadata) string {
	tag := strings.TrimSpace(strings.TrimPrefix(meta.Tag, "-"))

	if tag != "" && !isNoGroupTag(tag) {
		return name
	}

	noGroupPattern := regexp.MustCompile(`(?i)-(nogrp|nogroup|unknown|unk)$`)
	normalizedName := noGroupPattern.ReplaceAllString(name, "")
	normalizedName = strings.TrimRight(normalizedName, ".-")

	return normalizedName + "-NOGRP"
}

func isNoGroupTag(tag string) bool {
	value := strings.ToLower(strings.TrimSpace(tag))
	switch value {
	case "nogrp", "nogroup", "unknown", "unk":
		return true
	default:
		return false
	}
}

func removeDiacritics(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

func cleanAndNormalizeBTNName(value string) string {
	// 0. Remove diacritics
	value = removeDiacritics(value)

	// 1. Dot normalization (spaces to dots, collapse dots)
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, " ", ".")

	// 2. Replace plus in DD+
	value = strings.ReplaceAll(value, "DD+", "DDP")

	// 3. Atmos DDP normalization (e.g. DDP 5.1 Atmos -> DDPA5.1)
	value = regexp.MustCompile(`(?i)\.DDP\.(\d+(?:\.\d+)?)\.Atmos`).ReplaceAllString(value, `.DDPA$1`)

	// 4. Atmos TrueHD normalization (e.g. TrueHD 7.1 Atmos -> TrueHDA7.1)
	value = regexp.MustCompile(`(?i)\.TrueHD\.(\d+(?:\.\d+)?)\.Atmos`).ReplaceAllString(value, `.TrueHDA$1`)

	// 5. Other Audio channel normalization
	value = regexp.MustCompile(`\.DDP\.(\d)`).ReplaceAllString(value, `.DDP$1`)
	value = regexp.MustCompile(`\.DD\.(\d)`).ReplaceAllString(value, `.DD$1`)
	value = regexp.MustCompile(`\.DTS\.(\d)`).ReplaceAllString(value, `.DTS$1`)
	value = regexp.MustCompile(`\.AAC\.(\d)`).ReplaceAllString(value, `.AAC$1`)
	value = regexp.MustCompile(`\.FLAC\.(\d)`).ReplaceAllString(value, `.FLAC$1`)
	value = regexp.MustCompile(`(?i)\.TrueHD\.(\d)`).ReplaceAllString(value, `.TrueHD$1`)
	value = regexp.MustCompile(`(?i)\.PCM\.(\d)`).ReplaceAllString(value, `.PCM$1`)
	value = regexp.MustCompile(`(?i)\.LPCM\.(\d)`).ReplaceAllString(value, `.LPCM$1`)

	// 6. Remove non-alphanumeric characters (except dots and hyphens)
	value = regexp.MustCompile(`[^a-zA-Z0-9.\-]`).ReplaceAllString(value, ".")

	// Collapse any two or more dots
	value = regexp.MustCompile(`\.{2,}`).ReplaceAllString(value, ".")

	return strings.TrimSpace(value)
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
	if downloadResp.StatusCode < 200 || downloadResp.StatusCode >= 300 {
		return fmt.Errorf("trackers: BTN API download failed status=%d", downloadResp.StatusCode)
	}
	var downloadResult struct {
		Result struct {
			DownloadURL string `json:"DownloadURL"`
		} `json:"result"`
	}
	if err := json.NewDecoder(downloadResp.Body).Decode(&downloadResult); err != nil {
		return fmt.Errorf("trackers: BTN API decode download response: %w", err)
	}
	if downloadResult.Result.DownloadURL == "" {
		return errors.New("trackers: BTN API did not return DownloadURL")
	}

	if err := validateBTNAPIURL(ctx, downloadResult.Result.DownloadURL); err != nil {
		return fmt.Errorf("trackers: BTN API invalid download url: %w", err)
	}

	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadResult.Result.DownloadURL, nil)
	if err != nil {
		return fmt.Errorf("trackers: BTN API torrent fetch request build: %w", err)
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := validateBTNAPIURL(req.Context(), req.URL.String()); err != nil {
				return err
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
	dlResp, err := client.Do(dlReq)
	if err != nil {
		return fmt.Errorf("trackers: BTN API torrent fetch request: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode < 200 || dlResp.StatusCode >= 300 {
		return fmt.Errorf("trackers: BTN API download fetch failed status=%d", dlResp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(dlResp.Body, 8*1024*1024))
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

func resolveFastTorrent(cfg config.TrackerConfig) bool {
	if cfg.Unknown != nil {
		if raw, ok := cfg.Unknown["fast_torrent"]; ok {
			if b, ok := raw.(bool); ok {
				return b
			}
			if s, ok := raw.(string); ok {
				return strings.EqualFold(strings.TrimSpace(s), "true") || strings.TrimSpace(s) == "1"
			}
		}
	}
	return false
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

func mapResolution(meta api.PreparedMetadata) string {
	switch strings.ToLower(strings.TrimSpace(meta.Release.Resolution)) {
	case "2160p", "4320p", "8640p", "4k", "8k":
		return "2160p"
	case "1080p", "1440p":
		return "1080p"
	case "1080i":
		return "1080i"
	case "720p":
		return "720p"
	default:
		return "SD"
	}
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

// resolveCountryID extracts country information from external metadata and returns the BTN country ID.
// It tries TVDB first, then TMDB, then IMDB. Returns empty string if no country is found.
// All inputs are normalized to lowercase before matching to handle:
// - TVDB alpha-3 codes (e.g., "usa") - converted to alpha-2 then mapped
// - TMDB alpha-2 codes (e.g., "US") - normalized to lowercase then mapped
// - IMDB country names (e.g., "United States") - normalized to lowercase then matched
func resolveCountryID(meta api.PreparedMetadata) string {
	var countryStr string

	// Try TVDB first (ISO 3166-1 alpha-3, lowercase)
	if meta.ExternalMetadata.TVDB != nil && meta.ExternalMetadata.TVDB.OriginalCountry != "" {
		countryStr = meta.ExternalMetadata.TVDB.OriginalCountry
	}

	// Fall back to TMDB (ISO 3166-1 alpha-2, uppercase)
	if countryStr == "" && meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.OriginCountry) > 0 {
		countryStr = meta.ExternalMetadata.TMDB.OriginCountry[0]
	}

	// Fall back to IMDB (full country names)
	if countryStr == "" && meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.Country != "" {
		// IMDB can have multiple countries separated by commas, take the first one
		parts := strings.Split(meta.ExternalMetadata.IMDB.Country, ",")
		if len(parts) > 0 {
			countryStr = strings.TrimSpace(parts[0])
		}
	}

	if countryStr == "" {
		return ""
	}

	// Normalize to lowercase for all lookups
	normalized := strings.ToLower(strings.TrimSpace(countryStr))

	// Try direct lookup (handles alpha-2 codes, alpha-3 codes, and country names)
	if id, ok := btnCountryMap[normalized]; ok {
		return id
	}

	// Try partial name matching for fuzzy country name variations
	// (e.g., "united states of america" partially matches "united states").
	// Only match against longer names to prevent false positives from short codes.
	for key, id := range btnCountryMap {
		if len(key) > 3 && (strings.Contains(normalized, key) || strings.Contains(key, normalized)) {
			return id
		}
	}

	return ""
}

func validateBTNAPIURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return errors.New("missing host")
	}
	lowerHost := strings.ToLower(host)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") || strings.Contains(lowerHost, "%") {
		return fmt.Errorf("blocked private host %q", host)
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
			return fmt.Errorf("blocked private address %q", addr)
		}
		return nil
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	for _, item := range addrs {
		if addr, ok := netip.AddrFromSlice(item.IP); ok {
			addr = addr.Unmap()
			if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
				return fmt.Errorf("host %q resolved to blocked address %q", host, addr)
			}
		}
	}
	return nil
}
