// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
)

const imageBaseURL = "https://image.tmdb.org/t/p/original"

var yearPattern = regexp.MustCompile(`(18|19|20)\d{2}`)

func (c *Client) FetchMetadata(ctx context.Context, input MetadataInput) (MetadataResult, error) {
	if input.TMDBID == 0 {
		return MetadataResult{}, errNotFound
	}
	category := strings.ToUpper(strings.TrimSpace(input.Category))
	if category == "" {
		category = "MOVIE"
	}

	var media mediaResponse
	path := fmt.Sprintf("/movie/%d", input.TMDBID)
	if category == "TV" {
		path = fmt.Sprintf("/tv/%d", input.TMDBID)
	}
	if err := c.getJSON(ctx, path, map[string]string{"api_key": c.apiKey}, &media); err != nil {
		return MetadataResult{}, err
	}

	result := MetadataResult{}
	title := media.Title
	originalTitle := media.OriginalTitle
	if category == "TV" {
		title = media.Name
		originalTitle = media.OriginalName
	}
	result.Title = title
	result.OriginalTitle = metautil.FirstNonEmpty(originalTitle, title)
	result.Overview = media.Overview
	result.OriginCountry = append([]string{}, media.OriginCountry...)
	result.ProductionCompanies = media.ProductionCompanies
	result.ProductionCountries = media.ProductionCountries
	result.Networks = media.Networks

	posterPath := strings.TrimSpace(media.PosterPath)
	result.TMDBPosterPath = posterPath
	if strings.TrimSpace(input.Poster) != "" {
		result.Poster = strings.TrimSpace(input.Poster)
	} else if posterPath != "" {
		result.Poster = imageBaseURL + posterPath
	}

	backdropPath := strings.TrimSpace(media.BackdropPath)
	if backdropPath != "" {
		result.Backdrop = imageBaseURL + backdropPath
	}

	result.FirstAirDate = media.FirstAirDate
	result.LastAirDate = media.LastAirDate
	result.ReleaseDate = media.ReleaseDate

	if category == "MOVIE" {
		result.TMDBType = "Movie"
		year := parseYear(media.ReleaseDate)
		if year == 0 {
			year = input.SearchYear
		}
		result.Year = year
		runtime := media.Runtime
		if runtime == 0 {
			runtime = 60
		}
		result.Runtime = runtime
	} else {
		result.TMDBType = metautil.FirstNonEmpty(media.Type, "Scripted")
		year := parseYear(media.FirstAirDate)
		if year == 0 {
			year = input.SearchYear
		}
		if year == 0 && title != "" {
			year = parseYearFromTitle(title)
		}
		if year == 0 {
			year = parseYear(media.LastAirDate)
		}
		result.Year = year
		runtime := 60
		if len(media.EpisodeRunTime) > 0 && media.EpisodeRunTime[0] > 0 {
			runtime = media.EpisodeRunTime[0]
		}
		result.Runtime = runtime
	}

	originalLanguage := strings.TrimSpace(input.OriginalLanguage)
	if originalLanguage == "" {
		originalLanguage = strings.TrimSpace(input.ManualLanguage)
	}
	if originalLanguage == "" {
		originalLanguage = strings.TrimSpace(media.OriginalLanguage)
	}
	result.OriginalLanguage = originalLanguage

	result.Creators = uniqueNames(media.CreatedBy)
	result.Genres, result.GenreIDs = genresFromMedia(media.Genres)

	var (
		external externalIDsResponse
		videos   videosResponse
		keywords keywordsResponse
		credits  creditsResponse
		images   imagesResponse

		externalErr error
		videosErr   error
		keywordsErr error
		creditsErr  error
		imagesErr   error
	)

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		endpoint := path + "/external_ids"
		if err := c.getJSON(ctx, endpoint, map[string]string{"api_key": c.apiKey}, &external); err != nil {
			externalErr = err
		}
		return nil
	})
	group.Go(func() error {
		endpoint := path + "/videos"
		if err := c.getJSON(ctx, endpoint, map[string]string{"api_key": c.apiKey}, &videos); err != nil {
			videosErr = err
		}
		return nil
	})
	group.Go(func() error {
		endpoint := path + "/keywords"
		if err := c.getJSON(ctx, endpoint, map[string]string{"api_key": c.apiKey}, &keywords); err != nil {
			keywordsErr = err
		}
		return nil
	})
	group.Go(func() error {
		endpoint := path + "/credits"
		if err := c.getJSON(ctx, endpoint, map[string]string{"api_key": c.apiKey}, &credits); err != nil {
			creditsErr = err
		}
		return nil
	})
	if input.AddLogo {
		group.Go(func() error {
			endpoint := path + "/images"
			if err := c.getJSON(ctx, endpoint, map[string]string{"api_key": c.apiKey}, &images); err != nil {
				imagesErr = err
			}
			return nil
		})
	}
	_ = group.Wait()

	if externalErr == nil {
		result = applyExternalIDs(result, external, input, media)
	} else if c.logger != nil {
		c.logger.Warnf("tmdb: external ids lookup failed: %v", externalErr)
	}

	if videosErr == nil {
		result.YouTube = findTrailer(videos.Results)
	} else if c.logger != nil {
		c.logger.Warnf("tmdb: video lookup failed: %v", videosErr)
	}

	if keywordsErr == nil {
		result.Keywords = keywordsString(category, keywords)
	} else if c.logger != nil {
		c.logger.Warnf("tmdb: keywords lookup failed: %v", keywordsErr)
	}

	if creditsErr == nil {
		result.Directors, result.Cast = collectCredits(credits)
	} else if c.logger != nil {
		c.logger.Warnf("tmdb: credits lookup failed: %v", creditsErr)
	}

	if input.AddLogo && imagesErr == nil {
		logo, logoName := selectLogo(images.Logos, input.LogoLanguages)
		result.Logo = logo
		result.TMDBLogo = logoName
	} else if input.AddLogo && imagesErr != nil && c.logger != nil {
		c.logger.Warnf("tmdb: logo lookup failed: %v", imagesErr)
	}

	result.Anime = input.Anime
	if !result.Anime {
		result.Anime = isAnime(media.OriginalLanguage, media.Genres)
	}
	if result.Anime {
		animeResult, err := c.ResolveAnime(ctx, title, input)
		if err == nil {
			result.MALID = animeResult.MALID
			result.RetrievedAKA = animeResult.Romaji
			result.Demographic = animeResult.Demographic
		}
	}
	if input.MALManual != 0 {
		result.MALID = input.MALManual
	}

	if result.RetrievedAKA != "" {
		aka := "AKA " + result.RetrievedAKA
		if shouldKeepAKA(result.Title, aka, result.Year) {
			result.RetrievedAKA = aka
		} else {
			result.RetrievedAKA = ""
		}
	}

	if c.logger != nil {
		c.logger.Infof("tmdb: metadata loaded id=%d title=%q year=%d type=%s", input.TMDBID, result.Title, result.Year, result.TMDBType)
	}

	return result, nil
}

