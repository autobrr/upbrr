// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// Verdict is the upload decision derived from duplicate evidence and explicit authorization.
type Verdict string

const (
	// VerdictClear permits upload because the resolved outcome contains no duplicate.
	VerdictClear Verdict = "clear"
	// VerdictBlocked prevents upload until an overrideable outcome is explicitly authorized.
	VerdictBlocked Verdict = "blocked"
	// VerdictOverridden permits upload after explicit authorization of a remote candidate.
	VerdictOverridden Verdict = "overridden"
	// VerdictWaived permits upload after explicit authorization of unresolved remote work.
	VerdictWaived Verdict = "waived"
)

// AuthorizationKind identifies an explicit operator decision.
type AuthorizationKind string

const (
	// AuthorizationNone indicates that no operator authorization is attached.
	AuthorizationNone AuthorizationKind = ""
	// AuthorizationOverride authorizes a resolved remote duplicate candidate.
	AuthorizationOverride AuthorizationKind = "override"
	// AuthorizationWaiver authorizes a not-run or failed remote duplicate check.
	AuthorizationWaiver AuthorizationKind = "waiver"
)

type assessmentEntry struct {
	tracker       string
	binding       string
	outcomeID     string
	disposition   Disposition
	code          string
	hasDupes      bool
	verdict       Verdict
	authorization AuthorizationKind
	match         api.DupeMatch
	privateRaw    []api.DupeEntry
}

// Assessment is immutable reusable duplicate decision state. Full public outcomes are not retained.
// assessed distinguishes an explicit empty check result from the zero value, which means no
// assessment was supplied and therefore must not alter an existing metadata projection.
type Assessment struct {
	entries  map[string]assessmentEntry
	assessed bool
}

// Decision is a defensive assessment projection for Core tracker planning.
type Decision struct {
	// Tracker is the normalized tracker identifier.
	Tracker string
	// Disposition records whether search resolved, did not run, or failed.
	Disposition Disposition
	// Code is the stable disposition reason code.
	Code string
	// Verdict is the current upload eligibility decision.
	Verdict Verdict
	// Authorization records the explicit operator decision, when present.
	Authorization AuthorizationKind
	// Match contains a defensive public projection of matched evidence.
	Match api.DupeMatch
}

// AssessmentEvidence is one structural adapter outcome used to construct reusable decision state.
type AssessmentEvidence struct {
	// Tracker is the normalized tracker identifier.
	Tracker string
	// Disposition records whether the adapter resolved, did not run, or failed.
	Disposition Disposition
	// Code is the stable structural reason code.
	Code string
	// HasDupes reports whether filtering found a blocking candidate.
	HasDupes bool
	// Match contains the selected candidate evidence.
	Match api.DupeMatch
	// Raw contains private adapter evidence defensively copied into the assessment.
	Raw []api.DupeEntry
}

// EmptyAssessment returns an assessment with no bound entries.
func EmptyAssessment() Assessment { return Assessment{} }

// NewAssessment binds structural adapter evidence to the current release and tracker configuration.
func NewAssessment(meta api.DuplicateSubject, cfg config.Config, evidence []AssessmentEvidence) Assessment {
	assessment := Assessment{entries: make(map[string]assessmentEntry, len(evidence)), assessed: true}
	for _, item := range evidence {
		tracker := normalizeTracker(item.Tracker)
		if tracker == "" {
			continue
		}
		disposition := item.Disposition
		if disposition != DispositionResolved && disposition != DispositionNotRun && disposition != DispositionFailed {
			disposition = DispositionFailed
		}
		assessment.entries[tracker] = newAssessmentEntry(meta, cfg, tracker, disposition, item.Code, item.HasDupes, item.Match, item.Raw)
	}
	return assessment
}

// IsEmpty reports whether no tracker decisions are bound.
func (a Assessment) IsEmpty() bool { return len(a.entries) == 0 }

// Clone returns an independent immutable assessment value.
func (a Assessment) Clone() Assessment {
	if len(a.entries) == 0 && !a.assessed {
		return Assessment{}
	}
	out := Assessment{entries: make(map[string]assessmentEntry, len(a.entries)), assessed: a.assessed}
	for tracker, entry := range a.entries {
		out.entries[tracker] = cloneAssessmentEntry(entry)
	}
	return out
}

// Merge replaces selected tracker entries represented by delta, clearing prior authorization for rechecked trackers.
// Missing delta entries preserve prior state; a completed structural check represents a clear result with an entry.
func (a Assessment) Merge(delta Assessment, selected []string) Assessment {
	out := a.Clone()
	out.assessed = out.assessed || delta.assessed || len(selected) > 0
	if out.entries == nil {
		out.entries = make(map[string]assessmentEntry)
	}
	for _, tracker := range dedupeTrackers(selected) {
		entry, ok := delta.entries[tracker]
		if !ok {
			continue
		}
		entry.authorization = AuthorizationNone
		entry.verdict = defaultVerdict(entry)
		out.entries[tracker] = cloneAssessmentEntry(entry)
	}
	return out
}

