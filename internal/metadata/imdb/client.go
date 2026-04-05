// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moistari/rls"

	"github.com/autobrr/upbrr/internal/metadata/metautil"

	"github.com/autobrr/upbrr/pkg/api"
)

const defaultBaseURL = "https://api.graphql.imdb.com/"

type Client struct {
	baseURL string
	http    *http.Client
	logger  api.Logger
}

func NewClient(httpClient *http.Client, logger api.Logger) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Client{baseURL: defaultBaseURL, http: httpClient, logger: logger}
}

func (c *Client) GetInfo(ctx context.Context, imdbID string, manualLanguage string, debug bool) (Info, error) {
	info := Info{}
	id := metautil.NormalizeIMDbID(imdbID)
	if id == "" {
		return info, nil
	}

	query := fmt.Sprintf(`query GetTitleInfo { title(id: "%s") { id titleText { text isOriginalTitle country { text } } originalTitleText { text } releaseYear { year endYear } titleType { id } plot { plotText { plainText } } ratingsSummary { aggregateRating voteCount } primaryImage { url } runtime { displayableProperty { value { plainText } } seconds } titleGenres { genres { genre { text } } } principalCredits { category { text id } credits { name { id nameText { text } } } } episodes { episodes(first: 500) { edges { node { id series { displayableEpisodeNumber { displayableSeason { season } episodeNumber { text } } } titleText { text } releaseYear { year } releaseDate { year month day } } } pageInfo { hasNextPage hasPreviousPage } total } } runtimes(first: 10) { edges { node { id seconds displayableProperty { value { plainText } } attributes { text } } } } technicalSpecifications { soundMixes { items { text attributes { text } } } } akas(first: 100) { edges { node { text country { text } language { text } attributes { text } } } } countriesOfOrigin { countries { text } } } }`, escapeGraphQLString(id))

	var response map[string]any
	if err := c.postGraphQL(ctx, query, &response); err != nil {
		return info, err
	}

	titleData := getMap(response, "data", "title")
	if len(titleData) == 0 {
		return info, nil
	}

	info.IMDbID = id
	info.IMDbURL = "https://www.imdb.com/title/" + id
	info.Title = getString(titleData, "titleText", "text")
	countries := getList(titleData, "countriesOfOrigin", "countries")
	if len(countries) > 0 {
		firstCountry := getStringFromMap(countries[0], "text")
		info.Country = firstCountry
		countryNames := make([]string, 0, len(countries))
		for _, item := range countries {
			name := getStringFromMap(item, "text")
			if name != "" {
				countryNames = append(countryNames, name)
			}
		}
		info.CountryList = strings.Join(countryNames, ", ")
	}

	info.Year = getInt(titleData, "releaseYear", "year")
	info.EndYear = getInt(titleData, "releaseYear", "endYear")
	originalTitle := getString(titleData, "originalTitleText", "text")
	if originalTitle != "" && originalTitle != info.Title {
		info.AKA = originalTitle
	} else {
		info.AKA = info.Title
	}
	info.Type = getString(titleData, "titleType", "id")

	runtimeSeconds := getInt(titleData, "runtime", "seconds")
	if runtimeSeconds == 0 {
		runtimeSeconds = 60 * 60
	}
	info.RuntimeMinutes = runtimeSeconds / 60
	info.RuntimeText = strconv.Itoa(info.RuntimeMinutes)

	info.Cover = getString(titleData, "primaryImage", "url")
	plot := getString(titleData, "plot", "plotText", "plainText")
	if plot == "" {
		plot = "No plot available"
	}
	info.Plot = plot

	genres := getList(titleData, "titleGenres", "genres")
	genreNames := make([]string, 0, len(genres))
	for _, genre := range genres {
		name := getStringFromMap(getMapFromMap(genre, "genre"), "text")
		if name != "" {
			genreNames = append(genreNames, name)
		}
	}
	info.Genres = strings.Join(genreNames, ", ")

	rating := getFloat(titleData, "ratingsSummary", "aggregateRating")
	if rating == 0 {
		info.RatingText = "N/A"
	} else {
		info.Rating = rating
		info.RatingText = fmt.Sprintf("%.1f", rating)
	}
	info.RatingCount = getInt(titleData, "ratingsSummary", "voteCount")

	info.Directors = collectCredits(titleData, "Direct")
	info.Creators = collectCredits(titleData, "Creat")
	info.Writers = collectCredits(titleData, "Writ")
	info.Stars = collectCredits(titleData, "Star")

	edges := getList(titleData, "runtimes", "edges")
	if len(edges) > 0 {
		info.EditionDetails = make(map[string]EditionDetail)
		for _, edge := range edges {
			node := getMapFromMap(edge, "node")
			seconds := getIntFromMap(node, "seconds")
			display := getStringFromMap(node, "displayableProperty", "value", "plainText")
			attrs := getListFromMap(node, "attributes")
			attrTexts := make([]string, 0)
			for _, attr := range attrs {
				text := getStringFromMap(attr, "text")
				if text != "" {
					attrTexts = append(attrTexts, text)
				}
			}
			if seconds != 0 && display != "" {
				minutes := seconds / 60
				editionDisplay := fmt.Sprintf("%s (%d min)", display, minutes)
				if len(attrTexts) > 0 {
					editionDisplay = editionDisplay + " [" + strings.Join(attrTexts, ", ") + "]"
				}
				info.Editions = append(info.Editions, editionDisplay)
				runtimeKey := strconv.Itoa(minutes)
				info.EditionDetails[runtimeKey] = EditionDetail{
					DisplayName: display,
					Seconds:     seconds,
					Minutes:     minutes,
					Attributes:  attrTexts,
				}
			}
		}
	}

	akaEdges := getList(titleData, "akas", "edges")
	info.Akas = make([]AKA, 0, len(akaEdges))
	for _, edge := range akaEdges {
		node := getMapFromMap(edge, "node")
		info.Akas = append(info.Akas, AKA{
			Title:      getStringFromMap(node, "text"),
			Country:    getStringFromMap(node, "country", "text"),
			Language:   getStringFromMap(node, "language", "text"),
			Attributes: getStringSlice(node, "attributes"),
		})
	}

	if manualLanguage != "" {
		info.OriginalLanguage = manualLanguage
	}

	info.Episodes = make([]Episode, 0)
	episodesData := getMap(titleData, "episodes", "episodes")
	if len(episodesData) > 0 {
		edges := getListFromMap(episodesData, "edges")
		for _, edge := range edges {
			node := getMapFromMap(edge, "node")
			series := getMapFromMap(node, "series", "displayableEpisodeNumber")
			seasonInfo := getMapFromMap(series, "displayableSeason")
			episodeInfo := getMapFromMap(series, "episodeNumber")
			season := getIntFromMap(seasonInfo, "season")
			releaseYear := getIntFromMap(node, "releaseYear", "year")
			releaseDate := ReleaseDate{
				Year:  getIntFromMap(node, "releaseDate", "year"),
				Month: getIntFromMap(node, "releaseDate", "month"),
				Day:   getIntFromMap(node, "releaseDate", "day"),
			}
			info.Episodes = append(info.Episodes, Episode{
				ID:          getStringFromMap(node, "id"),
				Title:       metautil.FirstNonEmpty(getStringFromMap(node, "titleText", "text"), "Unknown Title"),
				ReleaseYear: releaseYear,
				ReleaseDate: releaseDate,
				Season:      season,
				EpisodeText: getStringFromMap(episodeInfo, "text"),
			})
		}
	}

	if len(info.Episodes) > 0 {
		seasonYears := make(map[int]map[int]struct{})
		for _, ep := range info.Episodes {
			if ep.Season == 0 || ep.ReleaseYear == 0 {
				continue
			}
			if seasonYears[ep.Season] == nil {
				seasonYears[ep.Season] = make(map[int]struct{})
			}
			seasonYears[ep.Season][ep.ReleaseYear] = struct{}{}
		}
		seasons := make([]int, 0, len(seasonYears))
		for season := range seasonYears {
			seasons = append(seasons, season)
		}
		sort.Ints(seasons)
		for _, season := range seasons {
			years := make([]int, 0, len(seasonYears[season]))
			for year := range seasonYears[season] {
				years = append(years, year)
			}
			sort.Ints(years)
			entry := SeasonSummary{
				Season: season,
				Year:   years[0],
			}
			if len(years) == 1 {
				entry.YearRange = strconv.Itoa(years[0])
			} else {
				entry.YearRange = fmt.Sprintf("%d-%d", years[0], years[len(years)-1])
			}
			info.SeasonsSummary = append(info.SeasonsSummary, entry)
		}
	}

	soundMixes := getList(titleData, "technicalSpecifications", "soundMixes", "items")
	for _, mix := range soundMixes {
		text := getStringFromMap(mix, "text")
		if text != "" {
			info.SoundMixes = append(info.SoundMixes, text)
		}
	}

	if info.EndYear != 0 {
		info.TVYear = info.EndYear
	} else if len(info.Episodes) > 0 {
		nowYear := time.Now().UTC().Year()
		closest := 0
		for _, ep := range info.Episodes {
			if ep.ReleaseYear == 0 {
				continue
			}
			if closest == 0 || metautil.AbsInt(ep.ReleaseYear-nowYear) < metautil.AbsInt(closest-nowYear) {
				closest = ep.ReleaseYear
			}
		}
		info.TVYear = closest
	}

	if c.logger != nil {
		c.logger.Infof("imdb: info loaded id=%s title=%q year=%d type=%s", id, info.Title, info.Year, info.Type)
	}

	if debug && c.logger != nil {
		c.logger.Debugf("imdb: info loaded for %s", id)
	}

	return info, nil
}

