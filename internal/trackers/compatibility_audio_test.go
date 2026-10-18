// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCompatibilityAssignmentReviewKeepsResourcesIndependent(t *testing.T) {
	required := make([]api.MediaTrackFacts, 0, 13)
	companions := make([]CompatibilityCompanion, 0, 36)
	for i := range 12 {
		resource := "resource-" + strconv.Itoa(i)
		main, first, second := resource+"-main", resource+"-dd-a", resource+"-dd-b"
		required = append(required, api.MediaTrackFacts{ID: main, ResourceID: resource})
		companions = append(companions,
			CompatibilityCompanion{
				ID:         first,
				ResourceID: resource,
				Mixes:      []string{main},
				CanCover:   true,
				Optional:   true,
			},
			CompatibilityCompanion{
				ID:         second,
				ResourceID: resource,
				Mixes:      []string{main},
				CanCover:   true,
				Optional:   true,
			},
			CompatibilityCompanion{
				ID:         resource + "-aac",
				ResourceID: resource,
				Mixes:      []string{main, first},
			},
		)
	}
	failures := []api.RuleFailure{NewRuleFailure("language_compatibility_count", "conflicting assignments", api.RuleDispositionStrict)}
	if !ReviewCompatibilityAssignments(failures, required, companions, true) {
		t.Fatal("independent repairable resources lost their corrections")
	}
	resource := "resource-11"
	alternate := resource + "-alternate"
	required = append(required, api.MediaTrackFacts{ID: alternate, ResourceID: resource})
	for i := len(companions) - 3; i < len(companions); i++ {
		companions[i].Mixes = append(companions[i].Mixes, alternate)
	}
	failures[0].QuestionnaireInput = false
	if ReviewCompatibilityAssignments(failures, required, companions, true) || failures[0].QuestionnaireInput {
		t.Fatal("another resource or coupled source roles supplied impossible coverage")
	}
}

func TestPendingCompatibilityAssignmentsRequireEnoughCompanions(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			failures := []api.RuleFailure{
				NewQuestionnaireRuleFailure("language_compatibility_missing", "main mix needs coverage"),
				NewQuestionnaireRuleFailure("language_compatibility_missing", "alternate mix needs coverage"),
			}
			required := []api.MediaTrackFacts{{ID: "main"}, {ID: "alternate"}}
			companions := make([]CompatibilityCompanion, count)
			for i := range companions {
				companions[i] = CompatibilityCompanion{Mixes: []string{"main", "alternate"}, CanCover: true}
			}
			want := count == 2
			if got := ReviewCompatibilityAssignments(failures, required, companions, false); got != want {
				t.Fatalf("pending coverage review=%t want=%t: %+v", got, want, failures)
			}
			for _, failure := range failures {
				if failure.QuestionnaireInput != want || failure.Disposition != api.RuleDispositionStrict {
					t.Fatalf("pending association overrode measured capacity or strict execution: %+v", failures)
				}
			}
		})
	}
}

func TestCompatibilityFormatReviewRequiresMeasuredRecovery(t *testing.T) {
	for _, marked := range []bool{false, true} {
		for _, covered := range []bool{false, true} {
			failures := []api.RuleFailure{NewRuleFailure("language_compatibility_format", "codec restriction", api.RuleDispositionStrict)}
			failures[0].QuestionnaireInput = marked
			var companions []CompatibilityCompanion
			if covered {
				companions = []CompatibilityCompanion{{
					ID:       "ac3",
					Mixes:    []string{"main"},
					CanCover: true,
				}}
			}
			want := marked && covered
			if got := ReviewCompatibilityAssignments(failures, []api.MediaTrackFacts{{ID: "main"}}, companions, true); got != want ||
				failures[0].QuestionnaireInput != want || failures[0].Disposition != api.RuleDispositionStrict {
				t.Fatalf("format review must require site permission and physical coverage: marked=%t covered=%t review=%t failures=%+v", marked, covered, got, failures)
			}
		}
	}
}

