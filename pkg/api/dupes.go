// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"strings"
	"time"
)

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
// stable machine-readable reason.
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
	SkipCode  string
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

// NewAcceptedDuplicateEvidence detaches one completed duplicate summary for
// transfer into an accepted dry-run plan.
func NewAcceptedDuplicateEvidence(release ReleaseRef, trackers []string, summary DupeCheckSummary) AcceptedDuplicateEvidence {
	selected := make(map[string]struct{}, len(trackers))
	for _, tracker := range trackers {
		if tracker = strings.ToUpper(strings.TrimSpace(tracker)); tracker != "" {
			selected[tracker] = struct{}{}
		}
	}
	results := make([]DupeCheckResult, 0, len(selected))
	for _, result := range summary.Results {
		if _, ok := selected[strings.ToUpper(strings.TrimSpace(result.Tracker))]; ok {
			results = append(results, result)
		}
	}
	return AcceptedDuplicateEvidence{
		Release:  release,
		Trackers: append([]string(nil), trackers...),
		Results:  CloneDupeCheckResults(results),
	}
}

// Clone returns duplicate evidence detached from caller-owned slices.
func (e AcceptedDuplicateEvidence) Clone() AcceptedDuplicateEvidence {
	e.Trackers = append([]string(nil), e.Trackers...)
	e.Results = CloneDupeCheckResults(e.Results)
	return e
}

// CloneDupeCheckResults returns duplicate results with every nested mutable
// collection detached from its source.
func CloneDupeCheckResults(results []DupeCheckResult) []DupeCheckResult {
	if results == nil {
		return nil
	}
	cloned := make([]DupeCheckResult, len(results))
	for index, result := range results {
		result.Raw = cloneDupeEntries(result.Raw)
		result.Filtered = cloneDupeEntries(result.Filtered)
		result.Match.MatchedEpisodeIDs = append([]DupeEpisodeMatch(nil), result.Match.MatchedEpisodeIDs...)
		result.Notes = append([]string(nil), result.Notes...)
		cloned[index] = result
	}
	return cloned
}

func cloneDupeEntries(entries []DupeEntry) []DupeEntry {
	if entries == nil {
		return nil
	}
	cloned := make([]DupeEntry, len(entries))
	for index, entry := range entries {
		entry.Files = append([]string(nil), entry.Files...)
		entry.Flags = append([]string(nil), entry.Flags...)
		cloned[index] = entry
	}
	return cloned
}
