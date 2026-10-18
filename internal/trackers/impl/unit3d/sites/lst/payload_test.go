// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
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

type editionRoundTripper func(*http.Request) (*http.Response, error)

func (f editionRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestLSTEditionUploadPreservesNameAndOmitsUnmappedID(t *testing.T) {
	// Submission uses this in-process transport; no request reaches LST.
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, test := range []struct {
		name, cut, edition, presentation, wantID, requestedName string
	}{
		{
			name:   "supported cut",
			cut:    "Director's Cut",
			wantID: "2",
		},
		{
			name:    "supported edition",
			edition: "Collector's Edition",
			wantID:  "1",
		},
		{
			name:    "explicit accepted Other",
			edition: "Other",
			wantID:  "0",
		},
		{name: "unsupported edition", edition: "Anniversary Edition"},
		{name: "empty edition"},
		{
			name:    "supported compound",
			cut:     "Extended",
			edition: "Uncut",
			wantID:  "4",
		},
		{
			name:    "unsupported compound",
			cut:     "Director's Cut",
			edition: "Collector's Edition",
		},
		{
			name:         "unsupported presentation compound",
			cut:          "Director's Cut",
			presentation: "Open Matte",
		},
		{
			name:          "requested name",
			edition:       "Anniversary Edition",
			requestedName: "Custom Anniversary Edition Release-GRP",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := api.UploadSubject{
				Cut:             test.cut,
				Edition:         test.edition,
				Presentation:    test.presentation,
				ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid},
				Identity:        api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
				Release:         api.ReleaseInfo{Resolution: "1080p"},
				LanguageFacts:   lstTestLanguageFacts("English", []string{"English"}, []string{"English"}),
				Type:            "WEBDL",
				Source:          "WEB-DL",
				Container:       "mkv",
				Assessments:     api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
			}
			meta.ReleaseName = "Example Release 2026 " + strings.TrimSpace(meta.EditionLabel()+" Hybrid") + " 1080p WEB-DL-GRP"
			meta.SourcePath = filepath.Join(t.TempDir(), "Example.mkv")
			meta.TorrentPath = filepath.Join(t.TempDir(), "Example.torrent")
			meta.MediaInfoTextPath = filepath.Join(t.TempDir(), "mediainfo.txt")
			for _, path := range []string{meta.TorrentPath, meta.MediaInfoTextPath} {
				if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			input := trackers.PreparationInput{
				Intent:        trackers.PreparationIntentUpload,
				Tracker:       "LST",
				TrackerConfig: config.TrackerConfig{APIKey: "test-key"},
				Meta:          meta,
				Assets:        &trackers.DescriptionAssets{Description: "description", Final: true},
			}
			wantName := meta.ReleaseName
			if test.requestedName != "" {
				input.RequestedUploadName = &test.requestedName
				wantName = test.requestedName
			}
			plan, failure := unit3d.NewWithProfile(Profile()).Prepare(t.Context(), input)
			if failure != nil {
				t.Fatal(failure)
			}
			preview := plan.DryRun()
			if preview.ReleaseName != wantName || preview.Payload["name"] != wantName {
				t.Fatalf("preview name = %q/%q, want %q", preview.ReleaseName, preview.Payload["name"], wantName)
			}
			if got, present := preview.Payload["edition_id"]; got != test.wantID || present != (test.wantID != "") {
				t.Fatalf("preview edition_id = %q, present=%t, want %q", got, present, test.wantID)
			}
			called := false
			http.DefaultTransport = editionRoundTripper(func(req *http.Request) (*http.Response, error) {
				called = true
				if err := req.ParseMultipartForm(1 << 20); err != nil {
					t.Fatal(err)
				}
				if err := req.MultipartForm.RemoveAll(); err != nil {
					t.Fatal(err)
				}
				values, present := req.MultipartForm.Value["edition_id"]
				if present != (test.wantID != "") || present && (len(values) != 1 || values[0] != test.wantID) {
					t.Fatalf("serialized edition_id = %v, present=%t, want %q", values, present, test.wantID)
				}
				if got := req.FormValue("name"); got != wantName {
					t.Fatalf("serialized name = %q, want %q", got, wantName)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			})
			if _, err := plan.Submit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("serialized upload was not submitted to the test transport")
			}
			if input.Meta.Cut != test.cut || input.Meta.Edition != test.edition || input.Meta.Presentation != test.presentation ||
				len(input.Meta.ReleaseFeatures) != 1 || input.Meta.ReleaseFeatures[0] != api.ReleaseFeatureHybrid {
				t.Fatal("LST preparation changed canonical edition or feature facts")
			}
		})
	}
}

