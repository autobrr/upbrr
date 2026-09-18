// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	czLimitedPattern     = regexp.MustCompile(`(?i)\bLIMITED\b`)
	czCriterionPattern   = regexp.MustCompile(`(?i)\bCriterion\s+Collection\b`)
	czResolutionPattern  = regexp.MustCompile(`(?i)\b(?:2K|4K)\b`)
	czAnniversaryPattern = regexp.MustCompile(`(?i)\b\d{1,3}(?:st|nd|rd|th)\s+Anniversary(?:\s+Edition)?\b`)
	czExtendedPattern    = regexp.MustCompile(`(?i)\b(?:Extended(?:\s+Cut)?|EXT)\b`)
	czDirectorPattern    = regexp.MustCompile("(?i)\\b(?:Director[’'`]s\\s+Cut|Directors\\s+Cut|DC)\\b")
	czTheatricalPattern  = regexp.MustCompile(`(?i)\b(?:Theatrical\s+Cut|TC)\b`)
	czUppercasePattern   = regexp.MustCompile(`(?i)\b(?:REPACK|PROPER|RESTORED|REMASTERED)\b`)
	phdLimitedPattern    = regexp.MustCompile(`(?i)\bLIMITED\b`)
	phdCriterionPattern  = regexp.MustCompile(`(?i)\bCriterion Collection\b`)
	phdAnniversary       = regexp.MustCompile(`(?i)\b\d{1,3}(?:st|nd|rd|th)\s+Anniversary Edition\b`)
	phdDirectorPattern   = regexp.MustCompile("(?i)\\bDirector[’'`]s\\s+Cut\\b")
	phdExtendedPattern   = regexp.MustCompile(`(?i)\bExtended\s+Cut\b`)
	phdTheatricalPattern = regexp.MustCompile(`(?i)\bTheatrical\s+Cut\b`)
	phdH264Pattern       = regexp.MustCompile(`(?i)\bH\.264\b`)
	phdH265Pattern       = regexp.MustCompile(`(?i)\bH\.265\b`)
)

func releaseNamePolicy(site siteDefinition) trackers.ReleaseNamePolicyBinding {
	version := "v3"
	movieYearProvider := api.IdentityProviderTMDB
	if site.Name == "CZ" {
		version = "v5"
		movieYearProvider = api.IdentityProviderIMDB
	}
	return trackers.WithMovieYearProvider(trackers.StructuredReleaseNamePolicy(
		fmt.Sprintf("azfamily/%s/%s", strings.ToLower(site.Name), version),
		trackers.StructuredNamePolicy{
			Defaults: func(editor *trackers.NameEditor, meta api.UploadSubject, trackerConfig config.TrackerConfig) error {
				return applyNameDefaults(site, editor, meta, trackerConfig)
			},
			Search: func(meta api.UploadSubject, _ config.TrackerConfig) string { return resolveSearchName(meta) },
		},
	), movieYearProvider)
}

func applyNameDefaults(site siteDefinition, editor *trackers.NameEditor, meta api.UploadSubject, _ config.TrackerConfig) error {
	switch site.Name {
	case "AZ":
		if err := applyAZNameDefaults(editor, meta); err != nil {
			return err
		}
	case "CZ":
		if err := applyCinemaZNameDefaults(editor, meta); err != nil {
			return err
		}
	default:
		if err := applyPHDNameDefaults(editor, meta); err != nil {
			return err
		}
	}
	return applyAZFamilyGroupDefault(site.Name, editor, meta.Tag)
}

func applyAZNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if err := setNameComponent(editor, api.NameRoleTitle, avistaZEnglishTitle(meta)); err != nil {
		return err
	}
	if err := omitNameComponents(editor, api.NameRoleAlternateTitle, api.NameRoleDubbed, api.NameRoleDualAudio); err != nil {
		return err
	}
	if !isTV(meta) {
		return nil
	}
	if isSeasonPack(meta) {
		if err := editor.MoveAfter(api.NameRoleSeason, api.NameRoleTitle); err != nil {
			return fmt.Errorf("move AZ season after title: %w", err)
		}
		if err := editor.MoveAfter(api.NameRoleYear, api.NameRoleSeason); err != nil {
			return fmt.Errorf("move AZ year after season: %w", err)
		}
		return nil
	}
	if err := editor.Omit(api.NameRoleYear); err != nil {
		return fmt.Errorf("omit AZ TV year: %w", err)
	}
	if err := editor.MoveAfter(api.NameRoleDailyDate, api.NameRoleTitle); err != nil {
		return fmt.Errorf("move AZ daily date after title: %w", err)
	}
	if err := editor.MoveAfter(api.NameRoleSeason, api.NameRoleTitle); err != nil {
		return fmt.Errorf("move AZ season after title: %w", err)
	}
	return nil
}

func applyCinemaZNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject) error {
	title := cinemaZTitle(meta)
	if title == "" {
		return &trackers.NameRuleError{
			Rule:   "azfamily/cz/v5",
			Role:   api.NameRoleTitle,
			Reason: "no Latin-safe title is available; set a Latin-safe manual original title and reprepare",
		}
	}
	if err := setNameComponent(editor, api.NameRoleTitle, title); err != nil {
		return err
	}
	if err := omitNameComponents(editor, api.NameRoleAlternateTitle, api.NameRoleDubbed, api.NameRoleDualAudio); err != nil {
		return err
	}
	if err := normalizeCinemaZEdition(editor); err != nil {
		return err
	}
	if err := normalizeUppercaseComponent(editor, api.NameRoleHybrid); err != nil {
		return err
	}
	if err := normalizeUppercaseComponent(editor, api.NameRoleRepack); err != nil {
		return err
	}
	if err := editor.MoveAfter(api.NameRoleHybrid, api.NameRoleResolution); err != nil {
		return fmt.Errorf("move CinemaZ hybrid after resolution: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "DVDRIP") {
		if err := editor.Omit(api.NameRoleSource); err != nil {
			return fmt.Errorf("omit CinemaZ DVD rip source: %w", err)
		}
		if err := editor.MoveBefore(api.NameRoleVideoFormat, api.NameRoleAudio); err != nil {
			return fmt.Errorf("move CinemaZ DVD rip format before audio: %w", err)
		}
		return moveVideoAfterAudio(editor)
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return applyCinemaZDVDDefaults(editor, meta)
	}
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		if err := editor.Set(api.NameRoleSource, "Blu-ray RAW"); err != nil {
			return fmt.Errorf("set CinemaZ BDMV source: %w", err)
		}
	}
	return nil
}

