// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL       = "https://www.torrentleech.org"
	apiUploadURL  = baseURL + "/torrents/upload/apiupload"
	httpUploadURL = baseURL + "/torrents/upload/"
	torrentURL    = baseURL + "/torrent/"
	sourceFlag    = "TorrentLeech.org"
)

type uploadState struct {
	torrentPath string
	description string
	releaseName string
	fields      map[string]string
	files       []commonhttp.FileField
	endpoint    string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, client, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, state.files)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.endpoint, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TL build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")

	resp, err := client.Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TL upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	torrentID := ""
	if req.TrackerConfig.APIUpload {
		text := strings.TrimSpace(string(responseBody))
		if _, err := strconv.Atoi(text); err == nil {
			torrentID = text
		}
	} else if resp.StatusCode == http.StatusFound {
		torrentID = strings.TrimPrefix(strings.TrimSpace(resp.Header.Get("Location")), "/successfulupload?torrentID=")
	}
	if torrentID == "" {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "TL", "upload_failure", responseBody, ".html")
		return api.UploadSummary{}, commonhttp.UploadHTTPError("TL", resp.StatusCode, responseBody)
	}

	urlValue := torrentURL + torrentID
	artifactPath := ""
	if announces := announceList(req.TrackerConfig); len(announces) > 0 {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "TL")
		if err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
		}
		if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announces[0], urlValue, sourceFlag); err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
		Tracker: "TL", TorrentID: torrentID, TorrentURL: urlValue, DownloadURL: urlValue, TorrentPath: artifactPath,
	}}}, nil
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, _, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.TrackerDryRunEntry{}, err
	}
	return api.TrackerDryRunEntry{
		Tracker:          "TL",
		Status:           "ready",
		Message:          "dry-run payload generated",
		ReleaseName:      state.releaseName,
		DescriptionGroup: "tl",
		Description:      state.description,
		Endpoint:         state.endpoint,
		Payload:          cloneFields(state.fields),
		Files:            []api.TrackerDryRunFile{{Field: "torrent", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, *http.Client, error) {
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)

	releaseName := resolveName(req.Meta)
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		releaseName: releaseName,
	}

	if req.TrackerConfig.APIUpload {
		state.endpoint = apiUploadURL
		state.fields = map[string]string{
			"announcekey": announceKey(req.TrackerConfig),
			"category":    resolveCategory(req.Meta),
			"description": description,
			"name":        releaseName,
			"nonscene":    boolWord(!req.Meta.Scene, "on", "off"),
		}
		switch {
		case req.Meta.Anime && req.Meta.MALID > 0:
			state.fields["animeid"] = fmt.Sprintf("https://anilist.co/anime/%d", req.Meta.MALID)
		case !isTV(req.Meta) && req.Meta.ExternalIDs.IMDBID > 0:
			state.fields["imdb"] = fmt.Sprintf("tt%07d", req.Meta.ExternalIDs.IMDBID)
		case isTV(req.Meta):
			state.fields["tvmazeid"] = strconv.Itoa(req.Meta.ExternalIDs.TVmazeID)
			if req.Meta.TVPack {
				state.fields["tvmazetype"] = "true"
			}
		}
		if req.TrackerConfig.Anon {
			state.fields["is_anonymous_upload"] = "on"
		}
		state.files = []commonhttp.FileField{{
			FieldName: "torrent",
			FileName:  releaseName + ".torrent",
			Path:      torrentPath,
		}}
		return state, &http.Client{Timeout: 30 * time.Second}, nil
	}

	state.endpoint = httpUploadURL
	state.fields = map[string]string{
		"name":                releaseName,
		"category":            resolveCategory(req.Meta),
		"nonscene":            boolWord(!req.Meta.Scene, "on", "off"),
		"imdbURL":             imdbURL(req.Meta),
		"tvMazeURL":           tvmazeURL(req.Meta),
		"igdbURL":             "",
		"torrentNFO":          "0",
		"torrentDesc":         "1",
		"nfotextbox":          "",
		"torrentComment":      "0",
		"uploaderComments":    "",
		"is_anonymous_upload": boolWord(req.TrackerConfig.Anon, "on", "off"),
	}
	if req.TrackerConfig.ImgRehost {
		for idx, shot := range screenshots(assets.Screenshots) {
			state.fields["screenshots["+strconv.Itoa(idx)+"]"] = shot
		}
	}
	state.files = []commonhttp.FileField{
		{FieldName: "torrent", FileName: "torrent.torrent", Path: torrentPath},
		{FieldName: "nfo", FileName: "description.txt", Content: []byte(description)},
	}
	client, err := cookieClient(ctx, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	return state, client, nil
}

func cookieClient(ctx context.Context, dbPath string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("trackers: TL create cookie jar: %w", err)
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Jar:     jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	cookies, err := loadCookies(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	target, _ := url.Parse(baseURL)
	jar.SetCookies(target, cookies)
	return client, nil
}

func buildDescription(req trackers.UploadRequest, assets trackers.DescriptionAssets) string {
	meta := req.Meta
	parts := make([]string, 0, 8)

	// Custom Header
	if header := strings.TrimSpace(req.AppConfig.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// Logo
	if req.AppConfig.Description.AddLogo {
		if logo, _ := descriptionunit3d.ResolveLogo(meta, req.AppConfig); logo != "" {
			parts = append(parts, `<center><img src="`+logo+`" style="max-width: 300px;"></center>`)
		}
	}

	// TV Episode details
	if strings.TrimSpace(meta.EpisodeOverview) != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]")
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeOverview)+"[/center]")
	}

	// File information (BDInfo or MediaInfo)
	if media := trackers.ReadBDinfoOrMediaInfo(req.AppConfig.MainSettings.DBPath, meta); media != "" {
		parts = append(parts, media)
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
	parts = append(parts, fmt.Sprintf("<div style=\"text-align: right; font-size: 11px;\"><a href=\"%s\">%s</a></div>", link, text))

	// finalize description
	finalDescription := bbcode.FinalizeTrackerDescription("TL", strings.TrimSpace(strings.Join(parts, "\n\n")))

	// save debug description
	if meta.Options.Debug {
		descriptionunit3d.SaveDescriptionDebug(meta, "TL", req.AppConfig.MainSettings.DBPath, finalDescription, req.Logger)
	}

	return finalDescription
}

