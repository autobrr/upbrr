// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package languageutil

import (
	"slices"
	"strings"
	"sync"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
	"golang.org/x/text/unicode/norm"
)

var (
	languageNameMapOnce sync.Once
	languageNameMap     map[string]string
)

// NormalizeLanguageDisplay returns the complete English base-language label.
// Region, script and subtitle coverage are omitted only from this projection.
// Blank and unresolved input returns empty.
func NormalizeLanguageDisplay(value string) string {
	if tag, ok := LanguageTag(value); ok {
		return languageDisplayName(tag)
	}
	return ""
}

// LanguageTag resolves supported codes and complete names without CLDR legacy
// grouping. Explicit regions and scripts survive; subtitle coverage is separate.
// Unknown/custom values and tags without an identified base return false.
func LanguageTag(value string) (language.Tag, bool) {
	base, _ := SubtitleLanguageParts(value)
	return resolveCompleteLanguageTag(base)
}

// NormalizeLanguageCode returns the ISO base identity used for language matching.
// Explicit variants and subtitle coverage do not change that base. Unresolved
// values return empty; und, mul and zxx retain their distinct special codes.
func NormalizeLanguageCode(value string) string {
	if tag, ok := LanguageTag(value); ok {
		base, _, _ := tag.Raw()
		return base.String()
	}
	return ""
}

// NormalizeLanguageList retains full identities and qualifiers, preserves unknown
// nonempty values, and removes equivalent duplicates in order. Complete supported
// names are resolved before falling back to comma-separated list syntax.
func NormalizeLanguageList(values []string) []string {
	return normalizeLanguageList(values, NormalizeLanguageLabel)
}

// NormalizeLanguageNames interprets human-name fields such as BDInfo language
// columns, retaining lowercase codes. It distinguishes Ga/Kru from ga/kru.
func NormalizeLanguageNames(values []string) []string {
	return normalizeLanguageList(values, NormalizeLanguageName)
}

func normalizeLanguageList(values []string, normalize func(string) string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		parts := []string{value}
		if normalize(value) == "" {
			parts = strings.Split(value, ",")
		}
		for _, part := range parts {
			label := strings.TrimSpace(part)
			if label == "" {
				continue
			}
			if normalized := normalize(label); normalized != "" {
				label = normalized
			}
			key := languageNameKey(label)
			if !seen[key] {
				seen[key] = true
				result = append(result, label)
			}
		}
	}
	return result
}

// NormalizeLanguageLabel returns a full English label retaining explicit tag
// variants and Full/Forced coverage. Unknown values return empty so list callers
// can preserve their original text without inventing a language.
func NormalizeLanguageLabel(value string) string {
	base, coverage := SubtitleLanguageParts(value)
	tag, ok := resolveCompleteLanguageTag(base)
	if !ok {
		return ""
	}
	return qualifiedLanguageLabel(tag, coverage)
}

// NormalizeLanguageName resolves lowercase codes before human names, preserving
// Ga/Kru as names and ga/kru as codes. Ambiguous labels retain canonical tags.
func NormalizeLanguageName(value string) string {
	base, coverage := SubtitleLanguageParts(value)
	if base == strings.ToLower(base) {
		if tag, ok := parseLanguageCode(base); ok {
			return qualifiedLanguageLabel(tag, coverage)
		}
	}
	languageNameMapOnce.Do(buildLanguageNameMap)
	if code := languageNameMap[languageNameKey(base)]; code != "" {
		if tag, ok := parseLanguageCode(code); ok {
			return qualifiedLanguageLabel(tag, coverage)
		}
	}
	return NormalizeLanguageLabel(value)
}

func qualifiedLanguageLabel(tag language.Tag, coverage string) string {
	label := languageLabel(tag)
	if coverage != "" {
		label += " (" + coverage + ")"
	}
	return label
}

// SubtitleLanguageParts separates explicit coverage without discarding custom
// text. Bare suffixes require a supported language; parentheses and hyphens also
// qualify custom names.
func SubtitleLanguageParts(value string) (string, string) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, coverage := range []string{"Full", "Forced"} {
		mode := strings.ToLower(coverage)
		for _, suffix := range []string{" (" + mode + ")", " - " + mode, " " + mode} {
			if strings.HasSuffix(lower, suffix) {
				base := strings.TrimSpace(value[:len(value)-len(suffix)])
				if suffix == " "+mode {
					if _, recognized := resolveCompleteLanguageTag(base); !recognized {
						continue
					}
				}
				return base, coverage
			}
		}
	}
	return value, ""
}

