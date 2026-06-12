// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path" //nolint:depguard // Extracts tracker response URL path basename.
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/services/imagehost"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://tvchaosuk.com"
	uploadURL  = baseURL + "/api/torrents/upload"
	sourceFlag = "TVCHAOS"
)

var categoryMap = map[string]string{
	"comedy": "29", "current affairs": "45", "documentary": "5", "drama": "11", "entertainment": "14",
	"factual": "19", "foreign": "43", "kids": "32", "movies": "44", "news": "54", "reality": "52", "soaps": "30", "sci-fi": "33", "sport": "42", "holding bin": "53",
}

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	questionnaire *api.TrackerQuestionnaire
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, err := prepareUploadState(ctx, req)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: TVC %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "torrent",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}})
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL+"?api_token="+url.QueryEscape(strings.TrimSpace(req.TrackerConfig.APIKey)), bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TVC build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TVC upload request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "TVC", "upload_failure", responseBody, ".txt")
		return api.UploadSummary{}, commonhttp.UploadHTTPError("TVC", resp.StatusCode, responseBody)
	}

	payload := string(responseBody)
	if strings.Contains(payload, "\n") {
		payload = strings.SplitN(payload, "\n", 2)[1]
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: TVC decode response: %w", err)
	}
	dataURL := strings.TrimSpace(fmt.Sprint(decoded["data"]))
	torrentID := path.Base(strings.TrimRight(dataURL, "/"))

	artifactPath := ""
	if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "TVC")
		if err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
		}
		if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, dataURL, sourceFlag); err != nil {
			return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return api.UploadSummary{Uploaded: 1, UploadedTorrents: []api.UploadedTorrent{{
		Tracker: "TVC", TorrentID: torrentID, TorrentURL: dataURL, DownloadURL: dataURL, TorrentPath: artifactPath,
	}}}, nil
}

func buildUploadDryRun(ctx context.Context, req trackers.UploadRequest) (api.TrackerDryRunEntry, error) {
	state, err := prepareUploadState(ctx, req)
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
		Tracker:          "TVC",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "tvc",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files:            []api.TrackerDryRunFile{{Field: "torrent", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest) (uploadState, error) {
	if strings.TrimSpace(req.TrackerConfig.APIKey) == "" {
		return uploadState{}, errors.New("trackers: TVC missing api_key")
	}
	torrentPath, err := trackers.ResolveUploadTorrentPath(req.Meta, req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req.Meta, req.TrackerConfig, assets)
	releaseName := resolveName(req.Meta)
	if override := strings.TrimSpace(questionnaireAnswers(req.Meta)["name_override"]); override != "" {
		releaseName = override
	}

	fields := map[string]string{
		"name":             releaseName,
		"description":      description,
		"mediainfo":        commonhttp.ReadOptionalFile(strings.TrimSpace(req.Meta.MediaInfoTextPath)),
		"bdinfo":           "",
		"category_id":      resolveCategory(req.Meta),
		"type":             resolveResolution(req.Meta),
		"tmdb":             strconv.Itoa(req.Meta.ExternalIDs.TMDBID),
		"imdb":             strconv.Itoa(req.Meta.ExternalIDs.IMDBID),
		"tvdb":             strconv.Itoa(req.Meta.ExternalIDs.TVDBID),
		"mal":              strconv.Itoa(req.Meta.MALID),
		"igdb":             "0",
		"anonymous":        boolNum(req.TrackerConfig.Anon),
		"stream":           boolNum(req.Meta.StreamOptimized > 0),
		"sd":               boolNum(isSD(req.Meta.Release.Resolution)),
		"keywords":         keywordsText(req.Meta),
		"personal_release": boolNum(req.Meta.PersonalRelease),
		"internal":         "0",
		"featured":         "0",
		"free":             "0",
		"doubleup":         "0",
		"sticky":           "0",
	}
	if isTV(req.Meta) {
		fields["season_number"] = strconv.Itoa(maxInt(req.Meta.SeasonInt, 0))
		fields["episode_number"] = strconv.Itoa(maxInt(req.Meta.EpisodeInt, 0))
	}

	return uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
		fields:        fields,
		questionnaire: buildQuestionnaire(req.Meta),
		blockedReason: validateUpload(req.Meta, req.TrackerConfig, assets),
	}, nil
}

func buildDescription(meta api.PreparedMetadata, cfg config.TrackerConfig, assets trackers.DescriptionAssets) string {
	parts := make([]string, 0, 6)
	if logo := strings.TrimSpace(meta.ExternalMetadata.TMDB.Logo); logo != "" {
		parts = append(parts, fmt.Sprintf("[center][img=%d]%s[/img][/center]", maxInt(cfg.ImageCount, 300), logo))
	}
	if title := strings.TrimSpace(meta.EpisodeTitle); title != "" {
		parts = append(parts, "[center][b]Episode Title:[/b] "+title+"[/center]")
	}
	if overview := strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.EpisodeOverview, meta.ExternalMetadata.TMDB.Overview)); overview != "" {
		parts = append(parts, "[center]"+overview+"[/center]")
	}
	if links := externalLinks(meta); links != "" {
		parts = append(parts, "[center]"+links+"[/center]")
	}
	if shots := screenshotBlock(assets.Screenshots, maxInt(cfg.ImageCount, 2)); shots != "" {
		parts = append(parts, "[center]"+shots+"[/center]")
	}
	if base := strings.TrimSpace(assets.Description); base != "" {
		parts = append(parts, "[center][b]Notes / Extra Info[/b]\n"+base+"[/center]")
	}
	return bbcode.FinalizeTrackerDescription("TVC", strings.TrimSpace(strings.Join(parts, "\n\n")))
}

