// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var tokenPattern = regexp.MustCompile(`name="csrfToken"\s+value="([^"]+)"`)
var successPattern = regexp.MustCompile(`details\.php\?id=([a-zA-Z0-9]+)|Upload successful!`)

type uploadState struct {
	baseURL       string
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	nfo           *commonhttp.FileField
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT %s", state.blockedReason)
	}
	files := []commonhttp.FileField{{FieldName: "torrent", FileName: filepath.Base(state.torrentPath), Path: state.torrentPath}}
	if state.nfo != nil {
		files = append(files, *state.nfo)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.baseURL+"/upload.php", bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDT upload request: %w", err)
	}
	defer resp.Body.Close()
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, _ := io.ReadAll(resp.Body)
	combined := finalURL + "\n" + string(responseBody)
	id := ""
	if match := regexp.MustCompile(`details\.php\?id=([a-zA-Z0-9]+)`).FindStringSubmatch(combined); len(match) >= 2 {
		id = match[1]
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && successPattern.MatchString(combined) {
		tURL := finalURL
		if id != "" && !strings.Contains(tURL, "details.php?id=") {
			tURL = state.baseURL + "/details.php?id=" + id
		}
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "HDT")
			if err != nil {
				return api.UploadSummary{}, err
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, tURL, "hd-torrents.org"); err != nil {
				return api.UploadSummary{}, err
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "HDT",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: tURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "HDT", "upload_failure", responseBody, ".html")
	return api.UploadSummary{}, fmt.Errorf("trackers: HDT upload failed status=%d", resp.StatusCode)
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
		Tracker:          "HDT",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "hdt",
		Description:      state.description,
		Endpoint:         state.baseURL + "/upload.php",
		Payload:          cloneFields(state.fields),
		Files:            []api.TrackerDryRunFile{{Field: "torrent", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, dryRun bool) (uploadState, []*http.Cookie, error) {
	base := resolveBaseURL(req.TrackerConfig.URL)
	cookies, err := loadCookies(req.AppConfig.MainSettings.DBPath, base)
	if err != nil {
		return uploadState{}, nil, err
	}
	token := strings.Join([]string{"dry", "run", "token"}, "-")
	if !dryRun {
		token, err = fetchToken(ctx, base, cookies)
		if err != nil {
			return uploadState{}, nil, err
		}
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
		"filename":  resolveName(req.Meta),
		"category":  strconv.Itoa(resolveCategoryID(req.Meta)),
		"info":      description,
		"csrfToken": token,
		"season":    boolString(req.Meta.TVPack),
		"anonymous": boolString(req.TrackerConfig.Anon),
	}
	if req.Meta.Is3D != "" {
		fields["3d"] = "true"
	}
	hdr := strings.ToUpper(strings.TrimSpace(req.Meta.HDR))
	if strings.Contains(hdr, "HDR10+") {
		fields["HDR10"] = "true"
		fields["HDR10Plus"] = "true"
	} else if strings.Contains(hdr, "HDR") {
		fields["HDR10"] = "true"
	}
	if strings.Contains(hdr, "DV") {
		fields["DolbyVision"] = "true"
	}
	if imdb := resolveIMDbURL(req.Meta); imdb != "" {
		fields["infosite"] = imdb + "/"
	}
	state := uploadState{
		baseURL:     base,
		torrentPath: torrentPath,
		description: description,
		releaseName: fields["filename"],
		fields:      fields,
	}
	if strings.TrimSpace(req.Meta.Release.Resolution) == "" {
		state.blockedReason = "missing resolution"
	}
	if file, ok := resolveNFO(req.Meta); ok {
		state.nfo = &file
	}
	return state, cookies, nil
}

func resolveBaseURL(configURL string) string {
	trimmed := strings.TrimSpace(configURL)
	if trimmed == "" {
		return "https://hd-torrents.me"
	}
	parsed, err := url.Parse(trimmed)
	if err == nil && parsed.Host != "" {
		return "https://" + parsed.Host
	}
	return strings.TrimRight(trimmed, "/")
}

func loadCookies(dbPath string, baseURL string) ([]*http.Cookie, error) {
	host := "hd-torrents.me"
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Host != "" {
		host = parsed.Host
	}
	for _, candidate := range commonhttp.CookiePathCandidates(dbPath, "HDT", ".txt") {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return commonhttp.LoadNetscapeCookies(candidate, host)
		}
	}
	return nil, errors.New("trackers: HDT cookie file not found")
}

func fetchToken(ctx context.Context, baseURL string, cookies []*http.Cookie) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/upload.php", nil)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("trackers: HDT token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	match := tokenPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", errors.New("trackers: HDT csrf token not found")
	}
	return strings.TrimSpace(match[1]), nil
}