// RetainValid drops entries whose Prepared release or effective tracker config binding changed.
func (a Assessment) RetainValid(meta api.DuplicateSubject, cfg config.Config) Assessment {
	out := Assessment{entries: make(map[string]assessmentEntry), assessed: a.assessed}
	for tracker, entry := range a.entries {
		if entry.binding != assessmentBinding(meta, tracker, effectiveTrackerConfig(cfg, tracker)) {
			continue
		}
		out.entries[tracker] = cloneAssessmentEntry(entry)
	}
	if len(out.entries) == 0 && !out.assessed {
		return Assessment{}
	}
	return out
}

// Authorize validates and binds explicit overrides/waivers to current outcomes.
func (a Assessment) Authorize(meta api.DuplicateSubject, cfg config.Config, trackerNames []string) (Assessment, error) {
	out := a.Clone()
	for _, tracker := range dedupeTrackers(trackerNames) {
		entry, ok := out.entries[tracker]
		if !ok {
			return Assessment{}, fmt.Errorf("duplicate authorization rejected: tracker %s has no current assessment", tracker)
		}
		if entry.binding != assessmentBinding(meta, tracker, effectiveTrackerConfig(cfg, tracker)) {
			return Assessment{}, fmt.Errorf("duplicate authorization rejected: tracker %s assessment is stale", tracker)
		}
		switch {
		case entry.disposition == DispositionResolved && entry.match.MatchedReason == "in_client":
			return Assessment{}, fmt.Errorf("duplicate authorization rejected: tracker %s is already in client", tracker)
		case entry.disposition == DispositionResolved && entry.verdict == VerdictBlocked:
			entry.authorization = AuthorizationOverride
			entry.verdict = VerdictOverridden
		case entry.disposition == DispositionNotRun || entry.disposition == DispositionFailed:
			entry.authorization = AuthorizationWaiver
			entry.verdict = VerdictWaived
		default:
			return Assessment{}, fmt.Errorf("duplicate authorization rejected: tracker %s outcome is not overrideable", tracker)
		}
		out.entries[tracker] = entry
	}
	return out, nil
}

// Decision returns one defensive tracker decision.
func (a Assessment) Decision(tracker string) (Decision, bool) {
	entry, ok := a.entries[normalizeTracker(tracker)]
	if !ok {
		return Decision{}, false
	}
	return Decision{
		Tracker:       entry.tracker,
		Disposition:   entry.disposition,
		Code:          entry.code,
		Verdict:       entry.verdict,
		Authorization: entry.authorization,
		Match:         clonePrivateMatch(entry.match),
	}, true
}

// Apply projects current decisions into the duplicate subject's block/cross-seed fields.
func (a Assessment) Apply(meta *api.DuplicateSubject) {
	if meta == nil || !a.assessed || len(a.entries) == 0 {
		return
	}
	blocked := removeDupeBlocks(meta.BlockedTrackers)
	crossSeeds := make([]api.UploadedTorrent, 0)
	for _, tracker := range sortedAssessmentTrackers(a.entries) {
		entry := a.entries[tracker]
		if entry.verdict == VerdictBlocked {
			blocked = addDupeBlock(blocked, tracker)
		}
		if entry.disposition != DispositionResolved || entry.match.MatchedReason == "in_client" || entry.authorization == AuthorizationOverride {
			continue
		}
		download := strings.TrimSpace(entry.match.MatchedDownload)
		if download == "" {
			continue
		}
		crossSeeds = append(crossSeeds, api.UploadedTorrent{
			Tracker:     tracker,
			TorrentID:   strings.TrimSpace(entry.match.MatchedID),
			DownloadURL: download,
			TorrentURL:  strings.TrimSpace(entry.match.MatchedLink),
		})
	}
	meta.BlockedTrackers = blocked
	meta.CrossSeedTorrents = crossSeeds
}

func newAssessmentEntry(
	meta api.DuplicateSubject,
	cfg config.Config,
	tracker string,
	disposition Disposition,
	code string,
	hasDupes bool,
	match api.DupeMatch,
	raw []api.DupeEntry,
) assessmentEntry {
	entry := assessmentEntry{
		tracker:     normalizeTracker(tracker),
		disposition: disposition,
		code:        strings.TrimSpace(code),
		hasDupes:    hasDupes,
		match:       clonePrivateMatch(match),
		privateRaw:  cloneEntries(raw),
	}
	entry.binding = assessmentBinding(meta, entry.tracker, effectiveTrackerConfig(cfg, entry.tracker))
	entry.outcomeID = outcomeIdentity(entry)
	entry.verdict = defaultVerdict(entry)
	return entry
}

func defaultVerdict(entry assessmentEntry) Verdict {
	if entry.disposition == DispositionResolved && !entry.hasDupes && entry.match.MatchedReason != "in_client" {
		return VerdictClear
	}
	return VerdictBlocked
}

