// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"reflect"
	"testing"
)

func TestPreparedReleaseMissingRequiredUploadFacts(t *testing.T) {
	complete := PreparedRelease{
		Identity: ExternalIdentity{Category: CanonicalCategoryMovie},
		Media:    MediaFacts{Type: "ENCODE", Source: "BluRay"},
		Naming: NamingFacts{
			Type:       "ENCODE",
			Source:     "BluRay",
			Resolution: "720p",
		},
	}
	for _, test := range []struct {
		name    string
		missing []string
	}{
		{name: "complete"},
		{name: "category", missing: []string{"category"}},
		{name: "type", missing: []string{"type"}},
		{name: "source", missing: []string{"source"}},
		{name: "resolution", missing: []string{"resolution"}},
		{name: "multiple", missing: []string{"category", "source"}},
		{name: "all", missing: []string{"category", "type", "source", "resolution"}},
	} {
		for _, empty := range []string{"", " \t\n"} {
			t.Run(test.name+empty, func(t *testing.T) {
				release := complete
				// A suggestive release name is not authority for absent saved facts.
				release.Naming.ReleaseName = "Example.2026.720p.BluRay.x264-GRP"
				for _, field := range test.missing {
					switch field {
					case "category":
						release.Identity.Category = CanonicalCategory(empty)
					case "type":
						release.Media.Type = empty
					case "source":
						release.Media.Source = empty
					case "resolution":
						release.Naming.Resolution = empty
					}
				}
				if got := release.MissingRequiredUploadFacts(); !reflect.DeepEqual(got, test.missing) {
					t.Fatalf("missing facts = %v, want %v", got, test.missing)
				}
			})
		}
	}
}

func TestPreparedReleaseUnknownCategoryIsMissing(t *testing.T) {
	release := PreparedRelease{
		Identity: ExternalIdentity{Category: CanonicalCategoryUnknown},
		Media:    MediaFacts{Type: "ENCODE", Source: "BluRay"},
		Naming: NamingFacts{
			Type:       "ENCODE",
			Source:     "BluRay",
			Resolution: "720p",
		},
	}
	if got := release.MissingRequiredUploadFacts(); !reflect.DeepEqual(got, []string{"category"}) {
		t.Fatalf("unknown category missing facts = %v", got)
	}
}

func TestRequiredUploadFactsUseMediaRatherThanNamingEvidence(t *testing.T) {
	release := PreparedRelease{
		Identity: ExternalIdentity{Category: CanonicalCategoryMovie},
		Naming: NamingFacts{
			Type:       "ENCODE",
			Source:     "BluRay",
			Resolution: "720p",
		},
	}
	if got := release.MissingRequiredUploadFacts(); !reflect.DeepEqual(got, []string{"type", "source"}) {
		t.Fatalf("naming evidence substituted for missing upload facts: %v", got)
	}
	release.Media = MediaFacts{Type: "ENCODE", Source: "BluRay"}
	release.Naming.Type, release.Naming.Source = "", ""
	if got := release.MissingRequiredUploadFacts(); len(got) != 0 {
		t.Fatalf("present upload facts rejected because naming evidence is absent: %v", got)
	}
}
