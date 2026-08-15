// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"slices"
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
			GroupKey:    "unit3d",
			Description: "Synthetic tracker description.",
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
			IMDBID:   new(456),
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
			NoEpisodeTitle:   new(false),
			NoDistributor:    new(false),
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
		*mapped.Preparation.Facts.ExternalIDs.IMDB.Value != "tt0000456" ||
		mapped.Preparation.Facts.ReleaseName.Tag == nil ||
		*mapped.Preparation.Facts.ReleaseName.Tag != "GRP" ||
		mapped.Preparation.Facts.ReleaseName.NoEpisodeTitle == nil ||
		*mapped.Preparation.Facts.ReleaseName.NoEpisodeTitle ||
		mapped.Preparation.Facts.ReleaseName.NoDistributor == nil ||
		*mapped.Preparation.Facts.ReleaseName.NoDistributor ||
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
		mapped.Descriptions.Overrides[0].Inline == nil ||
		*mapped.Descriptions.Overrides[0].Inline != "Synthetic tracker description." ||
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
	action := api.RequiredAction{
		ID:               "action-approval",
		Kind:             api.RequiredActionApproveTrackers,
		Status:           api.RequiredActionStatusPending,
		WorkflowRevision: 7,
		Prompt:           "Select trackers.",
		Options: []api.RequiredActionOption{
			{Value: "ALPHA", Label: "Alpha"},
			{Value: "BETA", Label: "Beta"},
		},
	}
	coreSvc := &cliWorkflowCoreFake{}
	coreSvc.startUploadFn = func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
		return releaseworkflow.CommandResult{
			Workflow: api.ReleaseWorkflow{
				ID:       "workflow-composite",
				Revision: 7,
				Status:   api.WorkflowStatusBlocked,
			},
			Continuation: api.WorkflowContinuation{RequiredActions: []api.RequiredAction{action}},
			Projections: &api.TrackerReleaseProjectionSet{
				Projections: []api.TrackerReleaseProjection{
					{
						TrackerID:         "ALPHA",
						DisplayName:       "Alpha",
						UploadReleaseName: "Example.Release.2026.ALPHA-GRP",
					},
					{
						TrackerID:         "BETA",
						DisplayName:       "Beta",
						UploadReleaseName: "Example.Release.2026.BETA-GRP",
					},
				},
			},
			Dupes: &api.DupeAssessment{Results: []api.TrackerDupeAssessment{
				{TrackerID: "ALPHA", Decision: api.DupeDecisionNoMatch},
				{TrackerID: "BETA", Decision: api.DupeDecisionNoMatch},
			}},
		}, nil
	}
	coreSvc.feedbackFn = func(feedback api.ReleaseWorkflowUploadFeedback) (releaseworkflow.CommandResult, error) {
		if feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackTrackerApproval ||
			feedback.Action.ID != action.ID ||
			feedback.Action.WorkflowRevision != action.WorkflowRevision ||
			feedback.Response.TrackerApproval == nil ||
			!slices.Equal(feedback.Response.TrackerApproval.TrackerIDs, []api.TrackerID{"ALPHA"}) {
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
	var (
		completeErr error
		output      string
	)
	output = captureStdout(t, func() {
		_, completeErr = session.complete(
			context.Background(),
			false,
			bufio.NewReader(strings.NewReader("y\nn\n")),
			config.Config{Logging: config.LoggingConfig{Level: "info"}},
			api.NopLogger{},
		)
	})
	err := completeErr
	if err != nil {
		t.Fatalf("complete composite upload: %v", err)
	}
	if strings.Contains(output, "Tracker projections") {
		t.Fatalf("INFO output included tracker projections: %q", output)
	}
	for _, prompt := range []string{
		`Use Alpha as "Example.Release.2026.ALPHA-GRP"? [y/N]:`,
		`Use Beta as "Example.Release.2026.BETA-GRP"? [y/N]:`,
	} {
		if !strings.Contains(output, prompt) {
			t.Fatalf("INFO output missing tracker approval prompt %q", prompt)
		}
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

func TestCLICompositeTrackerFeedbackConfirmsOrEditsReleaseName(t *testing.T) {
	const proposed = "Example.Release.2026-GRP"
	action := api.RequiredAction{
		Kind:             api.RequiredActionProvideTrackerInput,
		TrackerID:        "AR",
		Prompt:           "Confirm or edit the non-scene release name for AR.",
		Options:          []api.RequiredActionOption{{Value: proposed, Label: proposed}},
		AllowsFreeText:   true,
		WorkflowRevision: 4,
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "confirm proposed",
			input: "\n",
			want:  proposed,
		},
		{
			name:  "edit proposed",
			input: "Example.Release.2026.EDIT-GRP\n",
			want:  "Example.Release.2026.EDIT-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := &cliWorkflowSession{
				intent: cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
				uploadRequest: api.Request{
					Trackers: []string{"AR"},
				},
				current: releaseworkflow.CommandResult{
					Projections: &api.TrackerReleaseProjectionSet{
						Projections: []api.TrackerReleaseProjection{{TrackerID: "AR"}},
					},
				},
			}
			feedback, declined, err := session.collectCompositeTrackerFeedback(
				bufio.NewReader(strings.NewReader(test.input)),
				action,
				api.ReleaseWorkflowUploadFeedback{},
			)
			if err != nil {
				t.Fatalf("collect tracker feedback: %v", err)
			}
			if declined || feedback.Response.TrackerInput == nil {
				t.Fatalf("tracker feedback = %#v, declined=%t", feedback, declined)
			}
			patch := feedback.Response.TrackerInput.Projection.UploadReleaseName
			if !patch.Present || patch.Reset || patch.Value != test.want {
				t.Fatalf("release-name patch = %#v, want %q", patch, test.want)
			}
		})
	}
}

