// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

const tvdbSiblingConflictWarning = "TVDB series remote IDs conflict with the selected provider identities. Correct the conflicting IDs before resolving missing siblings."

// rejectTVDBEpisodeLink returns the unusable ID so other provider links cannot reintroduce it.
func rejectTVDBEpisodeLink(meta *preparationstate.State, ids *api.ExternalIdentity, metadata *api.SourceScopedMetadata) int {
	if ids.Dependencies.IMDB.TVDBID == 0 || metadata.IMDB == nil || !strings.EqualFold(metadata.IMDB.Type, "tvEpisode") || metadata.TVDB == nil {
		return 0
	}
	if ids.Provenance.IMDB == api.IdentityProvenanceRetained || ids.Provenance.IMDB == api.IdentityProvenanceExplicit {
		return 0
	}
	rejectedID := ids.IMDBID
	for i, link := range metadata.TVDB.RemoteLinks.Links {
		if link.Provider == api.IdentityProviderIMDB && link.ID == ids.IMDBID {
			metadata.TVDB.RemoteLinks.Links[i].State = "unsupported"
			metadata.TVDB.RemoteLinks.Links[i].ID = 0
		}
	}
	appendIdentityCorrectionWarning(meta, "TVDB's IMDb remote link identifies an episode rather than a series and was ignored.")
	clearInferredProviderID(&ids.IMDBID, &ids.Provenance.IMDB)
	ids.Dependencies.IMDB = api.IdentityDependency{}
	metadata.IMDB = nil
	return rejectedID
}

// markTVDBLinkConflict preserves the rejected namespace while withdrawing its ID from inference.
func markTVDBLinkConflict(metadata *api.TVDBMetadata, provider api.IdentityProvider, id int) {
	if metadata == nil || id <= 0 {
		return
	}
	for i, link := range metadata.RemoteLinks.Links {
		if link.Provider == provider && link.ID == id && link.State == "valid" {
			metadata.RemoteLinks.Links[i].State = "conflicting"
			metadata.RemoteLinks.Links[i].ID = 0
			metadata.RemoteLinks.Status = api.MetadataEvidenceStatusContradictory
		}
	}
}

func isUsableTVDBMALLink(link api.TVDBRemoteLink) bool {
	return link.Provider == api.IdentityProviderMAL && (link.State == "valid" || link.State == "conflicting")
}

func hasTVDBMALConflict(links api.TVDBRemoteLinks) bool {
	return slices.ContainsFunc(links.Links, func(link api.TVDBRemoteLink) bool {
		return link.Provider == api.IdentityProviderMAL && link.State == "conflicting"
	})
}

func restoreAgreeingTVDBMALLinks(links, raw api.TVDBRemoteLinks, malID int, explicitMAL bool) api.TVDBRemoteLinks {
	if raw.Status != api.MetadataEvidenceStatusComplete || hasTVDBMALConflict(raw) {
		return links
	}
	var agreeing []api.TVDBRemoteLink
	for _, link := range raw.Links {
		if link.Provider == api.IdentityProviderMAL && link.State == "valid" {
			if malID > 0 && link.ID != malID {
				return links
			}
			agreeing = append(agreeing, link)
		}
	}
	// An explicit MAL decision can supersede the old disagreement once TVDB
	// confirms that it no longer has a MAL link.
	if len(agreeing) == 0 && !explicitMAL {
		return links
	}
	for _, link := range links.Links {
		// Keep earlier rejection states, and reapply valid links only when the replay confirms them.
		if !isUsableTVDBMALLink(link) && (link.State != "valid" || slices.Contains(raw.Links, link)) {
			agreeing = append(agreeing, link)
		}
	}
	links.Links = agreeing
	if !slices.ContainsFunc(agreeing, func(link api.TVDBRemoteLink) bool { return link.State == "conflicting" }) {
		links.Status = raw.Status
	}
	return links
}

