// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// DupeReleaseEvidence retains verified source facts, independently of naming and upload flags.
// Empty values are unknown. InternalStatus is internal or non_internal; DiscForm is custom or unmodified.
type DupeReleaseEvidence struct {
	PersonalRelease bool   `json:"personalRelease,omitempty"`
	InternalStatus  string `json:"internalStatus,omitempty"`
	Group           string `json:"group,omitempty"`
	BackCatalogue   string `json:"backCatalogue,omitempty"`
	HDRLayerSource  string `json:"hdrLayerSource,omitempty"`
	DiscIdentity    string `json:"discIdentity,omitempty"`
	DiscForm        string `json:"discForm,omitempty"`
}

// DupeComparisonEvidence binds an explicitly reviewed comparison to the exact existing candidate.
// Quality is equivalent, superior or inferior from the proposed release's perspective.
type DupeComparisonEvidence struct {
	CandidateID          string              `json:"candidateId,omitempty"`
	CandidateFingerprint WorkflowFingerprint `json:"candidateFingerprint,omitempty"`
	Candidate            DupeReleaseEvidence `json:"candidate"`
	VisualImprovement    bool                `json:"visualImprovement,omitempty"`
	MeaningfulBenefit    string              `json:"meaningfulBenefit,omitempty"`
	Quality              string              `json:"quality,omitempty"`
	DefectImproved       bool                `json:"defectImproved,omitempty"`
	Replacement          string              `json:"replacement,omitempty"`
	Reviewed             bool                `json:"reviewed,omitempty"`
	Source               string              `json:"source,omitempty"`
}

// DupeEvidenceDecision is a tracker-owned evidence overlay; an empty relation preserves the ordinary result.
type DupeEvidenceDecision struct {
	Relation DupeRelation
	Reason   string
}

// DupeEvidenceContext supplies normalized comparison facts and the ordinary policy result.
type DupeEvidenceContext struct {
	// SlotEvidenceComplete requires authoritative work, content, resolution, media kind and HDR facts.
	SlotEvidenceComplete bool
	Target               TrackerDuplicateTarget
	Candidate            TrackerDuplicateTarget
	CandidateID          string
	CandidateFingerprint WorkflowFingerprint
	Trumpable            bool
	Relation             DupeRelation
	Reason               string
}
