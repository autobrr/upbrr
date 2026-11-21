// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"fmt"
	"strings"
	"testing"

	gomediainfo "github.com/autobrr/go-mediainfo"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSAMAudioCoexistenceUsesCompleteDirectionalEvidence(t *testing.T) {
	t.Parallel()
	rendered, err := gomediainfo.Render([]gomediainfo.Report{{
		Ref:     "Example.mkv",
		General: gomediainfo.Stream{Kind: gomediainfo.StreamGeneral, Fields: []gomediainfo.Field{{Name: "Format", Value: "Matroska"}}},
		Streams: []gomediainfo.Stream{
			{Kind: gomediainfo.StreamVideo, Fields: []gomediainfo.Field{{Name: "Format", Value: "AVC"}}},
			{Kind: gomediainfo.StreamAudio, Fields: []gomediainfo.Field{
				{Name: "Format", Value: "AAC"},
				{Name: "Channel(s)", Value: "6 channels"},
				{Name: "Language", Value: "Japanese"},
			}},
			{Kind: gomediainfo.StreamMenu, Fields: []gomediainfo.Field{{Name: "Format", Value: "Timed Text"}}},
		},
	}}, gomediainfo.OutputText)
	if err != nil {
		t.Fatal(err)
	}
	const original = "Audio #1\nFormat : E-AC-3\nChannel(s) : 6 channels\nLanguage : Japanese\n"
	const brazilian = "Audio #2\nFormat : AAC\nChannel(s) : 2 channels\nLanguage : Portuguese (Brazil)\n"
	const portuguese = "Audio #2\nFormat : AAC\nChannel(s) : 2 channels\nLanguage : Portuguese\n"
	const ambiguousPortuguese = "Audio #2\nFormat : E-AC-3\nChannel(s) : 6 channels\nLanguage : pt-BR\n" +
		"Audio #3\nFormat : AAC\nChannel(s) : 2 channels\nLanguage : Portuguese\n"
	const french = "Audio #2\nFormat : AAC\nChannel(s) : 2 channels\nLanguage : French\n"
	const subtitles = "Text\nFormat : UTF-8\nLanguage : Portuguese (Brazil)\n"
	for _, test := range []struct {
		name        string
		audioCount  int
		report      string
		proposedDub bool
		want        api.DupeRelation
	}{
		{"Brazilian dub added", 1, original, true, api.DupeRelationCoexists},
		{"Brazilian dub removed", 2, original + brazilian, false, api.DupeRelationSameSlot},
		{"Brazilian dub already present", 2, original + brazilian, true, api.DupeRelationSameSlot},
		{"unqualified Portuguese could already be the same dub", 2, original + portuguese, true, api.DupeRelationSameSlot},
		{"unqualified track could match the proposed technical variant", 3, original + ambiguousPortuguese, true, api.DupeRelationSameSlot},
		{"other language added", 1, original, false, api.DupeRelationSameSlot},
		{"extra subtitles", 1, original + subtitles, false, api.DupeRelationSameSlot},
		{"unchanged DD+ codec under a different label", 1, original, false, api.DupeRelationSameSlot},
		{"different codec", 1, strings.Replace(original, "E-AC-3", "AAC", 1), false, api.DupeRelationCoexists},
		{"TrueHD Atmos in a MediaInfo report", 1, strings.Replace(original, "E-AC-3", "MLP FBA 16-ch", 1), false, api.DupeRelationCoexists},
		{"DTS XLL in a MediaInfo report", 1, strings.Replace(original, "E-AC-3", "DTS XLL X", 1), false, api.DupeRelationCoexists},
		{"MPEG Audio Layer 2 in a MediaInfo report", 1, strings.Replace(original, "E-AC-3", "MPEG Audio\nFormat profile : Layer 2", 1), false, api.DupeRelationCoexists},
		{"MPEG Audio Layer 3 in a MediaInfo report", 1, strings.Replace(original, "E-AC-3", "MPEG Audio\nFormat profile : Layer 3", 1), false, api.DupeRelationCoexists},
		{"MPEG Audio without a known profile", 1, strings.Replace(original, "E-AC-3", "MPEG Audio", 1), false, api.DupeRelationSameSlot},
		{"different channels", 1, strings.Replace(original, "6 channels", "2 channels", 1), false, api.DupeRelationCoexists},
		{"matching original channels despite a different core", 1, strings.Replace(original, "6 channels", "2 channels\nChannel(s)_Original : 6 channels", 1), false, api.DupeRelationSameSlot},
		{"ambiguous channel count", 1, strings.Replace(original, "6 channels", "2 channels / 6 channels", 1), false, api.DupeRelationSameSlot},
		{"different codec in an upbrr MediaInfo report", -1, rendered, false, api.DupeRelationCoexists},
		{"numbered report has unknown final stream count", -1, "General\nVideo\nFormat : AVC\n" + original, true, api.DupeRelationSameSlot},
		{"truncated audio report", 2, original, true, api.DupeRelationSameSlot},
		{"missing audio report", 0, "", true, api.DupeRelationSameSlot},
		{"additional unrelated existing audio", 2, original + french, false, api.DupeRelationSameSlot},
	} {
		t.Run(test.name, func(t *testing.T) {
			media := api.MediaFacts{
				TrackCoverageComplete: true,
				Tracks: []api.MediaTrackFacts{{
					ResourceID:    "movie",
					Kind:          api.MediaTrackAudio,
					Role:          api.AudioRoleProgramme,
					Languages:     []string{"ja"},
					Codec:         "DD+",
					Channels:      6,
					ChannelsKnown: true,
				}},
				MediaFileFacts: api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{ResourceID: "movie"}}},
			}
			if test.proposedDub {
				media.Tracks = append(media.Tracks, api.MediaTrackFacts{
					ResourceID:    "movie",
					Kind:          api.MediaTrackAudio,
					Role:          api.AudioRoleProgramme,
					Languages:     []string{"pt"},
					Title:         "Portuguese (Brazil)",
					Codec:         "AAC",
					Channels:      2,
					ChannelsKnown: true,
				})
			}
			policy := duplicatePolicy()
			targetEvidence, _ := policy.TargetEvidence(api.UploadSubject{
				LanguageFacts:  mediafacts.ResolveLanguages(media),
				MediaFileFacts: media.MediaFileFacts,
			})
			hdr := api.HDRFacts{
				Status:  api.HDREvidenceComplete,
				Formats: []api.HDRFormat{api.HDRFormatSDR},
				Origin:  api.HDREvidenceMediaInfo,
			}
			target := api.TrackerDuplicateTarget{
				Evidence:   targetEvidence,
				Names:      []string{"Example.2026.1080p.WEB-DL.H264-GRP"},
				Category:   "MOVIE",
				Type:       "WEBDL",
				Source:     "WEB-DL",
				Resolution: "1080p",
				VideoCodec: "H.264",
				HDR:        hdr,
			}
			report := test.report
			if test.audioCount >= 0 {
				report = fmt.Sprintf("General\nCount of audio streams : %d\nVideo\nFormat : AVC\n%s", test.audioCount, report)
			}
			entry := api.DupeEntry{
				Name:          "Example.2026.1080p.WEB-DL.H264-OTHER",
				ID:            "123",
				CanonicalType: "WEBDL",
				Res:           "1080p",
				Codec:         "AVC",
				HDR:           hdr,
				MediaInfo:     report,
			}
			candidate := dupe.NormalizeCandidate(entry, "SAM")
			candidate.Evidence = policy.CandidateEvidence(entry)
			evaluation := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *policy, dupe.SearchEvidence{
				Complete: true, WorkScope: dupe.WorkScopeProviderID,
			})
			if len(evaluation.Candidates) != 1 || evaluation.Candidates[0].Relation != test.want {
				t.Fatalf("result = %+v, want %s", evaluation.Candidates, test.want)
			}
		})
	}
}

