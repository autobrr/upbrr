// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	defaultBaseURL = "https://api4.thetvdb.com/v4"
	// maxTVDBResponseBodyBytes bounds TVDB metadata responses before status
	// detail formatting or JSON decode.
	maxTVDBResponseBodyBytes = 16 << 20
)

var (
	errNotFound             = errors.New("tvdb: not found")
	yearAliasPattern        = regexp.MustCompile(`\((\d{4})\)`)
	yearPattern             = regexp.MustCompile(`(?:19|20)\d{2}`)
	nonAlphaNumSpacePattern = regexp.MustCompile(`[^a-z0-9 ]+`)
	multiSpacePattern       = regexp.MustCompile(`\s+`)
)

const (
	seriesYearSourceTranslationName  = "translation_name"
	seriesYearSourceTranslationAlias = "translation_alias"
	seriesYearSourceExtendedAlias    = "extended_alias"
	seriesYearSourceSlug             = "slug"

	seriesYearConfidenceHigh = "high"
	seriesYearConfidenceLow  = "low"
)

var cityTimezoneMap = map[string]string{
	"sydney":      "Australia/Sydney",
	"melbourne":   "Australia/Melbourne",
	"brisbane":    "Australia/Brisbane",
	"perth":       "Australia/Perth",
	"adelaide":    "Australia/Adelaide",
	"hobart":      "Australia/Hobart",
	"darwin":      "Australia/Darwin",
	"canberra":    "Australia/Sydney",
	"auckland":    "Pacific/Auckland",
	"wellington":  "Pacific/Auckland",
	"london":      "Europe/London",
	"dublin":      "Europe/Dublin",
	"toronto":     "America/Toronto",
	"vancouver":   "America/Vancouver",
	"montreal":    "America/Toronto",
	"new york":    "America/New_York",
	"los angeles": "America/Los_Angeles",
	"chicago":     "America/Chicago",
}

var locationTimezoneMap = map[string]string{
	"australia":      "Australia/Sydney",
	"new zealand":    "Pacific/Auckland",
	"united kingdom": "Europe/London",
	"ireland":        "Europe/Dublin",
	"canada":         "America/Toronto",
	"united states":  "America/New_York",
	"usa":            "America/New_York",
}

type Client struct {
	baseURL  string
	apiKey   string
	http     *http.Client
	logger   api.Logger
	cacheDir string

	mu        sync.Mutex
	authToken string
}

type Option func(*Client)

func NewClient(httpClient *http.Client, logger api.Logger, apiKey, cacheDir string, opts ...Option) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if logger == nil {
		logger = api.NopLogger{}
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = embeddedKey()
	}
	client := &Client{
		baseURL:  defaultBaseURL,
		apiKey:   key,
		http:     httpClient,
		logger:   logger,
		cacheDir: strings.TrimSpace(cacheDir),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	return client
}

func (c *Client) SearchSeries(ctx context.Context, filename, year string) ([]SeriesSearchResult, int, error) {
	filename, year = applyReleaseHints(filename, year)
	results, err := c.searchSeries(ctx, filename, year)
	if err != nil {
		return nil, 0, err
	}
	if len(results) == 0 {
		return nil, 0, nil
	}

	selected := selectBestSeries(results, year)
	if c.logger != nil {
		c.logger.Infof("tvdb: series search query=%q year=%q results=%d selected_id=%d", filename, year, len(results), selected)
	}
	return results, selected, nil
}

func applyReleaseHints(filename, year string) (string, string) {
	if strings.TrimSpace(filename) == "" {
		return filename, year
	}
	release := metautil.ParseRelease(filename)
	mainTitle := release.Title
	if mainTitle == "" {
		mainTitle = release.Alt
	}
	if mainTitle == "" {
		mainTitle = release.Subtitle
	}
	if mainTitle != "" {
		filename = mainTitle
	}
	if strings.TrimSpace(year) == "" && release.Year != 0 {
		year = strconv.Itoa(release.Year)
	}
	return filename, year
}

func (c *Client) GetEpisodes(ctx context.Context, seriesID int, query EpisodeQuery) (EpisodesData, string, error) {
	return c.GetEpisodesWithLanguage(ctx, seriesID, query, "eng")
}

func (c *Client) GetEpisodesWithLanguage(ctx context.Context, seriesID int, query EpisodeQuery, language string) (EpisodesData, string, error) {
	if seriesID == 0 {
		return EpisodesData{}, "", errNotFound
	}

	languageKey := normalizeLanguageParam(language)
	if languageKey == "" {
		languageKey = "orig"
	}

	cachePath := c.cachePath(query.CacheBasePath, seriesID, languageKey)
	if cachePath != "" {
		if cached, ok := readEpisodesCache(cachePath); ok {
			if episodeIsPresent(cached.Episodes, query) {
				if c.logger != nil {
					c.logger.Tracef("tvdb: episodes cache hit series_id=%d language=%s episodes=%d", seriesID, languageKey, len(cached.Episodes))
				}
				if upgraded, ok := applyLegacySeriesYearProvenance(cached); ok {
					cached = upgraded
					_ = writeEpisodesCache(cachePath, cached)
				}
				needsSeriesDetails := strings.TrimSpace(cached.SeriesTitle) == "" && cached.SeriesYear == 0
				needsSeriesDetails = needsSeriesDetails || (cached.SeriesYear > 0 && strings.TrimSpace(cached.SeriesYearSource) == "")
				if needsSeriesDetails {
					if details, err := c.fetchSeriesDetails(ctx, seriesID, language); err == nil {
						cached = applySeriesDetails(cached, details)
						if cachePath != "" {
							_ = writeEpisodesCache(cachePath, cached)
						}
					} else if c.logger != nil {
						c.logger.Debugf("tvdb: cached episodes series metadata refresh failed series_id=%d: %v", seriesID, err)
					}
				}
				return cached, specificSeriesAlias(cached), nil
			}
			if c.logger != nil {
				c.logger.Debugf("tvdb: cached episodes missing requested data for %d language=%s", seriesID, languageKey)
			}
		}
	}

	episodes, slug, err := c.fetchEpisodes(ctx, seriesID, language)
	if err != nil {
		return EpisodesData{}, "", err
	}
	details := seriesDetails{}
	if fetchedDetails, err := c.fetchSeriesDetails(ctx, seriesID, language); err == nil {
		details = fetchedDetails
	}
	data := EpisodesData{
		Episodes:             episodes,
		Aliases:              details.aliases,
		Slug:                 slug,
		SeriesTitle:          details.seriesTitle,
		SeriesYear:           details.seriesYear,
		SeriesYearSource:     details.seriesYearSource,
		SeriesYearConfidence: details.seriesYearConfidence,
		AirsDays:             details.airsDays,
		AirsTime:             details.airsTime,
		AirsTimezone:         details.airsTimezone,
		AirsTimezoneSource:   details.airsTimezoneSource,
	}

	if cachePath != "" && len(episodes) > 0 {
		_ = writeEpisodesCache(cachePath, data)
	}
	if c.logger != nil {
		c.logger.Debugf("tvdb: episodes loaded series_id=%d language=%s episodes=%d aliases=%d", seriesID, languageKey, len(episodes), len(details.aliases))
	}

	return data, specificSeriesAlias(data), nil
}

