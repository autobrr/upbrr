// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"time"
)

// MetadataRequirementField identifies one tracker-declared metadata fact.
type MetadataRequirementField string

// CorrectionConfirmation explains which retained facts need confirmation after
// source identity changes. Revision binds the correction patch to this record.
type CorrectionConfirmation struct {
	Revision         uint64                             `json:"revision"`
	Fields           []CorrectionField                  `json:"fields"`
	PreviousBindings map[CorrectionField]ContentBinding `json:"previousBindings"`
	CurrentBinding   ContentBinding                     `json:"currentBinding"`
}

// MetadataRequirementScope limits a metadata requirement to a canonical category.
type MetadataRequirementScope string

const (
	MetadataRequirementScopeAny   MetadataRequirementScope = "any"
	MetadataRequirementScopeMovie MetadataRequirementScope = "movie"
	MetadataRequirementScopeTV    MetadataRequirementScope = "tv"
)

// MetadataRequirement describes one AnyOf group consumed during canonical preparation.
type MetadataRequirement struct {
	Scope       MetadataRequirementScope   `json:"scope"`
	AnyOf       []MetadataRequirementField `json:"anyOf"`
	Disposition RuleDisposition            `json:"disposition"`
}

// MetadataRequirementSet is the tracker-ID-free normalized union of selected tracker demands.
type MetadataRequirementSet struct {
	Version      string                `json:"version"`
	Requirements []MetadataRequirement `json:"requirements,omitempty"`
}

// Normalize returns a deterministic demand set suitable for compatibility fingerprints.
func (s MetadataRequirementSet) Normalize() (MetadataRequirementSet, error) {
	normalized := MetadataRequirementSet{Version: strings.TrimSpace(s.Version)}
	if len(s.Requirements) == 0 {
		return normalized, nil
	}
	normalized.Requirements = make([]MetadataRequirement, 0, len(s.Requirements))
	for _, requirement := range s.Requirements {
		requirement.Scope = MetadataRequirementScope(strings.ToLower(strings.TrimSpace(string(requirement.Scope))))
		switch requirement.Scope {
		case "", MetadataRequirementScopeAny:
			requirement.Scope = MetadataRequirementScopeAny
		case MetadataRequirementScopeMovie, MetadataRequirementScopeTV:
		default:
			return MetadataRequirementSet{}, errors.New("unsupported metadata requirement scope")
		}
		requirement.Disposition = NormalizeRuleDisposition(requirement.Disposition)
		requirement.AnyOf = normalizeMetadataRequirementFields(requirement.AnyOf)
		if len(requirement.AnyOf) == 0 {
			return MetadataRequirementSet{}, errors.New("metadata requirement requires at least one field")
		}
		normalized.Requirements = append(normalized.Requirements, requirement)
	}
	slices.SortFunc(normalized.Requirements, func(left, right MetadataRequirement) int {
		if result := cmp.Compare(left.Scope, right.Scope); result != 0 {
			return result
		}
		if result := cmp.Compare(left.Disposition, right.Disposition); result != 0 {
			return result
		}
		return cmp.Compare(strings.Join(metadataRequirementStrings(left.AnyOf), "\x00"), strings.Join(metadataRequirementStrings(right.AnyOf), "\x00"))
	})
	normalized.Requirements = slices.CompactFunc(normalized.Requirements, func(left, right MetadataRequirement) bool {
		return left.Scope == right.Scope && left.Disposition == right.Disposition && slices.Equal(left.AnyOf, right.AnyOf)
	})
	return normalized, nil
}

func normalizeMetadataRequirementFields(fields []MetadataRequirementField) []MetadataRequirementField {
	normalized := make([]MetadataRequirementField, 0, len(fields))
	for _, field := range fields {
		field = MetadataRequirementField(strings.TrimSpace(string(field)))
		if field != "" && !slices.Contains(normalized, field) {
			normalized = append(normalized, field)
		}
	}
	slices.Sort(normalized)
	return normalized
}

func metadataRequirementStrings(fields []MetadataRequirementField) []string {
	values := make([]string, len(fields))
	for index := range fields {
		values[index] = string(fields[index])
	}
	return values
}

