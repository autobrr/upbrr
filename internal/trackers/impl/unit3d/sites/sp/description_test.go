// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSPVariationExplanationSurvivesPreparedDescription(t *testing.T) {
	meta := collectedPackSubject()
	meta.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
	answerPackQuestion(&meta, packVariationKey, "yes")
	const explanation = "Episode 2 retains the German audio of its original source."
	answerPackQuestion(&meta, packExplanationKey, explanation)
	profile := Profile()
	if profile.DescriptionGroup != "sp" || profile.Site.FinalizeDescription == nil {
		t.Fatal("SP description exception is not wired")
	}
	meta.DescriptionOverride = profile.Site.FinalizeDescription("Release notes", meta)
	if strings.Count(meta.DescriptionOverride, explanation) != 1 || profile.Site.FinalizeDescription(meta.DescriptionOverride, meta) != meta.DescriptionOverride {
		t.Fatalf("description omitted or duplicated source evidence: %s", meta.DescriptionOverride)
	}
	meta.DescriptionGroupsFinal = true
	if failures := packUniformityFailures(api.NewTrackerValidationSubject(meta, "SP")); slices.ContainsFunc(failures, func(failure api.RuleFailure) bool { return failure.Disposition != api.RuleDispositionAdvisory }) {
		t.Fatal(failures)
	}
	meta.DescriptionOverride = "A later manual description override removed the evidence."
	requirePackBlocked(t, meta, api.MetadataEvidenceStatusComplete)
	if got := profile.Site.FinalizeDescription(meta.DescriptionOverride, meta); !strings.Contains(got, explanation) {
		t.Fatal("rebuilding a removed explanation could not repair the description")
	}
	meta.Type = "DISC"
	if got := profile.Site.FinalizeDescription("Disc notes", meta); got != "Disc notes" {
		t.Fatalf("full-disc description changed: %s", got)
	}
}

func TestSPFinalGroupUsesActualEditedDescription(t *testing.T) {
	for _, globalFinal := range []bool{false, true} {
		meta := collectedPackSubject()
		meta.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
		answerPackQuestion(&meta, packVariationKey, "yes")
		const explanation = "Episode 2 retains its original German source audio."
		answerPackQuestion(&meta, packExplanationKey, explanation)
		meta.DescriptionOverride = explanation
		meta.DescriptionGroupsFinal = globalFinal
		meta.DescriptionGroups = []api.DescriptionBuilderGroup{
			{
				GroupKey:       "sp",
				Trackers:       []string{"SP"},
				Description:    explanation,
				RawDescription: "Edited description without the required source explanation.",
				Final:          true,
			},
			{
				GroupKey:       "other",
				Trackers:       []string{"OTHER"},
				RawDescription: "Unfinished description",
			},
		}
		requirePackBlocked(t, meta, api.MetadataEvidenceStatusComplete)
		meta.DescriptionGroups[0].RawDescription = "Current description: " + explanation
		if failures := packUniformityFailures(api.NewTrackerValidationSubject(meta, "SP")); slices.ContainsFunc(failures, func(failure api.RuleFailure) bool { return failure.Disposition != api.RuleDispositionAdvisory }) {
			t.Fatalf("globalFinal=%v: current group source retained the explanation: %#v", globalFinal, failures)
		}
	}
}
