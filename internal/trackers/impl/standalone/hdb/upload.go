// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	cookiepkg "github.com/autobrr/upbrr/internal/cookies"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	hdbBaseURL    = "https://hdbits.org"
	hdbUploadPath = "/upload/upload"
)

var hdbSuccessURLPattern = regexp.MustCompile(`(?i)details\.php\?id=(\d+)&uploaded=\d+`)

type preparedUploadState struct {
	torrentPath string
	description string
	fields      map[string]string
	uploadURL   string
	cookies     []*http.Cookie
	body        []byte
	contentType string
	passkey     string
	client      *http.Client
}

func uploadAt(ctx context.Context, req trackers.PreparationInput, baseURL string, httpClient *http.Client) (api.UploadSummary, error) {
	req.Intent = trackers.PreparationIntentUpload
	plan, failure := trackers.PrepareAdapter(ctx, req, nil, func(ctx context.Context, input trackers.PreparationInput) (trackers.PreparedOperation, error) {
		return prepareUploadAt(ctx, input, baseURL, httpClient)
	})
	if failure != nil {
		return api.UploadSummary{}, failure
	}
	summary, err := plan.Submit(ctx)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDB submit prepared upload: %w", err)
	}
	return summary, nil
}

func prepareUploadAt(
	ctx context.Context,
	req trackers.PreparationInput,
	baseURL string,
	httpClient *http.Client,
) (trackers.PreparedOperation, error) {
	select {
	case <-ctx.Done():
		return trackers.PreparedOperation{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	username := strings.TrimSpace(req.TrackerConfig.Username)
	passkey := strings.TrimSpace(req.TrackerConfig.Passkey)
	if username == "" || passkey == "" {
		return trackers.PreparedOperation{}, errors.New("trackers: HDB missing username/passkey")
	}

	category := hdbCategoryID(req.Meta)
	codec := hdbCodecID(req.Meta)
	medium := hdbMediumID(req.Meta)
	if category == 0 || codec == 0 || medium == 0 {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: HDB mapping failed category=%d codec=%d medium=%d", category, codec, medium)
	}

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: HDB description assets: %w", err)
		}
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	descriptionText := strings.TrimSpace(assets.Description)
	if !assets.Final {
		descriptionText, err = BuildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets.Description, assets.MenuImages, assets.Screenshots)
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}

	torrentPath, err := resolveTorrentPath(req.Meta, req.Runtime.DBPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}

	uploadURL := strings.TrimRight(baseURL, "/")
	uploadURL += hdbUploadPath

	fields := buildUploadFields(req.Meta, req.Runtime.DescriptionConfig(), category, codec, medium, descriptionText)
	preview := standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "HDB",
		ReleaseName:      resolveUploadName(req.Meta),
		DescriptionGroup: "hdb",
		Description:      descriptionText,
		Endpoint:         uploadURL,
		Payload:          fields,
		Files: []api.TrackerDryRunFile{{
			Field:   "file",
			Path:    torrentPath,
			Present: strings.TrimSpace(torrentPath) != "",
		}},
	})
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}

	cookies, err := resolveHDBCookies(ctx, req.Runtime.DBPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	body, contentType, err := buildMultipartPayload(fields, torrentPath)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 40 * time.Second}
	}
	state := preparedUploadState{
		torrentPath: torrentPath,
		description: descriptionText,
		fields:      fields,
		uploadURL:   uploadURL,
		cookies:     cookies,
		body:        body,
		contentType: contentType,
		passkey:     passkey,
		client:      client,
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state)
	}, nil), nil
}

