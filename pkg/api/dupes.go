// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"time"
)

// DupeEntry is one backend adapter's private duplicate-search hit.
type DupeEntry struct {
	Name          string
	SizeBytes     int64
	SizeKnown     bool
	SizeText      string
	Files         []string
	FileCount     int
	Trumpable     bool
	Link          string
	Download      string
	Flags         []string
	FlagsPresent  bool
	FlagsComplete bool
	HDR           HDRFacts
	ID            string
	Type          string
	CanonicalType string
	Res           string
	Category      string
	Source        string
	Codec         string
	Container     string
	Provider      string
	Group         string
	Edition       string
	Region        string
	ThreeD        string
	Repack        string
	Attributes    map[string]string
	Season        int
	Episode       int
	Pack          bool
	Internal      bool
	BDInfo        string
	Description   string
}

// DupeRelation is one directional proposed-to-candidate relationship.
type DupeRelation string

const (
	DupeRelationExactDuplicate       DupeRelation = "exact_duplicate"
	DupeRelationExistingPreferred    DupeRelation = "existing_preferred"
	DupeRelationSameSlot             DupeRelation = "same_slot"
	DupeRelationProposedTrumps       DupeRelation = "proposed_trumps"
	DupeRelationCoexists             DupeRelation = "coexists"
	DupeRelationManualReview         DupeRelation = "manual_review"
	DupeRelationInsufficientEvidence DupeRelation = "insufficient_evidence"
)

// DupeReason is one stable public-safe evaluator reason.
type DupeReason struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// DupeCandidateEvaluation is one sanitized per-candidate decision.
type DupeCandidateEvaluation struct {
	ID             string            `json:"id,omitempty"`
	Name           string            `json:"name"`
	Link           string            `json:"link,omitempty"`
	SizeBytes      int64             `json:"sizeBytes,omitempty"`
	Relation       DupeRelation      `json:"relation"`
	Reasons        []DupeReason      `json:"reasons"`
	Flags          []string          `json:"flags,omitempty"`
	HDR            HDRFacts          `json:"hdr"`
	Category       string            `json:"category,omitempty"`
	Type           string            `json:"type,omitempty"`
	Resolution     string            `json:"resolution,omitempty"`
	Source         string            `json:"source,omitempty"`
	Codec          string            `json:"codec,omitempty"`
	Container      string            `json:"container,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	Group          string            `json:"group,omitempty"`
	Edition        string            `json:"edition,omitempty"`
	Region         string            `json:"region,omitempty"`
	ThreeD         string            `json:"threeD,omitempty"`
	Repack         string            `json:"repack,omitempty"`
	Season         int               `json:"season,omitempty"`
	Episode        int               `json:"episode,omitempty"`
	Date           string            `json:"date,omitempty"`
	Pack           bool              `json:"pack"`
	Internal       bool              `json:"internal"`
	Trumpable      bool              `json:"trumpable"`
	EvidenceStatus HDREvidenceStatus `json:"evidenceStatus"`
}

// DupeSearchEvidence is the safe completion state of one tracker search.
type DupeSearchEvidence struct {
	Complete       bool     `json:"complete"`
	Pages          int      `json:"pages"`
	CandidateCount int      `json:"candidateCount"`
	Scope          string   `json:"scope,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
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

// DupeMatch is private retained authority for the selected candidate and any
// cross-seed injection evidence.
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

// DupeCheckResult is the sanitized duplicate-search outcome for one tracker.
// Private adapter payloads and download authority are retained separately.
type DupeCheckResult struct {
	Tracker               string
	CanonicalReleaseName  string
	UploadReleaseName     string
	ProjectionFingerprint WorkflowFingerprint
	CriteriaFingerprint   WorkflowFingerprint
	ProjectionStatus      ReadinessStatus
	PolicyID              string
	PolicyFingerprint     WorkflowFingerprint
	TargetFingerprint     WorkflowFingerprint
	SearchFingerprint     WorkflowFingerprint
	Search                DupeSearchEvidence
	Evaluations           []DupeCandidateEvaluation
	HasDupes              bool
	Notes                 []string
	Skipped               bool
	SkipReason            string
	// SkipCode is a stable machine-readable skip reason.
	SkipCode  string
	Status    string
	Error     string
	CheckedAt time.Time `ts_type:"string"`
}

// DupeCheckSummary groups duplicate-search results for one prepared source
// path.
type DupeCheckSummary struct {
	SourcePath string
	Results    []DupeCheckResult
	Notes      []string
}
