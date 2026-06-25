// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

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
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"path" //nolint:depguard // Reads poster URL path extension, not local filesystem extension.
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/imagehost"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	ptpBaseURL     = "https://passthepopcorn.me"
	ptpUploadPath  = "/upload.php"
	ptpTorrentPath = "/torrents.php"
	ptpLoginPath   = "/ajax.php?action=login"
	ptpCookieFile  = "PTP.json"
	ptpUserAgent   = "upbrr"
)

var (
	ptpAntiCsrfPattern     = regexp.MustCompile(`data-AntiCsrfToken="([^"]+)"`)
	ptpSuccessPattern      = regexp.MustCompile(`torrents\.php\?id=(\d+)&torrentid=(\d+)`)
	newPosterHTTPClient    = newPublicPosterHTTPClient
	reservedPosterPrefixes = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
	}
)

type uploadState struct {
	baseURL     string
	uploadURL   string
	announceURL string
	client      *http.Client
	groupID     string
	torrentPath string
	description string
	fields      map[string]string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}

	body, contentType, err := buildMultipartPayload(state.fields, state.torrentPath, "file_input")
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", ptpUserAgent)

	resp, err := state.client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	if matches := ptpSuccessPattern.FindStringSubmatch(finalURL); len(matches) == 3 {
		groupID := strings.TrimSpace(matches[1])
		torrentID := strings.TrimSpace(matches[2])
		torrentURL := strings.TrimRight(state.baseURL, "/") + "/torrents.php?id=" + url.QueryEscape(groupID) + "&torrentid=" + url.QueryEscape(torrentID)
		trackerTorrentPath, err := resolveTrackerTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath, "PTP")
		if err != nil {
			return api.UploadSummary{}, err
		}
		if err := trackers.WritePersonalizedTorrent(state.torrentPath, trackerTorrentPath, state.announceURL, torrentURL, "PTP"); err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: PTP write torrent artifact: %w", err)
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "PTP",
				TorrentID:   torrentID,
				TorrentURL:  torrentURL,
				DownloadURL: torrentURL,
				TorrentPath: trackerTorrentPath,
			}},
		}, nil
	}

	failurePath := ""
	if pathValue, pathErr := resolveFailurePath(req.Meta, req.AppConfig.MainSettings.DBPath); pathErr == nil {
		failurePath = pathValue
		_ = os.WriteFile(failurePath, bodyBytes, 0o600)
	}
	errText := commonhttp.RedactErrorDetail(extractAlertError(string(bodyBytes)))
	if errText == "" {
		errText = commonhttp.ExtractHTTPErrorDetail(bodyBytes)
	}
	if errText == "" {
		errText = "upload failed"
	}
	if failurePath != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: PTP upload failed status=%d url=%s error=%s failure=%s", resp.StatusCode, commonhttp.RedactErrorDetail(finalURL), compactError(errText), failurePath)
	}
	return api.UploadSummary{}, fmt.Errorf("trackers: PTP upload failed status=%d url=%s error=%s", resp.StatusCode, commonhttp.RedactErrorDetail(finalURL), compactError(errText))
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req, true)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	message := "dry-run payload generated"
	if state.groupID != "" {
		message += " for existing group"
	} else {
		message += " for new group"
	}
	return api.TrackerDryRunEntry{
		Tracker:          "PTP",
		Status:           "ready",
		Message:          message,
		ReleaseName:      resolveUploadName(req.Meta),
		DescriptionGroup: "ptp",
		Description:      state.description,
		Endpoint:         state.uploadURL,
		Payload:          state.fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
		Questionnaire: buildQuestionnaire(req.Meta, state.groupID),
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, dryRun bool) (uploadState, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(req.TrackerConfig.URL), "/")
	if baseURL == "" {
		baseURL = ptpBaseURL
	}
	announceURL := normalizedAnnounceURL(req.TrackerConfig.AnnounceURL)
	torrentPath, err := resolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: PTP resolve upload torrent: %w", err)
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req.Meta, req.TrackerConfig, req.AppConfig, assets)
	groupID, err := lookupGroupID(ctx, baseURL, req.TrackerConfig, req.Meta)
	if err != nil {
		return uploadState{}, err
	}
	answers := questionnaireAnswers(req.Meta, "PTP")
	poster := metautil.FirstNonEmptyTrimmed(answers["poster"], resolvePoster(req.Meta))
	if !dryRun {
		poster = rehostPosterToSelectedHost(ctx, req, poster)
	}
	fields, err := buildUploadFields(req.Meta, description, groupID, answers, poster)
	if err != nil {
		return uploadState{}, err
	}
	fields["AntiCsrfToken"] = "dry-run-token"

	var client *http.Client
	if !dryRun {
		client, fields["AntiCsrfToken"], err = resolveSession(ctx, req.TrackerConfig, req.AppConfig.MainSettings.DBPath, baseURL, req.Logger)
		if err != nil {
			return uploadState{}, err
		}
	}

	return uploadState{
		baseURL:     baseURL,
		uploadURL:   baseURL + ptpUploadPath,
		announceURL: announceURL,
		client:      client,
		groupID:     groupID,
		torrentPath: torrentPath,
		description: description,
		fields:      fields,
	}, nil
}

