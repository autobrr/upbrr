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

func TestAudioTierMatrix(t *testing.T) {
	for _, resolution := range []string{"720p", "1080p", "2160p"} {
		for _, test := range []struct {
			name, codec, layout string
			depth, channels     int
			anime               bool
			role                api.AudioTrackRole
			wantHD, wantGlobal  bool
		}{
			{
				name:     "lossy DTS",
				codec:    "DTS",
				layout:   "5.1",
				channels: 6,
			},
			{
				name:     "stereo FLAC",
				codec:    "FLAC",
				layout:   "2.0",
				channels: 2,
				depth:    16,
			},
			{
				name:     "mono FLAC",
				codec:    "FLAC",
				layout:   "1.0",
				channels: 1,
				depth:    16,
			},
			{
				name:     "24 bit",
				codec:    "FLAC",
				layout:   "2.0",
				channels: 2,
				depth:    24,
				wantHD:   true,
			},
			{
				name:     "20 bit",
				codec:    "FLAC",
				layout:   "2.0",
				channels: 2,
				depth:    20,
				wantHD:   true,
			},
			{
				name:     "anime 5.1",
				codec:    "FLAC",
				layout:   "L R C LFE Ls Rs",
				channels: 6,
				depth:    16,
				anime:    true,
			},
			{
				name:       "anime 6.0",
				codec:      "FLAC",
				layout:     "6.0",
				channels:   6,
				depth:      16,
				anime:      true,
				wantHD:     true,
				wantGlobal: true,
			},
			{
				name:       "anime 7.1",
				codec:      "FLAC",
				layout:     "7.1",
				channels:   8,
				depth:      16,
				anime:      true,
				wantHD:     true,
				wantGlobal: true,
			},
			{
				name:       "non anime 5.1",
				codec:      "FLAC",
				layout:     "5.1",
				channels:   6,
				depth:      16,
				wantHD:     true,
				wantGlobal: true,
			},
			{
				name:     "lossless DTS",
				codec:    "DTS-HD MA",
				layout:   "5.1",
				channels: 6,
				depth:    24,
				wantHD:   true,
			},
			{
				name:     "commentary",
				codec:    "FLAC",
				layout:   "2.0",
				channels: 2,
				depth:    16,
				role:     api.AudioRoleCommentary,
				wantHD:   true,
			},
			{
				name:     "score anime",
				codec:    "FLAC",
				layout:   "5.1",
				channels: 6,
				depth:    16,
				anime:    true,
				role:     api.AudioRoleIsolatedScore,
				wantHD:   true,
			},
			{
				name:     "alternate mix",
				codec:    "FLAC",
				layout:   "2.0",
				channels: 2,
				depth:    24,
				role:     api.AudioRoleAlternateMix,
				wantHD:   true,
			},
		} {
			t.Run(resolution+"/"+test.name, func(t *testing.T) {
				s := lstValidationSubject()
				s.Type = "ENCODE"
				s.Release.Resolution = resolution
				s.Anime = test.anime
				track := &s.LanguageFacts.Tracks[0]
				track.ResourceID = "episode-a"
				track.Codec = test.codec
				track.ChannelLayout = test.layout
				track.Channels = test.channels
				track.ChannelsKnown = true
				track.BitDepth = test.depth
				if test.role != "" {
					track.Role = test.role
				}
				failures := audioTierFailures(s)
				want := test.wantHD
				if resolution == "2160p" {
					want = test.wantGlobal
				}
				strict := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
					return f.Disposition == api.RuleDispositionStrict && f.EvidenceStatus == api.MetadataEvidenceStatusComplete
				})
				if strict != want {
					t.Fatalf("strict=%t want=%t: %+v", strict, want, failures)
				}
				for _, f := range failures {
					if strings.Contains(f.Rule, "guidance_lst_flac_bit_depth") {
						t.Fatal("obsolete advisory")
					}
					if f.Disposition == api.RuleDispositionStrict && !trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true) {
						t.Fatalf("acknowledgement waived %+v", f)
					}
				}
			})
		}
	}
}

