// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDPOriginalAudioEligibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		languages []string
		want      bool
	}{
		{name: "one dub", languages: []string{"English"}},
		{name: "two dubs", languages: []string{"Danish", "Swedish"}},
		{name: "duplicate languages", languages: []string{"Danish", "Swedish", "Danish"}},
		{name: "three with original", languages: []string{"Japanese", "Danish", "Swedish"}},
		{
			name:      "three without original",
			languages: []string{"English", "Danish", "Swedish"},
			want:      true,
		},
		{
			name:      "four without original",
			languages: []string{"English", "Danish", "Swedish", "French"},
			want:      true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := dpEligibilitySubject(test.languages...)
			failures := dpValidationFailures(t, meta)
			if test.want {
				requireDPLanguageFailure(t, failures, "language_multilingual_original", trackers.LanguageProhibited)
			} else if len(failures) != 0 {
				t.Fatalf("allowed language composition: %+v", failures)
			}
		})
	}
}

func TestDPOriginalAudioCountsProgrammeLanguagesOnly(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		languages []string
		want      bool
	}{
		{name: "third language is commentary", languages: []string{"Danish", "Swedish", "English"}},
		{
			name:      "original only in commentary",
			languages: []string{"Danish", "Swedish", "English", "Japanese"},
			want:      true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := dpEligibilitySubject(test.languages...)
			media := api.MediaFacts{
				OriginalLanguage:      "Japanese",
				Tracks:                meta.LanguageFacts.Tracks,
				TrackCoverageComplete: true,
				PrimaryAudioTrackID:   meta.LanguageFacts.PrimaryAudioTrackID,
			}
			media.Tracks[len(media.Tracks)-1].Role = api.AudioRoleCommentary
			meta.LanguageFacts = mediafacts.ResolveLanguages(media)
			failures := dpValidationFailures(t, meta)
			if test.want {
				requireDPLanguageFailure(t, failures, "language_multilingual_original", trackers.LanguageProhibited)
			} else if len(failures) != 0 {
				t.Fatalf("commentary counted as programme audio: %+v", failures)
			}
		})
	}
}

func TestDPOriginalAudioRequiresCompleteFinalFacts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*api.UploadSubject)
	}{
		{name: "partial programme", edit: func(meta *api.UploadSubject) { meta.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusPartial }},
		{name: "contradictory programme", edit: func(meta *api.UploadSubject) {
			meta.LanguageFacts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
		}},
		{name: "unknown programme language", edit: func(meta *api.UploadSubject) {
			meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage: "Japanese",
				Tracks: []api.MediaTrackFacts{{
					Kind:      api.MediaTrackAudio,
					Role:      api.AudioRoleProgramme,
					Languages: []string{"und"},
				}},
				TrackCoverageComplete: true,
			})
		}},
		{name: "missing original", edit: func(meta *api.UploadSubject) { meta.LanguageFacts.OriginalLanguagesKnown = false }},
		{name: "cleared original", edit: func(meta *api.UploadSubject) {
			meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{Tracks: meta.LanguageFacts.Tracks, TrackCoverageComplete: true})
		}},
		{name: "cleared programme languages", edit: func(meta *api.UploadSubject) {
			meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:         "Japanese",
				Tracks:                   meta.LanguageFacts.Tracks,
				TrackCoverageComplete:    true,
				AudioLanguagesProvenance: api.FactProvenanceManualEmpty,
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := dpEligibilitySubject("Danish", "Swedish", "English")
			// Stale presentation and provider values cannot refill cleared evidence.
			meta.AudioLanguages = []string{"Japanese", "Danish", "Swedish"}
			meta.ProviderMetadata.TMDB = &api.TMDBMetadata{OriginalLanguage: "Danish"}
			test.edit(&meta)
			requireDPLanguageFailure(t, dpValidationFailures(t, meta), "language_multilingual_evidence", trackers.LanguageUnresolved)
		})
	}
	for _, meta := range []api.UploadSubject{
		dpEligibilitySubject("Danish", "Swedish"),
		{
			Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
			Type:          "WEBDL",
			Release:       api.ReleaseInfo{Resolution: "1080p"},
			LanguageFacts: api.LanguageFacts{AudioAbsent: true, ProgrammeStatus: api.MetadataEvidenceStatusComplete},
		},
	} {
		meta.LanguageFacts.OriginalLanguagesKnown = false
		failures := dpValidationFailures(t, meta)
		if meta.LanguageFacts.AudioAbsent {
			if len(failures) != 0 {
				t.Fatalf("audio-absent release required original language: %+v", failures)
			}
		} else {
			requireDPLanguageFailure(t, failures, "language_multilingual_evidence", trackers.LanguageUnresolved)
		}
	}
}