func (c *Client) GetByExternalID(ctx context.Context, imdbID, tmdbID string, tvMovie bool) (int, string, error) {
	imdbRemote := normalizeIMDbRemote(imdbID)
	if imdbRemote != "" {
		id, name, ok, err := c.searchRemoteID(ctx, imdbRemote, tvMovie)
		if err != nil {
			return 0, "", err
		}
		if ok {
			if c.logger != nil {
				c.logger.Infof("tvdb: external match imdb=%s tvdb_id=%d name=%q", imdbRemote, id, name)
			}
			return id, name, nil
		}
	}

	if strings.TrimSpace(tmdbID) != "" {
		id, name, ok, err := c.searchRemoteID(ctx, tmdbID, tvMovie)
		if err != nil {
			return 0, "", err
		}
		if ok {
			if c.logger != nil {
				c.logger.Infof("tvdb: external match tmdb=%s tvdb_id=%d name=%q", tmdbID, id, name)
			}
			return id, name, nil
		}
	}

	return 0, "", nil
}

func (c *Client) GetSeriesMetadata(ctx context.Context, seriesID int) (SeriesMetadata, error) {
	return c.GetSeriesMetadataWithLanguage(ctx, seriesID, "eng")
}

func (c *Client) GetSeriesMetadataWithLanguage(ctx context.Context, seriesID int, language string) (SeriesMetadata, error) {
	if seriesID == 0 {
		return SeriesMetadata{}, errNotFound
	}

	path := fmt.Sprintf("/series/%d/extended", seriesID)
	var resp seriesExtendedResponse
	if err := c.getJSON(ctx, path, c.languageParamsFor(nil, language), &resp); err != nil {
		return SeriesMetadata{}, err
	}

	metadata := SeriesMetadata{
		TVDBID:           metautil.FirstInt(resp.Data.ID, seriesID),
		Name:             strings.TrimSpace(resp.Data.Name),
		Overview:         strings.TrimSpace(resp.Data.Overview),
		NameEnglish:      deriveEnglishSeriesName(resp.Data, language),
		OverviewEnglish:  deriveEnglishSeriesOverview(resp.Data, language),
		Slug:             strings.TrimSpace(resp.Data.Slug),
		FirstAired:       strings.TrimSpace(resp.Data.FirstAired),
		Type:             strings.TrimSpace(resp.Data.Type.Name),
		Status:           strings.TrimSpace(resp.Data.Status.Name),
		Network:          extractNetworkName(resp.Data),
		OriginalCountry:  strings.TrimSpace(resp.Data.OriginalCountry),
		OriginalLanguage: strings.TrimSpace(resp.Data.OriginalLanguage),
		Genres:           mapGenreNames(resp.Data.Genres),
		Poster:           extractPosterURL(resp.Data),
		Aliases:          mapAliases(resp.Data.Aliases),
	}
	seriesTranslation, translationErr := c.fetchSeriesTranslation(ctx, metadata.TVDBID, "eng")
	if translationErr != nil && c.logger != nil {
		c.logger.Debugf("tvdb: series english translation lookup failed series_id=%d: %v", metadata.TVDBID, translationErr)
	}
	seriesMeta := seriesTranslationMetadata(resp.Data, seriesTranslation)
	if strings.TrimSpace(seriesMeta.title) != "" {
		metadata.NameEnglish = metautil.FirstNonEmptyTrimmed(seriesMeta.title, metadata.NameEnglish)
	}
	metadata.SeriesYear = seriesMeta.year
	metadata.SeriesYearSource = seriesMeta.yearSource
	metadata.SeriesYearConfidence = seriesMeta.yearConfidence

	if metadata.Name == "" && len(resp.Data.Aliases) > 0 {
		metadata.Name = strings.TrimSpace(resp.Data.Aliases[0].Name)
	}
	if !isEnglishCode(metadata.OriginalLanguage) {
		needsEnglishName := strings.TrimSpace(metadata.NameEnglish) == "" && containsEnglishTranslation(resp.Data.NameTranslations)
		needsEnglishOverview := strings.TrimSpace(metadata.OverviewEnglish) == "" && containsEnglishTranslation(resp.Data.OverviewTranslations)
		if needsEnglishName || needsEnglishOverview {
			if translationErr == nil {
				if needsEnglishName {
					metadata.NameEnglish = metautil.FirstNonEmptyTrimmed(seriesTranslation.Name, metadata.NameEnglish)
				}
				if needsEnglishOverview {
					metadata.OverviewEnglish = metautil.FirstNonEmptyTrimmed(seriesTranslation.Overview, metadata.OverviewEnglish)
				}
			}
		}
	}
	metadata.HasEnglish = strings.TrimSpace(metadata.NameEnglish) != "" || strings.TrimSpace(metadata.OverviewEnglish) != ""

	if c.logger != nil {
		c.logger.Tracef(
			"tvdb: series metadata loaded series_id=%d language=%q name=%q first_aired=%q api_year=%d series_year=%d series_year_source=%q series_year_confidence=%q name_english=%q slug=%q",
			seriesID,
			normalizeLanguageParam(language),
			metadata.Name,
			metadata.FirstAired,
			int(resp.Data.Year),
			metadata.SeriesYear,
			metadata.SeriesYearSource,
			metadata.SeriesYearConfidence,
			metadata.NameEnglish,
			metadata.Slug,
		)
	}

	return metadata, nil
}

func (c *Client) GetIMDBFromEpisodeID(ctx context.Context, episodeID int) (string, error) {
	if episodeID == 0 {
		return "", errNotFound
	}

	path := fmt.Sprintf("/episodes/%d/extended", episodeID)
	var resp episodeExtendedResponse
	if err := c.getJSON(ctx, path, nil, &resp); err != nil {
		return "", err
	}
	for _, remote := range resp.Data.RemoteIDs {
		if remote.Type == 2 || strings.EqualFold(remote.SourceName, "IMDB") {
			imdbID := strings.TrimSpace(remote.ID)
			if c.logger != nil {
				c.logger.Infof("tvdb: episode imdb lookup episode_id=%d imdb=%s", episodeID, imdbID)
			}
			return imdbID, nil
		}
	}
	return "", nil
}

func (c *Client) GetEpisodeTranslation(ctx context.Context, episodeID int, language string) (EpisodeTranslation, error) {
	if episodeID == 0 {
		return EpisodeTranslation{}, errNotFound
	}
	lang := normalizeLanguageParam(language)
	if lang == "" {
		return EpisodeTranslation{}, nil
	}

	path := fmt.Sprintf("/episodes/%d/translations/%s", episodeID, urlEscape(lang))
	var resp episodeTranslationResponse
	if err := c.getJSON(ctx, path, nil, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return EpisodeTranslation{}, nil
		}
		return EpisodeTranslation{}, err
	}
	return EpisodeTranslation{
		Name:     strings.TrimSpace(resp.Data.Name),
		Overview: strings.TrimSpace(resp.Data.Overview),
	}, nil
}

