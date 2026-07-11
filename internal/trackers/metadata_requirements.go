// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// MetadataField identifies metadata that a tracker policy can require.
type MetadataField string

const (
	// MetadataFieldTMDBID represents a positive TMDB identifier.
	MetadataFieldTMDBID MetadataField = "tmdb_id"
	// MetadataFieldIMDBID represents a positive IMDb identifier.
	MetadataFieldIMDBID MetadataField = "imdb_id"
	// MetadataFieldTVDBID represents a positive TVDB identifier.
	MetadataFieldTVDBID MetadataField = "tvdb_id"
	// MetadataFieldTVmazeID represents a positive TVmaze identifier.
	MetadataFieldTVmazeID MetadataField = "tvmaze_id"
	// MetadataFieldTVDBTitle represents a non-empty title from matching TVDB metadata.
	MetadataFieldTVDBTitle MetadataField = "tvdb_title"
)

// MetadataScope limits a metadata requirement to a content category.
type MetadataScope string

const (
	// MetadataScopeAny applies regardless of content category.
	MetadataScopeAny MetadataScope = "any"
	// MetadataScopeMovie applies only to movie content.
	MetadataScopeMovie MetadataScope = "movie"
	// MetadataScopeTV applies only to TV content.
	MetadataScopeTV MetadataScope = "tv"
)

// MetadataRequirement defines one group of alternative metadata fields.
type MetadataRequirement struct {
	// Scope selects the content category to which the requirement applies.
	Scope MetadataScope
	// AnyOf is satisfied when at least one listed field is present and current.
	AnyOf []MetadataField
	// Severity defaults to blocking when empty or unrecognized.
	Severity api.RuleFailureSeverity
}

// TrackerMetadataPolicy defines declarative metadata requirements for a tracker.
type TrackerMetadataPolicy struct {
	// RequireKnownCategory blocks evaluation when content is neither movie nor TV.
	RequireKnownCategory bool
	// Requirements are evaluated in order after category resolution.
	Requirements []MetadataRequirement
}

var trackerMetadataPolicies = map[string]TrackerMetadataPolicy{
	"PTP": {Requirements: []MetadataRequirement{{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldIMDBID}, Severity: api.RuleFailureSeverityWarning}}},
	"HDB": {RequireKnownCategory: true, Requirements: []MetadataRequirement{
		{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldIMDBID}},
		{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldIMDBID, MetadataFieldTVDBID}},
	}},
	"NBL": {RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldTVmazeID}}}},
	"ANT": {RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldTMDBID}}}},
	"BHD": {RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldTMDBID}}}},
	"MTV": {RequireKnownCategory: true, Requirements: []MetadataRequirement{
		{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldTMDBID, MetadataFieldIMDBID}},
		{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldTVDBTitle}},
	}},
	"BTN": {RequireKnownCategory: true, Requirements: []MetadataRequirement{{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldIMDBID, MetadataFieldTVDBID}}}},
	"AR": {RequireKnownCategory: true, Requirements: []MetadataRequirement{
		{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldTMDBID, MetadataFieldIMDBID}},
		{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldTMDBID, MetadataFieldIMDBID, MetadataFieldTVDBID, MetadataFieldTVmazeID}},
	}},
	"AZ":  multiIDMetadataPolicy(),
	"CZ":  multiIDMetadataPolicy(),
	"PHD": multiIDMetadataPolicy(),
	"CZT": {Requirements: []MetadataRequirement{{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldIMDBID}}}},
}

// multiIDMetadataPolicy returns the shared movie and TV policy for trackers
// that accept several provider identifiers.
func multiIDMetadataPolicy() TrackerMetadataPolicy {
	return TrackerMetadataPolicy{RequireKnownCategory: true, Requirements: []MetadataRequirement{
		{Scope: MetadataScopeMovie, AnyOf: []MetadataField{MetadataFieldTMDBID, MetadataFieldIMDBID}},
		{Scope: MetadataScopeTV, AnyOf: []MetadataField{MetadataFieldTMDBID, MetadataFieldIMDBID, MetadataFieldTVDBID}},
	}}
}

// MetadataPolicyFor returns an independent copy of a tracker's metadata policy.
// Tracker names are case-insensitive and whitespace-trimmed. Known Unit3D
// trackers without an explicit policy require a current TMDB ID.
func MetadataPolicyFor(tracker string) (TrackerMetadataPolicy, bool) {
	name := strings.ToUpper(strings.TrimSpace(tracker))
	policy, ok := trackerMetadataPolicies[name]
	if !ok && IsUnit3DTracker(name) {
		policy = TrackerMetadataPolicy{Requirements: []MetadataRequirement{{Scope: MetadataScopeAny, AnyOf: []MetadataField{MetadataFieldTMDBID}}}}
		ok = true
	}
	if !ok {
		return TrackerMetadataPolicy{}, false
	}
	policy.Requirements = slices.Clone(policy.Requirements)
	for i := range policy.Requirements {
		policy.Requirements[i].AnyOf = slices.Clone(policy.Requirements[i].AnyOf)
	}
	return policy, true
}