func TestCLICompositeRuleAuthorizationHonorsInteractionMode(t *testing.T) {
	action := api.RequiredAction{
		ID:               "action-waive-rules",
		Kind:             api.RequiredActionAuthorizeRules,
		TrackerID:        "ALPHA",
		WorkflowRevision: 4,
		Prompt:           "ALPHA rule warning: language rule. Upload to this tracker anyway?",
	}
	session := &cliWorkflowSession{
		intent: cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
		current: releaseworkflow.CommandResult{
			Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
				{TrackerID: "ALPHA", Readiness: api.ReadinessStatusBlocked},
				{TrackerID: "BETA", Readiness: api.ReadinessStatusReady},
			}},
		},
	}
	var (
		feedback api.ReleaseWorkflowUploadFeedback
		declined bool
		err      error
	)
	captureStdout(t, func() {
		feedback, declined, err = session.collectCompositeUploadFeedback(
			context.Background(),
			bufio.NewReader(strings.NewReader("y\n")),
			config.Config{},
			api.NopLogger{},
			action,
		)
	})
	if err != nil || declined ||
		feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackRuleAuthorization ||
		feedback.Response.RuleAuthorization == nil || !feedback.Response.RuleAuthorization.Confirmed {
		t.Fatalf("rule authorization feedback = %#v declined=%t err=%v", feedback, declined, err)
	}

	captureStdout(t, func() {
		feedback, declined, err = session.collectCompositeUploadFeedback(
			context.Background(),
			bufio.NewReader(strings.NewReader("n\n")),
			config.Config{},
			api.NopLogger{},
			action,
		)
	})
	if err != nil || declined || feedback.Response.RuleAuthorization == nil || feedback.Response.RuleAuthorization.Confirmed {
		t.Fatalf("rule rejection feedback = %#v declined=%t err=%v", feedback, declined, err)
	}

	session.current.Projections.Projections = session.current.Projections.Projections[:1]
	captureStdout(t, func() {
		feedback, declined, err = session.collectCompositeUploadFeedback(
			context.Background(),
			bufio.NewReader(strings.NewReader("n\n")),
			config.Config{},
			api.NopLogger{},
			action,
		)
	})
	if err != nil || !declined ||
		feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackRuleAuthorization ||
		feedback.Response.RuleAuthorization == nil || feedback.Response.RuleAuthorization.Confirmed {
		t.Fatalf("last tracker rejection = %#v declined=%t err=%v", feedback, declined, err)
	}

	session.intent.interaction = api.InteractionModeUnattended
	session.current.Projections.Projections = append(
		session.current.Projections.Projections,
		api.TrackerReleaseProjection{TrackerID: "BETA", Readiness: api.ReadinessStatusReady},
	)
	feedback, declined, err = session.collectCompositeUploadFeedback(
		context.Background(),
		nil,
		config.Config{},
		api.NopLogger{},
		action,
	)
	if err != nil || declined || feedback.Response.RuleAuthorization == nil || feedback.Response.RuleAuthorization.Confirmed {
		t.Fatalf("strict unattended rule rejection = %#v declined=%t err=%v", feedback, declined, err)
	}

	session.current.Projections.Projections = session.current.Projections.Projections[:1]
	feedback, declined, err = session.collectCompositeUploadFeedback(
		context.Background(),
		nil,
		config.Config{},
		api.NopLogger{},
		action,
	)
	if err != nil || !declined ||
		feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackRuleAuthorization ||
		feedback.Response.RuleAuthorization == nil || feedback.Response.RuleAuthorization.Confirmed {
		t.Fatalf("strict unattended last tracker rejection = %#v declined=%t err=%v", feedback, declined, err)
	}
}

