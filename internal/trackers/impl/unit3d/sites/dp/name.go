// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/dp/v8", trackers.StructuredNamePolicy{
		Defaults: applyDPNameDefaults,
	})
}

func applyDPNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	if err := applyDPTVDBDisambiguation(editor, meta); err != nil {
		return err
	}
	if trackers.IsFullDiscUpload(meta.DiscType, meta.Type) {
		return applyDPLegacyAudioLabel(editor, meta)
	}
	label, established, err := audioLabelForFacts(meta)
	if err != nil {
		manual, _ := editor.Component(api.NameRoleDualAudio)
		if !manual.Manual {
			return err
		}
	}
	if !established {
		var chosen bool
		label, chosen = manualAudioLabel(api.NewTrackerValidationSubject(meta, "DP"))
		if chosen {
			for _, role := range []api.ReleaseNameRole{api.NameRoleDualAudio, api.NameRoleDubbed} {
				component, _ := editor.Component(role)
				if !component.Manual {
					continue
				}
				present := label != "" && role == audioLabelRole(label)
				if component.Present != present || present && component.Value != label {
					return &trackers.NameRuleError{
						Rule:   manualLanguageMarkerKey,
						Role:   role,
						Reason: "The DP language-marker choice conflicts with a forced language component; clear it or choose the same marker",
					}
				}
			}
		}
	}

	for _, role := range []api.ReleaseNameRole{api.NameRoleDualAudio, api.NameRoleDubbed} {
		if err := editor.Omit(role); err != nil {
			return fmt.Errorf("omit DP audio-language marker: %w", err)
		}
	}
	if label != "" {
		role := audioLabelRole(label)
		component, _ := editor.Component(role)
		if !component.Manual {
			anchor := api.NameRoleAudio
			if audio, ok := editor.Component(anchor); !ok || !audio.Present {
				anchor = api.NameRoleGroup
			}
			if component, ok := editor.Component(anchor); !ok || !component.Present {
				return &trackers.NameRuleError{
					Rule:   "audio_language_anchor",
					Role:   role,
					Reason: "The DP language marker needs a present audio or release-group component; reprepare the structured name",
				}
			}
			if err := editor.InsertBefore(role, label, anchor); err != nil {
				return fmt.Errorf("set DP audio label: %w", err)
			}
		}
	}
	if err := trackers.ApplyDefaultAudioName(editor, meta); err != nil {
		return fmt.Errorf("apply DP default audio: %w", err)
	}

	return nil
}

func audioLabelRole(label string) api.ReleaseNameRole {
	if strings.HasSuffix(label, "Dubbed") {
		return api.NameRoleDubbed
	}
	return api.NameRoleDualAudio
}

// applyDPLegacyAudioLabel preserves full-disc behavior outside issue 606.
func applyDPLegacyAudioLabel(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.IsDiscType(meta.DiscType) {
		return nil
	}
	if label := audioLabel(meta.AudioLanguages); label != "" {
		if err := editor.Set(api.NameRoleDualAudio, label); err != nil {
			return fmt.Errorf("preserve DP audio label: %w", err)
		}
	}
	return nil
}

func applyDPTVDBDisambiguation(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if unit3d.Category(meta) != "TV" {
		return nil
	}
	evidence, ok := trackers.CurrentTVDBNameDisambiguation(editor, meta)
	if !ok {
		return nil
	}
	manualYear := meta.EffectiveMetadata.YearProvenance.IsManual()
	if !manualYear {
		if !evidence.IncludeYear || evidence.SeriesYear <= 0 {
			if err := editor.Omit(api.NameRoleYear); err != nil {
				return fmt.Errorf("omit DP TVDB year: %w", err)
			}
		} else {
			if err := editor.Set(api.NameRoleYear, strconv.Itoa(evidence.SeriesYear)); err != nil {
				return fmt.Errorf("set DP TVDB year: %w", err)
			}
			if err := editor.Include(api.NameRoleYear); err != nil {
				return fmt.Errorf("include DP TVDB year: %w", err)
			}
		}
	}

	anchor := api.NameRoleAlternateTitle
	component, ok := editor.Component(anchor)
	if !ok || !component.Present {
		anchor = api.NameRoleTitle
	}
	if evidence.IncludeLocale && strings.TrimSpace(evidence.Locale) != "" {
		if err := editor.InsertAfter(api.NameRoleLocale, evidence.Locale, anchor); err != nil {
			return fmt.Errorf("insert DP TVDB locale: %w", err)
		}
		anchor = api.NameRoleLocale
	}
	if !manualYear && evidence.IncludeYear && evidence.SeriesYear > 0 {
		if err := editor.MoveAfter(api.NameRoleYear, anchor); err != nil {
			return fmt.Errorf("move DP TVDB year: %w", err)
		}
	}
	return nil
}