func (c *Client) GetKeywords(ctx context.Context, tmdbID int, category string) (string, error) {
	if tmdbID == 0 {
		return "", errNotFound
	}
	endpoint := "movie"
	if strings.EqualFold(category, "TV") {
		endpoint = "tv"
	}
	path := fmt.Sprintf("/%s/%d/keywords", endpoint, tmdbID)
	var keywords keywordsResponse
	if err := c.getJSON(ctx, path, map[string]string{"api_key": c.apiKey}, &keywords); err != nil {
		return "", err
	}
	return keywordsString(category, keywords), nil
}

func (c *Client) GetDirectors(ctx context.Context, tmdbID int, category string) ([]string, error) {
	if tmdbID == 0 {
		return nil, errNotFound
	}
	endpoint := "movie"
	if strings.EqualFold(category, "TV") {
		endpoint = "tv"
	}
	path := fmt.Sprintf("/%s/%d/credits", endpoint, tmdbID)
	var credits creditsResponse
	if err := c.getJSON(ctx, path, map[string]string{"api_key": c.apiKey}, &credits); err != nil {
		return nil, err
	}
	directors, _ := collectCredits(credits)
	return directors, nil
}

func (c *Client) GetEpisodeDetails(ctx context.Context, tmdbID, season, episode int) (EpisodeDetails, error) {
	if tmdbID == 0 || season == 0 || episode == 0 {
		return EpisodeDetails{}, errNotFound
	}
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d", tmdbID, season, episode)
	params := map[string]string{
		"api_key":            c.apiKey,
		"append_to_response": "images,credits,external_ids",
	}
	var resp episodeDetailsResponse
	if err := c.getJSON(ctx, path, params, &resp); err != nil {
		return EpisodeDetails{}, err
	}
	details := EpisodeDetails{
		Name:          resp.Name,
		Overview:      resp.Overview,
		AirDate:       resp.AirDate,
		StillPath:     resp.StillPath,
		VoteAverage:   resp.VoteAverage,
		EpisodeNumber: resp.EpisodeNumber,
		SeasonNumber:  resp.SeasonNumber,
		Runtime:       resp.Runtime,
		IMDbID:        resp.ExternalIDs.IMDbID,
	}
	if resp.StillPath != "" {
		details.StillURL = imageBaseURL + resp.StillPath
	}
	for _, crew := range resp.Crew {
		details.Crew = append(details.Crew, CrewMember(crew))
		if crew.Job == "Director" {
			details.Director = crew.Name
		}
		if crew.Job == "Writer" {
			details.Writer = crew.Name
		}
	}
	for _, guest := range resp.GuestStars {
		details.GuestStars = append(details.GuestStars, GuestStar(guest))
	}
	return details, nil
}

