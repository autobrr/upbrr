// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

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
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://immortalseed.me"
	uploadURL  = baseURL + "/upload.php"
	torrentURL = baseURL + "/details.php?hash="
	sourceFlag = "https://immortalseed.me"
)

var sslPattern = regexp.MustCompile(`details\.php\?hash=([a-zA-Z0-9]+)|download\.php\?id=([a-zA-Z0-9]+)`)

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
		return api.UploadSummary{}, fmt.Errorf("trackers: IS %s", state.blockedReason)
	}

	files := []commonhttp.FileField{{
		FieldName: "torrentfile",
		FileName:  metautil.FirstNonEmptyTrimmed(state.releaseName, filepath.Base(state.torrentPath)),
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
		return api.UploadSummary{}, fmt.Errorf("trackers: IS request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)

	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: IS upload request: %w", err)
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, _ := io.ReadAll(resp.Body)
	match := sslPattern.FindStringSubmatch(finalURL + "\n" + string(responseBody))
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && len(match) >= 3 {
		id := metautil.FirstNonEmptyTrimmed(match[1], match[2])
		if id != "" {
			tURL := torrentURL + id
			artifactPath := ""
			if announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announceURL != "" {
				artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "IS")
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
					Tracker:     "IS",
					TorrentID:   id,
					TorrentURL:  tURL,
					DownloadURL: tURL,
					TorrentPath: artifactPath,
				}},
			}, nil
		}
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "IS", "upload_failure", responseBody, ".html")
	return api.UploadSummary{}, fmt.Errorf("trackers: IS upload failed status=%d", resp.StatusCode)
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
		Tracker:          "IS",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "is",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Files:            []api.TrackerDryRunFile{{Field: "torrentfile", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
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
		"UseNFOasDescr": "no",
		"message":       buildMessage(req.Meta),
		"category":      strconv.Itoa(resolveCategoryID(req.Meta)),
		"subject":       resolveSubject(req.Meta),
		"nothingtopost": "1",
		"t_image_url":   resolvePoster(req.Meta),
		"submit":        "Upload Torrent",
		"anonymous":     yesNo(req.TrackerConfig.Anon),
	}
	if strings.EqualFold(categoryOf(req.Meta), "MOVIE") {
		fields["t_link"] = resolveIMDbURL(req.Meta)
	}
	state := uploadState{torrentPath: torrentPath, description: description, releaseName: fields["subject"], fields: fields}
	if strings.TrimSpace(fields["t_image_url"]) == "" {
		state.blockedReason = "missing poster URL"
	}
	if file, ok := resolveNFO(req.Meta); ok {
		state.nfo = &file
	}
	return state, cookies, nil
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	return cookies.LoadTrackerHTTPCookies(ctx, dbPath, "IS", "immortalseed.me")
}

func buildDescription(req trackers.UploadRequest, assets trackers.DescriptionAssets) (string, error) {
	meta := req.Meta
	parts := make([]string, 0, 8)
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "Title: "+strings.TrimSpace(meta.EpisodeTitle), "Overview: "+strings.TrimSpace(meta.EpisodeOverview))
	}
	if media := trackers.ReadBDinfoOrMediaInfo(req.AppConfig.MainSettings.DBPath, meta); media != "" {
		parts = append(parts, media)
	}
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}
	if len(assets.MenuImages) > 0 {
		var menuLines []string
		if header := strings.TrimSpace(req.AppConfig.Description.DiscMenuHeader); header != "" {
			menuLines = append(menuLines, header)
		}
		for _, image := range assets.MenuImages {
			if strings.TrimSpace(image.RawURL) != "" {
				menuLines = append(menuLines, image.RawURL)
			}
		}
		if len(menuLines) > 0 {
			parts = append(parts, strings.Join(menuLines, "\n"))
		}
	}
	if len(assets.Screenshots) > 0 {
		var shotLines []string
		for _, image := range assets.Screenshots {
			if strings.TrimSpace(image.RawURL) != "" {
				shotLines = append(shotLines, image.RawURL)
			}
		}
		if len(shotLines) > 0 {
			parts = append(parts, "Screenshots:\n"+strings.Join(shotLines, "\n"))
		}
	}
	return bbcode.FinalizeTrackerDescription("IS", strings.TrimSpace(strings.Join(parts, "\n\n"))), nil
}

