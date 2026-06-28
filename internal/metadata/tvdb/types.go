// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

type SeriesSearchResult struct {
	TVDBID  int
	Name    string
	Year    string
	Aliases []Alias
}

type SeriesMetadata struct {
	TVDBID      int
	Name        string
	NameEnglish string
	SeriesYear  int
	// SeriesYearSource identifies the TVDB title signal that made SeriesYear safe for release-name disambiguation.
	SeriesYearSource string
	// SeriesYearConfidence is "high" for explicit title/alias years and "low" for guarded slug-derived years.
	SeriesYearConfidence string
	Overview             string
	OverviewEnglish      string
	Slug                 string
	FirstAired           string
	Type                 string
	Status               string
	Network              string
	OriginalCountry      string
	OriginalLanguage     string
	HasEnglish           bool
	Genres               []string
	Poster               string
	Aliases              []Alias
}

type Alias struct {
	Name     string
	Language string
}

type EpisodesData struct {
	Episodes    []Episode
	Aliases     []Alias
	Slug        string
	SeriesTitle string
	SeriesYear  int
	// SeriesYearSource identifies the TVDB title signal that made SeriesYear safe for release-name disambiguation.
	SeriesYearSource string
	// SeriesYearConfidence is "high" for explicit title/alias years and "low" for guarded slug-derived years.
	SeriesYearConfidence string
	AirsDays             []string
	AirsTime             string
	AirsTimezone         string
	AirsTimezoneSource   string
}

type Episode struct {
	ID             int
	SeasonNumber   int
	Number         int
	AbsoluteNumber int
	SeasonName     string
	Name           string
	Overview       string
	Year           int
	Aired          string
}

type EpisodeQuery struct {
	Season        int
	Episode       int
	Absolute      int
	AiredDate     string
	CacheBasePath string
	Debug         bool
}

type EpisodeMatch struct {
	SeasonName    string
	EpisodeName   string
	Overview      string
	SeasonNumber  int
	EpisodeNumber int
	Year          int
	EpisodeID     int
	Aired         string
}

type EpisodeTranslation struct {
	Name     string
	Overview string
}