func assessmentBinding(meta api.DuplicateSubject, tracker string, trackerConfig config.TrackerConfig) string {
	bound := struct {
		SourcePath           string
		SourceSize           int64
		VideoPath            string
		FileList             []string
		Filename             string
		SceneName            string
		ReleaseName          string
		Release              api.ReleaseInfo
		ReleaseNameOverrides api.ReleaseNameOverrides
		Identity             api.ExternalIdentity
		ProviderMetadata     api.SourceScopedMetadata
		TrackerIDs           map[string]string
		DiscType             string
		Type                 string
		Source               string
		Tag                  string
		HDR                  string
		UHD                  string
		VideoEncode          string
		SeasonInt            int
		EpisodeInt           int
		SeasonStr            string
		EpisodeStr           string
		TVPack               bool
		Anime                bool
		InClient             bool
		RuleFailures         []api.RuleFailure
		Tracker              string
		Config               config.TrackerConfig
	}{
		SourcePath:           strings.TrimSpace(meta.SourcePath),
		SourceSize:           meta.SourceSize,
		VideoPath:            strings.TrimSpace(meta.VideoPath),
		FileList:             append([]string(nil), meta.FileList...),
		Filename:             strings.TrimSpace(meta.Filename),
		SceneName:            strings.TrimSpace(meta.SceneName),
		ReleaseName:          strings.TrimSpace(meta.ReleaseName),
		Release:              meta.Release,
		ReleaseNameOverrides: meta.ReleaseNameOverrides,
		Identity:             meta.Identity,
		ProviderMetadata:     meta.ProviderMetadata,
		TrackerIDs:           cloneStringMap(meta.TrackerIDs),
		DiscType:             strings.TrimSpace(meta.DiscType),
		Type:                 strings.TrimSpace(meta.Type),
		Source:               strings.TrimSpace(meta.Source),
		Tag:                  strings.TrimSpace(meta.Tag),
		HDR:                  strings.TrimSpace(meta.HDR),
		UHD:                  strings.TrimSpace(meta.UHD),
		VideoEncode:          strings.TrimSpace(meta.VideoEncode),
		SeasonInt:            meta.SeasonInt,
		EpisodeInt:           meta.EpisodeInt,
		SeasonStr:            strings.TrimSpace(meta.SeasonStr),
		EpisodeStr:           strings.TrimSpace(meta.EpisodeStr),
		TVPack:               meta.TVPack,
		Anime:                meta.Anime,
		InClient:             containsTracker(meta.MatchedTrackers, tracker),
		RuleFailures:         append([]api.RuleFailure(nil), trackerRuleFailures(meta, tracker)...),
		Tracker:              normalizeTracker(tracker),
		Config:               trackerConfig,
	}
	encoded, err := json.Marshal(bound)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	maps.Copy(out, input)
	return out
}

func trackerRuleFailures(meta api.DuplicateSubject, tracker string) []api.RuleFailure {
	for name, failures := range meta.TrackerRuleFailures {
		if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(tracker)) {
			return failures
		}
	}
	return nil
}

func outcomeIdentity(entry assessmentEntry) string {
	bound := struct {
		Disposition Disposition
		Code        string
		HasDupes    bool
		Match       api.DupeMatch
		Raw         []api.DupeEntry
	}{entry.disposition, entry.code, entry.hasDupes, entry.match, entry.privateRaw}
	encoded, err := json.Marshal(bound)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func cloneAssessmentEntry(entry assessmentEntry) assessmentEntry {
	entry.match = clonePrivateMatch(entry.match)
	entry.privateRaw = cloneEntries(entry.privateRaw)
	return entry
}

func clonePrivateMatch(match api.DupeMatch) api.DupeMatch {
	match.MatchedEpisodeIDs = append([]api.DupeEpisodeMatch(nil), match.MatchedEpisodeIDs...)
	return match
}

func removeDupeBlocks(input map[string][]api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	out := make(map[string][]api.TrackerBlockReason, len(input))
	for tracker, reasons := range input {
		for _, reason := range reasons {
			if reason != api.TrackerBlockReasonDupe {
				out[normalizeTracker(tracker)] = append(out[normalizeTracker(tracker)], reason)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func addDupeBlock(blocked map[string][]api.TrackerBlockReason, tracker string) map[string][]api.TrackerBlockReason {
	if blocked == nil {
		blocked = make(map[string][]api.TrackerBlockReason)
	}
	if slices.Contains(blocked[tracker], api.TrackerBlockReasonDupe) {
		return blocked
	}
	blocked[tracker] = append(blocked[tracker], api.TrackerBlockReasonDupe)
	return blocked
}

func sortedAssessmentTrackers(entries map[string]assessmentEntry) []string {
	trackers := make([]string, 0, len(entries))
	for tracker := range entries {
		trackers = append(trackers, tracker)
	}
	sortStrings(trackers)
	return trackers
}

func sortStrings(values []string) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current] < values[current-1]; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}