// evaluateMetadataRequirements returns policy results and whether the tracker
// has a metadata policy. An evaluated policy may produce a non-nil empty slice.
func evaluateMetadataRequirements(tracker string, meta api.PreparedMetadata) ([]api.RuleFailure, bool) {
	policy, ok := MetadataPolicyFor(tracker)
	if !ok {
		return nil, false
	}

	category := MetadataScope(strings.ToLower(strings.TrimSpace(resolveCategory(meta))))
	if policy.RequireKnownCategory && category != MetadataScopeMovie && category != MetadataScopeTV {
		return []api.RuleFailure{{
			Rule:     "require_metadata_category",
			Reason:   "missing category required to select tracker metadata requirements",
			Severity: api.RuleFailureSeverityBlocking,
		}}, true
	}

	failures := make([]api.RuleFailure, 0)
	for _, requirement := range policy.Requirements {
		if requirement.Scope != MetadataScopeAny && requirement.Scope != category {
			continue
		}
		if metadataRequirementPresent(requirement.AnyOf, meta) {
			continue
		}
		severity := api.NormalizeRuleFailureSeverity(requirement.Severity)
		rule := "require_metadata_id"
		reason := "missing required " + metadataFieldList(requirement.AnyOf)
		if slices.Contains(requirement.AnyOf, MetadataFieldTVDBTitle) {
			rule = "require_tvdb_title"
			reason = "missing required TVDB series title for MTV TV upload"
		} else if severity == api.RuleFailureSeverityWarning {
			reason = "missing recommended IMDb ID; PTP upload remains allowed"
		}
		failures = append(failures, api.RuleFailure{Rule: rule, Reason: reason, Severity: severity})
	}
	return failures, true
}

// metadataRequirementPresent reports whether any alternative field satisfies
// a requirement.
func metadataRequirementPresent(fields []MetadataField, meta api.PreparedMetadata) bool {
	for _, field := range fields {
		if metadataFieldPresent(field, meta) {
			return true
		}
	}
	return false
}

// metadataFieldPresent accepts only IDs and provider data scoped to the current
// source; an empty scope remains compatible with legacy unscoped metadata.
func metadataFieldPresent(field MetadataField, meta api.PreparedMetadata) bool {
	idsCurrent := sourceMatches(meta.ExternalIDs.SourcePath, meta.SourcePath)
	switch field {
	case MetadataFieldTMDBID:
		return idsCurrent && meta.ExternalIDs.TMDBID > 0
	case MetadataFieldIMDBID:
		return idsCurrent && meta.ExternalIDs.IMDBID > 0
	case MetadataFieldTVDBID:
		return idsCurrent && meta.ExternalIDs.TVDBID > 0
	case MetadataFieldTVmazeID:
		return idsCurrent && meta.ExternalIDs.TVmazeID > 0
	case MetadataFieldTVDBTitle:
		if !idsCurrent || meta.ExternalIDs.TVDBID <= 0 || !sourceMatches(meta.ExternalMetadata.SourcePath, meta.SourcePath) || meta.ExternalMetadata.TVDB == nil {
			return false
		}
		if meta.ExternalMetadata.TVDB.TVDBID <= 0 || meta.ExternalMetadata.TVDB.TVDBID != meta.ExternalIDs.TVDBID {
			return false
		}
		return strings.TrimSpace(meta.ExternalMetadata.TVDB.NameEnglish) != "" || strings.TrimSpace(meta.ExternalMetadata.TVDB.Name) != ""
	}
	return false
}

// sourceMatches reports whether data is unscoped or belongs to the current
// source. Path comparison is case-insensitive to match persisted source keys.
func sourceMatches(scopedPath, currentPath string) bool {
	trimmed := strings.TrimSpace(scopedPath)
	return trimmed == "" || strings.EqualFold(trimmed, strings.TrimSpace(currentPath))
}

// metadataFieldList formats alternative field names for a rule-result reason.
func metadataFieldList(fields []MetadataField) string {
	labels := make([]string, 0, len(fields))
	for _, field := range fields {
		switch field {
		case MetadataFieldTMDBID:
			labels = append(labels, "TMDB ID")
		case MetadataFieldIMDBID:
			labels = append(labels, "IMDb ID")
		case MetadataFieldTVDBID:
			labels = append(labels, "TVDB ID")
		case MetadataFieldTVmazeID:
			labels = append(labels, "TVmaze ID")
		case MetadataFieldTVDBTitle:
			labels = append(labels, "TVDB series title")
		}
	}
	if len(labels) == 0 {
		return "metadata"
	}
	if len(labels) == 1 {
		return labels[0]
	}
	return fmt.Sprintf("%s or %s", strings.Join(labels[:len(labels)-1], ", "), labels[len(labels)-1])
}