func resolveUploadTorrentPath(meta api.PreparedMetadata, dbPath string) (string, error) {
	preparedPath, err := trackers.ResolveTrackerTorrentArtifactPath(meta, dbPath, "PTP")
	if err == nil && strings.TrimSpace(meta.TorrentPath) == preparedPath {
		if info, statErr := os.Stat(preparedPath); statErr == nil && !info.IsDir() {
			return preparedPath, nil
		}
	}
	path, err := trackers.ResolveUploadTorrentPath(meta, dbPath)
	if err != nil {
		return "", fmt.Errorf("trackers: resolve upload torrent path: %w", err)
	}
	return path, nil
}

func buildDescription(meta api.PreparedMetadata, trackerConfig config.TrackerConfig, appConfig config.Config, assets trackers.DescriptionAssets) string {
	baseDescription := strings.TrimSpace(assets.Description)
	if assets.Final {
		return baseDescription
	}
	if baseDescription != "" {
		report := bbcode.CleanPTPDescription(baseDescription, meta.DiscType)
		baseDescription = strings.TrimSpace(report.Description)
	}

	var sections []string
	if mediaSection, err := buildMediaSection(meta, appConfig.MainSettings.DBPath); err == nil && mediaSection != "" {
		sections = append(sections, mediaSection)
	}
	if strings.TrimSpace(baseDescription) != "" {
		sections = append(sections, convertDescription(baseDescription))
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") && strings.TrimSpace(meta.ServiceLongName) != "" && trackerConfig.AddWebSourceToDesc {
		sections = append(sections, fmt.Sprintf("[quote][align=center]This release is sourced from %s[/align][/quote]", strings.TrimSpace(meta.ServiceLongName)))
	}
	if shots := buildScreenshotSection(meta, assets.Screenshots); shots != "" {
		sections = append(sections, shots)
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func buildMediaSection(meta api.PreparedMetadata, dbPath string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		text, err := readBDSummary(meta, dbPath)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		return "[mediainfo]" + strings.TrimSpace(text) + "[/mediainfo]", nil
	default:
		text, err := readTextFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		return "[mediainfo]" + strings.TrimSpace(text) + "[/mediainfo]", nil
	}
}

func buildScreenshotSection(meta api.PreparedMetadata, screenshots []api.ScreenshotImage) string {
	if len(screenshots) == 0 {
		return ""
	}
	minimum := meta.Options.Screens
	if minimum <= 0 {
		minimum = 2
	}
	if requiresMinimumTwoScreens(meta) && minimum < 2 {
		minimum = 2
	}
	allowed := make([]string, 0, len(screenshots))
	for _, screenshot := range screenshots {
		rawURL := strings.TrimSpace(screenshot.RawURL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(screenshot.ImgURL)
		}
		if rawURL == "" {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(screenshot.Host))
		if host == "" {
			host = imagehost.ExtractHost(rawURL)
		}
		if !isPTPImageHost(host) {
			continue
		}
		allowed = append(allowed, "[img]"+rawURL+"[/img]")
	}
	if len(allowed) < minimum {
		return ""
	}
	return strings.Join(allowed, "\n")
}

func isPTPImageHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "pixhost", "imgbb", "onlyimage", "ptscreens":
		return true
	default:
		return false
	}
}

func requiresMinimumTwoScreens(meta api.PreparedMetadata) bool {
	if len(meta.FileList) > 1 {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") || strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV")
}

func convertDescription(value string) string {
	replacer := strings.NewReplacer(
		"[spoiler", "[hide",
		"[/spoiler]", "[/hide]",
		"[center]", "[align=center]",
		"[/center]", "[/align]",
		"[left]", "[align=left]",
		"[/left]", "[/align]",
		"[right]", "[align=right]",
		"[/right]", "[/align]",
		"[h1]", "[u][b]",
		"[/h1]", "[/b][/u]",
		"[h2]", "[u][b]",
		"[/h2]", "[/b][/u]",
		"[h3]", "[u][b]",
		"[/h3]", "[/b][/u]",
	)
	return replacer.Replace(strings.TrimSpace(value))
}

