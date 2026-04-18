// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
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
	torrentPath     string
	origTorrentPath string
	artifactPath    string
	description     string
	releaseName     string
	fields          map[string]string
	nfo             *commonhttp.FileField
	blockedReason   string
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
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "HDS",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: baseURL + "/download.php?id=" + id,
				TorrentPath: state.artifactPath,
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
	imdbID := trackers.ResolveIMDbIDText(req.Meta)
	imdbID = strings.TrimPrefix(imdbID, "tt")

	categoryID := resolveCategoryID(req.Meta)
	filename := firstNonEmpty(req.Meta.ReleaseName, req.Meta.Filename, pathutil.Base(req.Meta.SourcePath))
	genre := resolveGenres(req.Meta)

	var blockedReason string
	switch {
	case strings.TrimSpace(imdbID) == "":
		blockedReason = "requires IMDb ID"
	case categoryID == 0:
		blockedReason = "unsupported category"
	case filename == "":
		blockedReason = "missing filename"
	case genre == "":
		blockedReason = "missing genre"
	}

	if blockedReason != "" {
		return uploadState{
			blockedReason: blockedReason,
			releaseName:   filename,
		}, nil, nil
	}

	origTorrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}

	torrentPath := origTorrentPath
	artifactPath := ""
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	if announceURL != "" {
		if ap, err := trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "HDS"); err == nil {
			// HDS doesn't have a native 'source' field for .torrents,
			// so we add one to prevent hash collisions with other trackers that also lack a source identifier.
			if err := writePersonalizedTorrentWithHashLink(origTorrentPath, ap, announceURL, sourceFlag); err == nil {
				torrentPath = ap
				artifactPath = ap
			}
		}
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
	description, err := buildDescription(req.Meta, assets)
	if err != nil {
		return uploadState{}, nil, err
	}

	fields := map[string]string{
		"category":      strconv.Itoa(categoryID),
		"filename":      filename,
		"genre":         genre,
		"imdb":          imdbID,
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
	state := uploadState{
		torrentPath:     torrentPath,
		origTorrentPath: origTorrentPath,
		artifactPath:    artifactPath,
		description:     description,
		releaseName:     fields["filename"],
		fields:          fields,
		blockedReason:   blockedReason,
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

	releaseOther := meta.Release.Other
	for _, other := range releaseOther {
		if strings.ToLower(other) == "remux" {
			return 40
		}
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
			return 0
		}
	}
	if category == "TV" {
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 45
		case "1080p", "1080i":
			return 22
		default:
			return 0
		}
	}
	if category == "MOVIE" {
		switch strings.TrimSpace(meta.Release.Resolution) {
		case "2160p":
			return 46
		case "1080p", "1080i":
			return 19
		default:
			return 0
		}
	}
	return 0
}

func buildDescription(meta api.PreparedMetadata, assets trackers.DescriptionAssets) (string, error) {
	parts := make([]string, 0, 6)
	if logo := resolveLogo(meta); logo != "" {
		parts = append(parts, "[center][img]"+logo+"[/img][/center]")
	}
	if media := resolveMedia(meta); media != "" {
		parts = append(parts, "[pre]"+media+"[/pre]")
	}
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}
	if shots := screenshots(assets.Screenshots); shots != "" {
		parts = append(parts, shots)
	}
	return bbcode.FinalizeTrackerDescription("HDS", strings.TrimSpace(strings.Join(parts, "\n\n"))), nil
}

func resolveMedia(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if summary, ok := meta.BDInfo["summary"].(string); ok {
			return strings.TrimSpace(summary)
		}
	}
	return firstNonEmpty(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
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

func resolveYouTube(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveNFO(meta api.PreparedMetadata) (commonhttp.FileField, bool) {
	dir := filepath.Dir(firstNonEmpty(meta.MediaInfoTextPath, meta.SourcePath))
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

func screenshots(images []api.ScreenshotImage) string {
	parts := make([]string, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.WebURL) == "" || strings.TrimSpace(image.ImgURL) == "" {
			continue
		}
		parts = append(parts, "[url="+image.WebURL+"][img]"+image.ImgURL+"[/img][/url]")
	}
	if len(parts) == 0 {
		return ""
	}
	return "[center]\n" + strings.Join(parts, "\n") + "\n[/center]"
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

func writePersonalizedTorrentWithHashLink(sourcePath string, outputPath string, announceURL string, source string) error {
	mi, err := metainfo.LoadFromFile(sourcePath)
	if err != nil {
		return err
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	info.Source = source
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return err
	}
	mi.InfoBytes = infoBytes

	// Calculate hash after setting source flag.
	// Hash is derived from InfoBytes.
	hash := mi.HashInfoBytes().HexString()
	tURL := "https://hd-space.org/index.php?page=torrent-details&id=" + hash

	mi.Comment = tURL

	if trimmedAnnounce := strings.TrimSpace(announceURL); trimmedAnnounce != "" {
		mi.Announce = trimmedAnnounce
		mi.AnnounceList = metainfo.AnnounceList{{trimmedAnnounce}}
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return err
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	return mi.Write(file)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
