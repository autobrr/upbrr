// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"slices"
	"strings"

	"golang.org/x/text/language"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/pkg/api"
)

func usableProgrammeEvidence(facts api.LanguageFacts) bool {
	return facts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory && len(facts.ProgrammeLanguages) > 0
}

func primaryAudioCorrectionMatches(facts api.LanguageFacts, files api.MediaFileFacts, correction []string) bool {
	primary := slices.IndexFunc(files.Files, func(file api.MediaFileFact) bool { return file.Primary })
	if len(correction) == 0 || primary < 0 || files.Files[primary].ResourceID == "" {
		return false
	}
	var inspected []string
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackAudio && track.ResourceID == files.Files[primary].ResourceID && programmeRole(track.Role) {
			inspected = append(inspected, track.Languages...)
		}
	}
	return allLanguagesPresent(inspected, correction) && allLanguagesPresent(correction, inspected)
}

func allLanguagesPresent(values, inventory []string) bool {
	return !slices.ContainsFunc(values, func(value string) bool {
		return !languageutil.SameLanguage(value, "zxx") && !languageutil.ContainsLanguage(inventory, value)
	})
}

func programmeRole(role api.AudioTrackRole) bool {
	return role == api.AudioRoleProgramme || role == api.AudioRoleAlternateMix
}

func programmeTrackLanguage(track api.MediaTrackFacts, aggregateCorrection []string) (string, bool) {
	if len(track.Languages) != 1 {
		return "", false
	}
	value := track.Languages[0]
	if !track.LanguageProvenance.IsManual() {
		var resolved bool
		value, resolved = regionalTrackLanguage(value, track.Title)
		if !resolved {
			return "", false
		}
	}
	if aggregateCorrection != nil && languageutil.SameLanguage(value, "pt") {
		region := portugueseRegion(value)
		if region != "" && !slices.ContainsFunc(aggregateCorrection, func(correction string) bool {
			return portugueseRegion(correction) == region
		}) {
			// A release-level correction can withdraw regional proof, but it
			// cannot establish which physical stream carries the new region.
			return "pt", true
		}
	}
	return value, true
}

// An explicit regional title qualifies a generic Portuguese tag. A title
// alone, or one that contradicts a tag's language or region, leaves it unresolved.
func regionalTrackLanguage(value, title string) (string, bool) {
	titleRegion := portugueseRegion(title)
	if titleRegion == "" {
		return value, true
	}
	region := portugueseRegion(value)
	if !languageutil.SameLanguage(value, "pt") && region == "" || region != "" && region != titleRegion {
		return "", false
	}
	return "pt-" + titleRegion, true
}

func hasBrazilianPortuguese(values []string) bool {
	return slices.ContainsFunc(values, isBrazilianPortuguese)
}

func isBrazilianPortuguese(value string) bool {
	return portugueseRegion(value) == "BR"
}

func portugueseRegion(value string) string {
	tag, ok := languageutil.LanguageTag(value)
	if ok {
		base, _ := tag.Base()
		region, confidence := tag.Region()
		if base.String() == "pt" && confidence == language.Exact {
			return region.String()
		}
	}
	base, _ := languageutil.SubtitleLanguageParts(value)
	switch strings.ToLower(strings.TrimSpace(base)) {
	case "brazilian portuguese", "portuguese (brazil)", "português (brasil)":
		return "BR"
	case "european portuguese", "portuguese (portugal)", "português (portugal)":
		return "PT"
	default:
		return ""
	}
}
