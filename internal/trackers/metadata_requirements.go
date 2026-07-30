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
	// MetadataFieldTMDBTitle represents a non-empty title from matching TMDB metadata.
	MetadataFieldTMDBTitle MetadataField = "tmdb_title"
	// MetadataFieldIMDBTitle represents a non-empty title from matching IMDb metadata.
	MetadataFieldIMDBTitle MetadataField = "imdb_title"
	// MetadataFieldTVDBTitle represents a non-empty title from matching TVDB metadata.
	MetadataFieldTVDBTitle MetadataField = "tvdb_title"
	// MetadataFieldTVDBYear represents a positive series year from matching TVDB metadata.
	MetadataFieldTVDBYear MetadataField = "tvdb_year"
	// MetadataFieldTVDBDisambiguation represents usable TVDB name-collision evidence.
	MetadataFieldTVDBDisambiguation MetadataField = "tvdb_disambiguation"
	// MetadataFieldTMDBOriginCountries represents non-empty origin countries from matching TMDB metadata.
	MetadataFieldTMDBOriginCountries MetadataField = "tmdb_origin_countries"
	// MetadataFieldTMDBUnavailable represents explicit evidence that TMDB has no matching entry.
	MetadataFieldTMDBUnavailable MetadataField = "tmdb_unavailable"
	// MetadataFieldIMDBUnavailable represents explicit evidence that IMDb has no matching entry.
	MetadataFieldIMDBUnavailable MetadataField = "imdb_unavailable"
	// MetadataFieldTVDBUnavailable represents explicit evidence that TVDB has no matching entry.
	MetadataFieldTVDBUnavailable MetadataField = "tvdb_unavailable"
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
			Disposition: metadataCategoryDisposition(policy),
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
		rule := metadataRequirementRule(requirement.AnyOf)
		reason := "missing required " + metadataFieldList(requirement.AnyOf)
		failures = append(failures, api.RuleFailure{
			Rule:        rule,
			Reason:      reason,
			Disposition: disposition,
		})
	}
	return failures, true
}

func metadataRequirementRule(fields []MetadataField) string {
	switch {
	case slices.Contains(fields, MetadataFieldPoster):
		return "require_metadata_poster"
	case slices.Contains(fields, MetadataFieldTVDBDisambiguation):
		return "require_tvdb_disambiguation"
	case slices.Contains(fields, MetadataFieldTMDBOriginCountries):
		return "require_metadata_origin_country"
	case slices.Contains(fields, MetadataFieldTMDBTitle),
		slices.Contains(fields, MetadataFieldIMDBTitle),
		slices.Contains(fields, MetadataFieldTVDBTitle),
		slices.Contains(fields, MetadataFieldTVDBYear):
		return "require_metadata_naming_fact"
	case slices.Contains(fields, MetadataFieldTMDBUnavailable),
		slices.Contains(fields, MetadataFieldIMDBUnavailable),
		slices.Contains(fields, MetadataFieldTVDBUnavailable):
		return "require_provider_availability"
	default:
		return "require_metadata_id"
	}
}

// metadataCategoryDisposition prevents a waivable missing-category result from
// bypassing a category-scoped strict metadata requirement.
func metadataCategoryDisposition(policy TrackerMetadataPolicy) api.RuleDisposition {
	if len(policy.Requirements) == 0 {
		return api.RuleDispositionWaivable
	}
	disposition := api.RuleDispositionAdvisory
	for _, requirement := range policy.Requirements {
		requirementDisposition := api.NormalizeRuleDisposition(requirement.Disposition)
		if requirementDisposition == api.RuleDispositionStrict {
			return api.RuleDispositionStrict
		}
		if requirementDisposition == api.RuleDispositionWaivable {
			disposition = api.RuleDispositionWaivable
		}
	}
	return disposition
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

// MetadataFieldPresent reports whether one source-scoped metadata field is
// present. Tracker-local validation uses it to compose conditional conjunctions
// that cannot be represented by one AnyOf row.
func MetadataFieldPresent(field MetadataField, meta api.RuleSubject) bool {
	return metadataFieldPresent(field, meta)
}

// metadataFieldPresent accepts only IDs and provider data scoped to the current
// source and prepared generation.
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
	case MetadataFieldTMDBTitle:
		return matchingTMDBTitle(meta)
	case MetadataFieldIMDBTitle:
		return matchingIMDBTitle(meta)
	case MetadataFieldTVDBTitle:
		return matchingTVDBTitle(meta)
	case MetadataFieldTVDBYear:
		return matchingTVDBMetadata(meta) && meta.ProviderMetadata.TVDB.Year > 0
	case MetadataFieldTVDBDisambiguation:
		return matchingTVDBDisambiguation(meta)
	case MetadataFieldTMDBOriginCountries:
		return matchingTMDBOriginCountries(meta)
	case MetadataFieldTMDBUnavailable:
		return matchingProviderUnavailable(meta, api.IdentityProviderTMDB)
	case MetadataFieldIMDBUnavailable:
		return matchingProviderUnavailable(meta, api.IdentityProviderIMDB)
	case MetadataFieldTVDBUnavailable:
		return matchingProviderUnavailable(meta, api.IdentityProviderTVDB)
	case MetadataFieldPoster:
		return matchingMetadataPoster(meta)
	}
	return false
}

