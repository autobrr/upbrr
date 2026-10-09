// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package yus

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestYUSNamingEvidenceRequiresKnownLanguageCount(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*api.MediaFacts)
	}{
		{"missing language", func(f *api.MediaFacts) { f.Tracks[1].Languages = nil }},
		{"unknown language", func(f *api.MediaFacts) { f.Tracks[1].Languages = []string{"unknown-label"} }},
		{"missing role", func(f *api.MediaFacts) { f.Tracks[1].Role = "" }},
		{"contradictory correction", func(f *api.MediaFacts) {
			f.AudioLanguages = []string{"German"}
			f.AudioLanguagesProvenance = api.FactProvenanceManual
		}},
		{"explicit clear", func(f *api.MediaFacts) { f.AudioLanguagesProvenance = api.FactProvenanceManualEmpty }},
		{"incomplete single language", func(f *api.MediaFacts) {
			f.Tracks = f.Tracks[:1]
			f.TrackCoverageComplete = false
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta, media := yusNamingEvidenceSubject(t, "English", "French")
			test.edit(&media)
			meta.LanguageFacts = mediafacts.ResolveLanguages(media)
			// Raw aggregate/provider values cannot repair finalized language facts.
			meta.AudioLanguages = []string{"English", "French"}
			meta.ProviderMetadata.TMDB.OriginalLanguage = "English"
			failures := yusNamingEvidenceFailures(t, meta)
			requireYUSNamingEvidenceFailure(t, failures)
			if got := yusName(t, meta, nil); strings.Contains(got, "Multi-Audio") {
				t.Fatalf("unresolved facts guessed a marker: %q", got)
			}
		})
	}
	meta, _ := yusNamingEvidenceSubject(t, "English", "French")
	meta.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusUnavailable
	requireYUSNamingEvidenceFailure(t, yusNamingEvidenceFailures(t, meta))
}

func TestYUSNamingEvidenceNeedsNoOriginalLanguageOrWhitelist(t *testing.T) {
	t.Parallel()
	for _, languages := range [][]string{{"English"}, {"German"}, {"German", "French"}, {"Japanese", "English", "German"}} {
		meta, _ := yusNamingEvidenceSubject(t, languages...)
		if meta.LanguageFacts.OriginalLanguagesKnown || len(meta.LanguageFacts.OriginalLanguages) != 0 {
			t.Fatal("fixture must not supply original-language metadata")
		}
		if failures := yusNamingEvidenceFailures(t, meta); len(failures) != 0 {
			t.Fatalf("known languages %v acquired an eligibility restriction: %+v", languages, failures)
		}
		if got := yusName(t, meta, nil); strings.Contains(got, "Multi-Audio") != (len(languages) >= 2) {
			t.Fatalf("known language count selected incorrect marker: %q", got)
		}
	}
	meta, media := yusNamingEvidenceSubject(t, "German", "French", "English")
	media.Tracks[2].Role = ""
	meta.LanguageFacts = mediafacts.ResolveLanguages(media)
	if meta.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial {
		t.Fatal("fixture must preserve the unresolved extra track")
	}
	if failures := yusNamingEvidenceFailures(t, meta); len(failures) != 0 {
		t.Fatalf("two established languages already prove Multi-Audio: %+v", failures)
	}
	if got := yusName(t, meta, nil); !strings.Contains(got, "Multi-Audio") {
		t.Fatalf("known positive evidence lost its marker: %q", got)
	}
	meta, media = yusNamingEvidenceSubject(t, "English", "French")
	media.Tracks[1].Role = api.AudioRoleCommentary
	meta.LanguageFacts = mediafacts.ResolveLanguages(media)
	if failures := yusNamingEvidenceFailures(t, meta); len(failures) != 0 {
		t.Fatalf("known commentary language made programme count unresolved: %+v", failures)
	}
	if got := yusName(t, meta, nil); strings.Contains(got, "Multi-Audio") {
		t.Fatalf("commentary created a programme marker: %q", got)
	}
	meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{AudioAbsent: true, TrackCoverageComplete: true})
	if failures := yusNamingEvidenceFailures(t, meta); len(failures) != 0 {
		t.Fatalf("verified absent audio required language evidence: %+v", failures)
	}
}

func TestYUSNamingEvidencePreservesDiscAndDebugBoundaries(t *testing.T) {
	t.Parallel()
	meta, media := yusNamingEvidenceSubject(t, "English", "French")
	media.Tracks[1].Languages = nil
	meta.LanguageFacts = mediafacts.ResolveLanguages(media)
	for _, disc := range []string{"", "BDMV", "DVD"} {
		meta.Type, meta.DiscType = "DISC", disc
		if failures := yusNamingEvidenceFailures(t, meta); len(failures) != 0 {
			t.Fatalf("full disc %q acquired language review: %+v", disc, failures)
		}
		meta.Type = "REMUX"
		failures := yusNamingEvidenceFailures(t, meta)
		requireYUSNamingEvidenceFailure(t, failures)
		for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
			blocking, err := trackers.FirstBlockingRuleFailure("YUS", failures, mode, nil)
			if err != nil || (blocking != nil) != (mode == api.WorkflowExecutionModeNormal) {
				t.Fatalf("mode=%s blocking=%+v err=%v", mode, blocking, err)
			}
		}
	}
}