func (c *Client) Search(ctx context.Context, input SearchInput) (SearchResult, error) {
	results := []map[string]any{}
	imdbID := 0
	attempted := 0

	input = applyReleaseHints(input)
	filename := strings.TrimSpace(input.Filename)
	if filename == "" {
		return SearchResult{}, nil
	}

	category := strings.ToUpper(strings.TrimSpace(input.Category))
	if category == "" {
		category = "MOVIE"
	}

	searchYear := input.SearchYear
	secondary := strings.TrimSpace(input.SecondaryTitle)
	parsedTitle := strings.TrimSpace(input.ParsedTitle)
	if parsedTitle == "" {
		parsedTitle = fallbackParsedTitle(input.UntouchedFilename)
	}

	run := func(name string, year int, wide bool) []map[string]any {
		if attempted > 0 {
			time.Sleep(1 * time.Second)
		}
		attempted++
		return c.runSearch(ctx, name, year, category, input.DurationMinutes, wide)
	}

	if len(results) == 0 {
		results = run(filename, searchYear, false)
	}
	if len(results) == 0 && secondary != "" {
		results = run(secondary, searchYear, true)
	}
	if len(results) == 0 {
		if trimmed := trimLeadingThe(filename); trimmed != "" && trimmed != filename {
			results = run(trimmed, searchYear, false)
		}
	}
	if len(results) == 0 {
		results = run(filename, searchYear, true)
	}
	if len(results) == 0 && parsedTitle != "" {
		results = run(parsedTitle, searchYear, true)
	}
	if len(results) == 0 {
		if reduced := metautil.ReduceTitle(filename, 1); reduced != "" {
			results = run(reduced, searchYear, true)
		}
	}
	if len(results) == 0 {
		if reduced := metautil.ReduceTitle(filename, 2); reduced != "" {
			results = run(reduced, searchYear, true)
		}
	}

	if input.Quickie {
		if len(results) == 0 {
			return SearchResult{}, nil
		}
		first := results[0]
		node := getMapFromMap(first, "node")
		title := getMapFromMap(node, "title")
		typeInfo := strings.ToLower(getStringFromMap(title, "titleType", "text"))
		year := getIntFromMap(title, "releaseYear", "year")
		id := getStringFromMap(title, "id")
		titleText := getStringFromMap(title, "titleText", "text")
		if typeMatches(category, typeInfo) {
			if searchYear > 0 && year != 0 && year != searchYear {
				return SearchResult{}, nil
			}
			imdbID = metautil.ParseIMDbNumeric(id)
		}
		if imdbID != 0 {
			if c.logger != nil {
				c.logger.Infof("imdb: search auto-selected id=%d title=%q year=%d category=%s", imdbID, titleText, year, category)
			}
			return SearchResult{IMDbID: imdbID, AutoSelected: true}, nil
		}
		return SearchResult{}, nil
	}

	if len(results) == 1 {
		imdbID = metautil.ParseIMDbNumeric(getStringFromMap(results[0], "node", "title", "id"))
		if imdbID != 0 {
			if c.logger != nil {
				c.logger.Infof("imdb: search auto-selected single result id=%d", imdbID)
			}
			return SearchResult{IMDbID: imdbID, AutoSelected: true}, nil
		}
	}

	if len(results) > 1 {
		candidates := rankCandidates(results, filename, searchYear)
		if len(candidates) == 0 {
			return SearchResult{}, nil
		}
		best := candidates[0]
		if best.Similarity >= 0.85 {
			second := 0.0
			if len(candidates) > 1 {
				second = candidates[1].Similarity
			}
			if best.Similarity-second >= 0.10 {
				if c.logger != nil {
					c.logger.Infof("imdb: search auto-selected id=%d similarity=%.2f", best.IMDbID, best.Similarity)
				}
				return SearchResult{IMDbID: best.IMDbID, Candidates: candidates, AutoSelected: true}, nil
			}
		}
		if input.Unattended {
			if c.logger != nil {
				c.logger.Infof("imdb: search unattended auto-selected id=%d similarity=%.2f", best.IMDbID, best.Similarity)
			}
			return SearchResult{IMDbID: best.IMDbID, Candidates: candidates, AutoSelected: true}, nil
		}
		return SearchResult{IMDbID: 0, Candidates: candidates}, nil
	}

	return SearchResult{}, nil
}

