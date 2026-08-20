// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
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

var groupPattern = regexp.MustCompile(`groupid=(\d+)|torrents\.php\?id=(\d+)`)

type uploadState struct {
	torrentPath   string
	description   string
	releaseName   string
	fields        map[string][]string
	blockedReason string
	questionnaire *api.TrackerQuestionnaire
}

func prepareUpload(ctx context.Context, req trackers.PreparationInput) (trackers.PreparedOperation, error) {
	if err := standalone.ValidatePreparation(ctx, req, validationPolicy()); err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: validate preparation: %w", err)
	}
	state, cookies, err := prepareUploadState(ctx, req, req.Intent != trackers.PreparationIntentUpload)
	if err != nil {
		return trackers.PreparedOperation{}, err
	}
	preview := buildUploadPreview(state)
	if req.Intent != trackers.PreparationIntentUpload {
		return trackers.NewPreparedOperation(preview, nil, nil), nil
	}
	if state.blockedReason != "" {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: BT %s", state.blockedReason)
	}
	body, contentType, err := commonhttp.BuildMultipartPayloadMulti(state.fields, []commonhttp.FileField{{
		FieldName: "file_input",
		FileName:  filepath.Base(state.torrentPath),
		Path:      state.torrentPath,
	}})

	if err != nil {
		return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
	}
	announceURL := strings.TrimSpace(req.TrackerConfig.AnnounceURL)
	artifactPath := ""
	if announceURL != "" {
		artifactPath, err = trackers.ResolveTrackerTorrentArtifactPath(req.Meta, req.Runtime.DBPath, "BT")
		if err != nil {
			return trackers.PreparedOperation{}, fmt.Errorf("trackers: %w", err)
		}
	}
	return trackers.NewPreparedOperation(preview, func(submitCtx context.Context) (api.UploadSummary, error) {
		return submitPreparedUpload(submitCtx, req, state, cookies, body, contentType, announceURL, artifactPath)
	}, nil), nil
}

func submitPreparedUpload(
	ctx context.Context,
	req trackers.PreparationInput,
	state uploadState,
	cookies []*http.Cookie,
	body []byte,
	contentType string,
	announceURL string,
	artifactPath string,
) (api.UploadSummary, error) {
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
	responseBody, responsePreview, err := commonhttp.ReadUploadResponseBody(
		resp,
		resp.StatusCode >= 200 && resp.StatusCode < 400,
		commonhttp.DefaultResponsePreviewBytes,
	)
	if err != nil {
		return api.UploadSummary{}, fmt.Errorf("trackers: BT read upload response: %w", err)
	}
	match := groupPattern.FindStringSubmatch(finalURL + "\n" + string(responseBody))
	id := metautil.FirstNonEmptyTrimmed(matchValue(match, 1), matchValue(match, 2))
	if resp.StatusCode >= 200 && resp.StatusCode < 400 && id != "" {
		tURL := torrentURL + id
		registeredPath := trackers.PersistReconstructedRegisteredTorrent(
			req.Logger, "BT", state.torrentPath, artifactPath, announceURL, sourceFlag,
		)
		return api.UploadSummary{
			Uploaded: 1,
			UploadedTorrents: []api.UploadedTorrent{{
				Tracker:     "BT",
				TorrentID:   id,
				TorrentURL:  tURL,
				TorrentPath: registeredPath,
			}},
		}, nil
	}
	_, _ = commonhttp.WriteFailureArtifact(req.Meta, req.Runtime.DBPath, "BT", "upload_failure", responsePreview, ".html")
	return api.UploadSummary{}, commonhttp.UploadHTTPError("BT", resp.StatusCode, responsePreview)
}

func buildUploadPreview(state uploadState) api.TrackerDryRunEntry {
	fields := flattenFields(state.fields)
	if _, ok := fields["auth"]; ok {
		fields["auth"] = "[redacted]"
	}
	return standalone.BuildPreview(standalone.PreviewSpec{
		Tracker:          "BT",
		BlockedReason:    state.blockedReason,
		ReleaseName:      state.releaseName,
		DescriptionGroup: "bt",
		Description:      state.description,
		Endpoint:         uploadURL,
		Payload:          fields,
		Questionnaire:    state.questionnaire,
		Files: []api.TrackerDryRunFile{{
			Field:   "file_input",
			Path:    state.torrentPath,
			Present: strings.TrimSpace(state.torrentPath) != "",
		}},
	})
}

