// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

const defaultBaseURL = "https://api.themoviedb.org/3"

var errNotFound = errors.New("tmdb: not found")

type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
	logger  api.Logger
}

func NewClient(httpClient *http.Client, logger api.Logger, apiKey string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Client{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: defaultBaseURL,
		http:    httpClient,
		logger:  logger,
	}
}

func NormalizeTitle(title string) string {
	value := strings.ToLower(title)
	value = strings.ReplaceAll(value, "&", "and")
	value = strings.ReplaceAll(value, "  ", " ")
	return strings.TrimSpace(value)
}

func (c *Client) FindByExternalID(ctx context.Context, input FindInput) (FindResult, error) {
	imdbID := metautil.NormalizeIMDbID(input.IMDbID)
	filenameSearch := false

	info, _ := c.findByExternal(ctx, imdbID, "imdb_id")
	hasMovie := len(info.MovieResults) > 0
	hasTV := len(info.TVResults) > 0

	if hasMovie && hasTV && input.CategoryPreference != "" {
		pref := strings.ToUpper(strings.TrimSpace(input.CategoryPreference))
		switch pref {
		case "MOVIE":
			if c.logger != nil {
				c.logger.Infof("tmdb: external match imdb=%s selected=movie tmdb_id=%d", imdbID, info.MovieResults[0].ID)
			}
			return FindResult{
				Category:         "MOVIE",
				TMDBID:           info.MovieResults[0].ID,
				OriginalLanguage: info.MovieResults[0].OriginalLanguage,
				FilenameSearch:   filenameSearch,
			}, nil
		case "TV":
			if c.logger != nil {
				c.logger.Infof("tmdb: external match imdb=%s selected=tv tmdb_id=%d", imdbID, info.TVResults[0].ID)
			}
			return FindResult{
				Category:         "TV",
				TMDBID:           info.TVResults[0].ID,
				OriginalLanguage: info.TVResults[0].OriginalLanguage,
				FilenameSearch:   filenameSearch,
			}, nil
		}
	}

	if hasMovie {
		if c.logger != nil {
			c.logger.Infof("tmdb: external match imdb=%s selected=movie tmdb_id=%d", imdbID, info.MovieResults[0].ID)
		}
		return FindResult{
			Category:         "MOVIE",
			TMDBID:           info.MovieResults[0].ID,
			OriginalLanguage: info.MovieResults[0].OriginalLanguage,
			FilenameSearch:   filenameSearch,
		}, nil
	}
	if hasTV {
		if c.logger != nil {
			c.logger.Infof("tmdb: external match imdb=%s selected=tv tmdb_id=%d", imdbID, info.TVResults[0].ID)
		}
		return FindResult{
			Category:         "TV",
			TMDBID:           info.TVResults[0].ID,
			OriginalLanguage: info.TVResults[0].OriginalLanguage,
			FilenameSearch:   filenameSearch,
		}, nil
	}

	if input.TVDBID != 0 {
		infoTVDB, _ := c.findByExternal(ctx, strconv.Itoa(input.TVDBID), "tvdb_id")
		if len(infoTVDB.TVResults) > 0 {
			if c.logger != nil {
				c.logger.Infof("tmdb: external match tvdb=%d selected=tv tmdb_id=%d", input.TVDBID, infoTVDB.TVResults[0].ID)
			}
			return FindResult{
				Category:         "TV",
				TMDBID:           infoTVDB.TVResults[0].ID,
				OriginalLanguage: infoTVDB.TVResults[0].OriginalLanguage,
				FilenameSearch:   filenameSearch,
			}, nil
		}
	}

	filenameSearch = true
	input = applyReleaseHints(input)
	imdbInfo := input.IMDbInfo
	title := ""
	if imdbInfo != nil {
		title = imdbInfo.Title
	}
	if title == "" {
		title = strings.TrimSpace(input.Filename)
	}
	if title == "" {
		return FindResult{FilenameSearch: filenameSearch}, nil
	}

	searchYear := input.SearchYear
	originalLanguage := "en"
	if imdbInfo != nil && imdbInfo.OriginalLanguage != "" {
		originalLanguage = imdbInfo.OriginalLanguage
	}

	secondary := ""
	if imdbInfo != nil {
		secondary = metautil.FirstNonEmpty(imdbInfo.OriginalTitle, imdbInfo.LocalizedTitle)
	}

	category := strings.ToUpper(strings.TrimSpace(input.CategoryPreference))
	if category == "" {
		category = "MOVIE"
	}

	outcome, err := c.SearchID(ctx, SearchInput{
		Filename:       title,
		SearchYear:     searchYear,
		Category:       category,
		SecondaryTitle: secondary,
		Unattended:     input.Unattended,
		DontSwitch:     category == "TV",
		Debug:          input.Debug,
	})
	if err != nil {
		return FindResult{}, err
	}
	if c.logger != nil && outcome.TMDBID != 0 {
		c.logger.Infof("tmdb: search match title=%q year=%d category=%s tmdb_id=%d", title, searchYear, outcome.Category, outcome.TMDBID)
	}
	return FindResult{
		Category:         outcome.Category,
		TMDBID:           outcome.TMDBID,
		OriginalLanguage: originalLanguage,
		FilenameSearch:   filenameSearch,
		Candidates:       outcome.Candidates,
		AutoSelected:     outcome.AutoSelected,
	}, nil
}

