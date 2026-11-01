// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPTPFullDVDValidation(t *testing.T) {
	registry := MustNewRegistry()
	for _, test := range []struct {
		name     string
		tracker  string
		discType string
		typeName string
		source   string
		size     int64
		blocked  bool
	}{
		{"PAL DVD5", "PTP", "DVD", "DISC", "PAL DVD", 4 << 30, true},
		{"PAL DVD9", "PTP", "DVD", "DISC", "PAL DVD", 7 << 30, true},
		{"NTSC DVD5", "PTP", "DVD", "DISC", "NTSC DVD", 4 << 30, true},
		{"NTSC DVD9", "PTP", "DVD", "DISC", "NTSC DVD", 7 << 30, true},
		{"DVD disc without disc label", "PTP", "", "DISC", "DVD", 4 << 30, true},
		{"PAL disc without disc label", "PTP", "", "DISC", "PAL DVD", 4 << 30, true},
		{"NTSC disc without disc label", "PTP", "", "DISC", "NTSC", 7 << 30, true},
		{"DVD encode", "PTP", "", "DVDRIP", "DVD", 1 << 30, false},
		{"DVD remux", "PTP", "", "REMUX", "DVD", 4 << 30, false},
		{"DVD remux retaining disc label", "PTP", "DVD", "REMUX", "DVD", 4 << 30, false},
		{"Blu-ray disc", "PTP", "BDMV", "DISC", "BluRay", 20 << 30, false},
		{"HD DVD disc without disc label", "PTP", "", "DISC", "HD DVD", 20 << 30, false},
		{"other tracker DVD", "AITHER", "DVD", "DISC", "PAL DVD", 4 << 30, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptor, ok := registry.LookupDescriptor(test.tracker)
			if !ok {
				t.Fatal("missing tracker descriptor")
			}
			meta := api.UploadSubject{
				DiscType:   test.discType,
				Type:       test.typeName,
				Source:     test.source,
				SourceSize: test.size,
				Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				Release:    api.ReleaseInfo{Resolution: "1080p"},
				Container:  "mkv",
				VideoCodec: "H.264",
				LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
					OriginalLanguage:      "English",
					TrackCoverageComplete: true,
					PrimaryAudioTrackID:   "audio-0",
					Tracks: []api.MediaTrackFacts{{
						ID:           "audio-0",
						Kind:         api.MediaTrackAudio,
						Role:         api.AudioRoleProgramme,
						Languages:    []string{"English"},
						Default:      true,
						DefaultKnown: true,
					}},
				}),
			}
			failures, err := descriptor.Validation.Check(t.Context(), api.NewTrackerValidationSubject(meta, test.tracker), api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(failures, func(failure api.RuleFailure) bool { return failure.Rule == "unsupported_disc" })
			if (index >= 0) != test.blocked {
				t.Fatalf("full-DVD rejection = %t, want %t: %+v", index >= 0, test.blocked, failures)
			}
			if test.blocked {
				if failures[index].Disposition != api.RuleDispositionStrict || !strings.Contains(failures[index].Reason, "MediaInfo version") {
					t.Fatalf("missing strict unsupported-version reason: %+v", failures[index])
				}
				for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
					err := standalone.ValidatePreparation(t.Context(), trackers.PreparationInput{
						Tracker:       test.tracker,
						Meta:          meta,
						ExecutionMode: mode,
					}, descriptor.Validation)
					if err == nil || !strings.Contains(err.Error(), "unsupported_disc") {
						t.Fatalf("direct preparation bypassed full-DVD restriction in %s: %v", mode, err)
					}
				}
			} else if test.tracker == "PTP" {
				if blocker, err := trackers.FirstBlockingRuleFailure(test.tracker, failures, api.WorkflowExecutionModeNormal, nil); err != nil || blocker != nil {
					t.Fatalf("supported PTP input was blocked: %+v, %v", blocker, err)
				}
			}
		})
	}
}