func submitPreparedUpload(ctx context.Context, req trackers.PreparationInput, state preparedUploadState) (api.UploadSummary, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.uploadURL, bytes.NewReader(state.body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDB request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", state.contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	for _, cookie := range state.cookies {
		httpReq.AddCookie(cookie)
	}

	resp, err := state.client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDB upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	matches := hdbSuccessURLPattern.FindStringSubmatch(finalURL)
	if len(matches) < 2 {
		bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return api.UploadSummary{}, commonhttp.UploadHTTPErrorWithURL("HDB", resp.StatusCode, finalURL, bodyPreview)
	}

	torrentID := strings.TrimSpace(matches[1])
	trackerTorrentPath := ""
	if torrentID != "" {
		trackerTorrentPath, err = resolveTrackerTorrentPath(req.Meta, req.Runtime.DBPath, "HDB")
		if err != nil {
			return api.UploadSummary{}, err
		}
		if err := downloadPersonalizedTorrent(
			ctx,
			state.client,
			state.uploadURL,
			req.Meta,
			trackerTorrentPath,
			torrentID,
			state.passkey,
			state.cookies,
		); err != nil &&
			req.Logger != nil {
			req.Logger.Warnf("trackers: HDB torrent redownload failed: %v", err)
		}
	}

	downloadURL := buildHDBDownloadURL(state.uploadURL, req.Meta, torrentID, state.passkey)
	return api.UploadSummary{
		Uploaded: 1,
		UploadedTorrents: []api.UploadedTorrent{{
			Tracker:     "HDB",
			TorrentID:   torrentID,
			DownloadURL: downloadURL,
			TorrentPath: trackerTorrentPath,
		}},
	}, nil
}

func buildUploadFields(meta api.UploadSubject, appConfig config.Config, categoryID int, codecID int, mediumID int, description string) map[string]string {
	fields := map[string]string{
		"name":     resolveUploadName(meta),
		"category": strconv.Itoa(categoryID),
		"codec":    strconv.Itoa(codecID),
		"medium":   strconv.Itoa(mediumID),
		"origin":   "0",
		"descr":    strings.TrimSpace(description),
		"techinfo": "",
	}

	if trackers.IsInternalGroup(appConfig, "HDB", meta) {
		fields["origin"] = "1"
	}

	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if strings.TrimSpace(meta.MediaInfoTextPath) != "" {
			content, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
			if err == nil {
				fields["techinfo"] = strings.TrimSpace(string(content))
			}
		}
	}

	if isHDBTVCategory(meta) && meta.Identity.TVDBID != 0 {
		fields["tvdb"] = strconv.Itoa(meta.Identity.TVDBID)
	}
	if imdb := resolveIMDbURL(meta); imdb != "" {
		fields["imdb"] = imdb
	} else {
		fields["imdb"] = "0"
	}
	if isHDBTVCategory(meta) {
		season := meta.SeasonInt
		episode := meta.EpisodeInt
		if season <= 0 {
			season = 1
		}
		if episode <= 0 {
			episode = 1
		}
		fields["tvdb_season"] = strconv.Itoa(season)
		fields["tvdb_episode"] = strconv.Itoa(episode)
	}

	return fields
}

// isHDBTVCategory reports whether HDB upload payloads may include TVDB fields.
// Canonical movie identity suppresses TVDB fields even when episode facts exist.
func isHDBTVCategory(meta api.UploadSubject) bool {
	category, err := meta.Identity.RequireCategory()
	return err == nil && category == api.CanonicalCategoryTV
}

func resolveUploadName(meta api.UploadSubject) string {
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

func resolveIMDbURL(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil {
		if value := strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL); value != "" {
			if !strings.HasSuffix(value, "/") {
				return value + "/"
			}
			return value
		}
	}
	if meta.Identity.IMDBID != 0 {
		return fmt.Sprintf("https://www.imdb.com/title/tt%07d/", meta.Identity.IMDBID)
	}
	return ""
}