func applyReleaseHints(input SearchInput) SearchInput {
	base := strings.TrimSpace(input.UntouchedFilename)
	if base == "" {
		base = strings.TrimSpace(input.Filename)
	}
	if base == "" {
		return input
	}
	release := metautil.ParseRelease(base)

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
	if input.SearchYear == 0 && release.Year != 0 {
		input.SearchYear = release.Year
	}
	if strings.TrimSpace(input.ParsedTitle) == "" {
		input.ParsedTitle = mainTitle
	}
	return input
}

func (c *Client) GetEpisodeInfo(ctx context.Context, imdbID string, debug bool) (EpisodeLookup, error) {
	id := metautil.NormalizeIMDbID(imdbID)
	if id == "" {
		return EpisodeLookup{}, nil
	}

	query := fmt.Sprintf(`query { title(id: "%s") { id titleText { text } series { displayableEpisodeNumber { displayableSeason { id season text } episodeNumber { id text } } nextEpisode { id titleText { text } } previousEpisode { id titleText { text } } series { id titleText { text } } } } }`, escapeGraphQLString(id))
	var response map[string]any
	if err := c.postGraphQL(ctx, query, &response); err != nil {
		return EpisodeLookup{}, err
	}
	title := getMap(response, "data", "title")
	if len(title) == 0 {
		return EpisodeLookup{}, nil
	}

	lookup := EpisodeLookup{
		ID:    getStringFromMap(title, "id"),
		Title: getStringFromMap(title, "titleText", "text"),
	}
	series := getMapFromMap(title, "series")
	if len(series) == 0 {
		return lookup, nil
	}

	displayable := getMapFromMap(series, "displayableEpisodeNumber")
	seasonInfo := getMapFromMap(displayable, "displayableSeason")
	episodeInfo := getMapFromMap(displayable, "episodeNumber")
	lookup.Series.SeasonID = getStringFromMap(seasonInfo, "id")
	lookup.Series.Season = getStringFromMap(seasonInfo, "season")
	lookup.Series.SeasonText = getStringFromMap(seasonInfo, "text")
	lookup.Series.EpisodeID = getStringFromMap(episodeInfo, "id")
	lookup.Series.EpisodeText = getStringFromMap(episodeInfo, "text")

	next := getMapFromMap(series, "nextEpisode")
	lookup.NextEpisode = EpisodeRef{
		ID:    getStringFromMap(next, "id"),
		Title: getStringFromMap(next, "titleText", "text"),
	}
	prev := getMapFromMap(series, "previousEpisode")
	lookup.PreviousEpisode = EpisodeRef{
		ID:    getStringFromMap(prev, "id"),
		Title: getStringFromMap(prev, "titleText", "text"),
	}
	seriesObj := getMapFromMap(series, "series")
	lookup.Series.SeriesID = getStringFromMap(seriesObj, "id")
	lookup.Series.SeriesTitle = getStringFromMap(seriesObj, "titleText", "text")

	if debug && c.logger != nil {
		c.logger.Debugf("imdb: episode lookup loaded for %s", id)
	}
	if c.logger != nil {
		c.logger.Infof("imdb: episode lookup loaded id=%s series=%q season=%s episode=%s", id, lookup.Series.SeriesTitle, lookup.Series.SeasonText, lookup.Series.EpisodeText)
	}

	return lookup, nil
}

