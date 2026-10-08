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

func TestSPPackGuidanceProjectionAndLatePreparation(t *testing.T) {
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
		name           string
		mutate         func(*api.UploadSubject)
		blockedRule    string
		projectionOnly bool
	}{
		{name: "unresolved source"},
		{name: "no per-file inspection", mutate: func(meta *api.UploadSubject) { meta.MediaFileFacts = api.MediaFileFacts{} }},
		{
			name: "language variance",
			mutate: func(meta *api.UploadSubject) {
				meta.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
			},
		},
		{
			name: "subtitle variance",
			mutate: func(meta *api.UploadSubject) {
				meta.MediaFileFacts.Files[1].SubtitleLanguages = []string{"English"}
			},
		},
		{
			name: "failed probe",
			mutate: func(meta *api.UploadSubject) {
				meta.MediaFileFacts.Files[1] = api.MediaFileFact{FileName: meta.FileList[1]}
			},
		},
		{name: "known technical variance", mutate: func(meta *api.UploadSubject) { meta.MediaFileFacts.Files[1].Resolution = "720p" }},
		{name: "missing language with technical variance", mutate: func(meta *api.UploadSubject) {
			meta.MediaFileFacts.Files[1].Resolution = "720p"
			meta.MediaFileFacts.Files[1].AudioStatus = api.MetadataEvidenceStatusPartial
		}},
		{
			name: "independent required settings",
			mutate: func(meta *api.UploadSubject) {
				meta.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusMissing
			},
			blockedRule: "require_valid_mi_setting",
		},
		{
			name: "independent required TMDB",
			mutate: func(meta *api.UploadSubject) {
				meta.Identity.TMDBID = 0
				meta.ProviderMetadata.TMDB = nil
			},
			blockedRule:    "require_metadata_id",
			projectionOnly: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := spPackUploadSubject()
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
				wantReady := test.blockedRule == ""
				_, _, _, projections, err := projector.Build(t.Context(), api.ReleaseSnapshot{}, meta, []api.TrackerID{"SP"}, nil, nil, mode)
				if err != nil {
					t.Fatal(err)
				}
				projection := projections.Projections[0]
				if got := projection.Readiness == api.ReadinessStatusReady && projection.DupeReady; got != wantReady {
					t.Fatalf("mode=%s: projection readiness=%s wantReady=%t decisions=%#v", mode, projection.Readiness, wantReady, projection.PolicyDecisions)
				}
				if test.blockedRule != "" && !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
					return decision.Code == test.blockedRule && decision.Disposition == api.RuleDispositionStrict && decision.Blocking
				}) {
					t.Fatalf("mode=%s: missing strict %s rejection: %#v", mode, test.blockedRule, projection.PolicyDecisions)
				}
				if len(projection.Questionnaire) != 0 || !slices.ContainsFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
					return decision.Code == "guidance_sp_pack_consistency" && decision.Decision == "advisory" && !decision.Blocking
				}) {
					t.Fatalf("mode=%s: pack guidance was missing or retained required questions: %#v", mode, projection)
				}
				if test.projectionOnly {
					// Metadata demands belong to the registry; direct adapter preparation uses only its site/family fallback.
					continue
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
					t.Fatalf("mode=%s: late preparation failure=%#v wantReady=%t", mode, failure, wantReady)
				}
				if failure != nil && (failure.Code() != "dry_run" || failure.Message() != "trackers: SP mediainfo missing required fields") {
					t.Fatalf("mode=%s: expected missing encode-settings rejection, got %#v", mode, failure)
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

func TestSPDebugPackGuidancePreservesCompletePackFailure(t *testing.T) {
	subject := spPassingSubject()
	subject.MediaFileFacts.Files[1].AudioLanguages = []string{"German"}
	subject.PackageFacts.DetectedEpisodes[0].Episodes = []int{1, 3}
	failures, err := ValidationPolicy().Check(t.Context(), subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	blocking, err := trackers.FirstBlockingRuleFailure("SP", failures, api.WorkflowExecutionModeDebug, nil)
	if err != nil || blocking == nil || blocking.Rule != "sp_pack_completeness" {
		t.Fatalf("debug pack guidance hid independent pack completeness: blocking=%#v err=%v", blocking, err)
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
		return decision.Code == "guidance_sp_pack_consistency" && decision.Decision == "advisory" && !decision.Blocking
	}) {
		t.Fatalf("debug lost passive pack guidance: %#v", projection.PolicyDecisions)
	}
}

func spPackUploadSubject() api.UploadSubject {
	files := []string{"Example.Show.S01E01.mkv", "Example.Show.S01E02.mkv"}
	meta := api.UploadSubject{
		SourcePath: "Example.Show.S01",
		TVPack:     true,
		SeasonInt:  1,
		Type:       "WEBDL",
		FileList:   files,
		Identity: api.ExternalIdentity{
			SourcePath: "Example.Show.S01",
			Generation: 1,
			Category:   api.CanonicalCategoryTV,
		},
		MediaFileFacts: api.MediaFileFacts{
			ExpectedFileCount: 2,
			Status:            api.MetadataEvidenceStatusPartial,
			TechnicalStatus:   api.MetadataEvidenceStatusPartial,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
		},
	}
	for _, file := range files {
		meta.MediaFileFacts.Files = append(meta.MediaFileFacts.Files, api.MediaFileFact{
			FileName:        file,
			Container:       "mkv",
			Resolution:      "1080p",
			VideoCodec:      "AVC",
			BitDepth:        "8",
			VideoTrackCount: 1,
			AudioLanguages:  []string{"Japanese"},
			AudioStatus:     api.MetadataEvidenceStatusComplete,
			SubtitleStatus:  api.MetadataEvidenceStatusComplete,
		})
	}
	return meta
}