func (c *Client) SearchID(ctx context.Context, input SearchInput) (SearchOutcome, error) {
	input = applySearchHints(input)
	category := strings.ToUpper(strings.TrimSpace(input.Category))
	if category == "" {
		category = "MOVIE"
	}

	if result := c.searchTMDb(ctx, input, category); result.TMDBID != 0 || len(result.Candidates) > 0 {
		return result, nil
	}

	if romanConverted := convertRomanNumerals(input.Filename); romanConverted != "" && romanConverted != input.Filename {
		candidate := input
		candidate.Filename = romanConverted
		if result := c.searchTMDb(ctx, candidate, category); result.TMDBID != 0 || len(result.Candidates) > 0 {
			return result, nil
		}
	}

	if input.SecondaryTitle != "" {
		candidate := input
		candidate.Filename = input.SecondaryTitle
		if result := c.searchTMDb(ctx, candidate, category); result.TMDBID != 0 || len(result.Candidates) > 0 {
			return result, nil
		}
	}

	if input.SearchYear > 0 {
		candidate := input
		candidate.SearchYear = input.SearchYear + 1
		if result := c.searchTMDb(ctx, candidate, category); result.TMDBID != 0 || len(result.Candidates) > 0 {
			return result, nil
		}
	}

	if !input.DontSwitch {
		switched := "TV"
		if category == "TV" {
			switched = "MOVIE"
		}
		candidate := input
		candidate.Category = switched
		if result := c.searchTMDb(ctx, candidate, switched); result.TMDBID != 0 || len(result.Candidates) > 0 {
			return result, nil
		}
	}

	reduced := metautil.ReduceTitle(input.Filename, 1)
	if reduced != "" && reduced != input.Filename {
		candidate := input
		candidate.Filename = reduced
		if result := c.searchTMDb(ctx, candidate, category); result.TMDBID != 0 || len(result.Candidates) > 0 {
			return result, nil
		}
	}

	further := metautil.ReduceTitle(input.Filename, 2)
	if further != "" && further != input.Filename {
		candidate := input
		candidate.Filename = further
		if result := c.searchTMDb(ctx, candidate, category); result.TMDBID != 0 || len(result.Candidates) > 0 {
			return result, nil
		}
	}

	return SearchOutcome{TMDBID: 0, Category: category}, nil
}