func (c *Client) runSearch(ctx context.Context, filename string, searchYear int, category string, duration int, wide bool) []map[string]any {
	if filename == "" {
		return nil
	}
	if category == "MOVIE" {
		filename = strings.ReplaceAll(filename, "and", "&")
		filename = strings.ReplaceAll(filename, "And", "&")
		filename = strings.ReplaceAll(filename, "AND", "&")
	}

	constraints := []string{fmt.Sprintf("titleTextConstraint: {searchTerm: \"%s\"}", escapeGraphQLString(filename))}
	if !wide && searchYear > 0 {
		start := searchYear - 1
		end := searchYear + 1
		constraints = append(constraints, fmt.Sprintf("releaseDateConstraint: {releaseDateRange: {start: \"%d-01-01\", end: \"%d-12-31\"}}", start, end))
	}
	if !wide && duration > 0 {
		constraints = append(constraints, fmt.Sprintf("runtimeConstraint: {runtimeRangeMinutes: {min: %d, max: %d}}", duration-10, duration+10))
	}
	constraintsString := strings.Join(constraints, ", ")

	query := fmt.Sprintf(`{ advancedTitleSearch(first: 10, constraints: {%s}) { total edges { node { title { id titleText { text } titleType { text } releaseYear { year } plot { plotText { plainText } } } } } } }`, constraintsString)
	var response map[string]any
	if err := c.postGraphQL(ctx, query, &response); err != nil {
		return nil
	}
	return getList(response, "data", "advancedTitleSearch", "edges")
}

