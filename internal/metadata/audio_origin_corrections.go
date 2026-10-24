// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

// applyTrackAudioOriginOverrides preserves language identification and records
// source review only for the exact inspected programme-track manifest.
func applyTrackAudioOriginOverrides(meta *preparationstate.State, corrections []api.TrackAudioOriginCorrection) error {
	if meta == nil {
		return nil
	}
	for _, correction := range corrections {
		index := slices.IndexFunc(meta.MediaTracks, func(track api.MediaTrackFacts) bool { return track.ID == correction.TrackID })
		conflict := func(reason string) error {
			return &api.CorrectionConflictError{
				Field:   api.CorrectionFieldMetadataTrackAudioOrigins,
				TrackID: correction.TrackID,
				Reason:  reason,
			}
		}
		if index < 0 {
			return conflict("track is not present in the inspected media")
		}
		track := &meta.MediaTracks[index]
		if correction.ManifestFingerprint == "" || correction.ManifestFingerprint != track.ManifestFingerprint {
			return conflict("track manifest changed")
		}
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			return conflict("audio origin requires an inspected programme track")
		}
		if correction.Origin != api.TrackAudioOriginal && correction.Origin != api.TrackAudioDubbed && correction.Origin != api.TrackAudioUnresolved {
			return conflict("track audio origin must be original, dubbed or unresolved")
		}
		track.AudioOrigin = correction.Origin
		track.AudioOriginProvenance = api.FactProvenanceManual
	}
	return nil
}
