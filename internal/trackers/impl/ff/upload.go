// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://www.funfile.org"
	loginURL   = baseURL + "/takelogin.php"
	uploadURL  = baseURL + "/takeupload.php"
	torrentURL = baseURL + "/details.php?id="
	sourceFlag = "FunFile"
)

var idPattern = regexp.MustCompile(`details\.php\?id=(\d+)`)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	extraFiles    []commonhttp.FileField
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: FF %s", state.blockedReason)
	}
	files := append([]commonhttp.FileField{{
		FieldName: "file",
		FileName:  state.releaseName + ".torrent",
		Path:      state.torrentPath,
	}}, state.extraFiles...)
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FF request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)
	client := httpclient.CloneWithTimeout(&http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}, httpclient.DefaultTimeout)
	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: FF upload request: %w", err)
	}
	defer resp.Body.Close()
	location := resp.Header.Get("Location")
	match := idPattern.FindStringSubmatch(location)
	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusFound && len(match) >= 2 {
		id := match[1]
		tURL := torrentURL + id
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "FF")
			if err != nil {
				return api.UploadSummary{}, err
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, err
			}
		}
		return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{Tracker: "FF", TorrentID: id, TorrentURL: tURL, DownloadURL: tURL, TorrentPath: artifactPath}}}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "FF", "upload_failure", bodyBytes, ".html")
	return api.UploadSummary{}, fmt.Errorf("trackers: FF upload failed status=%d", resp.StatusCode)
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
		Tracker:          "FF",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "ff",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Files:            []api.TrackerDryRunFile{{Field: "file", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, dryRun bool) (uploadState, []*http.Cookie, error) {
	cookies, err := resolveCookies(ctx, req.Logger, req.TrackerConfig, req.AppConfig.MainSettings.DBPath, dryRun)
	if err != nil {
		return uploadState{}, nil, err
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description, err := buildDescription(req.Meta, assets)
	if err != nil {
		return uploadState{}, nil, err
	}
	fields := map[string]string{
		"MAX_FILE_SIZE": "10000000",
		"type":          resolveTypeID(req.Meta),
		"tags":          "",
		"descr":         description,
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(req.Meta)))
	switch {
	case req.Meta.Anime:
		fields["anime_type"] = resolveAnimeType(req.Meta)
		fields["anime_source"] = resolveAnimeSource(req.Meta)
		fields["anime_container"] = "mkv"
		fields["anime_v_res"] = req.Meta.Release.Resolution
		fields["anime_v_dar"] = "16_9"
		fields["anime_v_codec"] = resolveAnimeVCodec(req.Meta)
	case category == "MOVIE":
		fields["movie_type"] = resolveMovieType(req.Meta)
		fields["movie_source"] = resolveMovieSource(req.Meta)
		fields["movie_imdb"] = resolveIMDbURL(req.Meta)
		fields["pack"] = "0"
	default:
		fields["tv_type"] = resolveTVType(req.Meta)
		fields["tv_source"] = resolveTVSource(req.Meta)
		fields["tv_imdb"] = resolveIMDbURL(req.Meta)
		fields["pack"] = "0"
		if req.Meta.TVPack {
			fields["pack"] = "1"
		}
	}
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		releaseName: resolveName(req.Meta),
		fields:      fields,
		extraFiles:  resolveExtraFiles(ctx, req.Meta),
	}
	return state, cookies, nil
}

func resolveCookies(ctx context.Context, logger api.Logger, cfg config.TrackerConfig, dbPath string, dryRun bool) ([]*http.Cookie, error) {
	loaded, err := cookies.LoadTrackerHTTPCookies(ctx, dbPath, "FF", "www.funfile.org")
	if err != nil {
		if logger != nil {
			logger.Debugf("trackers: LoadTrackerHTTPCookies failed for FF/www.funfile.org, dbPath=%s: %v", dbPath, err)
		}
	} else if len(loaded) > 0 {
		return loaded, nil
	}
	if dryRun {
		if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
			return nil, errors.New("trackers: FF cookies not found")
		}
		return []*http.Cookie{{Name: "dryrun", Value: "1", Domain: ".funfile.org", Path: "/"}}, nil
	}
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errors.New("trackers: FF cookie invalid/missing and username/password not configured")
	}
	data := url.Values{}
	data.Set("returnto", "/index.php")
	data.Set("username", strings.TrimSpace(cfg.Username))
	data.Set("password", strings.TrimSpace(cfg.Password))
	data.Set("login", "Login")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "upbrr")
	client := httpclient.CloneWithTimeout(&http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}, httpclient.DefaultTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("trackers: FF login failed status=%d", resp.StatusCode)
	}
	return resp.Cookies(), nil
}