func (c *Client) GetSeasonDetails(ctx context.Context, tmdbID, season int) (SeasonDetails, error) {
	if tmdbID == 0 || season == 0 {
		return SeasonDetails{}, errNotFound
	}
	path := fmt.Sprintf("/tv/%d/season/%d", tmdbID, season)
	params := map[string]string{
		"api_key":            c.apiKey,
		"append_to_response": "images,credits",
	}
	var resp seasonDetailsResponse
	if err := c.getJSON(ctx, path, params, &resp); err != nil {
		return SeasonDetails{}, err
	}
	result := SeasonDetails{
		ID:           resp.ID,
		AirDate:      resp.AirDate,
		Name:         resp.Name,
		Overview:     resp.Overview,
		PosterPath:   resp.PosterPath,
		SeasonNumber: resp.SeasonNumber,
		VoteAverage:  resp.VoteAverage,
		VoteCount:    resp.VoteCount,
	}
	for _, ep := range resp.Episodes {
		result.Episodes = append(result.Episodes, SeasonEpisode(ep))
	}
	result.Images = append([]PosterImage{}, resp.Images.Posters...)
	for _, cast := range resp.Credits.Cast {
		result.Credits = append(result.Credits, CastMember{Name: cast.Name, Character: cast.Character})
	}
	return result, nil
}

func (c *Client) DailyToSeasonEpisode(ctx context.Context, tmdbID int, date time.Time) (int, int, error) {
	if tmdbID == 0 {
		return 0, 0, errNotFound
	}
	var tv tvShowResponse
	if err := c.getJSON(ctx, fmt.Sprintf("/tv/%d", tmdbID), map[string]string{"api_key": c.apiKey}, &tv); err != nil {
		return 0, 0, err
	}
	seasonNumber := 0
	for _, season := range tv.Seasons {
		if season.AirDate == "" {
			continue
		}
		aired, err := time.Parse("2006-01-02", season.AirDate)
		if err != nil {
			continue
		}
		if !aired.After(date) && season.SeasonNumber > seasonNumber {
			seasonNumber = season.SeasonNumber
		}
	}
	if seasonNumber == 0 {
		return 0, 0, errNotFound
	}

	var seasonDetails seasonDetailsResponse
	path := fmt.Sprintf("/tv/%d/season/%d", tmdbID, seasonNumber)
	if err := c.getJSON(ctx, path, map[string]string{"api_key": c.apiKey}, &seasonDetails); err != nil {
		return 0, 0, err
	}
	for _, episode := range seasonDetails.Episodes {
		if episode.AirDate == date.Format("2006-01-02") {
			return seasonNumber, episode.EpisodeNumber, nil
		}
	}
	return 0, 0, errNotFound
}