func (c *Client) postGraphQL(ctx context.Context, query string, target any) error {
	payload := map[string]string{"query": query}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("imdb: http %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func collectCredits(data map[string]any, keyword string) []Person {
	credits := getList(data, "principalCredits")
	for _, item := range credits {
		categoryText := getStringFromMap(item, "category", "text")
		if !strings.Contains(categoryText, keyword) {
			continue
		}
		entries := getListFromMap(item, "credits")
		people := make([]Person, 0, len(entries))
		for _, entry := range entries {
			nameObj := getMapFromMap(entry, "name")
			personID := getStringFromMap(nameObj, "id")
			personName := getStringFromMap(nameObj, "nameText", "text")
			if personID != "" && personName != "" {
				people = append(people, Person{ID: personID, Name: personName})
			}
		}
		return people
	}
	return nil
}

func rankCandidates(results []map[string]any, filename string, searchYear int) []Candidate {
	filenameNorm := strings.ToLower(strings.TrimSpace(filename))
	searchYearInt := searchYear
	candidates := make([]Candidate, 0, len(results))

	for _, result := range results {
		node := getMapFromMap(result, "node")
		title := getMapFromMap(node, "title")
		text := getStringFromMap(title, "titleText", "text")
		year := getIntFromMap(title, "releaseYear", "year")
		imdbID := metautil.ParseIMDbNumeric(getStringFromMap(title, "id"))
		plot := getStringFromMap(title, "plot", "plotText", "plainText")
		posterURL := getStringFromMap(title, "primaryImage", "url")
		similarity := metautil.SimilarityRatio(filenameNorm, strings.ToLower(strings.TrimSpace(text)))
		if similarity >= 0.99 && searchYearInt > 0 && year > 0 {
			switch year {
			case searchYearInt:
				similarity += 0.1
			case searchYearInt - 1:
				similarity += 0.05
			}
		}
		candidates = append(candidates, Candidate{IMDbID: imdbID, Title: text, Year: year, Type: getStringFromMap(title, "titleType", "text"), Plot: plot, PosterURL: posterURL, Similarity: similarity})
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

func typeMatches(category, titleType string) bool {
	category = strings.ToLower(category)
	if category == "tv" {
		return strings.Contains(titleType, "tv series")
	}
	return !strings.Contains(titleType, "tv series")
}

func trimLeadingThe(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	if strings.EqualFold(fields[0], "the") {
		return strings.Join(fields[1:], " ")
	}
	return ""
}

func fallbackParsedTitle(untouched string) string {
	trimmed := strings.TrimSpace(untouched)
	if trimmed == "" {
		return ""
	}
	release := rls.ParseString(trimmed)
	return strings.TrimSpace(release.Title)
}

func escapeGraphQLString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return value
}

func getMap(root map[string]any, path ...string) map[string]any {
	value := any(root)
	for _, key := range path {
		m, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = m[key]
	}
	result, _ := value.(map[string]any)
	return result
}

func getList(root map[string]any, path ...string) []map[string]any {
	value := any(root)
	for _, key := range path {
		m, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = m[key]
	}
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	return mapsFromInterface(list)
}

func mapsFromInterface(list []any) []map[string]any {
	items := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items
}

func getMapFromMap(root map[string]any, path ...string) map[string]any {
	return getMap(root, path...)
}

func getListFromMap(root map[string]any, key string) []map[string]any {
	value, ok := root[key]
	if !ok {
		return nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	return mapsFromInterface(list)
}

func getString(root map[string]any, path ...string) string {
	value := any(root)
	for _, key := range path {
		m, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = m[key]
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func getStringFromMap(root map[string]any, path ...string) string {
	value := any(root)
	for _, key := range path {
		m, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = m[key]
	}
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case fmt.Stringer:
		return v.String()
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}

func getInt(root map[string]any, path ...string) int {
	value := any(root)
	for _, key := range path {
		m, ok := value.(map[string]any)
		if !ok {
			return 0
		}
		value = m[key]
	}
	return toInt(value)
}

func getIntFromMap(root map[string]any, path ...string) int {
	return getInt(root, path...)
}

func getFloat(root map[string]any, path ...string) float64 {
	value := any(root)
	for _, key := range path {
		m, ok := value.(map[string]any)
		if !ok {
			return 0
		}
		value = m[key]
	}
	return toFloat(value)
}

func getStringSlice(root map[string]any, key string) []string {
	list := getListFromMap(root, key)
	items := make([]string, 0, len(list))
	for _, item := range list {
		text := getStringFromMap(item, "text")
		if text != "" {
			items = append(items, text)
		}
	}
	return items
}

func toInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if parsed, err := v.Int64(); err == nil {
			return int(parsed)
		}
	case string:
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return 0
}

func toFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		if parsed, err := v.Float64(); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			return parsed
		}
	}
	return 0
}