func buildMultipartPayload(fields map[string]string, torrentPath string) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("trackers: HDB write multipart field %q: %w", key, err)
		}
	}

	file, err := os.Open(torrentPath)
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: HDB open torrent file: %w", err)
	}
	defer file.Close()

	part, err := writer.CreateFormFile("file", filepath.Base(torrentPath))
	if err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: HDB create torrent form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("trackers: HDB copy torrent file: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("trackers: HDB close multipart writer: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func resolveTorrentPath(meta api.UploadSubject, dbPath string) (string, error) {
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
			tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
			if err == nil {
				guessed := filepath.Join(tmpDir, base+".torrent")
				if info, err := os.Stat(guessed); err == nil && !info.IsDir() {
					return guessed, nil
				}
			}
		}
	}

	return "", errors.New("trackers: HDB torrent file not found")
}

func resolveTrackerTorrentPath(meta api.UploadSubject, dbPath string, tracker string) (string, error) {
	if strings.TrimSpace(dbPath) == "" || strings.TrimSpace(meta.SourcePath) == "" {
		return "", errors.New("trackers: HDB tracker torrent path requires db path and source path")
	}

	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: HDB tmp root: %w", err)
	}
	tmpDir, base, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return "", fmt.Errorf("trackers: HDB tmp release dir: %w", err)
	}
	name := strings.ToLower(strings.TrimSpace(tracker))
	name = strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(name)
	if name == "" {
		name = "tracker"
	}
	return filepath.Join(tmpDir, base+"."+name+".torrent"), nil
}

func resolveHDBCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	values, err := cookiepkg.LoadTrackerHTTPCookies(ctx, dbPath, "HDB", "hdbits.org")
	if err != nil {
		return values, fmt.Errorf("trackers: HDB load cookies: %w", err)
	}
	return values, nil
}

func downloadPersonalizedTorrent(
	ctx context.Context,
	client *http.Client,
	uploadURL string,
	meta api.UploadSubject,
	torrentPath string,
	torrentID string,
	passkey string,
	cookies []*http.Cookie,
) error {
	downloadURL := buildHDBDownloadURL(uploadURL, meta, torrentID, passkey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("trackers: HDB create personalized torrent request: %w", err)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("trackers: HDB download personalized torrent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("trackers: HDB read personalized torrent response: %w", err)
	}
	if len(body) == 0 {
		return errors.New("empty torrent response")
	}
	if err := os.MkdirAll(filepath.Dir(torrentPath), 0o700); err != nil {
		return fmt.Errorf("trackers: HDB create torrent output dir: %w", err)
	}
	if err := os.WriteFile(torrentPath, body, 0o600); err != nil {
		return fmt.Errorf("trackers: HDB write torrent output: %w", err)
	}
	return nil
}

func buildHDBDownloadURL(uploadURL string, meta api.UploadSubject, torrentID string, passkey string) string {
	if strings.TrimSpace(torrentID) == "" || strings.TrimSpace(passkey) == "" {
		return ""
	}
	base := strings.TrimSuffix(uploadURL, hdbUploadPath)
	filePart := pathutil.Base(meta.SourcePath)
	if filePart == "" || filePart == "." || filePart == string(filepath.Separator) {
		filePart = "download"
	}
	return fmt.Sprintf(
		"%s/download.php/%s?id=%s&passkey=%s",
		strings.TrimRight(base, "/"),
		url.PathEscape(filePart),
		url.QueryEscape(torrentID),
		url.QueryEscape(passkey),
	)
}

func hdbCategoryID(meta api.UploadSubject) int {
	category, _ := meta.Identity.RequireCategory()
	switch category {
	case api.CanonicalCategoryMovie:
		return 1
	case api.CanonicalCategoryTV:
		return 2
	case api.CanonicalCategoryUnknown:
	}
	genres := ""
	keywords := ""
	if meta.ProviderMetadata.TMDB != nil {
		genres = strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.Genres))
		keywords = strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.TMDB.Keywords))
	}
	if strings.Contains(genres, "documentary") || strings.Contains(keywords, "documentary") {
		return 3
	}
	if meta.ProviderMetadata.IMDB != nil {
		imdbType := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Type))
		imdbGenres := strings.ToLower(strings.TrimSpace(meta.ProviderMetadata.IMDB.Genres))
		if strings.Contains(imdbType, "concert") || (strings.Contains(imdbType, "video") && strings.Contains(imdbGenres, "music")) {
			return 4
		}
	}
	return 0
}

