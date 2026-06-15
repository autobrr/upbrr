// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
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
	fields        map[string][]string
	blockedReason string
	questionnaire *api.TrackerQuestionnaire
}

func upload(ctx context.Context, req trackers.UploadRequest) (api.UploadSummary, error) {
	state, cookies, err := prepareUploadState(ctx, req, false)
	if err != nil {
		return api.UploadSummary{}, err
	}
	if state.blockedReason != "" {
		return api.UploadSummary{}, fmt.Errorf("trackers: BT %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayloadMulti(state.fields, []commonhttp.FileField{{
		FieldName: "file_input",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}})

	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: %w", err)
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
	id := metautil.FirstNonEmptyTrimmed(matchValue(match, 1), matchValue(match, 2))
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && id != "" {
		tURL := torrentURL + id
		artifactPath := ""
		if announce := strings.TrimSpace(req.TrackerConfig.AnnounceURL); announce != "" {
			artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.AppConfig.MainSettings.DBPath, "BT")
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
				Tracker:     "BT",
				TorrentID:   id,
				TorrentURL:  tURL,
				DownloadURL: tURL,
				TorrentPath: artifactPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.AppConfig.MainSettings.DBPath, "BT", "upload_failure", responseBody, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("BT", resp.StatusCode, responseBody)
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
		Payload:          flattenFields(state.fields),
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
	fields := buildFields(req, description, auth, req.TrackerConfig, assets)
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   metautil.FirstNonEmptyTrimmed(req.Meta.ReleaseName, req.Meta.Release.Title, req.Meta.Filename),
		fields:        fields,
		questionnaire: buildQuestionnaire(req.Meta, fields),
	}
	switch {
	case len(fields["image"]) == 0 || strings.TrimSpace(fields["image"][0]) == "":
		state.blockedReason = "missing poster URL"
	case len(fields["sinopse"]) == 0 || strings.TrimSpace(fields["sinopse"][0]) == "":
		state.blockedReason = "missing overview"
	case len(fields["tags"]) == 0 || strings.TrimSpace(fields["tags"][0]) == "":
		state.blockedReason = "missing tags"
	}

	return state, cookies, nil
}

