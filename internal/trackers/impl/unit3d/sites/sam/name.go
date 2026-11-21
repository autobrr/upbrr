// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Match an explicit AKA separator followed by a title. Match punctuation in
// the separator separately from punctuation that belongs to the English title.
var samAKASeparator = regexp.MustCompile(
	`(?i)(?: *[-–—] *(?:[(\[] *)?| *[(\[] *| +|[._]+)` +
		`(?:aka|a\.k\.a\.?)[- ._:–—]+[^- ._:)\]–—]`,
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("unit3d/sam/v5", trackers.StructuredNamePolicy{
		Defaults:  applyNameDefaults,
		Mandatory: applySAMTitle,
		Authority: []trackers.NameAuthority{
			{Role: api.NameRoleTitle, Aspect: trackers.NameValue},
			{Role: api.NameRoleAlternateTitle, Aspect: trackers.NamePresence},
		},
		Opaque: trackers.OpaqueNameRebuild,
	})
}

func applyNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject, cfg config.TrackerConfig) error {
	if err := unit3d.ApplyLocalizedNameFormatting(editor, meta); err != nil {
		return fmt.Errorf("format SAM name: %w", err)
	}
	if err := applySAMYear(editor, meta); err != nil {
		return err
	}
	editor.AllowDerivedAudioMarkerDefaults()
	if err := editor.Omit(api.NameRoleDualAudio); err != nil {
		return fmt.Errorf("reset SAM audio marker: %w", err)
	}
	marker := programmeMarker(meta)
	if marker == "" {
		return nil
	}
	if err := unit3d.ApplyLocalizedAudioMarker(editor, meta, cfg, marker); err != nil {
		return fmt.Errorf("apply SAM audio marker: %w", err)
	}
	if group, ok := editor.Component(api.NameRoleGroup); !ok || !group.Present {
		roles := editor.PresentRoles()
		if len(roles) > 0 {
			if err := editor.InsertAfter(api.NameRoleDualAudio, marker, roles[len(roles)-1]); err != nil {
				return fmt.Errorf("append SAM audio marker: %w", err)
			}
		}
	}
	return nil
}

func applySAMTitle(editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	component, ok := editor.Component(api.NameRoleTitle)
	if !ok || !component.Present || meta.EffectiveMetadata.TitleProvenance == api.FactProvenanceManualEmpty {
		return missingSAMTitle()
	}
	manual := component.Manual || meta.EffectiveMetadata.TitleProvenance.IsManual()
	var title string
	if manual {
		title = component.Value
	} else {
		title = samEnglishTitle(meta)
	}
	title = strings.Join(strings.Fields(title), " ")
	if separator := samAKASeparator.FindStringIndex(title); separator != nil {
		title = strings.TrimSpace(title[:separator[0]])
	}
	if !manual && meta.Identity.Category == api.CanonicalCategoryTV {
		component, _ := editor.Component(api.NameRoleYear)
		year, _ := strconv.Atoi(metautil.FirstNonEmptyTrimmed(component.Value, component.AvailableValue))
		title = metautil.TrimTrailingParentheticalYear(title, year)
	}
	if title == "" {
		return missingSAMTitle()
	}
	if alternate, ok := editor.Component(api.NameRoleAlternateTitle); ok && alternate.Present {
		if err := editor.Omit(api.NameRoleAlternateTitle); err != nil {
			return fmt.Errorf("omit SAM alternate title: %w", err)
		}
	}
	if err := editor.Set(api.NameRoleTitle, title); err != nil {
		return fmt.Errorf("set SAM English title: %w", err)
	}
	return nil
}

func missingSAMTitle() *trackers.NameRuleError {
	return &trackers.NameRuleError{
		Rule:   "sam_english_title",
		Role:   api.NameRoleTitle,
		Reason: "SAM requires the English international title. Check the selected provider records, then refresh metadata and prepare the release again.",
	}
}

func samEnglishTitle(meta api.UploadSubject) string {
	if !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		return ""
	}
	english := ""
	if tvdb := meta.ProviderMetadata.TVDB; meta.Identity.Category == api.CanonicalCategoryTV && tvdb != nil &&
		meta.Identity.TVDBID > 0 && tvdb.TVDBID == meta.Identity.TVDBID {
		english = strings.TrimSpace(tvdb.NameEnglish)
	}
	if data := currentSAMTMDB(meta.SourcePath, meta.Identity, meta.ProviderMetadata); english == "" && data != nil {
		english = samTMDBEnglishTitle(data.LocalizedTitles)
		if english == "" && languageutil.SameLanguage(data.OriginalLanguage, "en") {
			english = metautil.FirstNonEmptyTrimmed(data.OriginalTitle, data.Title)
		}
	}
	if data := meta.ProviderMetadata.IMDB; english == "" && data != nil && meta.Identity.IMDBID > 0 && data.IMDBID == meta.Identity.IMDBID {
		english = samIMDBEnglishTitle(data.Akas)
	}
	if data := meta.ProviderMetadata.AniList; english == "" && data != nil && meta.Identity.Category == api.CanonicalCategoryMovie &&
		meta.Identity.MALID > 0 && data.MALID == meta.Identity.MALID && strings.EqualFold(strings.TrimSpace(data.Format), "MOVIE") {
		english = strings.TrimSpace(data.TitleEnglish)
	}
	return english
}

