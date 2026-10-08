// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDPManualLanguageMarkerQuestionnaire(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		originals []string
		programme []string
	}{
		{"Japanese original German dub", []string{"Japanese"}, []string{"German"}},
		{"English original German dub", []string{"English"}, []string{"German"}},
		{"English original Swedish dub", []string{"English"}, []string{"Swedish"}},
		{"Swedish original Finnish dub", []string{"Swedish"}, []string{"Finnish"}},
		{"Japanese original two non-Nordic dubs", []string{"Japanese"}, []string{"German", "French"}},
		{"English original two non-Nordic dubs", []string{"English"}, []string{"German", "French"}},
		{"English original Nordic and German dubs", []string{"English"}, []string{"Swedish", "German"}},
		{"two retained originals", []string{"German", "French"}, []string{"German", "French"}},
		{"retained English and French originals", []string{"English", "French"}, []string{"English", "French"}},
		{"English and French originals with French and German", []string{"English", "French"}, []string{"French", "German"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := dpManualMarkerSubject(t, test.originals, test.programme...)
			field := dpManualMarkerField(t, meta)
			wantKey := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "DP"), "manual_language_marker")
			if field.Key != wantKey || field.Kind != "select" || !field.Required || field.Value != "" {
				t.Fatalf("manual language-marker field must be bound, required and initially unanswered: %+v", field)
			}
			wantOptions := []string{"(omit)", "Dubbed", "Dual-Audio", "MULTi"}
			for _, language := range test.programme {
				wantOptions = append(wantOptions, language+" Dubbed", language+" MULTi")
			}
			slices.Sort(wantOptions)
			options := slices.Clone(field.Options)
			slices.Sort(options)
			if !slices.Equal(options, wantOptions) {
				t.Fatalf("manual-marker options = %v, want bounded choices %v", options, wantOptions)
			}
			if !strings.Contains(field.Help, "(omit)") {
				t.Fatalf("explicit omission is not explained: %+v", field)
			}
			requireDPManualMarkerBlocked(t, meta)
			if got := dpReviewedName(t, meta, nil); got != "Example 2026 1080p WEB-DL AAC 2.0 H.265-GRP" {
				t.Fatalf("unanswered composition guessed a marker or used stale aggregate audio: %q", got)
			}
			for _, answer := range []string{"  " + test.programme[0] + " MULTi  ", "(omit)"} {
				dpSetManualMarkerAnswer(&meta, answer)
				if failures := dpValidationFailures(t, meta); len(failures) != 0 {
					t.Fatalf("explicit choice %q stayed unresolved: %+v", answer, failures)
				}
				marker := strings.TrimSpace(answer)
				if marker == "(omit)" {
					marker = ""
				} else {
					marker += " "
				}
				if got, want := dpReviewedName(t, meta, nil), "Example 2026 1080p WEB-DL "+marker+"AAC 2.0 H.265-GRP"; got != want {
					t.Fatalf("choice %q: got %q, want %q", answer, got, want)
				}
			}
		})
	}
}

func TestDPManualLanguageMarkerPreservesAutomaticRows(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		original  string
		programme []string
	}{
		{"Japanese", []string{"Japanese"}},
		{"Japanese", []string{"English"}},
		{"Japanese", []string{"Swedish"}},
		{"Japanese", []string{"Japanese", "English"}},
		{"Japanese", []string{"Japanese", "German"}},
		{"English", []string{"English", "German"}},
		{"Japanese", []string{"Swedish", "German"}},
		{"Japanese", []string{"English", "German"}},
		{"Japanese", []string{"Swedish", "Finnish"}},
		{"Japanese", []string{"Japanese", "German", "French"}},
	} {
		meta := dpManualMarkerSubject(t, []string{test.original}, test.programme...)
		if schema := unit3d.NewWithProfile(Profile()).ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta}); schema != nil {
			t.Fatalf("known original=%s programme=%v acquired a manual choice: %+v", test.original, test.programme, schema)
		}
		before := dpReviewedName(t, meta, nil)
		dpSetManualMarkerAnswer(&meta, "Dubbed")
		if got := dpReviewedName(t, meta, nil); got != before {
			t.Fatalf("manual fallback changed an automatic row from %q to %q", before, got)
		}
	}
	for _, test := range []struct {
		originals []string
		programme []string
		marker    string
	}{
		{[]string{"Japanese", "French"}, []string{"Japanese", "English"}, "Dual-Audio"},
		{[]string{"English", "French"}, []string{"English", "German", "Japanese"}, "MULTi"},
	} {
		meta := dpManualMarkerSubject(t, test.originals, test.programme...)
		if schema := unit3d.NewWithProfile(Profile()).ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta}); schema != nil {
			t.Fatalf("established multi-original precedence acquired manual choice: %+v", schema)
		}
		if got := dpReviewedName(t, meta, nil); !strings.Contains(got, test.marker+" AAC 2.0") {
			t.Fatalf("established multi-original marker %q changed: %q", test.marker, got)
		}
	}
}