func buildMessage(meta api.PreparedMetadata) string {
	message := strings.TrimSpace(resolveOverview(meta))
	if trailer := resolveYouTube(meta); trailer != "" {
		if message != "" {
			message += "\n\n"
		}
		message += "[youtube]" + trailer + "[/youtube]"
	}
	return message
}

func resolveCategoryID(meta api.PreparedMetadata) int {
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	resolution := strings.TrimSpace(meta.Release.Resolution)
	genres := strings.ToLower(resolveGenres(meta) + " " + resolveKeywords(meta))
	nonEnglish := !hasEnglishAudio(meta)
	switch category {
	case "TV":
		if strings.Contains(genres, "documentary") {
			if isSD(meta) {
				return 13
			}
			return 15
		}
		if meta.Anime {
			return 6
		}
		if strings.Contains(genres, "children") || strings.Contains(genres, "cartoon") {
			return 5
		}
		if meta.TVPack {
			if resolution == "2160p" {
				return 63
			}
			if isSD(meta) {
				return 6
			}
			return 4
		}
		if resolution == "2160p" {
			return 64
		}
		if resolution == "1080p" || resolution == "1080i" || resolution == "720p" {
			return 8
		}
		if isSD(meta) {
			if strings.Contains(strings.ToLower(meta.VideoEncode), "xvid") {
				return 9
			}
			return 48
		}
		return 47
	default:
		if strings.Contains(genres, "documentary") {
			if isSD(meta) {
				return 13
			}
			return 15
		}
		if meta.Anime {
			return 6
		}
		if resolution == "2160p" {
			if nonEnglish {
				return 60
			}
			return 62
		}
		if !isSD(meta) {
			if nonEnglish {
				return 18
			}
			return 16
		}
		if isSD(meta) {
			if nonEnglish {
				return 33
			}
			return 14
		}
		if nonEnglish {
			return 34
		}
		return 17
	}
}

func hasEnglishAudio(meta api.PreparedMetadata) bool {
	for _, language := range meta.AudioLanguages {
		lower := strings.ToLower(strings.TrimSpace(language))
		if lower == "english" || lower == "en" {
			return true
		}
	}
	return false
}

func resolveSubject(meta api.PreparedMetadata) string {
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName)
	}
	name := strings.TrimSpace(meta.ReleaseName)
	name = strings.ReplaceAll(name, strings.TrimSpace(meta.Release.Alt), "")
	name = strings.ReplaceAll(name, "Dubbed", "")
	name = strings.ReplaceAll(name, "Dual-Audio", "")
	name = strings.Join(strings.Fields(name), ".")
	return strings.Trim(name, ".")
}

func resolvePoster(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil:
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster)
	case meta.ExternalMetadata.IMDB != nil:
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover)
	case meta.ExternalMetadata.TVmaze != nil:
		return strings.TrimSpace(meta.ExternalMetadata.TVmaze.Poster)
	default:
		return ""
	}
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

func resolveOverview(meta api.PreparedMetadata) string {
	switch {
	case meta.ExternalMetadata.TMDB != nil:
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Overview)
	case meta.ExternalMetadata.IMDB != nil:
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Plot)
	case meta.ExternalMetadata.TVmaze != nil:
		return strings.TrimSpace(meta.ExternalMetadata.TVmaze.Summary)
	default:
		return strings.TrimSpace(meta.EpisodeOverview)
	}
}

func resolveYouTube(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveGenres(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres)
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres)
	}
	return strings.TrimSpace(meta.Release.Genre)
}

func resolveKeywords(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
	}
	return ""
}

func resolveNFO(meta api.PreparedMetadata) (commonhttp.FileField, bool) {
	dir := filepath.Dir(metautil.FirstNonEmptyTrimmed(meta.MediaInfoTextPath, meta.SourcePath))
	payload, path, err := commonhttp.ReadFirstMatching(dir, "*.nfo")
	if err != nil {
		return commonhttp.FileField{}, false
	}
	return commonhttp.FileField{FieldName: "nfofile", FileName: filepath.Base(path), Content: payload}, true
}

func isSD(meta api.PreparedMetadata) bool {
	resolution := strings.TrimSpace(meta.Release.Resolution)
	return resolution == "" || resolution == "480p" || resolution == "576p"
}

func categoryOf(meta api.PreparedMetadata) string {
	if category := strings.TrimSpace(meta.ExternalIDs.Category); category != "" {
		return category
	}
	return strings.TrimSpace(meta.MediaInfoCategory)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
