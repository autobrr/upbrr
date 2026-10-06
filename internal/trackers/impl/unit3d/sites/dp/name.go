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
	return trackers.StructuredReleaseNamePolicy("unit3d/dp/v4", trackers.StructuredNamePolicy{
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
	label, established := audioLabelForFacts(meta)
	if !established {
		if err := applyDPLegacyAudioLabel(editor, meta); err != nil {
			return err
		}
		if err := trackers.ApplyDefaultAudioName(editor, meta); err != nil {
			return fmt.Errorf("apply DP default audio: %w", err)
		}
		return nil
	}

	for _, role := range []api.ReleaseNameRole{api.NameRoleDualAudio, api.NameRoleDubbed} {
		if err := editor.Omit(role); err != nil {
			return fmt.Errorf("omit DP audio-language marker: %w", err)
		}
	}
	if label != "" {
		role := api.NameRoleDualAudio
		if strings.HasSuffix(label, "Dubbed") {
			role = api.NameRoleDubbed
		}
		if err := editor.InsertBefore(role, label, api.NameRoleAudio); err != nil {
			return fmt.Errorf("set DP audio label: %w", err)
		}
	}
	if err := trackers.ApplyDefaultAudioName(editor, meta); err != nil {
		return fmt.Errorf("apply DP default audio: %w", err)
	}

	return nil
}

// applyDPLegacyAudioLabel preserves full-disc behavior and matrix rows whose
// exact replacement remains outside the approved examples in issue 606.
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
	if unit3d.Category(meta) != "TV" || meta.ProviderMetadata.TVDB == nil || !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		return nil
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	title, ok := editor.Component(api.NameRoleTitle)
	if !ok || !title.Present || !strings.EqualFold(strings.Join(strings.Fields(title.Value), " "), strings.Join(strings.Fields(evidence.CanonicalName), " ")) {
		return nil
	}
	if meta.EffectiveMetadata.YearProvenance.IsManual() {
		evidence.SeriesYear = meta.EffectiveMetadata.Year
	}
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
	if evidence.IncludeYear && evidence.SeriesYear > 0 {
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

// audioLabelForFacts implements the established original/English/Nordic cases.
// Remaining matrix combinations retain the existing policy pending issue 636.
func audioLabelForFacts(meta api.UploadSubject) (string, bool) {
	facts := meta.LanguageFacts
	languages := facts.ProgrammeLanguages
	if !facts.OriginalLanguagesKnown || facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete ||
		trackers.KnownProgrammeLanguageCount(facts) != len(languages) ||
		len(languages) == 0 {
		return "", true
	}
	original := facts.HasOriginalAudio()
	english := slices.Contains(languages, "English")
	if len(languages) == 1 && original {
		return "", true
	}
	if len(languages) >= 3 && original {
		return "MULTi", true
	}
	if facts.HasEnglishDub() && original {
		return "Dual-Audio", true
	}
	if len(languages) == 1 && facts.HasEnglishDub() {
		return "Dubbed", true
	}
	nordic := []string{"Danish", "Finnish", "Icelandic", "Norwegian", "Swedish"}
	if len(languages) == 1 && !original && !english && !slices.Contains(facts.OriginalLanguages, "English") &&
		!slices.ContainsFunc(
			facts.OriginalLanguages,
			func(language string) bool { return slices.Contains(nordic, language) },
		) && slices.Contains(nordic, languages[0]) {
		return languages[0] + " Dubbed", true
	}
	if len(languages) == 2 && original && english && slices.Contains(facts.OriginalLanguages, "English") {
		for _, language := range languages {
			if language != "English" {
				return language + " MULTi", true
			}
		}
	}
	return "", false
}