func applyReleaseHints(input FindInput) FindInput {
	base := strings.TrimSpace(input.Filename)
	if base == "" {
		return input
	}
	release := metautil.ParseRelease(base)
	_ = strings.TrimSpace(input.CategoryPreference)

	mainTitle := release.Title
	secondaryTitle := release.Alt
	if mainTitle == "" {
		mainTitle = release.Subtitle
	}
	if secondaryTitle == "" {
		secondaryTitle = release.Subtitle
	}
	if mainTitle != "" && secondaryTitle == mainTitle {
		secondaryTitle = ""
	}

	if mainTitle != "" {
		input.Filename = mainTitle
	}
	if strings.TrimSpace(input.CategoryPreference) == "" && release.Category != "" {
		input.CategoryPreference = release.Category
	}
	if input.SearchYear == 0 && release.Year != 0 {
		input.SearchYear = release.Year
	}
	if input.IMDbInfo == nil {
		input.IMDbInfo = &IMDbInfo{Title: mainTitle, OriginalTitle: secondaryTitle, Year: release.Year}
	}
	return input
}

func applySearchHints(input SearchInput) SearchInput {
	base := strings.TrimSpace(input.Filename)
	if base == "" && strings.TrimSpace(input.SecondaryTitle) == "" && input.SearchYear == 0 {
		return input
	}
	if base == "" {
		base = strings.TrimSpace(input.SecondaryTitle)
	}
	if base == "" {
		return input
	}
	release := metautil.ParseRelease(base)
	derivedCategory := strings.TrimSpace(input.Category) == "" && release.Category != ""

	mainTitle := release.Title
	secondaryTitle := release.Alt
	if mainTitle == "" {
		mainTitle = release.Subtitle
	}
	if secondaryTitle == "" {
		secondaryTitle = release.Subtitle
	}
	if mainTitle != "" && secondaryTitle == mainTitle {
		secondaryTitle = ""
	}

	if mainTitle != "" {
		input.Filename = mainTitle
	}
	if secondaryTitle != "" {
		input.SecondaryTitle = secondaryTitle
	}
	if strings.TrimSpace(input.Category) == "" && release.Category != "" {
		input.Category = release.Category
	}
	if derivedCategory && release.Category == "TV" {
		input.DontSwitch = true
	}
	if input.SearchYear == 0 && release.Year != 0 {
		input.SearchYear = release.Year
	}
	return input
}

func (c *Client) searchTMDb(ctx context.Context, input SearchInput, category string) SearchOutcome {
	items, err := c.searchTitle(ctx, input.Filename, input.SearchYear, category)
	if err != nil || len(items) == 0 {
		return SearchOutcome{TMDBID: 0, Category: category}
	}

	candidates := selectCandidates(ctx, c, items, input)
	if len(candidates) == 0 {
		return SearchOutcome{TMDBID: 0, Category: category}
	}

	if len(candidates) == 1 {
		return SearchOutcome{TMDBID: candidates[0].TMDBID, Category: category, Candidates: candidates, AutoSelected: true}
	}

	best := candidates[0]
	second := candidates[1]
	if best.Similarity >= 0.75 && best.Similarity-second.Similarity >= 0.10 {
		return SearchOutcome{TMDBID: best.TMDBID, Category: category, Candidates: candidates, AutoSelected: true}
	}

	if input.Unattended {
		return SearchOutcome{TMDBID: best.TMDBID, Category: category, Candidates: candidates, AutoSelected: true}
	}

	return SearchOutcome{TMDBID: 0, Category: category, Candidates: candidates}
}

