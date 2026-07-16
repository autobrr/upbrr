// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

// collectClientEvidence performs the preparation-owned client search and leaves
// state unchanged when client discovery is unavailable.
func (s *Service) collectClientEvidence(
	ctx context.Context,
	input api.PrepareInput,
	state preparationstate.State,
) (preparationstate.State, error) {
	if s.clients == nil {
		return state, nil
	}
	evidence, err := s.clients.Discover(ctx, clientdiscovery.SearchInput{
		SourcePath:   state.SourcePath,
		FileList:     state.FileList,
		DiscType:     state.DiscType,
		Policy:       input.Search,
		ForceRecheck: input.Controls.ForceRecheck,
	})
	if err != nil {
		return preparationstate.State{}, fmt.Errorf("metadata: discover client evidence: %w", err)
	}
	applyClientEvidence(&state, evidence)
	return state, nil
}

// applyClientEvidence merges a detached client snapshot into preparation state.
// Explicit tracker IDs already present in state take precedence over discovered IDs.
func applyClientEvidence(state *preparationstate.State, evidence clientdiscovery.Evidence) {
	if state == nil {
		return
	}
	if evidence.InfoHash != "" {
		state.InfoHash = evidence.InfoHash
	}
	if evidence.TorrentPath != "" {
		state.DiscoveredTorrentPath = evidence.TorrentPath
	}
	if state.TrackerIDs == nil && len(evidence.TrackerIDs) > 0 {
		state.TrackerIDs = make(map[string]string, len(evidence.TrackerIDs))
	}
	for key, value := range evidence.TrackerIDs {
		if _, exists := state.TrackerIDs[key]; !exists {
			state.TrackerIDs[key] = value
		}
	}
	state.FoundTrackerMatch = evidence.FoundTrackerMatch
	state.TorrentComments = cloneTorrentMatches(evidence.TorrentComments)
	state.PieceSizeConstraint = evidence.PieceSizeConstraint
	state.FoundPreferredPiece = evidence.FoundPreferredPiece
	state.MatchedEvidenceTrackers = mergeNormalizedTrackers(state.MatchedEvidenceTrackers, evidence.MatchedTrackers)
	state.EvidenceTrackers = mergeNormalizedTrackers(state.EvidenceTrackers, evidence.MatchedTrackers)
}

// mergeNormalizedTrackers preserves first-seen order while deduplicating uppercase names.
func mergeNormalizedTrackers(existing []string, additional []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(additional))
	result := make([]string, 0, len(existing)+len(additional))
	for _, values := range [][]string{existing, additional} {
		for _, value := range values {
			value = strings.ToUpper(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

// cloneTorrentMatches detaches nested URL slices before evidence enters mutable state.
func cloneTorrentMatches(values []api.TorrentMatch) []api.TorrentMatch {
	if values == nil {
		return nil
	}
	result := make([]api.TorrentMatch, len(values))
	for index, value := range values {
		result[index] = value
		result[index].TrackerURLsRaw = append([]string(nil), value.TrackerURLsRaw...)
		result[index].TrackerURLs = append([]api.TrackerMatch(nil), value.TrackerURLs...)
	}
	return result
}
