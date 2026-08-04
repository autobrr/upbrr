// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package utp

import (
	"github.com/autobrr/upbrr/internal/trackers"
)

// Rules returns UTP's release eligibility requirements. UTP prescribes its own
// space-delimited release naming (utp.to/pages/33, see buildName), so renaming
// away from the dotted scene/P2P name is expected there rather than a
// violation, and the rename signals do not apply.
func Rules() *trackers.RuleSet {
	return &trackers.RuleSet{SkipModifiedReleaseCheck: true}
}

// AudioPolicy allows Ukrainian and English as additional audio languages: UTP
// releases always carry Ukrainian plus the original audio and optionally
// English, so those tracks must not count as bloat.
func AudioPolicy() *trackers.AudioPolicy {
	return &trackers.AudioPolicy{AllowedLanguages: []string{"ukrainian", "english"}}
}