func buildQuestionnaire(meta api.PreparedMetadata) *api.TrackerQuestionnaire {
	return &api.TrackerQuestionnaire{
		Tracker: "TVC",
		Fields: []api.TrackerQuestionnaireField{{
			Key: "name_override", Label: "Upload Name", Kind: "text", Value: resolveName(meta), Required: true,
		}},
	}
}

func validateUpload(meta api.PreparedMetadata, cfg config.TrackerConfig, assets trackers.DescriptionAssets) string {
	if meta.Release.Resolution == "2160p" || strings.EqualFold(meta.DiscType, "BDMV") || strings.EqualFold(meta.Type, "REMUX") {
		return "TVC disallows UHD, disc, and remux uploads"
	}
	required := maxInt(cfg.ImageCount, 2)
	if len(assets.Screenshots) < required {
		return fmt.Sprintf("TVC requires at least %d screenshots", required)
	}
	for _, image := range assets.Screenshots {
		host := strings.ToLower(strings.TrimSpace(imagehost.ExtractHost(metautil.FirstNonEmptyTrimmed(image.WebURL, image.ImgURL, image.RawURL))))
		switch host {
		case "imgbb", "imgbox", "pixhost", "bam", "onlyimage":
		default:
			return "TVC screenshots must use an approved image host"
		}
	}
	return ""
}

func resolveCategory(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB.OriginalLanguage != "" && !strings.EqualFold(meta.ExternalMetadata.TMDB.OriginalLanguage, "en") &&
		!strings.EqualFold(meta.ExternalMetadata.TMDB.OriginalLanguage, "ga") &&
		!strings.EqualFold(meta.ExternalMetadata.TMDB.OriginalLanguage, "gd") &&
		!strings.EqualFold(meta.ExternalMetadata.TMDB.OriginalLanguage, "cy") {
		return categoryMap["foreign"]
	}
	genres := strings.ToLower(genresText(meta))
	for key, value := range categoryMap {
		if key != "foreign" && strings.Contains(genres, key) {
			return value
		}
	}
	if !isTV(meta) {
		return categoryMap["movies"]
	}
	return categoryMap["holding bin"]
}

func resolveResolution(meta api.PreparedMetadata) string {
	if meta.TVPack {
		switch meta.Release.Resolution {
		case "1080p", "1080i":
			return "HD1080p Pack"
		case "720p":
			return "HD720p Pack"
		default:
			return "SD Pack"
		}
	}
	switch meta.Release.Resolution {
	case "1080p", "1080i":
		return "HD1080p"
	case "720p":
		return "HD720p"
	default:
		return "SD"
	}
}

