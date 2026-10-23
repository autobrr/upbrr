// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import "github.com/autobrr/upbrr/pkg/api"

// UnresolvedProgrammeLanguages returns only addressable inspected programme
// tracks whose languages need correction. It does not guess roles or fill gaps
// in inspection, and leaves known multilingual and non-linguistic audio intact.
func UnresolvedProgrammeLanguages(facts api.LanguageFacts) []api.MediaTrackFacts {
	var tracks []api.MediaTrackFacts
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) ||
			track.ID == "" || track.ManifestFingerprint == "" || knownLanguages(track.Languages) {
			continue
		}
		tracks = append(tracks, track)
	}
	return tracks
}