func TestCLICompositeTrackerPreparationDeclineReturnsFeedback(t *testing.T) {
	session := &cliWorkflowSession{
		intent: cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
	}
	action := api.RequiredAction{
		ID:               "action-btn-autofill",
		Kind:             api.RequiredActionResolveTrackerPreparation,
		WorkflowRevision: 4,
		Prompt:           "Continue with BTN's autofill result?",
	}
	var (
		feedback api.ReleaseWorkflowUploadFeedback
		declined bool
		err      error
	)
	captureStdout(t, func() {
		feedback, declined, err = session.collectCompositeUploadFeedback(
			context.Background(),
			bufio.NewReader(strings.NewReader("n\n")),
			config.Config{},
			api.NopLogger{},
			action,
		)
	})
	if err != nil || declined || feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackTrackerPreparation ||
		feedback.Response.TrackerPreparation == nil || feedback.Response.TrackerPreparation.Confirmed {
		t.Fatalf("tracker preparation feedback = %#v declined=%t err=%v", feedback, declined, err)
	}

	session.intent.interaction = api.InteractionModeUnattended
	feedback, declined, err = session.collectCompositeUploadFeedback(
		context.Background(),
		nil,
		config.Config{},
		api.NopLogger{},
		action,
	)
	if err != nil || declined || feedback.Response.Kind != api.ReleaseWorkflowUploadFeedbackTrackerPreparation ||
		feedback.Response.TrackerPreparation == nil || feedback.Response.TrackerPreparation.Confirmed {
		t.Fatalf("strict unattended tracker preparation feedback = %#v declined=%t err=%v", feedback, declined, err)
	}
}

