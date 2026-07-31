// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvdb

import "github.com/autobrr/upbrr/pkg/api"

type SeriesSearchResult struct {
	TVDBID          int
	Name            string
	NameEnglish     string
	PrimaryLanguage string
	Year            string
	Aliases         []Alias
}

// SeriesMetadata contains extended series fields plus naming-safe year
// provenance and optional English translation data.
type SeriesMetadata struct {
	TVDBID      int
	Name        string
	NameEnglish string
	// Year is the selected TVDB series year independent of naming eligibility.
	Year       int
	SeriesYear int
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
	NameDisambiguation   NameDisambiguation
}

// NameDisambiguation contains the general TVDB name-search evidence for one
// selected series.
type NameDisambiguation struct {
	// CanonicalName is the selected English series name used for comparison.
	CanonicalName string
	// SeriesYear is the selected series year used for same-year comparison.
	SeriesYear int
	// Locale is emitted only when IncludeLocale is true.
	Locale string
	// SameNameSeries counts distinct other TVDB IDs with the same normalized
	// English primary name or alias.
	SameNameSeries int
	// SameNameAndYearSeries counts SameNameSeries entries with a matching known
	// year.
	SameNameAndYearSeries int
	// IncludeYear and IncludeLocale are prepared decisions for naming callers.
	IncludeYear   bool
	IncludeLocale bool
	// Status describes the completeness of the general-name search evidence.
	Status api.MetadataEvidenceStatus
	// Source identifies the versioned disambiguation algorithm.
	Source string
}

// NameDisambiguationInput contains authoritative selected-series facts needed
// to search for distinct TVDB series with the same English name.
type NameDisambiguationInput struct {
	TVDBID             int
	NameEnglish        string
	SeriesYear         int
	OriginalCountry    string
	ExplicitNamingYear bool
}

type Alias struct {
	Name     string
	Language string
}

// EpisodesData contains episode pages and series naming/schedule context. Air
// dates use provider YYYY-MM-DD strings; AirsTimezoneSource identifies whether
// the zone was explicit or inferred.
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
	// Image is the episode image URL returned by TVDB.
	Image string
}

// EpisodeQuery supplies date, season/episode, and absolute-number evidence used
// to validate cache completeness and select a match. CacheBasePath is a host
// filesystem directory override.
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
	// Image is the matched episode image URL returned by TVDB.
	Image string
}

type EpisodeTranslation struct {
	Name     string
	Overview string
}
