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

// CompatibilityCompanion describes measured assignment choices, independently
// of the currently saved answer. Sites retain codec and source policy ownership.
type CompatibilityCompanion struct {
	ID         string
	ResourceID string
	Mixes      []string
	CanCover   bool
	Optional   bool
}

// ReviewCompatibilityAssignments keeps coverage/count conflicts and site-marked
// removable format conflicts correctable only when measured assignments suffice.
// It does not change strict execution or independent evidence/format failures.
func ReviewCompatibilityAssignments(failures []api.RuleFailure, required []api.MediaTrackFacts, companions []CompatibilityCompanion, onePerMix bool) bool {
	var conflicts []int
	for i, failure := range failures {
		if failure.Disposition == api.RuleDispositionStrict &&
			(failure.Rule == "language_compatibility_missing" || failure.Rule == "language_compatibility_count" ||
				failure.Rule == "language_compatibility_format" && failure.QuestionnaireInput) {
			conflicts = append(conflicts, i)
		}
	}
	if len(conflicts) == 0 {
		return false
	}
	// An unanswered association does not establish enough physical companions.
	for _, i := range conflicts {
		failures[i].QuestionnaireInput = false
	}
	// Resources are independent. Solve them separately so a conflict in one
	// episode cannot multiply the search over another episode's role choices.
	byResource := map[string][]CompatibilityCompanion{}
	requiredByResource := map[string][]string{}
	for _, companion := range companions {
		byResource[companion.ResourceID] = append(byResource[companion.ResourceID], companion)
	}
	for _, mix := range required {
		if mix.ID == "" {
			return false
		}
		requiredByResource[mix.ResourceID] = append(requiredByResource[mix.ResourceID], mix.ID)
		if _, present := byResource[mix.ResourceID]; !present {
			return false
		}
	}
	for resource, choices := range byResource {
		if !compatibilityAssignmentsPossible(requiredByResource[resource], choices, onePerMix) {
			return false
		}
	}
	for _, i := range conflicts {
		failures[i].QuestionnaireInput = true
	}
	return true
}

func compatibilityAssignmentsPossible(required []string, companions []CompatibilityCompanion, onePerMix bool) bool {
	// Match every required mix to a distinct usable companion. Augmenting paths
	// preserve coverage when several mixes share the same candidate companions.
	matches := make([]string, len(companions))
	var cover func(string, []bool) bool
	cover = func(mix string, seen []bool) bool {
		for i, companion := range companions {
			if seen[i] || !companion.CanCover || !slices.Contains(companion.Mixes, mix) {
				continue
			}
			seen[i] = true
			if matches[i] == "" || cover(matches[i], seen) {
				matches[i] = mix
				return true
			}
		}
		return false
	}
	for _, mix := range required {
		if mix == "" || !cover(mix, make([]bool, len(companions))) {
			return false
		}
	}
	// HHD also requires every explicit companion to have a distinct parent.
	// Optional ordinary streams may instead remain non-compatibility audio.
	var assign func(int, map[string]bool) bool
	assign = func(index int, seen map[string]bool) bool {
		for _, mix := range companions[index].Mixes {
			if seen[mix] || slices.Contains(required, mix) && !companions[index].CanCover {
				continue
			}
			seen[mix] = true
			previous := slices.Index(matches, mix)
			if previous < 0 || companions[previous].Optional || assign(previous, seen) {
				if previous >= 0 && companions[previous].Optional {
					matches[previous] = ""
				}
				matches[index] = mix
				return true
			}
		}
		return false
	}
	for i, companion := range companions {
		if companion.Optional || matches[i] != "" {
			continue
		}
		if len(companion.Mixes) == 0 || onePerMix && !assign(i, map[string]bool{}) {
			return false
		}
	}
	if onePerMix {
		for i, companion := range companions {
			if companion.ID == "" || matches[i] == "" || !slices.Contains(matches, companion.ID) {
				continue
			}
			// An ordinary Dolby stream can be a source mix or a companion,
			// but cannot occupy both roles in the proposed assignment.
			choices := slices.Clone(companions)
			if companion.Optional {
				choices[i].Mixes = nil
				if compatibilityAssignmentsPossible(required, choices, true) {
					return true
				}
			}
			choices = slices.Clone(companions)
			for j := range choices {
				choices[j].Mixes = slices.DeleteFunc(slices.Clone(choices[j].Mixes), func(mix string) bool { return mix == companion.ID })
			}
			return compatibilityAssignmentsPossible(required, choices, true)
		}
	}
	return true
}
