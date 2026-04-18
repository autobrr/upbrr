// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://hd-space.org"
	uploadURL  = baseURL + "/index.php?page=upload"
	torrentURL = baseURL + "/index.php?page=torrent-details&id="
	sourceFlag = "HD-Space"
)

var idPattern = regexp.MustCompile(`download\.php\?id=([a-zA-Z0-9]+)`)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	nfo           *commonhttp.FileField
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDS %s", state.blockedReason)
	}
	files := []commonhttp.FileField{{
		FieldName: "torrent",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}}
	if state.nfo != nil {
		files = append(files, *state.nfo)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return api.UploadSummary{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDS request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: HDS upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, _ := io.ReadAll(resp.Body)
	combined := finalURL + "\n" + string(responseBody)
	match := idPattern.FindStringSubmatch(combined)
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && len(match) >= 2 {
		id := strings.TrimSpace(match[1])
		tURL := torrentURL + id
		artifactPath := ""
		if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "HDS")
			if err != nil {
				return api.UploadSummary{}, err
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announceURL, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, err
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "HDS",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: baseURL + "/download.php?id=" + id,
				TorrentPath: artifactPath,
			}},
		}, nil
	}

	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "HDS", "upload_failure", responseBody, ".html")
	return api.UploadSummary{}, fmt.Errorf("trackers: HDS upload failed status=%d", resp.StatusCode)
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, _, err := prepareUploadState(ctx, req)
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
		Tracker:          "HDS",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "hds",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Files:            []api.TrackerDryRunFile{{Field: "torrent", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, []*http.Cookie, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	cookies, err := loadCookies(ctx, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description, err := buildDescription(req, assets)
	if err != nil {
		return uploadState{}, nil, err
	}
	fields := map[string]string{
		"category":      strconv.Itoa(resolveCategoryID(req.Meta)),
		"filename":      metautil.FirstNonEmptyTrimmed(req.Meta.ReleaseName, req.Meta.Filename, pathutil.Base(req.Meta.SourcePath)),
		"genre":         resolveGenres(req.Meta),
		"imdb":          resolveIMDbURL(req.Meta),
		"info":          description,
		"nuk_rea":       "",
		"nuk":           "false",
		"req":           "false",
		"submit":        "Send",
		"t3d":           boolString(req.Meta.Is3D != ""),
		"user_id":       "",
		"youtube_video": resolveYouTube(req.Meta),
		"anonymous":     boolString(req.TrackerConfig.Anon),
	}
	state := uploadState{torrentPath: torrentPath, description: description, releaseName: fields["filename"], fields: fields}
	if !supportsHDSResolution(req.Meta.Release.Resolution) {
		state.blockedReason = "resolution must be at least 720p"
	}
	if id := resolveIMDbURL(req.Meta); strings.TrimSpace(id) == "" {
		state.blockedReason = "missing IMDb ID"
	}
	if file, ok := resolveNFO(req.Meta); ok {
		state.nfo = &file
	}
	return state, cookies, nil
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	return cookies.LoadTrackerHTTPCookies(ctx, dbPath, "HDS", "hd-space.org")
}

func resolveCategoryID(meta api.PreparedMetadata) int {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		return 15
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		return 40
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	if strings.Contains(strings.ToLower(resolveGenres(meta)+" "+resolveKeywords(meta)), "documentary") {
		if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "2160p") {
			return 47
		}
		if strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "1080p") || strings.EqualFold(strings.TrimSpace(meta.Release.Resolution), "1080i") {
			return 25
		}
		return 24
	}
	if meta.Anime {
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 48
		case "1080p", "1080i":
			return 28
		default:
			return 27
		}
	}
	if category == "TV" {
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 45
		case "1080p", "1080i":
			return 22
		default:
			return 21
		}
	}
	switch strings.TrimSpace(meta.Release.Resolution) {
	case "2160p":
		return 46
	case "1080p", "1080i":
		return 19
	default:
		return 18
	}
}

