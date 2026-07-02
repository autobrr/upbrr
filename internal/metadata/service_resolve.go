// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"sort"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

// serviceAlias carries a configured service alias and its normalized token span
// for deterministic filename matching.
type serviceAlias struct {
	name    string
	service string
	tokens  []string
}

// resolveService returns the normalized service token, display name, and source
// filename for metadata naming. Existing metadata service values win over
// filename-derived matches.
func resolveService(meta api.PreparedMetadata) (string, string, string) {
	services := serviceCodeMap()
	filename := strings.TrimSpace(meta.Filename)
	if filename == "" {
		filename = pathutil.Base(meta.SourcePath)
	}
	cleaned := filename
	if tag := strings.TrimSpace(meta.Tag); tag != "" {
		cleaned = strings.ReplaceAll(cleaned, tag, "")
	}
	if audio := strings.TrimSpace(meta.Audio); strings.Contains(audio, "DTS-HD MA") {
		cleaned = strings.ReplaceAll(cleaned, "DTS-HD.MA.", "")
		cleaned = strings.ReplaceAll(cleaned, "DTS-HD MA ", "")
	}

	service := strings.TrimSpace(meta.Service)
	if service == "" {
		service = resolveFilenameService(cleaned, services)
	}

	longName := ""
	if service != "" {
		for key, value := range services {
			if value == service {
				if len(key) > len(longName) {
					longName = key
				}
			}
		}
		if longName == "" {
			longName = service
		}
	}

	return service, longName, filename
}

// resolveFilenameService finds a service alias only when it is immediately
// before a WEB source marker, matching release-name service position and
// avoiding title or episode-name words.
func resolveFilenameService(filename string, services map[string]string) string {
	tokens := serviceMatchTokens(filename)
	if len(tokens) == 0 {
		return ""
	}

	aliases := orderedServiceAliases(services)
	for idx := range tokens {
		if !isWebSourceTokenAt(tokens, idx) {
			continue
		}
		for _, alias := range aliases {
			start := idx - len(alias.tokens)
			if start < 0 {
				continue
			}
			if matchTokenSpan(tokens[start:idx], alias.tokens) {
				return alias.service
			}
		}
	}
	return ""
}

// resolveServiceValue normalizes an exact service alias or service token from
// trusted metadata such as an NFO field.
func resolveServiceValue(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", ""
	}

	services := serviceCodeMap()
	for key, service := range services {
		if strings.EqualFold(strings.TrimSpace(key), trimmed) {
			return service, serviceLongName(service, services)
		}
	}
	for _, service := range services {
		if strings.EqualFold(strings.TrimSpace(service), trimmed) {
			return service, serviceLongName(service, services)
		}
	}
	return "", ""
}

// orderedServiceAliases returns deterministic alias candidates, preferring more
// specific aliases before shorter aliases that share the same suffix position.
func orderedServiceAliases(services map[string]string) []serviceAlias {
	aliases := make([]serviceAlias, 0, len(services))
	for name, service := range services {
		tokens := serviceMatchTokens(name)
		if len(tokens) == 0 {
			continue
		}
		aliases = append(aliases, serviceAlias{name: name, service: service, tokens: tokens})
	}
	sort.Slice(aliases, func(i, j int) bool {
		iWeight := serviceAliasTokenWeight(aliases[i])
		jWeight := serviceAliasTokenWeight(aliases[j])
		if iWeight != jWeight {
			return iWeight > jWeight
		}
		if len(aliases[i].tokens) != len(aliases[j].tokens) {
			return len(aliases[i].tokens) > len(aliases[j].tokens)
		}
		if len(aliases[i].name) != len(aliases[j].name) {
			return len(aliases[i].name) > len(aliases[j].name)
		}
		return aliases[i].name < aliases[j].name
	})
	return aliases
}

// serviceAliasTokenWeight measures alias specificity by normalized token
// length, keeping multi-character service names ahead of shorter overlaps.
func serviceAliasTokenWeight(alias serviceAlias) int {
	weight := 0
	for _, token := range alias.tokens {
		weight += len(token)
	}
	return weight
}

// serviceMatchTokens lowercases value and splits it on release-name separators
// while preserving service-significant characters such as plus and ampersand.
func serviceMatchTokens(value string) []string {
	fields := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+' && r != '&'
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		tokens = append(tokens, field)
	}
	return tokens
}

// isWebSourceTokenAt reports whether tokens at idx form a WEB-DL or WEBRip
// marker, including compact and split filename forms.
func isWebSourceTokenAt(tokens []string, idx int) bool {
	if idx < 0 || idx >= len(tokens) {
		return false
	}
	switch tokens[idx] {
	case "webdl", "webrip":
		return true
	case "web":
		if idx+1 >= len(tokens) {
			return false
		}
		return tokens[idx+1] == "dl" || tokens[idx+1] == "rip"
	default:
		return false
	}
}

// matchTokenSpan reports whether a candidate filename token span exactly equals
// an alias token span.
func matchTokenSpan(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for idx := range want {
		if got[idx] != want[idx] {
			return false
		}
	}
	return true
}

// serviceLongName returns the longest configured alias for a service token,
// using lexical order to break equal-length ties deterministically.
func serviceLongName(service string, services map[string]string) string {
	longName := ""
	for key, value := range services {
		if value != service {
			continue
		}
		if len(key) > len(longName) || len(key) == len(longName) && key < longName {
			longName = key
		}
	}
	if longName == "" {
		longName = service
	}
	return longName
}
