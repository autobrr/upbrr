// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/pkg/api"
)

// DolbyCompatibilityCodec recognizes the canonical DD/DD+ names and their
// supported aliases. Tracker-specific acceptance remains with each policy.
func DolbyCompatibilityCodec(codec string) bool {
	switch strings.ToUpper(strings.TrimSpace(codec)) {
	case "DD", "AC-3", "DD+", "DDP", "E-AC3", "E-AC-3":
		return true
	default:
		return false
	}
}

// StandaloneDolbyAudio identifies an ordinary Dolby stream that may accompany
// another mix without changing its canonical role or choosing that mix.
func StandaloneDolbyAudio(track api.MediaTrackFacts) bool {
	return ordinaryCompatibilityAudio(track) && track.ID != "" &&
		!track.EmbeddedCompatibility && !strings.Contains(strings.ToLower(track.Title), "embedded core") &&
		DolbyCompatibilityCodec(track.Codec)
}

func ordinaryCompatibilityAudio(track api.MediaTrackFacts) bool {
	if track.Kind != api.MediaTrackAudio {
		return false
	}
	if track.Role == api.AudioRoleCompatibility {
		// The producer also marks title-labeled compatibility as Commentary for
		// legacy aggregate exclusion; that flag does not make it commentary.
		return true
	}
	return (track.Role == api.AudioRoleProgramme || track.Role == "") && !track.Commentary
}

// KnownCompatibilityLanguages returns a sorted language-code set only when all
// supplied languages are identified. A known subset cannot establish a match.
func KnownCompatibilityLanguages(languages []string) []string {
	codes := make([]string, 0, len(languages))
	for _, language := range languages {
		code := languageutil.NormalizeLanguageCode(language)
		if code == "" || code == "und" || code == "mul" {
			return nil
		}
		codes = append(codes, code)
	}
	slices.Sort(codes)
	return slices.Compact(codes)
}

// AutomaticCompatibilityMix resolves one measured same-language relationship.
// Callers preserve current manual answers and supply all plausible mixes,
// including unidentified ones, so ambiguity cannot disappear during filtering.
// Codec acceptance, required cores, counts and dispositions remain tracker-local.
func AutomaticCompatibilityMix(companion api.MediaTrackFacts, candidates []api.MediaTrackFacts) string {
	if len(candidates) != 1 || !ordinaryCompatibilityAudio(companion) || companion.ID == "" || companion.Codec == "" ||
		companion.EmbeddedCompatibility || strings.Contains(strings.ToLower(companion.Title), "embedded core") {
		return ""
	}
	mix := candidates[0]
	if !ordinaryCompatibilityAudio(mix) || mix.Role == api.AudioRoleCompatibility || mix.ID == "" || mix.ID == companion.ID ||
		mix.Codec == "" || mix.ResourceID != companion.ResourceID {
		return ""
	}
	languages := KnownCompatibilityLanguages(companion.Languages)
	if len(languages) == 0 || !slices.Equal(languages, KnownCompatibilityLanguages(mix.Languages)) {
		return ""
	}
	return mix.ID
}

// CompatibilityAudioMixes keeps plausible source mixes within one resource.
// Unknown codecs, languages or IDs remain candidates so missing evidence cannot create
// a false unique match. Callers omit empty IDs from selectable answer options.
func CompatibilityAudioMixes(facts api.LanguageFacts, companion api.MediaTrackFacts, trueHDOnly bool) []api.MediaTrackFacts {
	var candidates []api.MediaTrackFacts
	languages := KnownCompatibilityLanguages(companion.Languages)
	for _, mix := range facts.Tracks {
		if mix.Kind != api.MediaTrackAudio || mix.Role == api.AudioRoleCompatibility ||
			(mix.ID != "" && mix.ID == companion.ID) || mix.ResourceID != companion.ResourceID ||
			(trueHDOnly && mix.Codec != "" && !strings.Contains(strings.ToLower(mix.Codec), "truehd")) {
			continue
		}
		other := KnownCompatibilityLanguages(mix.Languages)
		if len(languages) == 0 || len(other) == 0 || slices.ContainsFunc(other, func(language string) bool { return slices.Contains(languages, language) }) {
			candidates = append(candidates, mix)
		}
	}
	return candidates
}

// AutomaticDolbyCompatibilityMix recognizes an ordinary same-language Dolby
// companion for a unique TrueHD mix without reclassifying canonical track facts.
func AutomaticDolbyCompatibilityMix(facts api.LanguageFacts, companion api.MediaTrackFacts) string {
	if !StandaloneDolbyAudio(companion) {
		return ""
	}
	return AutomaticCompatibilityMix(companion, CompatibilityAudioMixes(facts, companion, true))
}
