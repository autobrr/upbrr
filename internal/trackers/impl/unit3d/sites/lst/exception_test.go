// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func exceptionTestSubject(t *testing.T, container, source, encoder string) api.TrackerValidationSubject {
	t.Helper()
	subject := api.TrackerValidationSubject{
		Tracker:      "LST",
		Container:    container,
		Source:       source,
		Type:         "WEBDL",
		VideoEncoder: encoder,
		SourcePath:   filepath.Join(t.TempDir(), "Example"),
		Identity: api.ExternalIdentity{
			Generation: 1,
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     123,
		},
		HDRFacts: api.HDRFacts{
			Status:             api.HDREvidenceComplete,
			DolbyVisionProfile: "5",
			Formats:            []api.HDRFormat{api.HDRFormatDolbyVision},
		},
		QuestionnaireAnswers: make(map[string]string),
	}
	subject.Identity.SourcePath = subject.SourcePath
	fingerprint, err := api.TitleSearchIdentityFingerprint(subject.Identity)
	if err != nil {
		t.Fatal(err)
	}
	subject.TitleSearchEvidence = api.TrackerTitleSearchEvidence{
		Status:            api.MetadataEvidenceStatusComplete,
		TitleFingerprint:  fingerprint,
		ResultFingerprint: "complete",
	}
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject, "exception_note")] = "Checked the source and applicable exception."
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject, "sole_source")] = "sole_source_verified"
	return subject
}

func TestContainerExceptionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, container, source, encoder string
		mutate                           func(*api.TrackerValidationSubject)
		rule                             string
	}{
		{name: "ordinary MKV", container: "mkv"},
		{
			name:      "broadcast TS",
			container: "ts",
			source:    "HDTV",
		},
		{
			name:      "UHD broadcast TS",
			container: "ts",
			source:    "UHDTV",
		},
		{
			name:      "SD broadcast TS",
			container: "ts",
			source:    "SDTV",
		},
		{
			name:      "profile 5 MP4",
			container: "mp4",
			source:    "WEB-DL",
		},
		{
			name:      "legacy AVI",
			container: "avi",
			encoder:   "XviD",
		},
		{
			name:      "legacy MKV",
			container: "mkv",
			encoder:   "DivX",
		},
		{
			name:      "unsupported AVI",
			container: "avi",
			encoder:   "x264",
			rule:      "lst_container",
		},
		{
			name:      "unsupported TS",
			container: "ts",
			source:    "Blu-ray",
			rule:      "lst_container",
		},
		{
			name:      "unsupported MP4 profile",
			container: "mp4",
			mutate:    func(s *api.TrackerValidationSubject) { s.HDRFacts.DolbyVisionProfile = "8.1" },
			rule:      "lst_container",
		},
		{
			name:      "unknown MP4 profile",
			container: "mp4",
			mutate:    func(s *api.TrackerValidationSubject) { s.HDRFacts.Status = api.HDREvidenceMissing },
			rule:      "lst_container_evidence",
		},
		{name: "unknown container", rule: "lst_container_evidence"},
		{
			name:      "missing explanation",
			container: "ts",
			source:    "HDTV",
			mutate:    func(s *api.TrackerValidationSubject) { s.QuestionnaireAnswers = nil },
			rule:      "lst_exception_note",
		},
		{
			name:      "blank explanation",
			container: "ts",
			source:    "HDTV",
			mutate: func(s *api.TrackerValidationSubject) {
				s.QuestionnaireAnswers[exceptionQuestionKey(*s, "exception_note")] = " \n\t "
			},
			rule: "lst_exception_note",
		},
		{
			name:      "technical search insufficient",
			container: "avi",
			encoder:   "XviD",
			mutate: func(s *api.TrackerValidationSubject) {
				s.TitleSearchEvidence.Status = api.MetadataEvidenceStatusPartial
			},
			rule: "lst_legacy_source_evidence",
		},
		{
			name:      "other version blocks",
			container: "mkv",
			encoder:   "XviD",
			mutate: func(s *api.TrackerValidationSubject) {
				s.QuestionnaireAnswers[exceptionQuestionKey(*s, "sole_source")] = "other_version_exists"
			},
			rule: "lst_legacy_source",
		},
		{
			name:      "manual source evidence missing",
			container: "avi",
			encoder:   "XviD",
			mutate: func(s *api.TrackerValidationSubject) {
				delete(s.QuestionnaireAnswers, exceptionQuestionKey(*s, "sole_source"))
			},
			rule: "lst_legacy_source_evidence",
		},
		{
			name:      "disc exemption",
			container: "iso",
			mutate:    func(s *api.TrackerValidationSubject) { s.DiscType = "BDMV" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := exceptionTestSubject(t, tc.container, tc.source, tc.encoder)
			if tc.mutate != nil {
				tc.mutate(&subject)
			}
			failures := exceptionValidationFailures(subject)
			if tc.rule == "" {
				if len(failures) != 0 {
					t.Fatalf("failures = %#v", failures)
				}
				return
			}
			if len(failures) != 1 || failures[0].Rule != tc.rule || failures[0].Disposition != api.RuleDispositionStrict {
				t.Fatalf("failures = %#v", failures)
			}
		})
	}
}