func TestDPManualLanguageMarkerRejectsInvalidAnswers(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{
		"", " \t ", "German\nMULTi", "German\rMULTi", "German\tMULTi", "German\x00MULTi", "German\x7fMULTi", "German\u0085MULTi",
		"German MULTi\n", "German MULTi\r", "Example Title", "AAC 2.0", "German", "German MULTI", "Japanese MULTi", "Italian MULTi",
		"German MULTi AAC 2.0", "German MULTi Example Title", "(OMIT)",
	} {
		t.Run(answer, func(t *testing.T) {
			meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
			dpSetManualMarkerAnswer(&meta, answer)
			if field := dpManualMarkerField(t, meta); field.Value != "" {
				t.Fatalf("invalid answer became the displayed choice: %+v", field)
			}
			requireDPManualMarkerBlocked(t, meta)
		})
	}
	meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
	key := dpManualMarkerField(t, meta).Key
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"OTHER": {key: "German MULTi"}, "DP": {"manual_language_marker": "German MULTi"}}
	requireDPManualMarkerBlocked(t, meta)
}

func TestDPManualLanguageMarkerRendersBoundedChoices(t *testing.T) {
	t.Parallel()
	for _, marker := range []string{"Dubbed", "Dual-Audio", "MULTi", "German Dubbed"} {
		meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
		dpSetManualMarkerAnswer(&meta, marker)
		if failures := dpValidationFailures(t, meta); len(failures) != 0 {
			t.Fatalf("explicit %q choice rejected: %+v", marker, failures)
		}
		if got, want := dpReviewedName(t, meta, nil), "Example 2026 1080p WEB-DL "+marker+" AAC 2.0 H.265-GRP"; got != want {
			t.Fatalf("choice %q: got %q, want %q", marker, got, want)
		}
	}
}

func TestDPManualLanguageMarkerInvalidatesChangedEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*api.UploadSubject)
	}{
		{"original language", func(meta *api.UploadSubject) { meta.LanguageFacts.OriginalLanguages = []string{"English"} }},
		{"programme language", func(meta *api.UploadSubject) {
			meta.LanguageFacts.ProgrammeLanguages = []string{"German", "Italian"}
			meta.LanguageFacts.Tracks[1].Languages = []string{"Italian"}
		}},
		{"generation", func(meta *api.UploadSubject) { meta.Identity.Generation++ }},
		{"source path", func(meta *api.UploadSubject) { meta.SourcePath = "changed-source" }},
		{"track identity", func(meta *api.UploadSubject) { meta.LanguageFacts.Tracks[1].ID = "replacement-track" }},
		{"primary track", func(meta *api.UploadSubject) {
			meta.LanguageFacts.PrimaryAudioTrackID = meta.LanguageFacts.Tracks[1].ID
		}},
		{"default track", func(meta *api.UploadSubject) {
			meta.LanguageFacts.Tracks[0].Default = false
			meta.LanguageFacts.Tracks[1].Default = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
			dpSetManualMarkerAnswer(&meta, "German MULTi")
			old := dpManualMarkerField(t, meta)
			if old.Value != "German MULTi" {
				t.Fatalf("current answer was not retained: %+v", old)
			}
			test.edit(&meta)
			current := dpManualMarkerField(t, meta)
			if current.Key == old.Key || current.Value != "" {
				t.Fatalf("changed evidence reused answer: old=%+v current=%+v", old, current)
			}
			requireDPManualMarkerBlocked(t, meta)
		})
	}
}

