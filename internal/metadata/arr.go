// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

type ArrLookupResult struct {
	Source       string
	TMDBID       int
	IMDBID       int
	TVDBID       int
	TVmazeID     int
	Year         int
	Genres       []string
	ReleaseGroup string
}

type arrInstance struct {
	name    string
	baseURL string
	apiKey  string
}

type httpArrLookupClient struct {
	service   string
	instances []arrInstance
	client    *http.Client
	logger    api.Logger
}

func (s *Service) collectArrIdentityEvidence(ctx context.Context, meta preparationstate.State) (preparationstate.State, error) {
	select {
	case <-ctx.Done():
		return preparationstate.State{}, fmt.Errorf("context canceled: %w", ctx.Err())
	default:
	}

	category := resolveCategoryPreference(meta)
	if category == "" {
		if isLikelyTV(meta) {
			category = "TV"
		} else {
			category = "MOVIE"
		}
	}

	var (
		client ArrLookupClient
		err    error
	)
	switch strings.ToUpper(strings.TrimSpace(category)) {
	case "TV":
		if !s.cfg.ArrIntegration.UseSonarr {
			return meta, nil
		}
		client, err = s.ensureSonarrClient()
	case "MOVIE":
		if !s.cfg.ArrIntegration.UseRadarr {
			return meta, nil
		}
		client, err = s.ensureRadarrClient()
	default:
		return meta, nil
	}
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: arr client unavailable category=%s: %v", category, err)
		}
		return meta, nil
	}

	result, err := client.Lookup(ctx, meta)
	if err != nil {
		if s.logger != nil {
			s.logger.Warnf("metadata: arr lookup failed category=%s path=%q: %v", category, meta.SourcePath, err)
		}
		return meta, nil
	}
	if result.TMDBID == 0 && result.IMDBID == 0 && result.TVDBID == 0 && result.TVmazeID == 0 && result.Year == 0 && len(result.Genres) == 0 &&
		result.ReleaseGroup == "" {
		return meta, nil
	}

	meta.ArrSource = strings.TrimSpace(result.Source)
	meta.ArrTMDBID = result.TMDBID
	meta.ArrIMDBID = result.IMDBID
	meta.ArrTVDBID = result.TVDBID
	meta.ArrTVmazeID = result.TVmazeID
	meta.ArrYear = result.Year
	meta.ArrGenres = append([]string{}, result.Genres...)
	meta.ArrReleaseGroup = strings.TrimSpace(result.ReleaseGroup)
	if meta.MetadataOverrides.Anime == nil && !meta.Anime && hasAnimeGenre(result.Genres) {
		meta.Anime = true
	}

	if s.logger != nil {
		s.logger.Infof(
			"metadata: arr resolved source=%s tmdb=%d imdb=%d tvdb=%d tvmaze=%d year=%d",
			meta.ArrSource,
			meta.ArrTMDBID,
			meta.ArrIMDBID,
			meta.ArrTVDBID,
			meta.ArrTVmazeID,
			meta.ArrYear,
		)
	}

	return meta, nil
}

func (s *Service) ensureSonarrClient() (ArrLookupClient, error) {
	if s.sonarr != nil {
		return s.sonarr, nil
	}
	instances := sonarrInstances(s.cfg.ArrIntegration)
	if len(instances) == 0 {
		return nil, errors.New("no sonarr instances configured")
	}
	s.sonarr = &httpArrLookupClient{
		service:   "sonarr",
		instances: instances,
		client:    http.DefaultClient,
		logger:    s.logger,
	}
	return s.sonarr, nil
}

func (s *Service) ensureRadarrClient() (ArrLookupClient, error) {
	if s.radarr != nil {
		return s.radarr, nil
	}
	instances := radarrInstances(s.cfg.ArrIntegration)
	if len(instances) == 0 {
		return nil, errors.New("no radarr instances configured")
	}
	s.radarr = &httpArrLookupClient{
		service:   "radarr",
		instances: instances,
		client:    http.DefaultClient,
		logger:    s.logger,
	}
	return s.radarr, nil
}

func (c *httpArrLookupClient) Lookup(ctx context.Context, meta preparationstate.State) (ArrLookupResult, error) {
	if c == nil {
		return ArrLookupResult{}, errors.New("arr client not configured")
	}
	httpClient := c.client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	var lastErr error
	for _, instance := range c.instances {
		result, err := c.lookupInstance(ctx, httpClient, instance, meta)
		if err != nil {
			lastErr = err
			if c.logger != nil {
				c.logger.Warnf("metadata: %s lookup failed instance=%s: %v", c.service, instance.name, err)
			}
			continue
		}
		if result.TMDBID != 0 || result.IMDBID != 0 || result.TVDBID != 0 || result.TVmazeID != 0 || result.Year != 0 || len(result.Genres) != 0 ||
			result.ReleaseGroup != "" {
			return result, nil
		}
	}
	if lastErr != nil {
		return ArrLookupResult{}, lastErr
	}
	return ArrLookupResult{}, nil
}