func TestCompatibilityAssignmentConflictsRequireMeasuredRecovery(t *testing.T) {
	for _, test := range []struct {
		name       string
		required   []string
		companions []CompatibilityCompanion
		onePerMix  bool
		want       bool
	}{
		{
			name:     "reassign multiple competing companions",
			required: []string{"main", "alternate", "commentary"},
			want:     true,
			companions: []CompatibilityCompanion{
				{Mixes: []string{"main", "alternate", "commentary"}, CanCover: true},
				{Mixes: []string{"main", "alternate"}, CanCover: true},
				{Mixes: []string{"main"}, CanCover: true},
			},
		},
		{
			name:       "physical companion missing",
			required:   []string{"main", "alternate"},
			companions: []CompatibilityCompanion{{Mixes: []string{"main", "alternate"}, CanCover: true}},
		},
		{
			name:     "different resource or language cannot cover mix",
			required: []string{"main", "alternate"},
			companions: []CompatibilityCompanion{
				{Mixes: []string{"main"}, CanCover: true}, {Mixes: []string{"main"}, CanCover: true},
			},
		},
		{
			name:       "embedded core cannot supply required standalone coverage",
			required:   []string{"main"},
			companions: []CompatibilityCompanion{{Mixes: []string{"main"}}},
		},
		{
			name:      "HHD ordinary stream can remain a source parent",
			required:  []string{"main"},
			onePerMix: true,
			want:      true,
			companions: []CompatibilityCompanion{
				{
					ID:       "dd-a",
					Mixes:    []string{"main"},
					CanCover: true,
					Optional: true,
				},
				{
					ID:       "dd-b",
					Mixes:    []string{"main"},
					CanCover: true,
					Optional: true,
				},
				{ID: "aac", Mixes: []string{"main", "dd-a"}},
			},
		},
		{
			name:      "HHD stream cannot be both source parent and companion",
			required:  []string{"main", "alternate"},
			onePerMix: true,
			companions: []CompatibilityCompanion{
				{
					ID:       "dd-a",
					Mixes:    []string{"main", "alternate"},
					CanCover: true,
					Optional: true,
				},
				{
					ID:       "dd-b",
					Mixes:    []string{"main", "alternate"},
					CanCover: true,
					Optional: true,
				},
				{ID: "aac", Mixes: []string{"main", "alternate", "dd-a"}},
			},
		},
		{
			name:      "HHD excess explicit companions",
			required:  []string{"main"},
			onePerMix: true,
			companions: []CompatibilityCompanion{
				{Mixes: []string{"main"}, CanCover: true}, {Mixes: []string{"main"}, CanCover: true},
			},
		},
		{
			name:      "HHD ordinary stream can remain noncompatibility",
			required:  []string{"main"},
			onePerMix: true,
			want:      true,
			companions: []CompatibilityCompanion{
				{
					Mixes:    []string{"main"},
					CanCover: true,
					Optional: true,
				}, {Mixes: []string{"main"}, CanCover: true},
			},
		},
		{
			name:      "HHD reassignment preserves required standalone coverage",
			required:  []string{"main"},
			onePerMix: true,
			want:      true,
			companions: []CompatibilityCompanion{
				{Mixes: []string{"main", "DTS"}, CanCover: true}, {Mixes: []string{"DTS"}},
			},
		},
		{
			name:      "HHD WEB substitute cannot displace mandatory AC3",
			required:  []string{"main"},
			onePerMix: true,
			companions: []CompatibilityCompanion{
				{Mixes: []string{"main", "DTS"}, CanCover: true}, {Mixes: []string{"main"}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			failures := []api.RuleFailure{
				NewRuleFailure("language_compatibility_missing", "uncovered mix", api.RuleDispositionStrict),
				NewRuleFailure("language_compatibility_count", "duplicate assignment", api.RuleDispositionStrict),
				NewRuleFailure("language_compatibility_evidence", "unknown codec", api.RuleDispositionStrict),
			}
			var required []api.MediaTrackFacts
			for _, id := range test.required {
				required = append(required, api.MediaTrackFacts{ID: id})
			}
			if got := ReviewCompatibilityAssignments(failures, required, test.companions, test.onePerMix); got != test.want {
				t.Fatalf("review=%t want=%t: %+v", got, test.want, failures)
			}
			for i, failure := range failures {
				if failure.QuestionnaireInput != (test.want && i < 2) || failure.Disposition != api.RuleDispositionStrict {
					t.Fatalf("assignment recovery changed independent evidence or execution: %+v", failures)
				}
			}
		})
	}
}

func TestAutomaticCompatibilityPreservesEvidenceBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*api.MediaTrackFacts, *[]api.MediaTrackFacts)
		want   bool
	}{
		{name: "ordinary same language", want: true},
		{
			name:   "known alias set",
			want:   true,
			mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Languages = []string{"eng", "English"} },
		},
		{
			name: "legacy compatibility aggregation flag",
			want: true,
			mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) {
				c.Role = api.AudioRoleCompatibility
				c.Commentary = true
			},
		},
		{
			name:   "unknown role",
			want:   true,
			mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Role = "" },
		},
		{name: "commentary", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Commentary = true }},
		{name: "alternate", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Role = api.AudioRoleAlternateMix }},
		{name: "embedded", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.EmbeddedCompatibility = true }},
		{name: "embedded title", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Title = "Embedded Core" }},
		{name: "other resource", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.ResourceID = "other" }},
		{name: "same physical track", mutate: func(c *api.MediaTrackFacts, m *[]api.MediaTrackFacts) { c.ID = (*m)[0].ID }},
		{name: "unknown language", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) { c.Languages = []string{"und"} }},
		{name: "partial language set", mutate: func(c *api.MediaTrackFacts, _ *[]api.MediaTrackFacts) {
			c.Languages = []string{"English", "Unknown language"}
		}},
		{name: "overlapping language sets", mutate: func(_ *api.MediaTrackFacts, m *[]api.MediaTrackFacts) {
			(*m)[0].Languages = []string{"English", "German"}
		}},
		{name: "commentary mix", mutate: func(_ *api.MediaTrackFacts, m *[]api.MediaTrackFacts) { (*m)[0].Role = api.AudioRoleCommentary }},
		{name: "unidentified mix", mutate: func(_ *api.MediaTrackFacts, m *[]api.MediaTrackFacts) { (*m)[0].ID = "" }},
		{name: "multiple mixes", mutate: func(_ *api.MediaTrackFacts, m *[]api.MediaTrackFacts) {
			second := (*m)[0]
			second.ID = "other"
			*m = append(*m, second)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			companion := api.MediaTrackFacts{
				ID:         "dd",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Codec:      "DD",
				ResourceID: "media",
				Languages:  []string{"English"},
			}
			mixes := []api.MediaTrackFacts{{
				ID:         "truehd",
				Kind:       api.MediaTrackAudio,
				Role:       api.AudioRoleProgramme,
				Codec:      "TrueHD",
				ResourceID: "media",
				Languages:  []string{"English"},
			}}
			if test.mutate != nil {
				test.mutate(&companion, &mixes)
			}
			if got := AutomaticCompatibilityMix(companion, mixes); (got != "") != test.want {
				t.Fatalf("association=%q want inferred=%t", got, test.want)
			}
		})
	}
}

