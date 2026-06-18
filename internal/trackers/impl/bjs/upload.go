// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	descriptionunit3d "github.com/autobrr/upbrr/internal/services/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	baseURL    = "https://bj-share.info"
	uploadURL  = baseURL + "/upload.php"
	torrentURL = baseURL + "/torrents.php?torrentid="
	sourceFlag = "BJ"
)

var (
	authPattern      = regexp.MustCompile(`name="auth"\s+value="([^"]+)"`)
	idPattern        = regexp.MustCompile(`action=download&id=(\d+)|torrentid=(\d+)`)
	durationPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?im)^duration(?:\s*/string\d*)?\s*:\s*(.+)$`),
		regexp.MustCompile(`(?im)^duration\s*:\s*(.+)$`),
	}
	isoDurationPattern  = regexp.MustCompile(`(?i)^pt(?:(\d+(?:\.\d+)?)h)?(?:(\d+(?:\.\d+)?)m)?(?:(\d+(?:\.\d+)?)s)?$`)
	hoursPattern        = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:hours?|hrs?|hr|h)\b`)
	minutesPattern      = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:minutes?|mins?|min|mn)\b`)
	secondsPattern      = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:seconds?|secs?|sec|s)\b`)
	millisecondsPattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*ms\b`)
)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string]string
	blockedReason string
	questionnaire *api.TrackerQuestionnaire
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: BJS %s", state.blockedReason)
	}
	files := []commonhttp.FileField{{
		FieldName: "file_input",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}}
	body, contentType, err := commonhttp.BuildMultipartPayload(state.fields, files)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(body))
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BJS request build: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", "upbrr")
	httpReq.Header.Set("Referer", uploadURL)
	commonhttp.ApplyCookies(httpReq, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(httpReq)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BJS upload request: %w", err)
	}
	defer resp.Body.Close()
	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	responseBody, _ := io.ReadAll(resp.Body)
	match := idPattern.FindStringSubmatch(finalURL + "\n" + string(responseBody))
	id := metautil.FirstNonEmptyTrimmed(matchValue(match, 1), matchValue(match, 2))
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && id != "" {
		tURL := torrentURL + id
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "BJS")
			if err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
			if err := trackers.WritePersonalizedTorrent(state.torrentPath, artifactPath, announce, tURL, sourceFlag); err != nil {
				return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
			}
		}
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "BJS",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: tURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "BJS", "upload_failure", responseBody, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("BJS", resp.StatusCode, responseBody)
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
		Tracker:          "BJS",
		Status:           status,
		Message:          message,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "bjs",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          cloneFields(state.fields),
		Questionnaire:    state.questionnaire,
		Files:            []api.TrackerDryRunFile{{Field: "file_input", Path: state.torrentPath, Present: strings.TrimSpace(state.torrentPath) != ""}},
	}, nil
}

func prepareUploadState(ctx context.Context, req trackers.UploadRequest, dryRun bool) (uploadState, []*http.Cookie, error) {
	cookies, err := loadCookies(ctx, req.AppConfig.MainSettings.DBPath)
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
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.ResolveDescriptionAssets(ctx, req.Tracker, req.Meta, req.Repo, req.Logger)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	fields := buildFields(req.Meta, description, auth, questionnaireAnswers(req.Meta))
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   metautil.FirstNonEmptyTrimmed(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename),
		fields:        fields,
		questionnaire: buildQuestionnaire(req.Meta, fields),
	}
	if reason := validateFields(fields); reason != "" {
		state.blockedReason = reason
	}
	return state, cookies, nil
}

func buildFields(meta api.PreparedMetadata, description string, auth string, answers map[string]string) map[string]string {
	width, height := resolveResolution(meta)
	runtimeMinutes := resolveRuntime(meta)
	ptBR := api.ExtractLocalizedPTBR(meta)

	var tmdbOriginalTitle, tmdbTitle string
	if meta.ExternalMetadata.TMDB != nil {
		tmdbOriginalTitle = meta.ExternalMetadata.TMDB.OriginalTitle
		tmdbTitle = meta.ExternalMetadata.TMDB.Title
	}

	fields := map[string]string{
		"audio":            resolveAudio(meta),
		"auth":             auth,
		"codecaudio":       resolveAudioCodec(meta),
		"codecvideo":       resolveVideoCodec(meta),
		"duracaoHR":        strconv.Itoa(runtimeMinutes / 60),
		"duracaoMIN":       strconv.Itoa(runtimeMinutes % 60),
		"duracaotipo":      "selectbox",
		"fichatecnica":     description,
		"formato":          resolveContainer(meta),
		"idioma":           resolveLanguage(meta),
		"imdblink":         resolveIDLink(meta),
		"qualidade":        resolveQuality(meta),
		"release":          strings.TrimSpace(meta.ServiceLongName),
		"remaster_title":   resolveRemasterTitle(meta),
		"resolucaoh":       height,
		"resolucaow":       width,
		"sinopse":          metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["overview"]), resolveOverview(meta, ptBR)),
		"submit":           "true",
		"tags":             metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["tags"]), resolveTags(meta, ptBR)),
		"tipolegenda":      resolveSubtitle(meta),
		"title":            metautil.FirstNonEmptyTrimmed(tmdbOriginalTitle, meta.Release.Title),
		"titulobrasileiro": metautil.FirstNonEmptyTrimmed(ptBR.Title, tmdbTitle, meta.Release.Title),
		"traileryoutube":   resolveYouTube(meta, ptBR),
		"type":             resolveType(meta),
		"year":             resolveYearLabel(meta),
	}
	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	if category == "MOVIE" {
		fields["adulto"] = resolveAdult(meta)
		fields["diretor"] = resolveDirectors(meta)
		fields["datalancamento"] = resolveReleaseDate(meta)
	}
	if category == "TV" {
		fields["diretor"] = resolveCreators(meta)
		if meta.TVPack {
			fields["tipo"] = "season"
		} else {
			fields["tipo"] = "episode"
		}
		fields["season"] = strconv.Itoa(meta.SeasonInt)
		fields["episode"] = strconv.Itoa(meta.EpisodeInt)
		fields["network"] = resolveNetworks(meta)

		numSeasons := 0
		if meta.ExternalMetadata.IMDB != nil {
			numSeasons = len(meta.ExternalMetadata.IMDB.SeasonsSummary)
		}
		fields["numtemporadas"] = strconv.Itoa(numSeasons)

		var originCountry []string
		if meta.ExternalMetadata.TMDB != nil {
			for _, code := range meta.ExternalMetadata.TMDB.OriginCountry {
				originCountry = append(originCountry, metautil.ISO3166PortugueseName(code, code))
			}
		}
		fields["pais"] = strings.Join(originCountry, ", ")

		fields["diretorserie"] = resolveDirectors(meta)
		fields["avaliacao"] = resolveIMDbRating(meta)
		fields["datalancamento"] = resolveReleaseDate(meta)
	}
	if !meta.Anime {
		fields["validimdb"] = "yes"
		fields["imdbrating"] = resolveIMDbRating(meta)
		fields["elenco"] = resolveCast(meta)
	}
	if meta.Anime && category == "MOVIE" {
		fields["tipo"] = "movie"
	}
	if meta.Anime && category == "TV" {
		fields["adulto"] = resolveAdult(meta)
	}
	if strings.TrimSpace(meta.Repack) != "" {
		fields["repack"] = "on"
	}
	if resolvePoster(meta) != "" {
		fields["image"] = resolvePoster(meta)
	}
	screens := resolveScreens(meta)
	if len(screens) > 0 {
		fields["screenshots[]"] = strings.Join(screens, ",")
	}
	return fields
}

func buildQuestionnaire(meta api.PreparedMetadata, fields map[string]string) *api.TrackerQuestionnaire {
	current := questionnaireAnswers(meta)
	var items []api.TrackerQuestionnaireField
	if strings.TrimSpace(fields["sinopse"]) == "" {
		items = append(items, api.TrackerQuestionnaireField{Key: "overview", Label: "Overview", Kind: "textarea", Value: current["overview"], Required: true})
	}
	if strings.TrimSpace(fields["tags"]) == "" {
		items = append(items, api.TrackerQuestionnaireField{Key: "tags", Label: "Tags", Kind: "text", Value: current["tags"], Required: true})
	}
	if len(items) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "BJS", Fields: items}
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["BJS"]
}

func validateFields(fields map[string]string) string {
	if strings.TrimSpace(fields["imdblink"]) == "" {
		return "missing IMDb or TMDb identifier"
	}
	if strings.TrimSpace(fields["sinopse"]) == "" {
		return "missing overview"
	}
	if strings.TrimSpace(fields["diretor"]) == "" || strings.EqualFold(strings.TrimSpace(fields["diretor"]), "skipped") {
		return "missing director/creator credits"
	}
	return ""
}

func buildDescription(req trackers.UploadRequest, assets trackers.DescriptionAssets) string {
	meta := req.Meta
	var parts []string

	// Custom Header
	if header := strings.TrimSpace(req.AppConfig.Description.CustomDescriptionHeader); header != "" {
		parts = append(parts, header)
	}

	// Logo
	if logo := resolveLogo(meta); logo != "" {
		parts = append(parts, "[align=center][img]"+logo+"[/img][/align]")
	}

	// TV Episode details
	epTitle := meta.EpisodeTitle
	epOverview := meta.EpisodeOverview
	if meta.ExternalMetadata.TMDB != nil && meta.ExternalMetadata.TMDB.Localized != nil {
		if ptBR, ok := meta.ExternalMetadata.TMDB.Localized["pt-BR"]; ok {
			if ptBR.EpisodeTitle != "" {
				epTitle = ptBR.EpisodeTitle
			}
			if ptBR.EpisodeOverview != "" {
				epOverview = ptBR.EpisodeOverview
			}
		}
	}
	epTitle = strings.TrimSpace(epTitle)
	epOverview = strings.TrimSpace(epOverview)
	if epOverview != "" {
		if epTitle != "" {
			parts = append(parts, "[align=center]"+epTitle+"[/align]")
		}
		parts = append(parts, "[align=center]"+epOverview+"[/align]")
	}

	// File information
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	if discType == "DVD" || discType == "HDDVD" {
		mediainfo := strings.TrimSpace(commonhttp.ReadOptionalFile(meta.MediaInfoTextPath))
		if mediainfo != "" {
			parts = append(parts, "[hide=DVD MediaInfo][pre]"+mediainfo+"[/pre][/hide]")
		}
	}
	if discType == "BDMV" {
		bdinfo, _ := trackers.ReadBDInfo(req.AppConfig.MainSettings.DBPath, meta)
		parts = append(parts, "[hide=BDInfo][pre]"+bdinfo+"[/pre][/hide]")
	}

	// User description
	if strings.TrimSpace(assets.Description) != "" {
		parts = append(parts, strings.TrimSpace(assets.Description))
	}

	// Tonemapped Header
	if tonemapHeader := strings.TrimSpace(req.AppConfig.Description.TonemappedHeader); tonemapHeader != "" && descriptionunit3d.ShouldIncludeTonemappedHeader(meta, req.AppConfig, assets.Screenshots) {
		parts = append(parts, tonemapHeader)
	}

	// Signature
	link, _ := descriptionunit3d.UppbrrSignatureLink()
	parts = append(parts, fmt.Sprintf("[align=center][url=%s]Upload realizado via %s[/url][/align]", link, "upbrr"))

	// Join and finalize
	description := strings.Join(parts, "\n\n")
	finalized := bbcode.FinalizeTrackerDescription("BJS", description)

	// Debug saving
	if meta.Options.Debug {
		descriptionunit3d.SaveDescriptionDebug(meta, "BJS", req.AppConfig.MainSettings.DBPath, finalized, req.Logger)
	}

	return finalized
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	return wrapTrackerResult(cookies.LoadTrackerHTTPCookies(ctx, dbPath, "BJS", "bj-share.info"))
}

func fetchAuth(ctx context.Context, cookies []*http.Cookie) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uploadURL, nil)
	if err != nil {
		return "", fmt.Errorf("trackers: BJS auth token request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
	if err != nil {
		return "", fmt.Errorf("trackers: BJS auth token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	match := authPattern.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", errors.New("trackers: BJS auth token not found")
	}
	return strings.TrimSpace(match[1]), nil
}

func resolveType(meta api.PreparedMetadata) string {
	if meta.Anime {
		return "13"
	}
	if strings.EqualFold(categoryOf(meta), "TV") {
		return "1"
	}
	return "0"
}

func resolveContainer(meta api.PreparedMetadata) string {
	container := strings.ToLower(strings.TrimSpace(meta.Container))
	switch container {
	case "mkv", "mp4", "avi", "vob", "m2ts", "ts":
		return strings.ToUpper(container)
	default:
		return "Outro"
	}
}

func resolveAudio(meta api.PreparedMetadata) string {
	for _, lang := range meta.AudioLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "portuguese" || lower == "português" || lower == "pt" {
			if len(meta.AudioLanguages) > 1 {
				return "Dual Áudio"
			}
			return "Dublado"
		}
	}
	return "Legendado"
}

func resolveLanguage(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB == nil {
		return "Outro"
	}

	langCode := strings.ToLower(strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage))
	if langCode == "" {
		return "Outro"
	}

	if langCode == "pt" {
		for _, country := range meta.ExternalMetadata.TMDB.OriginCountry {
			if strings.ToUpper(strings.TrimSpace(country)) == "PT" {
				return "Português (pt)"
			}
		}
		return "Português"
	}

	return metautil.ISO639PortugueseName(langCode, "Outro")
}

func resolveSubtitle(meta api.PreparedMetadata) string {
	for _, lang := range meta.SubtitleLanguages {
		lower := strings.ToLower(strings.TrimSpace(lang))
		if lower == "portuguese" || lower == "português" || lower == "pt" {
			return "Embutida"
		}
	}
	return "Nenhuma"
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
	default:
		return "0", "0"
	}
}

func resolveVideoCodec(meta api.PreparedMetadata) string {
	value := strings.ToLower(strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.VideoEncode, meta.VideoCodec)))
	switch {
	case strings.Contains(value, "265"), strings.Contains(value, "hevc"):
		return "H.265"
	case strings.Contains(value, "264"), strings.Contains(value, "avc"):
		return "H.264"
	case strings.Contains(value, "av1"):
		return "AV1"
	case strings.Contains(value, "vp9"):
		return "VP9"
	case strings.Contains(value, "xvid"):
		return "XviD"
	default:
		return metautil.FirstNonEmptyTrimmed(meta.VideoCodec, "Outro")
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

func resolveQuality(meta api.PreparedMetadata) string {
	switch strings.ToUpper(strings.TrimSpace(meta.Type)) {
	case "DISC":
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
		return "DVD9"
	case "REMUX":
		return "Remux"
	case "WEBDL":
		return "WEB-DL"
	case "WEBRIP":
		return "WEBRip"
	case "HDTV":
		return "HDTV"
	default:
		return "Outro"
	}
}

func resolveRuntime(meta api.PreparedMetadata) int {
	for _, candidate := range []string{
		commonhttp.ReadOptionalFile(meta.MediaInfoTextPath),
		strings.TrimSpace(meta.DVDVOBMediaInfoText),
	} {
		if minutes := parseMediaInfoDurationMinutes(candidate); minutes > 0 {
			return minutes
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if minutes := parseBDInfoLengthMinutes(meta.BDInfo["length"]); minutes > 0 {
			return minutes
		}
	}
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.RuntimeMinutes > 0 {
		return meta.ExternalMetadata.IMDB.RuntimeMinutes
	}
	if meta.ExternalMetadata.TMDB != nil {
		return meta.ExternalMetadata.TMDB.Runtime
	}
	if meta.ExternalMetadata.TVmaze != nil {
		return meta.ExternalMetadata.TVmaze.Runtime
	}
	return 0
}

func parseBDInfoLengthMinutes(value any) int {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return 0
	}
	parts := strings.Split(text, ":")
	if len(parts) != 3 {
		return 0
	}
	hours, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || hours < 0 {
		return 0
	}
	minutes, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil || minutes < 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil || seconds < 0 {
		return 0
	}
	totalSeconds := hours*3600 + minutes*60 + seconds
	if totalSeconds <= 0 {
		return 0
	}
	return int(math.Round(totalSeconds / 60))
}

func parseMediaInfoDurationMinutes(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	for _, pattern := range durationPatterns {
		matches := pattern.FindStringSubmatch(trimmed)
		if len(matches) < 2 {
			continue
		}
		if minutes := parseDurationComponentString(matches[1]); minutes > 0 {
			return minutes
		}
	}
	return 0
}

func parseDurationComponentString(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if matches := isoDurationPattern.FindStringSubmatch(trimmed); len(matches) == 4 {
		return roundedDurationMinutes(matches[1], matches[2], matches[3], "")
	}
	return roundedDurationMinutes(
		firstMatch(hoursPattern, trimmed),
		firstMatch(minutesPattern, trimmed),
		firstMatch(secondsPattern, trimmed),
		firstMatch(millisecondsPattern, trimmed),
	)
}

func roundedDurationMinutes(hours string, minutes string, seconds string, milliseconds string) int {
	totalSeconds := parseDurationComponent(hours) * 3600
	totalSeconds += parseDurationComponent(minutes) * 60
	totalSeconds += parseDurationComponent(seconds)
	totalSeconds += parseDurationComponent(milliseconds) / 1000
	if totalSeconds <= 0 {
		return 0
	}
	return int(math.Round(totalSeconds / 60))
}

func parseDurationComponent(value string) float64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func firstMatch(pattern *regexp.Regexp, value string) string {
	matches := pattern.FindStringSubmatch(value)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

func resolveIDLink(meta api.PreparedMetadata) string {
	if meta.ExternalIDs.IMDBID > 0 {
		return fmt.Sprintf("tt%07d", meta.ExternalIDs.IMDBID)
	}
	if meta.ExternalIDs.TMDBID > 0 {
		if strings.EqualFold(categoryOf(meta), "TV") {
			return fmt.Sprintf("tv/%d", meta.ExternalIDs.TMDBID)
		}
		return fmt.Sprintf("movie/%d", meta.ExternalIDs.TMDBID)
	}
	return ""
}

// resolveOverview prefers localized episode synopsis for TV uploads, then
// localized title-level overview, then TMDB or IMDB fallback text.
func resolveOverview(meta api.PreparedMetadata, ptBR api.TMDBLocalizedData) string {
	if strings.EqualFold(categoryOf(meta), "TV") && ptBR.EpisodeOverview != "" {
		return strings.TrimSpace(ptBR.EpisodeOverview)
	}
	if ptBR.Overview != "" {
		return strings.TrimSpace(ptBR.Overview)
	}
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Overview)
	}
	if meta.ExternalMetadata.IMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Plot)
	}
	return ""
}

// resolveTags returns BJS tag text from localized genres or translated fallback
// genres, preserving unknown fallback genre names after tag normalization.
func resolveTags(meta api.PreparedMetadata, ptBR api.TMDBLocalizedData) string {
	// 1. Use localized if available
	if ptBR.Genres != "" {
		genres := strings.Split(strings.TrimSpace(ptBR.Genres), ",")
		out := make([]string, 0, len(genres))
		for _, g := range genres {
			g = strings.TrimSpace(g)
			t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
			g, _, _ = transform.String(t, g)
			g = strings.ReplaceAll(g, " ", ".")
			g = strings.ToLower(g)
			if g != "" {
				out = append(out, g)
			}
		}
		return strings.Join(out, ", ")
	}

	// 2. Use metautil.TranslateGenreToPortugueseStrict to translate
	var genreText string
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres) != "" {
		genreText = strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres)
	} else {
		genreText = strings.TrimSpace(meta.Release.Genre)
	}

	if genreText == "" {
		return ""
	}

	genres := strings.Split(genreText, ",")
	out := make([]string, 0, len(genres))
	for _, g := range genres {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		translated := metautil.TranslateGenreToPortugueseStrict(g)
		if translated == "" {
			translated = g
		}
		t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
		tag, _, _ := transform.String(t, translated)
		tag = strings.ReplaceAll(strings.TrimSpace(tag), " ", ".")
		if tag != "" {
			out = append(out, strings.ToLower(tag))
		}
	}
	return strings.Join(out, ", ")
}

func resolveYouTube(meta api.PreparedMetadata, ptBR api.TMDBLocalizedData) string {
	if ptBR.TrailerURL != "" {
		return strings.TrimSpace(ptBR.TrailerURL)
	}
	if meta.ExternalMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.YouTube)
	}
	return ""
}

func resolveNetworks(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.Networks) > 0 {
		names := make([]string, 0, len(meta.ExternalMetadata.TMDB.Networks))
		for _, n := range meta.ExternalMetadata.TMDB.Networks {
			if strings.TrimSpace(n.Name) != "" {
				names = append(names, strings.TrimSpace(n.Name))
			}
		}
		return strings.Join(names, ", ")
	}
	return ""
}

func resolveReleaseDate(meta api.PreparedMetadata) string {
	rawDate := ""
	if meta.ExternalMetadata.TMDB != nil {
		rawDate = metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.ReleaseDate, meta.ExternalMetadata.TMDB.FirstAirDate)
	}
	if rawDate == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", rawDate)
	if err == nil {
		return t.Format("02 Jan 2006")
	}
	return ""
}

func resolveYearLabel(meta api.PreparedMetadata) string {
	year := resolveYear(meta)
	if strings.EqualFold(categoryOf(meta), "TV") {
		if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.EndYear > 0 {
			return fmt.Sprintf("%d-%d", year, meta.ExternalMetadata.IMDB.EndYear)
		}
		return fmt.Sprintf("%d-", year)
	}
	return strconv.Itoa(year)
}

func resolveRemasterTitle(meta api.PreparedMetadata) string {
	var tags []string

	edition := strings.TrimSpace(meta.Edition)
	editionLower := strings.ToLower(edition)
	editionEntries := []struct{ keyword, label string }{
		{"director's cut", "Director's Cut"},
		{"extended", "Extended Edition"},
		{"imax", "IMAX"},
		{"open matte", "Open Matte"},
		{"noir", "Noir Edition"},
		{"theatrical", "Theatrical Cut"},
		{"uncut", "Uncut"},
		{"unrated", "Unrated"},
		{"uncensored", "Uncensored"},
	}
	for _, entry := range editionEntries {
		if strings.Contains(editionLower, entry.keyword) {
			tags = append(tags, entry.label)
			break
		}
	}

	audio := strings.ToUpper(strings.TrimSpace(meta.Audio))
	if strings.Contains(audio, "ATMOS") {
		tags = append(tags, "Dolby Atmos")
	}

	if meta.BitDepth == "10" {
		tags = append(tags, "10-bit")
	}

	hdr := strings.ToUpper(strings.TrimSpace(meta.HDR))
	if strings.Contains(hdr, "DV") || strings.Contains(hdr, "DOLBY VISION") {
		tags = append(tags, "Dolby Vision")
	}
	if strings.Contains(hdr, "HDR10+") {
		tags = append(tags, "HDR10+")
	}
	if strings.Contains(hdr, "HDR") && !strings.Contains(hdr, "HDR10+") {
		tags = append(tags, "HDR10")
	}

	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		tags = append(tags, "Remux")
	}

	if meta.HasCommentary {
		tags = append(tags, "Com comentários")
	}

	priority := []string{
		"Dolby Atmos",
		"Remux",
		"Director's Cut",
		"Extended Edition",
		"IMAX",
		"Open Matte",
		"Noir Edition",
		"Theatrical Cut",
		"Uncut",
		"Unrated",
		"Uncensored",
		"10-bit",
		"Dolby Vision",
		"HDR10+",
		"HDR10",
		"Com extras",
		"Com comentários",
	}

	tagSet := make(map[string]bool)
	for _, t := range tags {
		tagSet[t] = true
	}

	var ordered []string
	for _, p := range priority {
		if tagSet[p] {
			ordered = append(ordered, p)
		}
	}

	return strings.Join(ordered, " / ")
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

func resolveAdult(meta api.PreparedMetadata) string {
	ptBR := api.ExtractLocalizedPTBR(meta)
	keywords := ""
	if meta.ExternalMetadata.TMDB != nil {
		keywords = metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.Keywords, "")
	}
	genres := strings.ToLower(resolveTags(meta, ptBR) + " " + keywords)
	if meta.Anime && strings.Contains(genres, "hentai") {
		return "1"
	}
	for _, keyword := range []string{"xxx", "erotic", "porn", "adult", "orgy"} {
		if strings.Contains(genres, keyword) {
			return "1"
		}
	}
	return "2"
}

func resolveDirectors(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.Directors) > 0 {
		return firstTrimmed(meta.ExternalMetadata.TMDB.Directors)
	}
	if meta.ExternalMetadata.IMDB != nil {
		names := make([]string, 0, len(meta.ExternalMetadata.IMDB.Directors))
		for _, p := range meta.ExternalMetadata.IMDB.Directors {
			if strings.TrimSpace(p.Name) != "" {
				names = append(names, strings.TrimSpace(p.Name))
			}
		}
		if len(names) > 0 {
			return names[0]
		}
	}
	return ""
}

func resolveCreators(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.Creators) > 0 {
		return firstTrimmed(meta.ExternalMetadata.TMDB.Creators)
	}
	if meta.ExternalMetadata.IMDB != nil {
		names := make([]string, 0, len(meta.ExternalMetadata.IMDB.Creators))
		for _, p := range meta.ExternalMetadata.IMDB.Creators {
			if strings.TrimSpace(p.Name) != "" {
				names = append(names, strings.TrimSpace(p.Name))
			}
		}
		if len(names) > 0 {
			return names[0]
		}
	}
	return ""
}

func resolveCast(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && len(meta.ExternalMetadata.TMDB.Cast) > 0 {
		return strings.Join(firstNTrimmed(meta.ExternalMetadata.TMDB.Cast, 5), ", ")
	}
	if meta.ExternalMetadata.IMDB != nil {
		names := make([]string, 0, len(meta.ExternalMetadata.IMDB.Stars))
		for _, p := range meta.ExternalMetadata.IMDB.Stars {
			if strings.TrimSpace(p.Name) != "" {
				names = append(names, strings.TrimSpace(p.Name))
			}
		}
		return strings.Join(firstNTrimmed(names, 5), ", ")
	}
	return ""
}

func resolveIMDbRating(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.Rating > 0 {
		return strconv.FormatFloat(meta.ExternalMetadata.IMDB.Rating, 'f', 1, 64)
	}
	return ""
}

func resolveLogo(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ExternalMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func resolvePoster(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		if meta.ExternalMetadata.TMDB.Localized != nil {
			if localized, ok := meta.ExternalMetadata.TMDB.Localized["pt-BR"]; ok && strings.TrimSpace(localized.Poster) != "" {
				return strings.TrimSpace(localized.Poster)
			}
		}
		return strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster)
	}
	return ""
}

func resolveScreens(_ api.PreparedMetadata) []string {
	return nil
}

func categoryOf(meta api.PreparedMetadata) string {
	if category := strings.TrimSpace(meta.ExternalIDs.Category); category != "" {
		return category
	}
	return strings.TrimSpace(meta.MediaInfoCategory)
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func firstTrimmed(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNTrimmed(values []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) == limit {
			break
		}
	}
	return out
}

func matchValue(values []string, idx int) string {
	if idx >= 0 && idx < len(values) {
		return values[idx]
	}
	return ""
}
