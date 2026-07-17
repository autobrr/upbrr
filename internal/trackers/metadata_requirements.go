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
	// MetadataFieldTMDBIDOnly represents a positive TMDB identifier without requiring fetched metadata.
	MetadataFieldTMDBIDOnly MetadataField = "tmdb_id_only"
	// MetadataFieldIMDBIDOnly represents a positive IMDb identifier without requiring fetched metadata.
	MetadataFieldIMDBIDOnly MetadataField = "imdb_id_only"
	// MetadataFieldTVDBIDOnly represents a positive TVDB identifier without requiring fetched metadata.
	MetadataFieldTVDBIDOnly MetadataField = "tvdb_id_only"
	// MetadataFieldTVmazeIDOnly represents a positive TVmaze identifier without requiring fetched metadata.
	MetadataFieldTVmazeIDOnly MetadataField = "tvmaze_id_only"
	// MetadataFieldTMDB represents fetched TMDB data matching the canonical ID.
	MetadataFieldTMDB MetadataField = "tmdb"
	// MetadataFieldIMDB represents fetched IMDb data matching the canonical ID.
	MetadataFieldIMDB MetadataField = "imdb"
	// MetadataFieldTVDB represents fetched TVDB data matching the canonical ID.
	MetadataFieldTVDB MetadataField = "tvdb"
	// MetadataFieldTVmaze represents fetched TVmaze data matching the canonical ID.
	MetadataFieldTVmaze MetadataField = "tvmaze"
	// MetadataFieldTVDBTitle represents a non-empty title from matching TVDB metadata.
	MetadataFieldTVDBTitle MetadataField = "tvdb_title"
	// MetadataFieldPoster represents poster artwork from matching provider metadata.
	MetadataFieldPoster MetadataField = "poster"
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
	// Disposition defaults to waivable for legacy empty values.
	Disposition api.RuleDisposition
}

// TrackerMetadataPolicy defines declarative metadata requirements for a tracker.
type TrackerMetadataPolicy struct {
	// RequireKnownCategory blocks evaluation when content is neither movie nor TV.
	RequireKnownCategory bool
	// Requirements are evaluated in order after category resolution.
	Requirements []MetadataRequirement
}

func cloneMetadataPolicy(policy TrackerMetadataPolicy) TrackerMetadataPolicy {
	policy.Requirements = slices.Clone(policy.Requirements)
	for i := range policy.Requirements {
		policy.Requirements[i].AnyOf = slices.Clone(policy.Requirements[i].AnyOf)
	}
	return policy
}