func GetSpecificEpisodeData(data EpisodesData, query EpisodeQuery) (EpisodeMatch, bool) {
	match, ok := findEpisodeMatch(data.Episodes, query)
	return match, ok
}

func (c *Client) searchSeries(ctx context.Context, filename, year string) ([]SeriesSearchResult, error) {
	params := map[string]string{
		"query": filename,
		"type":  "series",
	}
	params = c.languageParams(params)
	if strings.TrimSpace(year) != "" {
		params["year"] = strings.TrimSpace(year)
	}

	var resp searchSeriesResponse
	if err := c.getJSON(ctx, "/search", params, &resp); err != nil {
		return nil, err
	}
	results := make([]SeriesSearchResult, 0, len(resp.Data))
	for _, item := range resp.Data {
		results = append(results, SeriesSearchResult{
			TVDBID:  item.TVDBID,
			Name:    item.Name,
			Year:    item.Year,
			Aliases: mapAliases(item.Aliases),
		})
	}
	return results, nil
}

func selectBestSeries(results []SeriesSearchResult, year string) int {
	searchYear := strings.TrimSpace(year)
	if searchYear != "" {
		for _, result := range results {
			if result.Year == searchYear {
				return result.TVDBID
			}
		}
		for _, result := range results {
			for _, alias := range result.Aliases {
				if alias.Language == "eng" && aliasYearMatches(alias.Name, searchYear) {
					return result.TVDBID
				}
			}
		}
	}
	return results[0].TVDBID
}

func (c *Client) fetchEpisodes(ctx context.Context, seriesID int, language string) ([]Episode, string, error) {
	all := make([]Episode, 0)
	var seriesSlug string
	page := 0
	for page < 20 {
		path := fmt.Sprintf("/series/%d/episodes/default", seriesID)
		params := c.languageParamsFor(map[string]string{"page": strconv.Itoa(page)}, language)
		var resp episodesResponse
		if err := c.getJSON(ctx, path, params, &resp); err != nil {
			return nil, "", err
		}
		if seriesSlug == "" && strings.TrimSpace(resp.Slug) != "" {
			seriesSlug = strings.TrimSpace(resp.Slug)
		}
		if seriesSlug == "" && strings.TrimSpace(resp.Data.Slug) != "" {
			seriesSlug = strings.TrimSpace(resp.Data.Slug)
		}
		if len(resp.Data.Episodes) == 0 {
			break
		}
		for _, item := range resp.Data.Episodes {
			all = append(all, episodeFromResponse(item))
		}
		if len(resp.Data.Episodes) < 500 {
			break
		}
		page++
	}
	return all, seriesSlug, nil
}

type seriesDetails struct {
	aliases              []Alias
	seriesTitle          string
	seriesYear           int
	seriesYearSource     string
	seriesYearConfidence string
	airsDays             []string
	airsTime             string
	airsTimezone         string
	airsTimezoneSource   string
}

func (c *Client) fetchSeriesDetails(ctx context.Context, seriesID int, language string) (seriesDetails, error) {
	path := fmt.Sprintf("/series/%d/extended", seriesID)
	var resp seriesExtendedResponse
	if err := c.getJSON(ctx, path, c.languageParamsFor(nil, language), &resp); err != nil {
		return seriesDetails{}, err
	}
	airsDays, airsTime, airsTimezone, airsTimezoneSource := extractTVDBAirsSchedule(resp.Data)
	translation, translationErr := c.fetchSeriesTranslation(ctx, seriesID, "eng")
	if translationErr != nil && c.logger != nil {
		c.logger.Debugf("tvdb: series english translation lookup failed series_id=%d: %v", seriesID, translationErr)
	}
	seriesMeta := seriesTranslationMetadata(resp.Data, translation)
	return seriesDetails{
		aliases:              mapAliases(resp.Data.Aliases),
		seriesTitle:          seriesMeta.title,
		seriesYear:           seriesMeta.year,
		seriesYearSource:     seriesMeta.yearSource,
		seriesYearConfidence: seriesMeta.yearConfidence,
		airsDays:             airsDays,
		airsTime:             airsTime,
		airsTimezone:         airsTimezone,
		airsTimezoneSource:   airsTimezoneSource,
	}, nil
}

func (c *Client) languageParams(params map[string]string) map[string]string {
	return c.languageParamsFor(params, "eng")
}

func (c *Client) languageParamsFor(params map[string]string, language string) map[string]string {
	if params == nil {
		params = make(map[string]string, 1)
	}
	normalized := normalizeLanguageParam(language)
	if normalized == "" {
		return params
	}
	params["lang"] = normalized
	return params
}

func (c *Client) searchRemoteID(ctx context.Context, remoteID string, tvMovie bool) (int, string, bool, error) {
	path := "/search/remoteid/" + urlEscape(remoteID)
	var resp remoteIDResponse
	if err := c.getJSON(ctx, path, nil, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return 0, "", false, nil
		}
		return 0, "", false, err
	}
	for _, item := range resp.Data {
		if item.Series.ID != 0 {
			return item.Series.ID, item.Series.Name, true, nil
		}
	}
	if tvMovie {
		for _, item := range resp.Data {
			if item.Episode.SeriesID != 0 {
				return item.Episode.SeriesID, item.Episode.SeriesName, true, nil
			}
		}
		for _, item := range resp.Data {
			if item.Movie.ID != 0 {
				return item.Movie.ID, item.Movie.Name, true, nil
			}
		}
	}
	return 0, "", false, nil
}

func mapAliases(values []aliasResponse) []Alias {
	aliases := make([]Alias, 0, len(values))
	for _, value := range values {
		aliases = append(aliases, Alias(value))
	}
	return aliases
}