func applyCinemaZDVDDefaults(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if err := omitNameComponents(editor, api.NameRoleRegion, api.NameRoleDVDSystem, api.NameRoleSource); err != nil {
		return err
	}
	if err := editor.Include(api.NameRoleResolution); err != nil {
		return fmt.Errorf("include CinemaZ DVD resolution: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "REMUX") {
		if err := editor.Set(api.NameRoleVideoFormat, "DVD Remux"); err != nil {
			return fmt.Errorf("set CinemaZ DVD remux format: %w", err)
		}
		if err := editor.Include(api.NameRoleVideoFormat); err != nil {
			return fmt.Errorf("include CinemaZ DVD remux format: %w", err)
		}
		if err := editor.MoveBefore(api.NameRoleResolution, api.NameRoleVideoFormat); err != nil {
			return fmt.Errorf("move CinemaZ DVD resolution before format: %w", err)
		}
		if err := includeCinemaZDVDCodec(editor, meta); err != nil {
			return err
		}
		return moveVideoAfterAudio(editor)
	}
	if err := editor.MoveBefore(api.NameRoleResolution, api.NameRoleDVDSize); err != nil {
		return fmt.Errorf("move CinemaZ DVD resolution before size: %w", err)
	}
	if err := includeCinemaZDVDCodec(editor, meta); err != nil {
		return err
	}
	return moveVideoAfterAudio(editor)
}

func includeCinemaZDVDCodec(editor *trackers.NameEditor, meta api.UploadSubject) error {
	codec := cinemaZDVDVideo(meta.VideoCodec)
	if codec == "" {
		return nil
	}
	if err := editor.Include(api.NameRoleVideoCodec); err != nil {
		return fmt.Errorf("include CinemaZ DVD codec: %w", err)
	}
	if err := editor.Set(api.NameRoleVideoCodec, codec); err != nil {
		return fmt.Errorf("set CinemaZ DVD codec: %w", err)
	}
	return nil
}

func applyPHDNameDefaults(editor *trackers.NameEditor, meta api.UploadSubject) error {
	if err := omitNameComponents(editor, api.NameRoleAlternateTitle, api.NameRoleDubbed, api.NameRoleDualAudio); err != nil {
		return err
	}
	if err := normalizePHDEdition(editor); err != nil {
		return err
	}
	if isTV(meta) {
		if err := editor.Omit(api.NameRoleYear); err != nil {
			return fmt.Errorf("omit PHD TV year: %w", err)
		}
	}
	if meta.HasEncodeSettings {
		if err := normalizePHDVideoEncode(editor); err != nil {
			return err
		}
		if err := normalizePHDVideoCodec(editor); err != nil {
			return err
		}
	}
	if strings.EqualFold(strings.TrimSpace(meta.Type), "DVDRIP") {
		if err := editor.Omit(api.NameRoleSource); err != nil {
			return fmt.Errorf("omit PHD DVD rip source: %w", err)
		}
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		return nil
	}
	if err := omitNameComponents(editor, api.NameRoleRegion, api.NameRoleDVDSystem, api.NameRoleSource); err != nil {
		return err
	}
	if err := editor.Include(api.NameRoleResolution); err != nil {
		return fmt.Errorf("include PHD DVD resolution: %w", err)
	}
	if err := editor.MoveBefore(api.NameRoleResolution, api.NameRoleAudio); err != nil {
		return fmt.Errorf("move PHD DVD resolution before audio: %w", err)
	}
	if err := editor.Include(api.NameRoleVideoCodec); err != nil {
		return fmt.Errorf("include PHD DVD codec: %w", err)
	}
	return moveVideoAfterAudio(editor)
}

func omitNameComponents(editor *trackers.NameEditor, roles ...api.ReleaseNameRole) error {
	for _, role := range roles {
		if err := editor.Omit(role); err != nil {
			return fmt.Errorf("omit AZ-family %s: %w", role, err)
		}
	}
	return nil
}

func setNameComponent(editor *trackers.NameEditor, role api.ReleaseNameRole, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if err := editor.Set(role, value); err != nil {
		return fmt.Errorf("set AZ-family %s: %w", role, err)
	}
	return nil
}

func moveVideoAfterAudio(editor *trackers.NameEditor) error {
	for _, role := range []api.ReleaseNameRole{api.NameRoleVideoEncode, api.NameRoleVideoCodec} {
		if err := editor.MoveAfter(role, api.NameRoleAudio); err != nil {
			return fmt.Errorf("move AZ-family %s after audio: %w", role, err)
		}
	}
	return nil
}

func normalizePHDVideoEncode(editor *trackers.NameEditor) error {
	return normalizePHDVideoRole(editor, api.NameRoleVideoEncode)
}

func normalizePHDVideoCodec(editor *trackers.NameEditor) error {
	return normalizePHDVideoRole(editor, api.NameRoleVideoCodec)
}

func normalizePHDVideoRole(editor *trackers.NameEditor, role api.ReleaseNameRole) error {
	component, exists := editor.Component(role)
	if !exists || !component.Present {
		return nil
	}
	value := normalizePHDVideoValue(component.Value)
	if value == component.Value {
		return nil
	}
	if err := editor.Set(role, value); err != nil {
		return fmt.Errorf("set PHD %s label: %w", role, err)
	}
	return nil
}

func normalizePHDVideoValue(value string) string {
	value = phdH264Pattern.ReplaceAllString(value, "x264")
	return phdH265Pattern.ReplaceAllString(value, "x265")
}

func normalizeCinemaZEdition(editor *trackers.NameEditor) error {
	return normalizeNameComponent(editor, api.NameRoleEdition, func(value string) string {
		value = czLimitedPattern.ReplaceAllString(value, "")
		value = czCriterionPattern.ReplaceAllString(value, "")
		value = czResolutionPattern.ReplaceAllString(value, "")
		value = czAnniversaryPattern.ReplaceAllString(value, "")
		value = czExtendedPattern.ReplaceAllString(value, "EXT")
		value = czDirectorPattern.ReplaceAllString(value, "DC")
		value = czTheatricalPattern.ReplaceAllString(value, "TC")
		value = czUppercasePattern.ReplaceAllStringFunc(value, strings.ToUpper)
		return strings.Join(strings.Fields(value), " ")
	})
}

func normalizePHDEdition(editor *trackers.NameEditor) error {
	return normalizeNameComponent(editor, api.NameRoleEdition, func(value string) string {
		value = phdLimitedPattern.ReplaceAllString(value, "")
		value = phdCriterionPattern.ReplaceAllString(value, "")
		value = phdAnniversary.ReplaceAllString(value, "")
		value = phdDirectorPattern.ReplaceAllString(value, "DC")
		value = phdExtendedPattern.ReplaceAllString(value, "Extended")
		value = phdTheatricalPattern.ReplaceAllString(value, "Theatrical")
		return strings.Join(strings.Fields(value), " ")
	})
}

func normalizeUppercaseComponent(editor *trackers.NameEditor, role api.ReleaseNameRole) error {
	return normalizeNameComponent(editor, role, strings.ToUpper)
}

func normalizeNameComponent(editor *trackers.NameEditor, role api.ReleaseNameRole, normalize func(string) string) error {
	component, exists := editor.Component(role)
	if !exists || !component.Present {
		return nil
	}
	value := normalize(component.Value)
	if value == component.Value {
		return nil
	}
	if value == "" {
		if err := editor.Omit(role); err != nil {
			return fmt.Errorf("omit normalized AZ-family %s: %w", role, err)
		}
		return nil
	}
	if err := editor.Set(role, value); err != nil {
		return fmt.Errorf("normalize AZ-family %s: %w", role, err)
	}
	return nil
}

func applyAZFamilyGroupDefault(site string, editor *trackers.NameEditor, tag string) error {
	if normalized := normalizedReleaseGroup(tag); normalized != "" && !isNoGroupName(normalized) {
		return nil
	}
	switch site {
	case "AZ":
		if err := editor.Omit(api.NameRoleGroup); err != nil {
			return fmt.Errorf("omit AZ-family group: %w", err)
		}
		return nil
	case "CZ":
		if err := editor.Set(api.NameRoleGroup, "-NoGroup"); err != nil {
			return fmt.Errorf("set CinemaZ no-group suffix: %w", err)
		}
		if err := editor.Include(api.NameRoleGroup); err != nil {
			return fmt.Errorf("include CinemaZ no-group suffix: %w", err)
		}
		return nil
	default:
		if err := editor.Set(api.NameRoleGroup, "-NOGROUP"); err != nil {
			return fmt.Errorf("set PHD no-group suffix: %w", err)
		}
		if err := editor.Include(api.NameRoleGroup); err != nil {
			return fmt.Errorf("include PHD no-group suffix: %w", err)
		}
		return nil
	}
}

func avistaZEnglishTitle(meta api.UploadSubject) string {
	metadata := currentAZFamilyProviderMetadata(meta)
	provider := ""
	if isTV(meta) && metadata.TVDB != nil {
		provider = strings.TrimSpace(metadata.TVDB.NameEnglish)
	}
	if provider == "" && metadata.TMDB != nil {
		provider = strings.TrimSpace(metadata.TMDB.Title)
	}
	if provider == "" && metadata.IMDB != nil {
		provider = strings.TrimSpace(metadata.IMDB.Title)
	}
	return trackers.PreferredTitle(meta, provider)
}

func cinemaZTitle(meta api.UploadSubject) string {
	metadata := currentAZFamilyProviderMetadata(meta)
	original := cinemaZOriginalTitle(meta, metadata)
	if meta.EffectiveMetadata.OriginalTitleProvenance.IsManual() {
		if original == "" || !containsNonLatinLetter(original) {
			return original
		}
		if transliterated := transliterateCinemaZTitle(original); transliterated != "" && !containsNonLatinLetter(transliterated) {
			return transliterated
		}
		return ""
	}
	originalUsesNonLatin := containsNonLatinLetter(original)
	if title := cinemaZEnglishCountryAKA(metadata.IMDB, originalUsesNonLatin); title != "" {
		return title
	}
	if original == "" || !containsNonLatinLetter(original) {
		return original
	}
	for _, candidate := range cinemaZRomanizedCandidates(meta, metadata) {
		if candidate != "" && !containsNonLatinLetter(candidate) {
			return candidate
		}
	}
	if transliterated := transliterateCinemaZTitle(original); transliterated != "" && !containsNonLatinLetter(transliterated) {
		return transliterated
	}
	return ""
}

func cinemaZEnglishCountryAKA(metadata *api.IMDBMetadata, originalUsesNonLatin bool) string {
	if metadata == nil {
		return ""
	}
	for _, aka := range metadata.Akas {
		title, country := strings.TrimSpace(aka.Title), strings.TrimSpace(aka.Country)
		if title == "" || containsNonLatinLetter(title) || !isCinemaZCountry(country) || !isEnglishName(aka.Language) ||
			cinemaZAKAAttributesDisallowed(aka.Attributes, originalUsesNonLatin) {
			continue
		}
		return title
	}
	return ""
}

func isCinemaZCountry(country string) bool {
	country = strings.ToLower(strings.TrimSpace(country))
	if country == "" {
		return false
	}
	country = strings.NewReplacer("-", "", "_", "", " ", "").Replace(country)
	return !strings.Contains(country, "worldwide")
}

func cinemaZAKAAttributesDisallowed(attributes []string, originalUsesNonLatin bool) bool {
	for _, attribute := range attributes {
		attribute = strings.ToLower(strings.TrimSpace(attribute))
		switch {
		case strings.Contains(attribute, "informal"), strings.Contains(attribute, "working"), strings.Contains(attribute, "festival"):
			return true
		case strings.Contains(attribute, "transliter") && !originalUsesNonLatin:
			return true
		}
	}
	return false
}

func isEnglishName(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "en", "eng", "english":
		return true
	default:
		return false
	}
}