func TestDPManualLanguageMarkerUsesChoiceIndependentOfPrimaryAndDefault(t *testing.T) {
	t.Parallel()
	for primary := range 2 {
		for defaultTrack := range 2 {
			meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
			meta.LanguageFacts.PrimaryAudioTrackID = meta.LanguageFacts.Tracks[primary].ID
			meta.LanguageFacts.Tracks[0].Default = defaultTrack == 0
			meta.LanguageFacts.Tracks[1].Default = defaultTrack == 1
			slices.Reverse(meta.LanguageFacts.ProgrammeLanguages)
			dpSetManualMarkerAnswer(&meta, "German MULTi")
			want := "Example 2026 1080p WEB-DL German MULTi " + meta.LanguageFacts.Tracks[defaultTrack].AudioLabel + " H.265-GRP"
			if got := dpReviewedName(t, meta, nil); got != want {
				t.Fatalf("primary=%d default=%d: got %q, want %q", primary, defaultTrack, got, want)
			}
		}
	}
}

func TestDPManualLanguageMarkerSeparatesManualNamesFromChoice(t *testing.T) {
	t.Parallel()
	meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
	markDPComponent(t, meta.GeneratedName, api.NameRoleDualAudio, "German MULTi")
	meta.ReleaseName = meta.GeneratedName.Render().Name
	requireDPManualMarkerBlocked(t, meta)
	dpSetManualMarkerAnswer(&meta, "German MULTi")
	if got := dpReviewedName(t, meta, nil); !strings.Contains(got, "German MULTi AAC 2.0") {
		t.Fatalf("consistent manual marker changed: %q", got)
	}
	for _, choice := range []string{"French Dubbed", "(omit)"} {
		dpSetManualMarkerAnswer(&meta, choice)
		_, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "DP", Meta: meta}, namePolicy())
		if _, ok := errors.AsType[*trackers.NameRuleError](failure); !ok {
			t.Fatalf("conflicting manual marker and explicit %q choice did not report a name conflict: %v", choice, failure)
		}
	}
	meta = dpManualMarkerSubject(t, []string{"English"}, "Swedish", "German")
	requested := "Manual Whole Name-GRP"
	registry := dpManualMarkerRegistry(t)
	input := trackers.PreparationInput{
		Tracker:             "DP",
		Meta:                meta,
		RequestedUploadName: &requested,
		Logger:              api.NopLogger{},
	}
	projection, failure := registry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
	if failure != nil || projection.DupeReady || projection.UploadReady || len(projection.Questionnaire) != 1 {
		t.Fatalf("opaque override bypassed separate choice: projection=%+v failure=%v", projection, failure)
	}
	dpSetManualMarkerAnswer(&input.Meta, "German MULTi")
	projection, failure = registry.ProjectRelease(t.Context(), input, "answered", "catalog", "config")
	if failure != nil || !projection.DupeReady || !projection.UploadReady || projection.UploadReleaseName != requested {
		t.Fatalf("opaque name was changed or blocked after choice: projection=%+v failure=%v", projection, failure)
	}
}

func TestDPManualLanguageMarkerRegistryProjection(t *testing.T) {
	t.Parallel()
	registry := dpManualMarkerRegistry(t)
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		input := trackers.PreparationInput{
			Tracker:       "DP",
			Meta:          dpManualMarkerSubject(t, []string{"English"}, "Swedish", "German"),
			ExecutionMode: mode,
			Logger:        api.NopLogger{},
		}
		projection, failure := registry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
		if failure != nil || projection.DupeReady || projection.UploadReady || len(projection.Questionnaire) != 1 {
			t.Fatalf("mode=%s: missing choice not exposed before duplicates: projection=%+v failure=%v", mode, projection, failure)
		}
		field := projection.Questionnaire[0]
		if !field.Required || field.Kind != "select" || field.Value != "" {
			t.Fatalf("unsafe projected manual-marker field: %+v", field)
		}
		if strings.Contains(projection.UploadReleaseName, "Dual-Audio") || strings.Contains(projection.UploadReleaseName, "MULTi") {
			t.Fatalf("unanswered projection guessed from aggregate audio: %q", projection.UploadReleaseName)
		}
		dpSetManualMarkerAnswer(&input.Meta, "German MULTi")
		answered, failure := registry.ProjectRelease(t.Context(), input, "answered", "catalog", "config")
		if failure != nil || !answered.DupeReady || !answered.UploadReady || len(answered.Questionnaire) != 1 || answered.Questionnaire[0].Value != "German MULTi" {
			t.Fatalf("mode=%s: reviewed marker did not unlock projection: projection=%+v failure=%v", mode, answered, failure)
		}
		if answered.NamingFingerprint == projection.NamingFingerprint || !strings.Contains(answered.UploadReleaseName, "German MULTi AAC 2.0") {
			t.Fatalf("choice did not become versioned name authority: %+v", answered)
		}
	}
}

