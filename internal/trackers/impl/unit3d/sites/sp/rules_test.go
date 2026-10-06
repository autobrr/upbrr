// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// collectedPackSubject models the producer's measured components: source and
// encoding provenance remain unknown despite complete language inspection.
func collectedPackSubject() api.UploadSubject {
	files := []string{"Example.Show.S01E01.mkv", "Example.Show.S01E02.mkv"}
	meta := api.UploadSubject{
		SourcePath: "Example.Show.S01",
		TVPack:     true,
		SeasonInt:  1,
		Type:       "WEBDL",
		FileList:   files,
		Identity: api.ExternalIdentity{
			SourcePath: "Example.Show.S01",
			Generation: 1,
			Category:   api.CanonicalCategoryTV,
		},
		MediaFileFacts: api.MediaFileFacts{
			ExpectedFileCount: 2,
			Status:            api.MetadataEvidenceStatusPartial,
			TechnicalStatus:   api.MetadataEvidenceStatusPartial,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
		},
		TrackerQuestionnaireAnswers: map[string]map[string]string{"SP": {}},
	}
	for _, file := range files {
		meta.MediaFileFacts.Files = append(meta.MediaFileFacts.Files, api.MediaFileFact{
			FileName:        file,
			Container:       "mkv",
			Resolution:      "1080p",
			VideoCodec:      "AVC",
			BitDepth:        "8",
			VideoTrackCount: 1,
			AudioLanguages:  []string{"Japanese"},
			AudioStatus:     api.MetadataEvidenceStatusComplete,
			SubtitleStatus:  api.MetadataEvidenceStatusComplete,
		})
	}
	return meta
}

func answerPackQuestion(meta *api.UploadSubject, key, answer string) {
	subject := api.NewTrackerValidationSubject(*meta, "SP")
	meta.TrackerQuestionnaireAnswers["SP"][packQuestionKey(subject, key)] = answer
}

func requirePackBlocked(t *testing.T, meta api.UploadSubject, status api.MetadataEvidenceStatus) {
	t.Helper()
	failures := packUniformityFailures(api.NewTrackerValidationSubject(meta, "SP"))
	if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionStrict || failures[0].EvidenceStatus != status {
		t.Fatalf("pack must stay blocked with status %s: %#v", status, failures)
	}
	if !trackers.RuleFailureBlocksExecution(failures[0], api.WorkflowExecutionModeNormal, true) {
		t.Fatal("generic warning acknowledgement waived the pack consistency finding")
	}
}

func TestSPMeasuredPackNeedsOnlySourceAttestation(t *testing.T) {
	meta := collectedPackSubject()
	requirePackBlocked(t, meta, api.MetadataEvidenceStatusPartial)
	questionnaire := packQuestionnaire(trackers.PreparationInput{Meta: meta})
	if questionnaire == nil || len(questionnaire.Fields) != 1 || !strings.Contains(questionnaire.Fields[0].Help, "filenames") {
		t.Fatalf("missing bounded source evidence question: %#v", questionnaire)
	}
	answerPackQuestion(&meta, packSourceKey, "consistent")
	if failures := packUniformityFailures(api.NewTrackerValidationSubject(meta, "SP")); len(failures) != 0 {
		t.Fatalf("known empty subtitles or unknown provenance made measured pack incomplete: %#v", failures)
	}
	if meta.MediaFileFacts.Status != api.MetadataEvidenceStatusPartial || meta.MediaFileFacts.Files[1].Source != "" {
		t.Fatal("source attestation fabricated per-file facts")
	}
}

func TestSPSourceVariationRequiresCurrentExplanation(t *testing.T) {
	for _, change := range []string{"audio", "subtitles", "resolution", "codec", "container", "bit depth", "video tracks", "source"} {
		t.Run(change, func(t *testing.T) {
			meta := collectedPackSubject()
			switch change {
			case "audio":
				meta.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
			case "subtitles":
				meta.MediaFileFacts.Files[1].SubtitleLanguages = []string{"English"}
			case "resolution":
				meta.MediaFileFacts.Files[1].Resolution = "720p"
			case "codec":
				meta.MediaFileFacts.Files[1].VideoCodec = "HEVC"
			case "container":
				meta.MediaFileFacts.Files[1].Container = "mp4"
			case "bit depth":
				meta.MediaFileFacts.Files[1].BitDepth = "10"
			case "video tracks":
				meta.MediaFileFacts.Files[1].VideoTrackCount = 2
			}
			sourceAnswer := "consistent"
			if change == "source" {
				sourceAnswer = "different"
			}
			answerPackQuestion(&meta, packSourceKey, sourceAnswer)
			requirePackBlocked(t, meta, api.MetadataEvidenceStatusPartial)
			answerPackQuestion(&meta, packVariationKey, "no")
			requirePackBlocked(t, meta, api.MetadataEvidenceStatusComplete)
			answerPackQuestion(&meta, packVariationKey, "yes")
			requirePackBlocked(t, meta, api.MetadataEvidenceStatusPartial)
			answerPackQuestion(&meta, packExplanationKey, "Episode 2 retains the characteristics of its different original source.")
			if failures := packUniformityFailures(api.NewTrackerValidationSubject(meta, "SP")); len(failures) != 0 {
				t.Fatal(failures)
			}
		})
	}
}