func TestExceptionAnswersPersistOnlyForCurrentSource(t *testing.T) {
	subject := exceptionTestSubject(t, "ts", "HDTV", "")
	data, err := json.Marshal(subject.QuestionnaireAnswers)
	if err != nil {
		t.Fatal(err)
	}
	subject.QuestionnaireAnswers = nil
	if err := json.Unmarshal(data, &subject.QuestionnaireAnswers); err != nil {
		t.Fatal(err)
	}
	fields := exceptionQuestionnaireFields(subject)
	if len(fields) != 1 || !fields[0].Required || fields[0].Kind != "text" || fields[0].Value == "" {
		t.Fatalf("fields = %#v", fields)
	}
	for _, mutate := range []func(*api.TrackerValidationSubject){
		func(s *api.TrackerValidationSubject) { s.Identity.Generation++ },
		func(s *api.TrackerValidationSubject) { s.SourcePath += "-changed" },
		func(s *api.TrackerValidationSubject) { s.Source = "SDTV" },
	} {
		changed := subject
		mutate(&changed)
		if fields := exceptionQuestionnaireFields(changed); len(fields) != 1 || fields[0].Value != "" {
			t.Fatalf("stale answer survived: %#v", fields)
		}
	}
	subject.Container = "mkv"
	if fields := exceptionQuestionnaireFields(subject); len(fields) != 0 {
		t.Fatalf("removed exception retained fields: %#v", fields)
	}
}

func TestExceptionPreviewAndSubmissionPreserveOverride(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, modQ := range []bool{false, true} {
		meta := api.UploadSubject{
			Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
			Release:       api.ReleaseInfo{Resolution: "1080p"},
			ReleaseName:   "Example 2026 1080p HDTV-GRP",
			Type:          "HDTV",
			Source:        "HDTV",
			Container:     "ts",
			Assessments:   api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
			LanguageFacts: lstTestLanguageFacts("English", []string{"English"}, []string{"English"}),
		}
		meta.SourcePath = filepath.Join(t.TempDir(), "Example.ts")
		meta.TorrentPath = filepath.Join(t.TempDir(), "Example.torrent")
		meta.MediaInfoTextPath = filepath.Join(t.TempDir(), "mediainfo.txt")
		for _, path := range []string{meta.TorrentPath, meta.MediaInfoTextPath} {
			if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		subject := api.NewTrackerValidationSubject(meta, "LST")
		note := "This transport stream is the original broadcast capture."
		meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"LST": {exceptionQuestionKey(subject, "exception_note"): note}}
		screens := lstDescriptionScreenshots(&meta)
		input := trackers.PreparationInput{
			Intent:  trackers.PreparationIntentUpload,
			Tracker: "LST",
			Meta:    meta,
			TrackerConfig: config.TrackerConfig{
				APIKey: "test-key",
				ModQ:   modQ,
				Draft:  true,
			},
			Assets: &trackers.DescriptionAssets{Description: "My final override\n" + screens, Final: true},
		}
		plan, failure := unit3d.NewWithProfile(Profile()).Prepare(t.Context(), input)
		if failure != nil {
			t.Fatal(failure)
		}
		preview := plan.DryRun()
		if preview.Payload["mod_queue_opt_in"] != "1" || preview.Payload["draft_queue_opt_in"] != "1" || preview.Description != preview.Payload["description"] || !strings.Contains(preview.Description, note) || !strings.Contains(preview.Description, "My final override") {
			t.Fatalf("preview = %#v", preview)
		}
		called := false
		http.DefaultTransport = editionRoundTripper(func(req *http.Request) (*http.Response, error) {
			called = true
			if err := req.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := req.MultipartForm.RemoveAll(); err != nil {
					t.Errorf("clean multipart form: %v", err)
				}
			}()
			if req.FormValue("mod_queue_opt_in") != "1" || req.FormValue("description") != preview.Description {
				t.Fatal("serialized payload differs from preview")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":""}`)),
			}, nil
		})
		if _, err := plan.Submit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("submission not observed")
		}
		delete(input.Meta.TrackerQuestionnaireAnswers["LST"], exceptionQuestionKey(subject, "exception_note"))
		if _, failure := unit3d.NewWithProfile(Profile()).Prepare(t.Context(), input); failure == nil {
			t.Fatal("missing explanation accepted")
		}
	}
}