func extractTVDBAirsSchedule(data seriesExtendedDataResponse) ([]string, string, string, string) {
	type candidateMap struct {
		days        json.RawMessage
		dayOfWeek   json.RawMessage
		day         json.RawMessage
		on          json.RawMessage
		time        string
		timeAlt     string
		timeUTC     string
		airTime     string
		timezone    string
		timezoneAlt string
		timeZone    string
		zone        string
	}

	candidates := make([]candidateMap, 0, 4)
	candidates = append(candidates, candidateMap{
		days:        data.AirsDays,
		dayOfWeek:   data.AirsDayOfWeek,
		day:         data.AirsDay,
		on:          data.AirsOn,
		time:        strings.TrimSpace(data.AirsTime),
		timeAlt:     strings.TrimSpace(data.AirsTimeAlt),
		timeUTC:     strings.TrimSpace(data.AirsTimeUTC),
		airTime:     strings.TrimSpace(data.AirTime),
		timezone:    strings.TrimSpace(data.AirsTimeZone),
		timezoneAlt: strings.TrimSpace(data.AirsTimezone),
		timeZone:    strings.TrimSpace(data.TimeZone),
		zone:        strings.TrimSpace(data.Timezone),
	})

	for _, nested := range []scheduleResponse{data.Schedule, data.Airs, data.Broadcast} {
		candidates = append(candidates, candidateMap{
			days:        nested.AirsDays,
			dayOfWeek:   nested.AirsDayOfWeek,
			day:         nested.AirsDay,
			on:          nested.AirsOn,
			time:        strings.TrimSpace(nested.AirsTime),
			timeAlt:     strings.TrimSpace(nested.AirsTimeAlt),
			timeUTC:     strings.TrimSpace(nested.AirsTimeUTC),
			airTime:     strings.TrimSpace(nested.AirTime),
			timezone:    strings.TrimSpace(nested.AirsTimeZone),
			timezoneAlt: strings.TrimSpace(nested.AirsTimezone),
			timeZone:    strings.TrimSpace(nested.TimeZone),
			zone:        strings.TrimSpace(nested.Timezone),
		})
	}

	airsDays := []string{}
	airsTime := ""
	airsTimezone := ""
	airsTimezoneSource := ""

	for _, candidate := range candidates {
		if len(airsDays) == 0 {
			for _, raw := range []json.RawMessage{candidate.days, candidate.dayOfWeek, candidate.day, candidate.on} {
				airsDays = coerceDayList(raw)
				if len(airsDays) > 0 {
					break
				}
			}
		}

		if airsTime == "" {
			airsTime = metautil.FirstNonEmptyTrimmed(candidate.time, candidate.timeAlt, candidate.timeUTC, candidate.airTime)
		}

		if airsTimezone == "" {
			if value := metautil.FirstNonEmptyTrimmed(candidate.timezone, candidate.timezoneAlt, candidate.timeZone, candidate.zone); value != "" {
				airsTimezone = value
				airsTimezoneSource = "field"
			}
		}
	}

	if airsTimezone == "" {
		if inferred, source := inferTimezoneFromSeries(data); inferred != "" {
			airsTimezone = inferred
			airsTimezoneSource = source
		}
	}

	return airsDays, airsTime, airsTimezone, airsTimezoneSource
}