func TestSPVariationCannotExplainAwayUnknownEvidence(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*api.UploadSubject)
	}{
		{"failed probe", func(meta *api.UploadSubject) {
			meta.MediaFileFacts.Files[1] = api.MediaFileFact{FileName: meta.FileList[1]}
		}},
		{"partial video report", func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].VideoTrackCount = 0 }},
		{"unknown container", func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].Container = "" }},
		{"unmeasured zero bit depth", func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].BitDepth = "0" }},
		{"unknown bit depth", func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].BitDepth = "" }},
		{"unknown audio", func(meta *api.UploadSubject) {
			meta.MediaFileFacts.Files[1].AudioStatus = api.MetadataEvidenceStatusPartial
		}},
		{"unknown subtitles", func(meta *api.UploadSubject) {
			meta.MediaFileFacts.Files[1].SubtitleStatus = api.MetadataEvidenceStatusUnavailable
		}},
		{"unrecognized language", func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].AudioLanguages = []string{"und"} }},
		{"missing file", func(meta *api.UploadSubject) { meta.MediaFileFacts.Files = meta.MediaFileFacts.Files[:1] }},
	} {
		t.Run(change.name, func(t *testing.T) {
			meta := collectedPackSubject()
			change.mutate(&meta)
			answerPackQuestion(&meta, packSourceKey, "different")
			answerPackQuestion(&meta, packVariationKey, "yes")
			answerPackQuestion(&meta, packExplanationKey, "The sources differ.")
			requirePackBlocked(t, meta, api.MetadataEvidenceStatusPartial)
		})
	}
}

func TestSPChangedPackEvidenceInvalidatesException(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*api.UploadSubject)
	}{
		{"second file language", func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].AudioLanguages = []string{"French"} }},
		{"second file technical facts", func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].Resolution = "720p" }},
		{"package membership", func(meta *api.UploadSubject) { meta.FileList = append(meta.FileList, "Example.Show.S01E03.mkv") }},
		{"source attestation", func(meta *api.UploadSubject) { answerPackQuestion(meta, packSourceKey, "different") }},
		{"generation", func(meta *api.UploadSubject) { meta.Identity.Generation++ }},
		{"manual language clear", func(meta *api.UploadSubject) { meta.LanguageFacts.AudioStatus = api.MetadataEvidenceStatusUnavailable }},
	} {
		t.Run(change.name, func(t *testing.T) {
			meta := collectedPackSubject()
			meta.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
			answerPackQuestion(&meta, packSourceKey, "consistent")
			answerPackQuestion(&meta, packVariationKey, "yes")
			answerPackQuestion(&meta, packExplanationKey, "Episode 2 has a different source language.")
			change.mutate(&meta)
			requirePackBlocked(t, meta, api.MetadataEvidenceStatusPartial)
		})
	}
}

func TestSPPackPolicyPreservesDiscAndIndependentFailures(t *testing.T) {
	meta := collectedPackSubject()
	meta.DiscType, meta.Type = "BDMV", "REMUX"
	requirePackBlocked(t, meta, api.MetadataEvidenceStatusPartial)
	meta.Type = "DISC"
	if failures := packUniformityFailures(api.NewTrackerValidationSubject(meta, "SP")); len(failures) != 0 || packQuestionnaire(trackers.PreparationInput{Meta: meta}) != nil {
		t.Fatalf("new consistency assessment changed full-disc path: %#v", failures)
	}
	subject := spPassingSubject()
	subject.Tracker = "SP"
	subject.MediaFileFacts.Files[1].AudioLanguages = []string{"Japanese"}
	subject.PackageFacts.DetectedEpisodes[0].Episodes = []int{1, 3}
	subject.QuestionnaireAnswers = map[string]string{packQuestionKey(subject, packVariationKey): "yes", packQuestionKey(subject, packExplanationKey): "Episode 2 retains its different source language."}
	failures, err := ValidationPolicy().Check(t.Context(), subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(failures, func(failure api.RuleFailure) bool {
		return failure.Rule == "sp_pack_completeness" && failure.Disposition == api.RuleDispositionStrict
	}) {
		t.Fatalf("variation hid pack incompleteness: %#v", failures)
	}
}

func TestSPQuestionnaireScopesVariationToEstablishedSourceAnswer(t *testing.T) {
	meta := collectedPackSubject()
	meta.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
	questionnaire := packQuestionnaire(trackers.PreparationInput{Meta: meta})
	if questionnaire == nil || len(questionnaire.Fields) != 1 {
		t.Fatalf("variation asked before its source scope was established: %#v", questionnaire)
	}
	answerPackQuestion(&meta, packSourceKey, "consistent")
	questionnaire = packQuestionnaire(trackers.PreparationInput{Meta: meta})
	if len(questionnaire.Fields) != 2 || !strings.Contains(questionnaire.Fields[1].Help, "German") || !strings.Contains(questionnaire.Fields[1].Help, meta.FileList[1]) {
		t.Fatalf("variation question omitted actual differing evidence: %#v", questionnaire)
	}
	answerPackQuestion(&meta, packVariationKey, "yes")
	questionnaire = packQuestionnaire(trackers.PreparationInput{Meta: meta})
	if len(questionnaire.Fields) != 3 || questionnaire.Fields[2].Kind != "textarea" {
		t.Fatalf("missing factual explanation field: %#v", questionnaire)
	}
	answerPackQuestion(&meta, packSourceKey, "different")
	questionnaire = packQuestionnaire(trackers.PreparationInput{Meta: meta})
	if len(questionnaire.Fields) != 2 || questionnaire.Fields[1].Value != "" {
		t.Fatalf("changed source answer retained prior variation approval: %#v", questionnaire)
	}
}