func TestDPManualLanguageMarkerDiscAndStrictEligibilityBoundaries(t *testing.T) {
	t.Parallel()
	for _, disc := range []string{"", "BDMV", "DVD", "HDDVD"} {
		meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
		meta.Type, meta.DiscType = "DISC", disc
		if schema := unit3d.NewWithProfile(Profile()).ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta}); schema != nil {
			t.Fatalf("full disc %q acquired manual language choice: %+v", disc, schema)
		}
		if failures := dpValidationFailures(t, meta); len(failures) != 0 {
			t.Fatalf("full disc %q acquired issue-specific validation: %+v", disc, failures)
		}
		meta.Type = "REMUX"
		dpManualMarkerField(t, meta)
		requireDPManualMarkerBlocked(t, meta)
	}
	meta := dpManualMarkerSubject(t, []string{"Japanese"}, "Swedish", "German", "French")
	dpSetManualMarkerAnswer(&meta, "German MULTi")
	requireDPLanguageFailure(t, dpValidationFailures(t, meta), "language_multilingual_original", trackers.LanguageProhibited)
	meta = dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
	for _, edit := range []func(*api.LanguageFacts){
		func(facts *api.LanguageFacts) { facts.OriginalLanguagesKnown = false },
		func(facts *api.LanguageFacts) { facts.OriginalLanguages = nil },
		func(facts *api.LanguageFacts) { facts.ProgrammeStatus = api.MetadataEvidenceStatusPartial },
		func(facts *api.LanguageFacts) { facts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory },
		func(facts *api.LanguageFacts) { facts.ProgrammeLanguages = nil },
	} {
		changed := meta
		edit(&changed.LanguageFacts)
		dpSetManualMarkerAnswer(&changed, "German MULTi")
		if schema := unit3d.NewWithProfile(Profile()).ProjectionQuestionnaire(trackers.PreparationInput{Meta: changed}); schema != nil {
			t.Fatalf("incomplete facts were offered manual naming as a cure: %+v", schema)
		}
		if failures := dpValidationFailures(t, changed); !slices.ContainsFunc(failures, func(failure api.RuleFailure) bool {
			return strings.HasPrefix(failure.Reason, string(trackers.LanguageUnresolved)) && failure.Disposition == api.RuleDispositionStrict
		}) {
			t.Fatalf("manual marker cured incomplete facts: %+v", failures)
		}
	}
}

func TestDPManualLanguageMarkerLatePreparation(t *testing.T) {
	t.Parallel()
	definition := unit3d.NewWithProfile(Profile())
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		meta := dpManualMarkerSubject(t, []string{"English"}, "Swedish", "German")
		dir := t.TempDir()
		meta.MediaInfoTextPath, meta.TorrentPath = filepath.Join(dir, "MediaInfo.txt"), filepath.Join(dir, "example.torrent")
		for _, path := range []string{meta.MediaInfoTextPath, meta.TorrentPath} {
			if err := os.WriteFile(path, []byte("Synthetic prepared content"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		requested := "Manual Whole Name-GRP"
		input := trackers.PreparationInput{
			Intent:              trackers.PreparationIntentDryRun,
			ExecutionMode:       mode,
			Tracker:             "DP",
			Meta:                meta,
			RequestedUploadName: &requested,
			TrackerConfig:       config.TrackerConfig{APIKey: "synthetic-key"},
			Logger:              api.NopLogger{},
			Assets:              &trackers.DescriptionAssets{Description: "Synthetic description", Final: true},
		}
		if _, failure := definition.Prepare(t.Context(), input); failure == nil {
			t.Fatalf("mode=%s: full manual name bypassed late manual-marker choice", mode)
		}
		dpSetManualMarkerAnswer(&input.Meta, "(omit)")
		plan, failure := definition.Prepare(t.Context(), input)
		if failure != nil {
			t.Fatalf("mode=%s: explicit omission blocked late preparation: %v", mode, failure)
		}
		if _, err := plan.Submit(t.Context()); !errors.Is(err, trackers.ErrPlanNotSubmittable) {
			t.Fatalf("dry-run plan allowed submission: %v", err)
		}
		if err := plan.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func dpManualMarkerSubject(t *testing.T, originals []string, languages ...string) api.UploadSubject {
	t.Helper()
	meta := dpEligibilitySubject(languages...)
	meta.SourcePath = "synthetic-source"
	meta.Identity.Generation = 1
	meta.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: meta.SourcePath,
		Generation: meta.Identity.Generation,
		TMDB:       &api.TMDBMetadata{TMDBID: meta.Identity.TMDBID},
	}
	meta.LanguageFacts.OriginalLanguages = slices.Clone(originals)
	for index := range meta.LanguageFacts.Tracks {
		meta.LanguageFacts.Tracks[index].Codec = "DD+"
		meta.LanguageFacts.Tracks[index].AudioLabel = "DD+ 5.1"
	}
	meta.LanguageFacts.Tracks[0].Codec = "AAC"
	meta.LanguageFacts.Tracks[0].AudioLabel = "AAC 2.0"
	meta.AudioLanguages = []string{"Japanese", "English", "German"}
	generated := dpGeneratedSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Example",
		Year:        2026,
		Resolution:  "1080p",
		Source:      "WEB",
		Audio:       "Dual-Audio AAC 2.0",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	})
	meta.GeneratedName, meta.ReleaseName, meta.ReleaseNameNoTag = generated.GeneratedName, generated.ReleaseName, generated.ReleaseNameNoTag
	return meta
}

func dpManualMarkerField(t *testing.T, meta api.UploadSubject) api.TrackerQuestionnaireField {
	t.Helper()
	schema := unit3d.NewWithProfile(Profile()).ProjectionQuestionnaire(trackers.PreparationInput{Tracker: "DP", Meta: meta})
	if schema == nil || schema.Tracker != "DP" || len(schema.Fields) != 1 {
		t.Fatalf("uncovered complete facts lack a manual language-marker question: %+v", schema)
	}
	return schema.Fields[0]
}

func dpSetManualMarkerAnswer(meta *api.UploadSubject, answer string) {
	key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(*meta, "DP"), "manual_language_marker")
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"DP": {key: answer}}
}