func buildFields(req trackers.UploadRequest, description string, auth string, trackerCfg config.TrackerConfig, assets trackers.DescriptionAssets) map[string][]string {
	meta := req.Meta
	answers := questionnaireAnswers(meta)
	hasPT, subtitleIDs := resolveSubtitle(meta)
	width, height := resolveResolution(meta)
	ptBR := api.ExtractLocalizedPTBR(meta)
	fields := map[string][]string{
		"audio_c":     {resolveAudioCodec(meta)},
		"audio":       {resolveAudio(meta)},
		"auth":        {auth},
		"bitrate":     {resolveBitrate(meta)},
		"desc":        {""},
		"diretor":     {resolveDirectors(meta)},
		"duracao":     {fmt.Sprintf("%d min", resolveRuntime(meta))},
		"especificas": {description},
		"format":      {resolveContainer(meta)},
		"idioma_ori":  {resolveLanguage(meta)},
		"image":       {resolvePoster(meta)},
		"legenda":     {hasPT},
		"mediainfo":   {trackers.ReadBDinfoOrMediaInfo(req.AppConfig.MainSettings.DBPath, meta)},
		"resolucao_1": {width},
		"resolucao_2": {height},
		"sinopse":     {metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["overview"]), resolveOverview(meta, ptBR))},
		"submit":      {"true"},
		"tags":        {metautil.FirstNonEmptyTrimmed(strings.TrimSpace(answers["tags"]), resolveTags(meta, ptBR))},
		"title":       {resolveTitle(meta)},
		"type":        {resolveType(meta)},
		"video_c":     {resolveVideoCodec(meta)},
		"year":        {strconv.Itoa(resolveYear(meta))},
		"youtube":     {resolveYouTube(meta, ptBR)},
	}

	fields["subtitles[]"] = append(fields["subtitles[]"], subtitleIDs...)

	screens := resolveScreens(assets)
	fields["screen[]"] = append(fields["screen[]"], screens...)

	category := strings.ToUpper(strings.TrimSpace(categoryOf(meta)))
	if !meta.Anime && (category == "MOVIE" || category == "TV") {
		fields["3d"] = []string{yesNo(meta.Is3D != "")}
		fields["adulto"] = []string{"0"}
		fields["imdb_input"] = []string{resolveIMDbText(meta)}
		fields["nota_imdb"] = []string{resolveIMDbRating(meta)}
		fields["title_br"] = []string{resolveLocalizedTitle(meta, ptBR)}
	}
	if meta.Scene {
		fields["scene"] = []string{"on"}
	}
	if category == "TV" || meta.Anime {
		fields["episodio"] = []string{meta.EpisodeStr}
		fields["ntorrent"] = []string{meta.SeasonStr + meta.EpisodeStr}
		if meta.TVPack {
			fields["temporada"] = []string{meta.SeasonStr}
			fields["tipo"] = []string{"completa"}
		} else {
			fields["temporada_e"] = []string{meta.SeasonStr}
			fields["tipo"] = []string{"ep_individual"}
		}
	}
	if category == "MOVIE" {
		fields["versao"] = []string{resolveEdition(meta)}
	}
	if meta.Anime {
		fields["fundo_torrent"] = []string{resolveBackdrop(meta)}
		fields["rating"] = []string{resolveIMDbRating(meta)}
		fields["releasedate"] = []string{strconv.Itoa(resolveYear(meta))}
		fields["horas"] = []string{""}
		fields["minutos"] = []string{""}
		fields["vote"] = []string{""}
	}
	if trackerCfg.Anon {
		fields["anonymous"] = []string{"1"}
	}
	if trackers.IsInternalGroup(config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"BT": trackerCfg}}}, "BT", meta) {
		fields["internal"] = []string{"1"}
	}
	return fields
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
		parts = append(parts, "[center][img]"+logo+"[/img][/center]")
	}

	// TV Episode details
	epTitle := meta.EpisodeTitle
	epOverview := meta.EpisodeOverview
	ptBR := api.ExtractLocalizedPTBR(meta)
	if ptBR.EpisodeTitle != "" {
		epTitle = ptBR.EpisodeTitle
	}
	if ptBR.EpisodeOverview != "" {
		epOverview = ptBR.EpisodeOverview
	}
	if episode := strings.TrimSpace(epOverview); episode != "" {
		parts = append(parts, "[center]"+strings.TrimSpace(epTitle)+"[/center]")
		parts = append(parts, "[center]"+episode+"[/center]")
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
	parts = append(parts, fmt.Sprintf("[center][url=%s]Upload realizado via %s[/url][/center]", link, "upbrr"))

	// Join and finalize
	description := strings.Join(parts, "\n\n")
	finalized := bbcode.FinalizeTrackerDescription("BT", description)

	// Debug saving
	if meta.Options.Debug {
		descriptionunit3d.SaveDescriptionDebug(meta, "BT", req.AppConfig.MainSettings.DBPath, finalized, req.Logger)
	}

	return finalized
}

func loadCookies(ctx context.Context, dbPath string) ([]*http.Cookie, error) {
	return wrapTrackerResult(cookies.LoadTrackerHTTPCookies(ctx, dbPath, "BT", "brasiltracker.org"))
}

