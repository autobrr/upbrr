// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import "github.com/autobrr/upbrr/internal/trackers"

var nordic = []string{"english", "norwegian", "norsk", "no", "nb", "nn", "swedish", "sv", "danish", "da", "finnish", "fi", "icelandic", "is"}

func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{
		BlockSingleFileFolder: true,
		BlockHardcodedSubs:    true,
		BlockGroupUnlessType:  map[string][]string{"EVO": {"WEBDL"}},
		Language: &trackers.LanguageRule{
			Languages:    nordic,
			RequireAudio: true,
			RequireSubs:  true,
		},
	}
}