func TestSAMDoesNotCompareOneAudioReportForAnExistingDatedPack(t *testing.T) {
	policy := duplicatePolicy()
	target := api.TrackerDuplicateTarget{
		Names:      []string{"Example 2026 01 01 1080p WEB-DL H264-GRP"},
		Category:   "TV",
		Type:       "WEBDL",
		Source:     "WEB-DL",
		Date:       "2026-01-01",
		Resolution: "1080p",
		VideoCodec: "H.264",
		Evidence: api.DupeReleaseEvidence{ProgrammeAudio: []api.DupeAudioTrack{
			{Language: "ja", Codec: "aac"},
			{Language: "pt-BR", Codec: "aac"},
		}},
	}
	entry := api.DupeEntry{
		Name:          "Example.S01.2026.01.01.1080p.WEB-DL.H264-OTHER",
		ID:            "123",
		CanonicalType: "WEBDL",
		Res:           "1080p",
		Codec:         "AVC",
		MediaInfo:     "General\nVideo\nFormat : AVC\nAudio\nFormat : AAC\nLanguage : Japanese\n",
	}
	candidate := dupe.NormalizeCandidate(entry, "SAM")
	candidate.Evidence = policy.CandidateEvidence(entry)
	if !candidate.Pack || candidate.Date != target.Date || len(candidate.Evidence.ProgrammeAudio) != 1 {
		t.Fatalf("report did not produce dated pack evidence: %#v", candidate)
	}
	result := dupe.Evaluate(target, []dupe.TrackerCandidate{candidate}, *policy, dupe.SearchEvidence{
		Complete: true, WorkScope: dupe.WorkScopeProviderID,
	})
	if len(result.Candidates) != 1 || result.Candidates[0].Relation == api.DupeRelationCoexists {
		t.Fatalf("one audio report cleared an existing pack: %+v", result.Candidates)
	}
}

