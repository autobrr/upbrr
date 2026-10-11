// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSPPackGuidanceIsPassive(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
	}{
		{name: "consistent measured files"},
		{"no per-file inspection", func(subject *api.TrackerValidationSubject) {
			subject.MediaFileFacts = api.MediaFileFacts{}
		}},
		{"partial inspection", func(subject *api.TrackerValidationSubject) {
			subject.MediaFileFacts.Files = subject.MediaFileFacts.Files[:1]
			subject.MediaFileFacts.Status = api.MetadataEvidenceStatusPartial
		}},
		{"contradictory inspection", func(subject *api.TrackerValidationSubject) {
			subject.MediaFileFacts.Status = api.MetadataEvidenceStatusContradictory
		}},
		{"known differences", func(subject *api.TrackerValidationSubject) {
			subject.MediaFileFacts.Files[1] = api.MediaFileFact{
				FileName:          "Example.Show.S01E02.mkv",
				Source:            "Blu-ray",
				Resolution:        "720p",
				VideoCodec:        "HEVC",
				Container:         "mp4",
				BitDepth:          "10",
				VideoTrackCount:   2,
				AudioLanguages:    []string{"Japanese"},
				AudioStatus:       api.MetadataEvidenceStatusComplete,
				SubtitleLanguages: []string{"German"},
				SubtitleStatus:    api.MetadataEvidenceStatusComplete,
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := spPassingSubject()
			if test.mutate != nil {
				test.mutate(&subject)
			}
			before := subject.MediaFileFacts.Clone()
			failures, err := ValidationPolicy().Check(t.Context(), subject, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			if len(failures) != 1 || failures[0].Rule != "guidance_sp_pack_consistency" ||
				failures[0].Disposition != api.RuleDispositionAdvisory || !strings.HasPrefix(failures[0].Reason, "Guidance") {
				t.Fatalf("pack consistency must produce one passive reminder: %#v", failures)
			}
			for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
				if trackers.RuleFailureBlocksExecution(failures[0], mode, false) {
					t.Fatalf("mode=%s: pack guidance required acknowledgement", mode)
				}
			}
			if !reflect.DeepEqual(before, subject.MediaFileFacts) {
				t.Fatal("pack guidance changed source facts")
			}
		})
	}
}

func TestSPPackGuidanceScope(t *testing.T) {
	for _, test := range []struct {
		name     string
		tvPack   bool
		discType string
		typeName string
		want     bool
	}{
		{name: "single episode", typeName: "WEBDL"},
		{
			name:     "non-disc pack",
			tvPack:   true,
			typeName: "WEBDL",
			want:     true,
		},
		{
			name:     "remux with disc label",
			tvPack:   true,
			discType: "BDMV",
			typeName: "REMUX",
			want:     true,
		},
		{
			name:     "full disc",
			tvPack:   true,
			discType: "BDMV",
			typeName: "DISC",
		},
		{
			name:     "full disc without disc label",
			tvPack:   true,
			typeName: "DISC",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := spPassingSubject()
			subject.TVPack, subject.DiscType, subject.Type = test.tvPack, test.discType, test.typeName
			failures, err := ValidationPolicy().Check(t.Context(), subject, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			got := slices.ContainsFunc(failures, func(failure api.RuleFailure) bool {
				return failure.Rule == "guidance_sp_pack_consistency"
			})
			if got != test.want {
				t.Fatalf("pack guidance=%t, want %t: %#v", got, test.want, failures)
			}
		})
	}
}

func TestSPPackGuidanceRequiresNoQuestionnaireOrDescriptionChanges(t *testing.T) {
	profile := Profile()
	if profile.Site.ProjectionQuestionnaire != nil || profile.Site.FinalizeDescription != nil {
		t.Fatal("passive pack guidance retained a questionnaire or description transformation")
	}
}