func evaluateMetadataRequirementsWithRegistry(registry *Registry, tracker string, meta api.RuleSubject) ([]api.RuleFailure, bool) {
	policy, ok := registry.LookupMetadataPolicy(tracker)
	if !ok {
		return nil, false
	}

	category := MetadataScope(strings.ToLower(strings.TrimSpace(resolveCategory(meta))))
	if policy.RequireKnownCategory && category != MetadataScopeMovie && category != MetadataScopeTV {
		return []api.RuleFailure{{
			Rule:        "require_metadata_category",
			Reason:      "missing category required to select tracker metadata requirements",
			Disposition: api.RuleDispositionWaivable,
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
		disposition := api.NormalizeRuleDisposition(requirement.Disposition)
		rule := "require_metadata_id"
		reason := "missing required " + metadataFieldList(requirement.AnyOf)
		switch {
		case slices.Contains(requirement.AnyOf, MetadataFieldTVDBTitle):
			rule = "require_tvdb_title"
			reason = "missing required TVDB series title for MTV TV upload"
		case slices.Contains(requirement.AnyOf, MetadataFieldPoster):
			rule = "require_metadata_poster"
			reason = "missing required metadata poster"
		case disposition == api.RuleDispositionAdvisory:
			reason = "missing recommended IMDb ID; PTP upload remains allowed"
		}
		failures = append(failures, api.RuleFailure{
			Rule:        rule,
			Reason:      reason,
			Disposition: disposition,
		})
	}
	return failures, true
}

// metadataRequirementPresent reports whether any alternative field satisfies
// a requirement.
func metadataRequirementPresent(fields []MetadataField, meta api.RuleSubject) bool {
	for _, field := range fields {
		if metadataFieldPresent(field, meta) {
			return true
		}
	}
	return false
}

// metadataFieldPresent accepts only IDs and provider data scoped to the current
// source; an empty scope remains compatible with legacy unscoped metadata.
func metadataFieldPresent(field MetadataField, meta api.RuleSubject) bool {
	idsCurrent := sourceMatches(meta.Identity.SourcePath, meta.SourcePath)
	switch field {
	case MetadataFieldTMDBIDOnly:
		return idsCurrent && meta.Identity.TMDBID > 0
	case MetadataFieldIMDBIDOnly:
		return idsCurrent && meta.Identity.IMDBID > 0
	case MetadataFieldTVDBIDOnly:
		return idsCurrent && meta.Identity.TVDBID > 0
	case MetadataFieldTVmazeIDOnly:
		return idsCurrent && meta.Identity.TVmazeID > 0
	case MetadataFieldTMDB:
		return matchingTMDBMetadata(meta)
	case MetadataFieldIMDB:
		return matchingIMDBMetadata(meta)
	case MetadataFieldTVDB:
		return matchingTVDBMetadata(meta)
	case MetadataFieldTVmaze:
		return matchingTVmazeMetadata(meta)
	case MetadataFieldTVDBTitle:
		return matchingTVDBMetadata(meta)
	case MetadataFieldPoster:
		return matchingMetadataPoster(meta)
	}
	return false
}

func matchingTMDBMetadata(meta api.RuleSubject) bool {
	value := meta.ProviderMetadata.TMDB
	return providerMetadataCurrent(meta) && value != nil && meta.Identity.TMDBID > 0 &&
		value.TMDBID == meta.Identity.TMDBID && strings.TrimSpace(value.Title) != ""
}

func matchingIMDBMetadata(meta api.RuleSubject) bool {
	value := meta.ProviderMetadata.IMDB
	return providerMetadataCurrent(meta) && value != nil && meta.Identity.IMDBID > 0 &&
		value.IMDBID == meta.Identity.IMDBID && strings.TrimSpace(value.Title) != ""
}

func matchingTVDBMetadata(meta api.RuleSubject) bool {
	value := meta.ProviderMetadata.TVDB
	return providerMetadataCurrent(meta) && value != nil && meta.Identity.TVDBID > 0 &&
		value.TVDBID == meta.Identity.TVDBID &&
		(strings.TrimSpace(value.NameEnglish) != "" || strings.TrimSpace(value.Name) != "")
}

func matchingTVmazeMetadata(meta api.RuleSubject) bool {
	value := meta.ProviderMetadata.TVmaze
	return providerMetadataCurrent(meta) && value != nil && meta.Identity.TVmazeID > 0 &&
		value.TVmazeID == meta.Identity.TVmazeID && strings.TrimSpace(value.Name) != ""
}

// matchingMetadataPoster reports whether any current matching provider snapshot
// supplies poster artwork, independently of the provider used for identity.
func matchingMetadataPoster(meta api.RuleSubject) bool {
	if !providerMetadataCurrent(meta) {
		return false
	}
	if value := meta.ProviderMetadata.TMDB; value != nil && meta.Identity.TMDBID > 0 &&
		value.TMDBID == meta.Identity.TMDBID && strings.TrimSpace(value.Poster) != "" {
		return true
	}
	if value := meta.ProviderMetadata.IMDB; value != nil && meta.Identity.IMDBID > 0 &&
		value.IMDBID == meta.Identity.IMDBID && strings.TrimSpace(value.Cover) != "" {
		return true
	}
	if value := meta.ProviderMetadata.TVDB; value != nil && meta.Identity.TVDBID > 0 &&
		value.TVDBID == meta.Identity.TVDBID && strings.TrimSpace(value.Poster) != "" {
		return true
	}
	value := meta.ProviderMetadata.TVmaze
	return value != nil && meta.Identity.TVmazeID > 0 && value.TVmazeID == meta.Identity.TVmazeID &&
		(strings.TrimSpace(value.Poster) != "" || strings.TrimSpace(value.PosterMedium) != "")
}

func providerMetadataCurrent(meta api.RuleSubject) bool {
	return sourceMatches(meta.Identity.SourcePath, meta.SourcePath) &&
		sourceMatches(meta.ProviderMetadata.SourcePath, meta.SourcePath)
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
		case MetadataFieldTMDBIDOnly:
			labels = append(labels, "TMDB ID")
		case MetadataFieldIMDBIDOnly:
			labels = append(labels, "IMDb ID")
		case MetadataFieldTVDBIDOnly:
			labels = append(labels, "TVDB ID")
		case MetadataFieldTVmazeIDOnly:
			labels = append(labels, "TVmaze ID")
		case MetadataFieldTMDB:
			labels = append(labels, "fetched TMDB metadata")
		case MetadataFieldIMDB:
			labels = append(labels, "fetched IMDb metadata")
		case MetadataFieldTVDB:
			labels = append(labels, "fetched TVDB metadata")
		case MetadataFieldTVmaze:
			labels = append(labels, "fetched TVmaze metadata")
		case MetadataFieldTVDBTitle:
			labels = append(labels, "TVDB series title")
		case MetadataFieldPoster:
			labels = append(labels, "metadata poster")
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