func coerceDayList(raw json.RawMessage) []string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	if trimmed[0] == '{' {
		var dayMap map[string]bool
		if err := json.Unmarshal(trimmed, &dayMap); err == nil {
			ordered := []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
			out := make([]string, 0, len(ordered))
			for _, day := range ordered {
				if dayMap[day] {
					out = append(out, titleCaseWord(day))
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}

	if trimmed[0] == '[' {
		var list []string
		if err := json.Unmarshal(trimmed, &list); err == nil {
			out := make([]string, 0, len(list))
			for _, value := range list {
				if cleaned := strings.TrimSpace(value); cleaned != "" {
					out = append(out, cleaned)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if cleaned := strings.TrimSpace(part); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return out
}

func inferTimezoneFromSeries(data seriesExtendedDataResponse) (string, string) {
	cities := make([]string, 0, 10)
	cities = append(cities, data.City, data.PrimaryCity, data.AirsCity, data.BroadcastCity)
	locations := []string{data.Country, data.NetworkCountry, data.AirsCountry, data.BroadcastCountry, data.OriginalCountry}

	for _, nested := range []scheduleResponse{data.Schedule, data.Airs, data.Broadcast} {
		cities = append(cities, nested.City, nested.LocationCity)
		locations = append(locations, nested.Country, nested.LocationCountry, nested.Region)
	}

	for _, tag := range data.Tags {
		name := normalizeLocationKey(tag.TagName)
		if !strings.Contains(name, "geographic location") {
			continue
		}
		locations = append(locations, tag.Name)
	}

	for _, city := range cities {
		norm := normalizeLocationKey(city)
		if norm == "" {
			continue
		}
		if tz, ok := cityTimezoneMap[norm]; ok {
			return tz, "city:" + strings.TrimSpace(city)
		}
	}

	for _, location := range locations {
		norm := normalizeLocationKey(location)
		if norm == "" {
			continue
		}
		if tz, ok := locationTimezoneMap[norm]; ok {
			return tz, "location:" + strings.TrimSpace(location)
		}
	}

	return "", ""
}

func normalizeLocationKey(value string) string {
	cleaned := strings.ToLower(strings.TrimSpace(value))
	if cleaned == "" {
		return ""
	}
	cleaned = nonAlphaNumSpacePattern.ReplaceAllString(cleaned, " ")
	cleaned = multiSpacePattern.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

func titleCaseWord(value string) string {
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	return strings.ToUpper(lower[:1]) + lower[1:]
}

func deriveEnglishSeriesName(data seriesExtendedDataResponse, requestLanguage string) string {
	if isEnglishCode(requestLanguage) && strings.TrimSpace(data.Name) != "" {
		return strings.TrimSpace(data.Name)
	}
	if isEnglishCode(data.OriginalLanguage) {
		return strings.TrimSpace(data.Name)
	}
	if !containsEnglishTranslation(data.NameTranslations) {
		return ""
	}
	return selectEnglishAlias(data.Aliases)
}

func deriveEnglishSeriesOverview(data seriesExtendedDataResponse, requestLanguage string) string {
	if isEnglishCode(requestLanguage) && strings.TrimSpace(data.Overview) != "" {
		return strings.TrimSpace(data.Overview)
	}
	if isEnglishCode(data.OriginalLanguage) {
		return strings.TrimSpace(data.Overview)
	}
	return ""
}

type seriesMetadataFields struct {
	title          string
	year           int
	yearSource     string
	yearConfidence string
}

// seriesTranslationMetadata selects English title metadata and returns only
// naming-eligible years proven by explicit translation or alias evidence.
func seriesTranslationMetadata(data seriesExtendedDataResponse, translation seriesTranslationDataResponse) seriesMetadataFields {
	translationAliases := trimStringList(translation.Aliases)
	extendedEnglishAliases := englishAliasNames(data.Aliases)

	fallbackTitle := ""
	fallbackYearSource := ""
	if len(translationAliases) > 0 {
		fallbackTitle = translationAliases[len(translationAliases)-1]
		fallbackYearSource = seriesYearSourceTranslationAlias
	} else if len(extendedEnglishAliases) > 0 {
		fallbackTitle = extendedEnglishAliases[len(extendedEnglishAliases)-1]
		fallbackYearSource = seriesYearSourceExtendedAlias
	}

	translationName := strings.TrimSpace(translation.Name)
	title := metautil.FirstNonEmptyTrimmed(translationName, fallbackTitle)
	if year, ok := explicitTitleYear(translationName); ok {
		return seriesMetadataFields{
			title:          title,
			year:           year,
			yearSource:     seriesYearSourceTranslationName,
			yearConfidence: seriesYearConfidenceHigh,
		}
	}
	if translationName == "" {
		if year, ok := aliasYear(fallbackTitle); ok {
			return seriesMetadataFields{
				title:          title,
				year:           year,
				yearSource:     fallbackYearSource,
				yearConfidence: seriesYearConfidenceHigh,
			}
		}
		if year, ok := matchingAliasYear(extendedEnglishAliases, title); ok {
			return seriesMetadataFields{
				title:          title,
				year:           year,
				yearSource:     seriesYearSourceExtendedAlias,
				yearConfidence: seriesYearConfidenceHigh,
			}
		}
	} else {
		if year, ok := matchingAliasYear(translationAliases, title); ok {
			return seriesMetadataFields{
				title:          title,
				year:           year,
				yearSource:     seriesYearSourceTranslationAlias,
				yearConfidence: seriesYearConfidenceHigh,
			}
		}
		if year, ok := matchingAliasYear(extendedEnglishAliases, title); ok {
			return seriesMetadataFields{
				title:          title,
				year:           year,
				yearSource:     seriesYearSourceExtendedAlias,
				yearConfidence: seriesYearConfidenceHigh,
			}
		}
	}
	if year, ok := slugFallbackYear(data.Slug, data.Name, translation.Name, translationAliases, extendedEnglishAliases); ok {
		return seriesMetadataFields{
			title:          title,
			year:           year,
			yearSource:     seriesYearSourceSlug,
			yearConfidence: seriesYearConfidenceLow,
		}
	}
	return seriesMetadataFields{title: title}
}

// aliasYear returns the naming year carried by an alias. Parenthesized disambiguation years win over
// earlier title years, while plain standalone years remain a fallback for aliases without parentheses.
func aliasYear(alias string) (int, bool) {
	if matches := yearAliasPattern.FindAllStringSubmatch(alias, -1); len(matches) > 0 {
		year, err := strconv.Atoi(matches[len(matches)-1][1])
		if err != nil {
			return 0, false
		}
		return year, true
	}
	yearText := extractYearFromText(alias)
	if yearText == "" {
		return 0, false
	}
	year, err := strconv.Atoi(yearText)
	if err != nil {
		return 0, false
	}
	return year, true
}

// explicitTitleYear returns a naming-safe title year only when the title carries an explicit parenthesized year.
func explicitTitleYear(title string) (int, bool) {
	matches := yearAliasPattern.FindStringSubmatch(title)
	if len(matches) != 2 {
		return 0, false
	}
	year, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false
	}
	return year, true
}

func matchingAliasYear(aliases []string, title string) (int, bool) {
	for _, alias := range aliases {
		if normalizedTitleWithoutYear(title) != "" && normalizedTitleWithoutYear(alias) != normalizedTitleWithoutYear(title) {
			continue
		}
		if year, ok := aliasYear(alias); ok {
			return year, true
		}
	}
	return 0, false
}

func slugFallbackYear(slug, originalTitle, translationName string, translationAliases, extendedEnglishAliases []string) (int, bool) {
	slugYear := extractYearFromText(slug)
	if slugYear == "" {
		return 0, false
	}
	if cleanEnglishTitleExists(translationName, translationAliases, extendedEnglishAliases) {
		return 0, false
	}
	slugBase := normalizedTitleWithoutYear(strings.ReplaceAll(slug, "-", " "))
	if slugBase == "" {
		return 0, false
	}
	originalBase := normalizedTitleWithoutYear(originalTitle)
	if comparableTitleBase(originalBase) && originalBase != slugBase {
		return 0, false
	}
	year, err := strconv.Atoi(slugYear)
	if err != nil {
		return 0, false
	}
	return year, true
}

func comparableTitleBase(value string) bool {
	return len(strings.ReplaceAll(value, " ", "")) >= 3
}

func cleanEnglishTitleExists(translationName string, translationAliases, extendedEnglishAliases []string) bool {
	for _, value := range append(append([]string{translationName}, translationAliases...), extendedEnglishAliases...) {
		if strings.TrimSpace(value) != "" && extractYearFromText(value) == "" {
			return true
		}
	}
	return false
}

func normalizedTitleWithoutYear(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	withoutYear := yearAliasPattern.ReplaceAllString(trimmed, " ")
	withoutYear = yearPattern.ReplaceAllString(withoutYear, " ")
	withoutYear = strings.ReplaceAll(withoutYear, "×", "x")
	withoutYear = strings.ReplaceAll(withoutYear, "&", " and ")
	withoutYear = strings.ToLower(withoutYear)
	withoutYear = nonAlphaNumSpacePattern.ReplaceAllString(withoutYear, " ")
	withoutYear = multiSpacePattern.ReplaceAllString(withoutYear, " ")
	return strings.TrimSpace(withoutYear)
}

func englishAliasNames(values []aliasResponse) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !isEnglishCode(value.Language) {
			continue
		}
		name := strings.TrimSpace(value.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func trimStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (c *Client) fetchSeriesTranslation(ctx context.Context, seriesID int, language string) (seriesTranslationDataResponse, error) {
	if seriesID == 0 {
		return seriesTranslationDataResponse{}, errNotFound
	}
	lang := normalizeLanguageParam(language)
	if lang == "" {
		return seriesTranslationDataResponse{}, errNotFound
	}

	path := fmt.Sprintf("/series/%d/translations/%s", seriesID, urlEscape(lang))
	var resp seriesTranslationResponse
	if err := c.getJSON(ctx, path, nil, &resp); err != nil {
		if errors.Is(err, errNotFound) {
			return seriesTranslationDataResponse{}, nil
		}
		return seriesTranslationDataResponse{}, err
	}
	return resp.Data, nil
}

func containsEnglishTranslation(values []string) bool {
	return slices.ContainsFunc(values, isEnglishCode)
}

func selectEnglishAlias(values []aliasResponse) string {
	best := ""
	for _, value := range values {
		if !isEnglishCode(value.Language) {
			continue
		}
		alias := strings.TrimSpace(value.Name)
		if alias == "" {
			continue
		}
		if best == "" {
			best = alias
		}
		if !strings.Contains(alias, "[") && !strings.Contains(alias, "]") {
			return alias
		}
	}
	return best
}

func isEnglishCode(value string) bool {
	normalized := normalizeLanguageParam(value)
	return normalized == "eng"
}

func mapGenreNames(values []genreResponse) []string {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func extractNetworkName(data seriesExtendedDataResponse) string {
	if name := strings.TrimSpace(data.LatestNetwork.Name); name != "" {
		return name
	}
	for _, company := range data.Companies {
		if strings.TrimSpace(company.Name) == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(company.CompanyType.Name), "network") {
			return strings.TrimSpace(company.Name)
		}
		if company.CompanyType.CompanyTypeID == 1 {
			return strings.TrimSpace(company.Name)
		}
	}
	return ""
}

func extractPosterURL(data seriesExtendedDataResponse) string {
	if image := strings.TrimSpace(data.Image); image != "" {
		return image
	}
	for _, artwork := range data.Artworks {
		image := strings.TrimSpace(artwork.Image)
		if image == "" {
			continue
		}
		if artwork.Type == 2 || artwork.Type == 7 {
			return image
		}
	}
	for _, artwork := range data.Artworks {
		if image := strings.TrimSpace(artwork.Image); image != "" {
			return image
		}
	}
	return ""
}

func episodeFromResponse(item episodeResponse) Episode {
	return Episode{
		ID:             item.ID,
		SeasonNumber:   item.SeasonNumber,
		Number:         item.Number,
		AbsoluteNumber: item.AbsoluteNumber,
		SeasonName:     item.SeasonName,
		Name:           item.Name,
		Overview:       item.Overview,
		Year:           int(item.Year),
		Aired:          item.Aired,
	}
}

func applySeriesDetails(data EpisodesData, details seriesDetails) EpisodesData {
	if len(data.Aliases) == 0 {
		data.Aliases = details.aliases
	}
	if strings.TrimSpace(data.SeriesTitle) == "" {
		data.SeriesTitle = details.seriesTitle
	}
	if data.SeriesYear == 0 || strings.TrimSpace(data.SeriesYearSource) == "" {
		data.SeriesYear = details.seriesYear
	}
	if strings.TrimSpace(data.SeriesYearSource) == "" {
		data.SeriesYearSource = details.seriesYearSource
	}
	if strings.TrimSpace(data.SeriesYearConfidence) == "" {
		data.SeriesYearConfidence = details.seriesYearConfidence
	}
	if len(data.AirsDays) == 0 {
		data.AirsDays = details.airsDays
	}
	if strings.TrimSpace(data.AirsTime) == "" {
		data.AirsTime = details.airsTime
	}
	if strings.TrimSpace(data.AirsTimezone) == "" {
		data.AirsTimezone = details.airsTimezone
	}
	if strings.TrimSpace(data.AirsTimezoneSource) == "" {
		data.AirsTimezoneSource = details.airsTimezoneSource
	}
	return data
}

// applyLegacySeriesYearProvenance fills missing naming provenance for old episode caches only when the
// cached title or English alias proves the cached year. It leaves source-less API years non-eligible.
func applyLegacySeriesYearProvenance(data EpisodesData) (EpisodesData, bool) {
	if data.SeriesYear <= 0 || strings.TrimSpace(data.SeriesYearSource) != "" {
		return data, false
	}
	if year, ok := explicitTitleYear(data.SeriesTitle); ok && year == data.SeriesYear {
		data.SeriesYearSource = seriesYearSourceTranslationName
		data.SeriesYearConfidence = seriesYearConfidenceHigh
		return data, true
	}
	if year, ok := matchingAliasYear(episodeAliasNames(data.Aliases), data.SeriesTitle); ok && year == data.SeriesYear {
		data.SeriesYearSource = seriesYearSourceExtendedAlias
		data.SeriesYearConfidence = seriesYearConfidenceHigh
		return data, true
	}
	return data, false
}

func specificSeriesAlias(data EpisodesData) string {
	title := strings.TrimSpace(data.SeriesTitle)
	if title != "" {
		if data.SeriesYear > 0 && seriesYearSourceNamingEligible(data.SeriesYearSource) {
			if extractYearFromText(title) == strconv.Itoa(data.SeriesYear) {
				return title
			}
			return fmt.Sprintf("%s (%d)", title, data.SeriesYear)
		}
		return title
	}
	return explicitYearAlias(data.Aliases)
}

func seriesYearSourceNamingEligible(source string) bool {
	switch strings.TrimSpace(source) {
	case seriesYearSourceTranslationName, seriesYearSourceTranslationAlias, seriesYearSourceExtendedAlias, seriesYearSourceSlug:
		return true
	default:
		return false
	}
}

func explicitYearAlias(aliases []Alias) string {
	matches := make([]string, 0)
	for _, alias := range aliases {
		name := strings.TrimSpace(alias.Name)
		if !isEnglishCode(alias.Language) || name == "" {
			continue
		}
		if yearAliasPattern.MatchString(name) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func aliasYearMatches(name, year string) bool {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(year) == "" {
		return false
	}
	parenthesized := yearAliasPattern.FindStringSubmatch(name)
	if len(parenthesized) == 2 {
		return parenthesized[1] == year
	}
	plain := extractYearFromText(name)
	return plain == year
}

func extractYearFromText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	indexes := yearPattern.FindAllStringIndex(trimmed, -1)
	for _, pair := range indexes {
		start := pair[0]
		end := pair[1]
		if start > 0 && isASCIIDigit(trimmed[start-1]) {
			continue
		}
		if end < len(trimmed) && isASCIIDigit(trimmed[end]) {
			continue
		}
		return trimmed[start:end]
	}
	return ""
}

// episodeAliasNames returns non-empty English episode-cache aliases for provenance checks.
func episodeAliasNames(values []Alias) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !isEnglishCode(value.Language) {
			continue
		}
		name := strings.TrimSpace(value.Name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func isASCIIDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func episodeIsPresent(episodes []Episode, query EpisodeQuery) bool {
	if len(episodes) == 0 {
		return false
	}
	if query.Season == 0 && query.Episode == 0 && query.Absolute == 0 && strings.TrimSpace(query.AiredDate) == "" {
		return true
	}
	aired := strings.TrimSpace(query.AiredDate)
	if aired != "" {
		for _, ep := range episodes {
			if ep.Aired == aired {
				return true
			}
		}
	}

	if query.Episode == 0 && query.Absolute == 0 && aired == "" {
		return true
	}

	for _, ep := range episodes {
		if query.Absolute != 0 && ep.AbsoluteNumber == query.Absolute {
			return true
		}
		if query.Season != 0 && query.Episode != 0 && ep.SeasonNumber == query.Season && ep.Number == query.Episode {
			return true
		}
	}
	return false
}

func findEpisodeMatch(episodes []Episode, query EpisodeQuery) (EpisodeMatch, bool) {
	if len(episodes) == 0 {
		return EpisodeMatch{}, false
	}
	aired := strings.TrimSpace(query.AiredDate)
	if aired != "" {
		for _, ep := range episodes {
			if ep.Aired == aired {
				return toMatch(ep), true
			}
		}
	}
	if query.Season == 0 {
		return EpisodeMatch{}, false
	}
	if query.Episode == 0 {
		for _, ep := range episodes {
			if ep.SeasonNumber == query.Season {
				return toMatch(ep), true
			}
		}
	}
	for _, ep := range episodes {
		if ep.SeasonNumber == query.Season && ep.Number == query.Episode {
			return toMatch(ep), true
		}
	}
	if query.Absolute != 0 {
		for _, ep := range episodes {
			if ep.AbsoluteNumber == query.Absolute {
				return toMatch(ep), true
			}
		}
	}
	if query.Episode != 0 {
		for _, ep := range episodes {
			if ep.AbsoluteNumber == query.Episode {
				return toMatch(ep), true
			}
		}
	}
	return EpisodeMatch{}, false
}

func toMatch(ep Episode) EpisodeMatch {
	return EpisodeMatch{
		SeasonName:    ep.SeasonName,
		EpisodeName:   ep.Name,
		Overview:      ep.Overview,
		SeasonNumber:  ep.SeasonNumber,
		EpisodeNumber: ep.Number,
		Year:          ep.Year,
		EpisodeID:     ep.ID,
		Aired:         ep.Aired,
	}
}

func (c *Client) getJSON(ctx context.Context, path string, params map[string]string, target any) error {
	if strings.TrimSpace(c.apiKey) == "" {
		return errors.New("tvdb: api key missing")
	}
	if err := c.ensureToken(ctx); err != nil {
		return err
	}

	url := c.baseURL + path
	if len(params) > 0 {
		q := make([]string, 0, len(params))
		for key, value := range params {
			q = append(q, fmt.Sprintf("%s=%s", key, urlEscape(value)))
		}
		url = url + "?" + strings.Join(q, "&")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("tvdb: build request for %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token())
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tvdb: execute request for %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.refreshToken(ctx); err != nil {
			return err
		}
		return c.getJSON(ctx, path, params, target)
	}
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}

	body, err := readTVDBResponseBody(resp.Body)
	if err != nil {
		return fmt.Errorf("tvdb: read response body for %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tvdb: http %d: %s", resp.StatusCode, strings.TrimSpace(redaction.RedactValue(string(body), nil)))
	}

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("tvdb: decode %s: %w", path, err)
	}
	return nil
}

// readTVDBResponseBody returns a complete TVDB response payload up to
// maxTVDBResponseBodyBytes and reports oversized responses instead of
// truncating JSON or upstream error details.
func readTVDBResponseBody(body io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(body, maxTVDBResponseBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read limited response body: %w", err)
	}
	if len(payload) > maxTVDBResponseBodyBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxTVDBResponseBodyBytes)
	}
	return payload, nil
}

func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.authToken != "" {
		return nil
	}
	return c.loginLocked(ctx)
}

func (c *Client) refreshToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authToken = ""
	return c.loginLocked(ctx)
}

func (c *Client) token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authToken
}

func (c *Client) loginLocked(ctx context.Context) error {
	payload := map[string]string{"apikey": c.apiKey}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("tvdb: marshal login payload: %w", err)
	}

	url := c.baseURL + "/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("tvdb: build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tvdb: execute login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxTVDBResponseBodyBytes+1))
		return fmt.Errorf("tvdb: login http %d: %s", resp.StatusCode, strings.TrimSpace(redaction.RedactValue(string(body), nil)))
	}

	var loginResp loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("tvdb: decode login response: %w", err)
	}
	if strings.TrimSpace(loginResp.Data.Token) == "" {
		return errors.New("tvdb: login token missing")
	}
	c.authToken = strings.TrimSpace(loginResp.Data.Token)
	return nil
}