func cinemaZOriginalTitle(meta api.UploadSubject, metadata api.SourceScopedMetadata) string {
	provider := ""
	if metadata.IMDB != nil {
		provider = strings.TrimSpace(metadata.IMDB.AKA)
	}
	if provider == "" && metadata.TMDB != nil {
		provider = strings.TrimSpace(metadata.TMDB.OriginalTitle)
	}
	if provider == "" && isTV(meta) && metadata.TVDB != nil {
		provider = strings.TrimSpace(metadata.TVDB.Name)
	}
	return trackers.PreferredOriginalTitle(meta, provider)
}

func cinemaZRomanizedCandidates(meta api.UploadSubject, metadata api.SourceScopedMetadata) []string {
	candidates := make([]string, 0, 5)
	if metadata.TMDB != nil {
		candidates = append(candidates, trimAKAPrefix(metadata.TMDB.RetrievedAKA))
	}
	if metadata.IMDB != nil {
		candidates = append(candidates, strings.TrimSpace(metadata.IMDB.Title))
	}
	if metadata.TMDB != nil {
		candidates = append(candidates, strings.TrimSpace(metadata.TMDB.Title))
	}
	if isTV(meta) && metadata.TVDB != nil {
		candidates = append(candidates, strings.TrimSpace(metadata.TVDB.NameEnglish))
	}
	return append(candidates, strings.TrimSpace(meta.Release.Title))
}

