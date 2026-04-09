// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	baseURL    = "https://brasiltracker.org"
	uploadURL  = baseURL + "/upload.php"
	torrentURL = baseURL + "/torrents.php?id="
	sourceFlag = "BT"
)

var authPattern = regexp.MustCompile(`name="auth"\s+value="([^"]+)"`)
var groupPattern = regexp.MustCompile(`groupid=(\d+)|torrents\.php\?id=(\d+)`)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	blockedReason string
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: BT %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, []commonhttp.FileField{{
		FieldName: "file_input",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}})
	if err != nil {
		return api.UploadSummary{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BT request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(httpReq, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BT upload request: %w", err)
	}
	defer resp.Body.Close()
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, _ := io.ReadAll(resp.Body)
	match := groupPattern.FindStringSubmatch(finalURL + "\n" + string(responseBody))
	id := firstNonEmpty(matchValue(match, 1), matchValue(match, 2))
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && id != "" {
		tURL := torrentURL + id
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "BT")
			if err != nil {
				return api.UploadSummary{}, err
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, err
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "BT",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: tURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "BT", "upload_failure", responseBody, ".html")
	return api.UploadSummary{}, fmt.Errorf("trackers: BT upload failed status=%d", resp.StatusCode)
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
		Tracker:          "BT",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "bt",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Files:            []api.TrackerDryRunFile{{Field: "file_input", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, dryRun bool) (uploadState, []*http.Cookie, error) {
	cookies, err := loadCookies(req.AppConfig.MainSettings.DBPath)
	if err != nil {
		return uploadState{}, nil, err
	}
	auth := "dry-run-auth"
	if !dryRun {
		auth, err = fetchAuth(ctx, cookies)
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
	fields := buildFields(req.Meta, description, auth, req.TrackerConfig)
	state := uploadState{
		torrentPath: torrentPath,
		description: description,
		releaseName: firstNonEmpty(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename),
		fields:      fields,
	}
	if strings.TrimSpace(fields["image"]) == "" {
		state.blockedReason = "missing poster URL"
	}
	return state, cookies, nil
}

func buildFields(meta api.PreparedMetadata, description string, auth string, trackerCfg config.TrackerConfig) map[string]string {
	hasPT, subtitleIDs := resolveSubtitle(meta)
	width, height := resolveResolution(meta)
	fields := map[string]string{
		"audio_c":     resolveAudioCodec(meta),
		"audio":       resolveAudio(meta),
		"auth":        auth,
		"bitrate":     resolveBitrate(meta),
		"desc":        "",
		"diretor":     resolveDirectors(meta),
		"duracao":     fmt.Sprintf("%d min", resolveRuntime(meta)),
		"especificas": description,
		"format":      resolveContainer(meta),
		"idioma_ori":  resolveLanguage(meta),
		"image":       resolvePoster(meta),
		"legenda":     hasPT,
		"mediainfo":   resolveMedia(meta),
		"resolucao_1": width,
		"resolucao_2": height,
		"sinopse":     resolveOverview(meta),
		"submit":      "true",
		"tags":        resolveTags(meta),
		"title":       firstNonEmpty(meta.ExternalMetadata.TMDB.Title, meta.Release.Title),
		"type":        resolveType(meta),
		"video_c":     resolveVideoCodec(meta),
		"year":        strconv.Itoa(resolveYear(meta)),
		"youtube":     resolveYouTube(meta),
	}
	for _, id := range subtitleIDs {
		fields["subtitles[]"] = appendCSV(fields["subtitles[]"], id)
	}
	screens := resolveScreens(meta)
	if len(screens) > 0 {
		fields["screen[]"] = strings.Join(screens, ",")
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	if !meta.Anime && (category == "MOVIE" || category == "TV") {
		fields["3d"] = yesNo(meta.Is3D != "")
		fields["adulto"] = "0"
		fields["imdb_input"] = resolveIMDbText(meta)
		fields["nota_imdb"] = resolveIMDbRating(meta)
		fields["title_br"] = resolveLocalizedTitle(meta)
	}
	if meta.Scene {
		fields["scene"] = "on"
	}
	if category == "TV" || meta.Anime {
		fields["episodio"] = meta.EpisodeStr
		fields["ntorrent"] = meta.SeasonStr + meta.EpisodeStr
		if meta.TVPack {
			fields["temporada"] = meta.SeasonStr
			fields["tipo"] = "completa"
		} else {
			fields["temporada_e"] = meta.SeasonStr
			fields["tipo"] = "ep_individual"
		}
	}
	if category == "MOVIE" {
		fields["versao"] = resolveEdition(meta)
	}
	if meta.Anime {
		fields["fundo_torrent"] = resolveBackdrop(meta)
		fields["rating"] = resolveIMDbRating(meta)
		fields["releasedate"] = strconv.Itoa(resolveYear(meta))
	}
	if trackerCfg.Anon {
		fields["anonymous"] = "1"
	}
	if trackers.IsInternalGroup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BT": trackerCfg}}}, "BT", meta) {
		fields["internal"] = "1"
	}
	return fields
}

func buildDescription(meta api.PreparedMetadata, assets trackers.DescriptionAssets) (string, error) {
	parts := make([]string, 0, 4)
	if logo := resolveLogo(meta); logo != "" {
		parts = append(parts, "[center][img]"+logo+"[/img][/center]")
	}
	if episode := strings.TrimSpace(meta.EpisodeOverview); episode != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(meta.EpisodeTitle)+"[/center]\n[center]"+episode+"[/center]")
	}
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}
	return bbcode.FinalizeTrackerDescription("BT", strings.TrimSpace(strings.Join(parts, "\n\n"))), nil
}

func loadCookies(dbPath string) ([]*http.Cookie, error) {
	for _, candidate := range commonhttp.CookiePathCandidates(dbPath, "BT", ".txt") {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return commonhttp.LoadNetscapeCookies(candidate, "brasiltracker.org")
		}
	}
	return nil, errors.New("trackers: BT cookie file not found")
}

