// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

type DupeEntry struct {
	Name        string
	SizeBytes   int64
	SizeKnown   bool
	SizeText    string
	Files       []string
	FileCount   int
	Trumpable   bool
	Link        string
	Download    string
	Flags       []string
	ID          string
	Type        string
	Res         string
	Internal    bool
	BDInfo      string
	Description string
}

type DupeEpisodeMatch struct {
	ID       string
	Name     string
	Link     string
	Tracker  string
	Internal bool
}

type DupeMatch struct {
	FilenameMatch             string
	FileCountMatch            int
	SizeMatch                 string
	TrumpableID               string
	MatchedID                 string
	MatchedName               string
	MatchedLink               string
	MatchedDownload           string
	MatchedReason             string
	SeasonPackExists          bool
	SeasonPackName            string
	SeasonPackLink            string
	SeasonPackID              string
	SeasonPackContainsEpisode bool
	MatchedEpisodeIDs         []DupeEpisodeMatch
}

type DupeCheckResult struct {
	Tracker     string
	Raw         []DupeEntry
	Filtered    []DupeEntry
	HasDupes    bool
	ContentFail bool
	Match       DupeMatch
	Notes       []string
	Skipped     bool
	SkipReason  string
	SkipRules   []string
	Status      string
	Error       string
	CheckedAt   time.Time `ts_type:"string"`
}

type DupeCheckSummary struct {
	SourcePath string
	Results    []DupeCheckResult
	Notes      []string
}
