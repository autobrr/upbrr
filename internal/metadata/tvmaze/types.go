// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvmaze

// SearchInput controls identifier precedence and name fallback. StrictIDOnly
// disables name searches unless AllowNameFallback explicitly restores them;
// ManualDate prevents ordinary candidate auto-selection.
type SearchInput struct {
	Filename          string
	Year              string
	ImdbID            string
	TVDBID            string
	ManualDate        string
	ManualID          int
	StrictIDOnly      bool
	AllowNameFallback bool
	Debug             bool
}

// SearchResult contains the selected TVmaze identity plus deduplicated provider
// candidates. AutoSelected excludes manual-ID and manual-date results.
type SearchResult struct {
	SelectedID   int
	IMDBID       int
	TVDBID       int
	Candidates   []Candidate
	AutoSelected bool
}

type Candidate struct {
	ID             int
	Name           string
	Premiered      string
	Ended          string
	Summary        string
	Status         string
	Type           string
	Language       string
	Genres         []string
	Runtime        int
	AverageRuntime int
	Rating         float64
	Weight         int
	OfficialSite   string
	Country        string
	Network        TVNetwork
	WebChannel     TVNetwork
	Image          Image
	Externals      Externals
}

type TVNetwork struct {
	Name      string
	Country   string
	Logo      string
	LogoSmall string
}

type Image struct {
	Original string
	Medium   string
}

type Externals struct {
	IMDB  string
	TVDB  int
	Other map[string]any
}

// EpisodeLookupContext supplies date fallback evidence when number lookup returns
// not found. ManualDate takes precedence over TVDBEpisodeData.
type EpisodeLookupContext struct {
	ManualDate      string
	TVDBEpisodeID   int
	TVDBEpisodeData []TVDBEpisode
	Debug           bool
}

type TVDBEpisode struct {
	ID    int
	Aired string
}

type EpisodeData struct {
	EpisodeName       string
	Overview          string
	SeasonNumber      int
	EpisodeNumber     int
	AirDate           string
	Runtime           int
	SeriesName        string
	SeriesOverview    string
	Image             string
	ImageMedium       string
	SeriesImage       string
	SeriesImageMedium string
}