func lookupGroupID(ctx context.Context, baseURL string, trackerConfig config.TrackerConfig, meta api.PreparedMetadata) (string, error) {
	apiUser := strings.TrimSpace(trackerConfig.PTPAPIUser)
	apiKey := strings.TrimSpace(trackerConfig.PTPAPIKey)
	if apiUser == "" || apiKey == "" || meta.ExternalIDs.IMDBID == 0 {
		return "", nil
	}
	headers := map[string]string{
		"ApiUser":    apiUser,
		"ApiKey":     apiKey,
		"User-Agent": ptpUserAgent,
	}
	values := url.Values{}
	values.Set("imdb", fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+ptpTorrentPath+"?"+values.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP build group lookup request: %w", err)
	}
	for key, value := range headers {
		httpReq.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", nil
	}
	if movies, ok := payload["Movies"].([]any); ok && len(movies) > 0 {
		if movie, ok := movies[0].(map[string]any); ok {
			if groupID := stringFromAny(movie["GroupId"]); groupID != "" {
				return groupID, nil
			}
		}
	}
	return stringFromAny(payload["GroupId"]), nil
}

func rehostPosterToSelectedHost(ctx context.Context, req trackers.UploadRequest, imageURL string) string {
	trimmedURL := strings.TrimSpace(imageURL)
	if trimmedURL == "" {
		return ""
	}
	if req.Images == nil {
		return trimmedURL
	}
	if req.Meta.ImageHostOverrides.SkipUpload != nil && *req.Meta.ImageHostOverrides.SkipUpload {
		return trimmedURL
	}

	selectedHost, err := trackers.PreferredImageUploadHost("PTP", req.TrackerConfig, req.Meta.ImageHostOverrides)
	if err != nil {
		logPosterRehostFailure(req.Logger, "", err)
		return trimmedURL
	}
	selectedHost = strings.ToLower(strings.TrimSpace(selectedHost))
	if selectedHost == "" {
		return trimmedURL
	}
	if strings.EqualFold(strings.TrimSpace(imagehost.ExtractHost(trimmedURL)), selectedHost) {
		return trimmedURL
	}

	posterPath, err := downloadPoster(ctx, req.Meta, req.AppConfig.MainSettings.DBPath, trimmedURL)
	if err != nil {
		logPosterRehostFailure(req.Logger, selectedHost, err)
		return trimmedURL
	}
	uploaded, err := req.Images.Upload(ctx, req.Meta, selectedHost, "global", []api.ScreenshotImage{{Path: posterPath}})
	if err != nil {
		logPosterRehostFailure(req.Logger, selectedHost, err)
		return trimmedURL
	}
	if len(uploaded) == 0 {
		logPosterRehostFailure(req.Logger, selectedHost, errors.New("upload returned no links"))
		return trimmedURL
	}
	uploadedURL := metautil.FirstNonEmptyTrimmed(uploaded[0].RawURL, uploaded[0].ImgURL, uploaded[0].WebURL)
	if strings.TrimSpace(uploadedURL) == "" {
		logPosterRehostFailure(req.Logger, selectedHost, errors.New("upload returned blank link"))
		return trimmedURL
	}
	if req.Logger != nil {
		req.Logger.Infof("trackers: PTP poster rehosted to %s", selectedHost)
	}
	return strings.TrimSpace(uploadedURL)
}

func downloadPoster(ctx context.Context, meta api.PreparedMetadata, dbPath string, imageURL string) (string, error) {
	if err := validatePosterURL(imageURL); err != nil {
		return "", err
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", fmt.Errorf("poster request: %w", err)
	}
	httpReq.Header.Set("User-Agent", ptpUserAgent)

	client := newPosterHTTPClient()
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("poster download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("poster download status=%d", resp.StatusCode)
	}

	const maxPosterBytes = 25 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPosterBytes+1))
	if err != nil {
		return "", fmt.Errorf("poster read: %w", err)
	}
	if len(body) == 0 {
		return "", errors.New("poster download returned empty body")
	}
	if len(body) > maxPosterBytes {
		return "", errors.New("poster exceeds maximum size")
	}

	posterPath := filepath.Join(tmpDir, "PTP_POSTER"+posterExtension(imageURL, resp.Header.Get("Content-Type")))
	if err := os.WriteFile(posterPath, body, 0o600); err != nil {
		return "", fmt.Errorf("poster write: %w", err)
	}
	return posterPath, nil
}

func posterExtension(imageURL string, contentType string) string {
	if parsed, err := url.Parse(imageURL); err == nil {
		switch ext := strings.ToLower(path.Ext(parsed.Path)); ext {
		case ".jpg", ".jpeg", ".png", ".webp":
			return ext
		}
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/jpeg":
		return ".jpg"
	default:
		return ".jpg"
	}
}

func validatePosterURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("poster URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("poster URL must use http or https")
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return errors.New("poster URL host is required")
	}
	return nil
}

func newPublicPosterHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("poster dial address: %w", err)
			}
			target, err := resolvePublicPosterAddress(ctx, host, port)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, target)
		},
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}
}

func resolvePublicPosterAddress(ctx context.Context, host string, port string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok || !isPublicPosterIP(addr.Unmap()) {
			return "", fmt.Errorf("poster host %q resolves to non-public IP", host)
		}
		return net.JoinHostPort(addr.String(), port), nil
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("poster DNS lookup %q: %w", host, err)
	}
	for _, candidate := range resolved {
		addr, ok := netip.AddrFromSlice(candidate.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if isPublicPosterIP(addr) {
			return net.JoinHostPort(addr.String(), port), nil
		}
	}
	return "", fmt.Errorf("poster host %q has no public IP addresses", host)
}

func isPublicPosterIP(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return !ipInPrefixes(ip, reservedPosterPrefixes)
}

func ipInPrefixes(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func logPosterRehostFailure(logger api.Logger, host string, err error) {
	if logger == nil || err == nil {
		return
	}
	if strings.TrimSpace(host) == "" {
		logger.Warnf("trackers: PTP poster rehost failed: %v", err)
		return
	}
	logger.Warnf("trackers: PTP poster rehost to %s failed: %v", strings.TrimSpace(host), err)
}

func buildUploadFields(meta api.PreparedMetadata, description string, groupID string, answers map[string]string, poster string) (map[string]string, error) {
	resolution, otherResolution := resolveResolution(meta)
	fields := map[string]string{
		"submit":          "true",
		"remaster_year":   "",
		"remaster_title":  resolveRemasterTitle(meta),
		"type":            resolveType(meta),
		"codec":           "Other",
		"other_codec":     resolveCodec(meta),
		"container":       "Other",
		"other_container": resolveContainer(meta),
		"resolution":      resolution,
		"source":          "Other",
		"other_source":    resolveSource(meta.Source),
		"release_desc":    description,
		"nfo_text":        "",
		"subtitles[]":     joinInts(resolveSubtitles(meta)),
		"trumpable[]":     "",
	}
	if resolution == "Other" && otherResolution != "" {
		fields["other_resolution"] = otherResolution
	}
	if fields["remaster_title"] != "" {
		fields["remaster"] = "on"
	}
	if meta.Scene {
		fields["scene"] = "on"
	}
	if meta.PersonalRelease {
		fields["internalrip"] = "on"
	}
	if meta.ExternalIDs.IMDBID == 0 {
		fields["imdb"] = "0"
	} else {
		fields["imdb"] = fmt.Sprintf("%07d", meta.ExternalIDs.IMDBID)
	}
	if groupID != "" {
		fields["groupid"] = groupID
		return fields, nil
	}

	title, year := resolveGroupTitleYear(meta)
	title = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["title"]), title)
	year = metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["year"]), year)
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("trackers: PTP missing title for new group upload")
	}
	fields["title"] = title
	fields["year"] = year
	fields["image"] = strings.TrimSpace(poster)
	fields["tags"] = metautil.FirstNonEmptyTrimmed(answers["tags"], resolveTags(meta))
	fields["album_desc"] = metautil.FirstNonEmptyTrimmed(answers["album_desc"], resolveOverview(meta))
	fields["trailer"] = metautil.FirstNonEmptyTrimmed(answers["trailer"], resolveTrailer(meta))
	directors := resolveDirectors(meta)
	if len(directors) > 0 {
		fields["artist[]"] = strings.Join(directors, "\n")
		fields["importance[]"] = "1"
	}
	if fields["image"] == "" {
		return nil, errors.New("trackers: PTP missing poster for new group upload")
	}
	return fields, nil
}

func resolveSession(ctx context.Context, trackerConfig config.TrackerConfig, dbPath string, baseURL string, logger api.Logger) (*http.Client, string, error) {
	if logger == nil {
		logger = api.NopLogger{}
	}

	cookies, err := loadCookies(ctx, dbPath)
	if err == nil && len(cookies) > 0 {
		client, token, tokenErr := fetchAntiCsrfToken(ctx, baseURL, cookies)
		if tokenErr == nil {
			return client, token, nil
		}
		_ = cookiepkg.DeleteTrackerCookies(ctx, dbPath, "PTP")
	}
	return loginAndFetchAntiCsrfToken(ctx, trackerConfig, dbPath, baseURL, logger)
}