func audioLabel(values []string) string {
	unique := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			unique[strings.ToUpper(value)] = struct{}{}
		}
	}
	switch len(unique) {
	case 0:
		return ""
	case 1:
		for value := range unique {
			return value
		}
		return ""
	case 2:
		return "Dual-Audio"
	default:
		return "MULTi"
	}
}

// audioLabelForFacts applies established DP rows. A false boolean identifies an
// uncovered complete composition needing manual choice. Incomplete facts select
// no marker; ambiguous primary-track evidence returns a naming error.
func audioLabelForFacts(meta api.UploadSubject) (string, bool, error) {
	facts := meta.LanguageFacts
	languages := facts.ProgrammeLanguages
	if !facts.OriginalLanguagesKnown || len(facts.OriginalLanguages) == 0 || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete ||
		trackers.KnownProgrammeLanguageCount(facts) != len(languages) ||
		len(languages) == 0 {
		return "", true, nil
	}
	original := facts.HasOriginalAudio()
	english := slices.Contains(languages, "English")
	if len(languages) == 1 && original {
		return "", true, nil
	}
	if len(languages) >= 3 && original {
		return "MULTi", true, nil
	}
	if facts.HasEnglishDub() && original {
		return "Dual-Audio", true, nil
	}
	if len(languages) == 1 && facts.HasEnglishDub() {
		return "Dubbed", true, nil
	}
	nordic := []string{"Danish", "Finnish", "Icelandic", "Norwegian", "Swedish"}
	if len(languages) == 1 && !original && !english && !slices.Contains(facts.OriginalLanguages, "English") &&
		!slices.ContainsFunc(
			facts.OriginalLanguages,
			func(language string) bool { return slices.Contains(nordic, language) },
		) && slices.Contains(nordic, languages[0]) {
		return languages[0] + " Dubbed", true, nil
	}
	if len(languages) != 2 {
		return "", false, nil
	}
	if english {
		for _, language := range languages {
			if language != "English" {
				if slices.Contains(facts.OriginalLanguages, "English") && slices.Contains(facts.OriginalLanguages, language) {
					return "", false, nil
				}
				return language + " MULTi", true, nil
			}
		}
	}
	if !original &&
		slices.Contains(nordic, languages[0]) && slices.Contains(nordic, languages[1]) {
		if language := primaryProgrammeLanguage(facts); language != "" {
			return language + " MULTi", true, nil
		}
		return "", true, &trackers.NameRuleError{
			Rule:   "primary_audio_language",
			Role:   api.NameRoleDualAudio,
			Reason: "Unresolved DP language naming: one inspected primary programme audio track with a single established language is required",
		}
	}
	if !slices.Contains(facts.OriginalLanguages, "English") {
		for index, language := range languages {
			other := languages[1-index]
			if original {
				if slices.Contains(facts.OriginalLanguages, language) && !slices.Contains(facts.OriginalLanguages, other) {
					return other + " MULTi", true, nil
				}
			} else if slices.Contains(nordic, language) && !slices.Contains(nordic, other) {
				return other + " MULTi", true, nil
			}
		}
	}
	return "", false, nil
}

// primaryProgrammeLanguage uses inspected track identity, never aggregate order
// or the default flag. Duplicate identities and conflicting languages are unresolved.
func primaryProgrammeLanguage(facts api.LanguageFacts) string {
	if facts.PrimaryAudioTrackID == "" {
		return ""
	}
	language := ""
	matches := 0
	for _, track := range facts.Tracks {
		if track.ID != facts.PrimaryAudioTrackID {
			continue
		}
		matches++
		if track.Kind == api.MediaTrackAudio && (track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix) &&
			len(track.Languages) == 1 && slices.Contains(facts.ProgrammeLanguages, track.Languages[0]) {
			language = track.Languages[0]
		}
	}
	if matches != 1 {
		return ""
	}
	return language
}