func TestCompatibilityCandidatesRetainUncertainCompetingMix(t *testing.T) {
	companion := api.MediaTrackFacts{
		ID:        "dd",
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleProgramme,
		Codec:     "DD",
		Languages: []string{"English"},
	}
	facts := api.LanguageFacts{Tracks: []api.MediaTrackFacts{
		{
			ID:        "first",
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Codec:     "TrueHD",
			Languages: []string{"English"},
		},
		{
			ID:    "second",
			Kind:  api.MediaTrackAudio,
			Role:  api.AudioRoleProgramme,
			Codec: "TrueHD",
		},
		companion,
	}}
	if mixes := CompatibilityAudioMixes(facts, companion, true); len(mixes) != 2 || AutomaticCompatibilityMix(companion, mixes) != "" {
		t.Fatalf("unknown language created false unique match: %+v", mixes)
	}
	for _, codec := range []string{"DD", "AC-3", "DD+", "DDP", "E-AC3", "E-AC-3"} {
		companion.Codec = codec
		if !StandaloneDolbyAudio(companion) {
			t.Fatalf("alias %q not discovered", codec)
		}
	}
	companion.Codec = "AAC"
	if StandaloneDolbyAudio(companion) {
		t.Fatal("ordinary non-Dolby stream was reclassified")
	}
	before := slices.Clone(facts.Tracks)
	_ = AutomaticDolbyCompatibilityMix(facts, companion)
	if !slices.EqualFunc(before, facts.Tracks, func(a, b api.MediaTrackFacts) bool { return a.ID == b.ID && a.Role == b.Role }) {
		t.Fatal("canonical tracks changed")
	}
}