func trimAKAPrefix(value string) string {
	value = strings.TrimSpace(value)
	if after, ok := strings.CutPrefix(value, "AKA "); ok {
		return strings.TrimSpace(after)
	}
	return value
}

func cinemaZDVDVideo(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "MPEG-2") || strings.EqualFold(value, "MPEG 2") {
		return "MPEG2"
	}
	return value
}

func isSeasonPack(meta api.UploadSubject) bool {
	return meta.TVPack || (meta.SeasonInt > 0 && meta.EpisodeInt == 0 && strings.TrimSpace(meta.EpisodeStr) == "")
}

func normalizedReleaseGroup(tag string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tag), "-"))
}

func isNoGroupName(tag string) bool {
	switch strings.ToLower(strings.TrimSpace(tag)) {
	case "nogrp", "nogroup", "unknown", "unk":
		return true
	default:
		return false
	}
}

func containsNonLatinLetter(value string) bool {
	for _, current := range value {
		if unicode.IsLetter(current) && !unicode.In(current, unicode.Latin) {
			return true
		}
	}
	return false
}

func transliterateCinemaZTitle(value string) string {
	var result strings.Builder
	for _, current := range value {
		replacement, ok := cinemaZTransliteration[unicode.ToLower(current)]
		if !ok {
			result.WriteRune(current)
			continue
		}
		if unicode.IsUpper(current) && replacement != "" {
			runes := []rune(replacement)
			runes[0] = unicode.ToUpper(runes[0])
			replacement = string(runes)
		}
		result.WriteString(replacement)
	}
	return strings.Join(strings.Fields(result.String()), " ")
}