func buildDescription(req trackers.UploadRequest, assets trackers.DescriptionAssets) (string, error) {
	meta := req.Meta
	parts := make([]string, 0, 12)

	// Custom Header
	if header := strings.TrimSpace(req.AppConfig.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// Logo
	if req.AppConfig.Description.AddLogo {
		if logo := resolveLogo(meta); logo != "" {
			parts = append(parts, "[center][img]"+logo+"[/img][/center]")
		}
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information (BDInfo or MediaInfo)
	if media := trackers.ReadBDinfoOrMediaInfo(req.AppConfig.MainSettings.DBPath, meta); media != "" {
		parts = append(parts, "[pre]"+media+"[/pre]")
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}

	// menu
	if len(assets.MenuImages) > 0 {
		// header
		if header := strings.TrimSpace(req.AppConfig.Description.DiscMenuHeader); header != "" {
			parts = append(parts, header)
		}
		// images
		if shots := screenshotBlock(assets.MenuImages); shots != "" {
			parts = append(parts, shots)
		}
	}
	// Screenshot Header
	if header := strings.TrimSpace(req.AppConfig.Description.ScreenshotHeader); header != "" {
		parts = append(parts, header)
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(req.AppConfig.Description.TonemappedHeader); tonemapHeader != "" && descriptionunit3d.ShouldIncludeTonemappedHeader(meta, req.AppConfig, assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// screenshots
	if shots := screenshotBlock(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}

	// custom user signature
	if signature := strings.TrimSpace(req.AppConfig.Description.CustomSignature); signature != "" {
		parts = append(parts, signature)
	}

	// upbrr signature
	link, text := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[center][url=%s][size=2]%s[/size][/url][/center]", link, text))

	// finalize description
	finalDescription := bbcode.FinalizeTrackerDescription("HDS", strings.TrimSpace(strings.Join(parts, "\n\n")))

	// save debug description
	if meta.Options.Debug {
		descriptionunit3d.SaveDescriptionDebug(meta, "HDS", req.AppConfig.MainSettings.DBPath, finalDescription, req.Logger)
	}

	return finalDescription, nil
}

func resolveLogo(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func resolveGenres(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil:
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres)
	case meta.ExternalMetadata.IMDB != nil:
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres)
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

func resolveIMDbURL(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbURL) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.IMDbURL)
	}
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.ExternalIDs.IMDBID)
	}
	return ""
}

func resolveYouTube(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveNFO(meta api.PreparedMetadata) (commonhttp.FileField, bool) {
	dir := filepath.Dir(metautil.FirstNonEmptyTrimmed(meta.MediaInfoTextPath, meta.SourcePath))
	payload, path, err := commonhttp.ReadFirstMatching(dir, "*.nfo")
	if err != nil {
		return commonhttp.FileField{}, false
	}
	return commonhttp.FileField{FieldName: "nfo", FileName: filepath.Base(path), Content: payload}, true
}

func categoryOf(meta api.PreparedMetadata) string {
	if category := strings.TrimSpace(meta.ExternalIDs.Category); category != "" {
		return category
	}
	return strings.TrimSpace(meta.MediaInfoCategory)
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, image := range images {
		if strings.TrimSpace(image.WebURL) == "" || strings.TrimSpace(image.ImgURL) == "" {
			continue
		}
		sb.WriteString("[url=" + image.WebURL + "][img]" + image.ImgURL + "[/img][/url]")

		// HDS cannot resize images. If the image host does not provide small thumbnails(<400px), place only one image per line.
		// imgbox provides small thumbnails, so we can place them side-by-side.
		if !strings.Contains(strings.ToLower(image.WebURL), "imgbox") {
			sb.WriteString("\n")
		}
	}
	content := strings.TrimSpace(sb.String())
	if content == "" {
		return ""
	}
	return "[center]\n" + content + "\n[/center]"
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func supportsHDSResolution(value string) bool {
	switch strings.TrimSpace(value) {
	case "2160p", "1080p", "1080i", "720p":
		return true
	default:
		return false
	}
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