func ResolveSessionForTrackerAuth(ctx context.Context, trackerConfig config.TrackerConfig, dbPath string) error {
	baseURL := strings.TrimRight(strings.TrimSpace(trackerConfig.URL), "/")
	if baseURL == "" {
		baseURL = ptpBaseURL
	}
	_, _, err := resolveSession(ctx, trackerConfig, dbPath, baseURL, api.NopLogger{})
	return err
}

func fetchAntiCsrfToken(ctx context.Context, baseURL string, cookies map[string]string) (*http.Client, string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP create session cookie jar: %w", err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP parse base URL: %w", err)
	}
	jarCookies := make([]*http.Cookie, 0, len(cookies))
	for name, value := range cookies {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		// #nosec G124 -- Outbound tracker jar cookie mirrors configured PTP session values.
		jarCookies = append(jarCookies, &http.Cookie{Name: name, Value: value, Path: "/", Domain: parsed.Hostname()})
	}
	jar.SetCookies(parsed, jarCookies)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	token, err := requestAntiCsrfToken(ctx, client, baseURL)
	if err != nil {
		return nil, "", err
	}
	return client, token, nil
}

func loginAndFetchAntiCsrfToken(ctx context.Context, trackerConfig config.TrackerConfig, dbPath string, baseURL string, logger api.Logger) (*http.Client, string, error) {
	if logger == nil {
		logger = api.NopLogger{}
	}

	username := strings.TrimSpace(trackerConfig.Username)
	password := strings.TrimSpace(trackerConfig.Password)
	announceURL := normalizedAnnounceURL(trackerConfig.AnnounceURL)
	if username == "" || password == "" || announceURL == "" {
		return nil, "", errors.New("trackers: PTP requires username, password, and announce_url")
	}
	passkey, err := passkeyFromAnnounce(announceURL)
	if err != nil {
		return nil, "", err
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP create login cookie jar: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	form := url.Values{
		"username":   {username},
		"password":   {password},
		"passkey":    {passkey},
		"keeplogged": {"1"},
	}
	loginURL := strings.TrimRight(baseURL, "/") + ptpLoginPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP build login request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", ptpUserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("trackers: PTP login request: %w", err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", fmt.Errorf("trackers: PTP login decode: %w", err)
	}
	switch strings.TrimSpace(stringFromAny(payload["Result"])) {
	case "Ok":
	case "TfaRequired":
		code, codeErr := resolve2FACode(trackerConfig.OTPURI)
		if codeErr != nil {
			return nil, "", fmt.Errorf("trackers: PTP 2FA required: %w", codeErr)
		}
		form.Set("TfaType", "normal")
		form.Set("TfaCode", code)
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, "", fmt.Errorf("trackers: PTP build 2FA request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		httpReq.Header.Set("User-Agent", ptpUserAgent)
		resp, err = client.Do(httpReq)
		if err != nil {
			return nil, "", fmt.Errorf("trackers: PTP 2FA request: %w", err)
		}
		defer resp.Body.Close()
		payload = map[string]any{}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, "", fmt.Errorf("trackers: PTP 2FA decode: %w", err)
		}
		if strings.TrimSpace(stringFromAny(payload["Result"])) != "Ok" {
			return nil, "", errors.New("trackers: PTP login failed")
		}
	default:
		return nil, "", errors.New("trackers: PTP login failed")
	}

	if err := saveCookies(ctx, dbPath, client, baseURL); err != nil {
		logger.Warnf("trackers: PTP failed to persist login cookies: %v", err)
	}
	token := strings.TrimSpace(stringFromAny(payload["AntiCsrfToken"]))
	if token == "" {
		return nil, "", errors.New("trackers: PTP login missing anti csrf token")
	}
	return client, token, nil
}

func requestAntiCsrfToken(ctx context.Context, client *http.Client, baseURL string) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+ptpUploadPath, nil)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP build upload page request: %w", err)
	}
	httpReq.Header.Set("User-Agent", ptpUserAgent)
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP upload page: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP read upload page: %w", err)
	}
	matches := ptpAntiCsrfPattern.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return "", errors.New("trackers: PTP anti csrf token not found")
	}
	return strings.TrimSpace(matches[1]), nil
}

