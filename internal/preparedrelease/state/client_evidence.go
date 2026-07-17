// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparationstate

import (
	"maps"

	"github.com/autobrr/upbrr/pkg/api"
)

// ClientEvidenceDisposition identifies how preparation obtained its private
// torrent-client evidence.
type ClientEvidenceDisposition string

const (
	// ClientEvidenceDispositionUnknown is valid only before collection or while
	// an older persisted generation is awaiting private-evidence hydration.
	ClientEvidenceDispositionUnknown ClientEvidenceDisposition = ""
	// ClientEvidenceDispositionSearched reports that the client adapter ran,
	// including searches that found no matching torrent.
	ClientEvidenceDispositionSearched ClientEvidenceDisposition = "searched"
	// ClientEvidenceDispositionSkipped reports an explicit preparation policy skip.
	ClientEvidenceDispositionSkipped ClientEvidenceDisposition = "skipped"
	// ClientEvidenceDispositionUnavailable reports that no client adapter exists.
	ClientEvidenceDispositionUnavailable ClientEvidenceDisposition = "unavailable"
)

// ClientEvidenceSnapshot is detached private evidence owned by one prepared
// generation. It is never persisted in public prepared-release storage or
// exposed through transport DTOs.
type ClientEvidenceSnapshot struct {
	Disposition   ClientEvidenceDisposition
	Policy        api.ClientSearchPolicy
	ForcedRecheck bool
	Result        api.ClientSearchResult
}

// CloneClientEvidenceSnapshot returns a fully detached private snapshot.
func CloneClientEvidenceSnapshot(value ClientEvidenceSnapshot) ClientEvidenceSnapshot {
	value.Policy.Client = cloneClientEvidenceString(value.Policy.Client)
	value.Result.TrackerIDs = cloneClientEvidenceStringMap(value.Result.TrackerIDs)
	value.Result.MatchedTrackers = append([]string(nil), value.Result.MatchedTrackers...)
	value.Result.TorrentComments = cloneClientEvidenceTorrentMatches(value.Result.TorrentComments)
	return value
}

func cloneClientEvidenceString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneClientEvidenceStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	cloned := make(map[string]string, len(value))
	maps.Copy(cloned, value)
	return cloned
}

func cloneClientEvidenceTorrentMatches(value []api.TorrentMatch) []api.TorrentMatch {
	if value == nil {
		return nil
	}
	cloned := make([]api.TorrentMatch, len(value))
	for index, match := range value {
		cloned[index] = match
		cloned[index].TrackerURLsRaw = append([]string(nil), match.TrackerURLsRaw...)
		cloned[index].TrackerURLs = append([]api.TrackerMatch(nil), match.TrackerURLs...)
	}
	return cloned
}
