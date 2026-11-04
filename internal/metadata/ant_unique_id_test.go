// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"path/filepath"
	"slices"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ant"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTRequiresMediaInfoUniqueID(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(ant.New()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, general, extension, discType string
		want                               api.UniqueIDStatus
	}{
		{"absent", ``, ".mkv", "", api.UniqueIDStatusMissing},
		{"empty", `,"UniqueID":""`, ".mkv", "", api.UniqueIDStatusMissing},
		{"whitespace", `,"UniqueID":" \t\n"`, ".mkv", "", api.UniqueIDStatusMissing},
		{"alias empty", `,"UniqueID/String":""`, ".mkv", "", api.UniqueIDStatusMissing},
		{"alias whitespace", `,"UniqueID/String":" \t\n"`, ".mkv", "", api.UniqueIDStatusMissing},
		{"both blank", `,"UniqueID":" ","UniqueID/String":"\t"`, ".mkv", "", api.UniqueIDStatusMissing},
		{"ID present", `,"UniqueID":" 123 "`, ".mkv", "", api.UniqueIDStatusPresent},
		{"alias present", `,"UniqueID/String":" 123 (0x7B) "`, ".mkv", "", api.UniqueIDStatusPresent},
		{"blank ID with usable alias", `,"UniqueID":" ","UniqueID/String":"123"`, ".mkv", "", api.UniqueIDStatusPresent},
		{"non-MKV", ``, ".mp4", "", api.UniqueIDStatusNotApplicable},
		{"Blu-ray disc", ``, ".mkv", "BDMV", api.UniqueIDStatusNotApplicable},
		{"DVD disc", ``, ".mkv", "DVD", api.UniqueIDStatusNotApplicable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := preparationstate.State{SourcePath: filepath.Join(t.TempDir(), "Example"+tc.extension), DiscType: tc.discType}
			// A stream ID cannot stand in for the General-track container ID.
			doc := mustParseMediaInfoDoc(`{"media":{"track":[{"@type":"General"` + tc.general + `},{"@type":"Audio","UniqueID":"456"}]}}`)
			meta.MediaInfoUniqueID, meta.MediaInfoUniqueIDPresent = validateMediaInfoUniqueID(meta, doc)
			assessments := meta.ReleaseAssessments()
			if assessments.MediaInfoUniqueID != tc.want {
				t.Fatalf("assessment = %s, want %s", assessments.MediaInfoUniqueID, tc.want)
			}
			subject := api.TrackerValidationSubject{
				Identity:           api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
				ProviderMetadata:   api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{TMDBID: 123}},
				Assessments:        assessments,
				MediaInfoTextReady: true,
			}
			failures, err := trackers.EvaluateTrackerValidationWithRegistry(t.Context(), registry, "ANT", subject, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			missing := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == "require_unique_id" && f.Disposition == api.RuleDispositionStrict && trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, false)
			})
			if missing != (tc.want == api.UniqueIDStatusMissing) {
				t.Fatalf("unique-ID validation failures = %+v", failures)
			}
			if tc.want != api.UniqueIDStatusMissing && len(failures) != 0 {
				t.Fatalf("unexpected validation failures: %+v", failures)
			}
		})
	}
}