func TestDPOriginalAudioUsesCorrectedFactsOverRawMetadata(t *testing.T) {
	t.Parallel()
	meta := dpEligibilitySubject("Danish", "Swedish", "English")
	meta.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: "stale-source",
		Generation: 99,
		TMDB:       &api.TMDBMetadata{OriginalLanguage: "English"},
	}
	meta.AudioLanguages = []string{"Japanese"}
	requireDPLanguageFailure(t, dpValidationFailures(t, meta), "language_multilingual_original", trackers.LanguageProhibited)
	meta.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{{
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Languages: []string{"Japanese", "Danish", "Swedish"},
		}},
	})
	if failures := dpValidationFailures(t, meta); len(failures) != 0 {
		t.Fatalf("corrected programme facts did not clear prior prohibition: %+v", failures)
	}
	meta = dpEligibilitySubject("Danish", "Swedish", "English")
	meta.LanguageFacts.OriginalLanguages = []string{"Danish"}
	if failures := dpValidationFailures(t, meta); len(failures) != 0 {
		t.Fatalf("corrected original language did not clear prohibition: %+v", failures)
	}
}

func TestDPOriginalAudioDiscBoundary(t *testing.T) {
	t.Parallel()
	for _, disc := range []string{"", "BDMV", "DVD", "HDDVD"} {
		meta := dpEligibilitySubject("Danish", "Swedish", "English")
		meta.Type, meta.DiscType = "DISC", disc
		if failures := dpValidationFailures(t, meta); len(failures) != 0 {
			t.Fatalf("full disc %q assessed: %+v", disc, failures)
		}
		meta.Type = "REMUX"
		requireDPLanguageFailure(t, dpValidationFailures(t, meta), "language_multilingual_original", trackers.LanguageProhibited)
	}
}

func TestDPOriginalAudioPreservesConstructibility(t *testing.T) {
	t.Parallel()
	definition := unit3d.NewWithProfile(Profile())
	if got := definition.ValidationPolicy().ID; got != "unit3d-dp-constructibility-v1+unit3d-dp-policy-v2" {
		t.Fatalf("validation policy ID = %q", got)
	}
	meta := dpEligibilitySubject("Danish", "Swedish", "English")
	meta.Identity.Category = api.CanonicalCategoryUnknown
	failures := dpValidationFailures(t, meta)
	requireDPLanguageFailure(t, failures, "language_multilingual_original", trackers.LanguageProhibited)
	blocking, err := trackers.FirstBlockingRuleFailure("DP", failures, api.WorkflowExecutionModeDebug, nil)
	if err != nil || blocking == nil || blocking.Rule != "unsupported_category" {
		t.Fatalf("debug bypass hid constructibility: blocking=%+v err=%v", blocking, err)
	}
}

func TestDPOriginalAudioCannotBeWaived(t *testing.T) {
	t.Parallel()
	meta := dpEligibilitySubject("Danish", "Swedish", "English")
	chooseDPMarker(&meta, "MULTi")
	failures := dpValidationFailures(t, meta)
	requireDPLanguageFailure(t, failures, "language_multilingual_original", trackers.LanguageProhibited)
	failures = append(failures, trackers.NewRuleFailure("language_rule", "Existing Nordic requirement", api.RuleDispositionWaivable))
	fingerprint, err := trackers.WaivableRuleFailureFingerprint("DP", failures)
	if err != nil {
		t.Fatal(err)
	}
	for _, authority := range []api.WorkflowFingerprint{"", fingerprint, "stale-authority"} {
		for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
			projection := api.TrackerReleaseProjection{
				TrackerID:                    "DP",
				Readiness:                    api.ReadinessStatusReady,
				DupeReady:                    true,
				UploadReady:                  true,
				WaivableRuleFingerprint:      fingerprint,
				RuleAuthorizationFingerprint: authority,
			}
			blocking, err := trackers.FirstBlockingRuleFailure("DP", failures, mode, &projection)
			if err != nil || (blocking != nil) != (mode == api.WorkflowExecutionModeNormal) {
				t.Fatalf("mode=%s authority=%s blocking=%+v err=%v", mode, authority, blocking, err)
			}
			if err := trackers.ApplyProjectionRuleFailures(&projection, failures, mode, authority, api.NopLogger{}); err != nil {
				t.Fatal(err)
			}
			if projection.DupeReady != (mode == api.WorkflowExecutionModeDebug) || projection.UploadReady != projection.DupeReady {
				t.Fatalf("policy projection permitted normal bypass or blocked debug: %+v", projection)
			}
		}
	}
}