func buildMultipartPayload(fields map[string]string, torrentPath string, fileField string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if key == "artist[]" {
			for value := range strings.SplitSeq(fields[key], "\n") {
				if strings.TrimSpace(value) == "" {
					continue
				}
				if err := writer.WriteField(key, value); err != nil {
					_ = writer.Close()
					return nil, "", fmt.Errorf("trackers: PTP write multipart field %q: %w", key, err)
				}
			}
			continue
		}
		if key == "subtitles[]" || key == "trumpable[]" {
			for value := range strings.SplitSeq(fields[key], ",") {
				trimmed := strings.TrimSpace(value)
				if trimmed == "" {
					continue
				}
				if err := writer.WriteField(key, trimmed); err != nil {
					_ = writer.Close()
					return nil, "", fmt.Errorf("trackers: PTP write multipart field %q: %w", key, err)
				}
			}
			continue
		}
		if err := writer.WriteField(key, fields[key]); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: PTP write multipart field %q: %w", key, err)
		}
	}

	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: PTP open torrent file: %w", err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile(fileField, "placeholder.torrent")
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: PTP create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: PTP copy torrent file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: PTP close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveTrackerTorrentPath(meta api.PreparedMetadata, dbPath string, tracker string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: PTP tracker torrent path requires db path and source path")
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, base, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, base+"."+strings.ToLower(strings.TrimSpace(tracker))+".torrent"), nil
}

func resolveFailurePath(meta api.PreparedMetadata, dbPath string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: PTP failure path requires db path and source path")
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return filepath.Join(tmpDir, "[PTP]upload_failure.html"), nil
}

func loadCookies(ctx context.Context, dbPath string) (map[string]string, error) {
	values, err := cookiepkg.LoadTrackerCookieMap(ctx, dbPath, "PTP")
	return values, fmt.Errorf("trackers: %w", err)
}

func saveCookies(ctx context.Context, dbPath string, client *http.Client, baseURL string) error {
	if client == nil || client.Jar == nil {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("trackers: PTP parse cookie URL: %w", err)
	}
	cookies := make(map[string]string)
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		cookies[strings.TrimSpace(cookie.Name)] = strings.TrimSpace(cookie.Value)
	}
	if len(cookies) == 0 {
		return nil
	}
	return wrapTrackerError(cookiepkg.SaveTrackerCookieMap(ctx, dbPath, "PTP", cookies))
}

func passkeyFromAnnounce(announceURL string) (string, error) {
	parsed, err := url.Parse(announceURL)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP parse announce URL: %w", err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", errors.New("trackers: PTP failed to extract passkey from announce_url")
	}
	return parts[0], nil
}

func resolve2FACode(otpURI string) (string, error) {
	trimmed := strings.TrimSpace(otpURI)
	if trimmed == "" {
		return "", errors.New("otp_uri not configured")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP parse otp_uri: %w", err)
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
		return "", fmt.Errorf("trackers: PTP decode otp secret: %w", err)
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

func resolveType(meta api.PreparedMetadata) string {
	category := strings.ToLower(strings.TrimSpace(meta.ExternalIDs.Category))
	if category == "" {
		category = strings.ToLower(strings.TrimSpace(meta.MediaInfoCategory))
	}
	if meta.ExternalMetadata.IMDB != nil && strings.Contains(strings.ToLower(meta.ExternalMetadata.IMDB.Type), "concert") {
		return "Music"
	}
	if meta.ExternalMetadata.TMDB != nil && (strings.Contains(strings.ToLower(meta.ExternalMetadata.TMDB.Genres), "documentary") || strings.Contains(strings.ToLower(meta.ExternalMetadata.TMDB.Keywords), "documentary")) {
		return "Documentary"
	}
	if category == "movie" {
		return "Feature Film"
	}
	if category == "tv" {
		if meta.TVPack {
			return "Miniseries"
		}
		return "Short Film"
	}
	return "Feature Film"
}

func resolveCodec(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return "BD50"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return "DVD9"
	}
	codec := strings.TrimSpace(meta.VideoCodec)
	if codec == "" {
		codec = strings.TrimSpace(meta.VideoEncode)
	}
	replacer := strings.NewReplacer("AVC", "H.264", "HEVC", "H.265")
	codec = replacer.Replace(codec)
	if meta.HasEncodeSettings {
		codec = strings.ReplaceAll(codec, "H.", "x")
	}
	if codec == "" {
		return "Other"
	}
	return codec
}

func resolveResolution(meta api.PreparedMetadata) (string, string) {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if resolution == "" {
		resolution = "Other"
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		source := strings.TrimSpace(meta.Source)
		source = strings.ReplaceAll(source, " DVD", "")
		if source != "" {
			return source, ""
		}
	}
	if strings.EqualFold(resolution, "OTHER") {
		return "Other", "Other"
	}
	return resolution, ""
}

func resolveContainer(meta api.PreparedMetadata) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		return "m2ts"
	case "DVD":
		return "VOB IFO"
	default:
		switch strings.ToLower(filepath.Ext(firstFile(meta))) {
		case ".mkv":
			return "MKV"
		case ".mp4":
			return "MP4"
		default:
			return "Other"
		}
	}
}