func requireDPManualMarkerBlocked(t *testing.T, meta api.UploadSubject) {
	t.Helper()
	failures := dpValidationFailures(t, meta)
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		blocking, err := trackers.FirstBlockingRuleFailure("DP", failures, mode, nil)
		if err != nil || blocking == nil || blocking.Rule != "dp_language_marker" || blocking.DebugBypass || blocking.Disposition != api.RuleDispositionStrict {
			t.Fatalf("mode=%s: missing marker was not a non-bypassable constructibility failure: blocking=%+v error=%v", mode, blocking, err)
		}
	}
}

func dpManualMarkerRegistry(t *testing.T) *trackers.Registry {
	t.Helper()
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestDPManualLanguageMarkerRequiresPresentInsertionAnchor(t *testing.T) {
	t.Parallel()
	for _, omitGroup := range []bool{false, true} {
		meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
		for index := range meta.GeneratedName.Components {
			component := &meta.GeneratedName.Components[index]
			if component.Role == api.NameRoleAudio || omitGroup && component.Role == api.NameRoleGroup {
				component.Present = false
				component.Manual = true
			}
		}
		meta.ReleaseName = meta.GeneratedName.Render().Name
		dpSetManualMarkerAnswer(&meta, "German MULTi")
		prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "DP", Meta: meta}, namePolicy())
		if omitGroup {
			if nameFailure, ok := errors.AsType[*trackers.NameRuleError](failure); !ok || nameFailure.Rule != "audio_language_anchor" {
				t.Fatalf("absent naming anchors silently dropped the selected marker: %v", failure)
			}
			continue
		}
		if failure != nil {
			t.Fatal(failure)
		}
		name, err := prepared.ReviewedUploadName()
		if err != nil || !strings.Contains(name, "German MULTi") {
			t.Fatalf("manual audio omission dropped the selected marker: name=%q err=%v", name, err)
		}
	}
}

func TestDPManualLanguageMarkerPreservesExistingChoiceWithoutAnchors(t *testing.T) {
	t.Parallel()
	meta := dpManualMarkerSubject(t, []string{"Japanese"}, "German", "French")
	for index := range meta.GeneratedName.Components {
		component := &meta.GeneratedName.Components[index]
		if component.Role == api.NameRoleAudio || component.Role == api.NameRoleGroup {
			component.Present = false
			component.Manual = true
		}
	}
	markDPComponent(t, meta.GeneratedName, api.NameRoleDualAudio, "German MULTi")
	meta.ReleaseName = meta.GeneratedName.Render().Name
	dpSetManualMarkerAnswer(&meta, "German MULTi")
	if got := dpReviewedName(t, meta, nil); !strings.Contains(got, "German MULTi") {
		t.Fatalf("existing manual choice was lost without insertion anchors: %q", got)
	}
}