func TestDPOriginalAudioLatePreparation(t *testing.T) {
	definition := unit3d.NewWithProfile(Profile())
	meta := dpEligibilitySubject("Danish", "Swedish", "English")
	meta.ReleaseName = "Manually Reviewed Name MULTi-GRP"
	omit := true
	meta.ReleaseNameOverrides = api.ReleaseNameOverrides{NoDual: &omit, NoDub: &omit}
	meta.StaffUploadTokens = map[string]api.StaffUploadToken{"DP": api.NewStaffUploadToken("synthetic-token")}
	dir := t.TempDir()
	meta.MediaInfoTextPath, meta.TorrentPath = filepath.Join(dir, "MediaInfo.txt"), filepath.Join(dir, "example.torrent")
	for _, path := range []string{meta.MediaInfoTextPath, meta.TorrentPath} {
		if err := os.WriteFile(path, []byte("Synthetic prepared content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chooseDPMarker(&meta, "MULTi")
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		plan, failure := definition.Prepare(t.Context(), trackers.PreparationInput{
			Intent:                    trackers.PreparationIntentDryRun,
			ExecutionMode:             mode,
			Tracker:                   "DP",
			Meta:                      meta,
			RequestedUploadName:       &meta.ReleaseName,
			AuthorizedRuleFingerprint: "stale-authority",
			TrackerConfig:             config.TrackerConfig{APIKey: "synthetic-key"},
			Logger:                    api.NopLogger{},
			Assets:                    &trackers.DescriptionAssets{Description: "Synthetic description", Final: true},
		})
		if mode == api.WorkflowExecutionModeNormal {
			if failure == nil || !strings.Contains(failure.Error(), "Prohibited") {
				t.Fatalf("manual name, token or stale authority bypassed strict eligibility: %v", failure)
			}
			continue
		}
		if failure != nil {
			t.Fatalf("debug preparation blocked: %v", failure)
		}
		if _, err := plan.Submit(t.Context()); !errors.Is(err, trackers.ErrPlanNotSubmittable) {
			t.Fatalf("debug preview allowed submission: %v", err)
		}
		if err := plan.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func dpEligibilitySubject(languages ...string) api.UploadSubject {
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "audio-0",
	}
	for index, language := range languages {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:           "audio-" + strconv.Itoa(index),
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Languages:    []string{language},
			Codec:        "AC-3",
			AudioLabel:   "DD 5.1",
			Default:      index == 0,
			DefaultKnown: true,
		})
	}
	return api.UploadSubject{
		Identity:  api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 1234567},
		Type:      "WEBDL",
		Source:    "WEB",
		Container: "mkv",
		Release: api.ReleaseInfo{
			Title:      "Example",
			Year:       2026,
			Resolution: "1080p",
		},
		Tag:               "-GRP",
		LanguageFacts:     mediafacts.ResolveLanguages(media),
		AudioLanguages:    slices.Clone(languages),
		SubtitleLanguages: []string{"English"},
		Assessments:       api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusNotApplicable},
	}
}

func dpValidationFailures(t *testing.T, meta api.UploadSubject) []api.RuleFailure {
	t.Helper()
	failures, err := unit3d.NewWithProfile(Profile()).ValidationPolicy().Check(t.Context(), api.NewTrackerValidationSubject(meta, "DP"), api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	return failures
}

func requireDPLanguageFailure(t *testing.T, failures []api.RuleFailure, rule string, outcome trackers.LanguageOutcome) {
	t.Helper()
	status := api.MetadataEvidenceStatusComplete
	if outcome == trackers.LanguageUnresolved {
		status = api.MetadataEvidenceStatusPartial
	}
	if !slices.ContainsFunc(failures, func(failure api.RuleFailure) bool {
		return failure.Rule == rule && strings.HasPrefix(failure.Reason, string(outcome)) && failure.Disposition == api.RuleDispositionStrict && failure.EvidenceStatus == status && failure.DebugBypass
	}) {
		t.Fatalf("missing %s/%s: %+v", rule, outcome, failures)
	}
}

func chooseDPMarker(meta *api.UploadSubject, marker string) {
	key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(*meta, "DP"), manualLanguageMarkerKey)
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"DP": {key: marker}}
}
