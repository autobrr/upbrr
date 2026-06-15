// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type layoutData struct {
	Images  map[string]string
	Ratings []map[string]any
}

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

func fetchRichMedia(ctx context.Context, client *tmdb.Client, tmdbID int, category string, cachePath string) (richMediaResponse, error) {
	data, err := client.GetLocalizedData(ctx, tmdb.LocalizedDataInput{
		TMDBID:    tmdbID,
		Category:  strings.ToLower(category),
		DataType:  "main",
		CachePath: cachePath,
	})
	if err != nil {
		return richMediaResponse{}, fmt.Errorf("fetch rich media: %w", err)
	}
	var resp richMediaResponse
	if vote, ok := data["vote_average"].(float64); ok {
		resp.VoteAverage = vote
	}
	if homepage, ok := data["homepage"].(string); ok {
		resp.Homepage = homepage
	}
	return resp, nil
}

func fetchRichCredits(ctx context.Context, client *tmdb.Client, tmdbID int, category string, cachePath string) ([]richCreditItem, error) {
	data, err := client.GetLocalizedData(ctx, tmdb.LocalizedDataInput{
		TMDBID:           tmdbID,
		Category:         strings.ToLower(category),
		DataType:         "main",
		AppendToResponse: "credits",
		CachePath:        cachePath,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch rich credits: %w", err)
	}
	credits, ok := data["credits"].(map[string]any)
	if !ok {
		credits = data
	}
	castRaw, ok := credits["cast"].([]any)
	if !ok {
		return nil, errors.New("no cast found")
	}
	var cast []richCreditItem
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
	return cast, nil
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

func buildDescription(ctx context.Context, meta api.PreparedMetadata, cfg config.Config, assets trackers.DescriptionAssets, layoutID string) string {
	if assets.Override && strings.TrimSpace(assets.Description) != "" {
		return strings.TrimSpace(assets.Description)
	}
	layout, _ := fetchLayout(ctx, cfg.MainSettings.DBPath, meta, layoutID)
	parts := []string{"[center]"}

	for idx := 1; idx <= 3; idx++ {
		if image := layout.Images[fmt.Sprintf("BARRINHA_CUSTOM_T_%d", idx)]; image != "" {
			parts = append(parts, formatImage(image))
		}
	}
	if image := layout.Images["BARRINHA_APRESENTA"]; image != "" {
		parts = append(parts, formatImage(image))
	}
	parts = append(parts, "[size=3]"+resolveUploadTitle(meta)+"[/size]")

	appendSection := func(key string, content string) {
		if strings.TrimSpace(content) == "" {
			return
		}
		if image := layout.Images[key]; image != "" {
			parts = append(parts, formatImage(image))
		}
		parts = append(parts, content)
	}

	apiKey := strings.TrimSpace(cfg.MainSettings.TMDBAPI)
	tmdbID := meta.ExternalIDs.TMDBID

	// TMDB Sub-queries
	var richMedia *richMediaResponse
	var richCast []richCreditItem
	var richSeasons []richSeasonItem
	var richEpisode *richEpisodeDetails

	if apiKey != "" && tmdbID > 0 {
		tmdbClient := tmdb.NewClient(nil, nil, apiKey)
		dbPath := cfg.MainSettings.DBPath

		if media, err := fetchRichMedia(ctx, tmdbClient, tmdbID, categoryOf(meta), tmdbCachePath(dbPath, tmdbID, "main")); err == nil {
			richMedia = &media
		}
		if castList, err := fetchRichCredits(ctx, tmdbClient, tmdbID, categoryOf(meta), tmdbCachePath(dbPath, tmdbID, "credits")); err == nil {
			richCast = castList
		}
		if categoryOf(meta) == "TV" {
			if seasons, err := fetchRichSeasons(ctx, tmdbClient, tmdbID, tmdbCachePath(dbPath, tmdbID, "pt_seasons")); err == nil {
				richSeasons = seasons
			}
			if meta.SeasonInt > 0 && meta.EpisodeInt > 0 {
				suffix := fmt.Sprintf("ep_%d_%d", meta.SeasonInt, meta.EpisodeInt)
				if ep, err := fetchRichEpisode(ctx, tmdbClient, tmdbID, meta.SeasonInt, meta.EpisodeInt, tmdbCachePath(dbPath, tmdbID, suffix)); err == nil {
					richEpisode = &ep
				}
			}
		}
	}

	// 1. Poster
	if poster := resolvePoster(meta); poster != "" {
		poster = strings.ReplaceAll(poster, "/t/p/original/", "/t/p/w500/")
		appendSection("BARRINHA_CAPA", formatImage(poster))
	}

	// 2. Overview
	appendSection("BARRINHA_SINOPSE", resolveOverview(meta, questionnaireAnswers(meta)))

	// 3. Episode Specific Section
	if categoryOf(meta) == "TV" && richEpisode != nil {
		if richEpisode.Name != "" && richEpisode.Overview != "" {
			parts = append(parts, fmt.Sprintf("[size=4][b]Episódio:[/b] %s[/size]", richEpisode.Name))
			if strings.TrimSpace(richEpisode.StillPath) != "" {
				stillURL := "https://image.tmdb.org/t/p/w300" + strings.TrimSpace(richEpisode.StillPath)
				parts = append(parts, formatImage(stillURL))
			}
			parts = append(parts, richEpisode.Overview)
		}
	}

	// 4. Technical Sheet
	appendSection("BARRINHA_FICHA_TECNICA", buildTechnicalSheet(meta, richMedia))

	// 5. Production Companies
	if prodComp := buildProductionCompanies(meta); prodComp != "" {
		parts = append(parts, prodComp)
	}

	// 6. Cast
	appendSection("BARRINHA_ELENCO", buildCastSection(meta, richCast))

	// 7. Seasons Section (TV Packs / TV Seasons list)
	if categoryOf(meta) == "TV" && len(richSeasons) > 0 {
		var seasonsContent []string
		for _, s := range richSeasons {
			seasonName := strings.TrimSpace(s.Name)
			if seasonName == "" {
				seasonName = fmt.Sprintf("Temporada %d", s.SeasonNumber)
			}
			posterTemp := ""
			if strings.TrimSpace(s.PosterPath) != "" {
				posterTemp = formatImage("https://image.tmdb.org/t/p/w185" + strings.TrimSpace(s.PosterPath))
			}
			overviewTemp := ""
			if strings.TrimSpace(s.Overview) != "" {
				overviewTemp = "\n\nSinopse:\n" + strings.TrimSpace(s.Overview)
			}
			var innerContentParts []string
			if s.AirDate != "" {
				innerContentParts = append(innerContentParts, "Data: "+formatDate(s.AirDate))
			}
			if s.EpisodeCount != nil {
				innerContentParts = append(innerContentParts, fmt.Sprintf("Episódios: %d", *s.EpisodeCount))
			}
			if posterTemp != "" {
				innerContentParts = append(innerContentParts, posterTemp)
			}
			if overviewTemp != "" {
				innerContentParts = append(innerContentParts, overviewTemp)
			}
			innerContent := strings.Join(innerContentParts, "\n")
			seasonsContent = append(seasonsContent, fmt.Sprintf("\n[spoiler=%s]%s[/spoiler]\n", seasonName, innerContent))
		}
		appendSection("BARRINHA_EPISODIOS", strings.Join(seasonsContent, ""))
	}

	// 8. Ratings Section
	var ratingsList []map[string]any
	ratingsList = append(ratingsList, layout.Ratings...)
	hasIMDbRating := false
	hasTMDbRating := false
	for _, r := range ratingsList {
		source, _ := r["Source"].(string)
		if source == "Internet Movie Database" {
			hasIMDbRating = true
		}
		if source == "TMDb" {
			hasTMDbRating = true
		}
	}
	if !hasIMDbRating && meta.ExternalMetadata.IMDB != nil && meta.ExternalMetadata.IMDB.Rating > 0 {
		ratingsList = append(ratingsList, map[string]any{
			"Source": "Internet Movie Database",
			"Value":  fmt.Sprintf("%.1f/10", meta.ExternalMetadata.IMDB.Rating),
		})
	}
	if !hasTMDbRating && richMedia != nil && richMedia.VoteAverage > 0 {
		ratingsList = append(ratingsList, map[string]any{
			"Source": "TMDb",
			"Value":  fmt.Sprintf("%.1f/10", richMedia.VoteAverage),
		})
	}

	criticsKey := "BARRINHA_CRITICAS" //nolint:misspell
	if categoryOf(meta) == "MOVIE" && layout.Images["BARRINHA_INFORMACOES"] != "" {
		criticsKey = "BARRINHA_INFORMACOES"
	}
	appendSection(criticsKey, buildRatingsBBCode(meta, ratingsList))

	// 9. MediaInfo/BDInfo
	if media := buildMediaInfo(meta, cfg.MainSettings.DBPath); media != "" {
		parts = append(parts, "[spoiler=Informações do Arquivo]\n[left][font=Courier New]"+media+"[/font][/left][/spoiler]")
	}
	if notes := sanitizeDescriptionNotes(assets.Description); notes != "" {
		parts = append(parts, notes)
	}
	if customHeader := strings.TrimSpace(cfg.Description.CustomDescriptionHeader); customHeader != "" {
		parts = append(parts, customHeader)
	}

	for idx := 1; idx <= 3; idx++ {
		if image := layout.Images[fmt.Sprintf("BARRINHA_CUSTOM_B_%d", idx)]; image != "" {
			parts = append(parts, formatImage(image))
		}
	}
	parts = append(parts, "[/center]")
	parts = append(parts, "[center][url=https://github.com/autobrr/upbrr]Upload realizado via upbrr[/url][/center]")
	return strings.TrimSpace(strings.Join(filterEmpty(parts), "\n\n"))
}

func fetchLayout(ctx context.Context, dbPath string, meta api.PreparedMetadata, layoutID string) (layoutData, error) {
	cached, err := readLayoutCache(dbPath, layoutID)
	if err == nil {
		return cached, nil
	}
	cookies, _, err := LoadCookies(ctx, dbPath)
	if err != nil {
		return layoutData{}, err
	}
	form := url.Values{
		"imdb":   {metautil.FirstNonEmptyTrimmed(resolveIMDbIDText(meta), "tt0013442")},
		"layout": {metautil.FirstNonEmptyTrimmed(strings.TrimSpace(layoutID), "2")},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/search.php", strings.NewReader(form.Encode()))
	if err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC create layout request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp, err := httpclient.New(httpclient.DefaultTimeout).Do(req)
	if err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC fetch layout: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return layoutData{}, fmt.Errorf("layout fetch status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC read layout response: %w", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC unmarshal layout response: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload["ASC"], &raw); err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC unmarshal layout data: %w", err)
	}
	layout := normalizeLayout(raw)
	_ = writeLayoutCache(dbPath, layoutID, payload["ASC"])
	return layout, nil
}

func normalizeLayout(raw map[string]any) layoutData {
	layout := layoutData{Images: make(map[string]string)}
	for key, value := range raw {
		if strings.HasPrefix(key, "BARRINHA_") {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" && text != "<nil>" {
				layout.Images[key] = text
			}
		}
	}
	if ratingsVal, ok := raw["Ratings"]; ok {
		if ratingsSlice, ok := ratingsVal.([]any); ok {
			for _, r := range ratingsSlice {
				if rMap, ok := r.(map[string]any); ok {
					layout.Ratings = append(layout.Ratings, rMap)
				}
			}
		}
	}
	return layout
}

func readLayoutCache(dbPath string, layoutID string) (layoutData, error) {
	payload, err := os.ReadFile(layoutCachePath(dbPath, layoutID))
	if err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC read layout cache: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return layoutData{}, fmt.Errorf("trackers: ASC unmarshal layout cache: %w", err)
	}
	return normalizeLayout(raw), nil
}