func prepareUploadState(ctx context.Context, req trackers.PreparationInput, dryRun bool) (uploadState, []*http.Cookie, error) {
	cookies, err := loadCookies(ctx, req.Runtime.DBPath)
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
	torrentPath, err := trackers.PreparedUploadTorrentPath(req.Meta)
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: %w", err)
	}
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(req, assets)
	fields := buildFields(req, description, auth, req.TrackerConfig, assets)
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		return uploadState{}, nil, fmt.Errorf("trackers: BT reviewed upload name: %w", err)
	}
	state := uploadState{
		torrentPath:   torrentPath,
		description:   description,
		releaseName:   releaseName,
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

func buildFields(
	req trackers.PreparationInput,
	description string,
	auth string,
	trackerCfg config.TrackerConfig,
	assets trackers.DescriptionAssets,
) map[string][]string {
	meta := req.Meta
	answers := standalone.QuestionnaireAnswers(meta, "BT")
	hasPT, subtitleIDs := resolveSubtitle(meta)
	width, height := resolveResolution(meta)
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
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
		"mediainfo":   {trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, meta)},
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

func parseDimensionStr(val any) string {
	return metautil.ParseDimensionStr(val)
}

func removeDiacritics(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

func isSeen(seen map[string]struct{}, url string) bool {
	_, ok := seen[url]
	return ok
}

// shouldUseScopedTVOverview reports whether BT should prefer season or
// episode localized overview over title-level synopsis text.
func shouldUseScopedTVOverview(meta api.UploadSubject) bool {
	if meta.SeasonInt <= 0 {
		return false
	}
	if !isTVUpload(meta) {
		return false
	}
	if meta.TVPack {
		return true
	}
	return meta.EpisodeInt > 0
}

// isTVUpload reports whether canonical identity classifies the upload as TV.

func resolveYouTube(meta api.UploadSubject, ptBR api.TMDBLocalizedData) string {
	youtube := ""
	if ptBR.TrailerURL != "" {
		youtube = strings.TrimSpace(ptBR.TrailerURL)
	} else if meta.ProviderMetadata.TMDB != nil {
		youtube = strings.TrimSpace(meta.ProviderMetadata.TMDB.YouTube)
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

func resolveLogo(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil && strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo) != "" {
		return "https://image.tmdb.org/t/p/w300/" + strings.TrimPrefix(strings.TrimSpace(meta.ProviderMetadata.TMDB.TMDBLogo), "/")
	}
	return ""
}

func resolveYear(meta api.UploadSubject) int {
	if meta.ProviderMetadata.TMDB != nil && meta.ProviderMetadata.TMDB.Year > 0 {
		return meta.ProviderMetadata.TMDB.Year
	}
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Year > 0 {
		return meta.ProviderMetadata.IMDB.Year
	}
	return meta.Release.Year
}

func resolveTitle(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Title, meta.Release.Title)
	}
	return meta.Release.Title
}

func resolveLocalizedTitle(meta api.UploadSubject, ptBR api.TMDBLocalizedData) string {
	if ptBR.Title != "" {
		if meta.ProviderMetadata.TMDB != nil {
			return metautil.FirstNonEmptyTrimmed(ptBR.Title, meta.ProviderMetadata.TMDB.OriginalTitle)
		}
		return ptBR.Title
	}
	if meta.ProviderMetadata.TMDB != nil {
		return metautil.FirstNonEmptyTrimmed(meta.ProviderMetadata.TMDB.Title, meta.ProviderMetadata.TMDB.OriginalTitle)
	}
	return ""
}

func resolveBackdrop(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB != nil {
		return strings.TrimSpace(meta.ProviderMetadata.TMDB.Backdrop)
	}
	return ""
}

func resolveIMDbText(meta api.UploadSubject) string {
	if meta.Identity.IMDBID > 0 {
		return providerid.IMDb(meta.Identity.IMDBID).Prefixed()
	}
	return ""
}

func resolveIMDbRating(meta api.UploadSubject) string {
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Rating > 0 {
		return strconv.FormatFloat(meta.ProviderMetadata.IMDB.Rating, 'f', 1, 64)
	}
	return ""
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