func TestAudioEvidenceAndProcessing(t *testing.T) {
	s := lstValidationSubject()
	s.Type = "ENCODE"
	s.Release.Resolution = "1080p"
	s.Anime = true
	track := &s.LanguageFacts.Tracks[0]
	track.Codec = "FLAC"
	track.BitDepth = 16
	track.Channels = 6
	track.ChannelsKnown = true
	track.ResourceID = "episode-a"
	for _, layout := range []string{"", "0", " 0X00000000 "} {
		track.ChannelLayout = layout
		failures := audioTierFailures(s)
		if len(failures) != 1 || failures[0].Rule != "language_lst_audio_layout_assumed" ||
			failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial || failures[0].Disposition != api.RuleDispositionAdvisory ||
			trackers.RuleFailureBlocksExecution(failures[0], api.WorkflowExecutionModeNormal, false) {
			t.Fatalf("layout %q needs one non-blocking assumption warning: %+v", layout, failures)
		}
		if track.ChannelLayout != layout || exactAudioLayout(*track) != "" {
			t.Fatal("eligibility assumption changed measured layout evidence")
		}
	}
	track.ChannelLayout = "5.1"
	if got := audioTierFailures(s); len(got) != 0 {
		t.Fatalf("unknown processing blocked: %+v", got)
	}
	key := trackers.LanguageQuestionKey(s, "audio_processing_"+track.ID)
	s.QuestionnaireAnswers = map[string]string{key: "reduced_without_dither"}
	if got := audioTierFailures(s); len(got) != 0 {
		t.Fatalf("retired dither answer manufactured a finding: %+v", got)
	}
	s.QuestionnaireAnswers[key] = "reduced_with_dither"
	if got := audioTierFailures(s); len(got) != 0 {
		t.Fatalf("dithered rejected: %+v", got)
	}
	s.QuestionnaireAnswers[key] = "reduced_without_dither"
	track.ID = "changed"
	if got := audioTierFailures(s); len(got) != 0 {
		t.Fatalf("stale answer reused: %+v", got)
	}
	track.Channels = 2
	if exactAudioLayout(*track) != "" {
		t.Fatal("contradictory count/layout accepted")
	}
}

func TestAudioLayoutAssumptionPreservesEligibilityBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, codec, layout, wantRule string
		channels                      int
		channelsKnown, animeKnown     bool
		anime                         bool
	}{
		{"non-anime", "FLAC", "", "language_lst_audio_layout", 6, true, true, false},
		{"unknown anime", "FLAC", "", "language_lst_audio_anime_evidence", 6, true, false, true},
		{"unknown count", "FLAC", "", "language_lst_audio_layout_evidence", 6, false, true, true},
		{"contradictory count", "FLAC", "2.0", "language_lst_audio_layout_evidence", 6, true, true, true},
		{"nonzero mask", "FLAC", "0x0000003F", "language_lst_audio_layout_evidence", 6, true, true, true},
		{"unrecognized explicit layout", "FLAC", "L R C Lb Rb Cb", "language_lst_audio_layout_evidence", 6, true, true, true},
		{"other lossless codec", "LPCM", "", "language_lst_audio_codec", 6, true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := lstValidationSubject()
			s.Type = "ENCODE"
			s.Release.Resolution = "1080p"
			s.Anime = test.anime
			s.AnimeKnown = test.animeKnown
			track := &s.LanguageFacts.Tracks[0]
			track.Codec = test.codec
			track.BitDepth = 16
			track.ChannelLayout = test.layout
			track.Channels = test.channels
			track.ChannelsKnown = test.channelsKnown
			if !slices.ContainsFunc(audioTierFailures(s), func(f api.RuleFailure) bool {
				return f.Rule == test.wantRule && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true)
			}) {
				t.Fatalf("eligibility boundary bypassed: %+v", audioTierFailures(s))
			}
		})
	}
}

func TestAudioLayoutAssumptionIsReleaseScoped(t *testing.T) {
	s := lstValidationSubject()
	s.Anime = true
	track := s.LanguageFacts.Tracks[0]
	track.Codec = "FLAC"
	track.BitDepth = 16
	track.Channels = 6
	track.ChannelsKnown = true
	track.ID, track.ResourceID = "audio-a", "episode-a"
	s.LanguageFacts.Tracks = []api.MediaTrackFacts{track}
	track.ID, track.ResourceID = "audio-b", "episode-b"
	s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, track)
	track.ID, track.ResourceID, track.ChannelLayout = "audio-c", "episode-c", "6.0"
	s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, track)
	failures := audioTierFailures(s)
	if len(failures) != 2 {
		t.Fatalf("expected one assumption warning and the explicit layout violation: %+v", failures)
	}
	requireLSTValidationFailure(t, failures, "language_lst_audio_layout", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	requireLSTValidationFailure(t, failures, "language_lst_audio_layout_assumed", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusPartial)
	if !strings.Contains(failures[1].Reason, "2 six-channel FLAC") {
		t.Fatalf("warning omitted affected stream count: %+v", failures[1])
	}
}

func TestAudioSecondaryResourcesAndTierIsolation(t *testing.T) {
	s := lstValidationSubject()
	s.Type = "ENCODE"
	s.Release.Resolution = "720p"
	s.LanguageFacts.Tracks = append(s.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:            "secondary",
		ResourceID:    "episode-b",
		Kind:          api.MediaTrackAudio,
		Role:          api.AudioRoleProgramme,
		Codec:         "FLAC",
		ChannelLayout: "2.0",
		Channels:      2,
		ChannelsKnown: true,
		BitDepth:      24,
	})
	if got := audioTierFailures(s); len(got) != 1 || !strings.Contains(got[0].Reason, "episode-b") {
		t.Fatalf("secondary resource missed: %+v", got)
	}
	for _, kind := range []string{"REMUX", "WEBDL", "WEBRIP"} {
		s.Type = kind
		if got := audioTierFailures(s); len(got) != 0 {
			t.Fatalf("%s inherited encode limit: %+v", kind, got)
		}
	}
	s.Type = "ENCODE"
	s.DiscType = "BDMV"
	if got := audioTierFailures(s); len(got) != 0 {
		t.Fatalf("disc inherited encode limit: %+v", got)
	}
}