func fetchAuth(ctx context.Context, cookies []*http.Cookie) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uploadURL, nil)
	if err != nil {
		return "", fmt.Errorf("trackers: BT auth token request build: %w", err)
	}
	req.Header.Set("User-Agent", "upbrr")
	commonhttp.ApplyCookies(req, cookies)
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
	if err != nil {
		return "", fmt.Errorf("trackers: BT auth token request: %w", err)
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

var targetSiteIDs = map[string]string{
	"arabic":            "22",
	"bulgarian":         "29",
	"chinese":           "14",
	"croatian":          "23",
	"czech":             "30",
	"danish":            "10",
	"dutch":             "9",
	"english - forçada": "50",
	"english":           "3",
	"estonian":          "38",
	"finnish":           "15",
	"french":            "5",
	"german":            "6",
	"greek":             "26",
	"hebrew":            "40",
	"hindi":             "41",
	"hungarian":         "24",
	"icelandic":         "28",
	"indonesian":        "47",
	"italian":           "16",
	"japanese":          "8",
	"korean":            "19",
	"latvian":           "37",
	"lithuanian":        "39",
	"norwegian":         "12",
	"persian":           "52",
	"polish":            "17",
	"português":         "49",
	"romanian":          "13",
	"russian":           "7",
	"serbian":           "31",
	"slovak":            "42",
	"slovenian":         "43",
	"spanish":           "4",
	"swedish":           "11",
	"thai":              "20",
	"turkish":           "18",
	"ukrainian":         "34",
	"vietnamese":        "25",
}

var sourceAliasMap = map[string]string{
	"arabic":                "arabic",
	"ara":                   "arabic",
	"ar":                    "arabic",
	"brazilian portuguese":  "português",
	"brazilian":             "português",
	"portuguese-br":         "português",
	"pt-br":                 "português",
	"portuguese":            "português",
	"por":                   "português",
	"pt":                    "português",
	"pt-pt":                 "português",
	"português brasileiro":  "português",
	"português":             "português",
	"bulgarian":             "bulgarian",
	"bul":                   "bulgarian",
	"bg":                    "bulgarian",
	"chinese":               "chinese",
	"chi":                   "chinese",
	"zh":                    "chinese",
	"chinese (simplified)":  "chinese",
	"chinese (traditional)": "chinese",
	"cmn-hant":              "chinese",
	"cmn-hans":              "chinese",
	"yue-hant":              "chinese",
	"yue-hans":              "chinese",
	"croatian":              "croatian",
	"hrv":                   "croatian",
	"hr":                    "croatian",
	"scr":                   "croatian",
	"czech":                 "czech",
	"cze":                   "czech",
	"cz":                    "czech",
	"cs":                    "czech",
	"danish":                "danish",
	"dan":                   "danish",
	"da":                    "danish",
	"dutch":                 "dutch",
	"dut":                   "dutch",
	"nl":                    "dutch",
	"english - forced":      "english - forçada",
	"english (forced)":      "english - forçada",
	"en (forced)":           "english - forçada",
	"en-us (forced)":        "english - forçada",
	"english":               "english",
	"eng":                   "english",
	"en":                    "english",
	"en-us":                 "english",
	"en-gb":                 "english",
	"english (cc)":          "english",
	"english - sdh":         "english",
	"estonian":              "estonian",
	"est":                   "estonian",
	"et":                    "estonian",
	"finnish":               "finnish",
	"fin":                   "finnish",
	"fi":                    "finnish",
	"french":                "french",
	"fre":                   "french",
	"fr":                    "french",
	"fr-fr":                 "french",
	"fr-ca":                 "french",
	"german":                "german",
	"ger":                   "german",
	"de":                    "german",
	"greek":                 "greek",
	"gre":                   "greek",
	"el":                    "greek",
	"hebrew":                "hebrew",
	"heb":                   "hebrew",
	"he":                    "hebrew",
	"hindi":                 "hindi",
	"hin":                   "hindi",
	"hi":                    "hindi",
	"hungarian":             "hungarian",
	"hun":                   "hungarian",
	"hu":                    "hungarian",
	"icelandic":             "icelandic",
	"ice":                   "icelandic",
	"is":                    "icelandic",
	"indonesian":            "indonesian",
	"ind":                   "indonesian",
	"id":                    "indonesian",
	"italian":               "italian",
	"ita":                   "italian",
	"it":                    "italian",
	"japanese":              "japanese",
	"jpn":                   "japanese",
	"ja":                    "japanese",
	"korean":                "korean",
	"kor":                   "korean",
	"ko":                    "korean",
	"latvian":               "latvian",
	"lav":                   "latvian",
	"lv":                    "latvian",
	"lithuanian":            "lithuanian",
	"lit":                   "lithuanian",
	"lt":                    "lithuanian",
	"norwegian":             "norwegian",
	"nor":                   "norwegian",
	"no":                    "norwegian",
	"persian":               "persian",
	"fa":                    "persian",
	"far":                   "persian",
	"polish":                "polish",
	"pol":                   "polish",
	"pl":                    "polish",
	"romanian":              "romanian",
	"rum":                   "romanian",
	"ro":                    "romanian",
	"russian":               "russian",
	"rus":                   "russian",
	"ru":                    "russian",
	"serbian":               "serbian",
	"srp":                   "serbian",
	"sr":                    "serbian",
	"scc":                   "serbian",
	"slovak":                "slovak",
	"slo":                   "slovak",
	"sk":                    "slovak",
	"slovenian":             "slovenian",
	"slv":                   "slovenian",
	"sl":                    "slovenian",
	"spanish":               "spanish",
	"spa":                   "spanish",
	"es":                    "spanish",
	"es-es":                 "spanish",
	"es-419":                "spanish",
	"swedish":               "swedish",
	"swe":                   "swedish",
	"sv":                    "swedish",
	"thai":                  "thai",
	"tha":                   "thai",
	"th":                    "thai",
	"turkish":               "turkish",
	"tur":                   "turkish",
	"tr":                    "turkish",
	"ukrainian":             "ukrainian",
	"ukr":                   "ukrainian",
	"uk":                    "ukrainian",
	"vietnamese":            "vietnamese",
	"vie":                   "vietnamese",
	"vi":                    "vietnamese",
}

func resolveSubtitle(meta api.PreparedMetadata) (string, []string) {
	hasPT := "Nao"
	ids := make([]string, 0)
	seen := make(map[string]struct{})

	for _, lang := range meta.SubtitleLanguages {
		cleanLang := strings.ToLower(strings.TrimSpace(lang))

		targetKey, ok := sourceAliasMap[cleanLang]
		if !ok {
			targetKey = cleanLang
		}

		if id, exists := targetSiteIDs[targetKey]; exists {
			if _, alreadySeen := seen[id]; !alreadySeen {
				seen[id] = struct{}{}
				ids = append(ids, id)

				if id == "49" {
					hasPT = "Sim"
				}
			}
		}
	}

	if len(ids) == 0 {
		return "Nao", []string{"44"}
	}

	return hasPT, ids
}

func resolveResolution(meta api.PreparedMetadata) (string, string) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		heightStr := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(meta.Release.Resolution), "p"), "i")
		heightNum, err := strconv.Atoi(heightStr)
		if err == nil && heightNum > 0 {
			widthNum := int(math.Round((16.0 / 9.0) * float64(heightNum)))
			return strconv.Itoa(widthNum), strconv.Itoa(heightNum)
		}
	}

	if meta.MediaInfoJSONPath != "" {
		if payload, err := os.ReadFile(meta.MediaInfoJSONPath); err == nil {
			type mediaInfoDoc struct {
				Media struct {
					Track []map[string]any `json:"track"`
				} `json:"media"`
			}
			var doc mediaInfoDoc
			if err := json.Unmarshal(payload, &doc); err == nil {
				for _, track := range doc.Media.Track {
					trackType, _ := track["@type"].(string)
					if strings.ToLower(trackType) == "video" {
						widthVal := track["Width"]
						heightVal := track["Height"]

						widthStr := parseDimensionStr(widthVal)
						heightStr := parseDimensionStr(heightVal)

						if widthStr != "" && heightStr != "" {
							return widthStr, heightStr
						}
					}
				}
			}
		}
	}

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