func TestSAMComparesKnownTechnicalDifferencesInAnUnqualifiedPortugueseProgramme(t *testing.T) {
	t.Parallel()
	policy := duplicatePolicy()
	tracks := []api.MediaTrackFacts{{
		ResourceID:    "movie",
		Kind:          api.MediaTrackAudio,
		Role:          api.AudioRoleProgramme,
		Codec:         "DD",
		Channels:      6,
		ChannelsKnown: true,
	}}
	const stereoAAC = "Audio #1\nFormat : AAC\nChannel(s) : 2 channels\nLanguage : Portuguese\n"
	const surroundAC3 = "Audio #2\nFormat : AC-3\nChannel(s) : 6 channels\nLanguage : Portuguese (Brazil)\n"
	const surroundJOC = "Audio #1\nFormat : E-AC-3 JOC\nChannel(s) : 6 channels\nLanguage : Portuguese\n"
	for _, test := range []struct {
		language string
		streams int
		audio   string
		want    api.DupeRelation
	}{
		{"pt", 1, stereoAAC, api.DupeRelationCoexists},
		{"pt-BR", 1, stereoAAC, api.DupeRelationCoexists},
		{"pt", 1, strings.Replace(stereoAAC, "Portuguese", "Portuguese (Brazil)", 1), api.DupeRelationCoexists},
		{"pt", 1, surroundJOC, api.DupeRelationCoexists},
		{"pt", 1, strings.NewReplacer("AAC", "AC-3", "2 channels", "6 channels").Replace(stereoAAC), ""},
		{"pt", 2, stereoAAC + surroundAC3, ""},
		{"pt-BR", 2, stereoAAC + surroundAC3, ""},
	} {
		tracks[0].Languages = []string{test.language}
		target, _ := policy.TargetEvidence(api.UploadSubject{
			LanguageFacts:  mediafacts.ResolveLanguages(api.MediaFacts{TrackCoverageComplete: true, Tracks: tracks}),
			MediaFileFacts: api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{{ResourceID: "movie"}}},
		})
		report := fmt.Sprintf("General\nCount of audio streams : %d\nVideo\nFormat : AVC\n%s", test.streams, test.audio)
		decision := policy.CompareEvidence(api.DupeEvidenceContext{
			Target:             api.TrackerDuplicateTarget{Evidence: target},
			Candidate:          api.TrackerDuplicateTarget{Evidence: policy.CandidateEvidence(api.DupeEntry{MediaInfo: report})},
			Relation:           api.DupeRelationSameSlot,
			SameWorkAndContent: true,
		})
		if decision.Relation != test.want {
			t.Errorf("target language %q against %q: decision = %+v, want relation %q", test.language, test.audio, decision, test.want)
		}
	}
}