func samTMDBEnglishTitle(titles map[string]string) string {
	if preferred := metautil.FirstNonEmptyTrimmed(titles["en-US"], titles["en"], titles["en-GB"]); preferred != "" {
		return preferred
	}
	var english []string
	for locale, title := range titles {
		if title = strings.TrimSpace(title); title != "" && languageutil.SameLanguage(locale, "en") {
			english = append(english, title)
		}
	}
	slices.Sort(english)
	if english = slices.Compact(english); len(english) == 1 {
		return english[0]
	}
	return ""
}

func samIMDBEnglishTitle(akas []api.IMDBAKA) string {
	var english, us []string
	for _, aka := range akas {
		title := strings.TrimSpace(aka.Title)
		if title == "" || len(aka.Attributes) != 0 || !languageutil.SameLanguage(aka.Language, "en") {
			continue
		}
		english = append(english, title)
		switch strings.ToLower(strings.TrimSpace(aka.Country)) {
		case "us", "usa", "united states":
			us = append(us, title)
		}
	}
	for _, titles := range [][]string{us, english} {
		slices.Sort(titles)
		if titles = slices.Compact(titles); len(titles) == 1 {
			return titles[0]
		}
	}
	return ""
}

func currentSAMTMDB(sourcePath string, identity api.ExternalIdentity, metadata api.SourceScopedMetadata) *api.TMDBMetadata {
	data := metadata.TMDB
	if data == nil || identity.TMDBID <= 0 || data.TMDBID != identity.TMDBID || !metadata.IsCurrentFor(sourcePath, identity) {
		return nil
	}
	if category, err := api.NormalizeCanonicalCategory(data.Category); err != nil || category != identity.Category {
		return nil
	}
	return data
}

func applySAMYear(editor *trackers.NameEditor, meta api.UploadSubject) error {
	missingYear := &trackers.NameRuleError{
		Rule:   "sam_year",
		Role:   api.NameRoleYear,
		Reason: "SAM requires the release year. Supply a year before preparing the upload.",
	}
	if meta.EffectiveMetadata.YearProvenance == api.FactProvenanceManualEmpty {
		return missingYear
	}
	if component, ok := editor.Component(api.NameRoleYear); ok {
		if component.Manual && !component.Present {
			return missingYear
		}
		if component.Value != "" || component.AvailableValue != "" {
			if err := editor.Include(api.NameRoleYear); err != nil {
				return fmt.Errorf("include SAM year: %w", err)
			}
			return nil
		}
	}
	year := trackers.PreferredYear(meta, 0)
	if year == 0 && !meta.EffectiveMetadata.YearProvenance.IsManual() {
		if data := currentSAMTMDB(meta.SourcePath, meta.Identity, meta.ProviderMetadata); data != nil {
			year = data.Year
		}
	}
	if year <= 0 {
		return missingYear
	}
	if err := editor.InsertAfter(api.NameRoleYear, strconv.Itoa(year), api.NameRoleTitle); err != nil {
		return fmt.Errorf("set SAM year: %w", err)
	}
	return nil
}

func programmeMarker(meta api.UploadSubject) string {
	facts := meta.LanguageFacts
	files := meta.MediaFileFacts
	if unit3d.IsDiscType(meta.DiscType) || !facts.TrackCoverageComplete || files.ExpectedFileCount == 0 || len(files.Files) != files.ExpectedFileCount {
		return ""
	}
	if meta.ManualLanguages.AggregateAudio {
		if !primaryAudioCorrectionMatches(facts, files, meta.ManualLanguages.Audio) {
			return ""
		}
	} else if facts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
		return ""
	}
	marker := ""
	for i, file := range files.Files {
		if file.ResourceID == "" {
			return ""
		}
		var aggregateCorrection []string
		if file.Primary && meta.ManualLanguages.AggregateAudio {
			aggregateCorrection = meta.ManualLanguages.Audio
		}
		var languages []string
		for _, track := range facts.Tracks {
			if track.Kind == api.MediaTrackAudio && track.ResourceID == file.ResourceID && programmeRole(track.Role) {
				value, ok := programmeTrackLanguage(track, aggregateCorrection)
				if !ok {
					return ""
				}
				if !languageutil.SameLanguage(value, "zxx") {
					languages = append(languages, value)
				}
			}
		}
		got := languageMarker(languages)
		if got == "" || i > 0 && got != marker {
			return ""
		}
		marker = got
	}
	return marker
}

func languageMarker(languages []string) string {
	if !hasBrazilianPortuguese(languages) || slices.ContainsFunc(languages, func(language string) bool {
		return !languageutil.IsIdentifiedLanguage(language)
	}) {
		return ""
	}
	switch languageutil.LanguageIdentityCount(languages) {
	case 0, 1:
		return ""
	case 2:
		return "DUAL"
	default:
		return "MULTI"
	}
}
