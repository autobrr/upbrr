// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func rejectTVDBEpisodeLink(meta *preparationstate.State, ids *api.ExternalIdentity, metadata *api.SourceScopedMetadata) {
	if ids.Dependencies.IMDB.TVDBID == 0 || metadata.IMDB == nil || !strings.EqualFold(metadata.IMDB.Type, "tvEpisode") || metadata.TVDB == nil {
		return
	}
	for i, link := range metadata.TVDB.RemoteLinks.Links {
		if link.Provider == api.IdentityProviderIMDB && link.ID == ids.IMDBID {
			metadata.TVDB.RemoteLinks.Links[i].State = "unsupported"
			metadata.TVDB.RemoteLinks.Links[i].ID = 0
		}
	}
	appendIdentityCorrectionWarning(meta, "TVDB's IMDb remote link identifies an episode rather than a series. Select a series-level IMDb ID.")
	clearInferredProviderID(&ids.IMDBID, &ids.Provenance.IMDB)
	ids.Dependencies.IMDB = api.IdentityDependency{}
	metadata.IMDB = nil
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

// applyTVDBSeriesLinks fills missing siblings only when series evidence agrees with current identities.
// Independently selected IDs and explicit clears remain authoritative, including on conflicting links.
func applyTVDBSeriesLinks(meta *preparationstate.State, ids *api.ExternalIdentity, metadata api.SourceScopedMetadata, overrides api.ExternalIDOverrides) {
	if metadata.TVDB == nil || metadata.TVDB.TVDBID != ids.TVDBID || ids.TVDBID == 0 || !shouldUseTVDBForCategory(*meta, *ids) {
		return
	}
	links := metadata.TVDB.RemoteLinks
	if links.Status == "" || links.Status == api.MetadataEvidenceStatusUnavailable {
		return
	}
	type target struct {
		id         *int
		provenance *api.IdentityProvenance
		dependency *api.IdentityDependency
		override   *int
	}
	targets := map[api.IdentityProvider]target{
		api.IdentityProviderTMDB:   {&ids.TMDBID, &ids.Provenance.TMDB, &ids.Dependencies.TMDB, overrides.TMDBID},
		api.IdentityProviderIMDB:   {&ids.IMDBID, &ids.Provenance.IMDB, &ids.Dependencies.IMDB, overrides.IMDBID},
		api.IdentityProviderTVmaze: {&ids.TVmazeID, &ids.Provenance.TVmaze, &ids.Dependencies.TVmaze, overrides.TVmazeID},
		api.IdentityProviderMAL:    {&ids.MALID, &ids.Provenance.MAL, &ids.Dependencies.MAL, overrides.MALID},
	}
	// A manually selected TMDB resource retains its existing independent MAL authority.
	if positiveProviderOverride(overrides.TMDBID) {
		delete(targets, api.IdentityProviderMAL)
	}
	conflict := links.Status == api.MetadataEvidenceStatusContradictory
	for _, link := range links.Links {
		if link.State == "malformed" {
			appendIdentityCorrectionWarning(meta, "TVDB returned a malformed series remote ID; inspect provider candidates or correct the ID.")
		}
		if target, ok := targets[link.Provider]; ok && link.State == "valid" && *target.id > 0 && *target.id != link.ID {
			conflict = true
		}
	}
	if conflict {
		appendIdentityCorrectionWarning(
			meta,
			"TVDB series remote IDs conflict with the selected provider identities. Correct the conflicting IDs before resolving missing siblings.",
		)
		return
	}
	for _, link := range links.Links {
		target, ok := targets[link.Provider]
		if !ok || link.State != "valid" || link.ID <= 0 || target.override != nil {
			continue
		}
		applyLinkedProviderID(target.id, target.provenance, target.dependency, link.ID, api.IdentityDependency{TVDBID: ids.TVDBID})
	}
}
