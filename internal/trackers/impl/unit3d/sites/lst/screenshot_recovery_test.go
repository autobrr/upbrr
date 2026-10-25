// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLSTHostedScreenshotEvidenceNeedsNoReassurance(t *testing.T) {
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
	if ready := prepare(meta); ready.Failure != nil || ready.Preview.Questionnaire != nil {
		t.Fatalf("verified screenshots created late reassurance: %+v", ready)
	}
	meta.TrackerQuestionnaireAnswers["LST"][key] = "lossy_or_changed"
	if ready := prepare(meta); ready.Failure != nil {
		t.Fatalf("retired saved answer blocked preparation: %+v", ready.Failure)
	}
	meta.ExactMedia.ScreenshotUploads[0].RawURL += "?replacement=1"
	meta.DescriptionOverride = strings.ReplaceAll(meta.DescriptionOverride, "full-0.png", "full-0.png?replacement=1")
	if changed := prepare(meta); changed.Failure != nil || changed.Preview.Questionnaire != nil {
		t.Fatalf("changed valid host URL created questionnaire: %+v", changed)
	}
	meta.ExactMedia.Screenshots[0].Format = "jpeg"
	if invalid := prepare(meta); invalid.Failure == nil || invalid.Preview.Questionnaire != nil ||
		!strings.Contains(invalid.Failure.Message, "lossless") || strings.Count(invalid.Failure.Message, "trackers: LST") != 1 {
		t.Fatalf("measured format problem lost direct warning: %+v", invalid)
	}
	_, _, _, warningProjection, err := projector.Build(t.Context(), api.ReleaseSnapshot{Fingerprint: "source"}, meta, []api.TrackerID{"LST"}, nil, nil, api.WorkflowExecutionModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	warning := warningProjection.Projections[0].WaivableRuleFingerprint
	if warning == "" {
		t.Fatal("measured screenshot problem has no acknowledgement fingerprint")
	}
	_, _, _, acknowledged, err := projector.Build(t.Context(), api.ReleaseSnapshot{Fingerprint: "source"}, meta, []api.TrackerID{"LST"}, nil, map[api.TrackerID]api.WorkflowFingerprint{"LST": warning}, api.WorkflowExecutionModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	projections = acknowledged.Projections
	if ready := prepare(meta); ready.Failure != nil || ready.Preview.Questionnaire != nil {
		t.Fatalf("acknowledged warning left a hidden final-description gate: %+v", ready)
	}
	meta.ExactMedia.Screenshots[1].Format = "jpeg"
	if stale := prepare(meta); stale.Failure == nil {
		t.Fatal("changed screenshot evidence reused prior acknowledgement")
	}
	for i := range meta.ExactMedia.Screenshots {
		meta.ExactMedia.Screenshots[i].Format = "png"
	}
	meta.DescriptionOverride = strings.Join(strings.Split(meta.DescriptionOverride, "\n")[:2], "\n")
	if missing := prepare(meta); missing.Failure == nil || !strings.Contains(missing.Failure.Message, "2 distinct prepared screenshots") {
		t.Fatalf("final image deficiency lost its measured warning: %+v", missing)
	}
	_, _, _, warningProjection, err = projector.Build(t.Context(), api.ReleaseSnapshot{Fingerprint: "source"}, meta, []api.TrackerID{"LST"}, nil, nil, api.WorkflowExecutionModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	warning = warningProjection.Projections[0].WaivableRuleFingerprint
	if warning == "" {
		t.Fatal("final description deficiency has no acknowledgement fingerprint")
	}
	_, _, _, acknowledged, err = projector.Build(t.Context(), api.ReleaseSnapshot{Fingerprint: "source"}, meta, []api.TrackerID{"LST"}, nil, map[api.TrackerID]api.WorkflowFingerprint{"LST": warning}, api.WorkflowExecutionModeNormal)
	if err != nil {
		t.Fatal(err)
	}
	projections = acknowledged.Projections
	if ready := prepare(meta); ready.Failure != nil || ready.Preview.Questionnaire != nil {
		t.Fatalf("acknowledged two-image description hit a late strict gate: %+v", ready)
	}
	meta.DescriptionOverride += "\nEdited release note"
	if stale := prepare(meta); stale.Failure == nil {
		t.Fatal("changed final description reused prior acknowledgement")
	}
	// Uninspected imported screenshots remain uploadable without local images.
	{
		meta.ExactMedia = &api.ExactMediaAssets{}
		meta.Identity.SourcePath = meta.SourcePath
		meta.Identity.Resolution.SourceFingerprint = "source"
		meta.MediaBinding = api.PreparedMediaBinding{
			SourcePath:               meta.SourcePath,
			PreparedGeneration:       meta.Identity.Generation,
			PreparedMediaFingerprint: "media",
		}
		meta.ProviderMetadata.ImportedScreenshotEvidence = &api.ImportedScreenshotEvidence{
			Tracker:                "SOURCE",
			TrackerID:              "42",
			SourceFingerprint:      "source",
			PolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
			DescriptionFingerprint: "description",
		}
		meta.DescriptionOverride = "Imported release notes"
		for i := range 8 {
			raw := fmt.Sprintf("https://images2.imgbox.com/synthetic/shot-%d.png", i)
			meta.ProviderMetadata.ImportedScreenshotEvidence.References = append(meta.ProviderMetadata.ImportedScreenshotEvidence.References,
				api.ImportedScreenshotReference{RawURL: raw})
			meta.DescriptionOverride += "\n[img]" + raw + "[/img]"
		}
		_, _, _, imported, err := projector.Build(t.Context(), api.ReleaseSnapshot{Fingerprint: "source"}, meta,
			[]api.TrackerID{"LST"}, nil, nil, api.WorkflowExecutionModeNormal)
		if err != nil {
			t.Fatal(err)
		}
		projections = imported.Projections
		if !projections[0].UploadReady || projections[0].Artifacts.ScreenshotCount != 0 || projections[0].WaivableRuleFingerprint != "" {
			t.Fatalf("unavailable inspection became a capture/confirmation requirement: %+v", projections[0])
		}
		if ready := prepare(meta); ready.Failure != nil || ready.Preview.Questionnaire != nil {
			t.Fatalf("reference-only images hit a late preparation gate: %+v", ready)
		}
	}
}