func TestYUSNamingEvidencePreservesPresentationAndManualNames(t *testing.T) {
	t.Parallel()
	meta, media := yusNamingEvidenceSubject(t, "English", "French")
	media.Tracks[1].Role = ""
	meta.LanguageFacts = mediafacts.ResolveLanguages(media)
	forced := true
	meta.ReleaseNameOverrides = api.ReleaseNameOverrides{
		NoDual:    &forced,
		NoDub:     &forced,
		DualAudio: &forced,
	}
	requested := "Manual Whole Name-GRP"
	if got := yusName(t, meta, &requested); got != requested {
		t.Fatalf("manual naming was rewritten: %q", got)
	}
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
			Tracker:             "YUS",
			Meta:                meta,
			RequestedUploadName: &requested,
			ExecutionMode:       mode,
			Logger:              api.NopLogger{},
		}, "input", "catalog", "config")
		if failure != nil || projection.UploadReleaseName != requested || projection.UploadReady != (mode == api.WorkflowExecutionModeDebug) {
			t.Fatalf("mode=%s: projection=%+v failure=%v", mode, projection, failure)
		}
		if !slices.ContainsFunc(projection.PolicyDecisions, func(item api.TrackerPolicyDecision) bool {
			return item.Code == "language_naming_evidence" && item.Blocking == (mode == api.WorkflowExecutionModeNormal) &&
				(mode != api.WorkflowExecutionModeDebug || item.Decision == "bypassed")
		}) {
			t.Fatalf("missing visible naming assessment: %+v", projection.PolicyDecisions)
		}
	}
}

func yusNamingEvidenceSubject(t *testing.T, languages ...string) (api.UploadSubject, api.MediaFacts) {
	t.Helper()
	meta := yusSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Source:      "WEB",
		Resolution:  "1080p",
		VideoEncode: "H.265",
		Title:       "Example",
		Year:        2026,
		Audio:       "AAC 2.0",
		Tag:         "-GRP",
	})
	meta.Identity.TMDBID = 1234567
	meta.Identity.IMDBID = 1234567
	meta.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusNotApplicable
	meta.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: meta.SourcePath,
		Generation: meta.Identity.Generation,
		TMDB: &api.TMDBMetadata{
			TMDBID:   1234567,
			Category: string(meta.Identity.Category),
			Title:    "Example",
			Year:     2026,
		},
		IMDB: &api.IMDBMetadata{
			IMDBID: 1234567,
			Title:  "Example",
		},
	}
	media := api.MediaFacts{TrackCoverageComplete: true}
	for index, language := range languages {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:           "audio-" + strconv.Itoa(index),
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Languages:    []string{language},
			Default:      index == 0,
			DefaultKnown: true,
			Codec:        "AAC",
			AudioLabel:   "AAC 2.0",
		})
	}
	meta.LanguageFacts = mediafacts.ResolveLanguages(media)
	return meta, media
}

func yusNamingEvidenceFailures(t *testing.T, meta api.UploadSubject) []api.RuleFailure {
	t.Helper()
	failures, err := unit3d.NewWithProfile(Profile()).ValidationPolicy().Check(t.Context(), api.NewTrackerValidationSubject(meta, "YUS"), api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	return failures
}

func requireYUSNamingEvidenceFailure(t *testing.T, failures []api.RuleFailure) {
	t.Helper()
	if !slices.ContainsFunc(failures, func(failure api.RuleFailure) bool {
		return failure.Rule == "language_naming_evidence" && failure.Disposition == api.RuleDispositionStrict && failure.DebugBypass &&
			failure.EvidenceStatus == api.MetadataEvidenceStatusPartial && strings.Contains(failure.Reason, "Unresolved") && strings.Contains(failure.Reason, "YUS")
	}) {
		t.Fatalf("missing visible unresolved YUS naming evidence: %+v", failures)
	}
}

func TestYUSNamingEvidenceRetainsConstructibility(t *testing.T) {
	t.Parallel()
	definition := unit3d.NewWithProfile(Profile())
	if got := definition.ValidationPolicy().ID; got != "unit3d-yus-constructibility-v1+unit3d-yus-policy-v1" {
		t.Fatalf("validation policy = %q", got)
	}
	meta, _ := yusNamingEvidenceSubject(t, "German")
	meta.Identity.Category = api.CanonicalCategoryUnknown
	blocking, err := trackers.FirstBlockingRuleFailure("YUS", yusNamingEvidenceFailures(t, meta), api.WorkflowExecutionModeDebug, nil)
	if err != nil || blocking == nil || blocking.Rule != "unsupported_category" {
		t.Fatalf("language debug bypass masked mandatory constructibility: blocking=%+v err=%v", blocking, err)
	}
}