func fetchAuth(ctx context.Context, cookies []*http.Cookie) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uploadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	match := authPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", errors.New("trackers: BT auth token not found")
	}
	return strings.TrimSpace(match[1]), nil
}

func resolveType(meta api.PreparedMetadata) string {
	if meta.Anime {
		return "5"
	}
	if strings.EqualFold(categoryOf(meta), "TV") {
		return "1"
	}
	return "0"
}

func resolveContainer(meta api.PreparedMetadata) string {
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	switch container {
	case "avi", "m2ts", "m4v", "mkv", "mp4", "ts", "vob", "wmv":
		return strings.ToUpper(container)
	default:
		return "Outro"
	}
}

func resolveAudio(meta api.PreparedMetadata) string {
	pt := false
	for _, lang := range meta.AudioLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "portuguese" || lower == "português" || lower == "pt" {
			pt = true
			break
		}
	}
	orig := ""
	if meta.ExternalMetadata.TMDB != nil {
		orig = strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage))
	}
	if pt {
		if orig == "pt" {
			return "Nacional"
		}
		if len(meta.AudioLanguages) > 1 {
			return "Dual Audio"
		}
		return "Dublado"
	}
	return "Legendado"
}

func resolveSubtitle(meta api.PreparedMetadata) (string, []string) {
	hasPT := "Nao"
	var ids []string
	for _, lang := range meta.SubtitleLanguages {
		switch strings.ToLower(strings.TrimSpace(lang)) {
		case "portuguese", "português", "pt":
			hasPT = "Sim"
			ids = append(ids, "49")
		case "english", "en":
			ids = append(ids, "3")
		case "spanish", "es":
			ids = append(ids, "4")
		}
	}
	if len(ids) == 0 {
		ids = append(ids, "44")
	}
	return hasPT, ids
}

func resolveResolution(meta api.PreparedMetadata) (string, string) {
	height := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(meta.Release.Resolution), "p"), "i")
	switch height {
	case "2160":
		return "3840", "2160"
	case "1080":
		return "1920", "1080"
	case "720":
		return "1280", "720"
	case "576":
		return "1024", "576"
	case "480":
		return "854", "480"
	default:
		return "", ""
	}
}

func resolveVideoCodec(meta api.PreparedMetadata) string {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(meta.VideoEncode, meta.VideoCodec)))
	switch {
	case strings.Contains(value, "265"), strings.Contains(value, "hevc"):
		return "x265"
	case strings.Contains(value, "264"), strings.Contains(value, "avc"):
		return "x264"
	case strings.Contains(value, "vp9"):
		return "VP9"
	case strings.Contains(value, "mpeg-2"):
		return "MPEG-2"
	case strings.Contains(value, "vc-1"):
		return "VC-1"
	default:
		return firstNonEmpty(meta.VideoCodec, "Outro")
	}
}

func resolveAudioCodec(meta api.PreparedMetadata) string {
	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	switch {
	case strings.Contains(audio, "DTS:X"):
		return "DTS-X"
	case strings.Contains(audio, "ATMOS"):
		return "E-AC-3 JOC"
	case strings.Contains(audio, "TRUEHD"):
		return "TrueHD"
	case strings.Contains(audio, "DTS-HD"):
		return "DTS-HD"
	case strings.Contains(audio, "FLAC"):
		return "FLAC"
	case strings.Contains(audio, "LPCM"), strings.Contains(audio, "PCM"):
		return "PCM"
	case strings.Contains(audio, "DTS"):
		return "DTS"
	case strings.Contains(audio, "DD+"), strings.Contains(audio, "E-AC-3"):
		return "E-AC-3"
	case strings.Contains(audio, "DD"), strings.Contains(audio, "AC3"):
		return "AC3"
	case strings.Contains(audio, "AAC"):
		return "AAC"
	default:
		return "Outro"
	}
}