func selectCandidates(ctx context.Context, c *Client, items []SearchItem, input SearchInput) []Candidate {
	limited := items
	if input.SearchYear > 0 {
		filtered := make([]SearchItem, 0, len(items))
		for _, item := range items {
			if year := resultYear(item); year != 0 && metautil.AbsInt(year-input.SearchYear) <= 2 {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) > 0 {
			limited = filtered
		}
	}
	if len(limited) > 8 {
		limited = limited[:8]
	}

	filenameNorm := NormalizeTitle(input.Filename)
	secondaryNorm := NormalizeTitle(input.SecondaryTitle)
	exactMatches := make([]SearchItem, 0)

	if input.SearchYear > 0 {
		for _, item := range limited {
			title := NormalizeTitle(resultTitle(item))
			orig := NormalizeTitle(resultOriginalTitle(item))
			year := resultYear(item)
			if year == 0 {
				continue
			}
			if year == input.SearchYear || year == input.SearchYear+1 {
				if secondaryNorm != "" && (secondaryNorm == orig || secondaryNorm == title) {
					exactMatches = append(exactMatches, item)
				}
				if filenameNorm == title {
					exactMatches = append(exactMatches, item)
				}
			}
		}
	}

	unique := make(map[int]SearchItem)
	for _, item := range exactMatches {
		unique[item.ID] = item
	}
	if len(unique) == 1 {
		for _, item := range unique {
			return []Candidate{candidateFromItem(item, 1.0)}
		}
	}

	candidates := make([]Candidate, 0, len(limited))
	for _, item := range limited {
		title := NormalizeTitle(resultTitle(item))
		orig := NormalizeTitle(resultOriginalTitle(item))
		mainSimilarity := metautil.SimilarityRatio(filenameNorm, title)
		origSimilarity := metautil.SimilarityRatio(filenameNorm, orig)
		translatedTitle := ""
		translatedSimilarity := 0.0
		secondaryBest := 0.0

		if orig != "" && orig != title {
			if translated, _ := c.GetTranslations(ctx, item.ID, input.Category, "en"); translated != "" {
				translatedTitle = NormalizeTitle(translated)
				translatedSimilarity = metautil.SimilarityRatio(filenameNorm, translatedTitle)
			}
		}

		if secondaryNorm != "" {
			secondaryMain := metautil.SimilarityRatio(secondaryNorm, title)
			secondaryOrig := metautil.SimilarityRatio(secondaryNorm, orig)
			secondaryTrans := 0.0
			if translatedTitle != "" {
				secondaryTrans = metautil.SimilarityRatio(secondaryNorm, translatedTitle)
			}
			secondaryBest = maxFloat(secondaryMain, secondaryOrig, secondaryTrans)
		}

		similarity := blendSimilarity(mainSimilarity, origSimilarity, translatedSimilarity, secondaryBest)
		year := resultYear(item)
		if similarity >= 0.9 && input.SearchYear > 0 && year != 0 && (year == input.SearchYear || year == input.SearchYear+1) {
			similarity += 0.1
		}

		candidates = append(candidates, Candidate{
			TMDBID:        item.ID,
			Title:         resultTitle(item),
			OriginalTitle: resultOriginalTitle(item),
			Year:          year,
			Overview:      item.Overview,
			PosterPath:    item.PosterPath,
			Similarity:    similarity,
		})
	}

	if strings.EqualFold(input.Category, "TV") && len(candidates) > 0 {
		candidates[0].Similarity += 0.05
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Similarity > candidates[j].Similarity
	})

	if len(candidates) > 0 {
		best := candidates[0].Similarity
		if best >= 0.90 {
			filtered := make([]Candidate, 0, len(candidates))
			for _, cand := range candidates {
				if cand.Similarity >= 0.75 {
					filtered = append(filtered, cand)
				}
			}
			if len(filtered) > 0 {
				candidates = filtered
			}
		}
	}

	return candidates
}

