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

// AniListMetadataResult contains the AniList media fields used to build API
// preview metadata.
//
// Date fields keep AniList fuzzy-date precision, score fields are percentages
// from 0 to 100, and AiringAt fields are Unix timestamps in seconds. Tags keep
// adult/spoiler flags so downstream API/UI layers can filter them before
// display.
type AniListMetadataResult struct {
	// AniListID is the AniList media ID used in AniList URLs.
	AniListID int
	// MALID is the MyAnimeList media ID used as upbrr's canonical anime ID.
	MALID int
	// SiteURL is the canonical AniList media page URL.
	SiteURL string
	// Title* fields preserve AniList's localized title variants.
	TitleRomaji        string
	TitleEnglish       string
	TitleNative        string
	TitleUserPreferred string
	// Description is AniList's plain-text media description.
	Description string
	// Format, Status, Season, and Source are AniList enum values.
	Format string
	Status string
	// StartDate is formatted as YYYY, YYYY-MM, or YYYY-MM-DD depending on AniList precision.
	StartDate string
	// EndDate is formatted as YYYY, YYYY-MM, or YYYY-MM-DD depending on AniList precision.
	EndDate    string
	Season     string
	SeasonYear int
	Episodes   int
	// Duration is AniList's average episode duration in minutes.
	Duration        int
	CountryOfOrigin string
	Source          string
	// Cover* and BannerImage are AniList image URLs or color metadata used by previews.
	CoverExtraLarge string
	CoverLarge      string
	CoverMedium     string
	CoverColor      string
	BannerImage     string
	Genres          []string
	Synonyms        []string
	// AverageScore and MeanScore are AniList percentage scores from 0 to 100.
	AverageScore      int
	MeanScore         int
	Popularity        int
	Favourites        int
	IsAdult           bool
	Tags              []AniListTag
	Studios           []AniListStudio
	Trailer           AniListTrailer
	NextAiringEpisode AniListAiringEpisode
	ExternalLinks     []AniListExternalLink
}

// AniListTag is a media tag returned by AniList for the selected anime.
type AniListTag struct {
	Name string
	// Rank is AniList's tag relevance percentage from 0 to 100.
	Rank     int
	Category string
	// IsAdult and Is*Spoiler let UI consumers omit sensitive tag labels.
	IsAdult          bool
	IsGeneralSpoiler bool
	IsMediaSpoiler   bool
}

// AniListStudio is a studio attached to an AniList media entry.
type AniListStudio struct {
	ID   int
	Name string
	// SiteURL is the AniList studio page URL.
	SiteURL string
}

// AniListTrailer identifies a media trailer from AniList.
type AniListTrailer struct {
	ID   string
	Site string
	// Thumbnail is the provider thumbnail URL when AniList supplies one.
	Thumbnail string
}

// AniListAiringEpisode describes the next scheduled episode for an airing anime.
type AniListAiringEpisode struct {
	// AiringAt is a Unix timestamp in seconds.
	AiringAt int
	// TimeUntilAiring is seconds from AniList's response time until AiringAt.
	TimeUntilAiring int
	Episode         int
}

// AniListExternalLink is a public provider or official link attached to AniList media.
type AniListExternalLink struct {
	Site     string
	URL      string
	Type     string
	Language string
}