func TestCLICompositeDuplicateReviewPrintsMatchesAndSeparatesTrackers(t *testing.T) {
	session := &cliWorkflowSession{
		current: releaseworkflow.CommandResult{
			Dupes: &api.DupeAssessment{
				Results: []api.TrackerDupeAssessment{
					{
						TrackerID:         "ALPHA",
						UploadReleaseName: "Example.Release.2026.1080p-GRP",
						Matches: []api.DupeMatchProjection{
							{
								Name: "Example.Release.2026.1080p.WEB-DL-GRP",
								Link: "https://alpha.example/torrents/123",
							},
							{Name: "Example.Release.2026.1080p.BluRay-GRP"},
						},
						Decision: api.DupeDecisionAccepted,
					},
					{
						TrackerID:         "BETA",
						UploadReleaseName: "Example.Release.2026.1080p-GRP",
						Matches: []api.DupeMatchProjection{
							{Name: "Example.Release.2026.1080p.Encode-GRP"},
						},
						Decision: api.DupeDecisionAccepted,
					},
				},
			},
		},
	}
	var feedbackByTracker []api.ReleaseWorkflowUploadFeedback
	output := captureStdout(t, func() {
		for _, trackerID := range []api.TrackerID{"ALPHA", "BETA"} {
			feedback, declined, err := session.collectCompositeDuplicateFeedback(
				bufio.NewReader(strings.NewReader("n\n")),
				api.RequiredAction{TrackerID: trackerID},
				api.ReleaseWorkflowUploadFeedback{},
			)
			if err != nil {
				t.Fatalf("collect duplicate feedback for %s: %v", trackerID, err)
			}
			if declined {
				t.Fatalf("duplicate feedback for %s was declined", trackerID)
			}
			feedbackByTracker = append(feedbackByTracker, feedback)
		}
	})

	for _, expected := range []string{
		"Dupe check ALPHA: upload_name=Example.Release.2026.1080p-GRP candidates=2 decision=accepted search_complete=false pages=0 policy=none",
		"Duplicate candidates:\n  1. Example.Release.2026.1080p.WEB-DL-GRP\n     Relation: none  Evidence: none/none\n     Link: https://alpha.example/torrents/123\n  2. Example.Release.2026.1080p.BluRay-GRP",
		"Upload to ALPHA despite duplicate evidence? [y/N]: \nDupe check BETA:",
		"Duplicate candidates:\n  1. Example.Release.2026.1080p.Encode-GRP",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("duplicate review output missing %q: %q", expected, output)
		}
	}
	for index, trackerID := range []api.TrackerID{"ALPHA", "BETA"} {
		review := feedbackByTracker[index].Response.DuplicateReview
		if review == nil || review.TrackerID != trackerID || review.Decision != api.DupeDecisionAccepted {
			t.Fatalf("duplicate feedback for %s = %#v", trackerID, feedbackByTracker[index])
		}
	}
}

func TestCLIWorkflowProjectionOutputEnabledOnlyForDebugDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		override   string
		debug      bool
		want       bool
	}{
		{name: "info", configured: "info"},
		{name: "warn", configured: "warn"},
		{name: "error", configured: "error"},
		{
			name:       "debug configured",
			configured: "debug",
			want:       true,
		},
		{
			name:       "trace override",
			configured: "info",
			override:   "trace",
			want:       true,
		},
		{
			name:       "debug mode fallback",
			configured: "info",
			debug:      true,
			want:       true,
		},
		{
			name:       "explicit info overrides debug mode",
			configured: "trace",
			override:   "info",
			debug:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := cliWorkflowProjectionOutputEnabled(test.configured, test.override, test.debug); got != test.want {
				t.Fatalf("projection output enabled = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCLICompositeLegacyAuthActionRequiresFreshAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind api.RequiredActionKind
	}{
		{name: "authentication", kind: legacyTrackerAuthActionKind},
		{name: "unfinished two-factor", kind: legacyTrackerTwoFactorActionKind},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			session := &cliWorkflowSession{
				intent: cliWorkflowIntent{interaction: api.InteractionModeInteractive},
			}
			feedback, declined, err := session.collectCompositeUploadFeedback(
				context.Background(),
				nil,
				config.Config{},
				api.NopLogger{},
				api.RequiredAction{
					ID:               "action-auth-skip",
					Kind:             test.kind,
					WorkflowRevision: 4,
					TrackerID:        "ALPHA",
				},
			)
			if err == nil ||
				!strings.Contains(err.Error(), "outside the upload workflow") ||
				!strings.Contains(err.Error(), "fresh attempt") {
				t.Fatalf("legacy tracker auth error = %v", err)
			}
			if declined || feedback.Response.Kind != "" {
				t.Fatalf("legacy tracker auth feedback = %#v declined=%t", feedback, declined)
			}
		})
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