func (c *Client) searchTitle(ctx context.Context, filename string, year int, category string) ([]SearchItem, error) {
	params := map[string]string{
		"api_key":       c.apiKey,
		"query":         filename,
		"language":      "en-US",
		"include_adult": "true",
	}
	if year > 0 {
		if strings.EqualFold(category, "MOVIE") {
			params["year"] = strconv.Itoa(year)
		} else {
			params["first_air_date_year"] = strconv.Itoa(year)
		}
	}

	endpoint := "search/movie"
	if strings.EqualFold(category, "TV") {
		endpoint = "search/tv"
	}
	var resp SearchResponse
	if err := c.getJSON(ctx, "/"+endpoint, params, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

func (c *Client) GetTranslations(ctx context.Context, tmdbID int, category, targetLanguage string) (string, error) {
	endpoint := "movie"
	if strings.EqualFold(category, "TV") {
		endpoint = "tv"
	}
	path := fmt.Sprintf("/%s/%d/translations", endpoint, tmdbID)
	params := map[string]string{"api_key": c.apiKey}
	var resp TranslationResponse
	if err := c.getJSON(ctx, path, params, &resp); err != nil {
		return "", err
	}
	for _, translation := range resp.Translations {
		if translation.ISO6391 == targetLanguage {
			return metautil.FirstNonEmpty(translation.Data.Title, translation.Data.Name), nil
		}
	}
	return "", nil
}

func (c *Client) findByExternal(ctx context.Context, externalID, source string) (FindResponse, error) {
	if externalID == "" {
		return FindResponse{}, errNotFound
	}
	params := map[string]string{
		"api_key":         c.apiKey,
		"external_source": source,
	}
	path := "/find/" + url.PathEscape(externalID)
	var resp FindResponse
	if err := c.getJSON(ctx, path, params, &resp); err != nil {
		return FindResponse{}, err
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, path string, params map[string]string, target any) error {
	if strings.TrimSpace(c.apiKey) == "" {
		return errors.New("tmdb: api key missing")
	}

	endpoint := c.baseURL + path
	if len(params) > 0 {
		values := url.Values{}
		for key, value := range params {
			values.Set(key, value)
		}
		endpoint = endpoint + "?" + values.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("tmdb: build request for %s: %w", path, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tmdb: execute request for %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tmdb: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func resultTitle(item SearchItem) string {
	if item.Title != "" {
		return item.Title
	}
	return item.Name
}

func resultOriginalTitle(item SearchItem) string {
	if item.OriginalTitle != "" {
		return item.OriginalTitle
	}
	return item.OriginalName
}

func resultYear(item SearchItem) int {
	date := item.ReleaseDate
	if date == "" {
		date = item.FirstAirDate
	}
	if len(date) < 4 {
		return 0
	}
	value, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return value
}

func convertRomanNumerals(title string) string {
	roman := map[string]string{
		"II": "2", "III": "3", "IV": "4", "V": "5", "VI": "6", "VII": "7", "VIII": "8", "IX": "9", "X": "10",
	}
	words := strings.Fields(title)
	converted := false
	for i, word := range words {
		upper := strings.ToUpper(word)
		if value, ok := roman[upper]; ok {
			words[i] = value
			converted = true
		}
	}
	if !converted {
		return ""
	}
	return strings.Join(words, " ")
}

func blendSimilarity(main, original, translated, secondary float64) float64 {
	if floatClose(translated, 0, 1e-9) {
		if floatClose(secondary, 0, 1e-9) {
			return (main * 0.5) + (original * 0.5)
		}
		return (main * 0.3) + (original * 0.3) + (secondary * 0.4)
	}
	if floatClose(secondary, 0, 1e-9) {
		return (main * 0.5) + (translated * 0.5)
	}
	return (main * 0.5) + (secondary * 0.5)
}

func candidateFromItem(item SearchItem, similarity float64) Candidate {
	return Candidate{
		TMDBID:        item.ID,
		Title:         resultTitle(item),
		OriginalTitle: resultOriginalTitle(item),
		Year:          resultYear(item),
		Overview:      item.Overview,
		PosterPath:    item.PosterPath,
		Similarity:    similarity,
	}
}

func maxFloat(values ...float64) float64 {
	best := 0.0
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func floatClose(value, target, tolerance float64) bool {
	return math.Abs(value-target) <= tolerance
}