func (c *httpArrLookupClient) lookupInstance(
	ctx context.Context,
	httpClient *http.Client,
	instance arrInstance,
	meta preparationstate.State,
) (ArrLookupResult, error) {
	switch c.service {
	case "sonarr":
		return lookupSonarr(ctx, httpClient, instance, meta)
	case "radarr":
		return lookupRadarr(ctx, httpClient, instance, meta)
	default:
		return ArrLookupResult{}, fmt.Errorf("unsupported arr service %q", c.service)
	}
}

func lookupSonarr(ctx context.Context, httpClient *http.Client, instance arrInstance, meta preparationstate.State) (ArrLookupResult, error) {
	title, _ := resolveSearchTitles(meta)
	if strings.TrimSpace(title) != "" {
		parseURL, err := urlWithQuery(instance.baseURL, "/api/v3/parse", map[string]string{
			"title": title,
			"path":  meta.SourcePath,
		})
		if err != nil {
			return ArrLookupResult{}, err
		}
		result, err := fetchSonarrParse(ctx, httpClient, parseURL, instance.apiKey)
		if err != nil {
			return ArrLookupResult{}, err
		}
		if result.hasIDs() {
			result.Source = "sonarr"
			return result, nil
		}
	}

	tvdbID := firstNonZero(meta.ArrTVDBID, meta.MediaInfoTVDBID, meta.Identity.TVDBID)
	if tvdbID == 0 {
		return ArrLookupResult{}, nil
	}

	seriesURL, err := urlWithQuery(instance.baseURL, "/api/v3/series", map[string]string{
		"tvdbId":              strconv.Itoa(tvdbID),
		"includeSeasonImages": "false",
	})
	if err != nil {
		return ArrLookupResult{}, err
	}
	result, err := fetchSonarrSeries(ctx, httpClient, seriesURL, instance.apiKey)
	if err != nil {
		return ArrLookupResult{}, err
	}
	result.Source = "sonarr"
	return result, nil
}

func lookupRadarr(ctx context.Context, httpClient *http.Client, instance arrInstance, meta preparationstate.State) (ArrLookupResult, error) {
	term := pathutil.Base(meta.SourcePath)
	if term == "" {
		term = strings.TrimSpace(meta.SourcePath)
	}
	if term != "" {
		lookupURL, err := urlWithQuery(instance.baseURL, "/api/v3/movie/lookup", map[string]string{
			"term": term,
		})
		if err != nil {
			return ArrLookupResult{}, err
		}
		result, err := fetchRadarrLookup(ctx, httpClient, lookupURL, instance.apiKey, meta.SourcePath)
		if err != nil {
			return ArrLookupResult{}, err
		}
		if result.hasIDs() {
			result.Source = "radarr"
			return result, nil
		}
	}

	tmdbID := firstNonZero(meta.ArrTMDBID, meta.MediaInfoTMDBID, meta.Identity.TMDBID)
	if tmdbID == 0 {
		return ArrLookupResult{}, nil
	}
	movieURL, err := urlWithQuery(instance.baseURL, "/api/v3/movie", map[string]string{
		"tmdbId":             strconv.Itoa(tmdbID),
		"excludeLocalCovers": "true",
	})
	if err != nil {
		return ArrLookupResult{}, err
	}
	result, err := fetchRadarrMovie(ctx, httpClient, movieURL, instance.apiKey)
	if err != nil {
		return ArrLookupResult{}, err
	}
	result.Source = "radarr"
	return result, nil
}

func fetchSonarrParse(ctx context.Context, httpClient *http.Client, rawURL string, apiKey string) (ArrLookupResult, error) {
	var payload struct {
		Series            sonarrSeries `json:"series"`
		ParsedEpisodeInfo struct {
			ReleaseGroup string `json:"releaseGroup"`
		} `json:"parsedEpisodeInfo"`
	}
	if err := doArrJSON(ctx, httpClient, rawURL, apiKey, &payload); err != nil {
		return ArrLookupResult{}, err
	}
	result := payload.Series.toResult()
	result.ReleaseGroup = strings.TrimSpace(payload.ParsedEpisodeInfo.ReleaseGroup)
	return result, nil
}

func fetchSonarrSeries(ctx context.Context, httpClient *http.Client, rawURL string, apiKey string) (ArrLookupResult, error) {
	var payload []sonarrSeries
	if err := doArrJSON(ctx, httpClient, rawURL, apiKey, &payload); err != nil {
		return ArrLookupResult{}, err
	}
	if len(payload) == 0 {
		return ArrLookupResult{}, nil
	}
	return payload[0].toResult(), nil
}

func fetchRadarrLookup(ctx context.Context, httpClient *http.Client, rawURL string, apiKey string, sourcePath string) (ArrLookupResult, error) {
	var payload []radarrMovie
	if err := doArrJSON(ctx, httpClient, rawURL, apiKey, &payload); err != nil {
		return ArrLookupResult{}, err
	}
	if len(payload) == 0 {
		return ArrLookupResult{}, nil
	}

	cleanPath := pathutil.Clean(sourcePath)
	baseName := pathutil.Base(cleanPath)
	for _, item := range payload {
		if pathutil.Clean(item.MovieFile.OriginalFilePath) == cleanPath {
			return item.toResult(), nil
		}
	}
	for _, item := range payload {
		if pathutil.Base(item.MovieFile.OriginalFilePath) == baseName {
			return item.toResult(), nil
		}
	}
	return payload[0].toResult(), nil
}

