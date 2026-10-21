// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTHostedScreenshotQuestionSurvivesSourceOnlyAnswerProjection(t *testing.T) {
	root := t.TempDir()
	meta := api.UploadSubject{
		SourcePath:        filepath.Join(root, "Example.mkv"),
		TorrentPath:       filepath.Join(root, "Example.torrent"),
		MediaInfoTextPath: filepath.Join(root, "mediainfo.txt"),
		ReleaseName:       "Example.2026.1080p.WEB-DL-GRP",
		Identity: api.ExternalIdentity{
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     123,
			Generation: 1,
		},
		Release:       api.ReleaseInfo{Resolution: "1080p"},
		LanguageFacts: lstTestLanguageFacts("English", []string{"English"}, []string{"English"}),
		Type:          "WEBDL",
		Source:        "WEB-DL",
		Container:     "mkv",
		Assessments:   api.ReleaseAssessments{MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
	}
	for path, content := range map[string]string{
		meta.MediaInfoTextPath: "General\nFormat : Matroska\nVideo\nFormat : AVC",
		meta.TorrentPath:       "d4:infod6:lengthi1e4:name11:Example.mkv12:piece lengthi16384e6:pieces20:00000000000000000000ee",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	meta.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: meta.SourcePath,
		Generation: 1,
		TMDB: &api.TMDBMetadata{
			TMDBID:   123,
			Category: "movie",
			Title:    "Example",
			Year:     2026,
		},
	}
	meta.DescriptionOverride = lstDescriptionScreenshots(&meta)
	meta.DescriptionGroupsFinal = true
	key := assetQuestionKey(api.NewTrackerValidationSubject(meta, "LST"), "screenshot_preservation")
	delete(meta.TrackerQuestionnaireAnswers["LST"], key)
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "state")}, Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"LST": {APIKey: "test-key", AnnounceURL: "https://tracker.example/announce"}}}}
	service := trackers.NewServiceWithRegistry(cfg, api.NopLogger{}, nil, registry)
	projector, err := trackers.NewWorkflowProjector(registry, cfg, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	sourceOnly := meta
	sourceOnly.ExactMedia = nil
	_, _, _, initialProjection, err := projector.Build(t.Context(), api.ReleaseSnapshot{Fingerprint: "source"}, sourceOnly, []api.TrackerID{"LST"}, nil, nil, api.WorkflowExecutionModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	projections := initialProjection.Projections
	if !projections[0].UploadReady {
		t.Fatalf("source projection blocked: %+v", projections[0])
	}
	prepare := func(subject api.UploadSubject) trackers.RetainedTrackerPreparation {
		t.Helper()
		plan, err := service.PrepareRetainedUploadPlan(t.Context(), subject, projections)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := plan.Release(); err != nil {
				t.Error(err)
			}
		})
		return plan.Preparations()[0]
	}
	blocked := prepare(meta)
	if blocked.Failure == nil || blocked.Preview.Questionnaire == nil {
		t.Fatalf("missing recoverable late questionnaire: %+v failure=%+v", blocked, blocked.Failure)
	}
	found := false
	for _, field := range blocked.Preview.Questionnaire.Fields {
		if field.Key == key {
			found = field.Required && field.NeedsReview
		}
	}
	if !found {
		t.Fatalf("current hosted question missing: %+v", blocked.Preview.Questionnaire)
	}
	// CLI feedback rebuilds projections from source facts before exact assets return.
	answer := "lossless_preserved"
	_, _, _, projected, err := projector.Build(t.Context(), api.ReleaseSnapshot{Fingerprint: "source"}, sourceOnly, []api.TrackerID{"LST"},
		map[api.TrackerID]api.TrackerProjectionInstructions{"LST": {Questionnaire: map[string]*string{key: &answer}}}, nil, api.WorkflowExecutionModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	meta.TrackerQuestionnaireAnswers["LST"] = projected.Projections[0].QuestionnaireAnswers
	projections = projected.Projections
	if meta.TrackerQuestionnaireAnswers["LST"][key] != answer {
		t.Fatal("source-only projection dropped late answer")
	}
	if ready := prepare(meta); ready.Failure != nil {
		t.Fatalf("answered normal Unit3D preparation blocked: %+v", ready.Failure)
	}
	// Unrelated late HDR preparation does not invalidate screenshot inspection.
	meta.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatHDR10}}
	if got := assetQuestionKey(api.NewTrackerValidationSubject(meta, "LST"), "screenshot_preservation"); got != key {
		t.Fatal("unrelated HDR changed screenshot key")
	}
	meta.Identity.Generation++
	if got := assetQuestionKey(api.NewTrackerValidationSubject(meta, "LST"), "screenshot_preservation"); got == key {
		t.Fatal("source generation change retained screenshot key")
	}
	meta.Identity.Generation--
	meta.ExactMedia.ScreenshotUploads[0].RawURL += "?replacement=1"
	meta.DescriptionOverride = strings.ReplaceAll(meta.DescriptionOverride, "full-0.png", "full-0.png?replacement=1")
	if changed := prepare(meta); changed.Preview.Questionnaire == nil {
		t.Fatalf("changed host URL did not ask again: %+v", changed)
	}
	meta.ExactMedia.Screenshots[0].Format = "jpeg"
	if invalid := prepare(meta); invalid.Failure == nil || invalid.Preview.Questionnaire != nil || !strings.Contains(invalid.Failure.Message, "lossless") {
		t.Fatalf("strict format rejection was waived: %+v", invalid)
	}
}
