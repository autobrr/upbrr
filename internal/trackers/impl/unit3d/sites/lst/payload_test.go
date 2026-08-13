// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

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