func (c *Client) cachePath(baseDir string, seriesID int, language string) string {
	base := strings.TrimSpace(baseDir)
	if base == "" {
		base = c.cacheDir
	}
	if base == "" {
		return ""
	}
	lang := normalizeLanguageParam(language)
	if lang == "" {
		lang = "orig"
	}
	return filepath.Join(base, fmt.Sprintf("%d-%s.json", seriesID, lang))
}

func readEpisodesCache(path string) (EpisodesData, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return EpisodesData{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return EpisodesData{}, false
	}
	var cached EpisodesData
	if err := json.Unmarshal(data, &cached); err != nil {
		return EpisodesData{}, false
	}
	return cached, true
}

func writeEpisodesCache(path string, data EpisodesData) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("tvdb: create episodes cache dir: %w", err)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("tvdb: marshal episodes cache: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("tvdb: write episodes cache: %w", err)
	}
	return nil
}

func normalizeIMDbRemote(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "0" {
		return ""
	}
	if strings.HasPrefix(trimmed, "tt") {
		return trimmed
	}
	id, err := strconv.Atoi(trimmed)
	if err != nil {
		return trimmed
	}
	return fmt.Sprintf("tt%07d", id)
}

func normalizeLanguageParam(language string) string {
	trimmed := strings.TrimSpace(strings.ToLower(language))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	switch trimmed {
	case "", "orig", "original", "default":
		return ""
	case "en", "eng", "english":
		return "eng"
	}
	if strings.HasPrefix(trimmed, "en-") {
		return "eng"
	}
	return trimmed
}

