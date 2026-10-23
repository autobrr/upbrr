// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnresolvedProgrammeLanguagesPreservesDistinctEvidence(t *testing.T) {
	for _, test := range []struct {
		name      string
		role      api.AudioTrackRole
		languages []string
		manifest  string
		want      bool
	}{
		{"unknown programme", api.AudioRoleProgramme, nil, "manifest", true},
		{"unknown mix", api.AudioRoleAlternateMix, []string{"und"}, "manifest", true},
		{"unidentified multilingual", api.AudioRoleProgramme, []string{"mul"}, "manifest", true},
		{"known multilingual", api.AudioRoleProgramme, []string{"Japanese", "English"}, "manifest", false},
		{"non-linguistic", api.AudioRoleProgramme, []string{"ZXX"}, "manifest", false},
		{"commentary", api.AudioRoleCommentary, nil, "manifest", false},
		{"unknown role", "", nil, "manifest", false},
		{"missing manifest", api.AudioRoleProgramme, nil, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := api.LanguageFacts{Tracks: []api.MediaTrackFacts{{
				ID:                  "track",
				Kind:                api.MediaTrackAudio,
				Role:                test.role,
				Languages:           test.languages,
				ManifestFingerprint: test.manifest,
			}}}
			if got := UnresolvedProgrammeLanguages(facts); (len(got) == 1) != test.want {
				t.Fatalf("correction tracks = %#v", got)
			}
		})
	}
	if tracks := UnresolvedProgrammeLanguages(api.LanguageFacts{AudioAbsent: true}); len(tracks) != 0 {
		t.Fatal("invented absent audio")
	}
}
