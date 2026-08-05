// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imdb

// Info contains normalized IMDb title metadata. RuntimeMinutes is minutes;
// edition detail map keys are decimal runtime-minute strings.
type Info struct {
	IMDbID           string
	IMDbURL          string
	Title            string
	Country          string
	CountryList      string
	Year             int
	EndYear          int
	AKA              string
	Type             string
	RuntimeMinutes   int
	RuntimeText      string
	Cover            string
	Plot             string
	Genres           string
	Rating           float64
	RatingCount      int
	RatingText       string
	Directors        []Person
	Creators         []Person
	Writers          []Person
	Stars            []Person
	Editions         []string
	EditionDetails   map[string]EditionDetail
	Akas             []AKA
	Episodes         []Episode
	SeasonsSummary   []SeasonSummary
	SoundMixes       []string
	TVYear           int
	OriginalLanguage string
}

type Person struct {
	ID   string
	Name string
}

type EditionDetail struct {
	DisplayName string
	Seconds     int
	Minutes     int
	Attributes  []string
}

type AKA struct {
	Title      string
	Country    string
	Language   string
	Attributes []string
}

type Episode struct {
	ID          string
	Title       string
	ReleaseYear int
	ReleaseDate ReleaseDate
	Season      int
	EpisodeText string
}

type ReleaseDate struct {
	Year  int
	Month int
	Day   int
}

type SeasonSummary struct {
	Season    int
	Year      int
	YearRange string
}

// SearchInput controls release-hint parsing and automatic candidate selection.
// Quickie examines only the first result; Unattended permits selection of an
// otherwise ambiguous top-ranked candidate.
type SearchInput struct {
	Filename          string
	SearchYear        int
	Category          string
	SecondaryTitle    string
	UntouchedFilename string
	ParsedTitle       string
	DurationMinutes   int
	Quickie           bool
	Unattended        bool
	Debug             bool
}

// SearchResult retains ranked candidates even when interactive ambiguity leaves
// IMDbID unset. AutoSelected reports that no manual choice remains.
type SearchResult struct {
	IMDbID       int
	Candidates   []Candidate
	AutoSelected bool
}

type Candidate struct {
	IMDbID     int
	Title      string
	Year       int
	Type       string
	Plot       string
	PosterURL  string
	Similarity float64
}

type EpisodeLookup struct {
	ID              string
	Title           string
	Series          SeriesInfo
	NextEpisode     EpisodeRef
	PreviousEpisode EpisodeRef
}

type SeriesInfo struct {
	SeasonID    string
	Season      string
	SeasonText  string
	EpisodeID   string
	EpisodeText string
	SeriesID    string
	SeriesTitle string
}

type EpisodeRef struct {
	ID    string
	Title string
}