func TestAudioTierUsesEachResourceResolution(t *testing.T) {
	s := lstValidationSubject()
	s.Type = "ENCODE"
	s.Release.Resolution = "1080p"
	s.LanguageFacts.Tracks = []api.MediaTrackFacts{
		{
			ID:            "a",
			ResourceID:    "episode-a",
			Kind:          api.MediaTrackAudio,
			Role:          api.AudioRoleProgramme,
			Codec:         "FLAC",
			ChannelLayout: "2.0",
			BitDepth:      24,
		},
		{
			ID:            "b",
			ResourceID:    "episode-b",
			Kind:          api.MediaTrackAudio,
			Role:          api.AudioRoleProgramme,
			Codec:         "FLAC",
			ChannelLayout: "2.0",
			BitDepth:      24,
		},
	}
	s.MediaFileFacts.Files = []api.MediaFileFact{{ResourceID: "episode-a", Resolution: "2160p"}, {ResourceID: "episode-b", Resolution: "720p"}}
	failures := audioTierFailures(s)
	if len(failures) != 1 || !strings.Contains(failures[0].Reason, "episode-b") {
		t.Fatalf("release tier contaminated resources: %+v", failures)
	}
	s.MediaFileFacts.Files[1].Resolution = ""
	failures = audioTierFailures(s)
	if len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("unknown tier: %+v", failures)
	}
}

func TestAudioUnknownAnimeAndRole(t *testing.T) {
	s := lstValidationSubject()
	s.Type = "ENCODE"
	s.Release.Resolution = "720p"
	s.AnimeKnown = false
	s.LanguageFacts.Tracks = []api.MediaTrackFacts{{
		ID:            "a",
		ResourceID:    "episode-a",
		Kind:          api.MediaTrackAudio,
		Codec:         "FLAC",
		ChannelLayout: "5.1",
		BitDepth:      16,
	}}
	failures := audioTierFailures(s)
	if len(failures) != 2 {
		t.Fatalf("missing eligibility evidence: %+v", failures)
	}
	for _, failure := range failures {
		if failure.EvidenceStatus != api.MetadataEvidenceStatusPartial {
			t.Fatalf("unknown treated as violated: %+v", failure)
		}
	}
}

func TestDitherHistoryCannotBeEstablishedBySavedAnswers(t *testing.T) {
	s := lstValidationSubject()
	s.Type = "ENCODE"
	s.Release.Resolution = "1080p"
	track := &s.LanguageFacts.Tracks[0]
	track.Codec = "FLAC"
	track.ChannelLayout = "2.0"
	track.BitDepth = 16
	key := trackers.LanguageQuestionKey(s, "audio_processing_"+track.ID)
	for _, answer := range []string{"reduced_dither_unknown", "native_16_bit", "reduced_without_dither"} {
		s.QuestionnaireAnswers[key] = answer
		if failures := audioTierFailures(s); len(failures) != 0 {
			t.Fatalf("saved answer manufactured source history: %+v", failures)
		}
		failures := audioProcessingGuidance(s)
		if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionAdvisory || failures[0].QuestionnaireInput {
			t.Fatalf("dither guidance not passive: %+v", failures)
		}
	}
}

func TestAudioCompressionClassNeedsPositiveEvidence(t *testing.T) {
	for _, codec := range []string{"WavPack", "Monkey's Audio", "unclassified-format"} {
		for _, role := range []api.AudioTrackRole{api.AudioRoleProgramme, api.AudioRoleCommentary} {
			s := lstValidationSubject()
			s.Type = "ENCODE"
			s.Release.Resolution = "1080p"
			s.LanguageFacts.Tracks = []api.MediaTrackFacts{{
				ID:            "a",
				ResourceID:    "resource",
				Kind:          api.MediaTrackAudio,
				Role:          role,
				Codec:         codec,
				ChannelLayout: "2.0",
				BitDepth:      16,
			}}
			failures := audioTierFailures(s)
			if len(failures) == 0 || failures[0].Disposition != api.RuleDispositionStrict {
				t.Fatalf("unclassified or unsupported compression accepted: %s %+v", codec, failures)
			}
			if (failures[0].EvidenceStatus == api.MetadataEvidenceStatusPartial) != (codec == "unclassified-format") {
				t.Fatalf("wrong evidence state: %s %+v", codec, failures)
			}
		}
	}
}
