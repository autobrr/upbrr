// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "time"

// DupeEntry describes one tracker duplicate-search hit before or after local
// filtering.
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

// DupeEpisodeMatch identifies an episode-level match found inside a duplicate
// search result, including season packs that contain the requested episode.
type DupeEpisodeMatch struct {
	ID       string
	Name     string
	Link     string
	Tracker  string
	Internal bool
}

// DupeMatch records why duplicate filtering matched an entry and which tracker
// result can be reused for cross-seed injection.
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

// DupeCheckResult is the duplicate-search outcome for one tracker. Raw contains
// tracker results before filtering, Filtered contains blocking matches, and
// skipped or failed checks carry Status plus SkipReason or Error. SkipCode is a
// stable machine-readable reason, while SkipRules names upload rules that caused
// rule-failure skips.
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
	// SkipCode is a stable machine-readable skip reason.
	SkipCode string
	// SkipRules are upload rule keys that produced a rule-failure skip.
	SkipRules []string
	Status    string
	Error     string
	CheckedAt time.Time `ts_type:"string"`
}

// DupeCheckSummary groups duplicate-search results for one prepared source
// path.
type DupeCheckSummary struct {
	SourcePath  string
	Results     []DupeCheckResult
	Notes       []string
	Eligibility TrackerEligibility
}