func resolveSource(source string) string {
	switch strings.TrimSpace(source) {
	case "Blu-ray", "BluRay":
		return "Blu-ray"
	case "HD DVD", "HDDVD":
		return "HD-DVD"
	case "Web":
		return "WEB"
	case "HDTV", "UHDTV":
		return "HDTV"
	case "NTSC", "PAL":
		return "DVD"
	default:
		return "OtherR"
	}
}

func resolveSubtitles(meta api.PreparedMetadata) []int {
	if len(meta.SubtitleLanguages) == 0 {
		return []int{44}
	}
	ids := make([]int, 0, len(meta.SubtitleLanguages))
	seen := make(map[int]struct{})
	for _, language := range meta.SubtitleLanguages {
		if value, ok := subtitleIDs[strings.ToLower(strings.TrimSpace(language))]; ok {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			ids = append(ids, value)
		}
	}
	if len(ids) == 0 {
		return []int{44}
	}
	return ids
}

func resolveRemasterTitle(meta api.PreparedMetadata) string {
	parts := make([]string, 0, 8)
	distributor := strings.ToUpper(strings.TrimSpace(meta.Distributor))
	switch distributor {
	case "WARNER ARCHIVE", "WARNER ARCHIVE COLLECTION", "WAC":
		parts = append(parts, "Warner Archive Collection")
	case "CRITERION", "CRITERION COLLECTION", "CC":
		parts = append(parts, "The Criterion Collection")
	case "MASTERS OF CINEMA", "MOC":
		parts = append(parts, "Masters of Cinema")
	}
	edition := strings.TrimSpace(meta.Edition)
	switch {
	case strings.Contains(strings.ToLower(edition), "director's cut"):
		parts = append(parts, "Director's Cut")
	case strings.Contains(strings.ToLower(edition), "extended"):
		parts = append(parts, "Extended Edition")
	case strings.Contains(strings.ToLower(edition), "theatrical"):
		parts = append(parts, "Theatrical Cut")
	case strings.Contains(strings.ToLower(edition), "uncut"):
		parts = append(parts, "Uncut")
	case strings.Contains(strings.ToLower(edition), "unrated"):
		parts = append(parts, "Unrated")
	case edition != "":
		parts = append(parts, edition)
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		parts = append(parts, "Remux")
	}
	audio := strings.TrimSpace(meta.Audio)
	if strings.Contains(audio, "DTS:X") {
		parts = append(parts, "DTS:X")
	}
	if strings.Contains(audio, "Atmos") {
		parts = append(parts, "Dolby Atmos")
	}
	if strings.Contains(audio, "Dual") {
		parts = append(parts, "Dual Audio")
	}
	if strings.Contains(audio, "Dubbed") {
		parts = append(parts, "English Dub")
	}
	if meta.HDR == "" && meta.BitDepth == "10" {
		parts = append(parts, "10-bit")
	}
	if strings.Contains(meta.HDR, "DV") {
		parts = append(parts, "Dolby Vision")
	}
	if strings.Contains(meta.HDR, "HDR10+") {
		parts = append(parts, "HDR10+")
	} else if strings.Contains(meta.HDR, "HDR") {
		parts = append(parts, "HDR10")
	}
	if strings.Contains(meta.HDR, "HLG") {
		parts = append(parts, "HLG")
	}
	if meta.HasCommentary {
		parts = append(parts, "With Commentary")
	}
	return strings.Join(parts, " / ")
}

func resolveGroupTitleYear(meta api.PreparedMetadata) (string, string) {
	title := ""
	year := 0
	if meta.ExternalMetadata.TMDB != nil {
		title = strings.TrimSpace(meta.ExternalMetadata.TMDB.Title)
		year = meta.ExternalMetadata.TMDB.Year
	}
	if title == "" && meta.ExternalMetadata.IMDB != nil {
		title = strings.TrimSpace(meta.ExternalMetadata.IMDB.Title)
		year = meta.ExternalMetadata.IMDB.Year
	}
	if title == "" {
		title = strings.TrimSpace(meta.Release.Title)
	}
	if year == 0 {
		year = meta.Release.Year
	}
	if year == 0 {
		return title, ""
	}
	return title, strconv.Itoa(year)
}

func resolvePoster(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		if value := strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster); value != "" {
			return value
		}
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover)
	}
	return ""
}

func resolveOverview(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		if value := strings.TrimSpace(meta.ExternalMetadata.TMDB.Overview); value != "" {
			return value
		}
	}
	if meta.ExternalMetadata.IMDB != nil {
		if value := strings.TrimSpace(meta.ExternalMetadata.IMDB.Plot); value != "" {
			return value
		}
	}
	return ""
}