func resolveCategory(meta api.PreparedMetadata) string {
	if meta.Anime {
		return "34"
	}
	if !isTV(meta) {
		if meta.ExternalMetadata.TMDB.OriginalLanguage != "" && !strings.EqualFold(meta.ExternalMetadata.TMDB.OriginalLanguage, "en") {
			return "36"
		}
		if containsWord(genresText(meta), "Documentary") {
			return "29"
		}
		if meta.Release.Resolution == "2160p" {
			return "47"
		}
		if strings.EqualFold(meta.DiscType, "BDMV") || strings.EqualFold(meta.Type, "REMUX") && strings.EqualFold(meta.Source, "BluRay") {
			return "13"
		}
		if strings.EqualFold(meta.Type, "ENCODE") && strings.EqualFold(meta.Source, "BluRay") {
			return "14"
		}
		if strings.EqualFold(meta.DiscType, "DVD") || strings.Contains(strings.ToUpper(meta.Source), "DVD") && strings.EqualFold(meta.Type, "REMUX") {
			return "12"
		}
		if strings.Contains(strings.ToUpper(meta.Source), "DVD") || strings.EqualFold(meta.Type, "DVDRIP") {
			return "11"
		}
		if strings.Contains(strings.ToUpper(meta.Type), "WEB") {
			return "37"
		}
		if strings.EqualFold(meta.Type, "HDTV") {
			return "43"
		}
	}
	if isTV(meta) && meta.ExternalMetadata.TMDB.OriginalLanguage != "" && !strings.EqualFold(meta.ExternalMetadata.TMDB.OriginalLanguage, "en") {
		return "44"
	}
	if meta.TVPack {
		return "27"
	}
	if isSD(meta.Release.Resolution) {
		return "26"
	}
	return "32"
}

func resolveName(meta api.PreparedMetadata) string {
	if strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName)
	}
	return strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, meta.Release.Title, meta.Filename))
}

func announceKey(cfg config.TrackerConfig) string {
	return strings.TrimSpace(cfg.Passkey)
}

func announceList(cfg config.TrackerConfig) []string {
	passkey := strings.TrimSpace(cfg.Passkey)
	if passkey == "" {
		return nil
	}
	return []string{
		"https://tracker.torrentleech.org/a/" + passkey + "/announce",
		"https://tracker.tleechreload.org/a/" + passkey + "/announce",
	}
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	return wrapTrackerResult(cookies.LoadTrackerHTTPCookies(ctx, dbPath, "TL", "torrentleech.org"))
}

func imdbURL(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.ExternalIDs.IMDBID)
}

func tvmazeURL(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.TVmazeID <= 0 {
		return ""
	}
	return fmt.Sprintf("https://www.tvmaze.com/shows/%d", meta.ExternalIDs.TVmazeID)
}

func screenshots(images []api.ScreenshotImage) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		if raw := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(image.RawURL, image.ImgURL)); raw != "" {
			out = append(out, raw)
		}
	}
	return out
}

func screenshotBlock(images []api.ScreenshotImage) string {
	if len(images) == 0 {
		return ""
	}
	parts := []string{"<center>"}
	for idx, image := range images {
		img := metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL)
		web := metautil.FirstNonEmptyTrimmed(image.WebURL, img)
		if img == "" || web == "" {
			continue
		}
		parts = append(parts, `<a href="`+web+`"><img src="`+img+`" style="max-width: 350px;"></a>`)
		if (idx+1)%2 == 0 {
			parts = append(parts, "<br><br>")
		}
	}
	parts = append(parts, "</center>")
	return strings.Join(parts, "  ")
}

func containsWord(a string, b string) bool {
	return strings.Contains(strings.ToLower(a), strings.ToLower(b))
}

func isSD(res string) bool {
	return strings.HasPrefix(res, "480") || strings.HasPrefix(res, "576") || strings.HasPrefix(res, "540")
}

func boolWord(cond bool, yes string, no string) string {
	if cond {
		return yes
	}
	return no
}

func cloneFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	maps.Copy(out, input)
	return out
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}

func genresText(meta api.PreparedMetadata) string {
	return metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.Genres, meta.Release.Genre)
}