func hdbCodecID(meta api.UploadSubject) int {
	codec := strings.ToUpper(strings.TrimSpace(meta.VideoCodec))
	if codec == "" {
		codec = strings.ToUpper(strings.TrimSpace(meta.VideoEncode))
	}
	switch codec {
	case "AVC", "H.264":
		return 1
	case "MPEG-2":
		return 2
	case "VC-1":
		return 3
	case "XVID":
		return 4
	case "HEVC", "H.265":
		return 5
	case "VP9":
		return 6
	default:
		return 0
	}
}

func hdbMediumID(meta api.UploadSubject) int {
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	contentType := resolveHDBType(meta)
	if discType == "BDMV" || discType == "HD DVD" {
		return 1
	}
	if contentType == "HDTV" {
		if meta.HasEncodeSettings {
			return 3
		}
		return 4
	}
	switch contentType {
	case "ENCODE", "WEBRIP":
		return 3
	case "REMUX":
		return 5
	case "WEBDL":
		return 6
	default:
		return 0
	}
}

func resolveHDBType(meta api.UploadSubject) string {
	typeValue := normalizeHDBType(meta.Type)
	if typeValue == "" || isHDBCategoryType(typeValue) {
		if meta.ReleaseNameOverrides.Type != nil {
			typeValue = normalizeHDBType(*meta.ReleaseNameOverrides.Type)
		}
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		typeValue = normalizeHDBType(meta.Release.Type)
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		if strings.TrimSpace(meta.DiscType) != "" {
			typeValue = "DISC"
		}
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		typeValue = inferHDBTypeFromSource(meta.Source)
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		typeValue = inferHDBTypeFromPath(meta.SourcePath)
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		if strings.TrimSpace(meta.VideoEncode) != "" {
			typeValue = "ENCODE"
		}
	}
	if typeValue == "" || isHDBCategoryType(typeValue) {
		if strings.TrimSpace(meta.VideoCodec) != "" || strings.TrimSpace(meta.Release.Resolution) != "" || strings.TrimSpace(meta.Release.Ext) != "" {
			typeValue = "ENCODE"
		}
	}
	return typeValue
}

func normalizeHDBType(value string) string {
	upper := strings.ToUpper(strings.TrimSpace(value))
	if upper == "" {
		return ""
	}
	upper = strings.ReplaceAll(upper, "-", "")
	upper = strings.ReplaceAll(upper, " ", "")
	upper = strings.ReplaceAll(upper, "_", "")
	switch upper {
	case "WEBDL":
		return "WEBDL"
	case "WEBRIP":
		return "WEBRIP"
	}
	return upper
}

func isHDBCategoryType(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	return upper == "MOVIE" || upper == "TV"
}

func inferHDBTypeFromSource(source string) string {
	upper := normalizeHDBType(source)
	switch {
	case strings.Contains(upper, "WEBDL"):
		return "WEBDL"
	case strings.Contains(upper, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(upper, "HDTV"):
		return "HDTV"
	}
	return ""
}

func inferHDBTypeFromPath(path string) string {
	base := strings.ToUpper(strings.TrimSpace(path))
	compact := strings.NewReplacer(".", "", "-", "", "_", "", " ", "").Replace(base)
	switch {
	case strings.Contains(compact, "REMUX"):
		return "REMUX"
	case strings.Contains(compact, "WEBDL"):
		return "WEBDL"
	case strings.Contains(compact, "WEBRIP"):
		return "WEBRIP"
	case strings.Contains(compact, "HDTV"):
		return "HDTV"
	case strings.Contains(compact, "DVDRIP"):
		return "DVDRIP"
	}
	return ""
}
