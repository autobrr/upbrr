// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tmdb

type IMDbInfo struct {
	Title            string
	OriginalTitle    string
	LocalizedTitle   string
	OriginalLanguage string
	Year             int
}

type FindInput struct {
	IMDbID             string
	TVDBID             int
	SearchYear         int
	Filename           string
	CategoryPreference string
	IMDbInfo           *IMDbInfo
	Unattended         bool
	Debug              bool
}

type FindResult struct {
	Category         string
	TMDBID           int
	OriginalLanguage string
	FilenameSearch   bool
	Candidates       []Candidate
	AutoSelected     bool
}

type SearchInput struct {
	Filename       string
	SearchYear     int
	Category       string
	SecondaryTitle string
	Unattended     bool
	DontSwitch     bool
	Debug          bool
}

type SearchOutcome struct {
	TMDBID       int
	Category     string
	Candidates   []Candidate
	AutoSelected bool
}

type Candidate struct {
	TMDBID        int
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	PosterPath    string
	Similarity    float64
}

type FindResponse struct {
	MovieResults []FindItem `json:"movie_results"`
	TVResults    []FindItem `json:"tv_results"`
}

type FindItem struct {
	ID               int    `json:"id"`
	OriginalLanguage string `json:"original_language"`
}

type SearchResponse struct {
	Results []SearchItem `json:"results"`
}

type SearchItem struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
}

type TranslationResponse struct {
	Translations []Translation `json:"translations"`
}

type Translation struct {
	ISO6391 string `json:"iso_639_1"`
	// ISO31661 identifies the regional translation variant when TMDB supplies one.
	ISO31661 string          `json:"iso_3166_1"`
	Data     TranslationData `json:"data"`
}

type TranslationData struct {
	Title string `json:"title"`
	Name  string `json:"name"`
}

type MetadataInput struct {
	TMDBID           int
	Category         string
	SearchYear       int
	IMDbID           int
	TVDBID           int
	ManualLanguage   string
	Anime            bool
	MALManual        int
	AKA              string
	OriginalLanguage string
	Poster           string
	QuickieSearch    bool
	Filename         string
	Debug            bool
	AddLogo          bool
	LogoLanguages    []string
	ManualSeason     string
	Season           string
}

type MetadataResult struct {
	Title            string
	Year             int
	ReleaseDate      string
	FirstAirDate     string
	LastAirDate      string
	IMDbID           int
	TVDBID           int
	OriginCountry    []string
	OriginalLanguage string
	OriginalTitle    string
	Keywords         string
	Genres           string
	GenreIDs         string
	Creators         []string
	Directors        []string
	Cast             []string
	MALID            int
	Anime            bool
	Demographic      string
	RetrievedAKA     string
	// LocalizedTitles maps generic language keys and regional variants to TMDB translations.
	LocalizedTitles     map[string]string
	Poster              string
	TMDBPosterPath      string
	Logo                string
	TMDBLogo            string
	Backdrop            string
	Overview            string
	TMDBType            string
	Runtime             int
	YouTube             string
	Certification       string
	ProductionCompanies []Company
	ProductionCountries []Country
	Networks            []Network
	IMDbMismatch        bool
	MismatchedIMDbID    int
}

type Company struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	LogoPath      string `json:"logo_path"`
	OriginCountry string `json:"origin_country"`
}

type Country struct {
	ISO3166 string `json:"iso_3166_1"`
	Name    string `json:"name"`
}

type Network struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	LogoPath      string `json:"logo_path"`
	OriginCountry string `json:"origin_country"`
}

type EpisodeDetails struct {
	Name          string
	Overview      string
	AirDate       string
	StillPath     string
	StillURL      string
	VoteAverage   float64
	EpisodeNumber int
	SeasonNumber  int
	Runtime       int
	Crew          []CrewMember
	GuestStars    []GuestStar
	Director      string
	Writer        string
	IMDbID        string
}

type CrewMember struct {
	Name       string
	Job        string
	Department string
}

type GuestStar struct {
	Name        string
	Character   string
	ProfilePath string
}

type SeasonDetails struct {
	ID           int
	AirDate      string
	Name         string
	Overview     string
	PosterPath   string
	SeasonNumber int
	VoteAverage  float64
	VoteCount    int
	Episodes     []SeasonEpisode
	Images       []PosterImage
	Credits      []CastMember
}

type SeasonEpisode struct {
	AirDate       string
	EpisodeNumber int
	EpisodeType   string
	ID            int
	Name          string
	Overview      string
	Runtime       int
	SeasonNumber  int
	StillPath     string
	VoteAverage   float64
	VoteCount     int
}

type PosterImage struct {
	FilePath string `json:"file_path"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type CastMember struct {
	Name      string `json:"name"`
	Character string `json:"character"`
}

type LogoOptions struct {
	Languages []string
}

type LocalizedDataInput struct {
	DataType         string
	Category         string
	TMDBID           int
	Season           int
	Episode          int
	Language         string
	AppendToResponse string
	CachePath        string
}

type AnimeResult struct {
	Romaji      string
	MALID       int
	English     string
	SeasonYear  string
	Episodes    int
	Demographic string
}