func parseDimensionStr(val any) string {
	return metautil.ParseDimensionStr(val)
}

func resolveVideoCodec(meta api.PreparedMetadata) string {
	videoEncode := strings.ToLower(strings.TrimSpace(meta.VideoEncode))
	codecFinal := strings.TrimSpace(meta.VideoCodec)
	isHDR := meta.HDR != ""

	encodeMap := []struct{ Key, Value string }{
		{"x265", "x265"},
		{"h.265", "H.265"},
		{"x264", "x264"},
		{"h.264", "H.264"},
		{"vp9", "VP9"},
		{"xvid", "XviD"},
	}

	for _, item := range encodeMap {
		if strings.Contains(videoEncode, item.Key) {
			if (item.Value == "x265" || item.Value == "H.265") && isHDR {
				return item.Value + " HDR"
			}
			return item.Value
		}
	}

	codecLower := strings.ToLower(codecFinal)
	codecMap := []struct{ Key, Value string }{
		{"hevc", "x265"},
		{"265", "x265"},
		{"avc", "x264"},
		{"264", "x264"},
		{"mpeg-2", "MPEG-2"},
		{"vc-1", "VC-1"},
	}

	for _, item := range codecMap {
		if strings.Contains(codecLower, item.Key) {
			if item.Value == "x265" && isHDR {
				return "x265 HDR"
			}
			return item.Value
		}
	}

	if codecFinal != "" {
		return codecFinal
	}
	return "Outro"
}