func safeTVDBNameMatch(results []tvdb.SeriesSearchResult, title string, year int) int {
	if parsed := metautil.ParseRelease(title); parsed.Title != "" {
		title = parsed.Title
	}
	selected := 0
	for _, result := range results {
		resultYear, _ := strconv.Atoi(result.Year)
		if result.TVDBID <= 0 || year > 0 && resultYear != year {
			continue
		}
		matches := tmdb.NormalizeTitle(result.Name) == tmdb.NormalizeTitle(title) || tmdb.NormalizeTitle(result.NameEnglish) == tmdb.NormalizeTitle(title)
		for _, alias := range result.Aliases {
			matches = matches || tmdb.NormalizeTitle(alias.Name) == tmdb.NormalizeTitle(title)
		}
		if !matches {
			continue
		}
		if selected != 0 && selected != result.TVDBID {
			return 0
		}
		selected = result.TVDBID
	}
	return selected
}

type tvdbSiblingTarget struct {
	id         *int
	provenance *api.IdentityProvenance
	dependency *api.IdentityDependency
	override   *int
}

func tvdbSiblingTargets(ids *api.ExternalIdentity, overrides api.ExternalIDOverrides) map[api.IdentityProvider]tvdbSiblingTarget {
	targets := map[api.IdentityProvider]tvdbSiblingTarget{
		api.IdentityProviderTMDB:   {&ids.TMDBID, &ids.Provenance.TMDB, &ids.Dependencies.TMDB, overrides.TMDBID},
		api.IdentityProviderIMDB:   {&ids.IMDBID, &ids.Provenance.IMDB, &ids.Dependencies.IMDB, overrides.IMDBID},
		api.IdentityProviderTVmaze: {&ids.TVmazeID, &ids.Provenance.TVmaze, &ids.Dependencies.TVmaze, overrides.TVmazeID},
		api.IdentityProviderMAL:    {&ids.MALID, &ids.Provenance.MAL, &ids.Dependencies.MAL, overrides.MALID},
	}
	// A manually selected TMDB resource retains its existing independent MAL authority.
	if positiveProviderOverride(overrides.TMDBID) && !positiveProviderOverride(overrides.MALID) {
		delete(targets, api.IdentityProviderMAL)
	}
	return targets
}

// tvdbSiblingLinksMismatch rejects links to a different selected resource.
func tvdbSiblingLinksMismatch(links api.TVDBRemoteLinks, targets map[api.IdentityProvider]tvdbSiblingTarget) bool {
	for _, link := range links.Links {
		if target, ok := targets[link.Provider]; ok && link.State == "valid" && *target.id > 0 && *target.id != link.ID {
			return true
		}
	}
	return false
}

// applyTVDBSeriesLinks fills missing siblings only when series evidence agrees with current identities.
// Independently selected IDs and explicit clears remain authoritative, including on conflicting links.
// A link that failed IMDb verification remains raw evidence without being reapplied in this collection.
func applyTVDBSeriesLinks(
	meta *preparationstate.State,
	ids *api.ExternalIdentity,
	metadata api.SourceScopedMetadata,
	overrides api.ExternalIDOverrides,
	unverifiedIMDBID int,
) {
	if metadata.TVDB == nil || metadata.TVDB.TVDBID != ids.TVDBID || ids.TVDBID == 0 || !shouldUseTVDBForCategory(*meta, *ids) {
		return
	}
	links := metadata.TVDB.RemoteLinks
	if links.Status == "" || links.Status == api.MetadataEvidenceStatusUnavailable {
		return
	}
	targets := tvdbSiblingTargets(ids, overrides)
	for _, link := range links.Links {
		if link.State == "malformed" {
			appendIdentityCorrectionWarning(meta, "TVDB returned a malformed series remote ID; inspect provider candidates or correct the ID.")
		}
	}
	if links.Status == api.MetadataEvidenceStatusContradictory || tvdbSiblingLinksMismatch(links, targets) {
		appendIdentityCorrectionWarning(meta, tvdbSiblingConflictWarning)
		return
	}
	for _, link := range links.Links {
		target, ok := targets[link.Provider]
		if !ok || link.State != "valid" || link.ID <= 0 || target.override != nil {
			continue
		}
		if link.Provider == api.IdentityProviderIMDB && link.ID == unverifiedIMDBID {
			continue
		}
		applyLinkedProviderID(target.id, target.provenance, target.dependency, link.ID, api.IdentityDependency{TVDBID: ids.TVDBID})
	}
}