func TestAdditionalPayloadAddsLSTAPIFields(t *testing.T) {
	t.Parallel()

	data := make(map[string]string)
	additionalPayload(trackers.PreparationInput{
		TrackerConfig: config.TrackerConfig{Draft: true},
		Meta: api.UploadSubject{
			Edition: "Director's Cut",
			Service: "AND",
			Audio:   "Dual-Audio DD+ 5.1",
			HDRFacts: api.HDRFacts{
				Formats:            []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatHDR10Plus},
				DolbyVisionProfile: "8.1",
				Status:             api.HDREvidenceComplete,
			},
		},
	}, data)

	for key, want := range map[string]string{
		"draft_queue_opt_in": "1",
		"edition_id":         "2",
		"hdr_dv":             "DV P8 HDR10+",
		//nolint:misspell // ADN is LST's exact provider code.
		"provider":   "ADN",
		"dual_audio": "1",
	} {
		if got := data[key]; got != want {
			t.Fatalf("%s = %q, want %q; payload=%#v", key, got, want, data)
		}
	}
}

func TestAdditionalPayloadPreservesUnknownHDRAndDualAudioEvidence(t *testing.T) {
	t.Parallel()

	dualAudio := false
	data := make(map[string]string)
	additionalPayload(trackers.PreparationInput{Meta: api.UploadSubject{
		Service: "HSTR",
		HDRFacts: api.HDRFacts{
			Formats: []api.HDRFormat{api.HDRFormatWCG},
			Status:  api.HDREvidenceComplete,
		},
		ReleaseNameOverrides: api.ReleaseNameOverrides{DualAudio: &dualAudio},
	}}, data)
	if _, ok := data["hdr_dv"]; ok {
		t.Fatalf("unsupported HDR projected: %#v", data)
	}
	if data["provider"] != "HTSR" || data["dual_audio"] != "0" {
		t.Fatalf("payload = %#v", data)
	}
}

func TestAdditionalPayloadMarksCompleteSDR(t *testing.T) {
	t.Parallel()

	data := make(map[string]string)
	additionalPayload(trackers.PreparationInput{Meta: api.UploadSubject{HDRFacts: api.HDRFacts{
		Formats: []api.HDRFormat{api.HDRFormatSDR},
		Status:  api.HDREvidenceComplete,
	}}}, data)
	if value, ok := data["hdr_dv"]; !ok || value != "" {
		t.Fatalf("SDR hdr_dv = %q, %t; payload=%#v", value, ok, data)
	}
}

func TestLSTPayloadPreservesCanonicalCut(t *testing.T) {
	t.Parallel()
	for cut, want := range map[string]string{
		"Director's Cut": "2",
		"Extended Cut":   "3",
		"Theatrical Cut": "8",
		"Uncut":          "9",
		"Unrated Cut":    "10",
	} {
		t.Run(cut, func(t *testing.T) {
			data := make(map[string]string)
			additionalPayload(trackers.PreparationInput{Meta: api.UploadSubject{Cut: cut}}, data)
			if data["edition_id"] != want {
				t.Fatalf("canonical cut payload = %#v, want %s", data, want)
			}
		})
	}
}

func TestHybridDoesNotEnterEditionMapping(t *testing.T) {
	for _, cut := range []string{"", "Director's Cut"} {
		subject := api.UploadSubject{Cut: cut, ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid}}
		data := make(map[string]string)
		additionalPayload(trackers.PreparationInput{Meta: subject}, data)
		want := ""
		if cut != "" {
			want = "2"
		}
		if data["edition_id"] != want {
			t.Fatalf("edition_id=%q want %q", data["edition_id"], want)
		}
		failures, err := validationPolicy().Check(t.Context(), api.NewTrackerValidationSubject(subject, "LST"), api.NopLogger{})
		if err != nil {
			t.Fatal(err)
		}
		for _, failure := range failures {
			if failure.Rule == "unsupported_edition" {
				t.Fatal("Hybrid rejected as edition")
			}
		}
	}
}