func (c *Client) GetLocalizedData(ctx context.Context, input LocalizedDataInput) (map[string]any, error) {
	endpoint, err := localizedEndpoint(input)
	if err != nil {
		return nil, err
	}
	params := map[string]string{
		"api_key":  c.apiKey,
		"language": input.Language,
	}
	if strings.TrimSpace(input.AppendToResponse) != "" {
		params["append_to_response"] = input.AppendToResponse
	}

	if strings.TrimSpace(input.CachePath) == "" {
		var data map[string]any
		if err := c.getJSON(ctx, endpoint, params, &data); err != nil {
			return nil, err
		}
		return data, nil
	}

	return loadCachedLocalized(ctx, c, input, endpoint, params)
}

func genresFromMedia(genres []genre) (string, string) {
	if len(genres) == 0 {
		return "", ""
	}
	nameParts := make([]string, 0, len(genres))
	idParts := make([]string, 0, len(genres))
	for _, g := range genres {
		nameParts = append(nameParts, strings.ReplaceAll(g.Name, ",", " "))
		idParts = append(idParts, strconv.Itoa(g.ID))
	}
	return strings.Join(nameParts, ", "), strings.Join(idParts, ", ")
}

func keywordsString(category string, keywords keywordsResponse) string {
	items := keywords.Keywords
	if strings.EqualFold(category, "TV") {
		items = keywords.Results
	}
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, strings.ReplaceAll(item.Name, ",", " "))
	}
	return strings.Join(parts, ", ")
}

func collectCredits(credits creditsResponse) ([]string, []string) {
	directors := make([]string, 0)
	cast := make([]string, 0)
	seenDirectors := make(map[string]struct{})
	seenCast := make(map[string]struct{})

	for _, item := range append(credits.Cast, credits.Crew...) {
		name := metautil.FirstNonEmpty(item.OriginalName, item.Name)
		if name == "" {
			continue
		}
		if item.KnownForDepartment == "Directing" || item.Job == "Director" {
			if _, ok := seenDirectors[name]; !ok {
				seenDirectors[name] = struct{}{}
				directors = append(directors, name)
			}
		}
		if item.KnownForDepartment == "Acting" || item.Job == "Actor" || item.Job == "Actress" {
			if _, ok := seenCast[name]; !ok {
				seenCast[name] = struct{}{}
				cast = append(cast, name)
			}
		}
	}

	if len(directors) > 5 {
		directors = directors[:5]
	}
	if len(cast) > 5 {
		cast = cast[:5]
	}
	return directors, cast
}