func resolveCategoryID(meta api.PreparedMetadata) int {
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	if category == "TV" {
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
			if resolution == "2160p" {
				return 72
			}
			return 59
		}
		if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
			if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p" {
				return 73
			}
			return 60
		}
		switch resolution {
		case "2160p":
			return 65
		case "1080p", "1080i":
			return 30
		default:
			return 38
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") || strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
		if resolution == "2160p" {
			return 70
		}
		return 1
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		if strings.EqualFold(strings.TrimSpace(meta.UHD), "UHD") && resolution == "2160p" {
			return 71
		}
		return 2
	}
	switch resolution {
	case "2160p":
		return 64
	case "1080p", "1080i":
		return 5
	default:
		return 3
	}
}

func buildDescription(meta api.PreparedMetadata, assets trackers.DescriptionAssets) (string, error) {
	parts := make([]string, 0, 6)
	if logo := resolveLogo(meta); logo != "" {
		parts = append(parts, "[center][img]"+logo+"[/img][/center]")
	}
	if media := resolveMedia(meta); media != "" {
		parts = append(parts, "[left][font=consolas]"+media+"[/font][/left]")
	}
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}
	if shots := screenshots(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}
	return bbcode.FinalizeTrackerDescription("HDT", strings.TrimSpace(strings.Join(parts, "\n\n"))), nil
}

func resolveName(meta api.PreparedMetadata) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if strings.EqualFold(strings.TrimSpace(meta.Type), "WEBDL") || strings.EqualFold(strings.TrimSpace(meta.Type), "WEBRIP") || strings.EqualFold(strings.TrimSpace(meta.Type), "ENCODE") {
		name = strings.Replace(name, meta.Audio, strings.Replace(meta.Audio, " ", "", 1), 1)
	}
	name = strings.ReplaceAll(name, " DV ", " DoVi ")
	name = strings.ReplaceAll(name, "BluRay REMUX", "Blu-ray Remux")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.ReplaceAll(name, ":", "")
	return strings.TrimSpace(name)
}

func resolveLogo(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func resolveMedia(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if summary, ok := meta.BDInfo["summary"].(string); ok {
			return strings.TrimSpace(summary)
		}
	}
	return firstNonEmpty(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
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

func resolveNFO(meta api.PreparedMetadata) (commonhttp.FileField, bool) {
	dir := filepath.Dir(firstNonEmpty(meta.MediaInfoTextPath, meta.SourcePath))
	payload, path, err := commonhttp.ReadFirstMatching(dir, "*.nfo")
	if err != nil {
		return commonhttp.FileField{}, false
	}
	return commonhttp.FileField{FieldName: "nfos", FileName: filepath.Base(path), Content: payload}, true
}

func screenshots(images []api.ScreenshotImage) string {
	parts := make([]string, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.RawURL) == "" || strings.TrimSpace(image.ImgURL) == "" {
			continue
		}
		parts = append(parts, "<a href='"+image.RawURL+"'><img src='"+image.ImgURL+"' height=137></a>")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[center]\n" + strings.Join(parts, " ") + "[/center]"
}

func categoryOf(meta api.PreparedMetadata) string {
	if category := strings.TrimSpace(meta.ExternalIDs.Category); category != "" {
		return category
	}
	return strings.TrimSpace(meta.MediaInfoCategory)
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
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