func resolveBitrate(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.Type), "DISC") {
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
			if meta.SourceSize > 66<<30 {
				return "BD100"
			}
			if meta.SourceSize > 50<<30 {
				return "BD66"
			}
			if meta.SourceSize > 25<<30 {
				return "BD50"
			}
			return "BD25"
		}
		if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
			return "DVD9"
		}
	}
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "REMUX":
		return "Remux"
	case "WEBDL":
		return "WEB-DL"
	case "WEBRIP":
		return "WEBRip"
	case "HDTV":
		return "HDTV"
	case "ENCODE":
		return "Blu-ray"
	default:
		return "Outro"
	}
}

func resolveMedia(meta api.PreparedMetadata) string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if summary, ok := meta.BDInfo["summary"].(string); ok {
			return strings.TrimSpace(summary)
		}
	}
	return firstNonEmpty(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
}

func resolveEdition(meta api.PreparedMetadata) string {
	edition := strings.ToLower(strings.TrimSpace(meta.Edition))
	switch {
	case strings.Contains(edition, "director"):
		return "Director's Cut"
	case strings.Contains(edition, "theatrical"):
		return "Theatrical Cut"
	case strings.Contains(edition, "extended"):
		return "Extended"
	case strings.Contains(edition, "uncut"):
		return "Uncut"
	case strings.Contains(edition, "unrated"):
		return "Unrated"
	case strings.Contains(edition, "imax"):
		return "IMAX"
	default:
		return ""
	}
}

func resolveTags(meta api.PreparedMetadata) string {
	genreText := strings.TrimSpace(meta.Release.Genre)
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres) != "" {
		genreText = strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres)
	} else if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres) != "" {
		genreText = strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres)
	}
	genres := strings.Split(genreText, ",")
	out := make([]string, 0, len(genres))
	for _, genre := range genres {
		tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(genre), " ", "."))
		if tag != "" {
			out = append(out, tag)
		}
	}
	return strings.Join(out, ", ")
}

func resolveRuntime(meta api.PreparedMetadata) int {
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.RuntimeMinutes > 0 {
		return meta.ExternalMetadata.IMDB.RuntimeMinutes
	}
	if meta.ExternalMetadata.TMDB != nil {
		return meta.ExternalMetadata.TMDB.Runtime
	}
	return 0
}

func resolveDirectors(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.Directors) > 0 {
		return strings.Join(meta.ExternalMetadata.TMDB.Directors, ", ")
	}
	if meta.ExternalMetadata.IMDB != nil && len(meta.ExternalMetadata.IMDB.Directors) > 0 {
		names := make([]string, 0, len(meta.ExternalMetadata.IMDB.Directors))
		for _, person := range meta.ExternalMetadata.IMDB.Directors {
			if strings.TrimSpace(person.Name) != "" {
				names = append(names, strings.TrimSpace(person.Name))
			}
		}
		return strings.Join(names, ", ")
	}
	return "N/A"
}

func resolvePoster(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster)
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover)
	}
	return ""
}

func resolveScreens(meta api.PreparedMetadata) []string {
	return nil
}

func resolveOverview(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Overview)
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Plot)
	}
	return ""
}

func resolveYouTube(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveLogo(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func resolveYear(meta api.PreparedMetadata) int {
	if meta.ExternalMetadata.TMDB != nil && meta.ExternalMetadata.TMDB.Year > 0 {
		return meta.ExternalMetadata.TMDB.Year
	}
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.Year > 0 {
		return meta.ExternalMetadata.IMDB.Year
	}
	return meta.Release.Year
}

func resolveLocalizedTitle(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return firstNonEmpty(meta.ExternalMetadata.TMDB.Title, meta.ExternalMetadata.TMDB.OriginalTitle)
	}
	return ""
}

func resolveLanguage(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage)
	}
	return ""
}

func resolveBackdrop(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Backdrop)
	}
	return ""
}

func resolveIMDbText(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
	}
	return ""
}

func resolveIMDbRating(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.Rating > 0 {
		return strconv.FormatFloat(meta.ExternalMetadata.IMDB.Rating, 'f', 1, 64)
	}
	return ""
}

func categoryOf(meta api.PreparedMetadata) string {
	if category := strings.TrimSpace(meta.ExternalIDs.Category); category != "" {
		return category
	}
	return strings.TrimSpace(meta.MediaInfoCategory)
}

func appendCSV(current string, value string) string {
	if strings.TrimSpace(current) == "" {
		return value
	}
	return current + "," + value
}

func yesNo(value bool) string {
	if value {
		return "Sim"
	}
	return "Nao"
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

func matchValue(values []string, idx int) string {
	if idx >= 0 && idx < len(values) {
		return values[idx]
	}
	return ""
}