func uniqueNames(creators []creator) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, item := range creators {
		name := metautil.FirstNonEmpty(item.OriginalName, item.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func selectLogo(logos []logoImage, languages []string) (string, string) {
	langs := normalizeLanguages(languages)
	for _, lang := range langs {
		for _, logo := range logos {
			if logo.ISO6391 == lang {
				return imageBaseURL + logo.FilePath, strings.TrimPrefix(logo.FilePath, "/")
			}
		}
	}
	for _, logo := range logos {
		if logo.ISO6391 == "" {
			return imageBaseURL + logo.FilePath, strings.TrimPrefix(logo.FilePath, "/")
		}
	}
	return "", ""
}

func normalizeLanguages(languages []string) []string {
	items := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		items = append(items, value)
	}
	for _, lang := range languages {
		add(lang)
	}
	add("en")
	return items
}

func parseYear(value string) int {
	if len(value) < 4 {
		return 0
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil {
		return 0
	}
	return year
}

func parseYearFromTitle(title string) int {
	match := yearPattern.FindString(title)
	if match == "" {
		return 0
	}
	year, err := strconv.Atoi(match)
	if err != nil {
		return 0
	}
	return year
}

func isAnime(language string, genres []genre) bool {
	if !strings.EqualFold(strings.TrimSpace(language), "ja") {
		return false
	}
	for _, g := range genres {
		if g.ID == 16 {
			return true
		}
	}
	return false
}

func shouldKeepAKA(title, aka string, year int) bool {
	cleanTitle := strings.ToLower(title)
	cleanAKA := strings.ToLower(strings.TrimPrefix(aka, "AKA "))
	if cleanAKA == "" {
		return false
	}
	if metautil.SimilarityRatio(cleanTitle, cleanAKA) >= 0.7 {
		return false
	}
	if year > 0 {
		cleanAKA = strings.ReplaceAll(cleanAKA, fmt.Sprintf("(%d)", year), "")
		cleanAKA = strings.TrimSpace(cleanAKA)
		if cleanAKA == "" {
			return false
		}
	}
	return true
}

func applyExternalIDs(result MetadataResult, external externalIDsResponse, input MetadataInput, media mediaResponse) MetadataResult {
	originalIMDbID := input.IMDbID
	imdbID := originalIMDbID
	if input.QuickieSearch || imdbID == 0 {
		if external.IMDbID != "" {
			parsed := ExtractIMDbID(external.IMDbID)
			if parsed != 0 {
				if originalIMDbID != 0 && parsed != originalIMDbID && input.QuickieSearch {
					result.IMDbMismatch = true
					result.MismatchedIMDbID = parsed
					imdbID = originalIMDbID
				} else {
					imdbID = parsed
				}
			}
		}
	}
	result.IMDbID = imdbID
	result.TVDBID = input.TVDBID
	if result.TVDBID == 0 && external.TVDBID != 0 {
		result.TVDBID = external.TVDBID
	}
	if result.IMDbID == 0 && media.IMDbID != "" {
		result.IMDbID = ExtractIMDbID(media.IMDbID)
	}
	return result
}

func findTrailer(items []videoItem) string {
	for _, item := range items {
		if item.Site == "YouTube" && item.Type == "Trailer" {
			return "https://www.youtube.com/watch?v=" + item.Key
		}
	}
	return ""
}

func ExtractIMDbID(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if strings.Contains(value, "/title/") {
		parts := strings.Split(value, "/title/")
		value = parts[len(parts)-1]
	}
	value = strings.TrimPrefix(value, "tt")
	value = strings.Trim(value, "/")
	id, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return id
}

func localizedEndpoint(input LocalizedDataInput) (string, error) {
	typeValue := strings.TrimSpace(strings.ToLower(input.DataType))
	category := strings.ToLower(strings.TrimSpace(input.Category))
	if category == "" {
		category = "movie"
	}
	if input.TMDBID == 0 {
		return "", errNotFound
	}

	switch typeValue {
	case "main":
		return fmt.Sprintf("/%s/%d", category, input.TMDBID), nil
	case "season":
		if input.Season == 0 {
			return "", errNotFound
		}
		return fmt.Sprintf("/tv/%d/season/%d", input.TMDBID, input.Season), nil
	case "episode":
		if input.Season == 0 || input.Episode == 0 {
			return "", errNotFound
		}
		return fmt.Sprintf("/tv/%d/season/%d/episode/%d", input.TMDBID, input.Season, input.Episode), nil
	default:
		return "", fmt.Errorf("tmdb: unknown localized data type: %s", input.DataType)
	}
}

func loadCachedLocalized(ctx context.Context, c *Client, input LocalizedDataInput, endpoint string, params map[string]string) (map[string]any, error) {
	lock := localizedLock(input.CachePath)
	lock.mu.Lock()
	defer lock.mu.Unlock()

	cached := map[string]map[string]any{}
	if data, err := readJSONFile(input.CachePath); err == nil {
		_ = json.Unmarshal(data, &cached)
	}
	if langData, ok := cached[input.Language]; ok {
		if value, ok := langData[input.DataType]; ok {
			if result, ok := value.(map[string]any); ok {
				return result, nil
			}
		}
	}

	var result map[string]any
	if err := c.getJSON(ctx, endpoint, params, &result); err != nil {
		return nil, err
	}

	if cached[input.Language] == nil {
		cached[input.Language] = map[string]any{}
	}
	cached[input.Language][input.DataType] = result
	if encoded, err := json.Marshal(cached); err == nil {
		_ = writeJSONFile(input.CachePath, encoded)
	}
	return result, nil
}

func readJSONFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errNotFound
	}
	return os.ReadFile(path)
}

func writeJSONFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return errNotFound
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

var localizedLocks = newLockPool()

func localizedLock(path string) *lockHandle {
	return localizedLocks.lock(path)
}

type lockPool struct {
	mu   sync.Mutex
	pool map[string]*lockHandle
}

type lockHandle struct {
	mu sync.Mutex
}

func newLockPool() *lockPool {
	return &lockPool{pool: make(map[string]*lockHandle)}
}