func resolveName(meta api.PreparedMetadata) string {
	typeName := strings.ReplaceAll(meta.Type, "WEBDL", "WEB-DL")
	var name string
	switch {
	case !isTV(meta):
		name = fmt.Sprintf("%s (%d) [%s %s %s]", metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName), maxInt(meta.Release.Year, meta.ExternalMetadata.TMDB.Year), meta.Release.Resolution, typeName, videoSuffix(meta.VideoCodec))
	case meta.TVPack:
		name = fmt.Sprintf("%s - Series %d (%d) [%s %s %s]", metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName), maxInt(meta.SeasonInt, 1), maxInt(meta.Release.Year, meta.ExternalMetadata.TMDB.Year), meta.Release.Resolution, typeName, videoSuffix(meta.VideoCodec))
	default:
		name = fmt.Sprintf("%s S%02dE%02d [%s %s %s]", metautil.FirstNonEmptyTrimmed(meta.Release.Title, meta.ReleaseName), maxInt(meta.SeasonInt, 1), maxInt(meta.EpisodeInt, 1), meta.Release.Resolution, typeName, videoSuffix(meta.VideoCodec))
	}
	if strings.EqualFold(strings.TrimSpace(meta.VideoCodec), "HEVC") {
		name = strings.Replace(name, "]", " HEVC]", 1)
	}
	return appendCountryCode(meta, name)
}

func appendCountryCode(meta api.PreparedMetadata, name string) string {
	mapping := map[string]string{"AT": "AUT", "AU": "AUS", "BE": "BEL", "CA": "CAN", "CH": "CHE", "CZ": "CZE", "DE": "GER", "DK": "DNK", "EE": "EST", "ES": "SPA", "FI": "FIN", "FR": "FRA", "IE": "IRL", "IS": "ISL", "IT": "ITA", "NL": "NLD", "NO": "NOR", "NZ": "NZL", "PL": "POL", "PT": "POR", "RU": "RUS", "SE": "SWE"}
	for _, code := range meta.ExternalMetadata.TMDB.OriginCountry {
		if mapped := mapping[strings.ToUpper(strings.TrimSpace(code))]; mapped != "" {
			return name + " [" + mapped + "]"
		}
	}
	return name
}

func externalLinks(meta api.PreparedMetadata) string {
	parts := make([]string, 0, 3)
	if meta.ExternalIDs.TMDBID > 0 {
		parts = append(parts, fmt.Sprintf("[url=https://www.themoviedb.org/%s/%d]TMDB[/url]", strings.ToLower(categoryName(meta)), meta.ExternalIDs.TMDBID))
	}
	if meta.ExternalIDs.IMDBID > 0 {
		parts = append(parts, fmt.Sprintf("[url=https://www.imdb.com/title/tt%07d]IMDb[/url]", meta.ExternalIDs.IMDBID))
	}
	if meta.ExternalIDs.TVDBID > 0 {
		parts = append(parts, fmt.Sprintf("[url=https://www.thetvdb.com/?id=%d&tab=series]TVDB[/url]", meta.ExternalIDs.TVDBID))
	}
	return strings.Join(parts, " | ")
}

func screenshotBlock(images []api.ScreenshotImage, count int) string {
	if len(images) < count {
		return ""
	}
	parts := []string{"[b]Screenshots[/b]"}
	for _, image := range images[:count] {
		web := metautil.FirstNonEmptyTrimmed(image.WebURL, image.ImgURL, image.RawURL)
		img := metautil.FirstNonEmptyTrimmed(image.ImgURL, image.RawURL)
		if web == "" || img == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[url=%s][img=350]%s[/img][/url]", web, img))
	}
	return strings.Join(parts, " ")
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["TVC"]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func boolNum(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func isSD(res string) bool {
	return strings.HasPrefix(res, "480") || strings.HasPrefix(res, "576") || strings.HasPrefix(res, "540")
}

func videoSuffix(codec string) string {
	value := strings.ToUpper(strings.TrimSpace(codec))
	if len(value) <= 3 {
		return value
	}
	return value[len(value)-3:]
}

func cloneFields(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func isTV(meta api.PreparedMetadata) bool {
	return meta.TVPack || meta.SeasonInt > 0 || meta.EpisodeInt > 0 || strings.EqualFold(meta.ExternalIDs.Category, "TV")
}

func categoryName(meta api.PreparedMetadata) string {
	if isTV(meta) {
		return "TV"
	}
	return "MOVIE"
}

func genresText(meta api.PreparedMetadata) string {
	return metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.Genres, meta.Release.Genre)
}

func keywordsText(meta api.PreparedMetadata) string {
	return strings.TrimSpace(meta.ExternalMetadata.TMDB.Keywords)
}