func writeLayoutCache(dbPath string, layoutID string, payload []byte) error {
	path := layoutCachePath(dbPath, layoutID)
	if path == "" {
		return errors.New("missing layout cache path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("trackers: ASC create layout cache dir: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("trackers: ASC write layout cache: %w", err)
	}
	return nil
}

func layoutCachePath(dbPath string, layoutID string) string {
	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	cacheRoot, err := db.Subdir(dbPath, "cache")
	if err != nil {
		return ""
	}
	return filepath.Join(cacheRoot, "asc_layout_"+metautil.FirstNonEmptyTrimmed(strings.TrimSpace(layoutID), "2")+".json")
}

func buildTechnicalSheet(meta api.PreparedMetadata, richMedia *richMediaResponse) string {
	items := make([]string, 0, 5)
	if runtime := resolveRuntime(meta); runtime != "" {
		items = append(items, "Duração: "+runtime)
	}
	if countries := resolveCountries(meta); countries != "" {
		items = append(items, "País de Origem: "+countries)
	}
	if genres := resolveGenres(meta, questionnaireAnswers(meta)); genres != "" {
		items = append(items, "Gêneros: "+genres)
	}
	if releaseDate := resolveReleaseDate(meta); releaseDate != "" {
		items = append(items, "Data de Lançamento: "+formatDate(releaseDate))
	}
	if richMedia != nil && richMedia.Homepage != "" {
		items = append(items, fmt.Sprintf("Site: [url=%s]Clique aqui[/url]", richMedia.Homepage))
	}
	return strings.Join(items, "\n")
}

func buildProductionCompanies(meta api.PreparedMetadata) string {
	if meta.ExternalMetadata.TMDB == nil || len(meta.ExternalMetadata.TMDB.ProductionCompanies) == 0 {
		return ""
	}
	var parts []string
	parts = append(parts, "[size=4][b]Produtoras[/b][/size]")
	for _, comp := range meta.ExternalMetadata.TMDB.ProductionCompanies {
		if strings.TrimSpace(comp.Name) == "" {
			continue
		}
		logo := ""
		if logoPath := strings.TrimSpace(comp.LogoPath); logoPath != "" {
			logo = formatImage("https://image.tmdb.org/t/p/w45" + logoPath)
		}
		if logo != "" {
			parts = append(parts, fmt.Sprintf("%s[size=2] - [b]%s[/b][/size]", logo, comp.Name))
		} else {
			parts = append(parts, fmt.Sprintf("[size=2][b]%s[/b][/size]", comp.Name))
		}
	}
	return strings.Join(parts, "\n")
}

func buildCastSection(meta api.PreparedMetadata, richCast []richCreditItem) string {
	if len(richCast) == 0 {
		names := resolveCast(meta)
		if len(names) == 0 {
			return ""
		}
		limit := min(len(names), 10)
		parts := make([]string, 0, limit)
		for idx := range limit {
			parts = append(parts, "[size=2][b]"+names[idx]+"[/b][/size]")
		}
		return strings.Join(parts, "\n")
	}

	limit := min(len(richCast), 10)
	var parts []string
	for idx := range limit {
		person := richCast[idx]
		profileURL := "https://i.imgur.com/eCCCtFA.png"
		if strings.TrimSpace(person.ProfilePath) != "" {
			profileURL = "https://image.tmdb.org/t/p/w45" + strings.TrimSpace(person.ProfilePath)
		}
		tmdbURL := fmt.Sprintf("https://www.themoviedb.org/person/%d?language=pt-BR", person.ID)
		imgTag := formatImage(profileURL)

		charName := strings.TrimSpace(person.Character)
		characterInfo := fmt.Sprintf("(%s)", person.Name)
		if charName != "" {
			characterInfo = fmt.Sprintf("(%s) como %s", person.Name, charName)
		}

		parts = append(parts, fmt.Sprintf("[url=%s]%s[/url]\n[size=2][b]%s[/b][/size]\n", tmdbURL, imgTag, characterInfo))
	}
	return strings.Join(parts, "")
}

func buildRatingsBBCode(meta api.PreparedMetadata, ratingsList []map[string]any) string {
	if len(ratingsList) == 0 {
		return ""
	}
	ratingsMap := map[string]string{
		"Internet Movie Database": "[img]https://i.postimg.cc/Pr8Gv4RQ/IMDB.png[/img]",
		"Rotten Tomatoes":         "[img]https://i.postimg.cc/rppL76qC/rotten.png[/img]",
		"Metacritic":              "[img]https://i.postimg.cc/SKkH5pNg/Metacritic45x45.png[/img]",
		"TMDb":                    "[img]https://i.postimg.cc/T13yyzyY/tmdb.png[/img]",
	}
	var parts []string
	for _, rating := range ratingsList {
		source, _ := rating["Source"].(string)
		valueRaw := rating["Value"]
		if source == "" || valueRaw == nil {
			continue
		}
		value := strings.TrimSpace(fmt.Sprint(valueRaw))
		imgTag, ok := ratingsMap[source]
		if !ok {
			continue
		}
		switch source {
		case "Internet Movie Database":
			imdbURL := ""
			if meta.ExternalMetadata.IMDB != nil {
				imdbURL = meta.ExternalMetadata.IMDB.IMDbURL
			}
			if imdbURL == "" && meta.ExternalIDs.IMDBID > 0 {
				imdbURL = fmt.Sprintf("https://www.imdb.com/title/tt%07d", meta.ExternalIDs.IMDBID)
			}
			parts = append(parts, fmt.Sprintf("\n[url=%s]%s[/url]\n[b]%s[/b]\n", imdbURL, imgTag, value))
		case "TMDb":
			category := strings.ToLower(categoryOf(meta))
			tmdbID := meta.ExternalIDs.TMDBID
			parts = append(parts, fmt.Sprintf("[url=https://www.themoviedb.org/%s/%d]%s[/url]\n[b]%s[/b]\n", category, tmdbID, imgTag, value))
		default:
			parts = append(parts, fmt.Sprintf("%s\n[b]%s[/b]\n", imgTag, value))
		}
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

func buildMediaInfo(meta api.PreparedMetadata, dbPath string) string {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		text, _ := readBDSummary(meta, dbPath)
		return text
	case "DVD":
		return metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.DVDVOBMediaInfoText), readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath)))
	default:
		return readTextFileNoErr(strings.TrimSpace(meta.MediaInfoTextPath))
	}
}

func readBDSummary(meta api.PreparedMetadata, dbPath string) (string, error) {
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return readTextFile(paths.BDMVSummaryPath(tmpDir, paths.PrimaryBDMVPlaylist(meta)))
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
	return strings.TrimSpace(replacer.Replace(value))
}

func formatImage(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "[img]" + strings.TrimSpace(value) + "[/img]"
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
