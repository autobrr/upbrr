// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAZFamilyOrdinaryCompatibilityDoesNotCountAsExtraProgramme(t *testing.T) {
	for _, site := range []string{"AZ", "CZ"} {
		for _, reverse := range []bool{false, true} {
			subject := api.TrackerValidationSubject{
				Tracker:       site,
				Type:          "REMUX",
				LanguageFacts: azTestLanguageFacts("English", "English", "English"),
			}
			subject.LanguageFacts.Tracks[0].Codec = "TrueHD"
			subject.LanguageFacts.Tracks[1].Codec = "DD"
			if reverse {
				slices.Reverse(subject.LanguageFacts.Tracks)
			}
			if failures := evaluateAZLanguageRules(siteFor(site), subject); len(failures) != 0 {
				t.Fatalf("%s: ordinary companion rejected: %+v", site, failures)
			}
			for i := range subject.LanguageFacts.Tracks {
				if subject.LanguageFacts.Tracks[i].Codec == "DD" {
					subject.LanguageFacts.Tracks[i].Codec = "DD+"
				}
			}
			failures := evaluateAZLanguageRules(siteFor(site), subject)
			if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == "language_compatibility_format" && f.Disposition == api.RuleDispositionStrict
			}) {
				t.Fatalf("%s lost AC-3-only rule: %+v", site, failures)
			}
		}
	}
}