func resolveTrailer(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveDirectors(meta api.PreparedMetadata) []string {
	if meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.Directors) > 0 {
		return append([]string{}, meta.ExternalMetadata.TMDB.Directors...)
	}
	if meta.ExternalMetadata.IMDB != nil && len(meta.ExternalMetadata.IMDB.Directors) > 0 {
		names := make([]string, 0, len(meta.ExternalMetadata.IMDB.Directors))
		for _, person := range meta.ExternalMetadata.IMDB.Directors {
			if strings.TrimSpace(person.Name) != "" {
				names = append(names, strings.TrimSpace(person.Name))
			}
		}
		return names
	}
	return nil
}

func resolveTags(meta api.PreparedMetadata) string {
	values := make([]string, 0, 8)
	if meta.ExternalMetadata.TMDB != nil {
		for item := range strings.SplitSeq(meta.ExternalMetadata.TMDB.Genres, ",") {
			trimmed := strings.ToLower(strings.TrimSpace(item))
			if trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}
	if len(values) == 0 && strings.TrimSpace(meta.Release.Genre) != "" {
		for item := range strings.SplitSeq(meta.Release.Genre, ",") {
			trimmed := strings.ToLower(strings.TrimSpace(item))
			if trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}
	if len(values) == 0 {
		values = append(values, "action")
	}
	seen := make(map[string]struct{}, len(values))
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		filtered = append(filtered, value)
	}
	return strings.Join(filtered, ", ")
}

func firstFile(meta api.PreparedMetadata) string {
	if len(meta.FileList) > 0 {
		return meta.FileList[0]
	}
	return meta.SourcePath
}

func normalizedAnnounceURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "http://please.passthepopcorn.me") {
		return strings.Replace(trimmed, "http://", "https://", 1)
	}
	return trimmed
}

func extractAlertError(body string) string {
	start := strings.Index(body, `alert alert--error`)
	if start == -1 {
		return ""
	}
	segment := body[start:]
	end := strings.Index(segment, "</div>")
	if end != -1 {
		segment = segment[:end]
	}
	return stripTags(segment)
}

func stripTags(value string) string {
	inTag := false
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				builder.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func compactError(value string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(trimmed) > 220 {
		return trimmed[:220]
	}
	return trimmed
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.Itoa(int(typed))
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func joinInts(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
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

func readTextFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP read text file: %w", err)
	}
	return string(payload), nil
}

func buildQuestionnaire(meta api.PreparedMetadata, groupID string) *api.TrackerQuestionnaire {
	if strings.TrimSpace(groupID) != "" {
		return nil
	}
	title, year := resolveGroupTitleYear(meta)
	fields := []api.TrackerQuestionnaireField{
		{Key: "title", Label: "Group Title", Kind: "text", Value: title, Required: true},
		{Key: "year", Label: "Year", Kind: "text", Value: year, Required: false, Placeholder: "Release year"},
		{Key: "poster", Label: "Poster URL", Kind: "text", Value: resolvePoster(meta), Required: true},
		{Key: "tags", Label: "Tags", Kind: "text", Value: resolveTags(meta), Required: true, Placeholder: "Comma separated tags"},
		{Key: "trailer", Label: "Trailer URL", Kind: "text", Value: resolveTrailer(meta), Required: false, Placeholder: "YouTube trailer URL"},
		{Key: "album_desc", Label: "Group Description", Kind: "textarea", Value: resolveOverview(meta), Required: false},
	}
	return &api.TrackerQuestionnaire{
		Tracker: "PTP",
		Fields:  fields,
	}
}

func questionnaireAnswers(meta api.PreparedMetadata, tracker string) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers[strings.ToUpper(strings.TrimSpace(tracker))]
}

var subtitleIDs = map[string]int{
	"arabic":               22,
	"brazilian portuguese": 49,
	"bulgarian":            29,
	"chinese":              14,
	"croatian":             23,
	"czech":                30,
	"danish":               10,
	"dutch":                9,
	"english":              3,
	"english - forced":     50,
	"english forced":       50,
	"english intertitles":  51,
	"estonian":             38,
	"finnish":              15,
	"french":               5,
	"german":               6,
	"greek":                26,
	"hebrew":               40,
	"hindi":                41,
	"hungarian":            24,
	"icelandic":            28,
	"indonesian":           47,
	"italian":              16,
	"japanese":             8,
	"korean":               19,
	"latvian":              37,
	"lithuanian":           39,
	"norwegian":            12,
	"polish":               17,
	"portuguese":           21,
	"romanian":             13,
	"russian":              7,
	"serbian":              31,
	"slovak":               42,
	"slovenian":            43,
	"spanish":              4,
	"swedish":              11,
	"thai":                 20,
	"turkish":              18,
	"ukrainian":            34,
	"vietnamese":           25,
}