func fetchRadarrMovie(ctx context.Context, httpClient *http.Client, rawURL string, apiKey string) (ArrLookupResult, error) {
	var payload []radarrMovie
	if err := doArrJSON(ctx, httpClient, rawURL, apiKey, &payload); err != nil {
		return ArrLookupResult{}, err
	}
	if len(payload) == 0 {
		return ArrLookupResult{}, nil
	}
	return payload[0].toResult(), nil
}

func doArrJSON(ctx context.Context, httpClient *http.Client, rawURL string, apiKey string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(redaction.RedactValue(string(body), nil)))
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

type sonarrSeries struct {
	TVDBID       int      `json:"tvdbId"`
	IMDBID       string   `json:"imdbId"`
	TVmazeID     int      `json:"tvMazeId"`
	TMDBID       int      `json:"tmdbId"`
	Genres       []string `json:"genres"`
	Year         int      `json:"year"`
	ReleaseGroup string   `json:"releaseGroup"`
}

func (s sonarrSeries) toResult() ArrLookupResult {
	return ArrLookupResult{
		TMDBID:       s.TMDBID,
		IMDBID:       parseIMDbNumber(s.IMDBID),
		TVDBID:       s.TVDBID,
		TVmazeID:     s.TVmazeID,
		Year:         s.Year,
		Genres:       compactGenres(s.Genres),
		ReleaseGroup: strings.TrimSpace(s.ReleaseGroup),
	}
}

type radarrMovie struct {
	IMDBID    string   `json:"imdbId"`
	TMDBID    int      `json:"tmdbId"`
	Year      int      `json:"year"`
	Genres    []string `json:"genres"`
	MovieFile struct {
		OriginalFilePath string `json:"originalFilePath"`
		ReleaseGroup     string `json:"releaseGroup"`
	} `json:"movieFile"`
}

func (m radarrMovie) toResult() ArrLookupResult {
	return ArrLookupResult{
		TMDBID:       m.TMDBID,
		IMDBID:       parseIMDbNumber(m.IMDBID),
		Year:         m.Year,
		Genres:       compactGenres(m.Genres),
		ReleaseGroup: strings.TrimSpace(m.MovieFile.ReleaseGroup),
	}
}

func (r ArrLookupResult) hasIDs() bool {
	return r.TMDBID != 0 || r.IMDBID != 0 || r.TVDBID != 0 || r.TVmazeID != 0
}

func sonarrInstances(cfg config.ArrIntegrationConfig) []arrInstance {
	return collectArrInstances("sonarr", []arrInstance{
		{
			name:    "default",
			baseURL: cfg.SonarrURL,
			apiKey:  cfg.SonarrAPIKey,
		},
		{
			name:    "1",
			baseURL: cfg.SonarrURL1,
			apiKey:  cfg.SonarrAPIKey1,
		},
		{
			name:    "2",
			baseURL: cfg.SonarrURL2,
			apiKey:  cfg.SonarrAPIKey2,
		},
		{
			name:    "3",
			baseURL: cfg.SonarrURL3,
			apiKey:  cfg.SonarrAPIKey3,
		},
	})
}

func radarrInstances(cfg config.ArrIntegrationConfig) []arrInstance {
	return collectArrInstances("radarr", []arrInstance{
		{
			name:    "default",
			baseURL: cfg.RadarrURL,
			apiKey:  cfg.RadarrAPIKey,
		},
		{
			name:    "1",
			baseURL: cfg.RadarrURL1,
			apiKey:  cfg.RadarrAPIKey1,
		},
		{
			name:    "2",
			baseURL: cfg.RadarrURL2,
			apiKey:  cfg.RadarrAPIKey2,
		},
		{
			name:    "3",
			baseURL: cfg.RadarrURL3,
			apiKey:  cfg.RadarrAPIKey3,
		},
	})
}

func collectArrInstances(service string, raw []arrInstance) []arrInstance {
	instances := make([]arrInstance, 0, len(raw))
	for _, instance := range raw {
		baseURL := strings.TrimRight(strings.TrimSpace(instance.baseURL), "/")
		apiKey := strings.TrimSpace(instance.apiKey)
		if baseURL == "" || apiKey == "" {
			continue
		}
		instance.baseURL = baseURL
		instance.apiKey = apiKey
		if instance.name == "" {
			instance.name = service
		}
		instances = append(instances, instance)
	}
	return instances
}

func urlWithQuery(baseURL string, endpoint string, params map[string]string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpoint
	query := parsed.Query()
	for key, value := range params {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		query.Set(key, trimmed)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func parseIMDbNumber(value string) int {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(strings.ToLower(trimmed), "tt")
	if trimmed == "" {
		return 0
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0
	}
	return parsed
}

func compactGenres(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func hasAnimeGenre(genres []string) bool {
	for _, genre := range genres {
		if strings.EqualFold(strings.TrimSpace(genre), "anime") {
			return true
		}
	}
	return false
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
