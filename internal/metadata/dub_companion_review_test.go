// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDubCompanionCorrectionsProgressThroughRepreparation(t *testing.T) {
	for _, tc := range []struct {
		name, dubLanguage, companionLanguage string
		companionKnownLanguage              string
		companionOrigin                     api.TrackAudioOrigin
	}{
		{
			name:            "original companion",
			dubLanguage:     "German",
			companionOrigin: api.TrackAudioOriginal,
		},
		{name: "English dub already satisfies rule", dubLanguage: "English"},
		{
			name:              "English companion dub",
			dubLanguage:       "German",
			companionOrigin:   api.TrackAudioDubbed,
			companionLanguage: "English",
		},
		{
			name:                   "companion language identified before origin",
			dubLanguage:            "German",
			companionKnownLanguage: "Japanese",
			companionOrigin:        api.TrackAudioOriginal,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Movie.2026.1080p.WEB-DL.H264-GRP.mkv")
			fixture.input.Instructions.Category = new(api.CanonicalCategoryMovie)
			fixture.input.Instructions.Metadata.OriginalLanguage = new("")
			fixture.mediaInfo = mediainfo.NewService(nil, &programmeReportAnalyzer{report: `{"media":{"track":[
				{"@type":"General","UniqueID":"1234567890","Format":"Matroska"},
				{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080","FrameRate":"24","ScanType":"Progressive","BitDepth":"8"},
				{"@type":"Audio","ID":"1","StreamOrder":"1","Language":"und","Title":"Main","Format":"AAC","Channels":"2","Default":"Yes"},
				{"@type":"Audio","ID":"2","StreamOrder":"2","Language":"und","Title":"Main","Format":"AAC","Channels":"2","Default":"No"}
			]}}`})
			module, repo := fixture.open(t)
			input := fixture.input
			registry := impl.MustNewRegistry()
			selected := []api.TrackerID{"AZ", "PTP"}
			var generation api.PreparedGeneration
			prepare := func(metadata api.MetadataOverrides) api.UploadSubject {
				t.Helper()
				update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
				if generation != 0 {
					if err := repo.Close(); err != nil {
						t.Fatal(err)
					}
					module, repo = fixture.open(t)
					input = api.PrepareInput{SourcePath: input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
					update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: metadata}}}
				}
				result := prepareCorrectionEvidence(t, module, input, update)
				if result.Release.Generation <= generation {
					t.Fatal("source correction reused the old prepared generation")
				}
				generation = result.Release.Generation
				subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: input.SourcePath, Generation: generation}})
				if err != nil {
					t.Fatal(err)
				}
				return subject
			}
			check := func(subject api.UploadSubject, trackID string, correction api.CorrectionField) {
				t.Helper()
				readiness, err := trackers.EvaluateInputReadiness(registry, selected, subject)
				if err != nil {
					t.Fatal(err)
				}
				fields := slices.DeleteFunc(readiness.Fields, func(field api.InputReadinessFieldOutcome) bool {
					return field.CorrectionField == nil || (*field.CorrectionField != api.CorrectionFieldMetadataTrackLanguages && *field.CorrectionField != api.CorrectionFieldMetadataTrackAudioOrigins)
				})
				switch {
				case trackID == "":
					if len(fields) != 0 {
						t.Fatalf("satisfied rule still interrogated unrelated tracks: %#v", fields)
					}
				case len(fields) != 1 || fields[0].TrackID != trackID || *fields[0].CorrectionField != correction || !slices.Equal(fields[0].TrackerIDs, selected):
					t.Fatalf("next offered correction must resolve this stage: want %s/%s, got %#v", trackID, correction, fields)
				case correction == api.CorrectionFieldMetadataTrackAudioOrigins && (!strings.Contains(fields[0].Message, "original") || !strings.Contains(fields[0].Message, "English")):
					t.Fatalf("companion review lacks the actual rule's alternatives: %s", fields[0].Message)
				}
				for _, tracker := range selected {
					descriptor, _ := registry.LookupDescriptor(string(tracker))
					failures := descriptor.Validation.LanguageAssessment(api.NewTrackerValidationSubject(subject, string(tracker)))
					if slices.ContainsFunc(failures, api.IsStrictRuleFailure) != (trackID != "") {
						t.Fatalf("%s language eligibility disagrees with staged review: %#v", tracker, failures)
					}
				}
				if subject.LanguageFacts.OriginalLanguagesKnown || len(subject.LanguageFacts.OriginalLanguages) != 0 {
					t.Fatal("source corrections invented title-language metadata")
				}
			}
			subject := prepare(api.MetadataOverrides{})
			check(subject, "", "")
			audio := slices.DeleteFunc(slices.Clone(subject.LanguageFacts.Tracks), func(track api.MediaTrackFacts) bool { return track.Kind != api.MediaTrackAudio })
			if len(audio) != 2 {
				t.Fatalf("inspected programme tracks: %#v", audio)
			}
			dub, companion := audio[0], audio[1]
			subject = prepare(api.MetadataOverrides{TrackAudioOrigins: []api.TrackAudioOriginCorrection{{
				TrackID:             dub.ID,
				Origin:              api.TrackAudioDubbed,
				ManifestFingerprint: dub.ManifestFingerprint,
			}}})
			check(subject, dub.ID, api.CorrectionFieldMetadataTrackLanguages)
			subject = prepare(api.MetadataOverrides{TrackLanguages: []api.TrackLanguageCorrection{{
				TrackID:             dub.ID,
				Languages:           []string{tc.dubLanguage},
				ManifestFingerprint: dub.ManifestFingerprint,
			}}})
			if tc.dubLanguage == "English" {
				check(subject, "", "")
				if subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial {
					t.Fatal("unrelated unidentified companion was filled in")
				}
				return
			}
			check(subject, companion.ID, api.CorrectionFieldMetadataTrackAudioOrigins)
			if tc.companionKnownLanguage != "" {
				subject = prepare(api.MetadataOverrides{TrackLanguages: []api.TrackLanguageCorrection{{
					TrackID: companion.ID,
					Languages: []string{tc.companionKnownLanguage},
					ManifestFingerprint: companion.ManifestFingerprint,
				}}})
				check(subject, companion.ID, api.CorrectionFieldMetadataTrackAudioOrigins)
			}
			subject = prepare(api.MetadataOverrides{TrackAudioOrigins: []api.TrackAudioOriginCorrection{{
				TrackID:             companion.ID,
				Origin:              tc.companionOrigin,
				ManifestFingerprint: companion.ManifestFingerprint,
			}}})
			if tc.companionOrigin == api.TrackAudioOriginal {
				check(subject, "", "")
				if tc.companionKnownLanguage == "" && subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusPartial {
					t.Fatal("original-soundtrack evidence was replaced with an invented language")
				}
				return
			}
			check(subject, companion.ID, api.CorrectionFieldMetadataTrackLanguages)
			subject = prepare(api.MetadataOverrides{TrackLanguages: []api.TrackLanguageCorrection{{
				TrackID:             companion.ID,
				Languages:           []string{tc.companionLanguage},
				ManifestFingerprint: companion.ManifestFingerprint,
			}}})
			check(subject, "", "")
		})
	}
}
