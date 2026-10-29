// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"maps"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

// NumberedTVScopeFromFiles resolves complete per-file episode membership using
// the existing TV parser. Unknown videos, incomplete lists and conflicting
// seasons retain unknown membership. It never infers whole-season coverage.
func NumberedTVScopeFromFiles(files []string, fileCount int, season *int) *api.DupeTVScope {
	scope := (&api.DupeTVScope{Season: season, FromFiles: true}).Clone()
	if season == nil || *season < 0 || len(files) == 0 || fileCount != len(files) {
		return scope
	}
	episodes := make(map[int]struct{})
	for _, file := range videoFileSet(files) {
		membership := contentScopeFromTitle(file, FactOriginTrackerTitle)
		if (membership.Kind != contentScopeEpisode && membership.Kind != contentScopeEpisodeRange) ||
			membership.Season != *season || membership.EpisodeStart <= 0 || membership.EpisodeEnd < membership.EpisodeStart {
			return scope
		}
		for episode := membership.EpisodeStart; episode <= membership.EpisodeEnd; episode++ {
			episodes[episode] = struct{}{}
		}
	}
	scope.Episodes = slices.Sorted(maps.Keys(episodes))
	return scope
}

func numberedTVScope(scope *api.DupeTVScope, origin FactOrigin) contentScope {
	if scope.FromFiles {
		if origin == FactOriginTargetMedia {
			origin = FactOriginContentName
		} else {
			origin = FactOriginTrackerTitle
		}
	}
	result := contentScope{Kind: contentScopeUnknownTV, Origin: origin}
	if scope.Season == nil || *scope.Season < 0 {
		return result
	}
	result.Season, result.SeasonKnown = *scope.Season, true
	episodes := slices.Clone(scope.Episodes)
	slices.Sort(episodes)
	episodes = slices.Compact(episodes)
	if len(episodes) == 0 || episodes[0] <= 0 {
		return result
	}
	result.Kind = contentScopeEpisode
	if len(episodes) > 1 {
		result.Kind = contentScopeEpisodeRange
	}
	result.Episodes, result.EpisodeStart, result.EpisodeEnd = episodes, episodes[0], episodes[len(episodes)-1]
	return result
}

func compareEpisodeMembership(a, b contentScope) contentOverlap {
	if len(a.Episodes) == 0 && len(b.Episodes) > 0 {
		a, b = b, a
	}
	overlap := 0
	if len(a.Episodes) == 0 {
		overlap = max(0, min(a.EpisodeEnd, b.EpisodeEnd)-max(a.EpisodeStart, b.EpisodeStart)+1)
	} else {
		for _, episode := range a.Episodes {
			contains := episode >= b.EpisodeStart && episode <= b.EpisodeEnd
			if len(b.Episodes) > 0 {
				_, contains = slices.BinarySearch(b.Episodes, episode)
			}
			if contains {
				overlap++
			}
		}
	}
	if overlap == 0 {
		return contentDefinitelyDisjoint
	}
	aCount, bCount := len(a.Episodes), len(b.Episodes)
	if aCount == 0 {
		aCount = a.EpisodeEnd - a.EpisodeStart + 1
	}
	if bCount == 0 {
		bCount = b.EpisodeEnd - b.EpisodeStart + 1
	}
	if aCount == bCount && overlap == aCount {
		return contentEqual
	}
	return contentOverlaps
}

func numberedScopeContradictsTitle(scope, title contentScope, numbered bool) bool {
	// A season marker in an API-generated name identifies the season without
	// claiming every episode. Narrower complete membership takes precedence.
	if numbered && title.Kind == contentScopeSeasonPack && contentSeasonKnown(scope) && scope.Season == title.Season {
		return false
	}
	return contentScopesContradict(scope, title)
}
