// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestMapCLICompositeUploadRequestPreservesPerUploadOptions(t *testing.T) {
	t.Parallel()

	request := api.Request{
		SourcePath: `C:\releases\Example.Release.2026.1080p-GRP`,
		Options: api.UploadOptions{
			RunLogLevel:     "trace",
			Screens:         7,
			NoSeed:          true,
			SkipAutoTorrent: true,
			OnlyID:          true,
			KeepFolder:      true,
			KeepImages:      true,
			CaptureDVDMenus: true,
			InteractionMode: api.InteractionModeUnattendedConfirm,
		},
		Execution: api.ExecutionOptions{SiteCheck: true},
		Trackers:  []string{"alpha", "beta"},
		TrackersRemove: []string{
			"gamma",
		},
		IgnoreDupesFor:   []string{"BETA"},
		SkipDupeCheck:    true,
		SkipDupeAsActual: true,
		DoubleDupeCheck:  true,
		DescriptionGroups: []api.DescriptionBuilderGroup{{
			GroupKey:       "unit3d",
			RawDescription: "Synthetic tracker description.",
		}},
		DescriptionOverrideURL:   "https://example.test/description.txt",
		DescriptionOverrideGroup: "default",
		MetadataOverrides: api.MetadataOverrides{
			Distributor:      new("Synthetic Distributor"),
			OriginalLanguage: new("English"),
			PersonalRelease:  new(true),
			Commentary:       new(true),
			WebDV:            new(true),
			StreamOptimized:  new(true),
			Anime:            new(false),
		},
		TrackerConfigOverrides: api.TrackerConfigOverrides{
			Anon:    new(false),
			Draft:   new(true),
			ModQ:    new(false),
			Channel: new("synthetic"),
		},
		TrackerSiteOverrides: api.TrackerSiteOverrides{
			TIK: api.TIKOverrides{
				Foreign:  new(true),
				Opera:    new(false),
				Asian:    new(true),
				DiscType: new("UHD"),
			},
		},
		ClientOverrides: api.ClientOverrides{
			Client:       new("qBittorrent"),
			QbitTag:      new("synthetic"),
			QbitCategory: new("tests"),
			ForceRecheck: new(false),
		},
		ImageHostOverrides: api.ImageHostOverrides{
			PreferredHost: new("imgbox"),
			SkipUpload:    new(false),
		},
		ScreenshotOverrides: api.ScreenshotOverrides{
			ManualFrames:           []int{100, 200},
			ComparisonPaths:        []string{`C:\images\one.png`, `C:\images\two.png`},
			ComparisonPrimaryIndex: new(2),
			MenuPaths:              []string{`C:\images\menus`},
		},
		TorrentOverrides: api.TorrentOverrides{
			InfoHash:        new("0123456789abcdef0123456789abcdef01234567"),
			MaxPieceSizeMiB: new(16),
			NoHash:          new(false),
			Rehash:          new(true),
		},
		TrackerIDOverrides: map[string]string{
			"ALPHA": "synthetic-source-id",
		},
		ExternalIDOverrides: api.ExternalIDOverrides{
			TMDBID:   new(123),
			IMDBID:   new(1234567),
			MALID:    new(456),
			TVDBID:   new(789),
			TVmazeID: new(321),
		},
		ReleaseNameOverrides: api.ReleaseNameOverrides{
			Category:         new("Movie"),
			Type:             new("Encode"),
			Source:           new("WEB-DL"),
			Resolution:       new("1080p"),
			Tag:              new("GRP"),
			Service:          new("Example"),
			Edition:          new("Synthetic"),
			Season:           new("S01"),
			Episode:          new("E01"),
			EpisodeTitle:     new("Pilot"),
			ManualYear:       new(2026),
			ManualDate:       new("2026-07-25"),
			UseSeasonEpisode: new(true),
			NoSeason:         new(false),
			NoYear:           new(false),
			NoAKA:            new(false),
			NoTag:            new(false),
			NoEdition:        new(false),
			NoDub:            new(false),
			NoDual:           new(false),
			DualAudio:        new(true),
			Region:           new("A"),
		},
		TrackerQuestionnaireAnswers: map[string]map[string]string{
			"ALPHA": {"source": "web"},
		},
		PlaylistInstruction: api.PlaylistInstruction{
			Set:      true,
			Selected: []string{"00001.mpls"},
			UseAll:   false,
		},
		ConfirmBDMVRescan: true,
	}

	mapped, err := mapCLICompositeUploadRequest(request, false, "cli-parity")
	if err != nil {
		t.Fatalf("map CLI composite upload: %v", err)
	}
	if err := mapped.Validate(); err != nil {
		t.Fatalf("validate mapped request: %v", err)
	}
	if mapped.Execution.Mode != api.ReleaseWorkflowUploadModeDebug ||
		mapped.Execution.PreparedRelease != api.ReleaseWorkflowPreparedReleaseRequire ||
		mapped.Unattended == nil || !mapped.Unattended.Confirm {
		t.Fatalf("execution mapping = %#v / %#v", mapped.Execution, mapped.Unattended)
	}
	if !slicesEqual(mapped.Trackers.Include, []api.TrackerID{"ALPHA", "BETA"}) ||
		!slicesEqual(mapped.Trackers.Remove, []api.TrackerID{"GAMMA"}) ||
		mapped.Trackers.SourceIDs["ALPHA"] != "synthetic-source-id" {
		t.Fatalf("tracker mapping = %#v", mapped.Trackers)
	}
	if mapped.Trackers.DefaultProjection == nil ||
		mapped.Trackers.DefaultProjection.Config.Anon == nil ||
		*mapped.Trackers.DefaultProjection.Config.Anon ||
		mapped.Trackers.Projection["ALPHA"].Questionnaire["source"] == nil {
		t.Fatalf("projection mapping = %#v", mapped.Trackers)
	}
	if mapped.Duplicates.RemoteCheck == nil || *mapped.Duplicates.RemoteCheck ||
		mapped.Duplicates.CheckCount == nil || *mapped.Duplicates.CheckCount != 2 ||
		mapped.Duplicates.OnEvidence != api.ReleaseWorkflowDuplicateUpload ||
		!slicesEqual(mapped.Duplicates.AllowUpload, []api.TrackerID{"BETA"}) {
		t.Fatalf("duplicate mapping = %#v", mapped.Duplicates)
	}
	if mapped.Preparation.Facts.ExternalIDs.IMDB == nil ||
		mapped.Preparation.Facts.ExternalIDs.IMDB.Value == nil ||
		*mapped.Preparation.Facts.ExternalIDs.IMDB.Value != "tt1234567" ||
		mapped.Preparation.Facts.ReleaseName.Tag == nil ||
		*mapped.Preparation.Facts.ReleaseName.Tag != "GRP" ||
		mapped.Preparation.Facts.Metadata.Anime == nil ||
		*mapped.Preparation.Facts.Metadata.Anime {
		t.Fatalf("fact mapping = %#v", mapped.Preparation.Facts)
	}
	if mapped.Media.Screenshots.Count == nil || *mapped.Media.Screenshots.Count != 7 ||
		mapped.Media.Screenshots.ComparisonPrimaryIndex == nil ||
		*mapped.Media.Screenshots.ComparisonPrimaryIndex != 2 ||
		mapped.Media.DVDMenus.Capture == nil || !*mapped.Media.DVDMenus.Capture {
		t.Fatalf("media mapping = %#v", mapped.Media)
	}
	if len(mapped.Descriptions.Overrides) != 2 ||
		mapped.Descriptions.Overrides[1].URL == nil ||
		*mapped.Descriptions.Overrides[1].URL != "https://example.test/description.txt" {
		t.Fatalf("description mapping = %#v", mapped.Descriptions)
	}
	if mapped.Client.NoSeed == nil || !*mapped.Client.NoSeed ||
		mapped.Client.SkipAutoDiscovery == nil || !*mapped.Client.SkipAutoDiscovery ||
		mapped.Torrent.Rehash == nil || !*mapped.Torrent.Rehash {
		t.Fatalf("client/torrent mapping = %#v / %#v", mapped.Client, mapped.Torrent)
	}
}

