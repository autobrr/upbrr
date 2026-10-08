// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/providerid"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

type richCreditItem struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
}

type richSeasonItem struct {
	AirDate      string `json:"air_date"`
	EpisodeCount *int   `json:"episode_count"`
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"poster_path"`
	SeasonNumber int    `json:"season_number"`
}

type richEpisodeDetails struct {
	Name      string `json:"name"`
	Overview  string `json:"overview"`
	StillPath string `json:"still_path"`
}

type richMediaResponse struct {
	VoteAverage float64 `json:"vote_average"`
	Homepage    string  `json:"homepage"`
}

func tmdbCachePath(dbPath string, tmdbID int, suffix string) string {
	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	cacheRoot, err := db.Subdir(dbPath, "cache")
	if err != nil {
		return ""
	}
	return filepath.Join(cacheRoot, fmt.Sprintf("tmdb_localized_%d_%s.json", tmdbID, suffix))
}

// fetchRichMain loads the localized TMDB main resource with credits in one
// request and returns the rating/homepage extras and the cast list.
func fetchRichMain(ctx context.Context, client *tmdb.Client, tmdbID int, category string, cachePath string) (richMediaResponse, []richCreditItem, error) {
	data, err := client.GetLocalizedData(ctx, tmdb.LocalizedDataInput{
		TMDBID:           tmdbID,
		Category:         strings.ToLower(category),
		DataType:         "main",
		AppendToResponse: "credits",
		CachePath:        cachePath,
	})
	if err != nil {
		return richMediaResponse{}, nil, fmt.Errorf("fetch rich main: %w", err)
	}
	var media richMediaResponse
	if vote, ok := data["vote_average"].(float64); ok {
		media.VoteAverage = vote
	}
	if homepage, ok := data["homepage"].(string); ok {
		media.Homepage = homepage
	}
	credits, ok := data["credits"].(map[string]any)
	if !ok {
		credits = data
	}
	castRaw, _ := credits["cast"].([]any)
	cast := make([]richCreditItem, 0, len(castRaw))
	for _, item := range castRaw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var credit richCreditItem
		if idVal, ok := m["id"].(float64); ok {
			credit.ID = int(idVal)
		}
		if nameVal, ok := m["name"].(string); ok {
			credit.Name = nameVal
		}
		if charVal, ok := m["character"].(string); ok {
			credit.Character = charVal
		}
		if profileVal, ok := m["profile_path"].(string); ok {
			credit.ProfilePath = profileVal
		}
		cast = append(cast, credit)
	}
	return media, cast, nil
}

func fetchRichSeasons(ctx context.Context, client *tmdb.Client, tmdbID int, cachePath string) ([]richSeasonItem, error) {
	data, err := client.GetLocalizedData(ctx, tmdb.LocalizedDataInput{
		TMDBID:    tmdbID,
		Category:  "tv",
		DataType:  "main",
		Language:  "pt-BR",
		CachePath: cachePath,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch rich seasons: %w", err)
	}
	seasonsRaw, ok := data["seasons"].([]any)
	if !ok {
		return nil, errors.New("no seasons found")
	}
	var seasons []richSeasonItem
	for _, item := range seasonsRaw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var season richSeasonItem
		if airVal, ok := m["air_date"].(string); ok {
			season.AirDate = airVal
		}
		if countVal, ok := m["episode_count"].(float64); ok {
			c := int(countVal)
			season.EpisodeCount = &c
		}
		if idVal, ok := m["id"].(float64); ok {
			season.ID = int(idVal)
		}
		if nameVal, ok := m["name"].(string); ok {
			season.Name = nameVal
		}
		if overVal, ok := m["overview"].(string); ok {
			season.Overview = overVal
		}
		if posterVal, ok := m["poster_path"].(string); ok {
			season.PosterPath = posterVal
		}
		if numVal, ok := m["season_number"].(float64); ok {
			season.SeasonNumber = int(numVal)
		}
		seasons = append(seasons, season)
	}
	return seasons, nil
}

func fetchRichEpisode(ctx context.Context, client *tmdb.Client, tmdbID, season, episode int, cachePath string) (richEpisodeDetails, error) {
	data, err := client.GetLocalizedData(ctx, tmdb.LocalizedDataInput{
		TMDBID:    tmdbID,
		Category:  "tv",
		DataType:  "episode",
		Season:    season,
		Episode:   episode,
		Language:  "pt-BR",
		CachePath: cachePath,
	})
	if err != nil {
		return richEpisodeDetails{}, fmt.Errorf("fetch rich episode: %w", err)
	}
	var ep richEpisodeDetails
	if nameVal, ok := data["name"].(string); ok {
		ep.Name = nameVal
	}
	if overVal, ok := data["overview"].(string); ok {
		ep.Overview = overVal
	}
	if stillVal, ok := data["still_path"].(string); ok {
		ep.StillPath = stillVal
	}
	return ep, nil
}

// buildDescription composes the ASC BBCode description without images: ASC
// rejects images hosted elsewhere and renders cover, screenshots and MediaInfo
// from their own form fields. Final and override descriptions, and the custom
// header, pass through verbatim; validatePayloadFields blocks any that embed
// images hosted outside ASC.
func buildDescription(
	ctx context.Context,
	meta api.UploadSubject,
	cfg config.Config,
	assets trackers.DescriptionAssets,
	logger api.Logger,
) string {
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	assets.Description = trackers.StripDescriptionSignatures(assets.Description)
	if assets.Override && strings.TrimSpace(assets.Description) != "" {
		return strings.TrimSpace(assets.Description)
	}
	rich := fetchRichDetails(ctx, meta, cfg, logger)
	answers := standalone.QuestionnaireAnswers(meta, "ASC")

	parts := []string{"[center]", "[size=20][b]" + resolveUploadTitle(meta) + "[/b][/size]"}
	appendSection := func(heading string, content string) {
		if strings.TrimSpace(content) == "" {
			return
		}
		parts = append(parts, "[b]"+heading+"[/b]\n"+strings.TrimSpace(content))
	}

	appendSection("Sinopse", resolveOverview(meta, answers))
	if categoryOf(meta) == "TV" && rich.episode != nil && rich.episode.Name != "" && rich.episode.Overview != "" {
		appendSection("Episódio: "+rich.episode.Name, rich.episode.Overview)
	}
	appendSection("Ficha técnica", buildTechnicalSheet(meta, rich.media))
	appendSection("Produtoras", buildProductionCompanies(meta))
	appendSection("Elenco", buildCastSection(meta, rich.cast))
	if categoryOf(meta) == "TV" {
		appendSection("Temporadas", buildSeasonsSection(rich.seasons))
	}
	appendSection("Avaliações", buildRatingsBBCode(meta, rich.media))
	parts = append(parts, "[/center]")

	if notes := sanitizeDescriptionNotes(assets.Description); notes != "" {
		parts = append(parts, notes)
	}
	if customHeader := strings.TrimSpace(cfg.Description.CustomDescriptionHeader); customHeader != "" {
		parts = append(parts, customHeader)
	}
	parts = append(parts, "[center][url=https://github.com/autobrr/upbrr]Upload realizado via upbrr[/url][/center]")
	return strings.TrimSpace(strings.Join(filterEmpty(parts), "\n\n"))
}

type richDetails struct {
	media   *richMediaResponse
	cast    []richCreditItem
	seasons []richSeasonItem
	episode *richEpisodeDetails
}

// fetchRichDetails loads optional localized TMDB extras; each lookup failure
// only omits its section and is logged at debug level.
func fetchRichDetails(ctx context.Context, meta api.UploadSubject, cfg config.Config, logger api.Logger) richDetails {
	var rich richDetails
	apiKey := strings.TrimSpace(cfg.MainSettings.TMDBAPI)
	tmdbID := meta.Identity.TMDBID
	if apiKey == "" || tmdbID <= 0 {
		return rich
	}
	client := tmdb.NewClient(nil, nil, apiKey)
	dbPath := cfg.MainSettings.DBPath
	omit := func(section string, err error) {
		logger.Debugf("trackers: ASC tmdb extra omitted tracker=ASC section=%s err=%s", section, redaction.RedactValue(err.Error(), nil))
	}
	if media, cast, err := fetchRichMain(ctx, client, tmdbID, categoryOf(meta), tmdbCachePath(dbPath, tmdbID, "credits")); err == nil {
		rich.media = &media
		rich.cast = cast
	} else {
		omit("main", err)
	}
	if categoryOf(meta) != "TV" {
		return rich
	}
	if seasons, err := fetchRichSeasons(ctx, client, tmdbID, tmdbCachePath(dbPath, tmdbID, "pt_seasons")); err == nil {
		rich.seasons = seasons
	} else {
		omit("seasons", err)
	}
	if meta.SeasonInt > 0 && meta.EpisodeInt > 0 {
		suffix := fmt.Sprintf("ep_%d_%d", meta.SeasonInt, meta.EpisodeInt)
		if ep, err := fetchRichEpisode(ctx, client, tmdbID, meta.SeasonInt, meta.EpisodeInt, tmdbCachePath(dbPath, tmdbID, suffix)); err == nil {
			rich.episode = &ep
		} else {
			omit("episode", err)
		}
	}
	return rich
}

func buildTechnicalSheet(meta api.UploadSubject, richMedia *richMediaResponse) string {
	items := make([]string, 0, 5)
	if runtime := resolveRuntime(meta); runtime != "" {
		items = append(items, "Duração: "+runtime)
	}
	if countries := resolveCountries(meta); countries != "" {
		items = append(items, "País de Origem: "+countries)
	}
	if genres := resolveGenres(meta, standalone.QuestionnaireAnswers(meta, "ASC")); genres != "" {
		items = append(items, "Gêneros: "+genres)
	}
	if releaseDate := resolveReleaseDate(meta); releaseDate != "" {
		items = append(items, "Data de Lançamento: "+formatDate(releaseDate))
	}
	if richMedia != nil {
		if homepageURL := sanitizeHomepageURL(richMedia.Homepage); homepageURL != "" {
			items = append(items, fmt.Sprintf("Site: [url=%s]Clique aqui[/url]", homepageURL))
		}
	}
	return strings.Join(items, "\n")
}

// sanitizeHomepageURL returns an absolute http or https homepage safe for ASC
// BBCode URL attributes, rejecting literal or escaped URL-attribute delimiters.
func sanitizeHomepageURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || containsBBCodeURLAttributeUnsafeChar(trimmed) {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() || strings.TrimSpace(parsed.Host) == "" {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return ""
	}
	normalized := parsed.String()
	if containsBBCodeURLAttributeUnsafeChar(normalized) || hasEscapedBBCodeURLAttributeUnsafeChar(parsed) {
		return ""
	}
	return normalized
}

func containsBBCodeURLAttributeUnsafeChar(value string) bool {
	if strings.ContainsAny(value, "[]\"'") {
		return true
	}
	for _, r := range value {
		if r < ' ' || r == 0x7f {
			return true
		}
	}
	return false
}

func hasEscapedBBCodeURLAttributeUnsafeChar(parsed *url.URL) bool {
	if slices.ContainsFunc([]string{parsed.Path, parsed.Fragment}, containsBBCodeURLAttributeUnsafeChar) {
		return true
	}
	encodedValues := []string{parsed.RawQuery}
	if parsed.User != nil {
		encodedValues = append(encodedValues, parsed.User.String())
	}
	for _, encoded := range encodedValues {
		if encoded == "" {
			continue
		}
		decoded, err := url.QueryUnescape(encoded)
		if err != nil || containsBBCodeURLAttributeUnsafeChar(decoded) {
			return true
		}
	}
	return false
}

func buildProductionCompanies(meta api.UploadSubject) string {
	if meta.ProviderMetadata.TMDB == nil {
		return ""
	}
	names := make([]string, 0, len(meta.ProviderMetadata.TMDB.ProductionCompanies))
	for _, company := range meta.ProviderMetadata.TMDB.ProductionCompanies {
		if name := strings.TrimSpace(company.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func buildCastSection(meta api.UploadSubject, richCast []richCreditItem) string {
	if len(richCast) == 0 {
		names := resolveCast(meta)
		return strings.Join(names[:min(len(names), 10)], "\n")
	}
	parts := make([]string, 0, min(len(richCast), 10))
	for _, person := range richCast[:min(len(richCast), 10)] {
		name := strings.TrimSpace(person.Name)
		if name == "" {
			continue
		}
		line := fmt.Sprintf("[url=https://www.themoviedb.org/person/%d?language=pt-BR]%s[/url]", person.ID, name)
		if character := strings.TrimSpace(person.Character); character != "" {
			line += " como " + character
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n")
}

func buildSeasonsSection(seasons []richSeasonItem) string {
	out := make([]string, 0, len(seasons))
	for _, season := range seasons {
		name := strings.TrimSpace(season.Name)
		if name == "" {
			name = fmt.Sprintf("Temporada %d", season.SeasonNumber)
		}
		var lines []string
		if season.AirDate != "" {
			lines = append(lines, "Data: "+formatDate(season.AirDate))
		}
		if season.EpisodeCount != nil {
			lines = append(lines, fmt.Sprintf("Episódios: %d", *season.EpisodeCount))
		}
		if overview := strings.TrimSpace(season.Overview); overview != "" {
			lines = append(lines, "Sinopse: "+overview)
		}
		out = append(out, fmt.Sprintf("[spoiler=%s]%s[/spoiler]", name, strings.Join(lines, "\n")))
	}
	return strings.Join(out, "\n")
}

func buildRatingsBBCode(meta api.UploadSubject, richMedia *richMediaResponse) string {
	var parts []string
	if meta.ProviderMetadata.IMDB != nil && meta.ProviderMetadata.IMDB.Rating > 0 {
		imdbURL := strings.TrimSpace(meta.ProviderMetadata.IMDB.IMDbURL)
		if imdbURL == "" && meta.Identity.IMDBID > 0 {
			imdbURL = providerid.IMDb(meta.Identity.IMDBID).URL()
		}
		label := "IMDb"
		if imdbURL != "" {
			label = fmt.Sprintf("[url=%s]IMDb[/url]", imdbURL)
		}
		parts = append(parts, fmt.Sprintf("%s: %.1f/10", label, meta.ProviderMetadata.IMDB.Rating))
	}
	if richMedia != nil && richMedia.VoteAverage > 0 && meta.Identity.TMDBID > 0 {
		parts = append(parts, fmt.Sprintf(
			"[url=https://www.themoviedb.org/%s/%d]TMDb[/url]: %.1f/10",
			strings.ToLower(categoryOf(meta)), meta.Identity.TMDBID, richMedia.VoteAverage,
		))
	}
	return strings.Join(parts, "\n")
}

func formatDate(dateStr string) string {
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" || strings.EqualFold(dateStr, "N/A") {
		return "N/A"
	}
	if t, err := time.Parse("2006-01-02", dateStr); err == nil {
		return t.Format("02/01/2006")
	}
	return dateStr
}

func sanitizeDescriptionNotes(value string) string {
	replacer := strings.NewReplacer(
		"[user]", "", "[/user]", "",
		"[align=left]", "", "[/align]", "",
		"[align=right]", "", "[/align]", "",
		"[alert]", "", "[/alert]", "",
		"[note]", "", "[/note]", "",
		"[h1]", "[u][b]", "[/h1]", "[/b][/u]",
		"[h2]", "[u][b]", "[/h2]", "[/b][/u]",
		"[h3]", "[u][b]", "[/h3]", "[/b][/u]",
	)
	return strings.TrimSpace(stripForeignImages(replacer.Replace(value)))
}

var (
	descriptionImagePattern = regexp.MustCompile(`(?is)\[img(?:=[^\]]*)?\]\s*(.*?)\s*\[/img\]`)
	emptyURLWrapperPattern  = regexp.MustCompile(`(?is)\[url=[^\]]*\]\s*\[/url\]`)
)

// stripForeignImages removes image tags ASC would reject, then drops any empty
// [url=…][/url] wrappers.
func stripForeignImages(value string) string {
	stripped := descriptionImagePattern.ReplaceAllStringFunc(value, func(tag string) string {
		match := descriptionImagePattern.FindStringSubmatch(tag)
		if len(match) == 2 && isSiteHostedImage(match[1]) {
			return tag
		}
		return ""
	})
	return emptyURLWrapperPattern.ReplaceAllString(stripped, "")
}

// hasForeignImages reports whether BBCode embeds images ASC would reject.
func hasForeignImages(value string) bool {
	for _, match := range descriptionImagePattern.FindAllStringSubmatch(value, -1) {
		if !isSiteHostedImage(match[1]) {
			return true
		}
	}
	return false
}

func isSiteHostedImage(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == cookieDomain || strings.HasSuffix(host, "."+cookieDomain)
}

func filterEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func prepareDescription(ctx context.Context, req trackers.PreparationInput) (trackers.DescriptionResult, error) {
	select {
	case <-ctx.Done():
		return trackers.DescriptionResult{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		trackers.LogDescriptionAssetResolutionFailure(req.Logger, req.Tracker, err)
		assets = trackers.DescriptionAssets{}
	}
	description := buildDescription(ctx, req.Meta, req.Runtime.DescriptionConfig(), assets, req.Logger)
	return trackers.DescriptionResult{
		Group:       "asc",
		Description: strings.TrimSpace(description),
	}, nil
}