func resolveAudioCodec(meta api.PreparedMetadata) string {
	priorityOrder := []string{
		"DTS-X", "E-AC-3 JOC", "TrueHD", "DTS-HD", "PCM", "FLAC", "DTS-ES",
		"DTS", "E-AC-3", "AC3", "AAC", "Opus", "Vorbis", "MP3", "MP2",
	}

	codecMap := map[string][]string{
		"DTS-X":      {"DTS:X"},
		"E-AC-3 JOC": {"DD+ 5.1 Atmos", "DD+ 7.1 Atmos", "ATMOS"},
		"TrueHD":     {"TRUEHD"},
		"DTS-HD":     {"DTS-HD"},
		"PCM":        {"LPCM", "PCM"},
		"FLAC":       {"FLAC"},
		"DTS-ES":     {"DTS-ES"},
		"DTS":        {"DTS"},
		"E-AC-3":     {"DD+", "E-AC-3"},
		"AC3":        {"DD", "AC3"},
		"AAC":        {"AAC"},
		"Opus":       {"OPUS"},
		"Vorbis":     {"VORBIS"},
		"MP2":        {"MP2"},
		"MP3":        {"MP3"},
	}

	audioDescription := strings.ToUpper(strings.TrimSpace(meta.Audio))
	if audioDescription == "" {
		return "Outro"
	}

	for _, codecName := range priorityOrder {
		searchTerms := codecMap[codecName]
		for _, term := range searchTerms {
			if strings.Contains(audioDescription, term) {
				return codecName
			}
		}
	}

	return "Outro"
}

func resolveBitrate(meta api.PreparedMetadata) string {
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	if strings.ToUpper(strings.TrimSpace(meta.Type)) == "DISC" || discType == "BDMV" || discType == "DVD" {
		if discType == "BDMV" {
			size := meta.SourceSize
			switch {
			case size > 66000000000:
				return "BD100"
			case size > 50000000000:
				return "BD66"
			case size > 25000000000:
				return "BD50"
			default:
				return "BD25"
			}
		}
		if discType == "DVD" {
			dvdSize := strings.ToUpper(strings.TrimSpace(meta.Release.Size))
			if dvdSize == "DVD9" || dvdSize == "DVD5" {
				return dvdSize
			}
			return "DVD9"
		}
	}

	sourceType := strings.ToLower(strings.TrimSpace(meta.Type))
	keywordMap := map[string]string{
		"remux":  "Remux",
		"webdl":  "WEB-DL",
		"webrip": "WEBRip",
		"web":    "WEB",
		"encode": "Blu-ray",
		"bdrip":  "BDRip",
		"brrip":  "BRRip",
		"hdtv":   "HDTV",
		"sdtv":   "SDTV",
		"dvdrip": "DVDRip",
		"hd-dvd": "HD-DVD",
		"tvrip":  "TVRip",
	}

	if val, ok := keywordMap[sourceType]; ok {
		return val
	}

	source := strings.ToLower(strings.TrimSpace(meta.Release.Source))
	if val, ok := keywordMap[source]; ok {
		return val
	}

	return "Outro"
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
	case strings.Contains(edition, "noir"):
		return "Noir"
	case strings.Contains(edition, "remaster"):
		return "Remastered"
	default:
		return ""
	}
}

func removeDiacritics(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

func resolveTags(meta api.PreparedMetadata, ptBR api.TMDBLocalizedData) string {
	// 1. Use localized if available
	if ptBR.Genres != "" {
		genres := strings.Split(strings.TrimSpace(ptBR.Genres), ",")
		out := make([]string, 0, len(genres))
		for _, genre := range genres {
			cleaned := removeDiacritics(genre)
			tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(cleaned), " ", "."))
			if tag != "" {
				out = append(out, tag)
			}
		}
		return strings.Join(out, ", ")
	}

	// 2. Use metautil.TranslateGenreToPortugueseStrict to translate
	var genreText string
	switch {
	case meta.ExternalMetadata.TMDB != nil && strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres) != "":
		genreText = strings.TrimSpace(meta.ExternalMetadata.TMDB.Genres)
	case meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres) != "":
		genreText = strings.TrimSpace(meta.ExternalMetadata.IMDB.Genres)
	default:
		genreText = strings.TrimSpace(meta.Release.Genre)
	}

	if genreText == "" {
		return ""
	}

	genres := strings.Split(genreText, ",")
	out := make([]string, 0, len(genres))
	for _, genre := range genres {
		translated := metautil.TranslateGenreToPortugueseStrict(genre)
		if translated == "" {
			translated = genre
		}
		cleaned := removeDiacritics(translated)
		tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(cleaned), " ", "."))
		if tag != "" {
			out = append(out, tag)
		}
	}
	return strings.Join(out, ", ")
}