func TestCLICompleteUsesCompositeStartAndFeedback(t *testing.T) {
	t.Parallel()

	action := api.RequiredAction{
		ID:               "action-approval",
		Kind:             api.RequiredActionApproveUpload,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: 7,
		Prompt:           "Approve exact upload.",
	}
	coreSvc := &cliWorkflowCoreFake{}
	coreSvc.startUploadFn = func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
		return releaseworkflow.CommandResult{
			Workflow: api.ReleaseWorkflow{
				ID:              "workflow-composite",
				Revision:        7,
				Status:          api.WorkflowStatusBlocked,
				RequiredActions: []api.RequiredAction{action},
			},
			DryRun: &api.UploadDryRunResult{ID: "dry-run-composite", Revision: 7},
		}, nil
	}
	coreSvc.feedbackFn = func(feedback api.ReleaseWorkflowUploadFeedback) (releaseworkflow.CommandResult, error) {
		if feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackUploadApproval ||
			feedback.Action.ID != action.ID ||
			feedback.Action.WorkflowRevision != action.WorkflowRevision {
			t.Fatalf("approval feedback = %#v", feedback)
		}
		return releaseworkflow.CommandResult{
			Workflow: api.ReleaseWorkflow{
				ID:       "workflow-composite",
				Revision: 9,
				Status:   api.WorkflowStatusCompleted,
			},
			UploadResult: &api.UploadResult{ID: "upload-composite", Status: api.StageStatusCompleted},
		}, nil
	}
	session := &cliWorkflowSession{
		core: coreSvc,
		intent: cliWorkflowIntent{
			sourcePath:  `C:\releases\Example.Release.2026.1080p-GRP`,
			interaction: api.InteractionModeUnattendedConfirm,
		},
		uploadRequest: api.Request{
			SourcePath: `C:\releases\Example.Release.2026.1080p-GRP`,
			Options: api.UploadOptions{
				Screens:         0,
				InteractionMode: api.InteractionModeUnattendedConfirm,
			},
		},
	}
	_, err := session.complete(
		context.Background(),
		false,
		bufio.NewReader(strings.NewReader("y\n")),
		config.Config{},
		api.NopLogger{},
	)
	if err != nil {
		t.Fatalf("complete composite upload: %v", err)
	}
	if len(coreSvc.uploadRequests) != 1 || len(coreSvc.uploadFeedback) != 1 ||
		len(coreSvc.continuations) != 0 {
		t.Fatalf(
			"composite calls: starts=%d feedback=%d granular=%d",
			len(coreSvc.uploadRequests),
			len(coreSvc.uploadFeedback),
			len(coreSvc.continuations),
		)
	}
}

func slicesEqual[T comparable](actual []T, expected []T) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}