func urlEscape(value string) string {
	return url.QueryEscape(value)
}

func embeddedKey() string {
	encoded := []byte(
		"MDEwMTEwMDEwMDExMDAxMDAxMDExMDAxMDExMTEwMDAwMTAwMTExMDAxMTAxMTAxMDEwMTAwMDEwMDExMDEwMD" +
			"AxMDAxMTAxMDExMDEwMTAwMTAxMDAwMTAxMTEwMTAwMDEwMTEwMDEwMDExMDAxMDAxMDAxMDAxMDAxMTAwMTEw" +
			"MTAwMTEwMTAxMDEwMDExMDAxMTAwMDAwMDExMDAwMDAxMDAxMTEwMDExMDEwMTAwMTEwMTAwMDAxMTAxMTAwMD" +
			"EwMDExMDAwMTAxMDExMTAxMDAwMTAxMDAxMTAwMTAwMTAxMTAwMTAxMDEwMTAwMDEwMDAxMDEwMTExMDEwMDAx" +
			"MDAxMTEwMDExMTEwMTAwMTAwMDAxMDAxMTAxMDEwMDEwMTEwMTAwMTAwMDExMTAxMDEwMDAxMDExMTEwMDAwMT" +
			"AxMTAwMTAxMDEwMTExMDEwMTAwMTAwMTEwMTAxMDAxMDAxMTEwMDEwMTAxMTEwMTAxMTAxMDAxMTAxMDEw",
	)
	binaryBytes, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return ""
	}
	decoded := make([]byte, 0, len(binaryBytes)/8)
	for i := 0; i+8 <= len(binaryBytes); i += 8 {
		chunk := binaryBytes[i : i+8]
		value, err := strconv.ParseUint(string(chunk), 2, 8)
		if err != nil {
			return ""
		}
		decoded = append(decoded, byte(value))
	}
	finalBytes, err := base64.StdEncoding.DecodeString(string(decoded))
	if err != nil {
		return ""
	}
	return string(finalBytes)
}

type loginResponse struct {
	Data struct {
		Token string `json:"token"`
	} `json:"data"`
}

type searchSeriesResponse struct {
	Data []seriesResult `json:"data"`
}

type seriesResult struct {
	TVDBID  int             `json:"tvdb_id"`
	Name    string          `json:"name"`
	Year    string          `json:"year"`
	Aliases []aliasResponse `json:"aliases"`
}

type aliasResponse struct {
	Name     string `json:"name"`
	Language string `json:"language"`
}

