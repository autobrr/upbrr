// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package clientdiscovery owns normalized, source-scoped torrent-client
// discovery for preparation and current workflow operations.
package clientdiscovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// SearchInput contains only source facts and search choices needed for one
// client-discovery operation.
type SearchInput struct {
	// SourcePath is the canonical local source path supplied to the client adapter.
	SourcePath string
	// FileList contains source-relative or local file evidence used for matching.
	FileList []string
	// DiscType identifies disc layouts whose files require client-specific matching.
	DiscType string
	// Policy controls skipping and optional client selection.
	Policy api.ClientSearchPolicy
	// ForceRecheck forwards an explicit hash-recheck choice to the selected client.
	ForceRecheck *bool
	// Debug enables client-search diagnostics.
	Debug bool
}

// Evidence is a detached normalized client snapshot. Callers decide which
// fields become private preparation evidence or operation-local state.
type Evidence struct {
	// InfoHash is the normalized hash of the matched local torrent.
	InfoHash string
	// TorrentPath is the reusable local metainfo path returned by the client search.
	TorrentPath string
	// TrackerIDs maps normalized tracker names to IDs extracted from the matched torrent.
	TrackerIDs map[string]string
	// FoundTrackerMatch reports whether any configured tracker matched.
	FoundTrackerMatch bool
	// TorrentComments contains detached tracker comment evidence.
	TorrentComments []api.TorrentMatch
	// PieceSizeConstraint carries any client-derived piece-size requirement.
	PieceSizeConstraint string
	// FoundPreferredPiece records the preferred reusable piece size when found.
	FoundPreferredPiece string
	// MatchedTrackers contains deduplicated, uppercase tracker names in sorted order.
	MatchedTrackers []string
}

// Module hides client availability, skip semantics, normalization, and error
// wrapping behind one operation-shaped interface.
type Module struct {
	clients api.ClientService
	logger  api.Logger
}

// New constructs a client-discovery module. A nil client adapter represents a
// runtime without local client discovery and produces empty evidence.
func New(clients api.ClientService, logger api.Logger) *Module {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Module{clients: clients, logger: logger}
}

// Discover obtains one current client snapshot or returns empty evidence when
// search is explicitly skipped or no client adapter is available.
func (m *Module) Discover(ctx context.Context, input SearchInput) (Evidence, error) {
	if m == nil {
		return Evidence{}, errors.New("client discovery: module is not initialized")
	}
	if ctx == nil {
		return Evidence{}, errors.New("client discovery: context is required")
	}
	if strings.TrimSpace(input.SourcePath) == "" {
		return Evidence{}, fmt.Errorf("client discovery: source path is required: %w", internalerrors.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return Evidence{}, fmt.Errorf("client discovery: canceled before search: %w", err)
	}
	if input.Policy.Skip {
		m.logger.Debugf("client discovery: decision=skip reason=requested")
		return Evidence{}, nil
	}
	if m.clients == nil {
		m.logger.Debugf("client discovery: decision=skip reason=client_unavailable")
		return Evidence{}, nil
	}

	m.logger.Debugf("client discovery: decision=start files=%d disc=%t", len(input.FileList), strings.TrimSpace(input.DiscType) != "")
	result, err := m.clients.SearchPathedTorrents(ctx, api.ClientSubject{
		SourcePath: strings.TrimSpace(input.SourcePath),
		FileList:   append([]string(nil), input.FileList...),
		DiscType:   strings.TrimSpace(input.DiscType),
		ClientOverrides: api.ClientOverrides{
			Client:       cloneString(input.Policy.Client),
			ForceRecheck: cloneBool(input.ForceRecheck),
		},
		Debug: input.Debug,
	})
	if err != nil {
		return Evidence{}, fmt.Errorf("client discovery: search pathed torrents: %w", err)
	}
	evidence := normalizeEvidence(result)
	m.logger.Debugf(
		"client discovery: decision=complete matched=%t trackers=%d ids=%d reusable_torrent=%t",
		evidence.FoundTrackerMatch,
		len(evidence.MatchedTrackers),
		len(evidence.TrackerIDs),
		evidence.TorrentPath != "",
	)
	return evidence, nil
}

// normalizeEvidence detaches slices and maps from the client result and applies
// the canonical tracker-name and whitespace normalization used by all callers.
func normalizeEvidence(value api.ClientSearchResult) Evidence {
	return Evidence{
		InfoHash:            strings.TrimSpace(value.InfoHash),
		TorrentPath:         strings.TrimSpace(value.TorrentPath),
		TrackerIDs:          normalizeTrackerIDs(value.TrackerIDs),
		FoundTrackerMatch:   value.FoundTrackerMatch,
		TorrentComments:     cloneTorrentMatches(value.TorrentComments),
		PieceSizeConstraint: strings.TrimSpace(value.PieceSizeConstraint),
		FoundPreferredPiece: strings.TrimSpace(value.FoundPreferredPiece),
		MatchedTrackers:     normalizeTrackers(value.MatchedTrackers),
	}
}

// normalizeTrackerIDs lowercases tracker keys and drops blank keys and values.
func normalizeTrackerIDs(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// normalizeTrackers returns unique uppercase tracker names in deterministic order.
func normalizeTrackers(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
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
	sort.Strings(result)
	return result
}

// cloneTorrentMatches detaches nested tracker URL slices from client-owned results.
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

// cloneString trims and detaches a non-empty optional client selector.
func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := strings.TrimSpace(*value)
	if cloned == "" {
		return nil
	}
	return &cloned
}

// cloneBool detaches an optional boolean override.
func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