func resolveRuntime(meta api.PreparedMetadata) int {
	if meta.MediaInfoTextPath != "" {
		if payload, err := os.ReadFile(meta.MediaInfoTextPath); err == nil {
			if minutes := parseMediaInfoDurationMinutes(string(payload)); minutes > 0 {
				return minutes
			}
		}
	}
	if meta.DVDVOBMediaInfoText != "" {
		if minutes := parseMediaInfoDurationMinutes(meta.DVDVOBMediaInfoText); minutes > 0 {
			return minutes
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") && meta.BDInfo != nil {
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

func parseMediaInfoDurationMinutes(content string) int {
	re := regexp.MustCompile(`(?im)^duration\s*:\s*(.+)$`)
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return 0
	}
	val := strings.ToLower(strings.TrimSpace(match[1]))

	hours := 0
	minutes := 0

	hRe := regexp.MustCompile(`(\d+)\s*h`)
	mRe := regexp.MustCompile(`(\d+)\s*m`)

	if hMatch := hRe.FindStringSubmatch(val); len(hMatch) == 2 {
		hours, _ = strconv.Atoi(hMatch[1])
	}
	if mMatch := mRe.FindStringSubmatch(val); len(mMatch) == 2 {
		minutes, _ = strconv.Atoi(mMatch[1])
	}

	total := hours*60 + minutes
	if total > 0 {
		return total
	}

	if ms, err := strconv.ParseFloat(strings.Fields(val)[0], 64); err == nil && ms > 10000 {
		return int(math.Round(ms / 60000.0))
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
	return int(math.Round(totalSeconds / 60.0))
}

func resolveDirectors(meta api.PreparedMetadata) string {
	var directors []string
	seen := make(map[string]struct{})

	addDirector := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			directors = append(directors, name)
		}
	}

	if meta.ExternalMetadata.TMDB != nil {
		for _, name := range meta.ExternalMetadata.TMDB.Directors {
			addDirector(name)
		}
	}
	if meta.ExternalMetadata.IMDB != nil {
		for _, person := range meta.ExternalMetadata.IMDB.Directors {
			addDirector(person.Name)
		}
	}

	if len(directors) > 0 {
		limit := min(len(directors), 5)
		return strings.Join(directors[:limit], ", ")
	}

	return "N/A"
}

func resolvePoster(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		if meta.ExternalMetadata.TMDB.Localized != nil {
			if localized, ok := meta.ExternalMetadata.TMDB.Localized["pt-BR"]; ok && strings.TrimSpace(localized.Poster) != "" {
				return strings.TrimSpace(localized.Poster)
			}
		}
		if strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster) != "" {
			return strings.TrimSpace(meta.ExternalMetadata.TMDB.Poster)
		}
	}
	if meta.ExternalMetadata.IMDB != nil && strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover) != "" {
		return strings.TrimSpace(meta.ExternalMetadata.IMDB.Cover)
	}
	return ""
}

func resolveScreens(assets trackers.DescriptionAssets) []string {
	var screens []string
	seen := make(map[string]struct{})

	for _, image := range assets.MenuImages {
		u := strings.TrimSpace(image.RawURL)
		if u == "" {
			u = strings.TrimSpace(image.ImgURL)
		}
		if u != "" && !isSeen(seen, u) {
			screens = append(screens, u)
			seen[u] = struct{}{}
		}
	}
	for _, image := range assets.Screenshots {
		u := strings.TrimSpace(image.RawURL)
		if u == "" {
			u = strings.TrimSpace(image.ImgURL)
		}
		if u != "" && !isSeen(seen, u) {
			screens = append(screens, u)
			seen[u] = struct{}{}
		}
	}
	return screens
}

