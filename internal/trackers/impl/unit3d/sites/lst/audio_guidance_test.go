// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTPrimaryFLACBitDepthGuidance(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*api.TrackerValidationSubject)
		want   bool
	}{
		{name: "stereo", want: true},
		{
			name:   "mono",
			mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Channels = 1 },
			want:   true,
		},
		{
			name:   "anime",
			mutate: func(s *api.TrackerValidationSubject) { s.Anime = true },
			want:   true,
		},
		{
			name:   "personal",
			mutate: func(s *api.TrackerValidationSubject) { s.PersonalRelease = true },
			want:   true,
		},
		{name: "16 bit", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].BitDepth = 16 }},
		{name: "unknown depth", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].BitDepth = 0 }},
		{name: "video depth is not audio", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].BitDepth = 0; s.BitDepth = "24" }},
		{name: "multichannel", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Channels = 6 }},
		{name: "three channels", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Channels = 3 }},
		{name: "unknown channels", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Channels = 0 }},
		{name: "partial channels", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].ChannelsKnown = false }},
		{name: "lossy", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Codec = "DD" }},
		{name: "unknown codec", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Codec = "" }},
		{name: "aggregate label is not codec", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Codec = "AAC"; s.Audio = "FLAC 2.0" }},
		{name: "missing primary", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.PrimaryAudioTrackID = "" }},
		{name: "unmatched primary", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.PrimaryAudioTrackID = "missing" }},
		{name: "duplicate primary", mutate: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, s.LanguageFacts.Tracks[0])
		}},
		{name: "secondary FLAC", mutate: func(s *api.TrackerValidationSubject) {
			secondary := s.LanguageFacts.Tracks[0]
			secondary.ID, secondary.StreamOrder, secondary.Default = "secondary", 2, false
			s.LanguageFacts.Tracks[0].Codec = "AAC"
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, secondary)
		}},
		{name: "unknown role", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = "" }},
		{name: "commentary", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = api.AudioRoleCommentary }},
		{name: "commentary marker", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Commentary = true }},
		{name: "isolated score", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = api.AudioRoleIsolatedScore }},
		{name: "alternate mix", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].Role = api.AudioRoleAlternateMix }},
		{name: "unknown order", mutate: func(s *api.TrackerValidationSubject) { s.LanguageFacts.Tracks[0].StreamOrderKnown = false }},
		{name: "unresolved competing order", mutate: func(s *api.TrackerValidationSubject) {
			other := s.LanguageFacts.Tracks[0]
			other.ID, other.StreamOrderKnown = "other", false
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, other)
		}},
		{name: "earlier competing programme", mutate: func(s *api.TrackerValidationSubject) {
			other := s.LanguageFacts.Tracks[0]
			other.ID, other.StreamOrder = "other", 0
			s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, other)
		}},
		{
			name: "earlier commentary",
			mutate: func(s *api.TrackerValidationSubject) {
				other := s.LanguageFacts.Tracks[0]
				other.ID, other.StreamOrder, other.Role = "commentary", 0, api.AudioRoleCommentary
				s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, other)
			},
			want: true,
		},
		{name: "720p", mutate: func(s *api.TrackerValidationSubject) { s.Release.Resolution = "720p" }},
		{name: "2160p", mutate: func(s *api.TrackerValidationSubject) { s.Release.Resolution = "2160p" }},
		{name: "missing resolution", mutate: func(s *api.TrackerValidationSubject) { s.Release.Resolution = "" }},
		{name: "remux", mutate: func(s *api.TrackerValidationSubject) { s.Type = "REMUX" }},
		{name: "web download", mutate: func(s *api.TrackerValidationSubject) { s.Type = "WEBDL" }},
		{name: "web rip", mutate: func(s *api.TrackerValidationSubject) { s.Type = "WEBRIP" }},
		{name: "missing type", mutate: func(s *api.TrackerValidationSubject) { s.Type = "" }},
		{name: "disc", mutate: func(s *api.TrackerValidationSubject) { s.DiscType = "BDMV" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := lstValidationSubject()
			subject.Type, subject.Release.Resolution = "ENCODE", "1080p"
			track := &subject.LanguageFacts.Tracks[0]
			track.Codec, track.Channels, track.BitDepth = "FLAC", 2, 24
			track.ChannelsKnown = true
			track.StreamOrder, track.StreamOrderKnown = 1, true
			if test.mutate != nil {
				test.mutate(&subject)
			}
			failures, err := Profile().ValidationPolicy.Check(t.Context(), subject, nil)
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "guidance_lst_flac_bit_depth" })
			if (index >= 0) != test.want {
				t.Fatalf("guidance=%t want=%t: %+v", index >= 0, test.want, failures)
			}
			if index < 0 {
				return
			}
			failure := failures[index]
			if failure.Disposition != api.RuleDispositionAdvisory || failure.EvidenceStatus != api.MetadataEvidenceStatusComplete ||
				!strings.Contains(failure.Reason, "16-bit") || !strings.Contains(failure.Reason, "dither") {
				t.Fatalf("unexpected guidance: %+v", failure)
			}
			for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
				if trackers.RuleFailureBlocksExecution(failure, mode, false) {
					t.Fatalf("guidance blocks %s", mode)
				}
			}
			if q := languageQuestionnaire(trackers.PreparationInput{Meta: api.UploadSubject{Type: subject.Type, LanguageFacts: subject.LanguageFacts}}); q != nil {
				t.Fatalf("guidance added a questionnaire: %+v", q)
			}
		})
	}
}

func TestLSTAudioGuidanceKeepsIndependentRequirements(t *testing.T) {
	subject := lstValidationSubject()
	subject.Type, subject.Release.Resolution = "ENCODE", "1080p"
	track := &subject.LanguageFacts.Tracks[0]
	track.Codec, track.Channels, track.BitDepth = "FLAC", 2, 24
	track.ChannelsKnown = true
	track.Default = false
	failures, err := Profile().ValidationPolicy.Check(t.Context(), subject, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "guidance_lst_flac_bit_depth" && f.Disposition == api.RuleDispositionAdvisory
	}) || !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_default_audio" && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
	}) {
		t.Fatalf("guidance changed independent default requirement: %+v", failures)
	}
}