func resolveSearchName(meta api.UploadSubject) string {
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		return strings.TrimSpace(trackers.PreferredTitle(meta, ""))
	}
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	if metadata := currentAZFamilyProviderMetadata(meta); metadata.TMDB != nil {
		if title := strings.TrimSpace(metadata.TMDB.Title); title != "" {
			return title
		}
	}
	return strings.TrimSpace(meta.Filename)
}

func currentAZFamilyProviderMetadata(meta api.UploadSubject) api.SourceScopedMetadata {
	if !meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity) {
		return api.SourceScopedMetadata{}
	}
	return meta.ProviderMetadata
}

var cinemaZTransliteration = map[rune]string{
	'а': "a",
	'б': "b",
	'в': "v",
	'г': "g",
	'д': "d",
	'е': "e",
	'ё': "yo",
	'ж': "zh",
	'з': "z",
	'и': "i",
	'й': "y",
	'к': "k",
	'л': "l",
	'м': "m",
	'н': "n",
	'о': "o",
	'п': "p",
	'р': "r",
	'с': "s",
	'т': "t",
	'у': "u",
	'ф': "f",
	'х': "kh",
	'ц': "ts",
	'ч': "ch",
	'ш': "sh",
	'щ': "shch",
	'ъ': "",
	'ы': "y",
	'ь': "",
	'э': "e",
	'ю': "yu",
	'я': "ya",
	'є': "ye",
	'і': "i",
	'ї': "yi",
	'ґ': "g",
	'α': "a",
	'β': "v",
	'γ': "g",
	'δ': "d",
	'ε': "e",
	'ζ': "z",
	'η': "i",
	'θ': "th",
	'ι': "i",
	'κ': "k",
	'λ': "l",
	'μ': "m",
	'ν': "n",
	'ξ': "x",
	'ο': "o",
	'π': "p",
	'ρ': "r",
	'σ': "s",
	'ς': "s",
	'τ': "t",
	'υ': "y",
	'φ': "f",
	'χ': "ch",
	'ψ': "ps",
	'ω': "o",
}
