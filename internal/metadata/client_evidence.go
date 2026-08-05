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

// collectClientEvidence performs the preparation-owned client search and
// retains its explicit disposition with the detached result.
func (s *Service) collectClientEvidence(
	ctx context.Context,
	input api.PrepareInput,
	state preparationstate.State,
) (preparationstate.State, error) {
	evidence, err := s.discoverClientEvidence(ctx, input, state.SourcePath, state.FileList, state.DiscType)
	if err != nil {
		return preparationstate.State{}, err
	}
	applyClientEvidence(&state, input, evidence)
	return state, nil
}

func (s *Service) discoverClientEvidence(
	ctx context.Context,
	input api.PrepareInput,
	sourcePath string,
	fileList []string,
	discType string,
) (clientdiscovery.Evidence, error) {
	if s.clients == nil {
		return clientdiscovery.Evidence{Disposition: clientdiscovery.DispositionUnavailable}, nil
	}
	evidence, err := s.clients.Discover(ctx, clientdiscovery.SearchInput{
		SourcePath:   sourcePath,
		FileList:     fileList,
		DiscType:     discType,
		Policy:       input.Search,
		ForceRecheck: input.Controls.ForceRecheck,
	})
	if err != nil {
		return clientdiscovery.Evidence{}, fmt.Errorf("metadata: discover client evidence: %w", err)
	}
	return evidence, nil
}

// applyClientEvidence merges a detached client snapshot into preparation state.
// Explicit tracker IDs already present in state take precedence over discovered IDs.
func applyClientEvidence(state *preparationstate.State, input api.PrepareInput, evidence clientdiscovery.Evidence) {
	if state == nil {
		return
	}
	state.ClientEvidence = clientEvidenceSnapshot(input, evidence)
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

func clientEvidenceSnapshot(input api.PrepareInput, evidence clientdiscovery.Evidence) preparationstate.ClientEvidenceSnapshot {
	result := api.ClientSearchResult{
		InfoHash:            evidence.InfoHash,
		TorrentPath:         evidence.TorrentPath,
		TrackerIDs:          cloneTrackerIDs(evidence.TrackerIDs),
		FoundTrackerMatch:   evidence.FoundTrackerMatch,
		TorrentComments:     cloneTorrentMatches(evidence.TorrentComments),
		PieceSizeConstraint: evidence.PieceSizeConstraint,
		FoundPreferredPiece: evidence.FoundPreferredPiece,
		MatchedTrackers:     append([]string(nil), evidence.MatchedTrackers...),
	}
	disposition := preparationstate.ClientEvidenceDispositionUnknown
	switch evidence.Disposition {
	case clientdiscovery.DispositionSearched:
		disposition = preparationstate.ClientEvidenceDispositionSearched
	case clientdiscovery.DispositionSkipped:
		disposition = preparationstate.ClientEvidenceDispositionSkipped
	case clientdiscovery.DispositionUnavailable:
		disposition = preparationstate.ClientEvidenceDispositionUnavailable
	}
	forced := input.Controls.ForceRecheck != nil && *input.Controls.ForceRecheck && disposition == preparationstate.ClientEvidenceDispositionSearched
	return preparationstate.CloneClientEvidenceSnapshot(preparationstate.ClientEvidenceSnapshot{
		Disposition:   disposition,
		Policy:        input.Search,
		ForcedRecheck: forced,
		Result:        result,
	})
}

// HydrateClientEvidence rebuilds only the private client snapshot needed when
// a compatible persisted generation is first reused after process restart.
func (s *Service) HydrateClientEvidence(
	ctx context.Context,
	request preparationstate.Request,
) (preparationstate.ClientEvidenceSnapshot, error) {
	files := make([]string, 0, len(request.Manifest.Entries))
	for _, entry := range request.Manifest.Entries {
		if entry.Type == api.SourceEntryTypeFile || entry.Type == api.SourceEntryTypePlaylist {
			files = append(files, entry.Path)
		}
	}
	discType := request.Layout.DiscType
	if strings.TrimSpace(discType) == "" {
		discType = request.Manifest.Classification.DiscType
	}
	evidence, err := s.discoverClientEvidence(ctx, request.Input, request.Manifest.SourcePath, files, discType)
	if err != nil {
		return preparationstate.ClientEvidenceSnapshot{}, fmt.Errorf("metadata: hydrate client evidence: %w", err)
	}
	return clientEvidenceSnapshot(request.Input, evidence), nil
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