func buildDescription(meta api.PreparedMetadata, assets trackers.DescriptionAssets) (string, error) {
	if strings.TrimSpace(assets.Description) != "" {
		return bbcode.FinalizeTrackerDescription("FF", strings.TrimSpace(assets.Description)), nil
	}
	return "", nil
}

func resolveExtraFiles(ctx context.Context, meta api.PreparedMetadata) []commonhttp.FileField {
	files := make([]commonhttp.FileField, 0, 2)
	if ctx == nil {
		return files
	}
	if poster := resolvePoster(meta); poster != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, poster, nil)
		if err == nil {
			resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
			if err == nil {
				defer resp.Body.Close()
				if body, err := io.ReadAll(resp.Body); err == nil && len(body) > 0 {
					files = append(files, commonhttp.FileField{FieldName: "poster", FileName: "poster.jpg", Content: body})
				}
			}
		}
	}
	dir := filepath.Dir(firstNonEmpty(meta.MediaInfoTextPath, meta.SourcePath))
	if payload, path, err := commonhttp.ReadFirstMatching(dir, "*.nfo"); err == nil {
		files = append(files, commonhttp.FileField{FieldName: "nfo", FileName: filepath.Base(path), Content: payload})
	}
	return files
}

func resolveTypeID(meta api.PreparedMetadata) string {
	if meta.Anime {
		return "44"
	}
	if strings.EqualFold(categoryOf(meta), "TV") {
		return "7"
	}
	return "19"
}

func resolveMovieType(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.Source), "DVD") {
		return "DVDR"
	}
	if strings.Contains(strings.ToLower(meta.VideoCodec), "hevc") || strings.Contains(strings.ToLower(meta.VideoEncode), "265") {
		return "x265"
	}
	return "x264"
}

func resolveTVType(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.Source), "DVD") {
		return "DVDR"
	}
	if strings.Contains(strings.ToLower(meta.Source), "web") {
		if isSD(meta) {
			return "Web-SD"
		}
		return "Web-HD"
	}
	if strings.Contains(strings.ToLower(meta.VideoCodec), "hevc") || strings.Contains(strings.ToLower(meta.VideoEncode), "265") {
		if isSD(meta) {
			return "x265-SD"
		}
		return "x265-HD"
	}
	if isSD(meta) {
		return "x264-SD"
	}
	return "x264-HD"
}

func resolveAnimeType(meta api.PreparedMetadata) string {
	if meta.SeasonInt == 0 {
		return "TVSpecial"
	}
	if strings.EqualFold(categoryOf(meta), "MOVIE") {
		return "Movie"
	}
	return "TVSeries"
}

func resolveMovieSource(meta api.PreparedMetadata) string {
	switch strings.ToLower(strings.TrimSpace(meta.Source)) {
	case "dvd":
		return "DVD"
	case "blu-ray", "bluray":
		return "BluRay"
	case "hdtv":
		return "HDTV"
	case "webrip", "webdl", "web":
		return "WebRIP"
	default:
		return "BluRay"
	}
}

func resolveTVSource(meta api.PreparedMetadata) string {
	switch strings.ToLower(strings.TrimSpace(meta.Source)) {
	case "dvd":
		return "DVD"
	case "blu-ray", "bluray":
		return "BluRay"
	case "hdtv":
		return "HDTV"
	case "webrip", "webdl", "web":
		return "WebRIP"
	default:
		return "HDTV"
	}
}

func resolveAnimeSource(meta api.PreparedMetadata) string {
	switch strings.ToLower(strings.TrimSpace(meta.Source)) {
	case "dvd":
		return "DVD"
	case "blu-ray", "bluray":
		return "BluRay"
	default:
		return "Anime Series"
	}
}

func resolveAnimeVCodec(meta api.PreparedMetadata) string {
	if strings.Contains(strings.ToLower(meta.VideoCodec), "vc-1") {
		return "VC1"
	}
	if strings.Contains(strings.ToLower(meta.VideoEncode), "h.264") {
		return "h264"
	}
	return "x264"
}

func resolveName(meta api.PreparedMetadata) string {
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName)
	}
	return strings.ReplaceAll(firstNonEmpty(meta.ReleaseNameClean, meta.ReleaseName, meta.Filename), " ", ".")
}

func resolveIMDbURL(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbURL)
	}
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.ExternalIDs.IMDBID)
	}
	return ""
}

func resolvePoster(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster)
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover)
	}
	return ""
}

func categoryOf(meta api.PreparedMetadata) string {
	if category := strings.TrimSpace(meta.ExternalIDs.Category); category != "" {
		return category
	}
	return strings.TrimSpace(meta.MediaInfoCategory)
}

func isSD(meta api.PreparedMetadata) bool {
	return strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "480p") || strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "576p")
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
