// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDescriptionValidationDoesNotRequireScreenshotsBeforeGeneration(t *testing.T) {
	meta := api.NewTrackerValidationSubject(oeTestSubject(), "OE")
	meta.AssetFacts = api.AssetFacts{Status: api.MetadataEvidenceStatusComplete}
	failures, err := checkDescriptionRequirements(t.Context(), meta, api.NopLogger{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("pre-dupe validation blocked screenshot generation: %v %v", failures, err)
	}
}

func TestDescriptionValidationRejectsRemovedFinalEvidence(t *testing.T) {
	meta := oeTestSubject()
	description, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "notes", nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, remove, rule string }{
		{"settings", "SVT-AV1 preset=4 crf=20", "oe_av1_encoding_settings_description"},
		{"source", "Example BluRay source; original HDR10 only", "oe_sm737_source_notes_description"},
		{"screenshot", "[url=https://images.example/two.png][img=350]https://images.example/two.png[/img][/url]", "oe_description_screenshots"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta.DescriptionGroupsFinal = true
			meta.DescriptionOverride = strings.ReplaceAll(description, tc.remove, "")
			failures, err := checkDescriptionRequirements(t.Context(), api.NewTrackerValidationSubject(meta, "OE"), api.NopLogger{})
			if err != nil || len(failures) != 1 || failures[0].Rule != tc.rule || failures[0].Disposition != api.RuleDispositionStrict {
				t.Fatalf("removed evidence must block upload: %+v %v", failures, err)
			}
		})
	}
	meta.DescriptionOverride = strings.Repeat("[url=https://images.example/one][img=350]https://images.example/one.png[/img][/url]", 3)
	meta.Type = "WEBDL"
	meta.Tag = "GRP"
	failures, err := checkDescriptionRequirements(t.Context(), api.NewTrackerValidationSubject(meta, "OE"), api.NopLogger{})
	if err != nil || len(failures) != 1 || failures[0].Rule != "oe_description_screenshots" {
		t.Fatalf("repeated image must not meet minimum: %+v %v", failures, err)
	}
}