func TestExceptionDescriptionEscapesMarkup(t *testing.T) {
	got := exceptionDescriptionAppendix("  [url=bad]explanation[/url]<script>  ")
	if strings.Contains(got, "[url") || strings.Contains(got, "<script>") || !strings.Contains(got, "explanation") {
		t.Fatalf("appendix = %q", got)
	}
	if got := exceptionDescriptionAppendix(" "); got != "" {
		t.Fatalf("empty appendix = %q", got)
	}
}

func TestLSTContainerPackEvidence(t *testing.T) {
	subject := exceptionTestSubject(t, "mkv", "WEB-DL", "x264")
	subject.MediaFileFacts = api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{{Container: "mkv", Primary: true}, {Container: "avi", VideoEncoder: "x264"}}}
	failures := exceptionValidationFailures(subject)
	if len(failures) != 1 || failures[0].Rule != "lst_resource_container" || failures[0].EvidenceStatus != api.MetadataEvidenceStatusComplete {
		t.Fatalf("unsupported resource = %#v", failures)
	}
	subject.MediaFileFacts.Files[1] = api.MediaFileFact{Container: "mp4", Source: "WEB-DL"}
	failures = exceptionValidationFailures(subject)
	if len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("borrowed primary DV proof = %#v", failures)
	}
	subject.MediaFileFacts.Files[1].HDRFacts = subject.HDRFacts
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject, "exception_note")] = "Profile 5 source evidence reviewed."
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("per-resource profile 5 = %#v", failures)
	}
	subject.MediaFileFacts.Files[1] = api.MediaFileFact{Container: "mkv", VideoEncoder: "DivX"}
	if !legacyContainerCandidate(subject) || containerException(subject) != "legacy" {
		t.Fatal("secondary legacy source not identified")
	}
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject, "sole_source")] = "sole_source_verified"
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject, "exception_note")] = "Sole source evidence reviewed."
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("reviewed legacy resource = %#v", failures)
	}
	subject.MediaFileFacts.Files = subject.MediaFileFacts.Files[:1]
	failures = exceptionValidationFailures(subject)
	if len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
		t.Fatalf("incomplete pack = %#v", failures)
	}
}

func TestLSTLegacySearchChangesInvalidateReview(t *testing.T) {
	subject := exceptionTestSubject(t, "avi", "DVD", "XviD")
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("initial evidence = %#v", failures)
	}
	subject.TitleSearchEvidence.TorrentCount = 1
	subject.TitleSearchEvidence.ResultFingerprint = "changed-results"
	failures := exceptionValidationFailures(subject)
	if len(failures) != 1 || failures[0].Rule != "lst_legacy_source_evidence" {
		t.Fatalf("changed search retained review = %#v", failures)
	}
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject, "sole_source")] = "sole_source_verified"
	if failures := exceptionValidationFailures(subject); len(failures) != 0 {
		t.Fatalf("same-source mirror after review = %#v", failures)
	}
}
