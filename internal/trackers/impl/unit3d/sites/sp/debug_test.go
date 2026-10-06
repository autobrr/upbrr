// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sp

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSPPackDebugProjectionAndLatePreparation(t *testing.T) {
	definition := unit3d.NewWithProfile(Profile())
	registry := trackers.NewRegistry()
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	projector, err := trackers.NewWorkflowProjector(registry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	report, torrent := filepath.Join(dir, "MediaInfo.txt"), filepath.Join(dir, "example.torrent")
	for _, path := range []string{report, torrent} {
		if err := os.WriteFile(path, []byte("Synthetic prepared content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name       string
		mutate     func(*api.UploadSubject)
		debugReady bool
	}{
		{name: "unresolved source", debugReady: true},
		{
			name: "language variance",
			mutate: func(meta *api.UploadSubject) {
				meta.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
				answerPackQuestion(meta, packSourceKey, "consistent")
			},
			debugReady: true,
		},
		{
			name: "subtitle variance",
			mutate: func(meta *api.UploadSubject) {
				meta.MediaFileFacts.Files[1].SubtitleLanguages = []string{"English"}
				answerPackQuestion(meta, packSourceKey, "consistent")
			},
			debugReady: true,
		},
		{
			name: "failed probe",
			mutate: func(meta *api.UploadSubject) {
				meta.MediaFileFacts.Files[1] = api.MediaFileFact{FileName: meta.FileList[1]}
			},
			debugReady: true,
		},
		{name: "known technical variance", mutate: func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].Resolution = "720p" }},
		{name: "missing language with technical variance", mutate: func(meta *api.UploadSubject) {
			meta.MediaFileFacts.Files[1].Resolution = "720p"
			meta.MediaFileFacts.Files[1].AudioStatus = api.MetadataEvidenceStatusPartial
		}},
		{name: "independent required settings", mutate: func(meta *api.UploadSubject) {
			meta.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusMissing
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := collectedPackSubject()
			meta.Scene, meta.Source, meta.Container = true, "WEB", "mkv"
			meta.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusNotApplicable
			meta.VideoCodec, meta.VideoEncode, meta.Tag = "AVC", "H.264", "-GRP"
			meta.ReleaseName = "Example.Show.S01.1080p.WEB-DL.H.264-GRP"
			meta.Release = api.ReleaseInfo{
				Title:      "Example Show",
				Year:       2026,
				Resolution: "1080p",
			}
			meta.Identity.TMDBID = 1234567
			meta.ProviderMetadata = api.SourceScopedMetadata{
				SourcePath: meta.SourcePath,
				Generation: meta.Identity.Generation,
				TMDB:       &api.TMDBMetadata{TMDBID: 1234567, Genres: "Drama"},
			}
			meta.MediaInfoTextPath, meta.TorrentPath = report, torrent
			if test.mutate != nil {
				test.mutate(&meta)
			}
			for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
				wantReady := mode == api.WorkflowExecutionModeDebug && test.debugReady
				_, _, _, projections, err := projector.Build(t.Context(), api.ReleaseSnapshot{}, meta, []api.TrackerID{"SP"}, nil, nil, mode)
				if err != nil {
					t.Fatal(err)
				}
				projection := projections.Projections[0]
				if got := projection.Readiness == api.ReadinessStatusReady && projection.DupeReady; got != wantReady {
					t.Fatalf("mode=%s: projection readiness=%s wantReady=%t decisions=%#v", mode, projection.Readiness, wantReady, projection.PolicyDecisions)
				}
				if wantReady && (len(projection.Questionnaire) != 0 || !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
					return decision.Code == "sp_pack_uniformity" && decision.Decision == "bypassed" && !decision.Blocking
				})) {
					t.Fatalf("debug lost its bypass notice or retained required pack questions: %#v", projection)
				}
				plan, failure := definition.Prepare(t.Context(), trackers.PreparationInput{
					Intent:              trackers.PreparationIntentDryRun,
					Tracker:             "SP",
					Logger:              api.NopLogger{},
					ExecutionMode:       mode,
					RequestedUploadName: &meta.ReleaseName,
					TrackerConfig:       config.TrackerConfig{APIKey: "test-key"},
					Assets:              &trackers.DescriptionAssets{Description: "Synthetic description", Final: true},
					Meta:                meta,
				})
				if (failure == nil) != wantReady {
					t.Fatalf("mode=%s: late preparation failure=%v wantReady=%t", mode, failure, wantReady)
				}
				if failure == nil {
					if _, err := plan.Submit(t.Context()); !errors.Is(err, trackers.ErrPlanNotSubmittable) {
						t.Fatalf("debug preview allowed submission: %v", err)
					}
					if err := plan.Release(); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestSPDebugLanguageBypassPreservesCompletePackFailure(t *testing.T) {
	subject := spPassingSubject()
	subject.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
	subject.PackageFacts.DetectedEpisodes[0].Episodes = []int{1, 3}
	failures, err := ValidationPolicy().Check(t.Context(), subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	blocking, err := trackers.FirstBlockingRuleFailure("SP", failures, api.WorkflowExecutionModeDebug, nil)
	if err != nil || blocking == nil || blocking.Rule != "sp_pack_completeness" {
		t.Fatalf("debug language bypass hid independent pack completeness: blocking=%#v err=%v", blocking, err)
	}
	projection := api.TrackerReleaseProjection{
		TrackerID:   "SP",
		Readiness:   api.ReadinessStatusReady,
		DupeReady:   true,
		UploadReady: true,
	}
	if err := trackers.ApplyProjectionRuleFailures(&projection, failures, api.WorkflowExecutionModeDebug, "", api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	if projection.Readiness != api.ReadinessStatusIneligible || projection.DupeReady {
		t.Fatalf("debug projection waived independent pack completeness: %#v", projection)
	}
	if !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
		return decision.Code == "sp_pack_uniformity" && decision.Decision == "bypassed" && !decision.Blocking
	}) {
		t.Fatalf("language-only debug finding lost its bypass notice: %#v", projection.PolicyDecisions)
	}
}