func (p *lockPool) lock(key string) *lockHandle {
	p.mu.Lock()
	defer p.mu.Unlock()
	if key == "" {
		key = "default"
	}
	if handle, ok := p.pool[key]; ok {
		return handle
	}
	handle := &lockHandle{}
	p.pool[key] = handle
	return handle
}

// JSON response types.

type mediaResponse struct {
	Title               string    `json:"title"`
	Name                string    `json:"name"`
	OriginalTitle       string    `json:"original_title"`
	OriginalName        string    `json:"original_name"`
	ReleaseDate         string    `json:"release_date"`
	FirstAirDate        string    `json:"first_air_date"`
	LastAirDate         string    `json:"last_air_date"`
	Runtime             int       `json:"runtime"`
	EpisodeRunTime      []int     `json:"episode_run_time"`
	Type                string    `json:"type"`
	Genres              []genre   `json:"genres"`
	Overview            string    `json:"overview"`
	OriginalLanguage    string    `json:"original_language"`
	PosterPath          string    `json:"poster_path"`
	BackdropPath        string    `json:"backdrop_path"`
	ProductionCompanies []Company `json:"production_companies"`
	ProductionCountries []Country `json:"production_countries"`
	OriginCountry       []string  `json:"origin_country"`
	CreatedBy           []creator `json:"created_by"`
	Networks            []Network `json:"networks"`
	IMDbID              string    `json:"imdb_id"`
}

type creator struct {
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
}

type genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type externalIDsResponse struct {
	IMDbID string `json:"imdb_id"`
	TVDBID int    `json:"tvdb_id"`
}

type videosResponse struct {
	Results []videoItem `json:"results"`
}

type videoItem struct {
	Site string `json:"site"`
	Type string `json:"type"`
	Key  string `json:"key"`
}

type keywordsResponse struct {
	Keywords []keyword `json:"keywords"`
	Results  []keyword `json:"results"`
}

type keyword struct {
	Name string `json:"name"`
}

type creditsResponse struct {
	Cast []creditItem `json:"cast"`
	Crew []creditItem `json:"crew"`
}

type creditItem struct {
	Name               string `json:"name"`
	OriginalName       string `json:"original_name"`
	KnownForDepartment string `json:"known_for_department"`
	Job                string `json:"job"`
}

type imagesResponse struct {
	Logos []logoImage `json:"logos"`
}

type logoImage struct {
	FilePath string `json:"file_path"`
	ISO6391  string `json:"iso_639_1"`
}

type episodeDetailsResponse struct {
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	StillPath     string  `json:"still_path"`
	VoteAverage   float64 `json:"vote_average"`
	EpisodeNumber int     `json:"episode_number"`
	SeasonNumber  int     `json:"season_number"`
	Runtime       int     `json:"runtime"`
	ExternalIDs   struct {
		IMDbID string `json:"imdb_id"`
	} `json:"external_ids"`
	Crew       []creditMember `json:"crew"`
	GuestStars []guestStar    `json:"guest_stars"`
}

type creditMember struct {
	Name       string `json:"name"`
	Job        string `json:"job"`
	Department string `json:"department"`
}

type guestStar struct {
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
}

type seasonDetailsResponse struct {
	ID           int             `json:"id"`
	AirDate      string          `json:"air_date"`
	Name         string          `json:"name"`
	Overview     string          `json:"overview"`
	PosterPath   string          `json:"poster_path"`
	SeasonNumber int             `json:"season_number"`
	VoteAverage  float64         `json:"vote_average"`
	VoteCount    int             `json:"vote_count"`
	Episodes     []seasonEpisode `json:"episodes"`
	Images       struct {
		Posters []PosterImage `json:"posters"`
	} `json:"images"`
	Credits struct {
		Cast []CastMember `json:"cast"`
	} `json:"credits"`
}

type seasonEpisode struct {
	AirDate       string  `json:"air_date"`
	EpisodeNumber int     `json:"episode_number"`
	EpisodeType   string  `json:"episode_type"`
	ID            int     `json:"id"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	Runtime       int     `json:"runtime"`
	SeasonNumber  int     `json:"season_number"`
	StillPath     string  `json:"still_path"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
}

type tvShowResponse struct {
	Seasons []seasonRef `json:"seasons"`
}

type seasonRef struct {
	SeasonNumber int    `json:"season_number"`
	AirDate      string `json:"air_date"`
}