func matchingTMDBMetadata(meta api.RuleSubject) bool {
	value := meta.ProviderMetadata.TMDB
	return providerMetadataCurrent(meta) && value != nil && meta.Identity.TMDBID > 0 &&
		value.TMDBID == meta.Identity.TMDBID
}

func matchingIMDBMetadata(meta api.RuleSubject) bool {
	value := meta.ProviderMetadata.IMDB
	return providerMetadataCurrent(meta) && value != nil && meta.Identity.IMDBID > 0 &&
		value.IMDBID == meta.Identity.IMDBID
}

func matchingTVDBMetadata(meta api.RuleSubject) bool {
	value := meta.ProviderMetadata.TVDB
	return providerMetadataCurrent(meta) && value != nil && meta.Identity.TVDBID > 0 &&
		value.TVDBID == meta.Identity.TVDBID
}

func matchingTVmazeMetadata(meta api.RuleSubject) bool {
	value := meta.ProviderMetadata.TVmaze
	return providerMetadataCurrent(meta) && value != nil && meta.Identity.TVmazeID > 0 &&
		value.TVmazeID == meta.Identity.TVmazeID && strings.TrimSpace(value.Name) != ""
}

func matchingTMDBTitle(meta api.RuleSubject) bool {
	return matchingTMDBMetadata(meta) && strings.TrimSpace(meta.ProviderMetadata.TMDB.Title) != ""
}

func matchingIMDBTitle(meta api.RuleSubject) bool {
	return matchingIMDBMetadata(meta) && strings.TrimSpace(meta.ProviderMetadata.IMDB.Title) != ""
}

func matchingTVDBTitle(meta api.RuleSubject) bool {
	return matchingTVDBMetadata(meta) &&
		(strings.TrimSpace(meta.ProviderMetadata.TVDB.NameEnglish) != "" || strings.TrimSpace(meta.ProviderMetadata.TVDB.Name) != "")
}

func matchingTVDBDisambiguation(meta api.RuleSubject) bool {
	if !matchingTVDBTitle(meta) {
		return false
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	if strings.TrimSpace(evidence.Source) == "" {
		return false
	}
	return evidence.Status == api.MetadataEvidenceStatusComplete || evidence.Status == api.MetadataEvidenceStatusPartial
}

func matchingTMDBOriginCountries(meta api.RuleSubject) bool {
	if !matchingTMDBMetadata(meta) {
		return false
	}
	for _, country := range meta.ProviderMetadata.TMDB.OriginCountry {
		if strings.TrimSpace(country) != "" {
			return true
		}
	}
	return false
}

func matchingProviderUnavailable(meta api.RuleSubject, provider api.IdentityProvider) bool {
	if !providerMetadataCurrent(meta) {
		return false
	}
	if _, found := meta.Identity.ProviderID(provider); found {
		return false
	}
	for _, evidence := range meta.ProviderMetadata.ProviderAvailability {
		if evidence.Provider == provider &&
			evidence.Status == api.ProviderAvailabilityStatusNotFound &&
			strings.TrimSpace(evidence.Source) != "" {
			return true
		}
	}
	return false
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
	return meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity)
}

// sourceMatches preserves legacy unscoped canonical identity while rejecting
// an explicitly different source. Path comparison is case-insensitive to match
// persisted source keys.
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
		case MetadataFieldTMDBTitle:
			labels = append(labels, "fetched TMDB title")
		case MetadataFieldIMDBTitle:
			labels = append(labels, "fetched IMDb title")
		case MetadataFieldTVDBTitle:
			labels = append(labels, "fetched TVDB series title")
		case MetadataFieldTVDBYear:
			labels = append(labels, "fetched TVDB series year")
		case MetadataFieldTVDBDisambiguation:
			labels = append(labels, "TVDB name disambiguation")
		case MetadataFieldTMDBOriginCountries:
			labels = append(labels, "fetched TMDB origin countries")
		case MetadataFieldTMDBUnavailable:
			labels = append(labels, "explicit TMDB entry-unavailable evidence")
		case MetadataFieldIMDBUnavailable:
			labels = append(labels, "explicit IMDb entry-unavailable evidence")
		case MetadataFieldTVDBUnavailable:
			labels = append(labels, "explicit TVDB entry-unavailable evidence")
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
