// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

// ProviderSearchRequest inspects titles without applying identity or preparation corrections.
type ProviderSearchRequest struct {
	Query    string
	Category CanonicalCategory
}

// ProviderSearchResult retains the submitted query and the actual provider requests.
type ProviderSearchResult struct {
	Query     string
	Category  CanonicalCategory
	Providers []ProviderCandidateResult
}

// ProviderCandidateResult distinguishes a successful empty search from an unavailable provider.
// Query, Year and Category describe the effective request, not the editable browser draft.
type ProviderCandidateResult struct {
	Provider IdentityProvider
	Query    string
	Year     int
	Category CanonicalCategory
	Status   MetadataEvidenceStatus
	// SearchFailed distinguishes an attempted search failure from an unavailable client.
	// Provider errors are deliberately omitted from this public result.
	SearchFailed bool
	Filters      map[string]string
	Candidates   []ExternalIdentityCandidate
}