// KnownLanguageLabels returns sorted base-language suggestions. The suggestions
// do not restrict custom corrections or explicit regional/script tags.
func KnownLanguageLabels() []string {
	languageNameMapOnce.Do(buildLanguageNameMap)
	seen := make(map[string]bool)
	for _, tag := range display.Supported.Tags() {
		base, _, _ := tag.Raw()
		if resolved, ok := parseLanguageCode(base.String()); ok {
			seen[languageLabel(resolved)] = true
		}
	}
	for code, record := range registryLanguages {
		if record.scope == "collection" {
			if tag, ok := parseLanguageCode(code); ok {
				seen[languageLabel(tag)] = true
			}
		}
	}
	labels := make([]string, 0, len(seen))
	for label := range seen {
		if label != "" {
			labels = append(labels, label)
		}
	}
	slices.Sort(labels)
	return labels
}

func languageDisplayName(tag language.Tag) string {
	base, _, _ := tag.Raw()
	code := base.String()
	switch code {
	case "mul":
		return "Multiple Languages"
	case "zxx":
		return "ZXX"
	case "und":
		return "Undetermined"
	}
	record, ok := registryLanguages[code]
	if !ok {
		return ""
	}
	grouped, _ := language.All.Canonicalize(language.Raw.Make(code))
	groupBase, _, _ := grouped.Raw()
	if record.scope == "collection" || record.scope == "macrolanguage" || groupBase.String() != code {
		return record.names[0]
	}
	if name := strings.TrimSpace(display.English.Languages().Name(language.Raw.Make(code))); name != "" {
		return name
	}
	return record.names[0]
}

func languageLabel(tag language.Tag) string {
	label := languageDisplayName(tag)
	base, _, _ := tag.Raw()
	nameTag, _ := parseLanguageCode(label)
	languageNameMapOnce.Do(buildLanguageNameMap)
	nameCode := languageNameMap[languageNameKey(label)]
	if tag.String() != base.String() || nameTag != language.Und && nameTag.String() != base.String() || nameCode != base.String() {
		label += " [" + tag.String() + "]"
	}
	return label
}

func resolveCompleteLanguageTag(value string) (language.Tag, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return language.Tag{}, false
	}
	tag, ok := parseLanguageCode(value)
	if ok {
		return tag, true
	}
	if tag != language.Und {
		return language.Tag{}, false
	}
	languageNameMapOnce.Do(buildLanguageNameMap)
	if code := languageNameMap[languageNameKey(value)]; code != "" {
		return parseLanguageCode(code)
	}
	if index := strings.LastIndex(value, " ["); index >= 0 && strings.HasSuffix(value, "]") {
		prefix, code := value[:index], value[index+2:len(value)-1]
		if tag, valid := parseLanguageCode(code); valid && languageNameKey(prefix) == languageNameKey(languageDisplayName(tag)) {
			return tag, true
		}
	}
	return language.Tag{}, false
}

func parseLanguageCode(value string) (language.Tag, bool) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "_", "-")
	if unresolvedLegacyTags[strings.ToLower(value)] {
		return language.Tag{}, false
	}
	base, rest, hasRest := strings.Cut(value, "-")
	if replacement := bibliographicAliases[strings.ToLower(base)]; replacement != "" {
		base = replacement
	}
	if preferred := registryLanguages[strings.ToLower(base)].preferred; preferred != "" {
		base = preferred
	}
	value = base
	if hasRest {
		value += "-" + rest
	}
	tag, err := language.Deprecated.Parse(value)
	if err != nil {
		return language.Tag{}, false
	}
	baseLanguage, _, _ := tag.Raw()
	if baseLanguage.String() == "und" && tag.String() != "und" {
		return language.Tag{}, false
	}
	return tag, languageDisplayName(tag) != ""
}

func languageNameKey(value string) string {
	return strings.ToLower(norm.NFC.String(strings.Join(strings.Fields(value), " ")))
}

func buildLanguageNameMap() {
	languageNameMap = make(map[string]string)
	add := func(name, code string) {
		key := languageNameKey(name)
		if previous, exists := languageNameMap[key]; exists && previous != code {
			languageNameMap[key] = ""
		} else {
			languageNameMap[key] = code
		}
	}
	for code, record := range registryLanguages {
		if tag, ok := parseLanguageCode(code); ok {
			add(languageDisplayName(tag), tag.String())
			for _, name := range record.names {
				add(name, tag.String())
			}
		}
	}
	for _, tag := range display.Supported.Tags() {
		base, _, _ := tag.Raw()
		if tag.String() != base.String() {
			add(display.English.Tags().Name(tag), tag.String())
		}
	}
	for alias, code := range languageAliases {
		if tag, ok := parseLanguageCode(code); ok {
			add(alias, tag.String())
		}
	}
}