// UnmarshalJSON accepts both object aliases and the string aliases returned by TVDB search.
func (a *aliasResponse) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*a = aliasResponse{}
		return nil
	}
	if trimmed[0] == '"' {
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return fmt.Errorf("tvdb: unmarshal alias string: %w", err)
		}
		*a = aliasResponse{Name: strings.TrimSpace(name), Language: "eng"}
		return nil
	}
	var payload struct {
		Name     string `json:"name"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return fmt.Errorf("tvdb: unmarshal alias object: %w", err)
	}
	*a = aliasResponse{Name: strings.TrimSpace(payload.Name), Language: strings.TrimSpace(payload.Language)}
	return nil
}

type episodesResponse struct {
	Data episodesDataResponse `json:"data"`
	Slug string               `json:"slug"`
}

type episodesDataResponse struct {
	Episodes []episodeResponse
	Slug     string
}

func (e *episodesDataResponse) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		e.Episodes = nil
		e.Slug = ""
		return nil
	}

	switch trimmed[0] {
	case '[':
		var episodes []episodeResponse
		if err := json.Unmarshal(trimmed, &episodes); err != nil {
			return fmt.Errorf("tvdb: unmarshal episodes list: %w", err)
		}
		e.Episodes = episodes
		e.Slug = ""
		return nil
	case '{':
		var payload struct {
			Episodes []episodeResponse `json:"episodes"`
			Slug     string            `json:"slug"`
		}
		if err := json.Unmarshal(trimmed, &payload); err != nil {
			return fmt.Errorf("tvdb: unmarshal episodes payload: %w", err)
		}
		e.Episodes = payload.Episodes
		e.Slug = payload.Slug
		return nil
	default:
		return fmt.Errorf("tvdb: episodes data has unsupported JSON type %q", string(trimmed[0]))
	}
}

type episodeResponse struct {
	ID             int         `json:"id"`
	SeasonNumber   int         `json:"seasonNumber"`
	Number         int         `json:"number"`
	AbsoluteNumber int         `json:"absoluteNumber"`
	SeasonName     string      `json:"seasonName"`
	Name           string      `json:"name"`
	Overview       string      `json:"overview"`
	Year           intOrString `json:"year"`
	Aired          string      `json:"aired"`
}

type intOrString int

func (v *intOrString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*v = 0
		return nil
	}

	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return fmt.Errorf("tvdb: unmarshal integer string: %w", err)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			*v = 0
			return nil
		}
		parsed, err := strconv.Atoi(text)
		if err != nil {
			return fmt.Errorf("tvdb: invalid integer string %q", text)
		}
		*v = intOrString(parsed)
		return nil
	}

	var numeric int
	if err := json.Unmarshal(trimmed, &numeric); err != nil {
		return fmt.Errorf("tvdb: unmarshal integer: %w", err)
	}
	*v = intOrString(numeric)
	return nil
}

type seriesExtendedResponse struct {
	Data seriesExtendedDataResponse `json:"data"`
}

type seriesTranslationResponse struct {
	Data seriesTranslationDataResponse `json:"data"`
}

type seriesTranslationDataResponse struct {
	Name     string   `json:"name"`
	Overview string   `json:"overview"`
	Aliases  []string `json:"aliases"`
}

type seriesExtendedDataResponse struct {
	ID                   int                     `json:"id"`
	Name                 string                  `json:"name"`
	Overview             string                  `json:"overview"`
	Slug                 string                  `json:"slug"`
	Year                 intOrString             `json:"year"`
	NameTranslations     []string                `json:"nameTranslations"`
	OverviewTranslations []string                `json:"overviewTranslations"`
	FirstAired           string                  `json:"firstAired"`
	OriginalLanguage     string                  `json:"originalLanguage"`
	OriginalCountry      string                  `json:"originalCountry"`
	Image                string                  `json:"image"`
	Aliases              []aliasResponse         `json:"aliases"`
	Genres               []genreResponse         `json:"genres"`
	Type                 namedResponse           `json:"type"`
	Status               namedResponse           `json:"status"`
	LatestNetwork        namedResponse           `json:"latestNetwork"`
	Artworks             []artworkResponse       `json:"artworks"`
	Companies            []seriesCompanyResponse `json:"companies"`
	AirsDays             json.RawMessage         `json:"airsDays"`
	AirsDayOfWeek        json.RawMessage         `json:"airsDayOfWeek"`
	AirsDay              json.RawMessage         `json:"airsDay"`
	AirsOn               json.RawMessage         `json:"airsOn"`
	AirsTime             string                  `json:"airsTime"`
	AirsTimeAlt          string                  `json:"airs_time"`
	AirsTimeUTC          string                  `json:"airsTimeUTC"`
	AirTime              string                  `json:"airTime"`
	AirsTimeZone         string                  `json:"airsTimeZone"`
	AirsTimezone         string                  `json:"airsTimezone"`
	TimeZone             string                  `json:"timeZone"`
	Timezone             string                  `json:"timezone"`
	City                 string                  `json:"city"`
	PrimaryCity          string                  `json:"primaryCity"`
	AirsCity             string                  `json:"airsCity"`
	BroadcastCity        string                  `json:"broadcastCity"`
	Country              string                  `json:"country"`
	NetworkCountry       string                  `json:"networkCountry"`
	AirsCountry          string                  `json:"airsCountry"`
	BroadcastCountry     string                  `json:"broadcastCountry"`
	Schedule             scheduleResponse        `json:"schedule"`
	Airs                 scheduleResponse        `json:"airs"`
	Broadcast            scheduleResponse        `json:"broadcast"`
	Tags                 []tagResponse           `json:"tags"`
}

type scheduleResponse struct {
	AirsDays        json.RawMessage `json:"airsDays"`
	AirsDayOfWeek   json.RawMessage `json:"airsDayOfWeek"`
	AirsDay         json.RawMessage `json:"airsDay"`
	AirsOn          json.RawMessage `json:"airsOn"`
	AirsTime        string          `json:"airsTime"`
	AirsTimeAlt     string          `json:"airs_time"`
	AirsTimeUTC     string          `json:"airsTimeUTC"`
	AirTime         string          `json:"airTime"`
	AirsTimeZone    string          `json:"airsTimeZone"`
	AirsTimezone    string          `json:"airsTimezone"`
	TimeZone        string          `json:"timeZone"`
	Timezone        string          `json:"timezone"`
	City            string          `json:"city"`
	LocationCity    string          `json:"locationCity"`
	Country         string          `json:"country"`
	LocationCountry string          `json:"locationCountry"`
	Region          string          `json:"region"`
}

type tagResponse struct {
	TagName string `json:"tagName"`
	Name    string `json:"name"`
}

type genreResponse struct {
	Name string `json:"name"`
}

type namedResponse struct {
	Name string `json:"name"`
}

type artworkResponse struct {
	Image string `json:"image"`
	Type  int    `json:"type"`
}

type companyTypeResponse struct {
	CompanyTypeID int    `json:"companyTypeId"`
	Name          string `json:"name"`
}

type seriesCompanyResponse struct {
	Name        string              `json:"name"`
	CompanyType companyTypeResponse `json:"companyType"`
}

type remoteIDResponse struct {
	Data []remoteIDItem `json:"data"`
}

type remoteIDItem struct {
	Series struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"series"`
	Episode struct {
		SeriesID   int    `json:"seriesId"`
		SeriesName string `json:"seriesName"`
	} `json:"episode"`
	Movie struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"movie"`
}

type episodeExtendedResponse struct {
	Data struct {
		RemoteIDs []remoteIDEntry `json:"remoteIds"`
	} `json:"data"`
}

type episodeTranslationResponse struct {
	Data episodeTranslationDataResponse `json:"data"`
}

type episodeTranslationDataResponse struct {
	Name     string `json:"name"`
	Overview string `json:"overview"`
}

type remoteIDEntry struct {
	ID         string `json:"id"`
	Type       int    `json:"type"`
	SourceName string `json:"sourceName"`
}