func isSeen(seen map[string]struct{}, url string) bool {
	_, ok := seen[url]
	return ok
}

func resolveOverview(meta api.PreparedMetadata, ptBR api.TMDBLocalizedData) string {
	if categoryOf(meta) == "TV" && ptBR.EpisodeOverview != "" {
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

func resolveYouTube(meta api.PreparedMetadata, ptBR api.TMDBLocalizedData) string {
	youtube := ""
	if ptBR.TrailerURL != "" {
		youtube = strings.TrimSpace(ptBR.TrailerURL)
	} else if meta.ExternalMetadata.TMDB != nil {
		youtube = strings.TrimSpace(meta.ExternalMetadata.TMDB.YouTube)
	}

	if strings.Contains(youtube, "youtube.com") || strings.Contains(youtube, "youtu.be") {
		switch {
		case strings.Contains(youtube, "v="):
			parts := strings.Split(youtube, "v=")
			if len(parts) > 1 {
				youtube = parts[1]
			}
		case strings.Contains(youtube, "embed/"):
			parts := strings.Split(youtube, "embed/")
			if len(parts) > 1 {
				youtube = parts[1]
			}
		case strings.Contains(youtube, "youtu.be/"):
			parts := strings.Split(youtube, "youtu.be/")
			if len(parts) > 1 {
				youtube = parts[1]
			}
		}
	}

	if idx := strings.Index(youtube, "&"); idx != -1 {
		youtube = youtube[:idx]
	}
	if idx := strings.Index(youtube, "?"); idx != -1 {
		youtube = youtube[:idx]
	}
	youtube = strings.ReplaceAll(youtube, "/", "")
	return youtube
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

func resolveTitle(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.Title, meta.Release.Title)
	}
	return meta.Release.Title
}

func resolveLocalizedTitle(meta api.PreparedMetadata, ptBR api.TMDBLocalizedData) string {
	if ptBR.Title != "" {
		if meta.ExternalMetadata.TMDB != nil {
			return metautil.FirstNonEmptyTrimmed(ptBR.Title, meta.ExternalMetadata.TMDB.OriginalTitle)
		}
		return ptBR.Title
	}
	if meta.ExternalMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ExternalMetadata.TMDB.Title, meta.ExternalMetadata.TMDB.OriginalTitle)
	}
	return ""
}

func resolveLanguage(meta api.PreparedMetadata) string {
	var lang string
	if meta.ExternalMetadata.TMDB != nil {
		lang = strings.TrimSpace(meta.ExternalMetadata.TMDB.OriginalLanguage)
	}
	if lang == "" {
		if len(meta.Release.Language) > 0 {
			lang = meta.Release.Language[0]
		}
	}
	lang = strings.ToLower(lang)
	if lang == "" {
		return ""
	}

	return metautil.ISO639PortugueseName(lang, lang)
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

func yesNo(value bool) string {
	if value {
		return "Sim"
	}
	return "Nao"
}

func flattenFields(in map[string][]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, values := range in {
		if len(values) > 0 {
			out[key] = strings.Join(values, ", ")
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

func buildQuestionnaire(meta api.PreparedMetadata, fields map[string][]string) *api.TrackerQuestionnaire {
	current := questionnaireAnswers(meta)
	var items []api.TrackerQuestionnaireField

	sinopse := ""
	if len(fields["sinopse"]) > 0 {
		sinopse = strings.TrimSpace(fields["sinopse"][0])
	}
	if sinopse == "" {
		items = append(items, api.TrackerQuestionnaireField{
			Key: "overview", Label: "Overview", Kind: "textarea", Value: current["overview"], Required: true,
		})
	}

	tags := ""
	if len(fields["tags"]) > 0 {
		tags = strings.TrimSpace(fields["tags"][0])
	}
	if tags == "" {
		items = append(items, api.TrackerQuestionnaireField{
			Key: "tags", Label: "Tags", Kind: "text", Value: current["tags"], Required: true,
		})
	}

	if len(items) == 0 {
		return nil
	}
	return &api.TrackerQuestionnaire{Tracker: "BT", Fields: items}
}

func questionnaireAnswers(meta api.PreparedMetadata) map[string]string {
	if len(meta.TrackerQuestionnaireAnswers) == 0 {
		return nil
	}
	return meta.TrackerQuestionnaireAnswers["BT"]
}
