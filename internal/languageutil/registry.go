// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package languageutil

import (
	_ "embed"
	"strings"
)

//go:embed language_registry.tsv
var languageRegistry string

type languageRecord struct {
	scope     string
	preferred string
	names     []string
}

var registryLanguages = loadLanguageRegistry()

func loadLanguageRegistry() map[string]languageRecord {
	records := make(map[string]languageRecord)
	for line := range strings.SplitSeq(languageRegistry, "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		code, rest, _ := strings.Cut(line, "\t")
		scope, rest, _ := strings.Cut(rest, "\t")
		preferred, names, _ := strings.Cut(rest, "\t")
		records[code] = languageRecord{
			scope:     scope,
			preferred: preferred,
			names:     strings.Split(names, "|"),
		}
	}
	return records
}