// InputReadinessFieldStatus describes one evaluated Input field.
type InputReadinessFieldStatus string

const (
	InputReadinessFieldReady   InputReadinessFieldStatus = "ready"
	InputReadinessFieldMissing InputReadinessFieldStatus = "missing"
	InputReadinessFieldInvalid InputReadinessFieldStatus = "invalid"
)

// InputReadinessFieldOutcome is one safe local-input outcome. Key is a stable
// evaluator schema key, while CorrectionField identifies a fact correction when applicable.
type InputReadinessFieldOutcome struct {
	Key             string                    `json:"key"`
	CorrectionField *CorrectionField          `json:"correctionField,omitempty"`
	TrackerIDs      []TrackerID               `json:"trackerIds,omitempty"`
	Status          InputReadinessFieldStatus `json:"status"`
	Disposition     RuleDisposition           `json:"disposition"`
	Message         string                    `json:"message,omitempty"`
}

// InputReadinessEvaluation is the pure, local result returned by the tracker policy owner.
type InputReadinessEvaluation struct {
	Schemas                 []TrackerQuestionnaire       `json:"schemas,omitempty"`
	RequirementsFingerprint WorkflowFingerprint          `json:"requirementsFingerprint"`
	Fields                  []InputReadinessFieldOutcome `json:"fields,omitempty"`
	RequiredActions         []RequiredAction             `json:"requiredActions,omitempty"`
}

// InputReadinessSnapshot records local readiness before tracker projection,
// authentication, duplicate checks, or other remote work.
type InputReadinessSnapshot struct {
	Schemas                 []TrackerQuestionnaire            `json:"schemas,omitempty"`
	ID                      InputReadinessSnapshotID          `json:"id"`
	WorkflowID              WorkflowID                        `json:"workflowId"`
	Revision                WorkflowRevision                  `json:"revision"`
	Release                 ReleaseRef                        `json:"release"`
	FactInstructions        ReleaseFactInstructionSnapshotRef `json:"factInstructions"`
	CorrectionRevision      uint64                            `json:"correctionRevision,omitempty"`
	SelectedTrackerIDs      []TrackerID                       `json:"selectedTrackerIds,omitempty"`
	RequirementsFingerprint WorkflowFingerprint               `json:"requirementsFingerprint"`
	Fields                  []InputReadinessFieldOutcome      `json:"fields,omitempty"`
	RequiredActions         []RequiredAction                  `json:"requiredActions,omitempty"`
	Status                  StageStatus                       `json:"status"`
	CreatedAt               time.Time                         `json:"createdAt" ts_type:"string"`
}

// Validate verifies snapshot identity and its exact local-input bindings.
func (s InputReadinessSnapshot) Validate() error {
	if err := validateSnapshotIdentity(string(s.ID), s.Revision, s.CreatedAt); err != nil {
		return errors.New("input readiness requires valid identity and timestamp")
	}
	if err := validateWorkflowIdentity(s.WorkflowID, s.Revision); err != nil {
		return errors.New("input readiness requires a workflow")
	}
	if strings.TrimSpace(s.Release.SourcePath) == "" || s.Release.Generation == 0 {
		return errors.New("input readiness requires an exact release")
	}
	if err := validateTypedRef(s.FactInstructions.ID, s.FactInstructions.Revision, "fact instructions"); err != nil {
		return err
	}
	if err := validateWorkflowFingerprint(s.RequirementsFingerprint); err != nil {
		return errors.New("input readiness requires a requirements fingerprint")
	}
	for _, field := range s.Fields {
		if strings.TrimSpace(field.Key) == "" {
			return errors.New("input readiness field key is required")
		}
		switch field.Status {
		case InputReadinessFieldReady, InputReadinessFieldMissing, InputReadinessFieldInvalid:
		default:
			return errors.New("input readiness field status is invalid")
		}
	}
	return nil
}

// Clone returns a detached readiness snapshot.
func (s InputReadinessSnapshot) Clone() (InputReadinessSnapshot, error) { return cloneWorkflowValue(s) }
