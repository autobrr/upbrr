// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestComparisonApprovalBindsRetainedPixelsAcrossRestart(t *testing.T) {
	media, retained := testComparisonDescriptionMedia(t, false)
	media.Comparisons.ApprovedFingerprint = ""
	b := workflowMediaBuilder{media: &mediaModule{}}
	if err := verifyWorkflowComparisons(t.Context(), media, retained); err == nil {
		t.Fatal("unreviewed cells gained hosting authority")
	}
	updated, private, err := b.EditComparisons(t.Context(), api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, &media, retained,
		api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: media.Comparisons.Fingerprint}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	approved, ok := private.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatal("comparison private resource has wrong type")
	}
	_, payload, err := approved.MarshalPrivateResource()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyWorkflowComparisons(t.Context(), updated, restored); err != nil {
		t.Fatalf("restart lost reviewed identity: %v", err)
	}
	rebound, reboundPrivate, err := b.RestoreComparisons(t.Context(), api.ReleaseRef{SourcePath: "Example.Movie.mkv", Generation: 2}, api.TrackerReleaseProjectionSet{}, updated, restored, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rebound.Comparisons.ApprovedFingerprint != "" || len(rebound.Comparisons.Rows) != len(updated.Comparisons.Rows) {
		t.Fatal("repreparation lost comparison intent or inherited review")
	}
	for r, row := range updated.Comparisons.Rows {
		for c, cell := range row.Cells {
			next := rebound.Comparisons.Rows[r].Cells[c]
			if cell.ArtifactID != next.ArtifactID || cell.ContentSHA256 != next.ContentSHA256 {
				t.Fatal("repreparation shifted retained pixels")
			}
			if _, ok := reboundPrivate.(workflowMediaPrivateArtifacts); !ok {
				t.Fatal("rebound comparison private resource has wrong type")
			}
		}
	}
	cell := updated.Comparisons.Rows[0].Cells[0]
	path, err := restored.MediaArtifactPath(updated, cell.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	if path != approved.ArtifactImages[cell.ArtifactID].Path {
		t.Fatal("CLI review selected different pixels")
	}
	if err := os.WriteFile(path, []byte("changed after review"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = b.UploadImages(t.Context(), api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, updated, restored, []api.PublicResourceID{cell.ArtifactID}, "", false, nil, time.Now())
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed bytes reached hosting boundary: %v", err)
	}
}

func TestComparisonImportReplacementRetainsMappingAndPixels(t *testing.T) {
	media, retained := testComparisonDescriptionMedia(t, false)
	plan, err := media.Comparisons.Clone()
	if err != nil {
		t.Fatal(err)
	}
	plan.Rows[0], plan.Rows[1] = plan.Rows[1], plan.Rows[0]
	plan.Sources[0], plan.Sources[2] = plan.Sources[2], plan.Sources[0]
	original := plan.Rows[0].Cells[1]
	replacement := plan.Rows[1].Cells[0]
	content, err := os.ReadFile(retained.ArtifactImages[replacement.ArtifactID].Path)
	if err != nil {
		t.Fatal(err)
	}
	plan.Rows[1].Cells[0].ArtifactID = "replacement"
	root := t.TempDir()
	b := workflowMediaBuilder{config: config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "app.sqlite")}}, media: &mediaModule{}}
	updated, private, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: filepath.Join(root, "Example.Movie.mkv"), Generation: 1}, api.TrackerReleaseProjectionSet{}, &media, retained,
		api.ComparisonInstructions{Action: "configure", Plan: plan}, []releaseworkflow.StagedMediaAttachment{{
			Attachment: api.MediaAttachment{
				Resource: api.WorkflowResourceRef{ID: "replacement"},
				Kind:     api.MediaArtifactComparison,
				Purpose:  api.ScreenshotPurposeComparison,
			},
			Content: releaseworkflow.StagedMediaContent{Name: "replacement.png", Bytes: content},
		}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Comparisons.ApprovedFingerprint != "" || len(updated.Comparisons.Problems) != 0 {
		t.Fatalf("replacement review: %+v", updated.Comparisons)
	}
	if updated.Comparisons.Rows[0].ID != plan.Rows[0].ID || updated.Comparisons.Sources[0].ID != plan.Sources[0].ID {
		t.Fatal("replacement reordered reviewed rows/columns")
	}
	kept := updated.Comparisons.Rows[0].Cells[1]
	if kept.ArtifactID != original.ArtifactID || kept.ContentSHA256 != original.ContentSHA256 {
		t.Fatal("replacement shifted an untouched cell")
	}
	keptPrivate, ok := private.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatal("comparison private resource has wrong type")
	}
	if len(keptPrivate.HostedImages) != 5 || keptPrivate.HostedSources[api.PublicResourceID("hosted-"+string(original.ArtifactID))] != original.ArtifactID {
		t.Fatal("replacing one cell discarded unchanged hosted links")
	}
	pixels, err := os.ReadFile(keptPrivate.ArtifactImages[kept.ArtifactID].Path)
	if err != nil {
		t.Fatal(err)
	}
	oldPixels, err := os.ReadFile(retained.ArtifactImages[original.ArtifactID].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pixels, oldPixels) {
		t.Fatal("untouched imported pixels changed")
	}
	if media.Comparisons.Rows[0].ID == plan.Rows[0].ID {
		t.Fatal("editing mutated the retained prior revision")
	}
	incomplete, err := updated.Comparisons.Clone()
	if err != nil {
		t.Fatal(err)
	}
	incomplete.Rows = append(incomplete.Rows, api.ComparisonRow{ID: "unmapped", Cells: []api.ComparisonCell{{SourceID: "1"}, {SourceID: "2"}, {SourceID: "3"}}})
	incomplete.Sources = append(incomplete.Sources, api.ComparisonSource{
		ID:       "empty-column",
		Label:    "Pending",
		Imported: true,
	})
	incomplete.Rows[0].Cells[0].ArtifactID = "next-replacement"
	updated, _, err = b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: filepath.Join(root, "Example.Movie.mkv"), Generation: 1}, api.TrackerReleaseProjectionSet{}, &updated, private,
		api.ComparisonInstructions{Action: "configure", Plan: incomplete}, []releaseworkflow.StagedMediaAttachment{{
			Attachment: api.MediaAttachment{
				Resource: api.WorkflowResourceRef{ID: "next-replacement"},
				Kind:     api.MediaArtifactComparison,
				Purpose:  api.ScreenshotPurposeComparison,
			},
			Content: releaseworkflow.StagedMediaContent{Name: "replacement.png", Bytes: content},
		}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Comparisons.Rows) != 3 || updated.Comparisons.Rows[2].ID != "unmapped" || len(updated.Comparisons.Rows[2].Cells) != 3 ||
		len(updated.Comparisons.Sources) != 4 || len(updated.Comparisons.Problems) == 0 {
		t.Fatalf("replacement discarded incomplete review intent: %+v", updated.Comparisons)
	}
}

func TestComparisonVideoPipelineUsesObservedSourceClock(t *testing.T) {
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("native synthetic FFmpeg fixture requires ffmpeg")
	}
	root := t.TempDir()
	mainPath := filepath.Join(root, "Example.Main.mkv")
	secondPath := filepath.Join(root, "Example.Second.mkv")
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=10:duration=4", "-vf", "setpts=PTS+5/TB", "-c:v", "ffv1", mainPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("synthetic main fixture: %v %s", err, output)
	}
	command = exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-i", mainPath, "-vf", "setpts=PTS-STARTPTS", "-c:v", "ffv1", secondPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("synthetic second fixture: %v %s", err, output)
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "app.sqlite")}}
	renderer := screenshots.NewService(cfg, api.NopLogger{}, root, nil)
	main, err := renderer.InspectComparisonSource(t.Context(), mainPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderer.InspectComparisonSource(t.Context(), secondPath)
	if err != nil {
		t.Fatal(err)
	}
	mainFingerprint, err := comparisonVideoFingerprint(t.Context(), mainPath)
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint, err := comparisonVideoFingerprint(t.Context(), secondPath)
	if err != nil {
		t.Fatal(err)
	}
	retained := workflowMediaPrivateArtifacts{
		ComparisonSources: map[string]workflowComparisonSource{"main": {Source: main, Fingerprint: mainFingerprint}, "other": {Source: second, Fingerprint: secondFingerprint}},
		ArtifactImages:    map[api.PublicResourceID]api.ScreenshotImage{},
		HostedImages:      map[api.PublicResourceID]api.UploadedImageLink{},
		HostedSources:     map[api.PublicResourceID]api.PublicResourceID{},
	}
	plan := api.ComparisonPlan{
		MainSourceID: "main",
		Preferences:  api.ComparisonPreferences{Count: 2},
		Sources: []api.ComparisonSource{{
			ID:          "main",
			Label:       "Source",
			Fingerprint: mainFingerprint,
		}, {
			ID:          "other",
			Label:       "Encode",
			Fingerprint: secondFingerprint,
		}},
		Rows: []api.ComparisonRow{{ID: "1", Cells: []api.ComparisonCell{{SourceID: "main", TimestampSeconds: new(1.0)}}}, {ID: "2", Cells: []api.ComparisonCell{{SourceID: "main", TimestampSeconds: new(2.0)}}}},
	}
	b := workflowMediaBuilder{
		config:             cfg,
		media:              &mediaModule{},
		comparisonRenderer: renderer,
		resolver:           workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{VideoPath: mainPath}},
	}
	matched, err := b.matchWorkflowComparisons(t.Context(), retained, plan)
	if err != nil {
		t.Fatal(err)
	}
	automatic, err := plan.Clone()
	if err != nil {
		t.Fatal(err)
	}
	automatic.Rows = nil
	automatic, err = b.matchWorkflowComparisons(t.Context(), retained, automatic)
	if err != nil {
		t.Fatal(err)
	}
	automaticPrior, err := automatic.Clone()
	if err != nil {
		t.Fatal(err)
	}
	automatic.Sources[0].Overlay = true
	retainComparisonCellFacts(&automatic, &api.MediaArtifactSet{Comparisons: &automaticPrior})
	automatic.Preferences.Count = 3
	automatic, err = b.matchWorkflowComparisons(t.Context(), retained, automatic)
	if err != nil || len(automatic.Rows) != 3 {
		t.Fatalf("automatic rows ignored changed selection preferences: rows=%d err=%v", len(automatic.Rows), err)
	}
	snapshot := api.MediaArtifactSet{}
	captured, err := b.captureWorkflowComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, &snapshot, &retained, matched, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Rows) != 2 {
		t.Fatalf("rows=%d", len(captured.Rows))
	}
	for _, row := range captured.Rows {
		for _, cell := range row.Cells {
			if cell.ObservedPTS == nil || cell.TimeBaseDenominator == 0 || cell.FrameNumber != nil || cell.PictureType == "" {
				t.Fatalf("invented/missing decoded observation: %+v", cell)
			}
			raw := float64(*cell.ObservedPTS) * float64(cell.TimeBaseNumerator) / float64(cell.TimeBaseDenominator)
			if cell.SourceID == "main" && raw-*cell.TimestampSeconds < 4.9 {
				t.Fatalf("nonzero source clock was lost: %+v", cell)
			}
		}
	}
	fingerprint, err := comparisons.ReviewFingerprint(captured)
	if err != nil {
		t.Fatal(err)
	}
	captured.Fingerprint, captured.ApprovedFingerprint = fingerprint, fingerprint
	snapshot.Comparisons = &captured
	syncComparisonSelections(&snapshot, captured)
	if err := verifyWorkflowComparisons(t.Context(), snapshot, retained); err != nil {
		t.Fatal(err)
	}
	plainPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	overlaySnapshot, err := snapshot.Clone()
	if err != nil {
		t.Fatal(err)
	}
	overlaySnapshot.Comparisons.Sources[0].Overlay = true
	overlayPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, overlaySnapshot, retained, "main", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plainPreview.Bytes, overlayPreview.Bytes) || *plainPreview.ObservedPTS != *overlayPreview.ObservedPTS {
		t.Error("comparison preview ignored its overlay or changed frame")
	}
	nextPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", plainPreview.TimestampSeconds, 1)
	if err != nil {
		t.Fatal(err)
	}
	previousPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", nextPreview.TimestampSeconds, -1)
	if err != nil {
		t.Fatal(err)
	}
	if nextPreview.TimestampSeconds <= plainPreview.TimestampSeconds || *previousPreview.ObservedPTS != *plainPreview.ObservedPTS {
		t.Error("decoded frame stepping skipped or repeated its source clock")
	}
	typed, err := captured.Clone()
	if err != nil {
		t.Fatal(err)
	}
	typed.Sources[1].AllowedFrameTypes = []string{"B"}
	typeChanged, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained,
		api.ComparisonInstructions{Action: "capture", Plan: typed}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cell := typeChanged.Comparisons.Rows[0].Cells[1]; cell.PictureType != "I" || !strings.Contains(cell.Warning, "B") {
		t.Error("changed type preference silently reused an incompatible frame")
	}
	// A retained orphan image cannot be relabelled as another requested frame.
	edited, err := captured.Clone()
	if err != nil {
		t.Fatal(err)
	}
	old := edited.Rows[0].Cells[0]
	retained.ArtifactImages["orphan"] = retained.ArtifactImages[old.ArtifactID]
	edited.Rows[0].Cells[0].ArtifactID = "orphan"
	edited.Rows[0].Cells[0].TimestampSeconds = new(*old.TimestampSeconds + 0.1)
	edited.Rows[0].Cells[0].FrameNumber = new(int64(999))
	fresh, private, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained, api.ComparisonInstructions{Action: "capture", Plan: edited}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	current := fresh.Comparisons.Rows[0].Cells[0]
	if current.ArtifactID == "orphan" || current.ArtifactID == old.ArtifactID || current.FrameNumber != nil || fresh.Comparisons.ApprovedFingerprint != "" {
		t.Fatalf("old pixels/fabricated ordinal survived recapture: %+v", current)
	}
	if fresh.Comparisons.Rows[0].Cells[1].Confidence != nil || fresh.Comparisons.Rows[0].Cells[1].ArtifactID != captured.Rows[0].Cells[1].ArtifactID {
		t.Fatal("changing the main moment retained stale correspondence confidence or replaced unchanged pixels")
	}
	// Recapturing the same observed frame must not append duplicate artifact IDs.
	again, err := fresh.Comparisons.Clone()
	if err != nil {
		t.Fatal(err)
	}
	again.Rows[0].Cells[0].ArtifactID = ""
	again.Rows[0].Cells[0].ContentSHA256 = ""
	repeated, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &fresh, private, api.ComparisonInstructions{Action: "capture", Plan: again}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identities := map[api.PublicResourceID]bool{}
	for _, artifact := range repeated.Artifacts {
		if identities[artifact.ID] {
			t.Fatal("same-frame recapture duplicated artifact identity")
		}
		identities[artifact.ID] = true
	}
	// A replacement submitted with matching must select that video immediately.
	fresh.Comparisons.Anchors = []api.ComparisonAnchor{{
		SourceID:      "other",
		MainSeconds:   1,
		SourceSeconds: 1.1,
	}}
	replaced, replacedPrivate, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &fresh, private,
		api.ComparisonInstructions{
			Action:       "match",
			Plan:         *fresh.Comparisons,
			VideoSources: []api.ComparisonVideoSelection{{ID: "other", Path: mainPath}},
		}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replacedArtifacts, ok := replacedPrivate.(workflowMediaPrivateArtifacts)
	if !ok || replacedArtifacts.ComparisonSources["other"].Source.Path != mainPath || replaced.Comparisons.ApprovedFingerprint != "" || len(replaced.Comparisons.Anchors) != 0 {
		t.Fatal("matching silently used the previously selected video")
	}
	for _, row := range replaced.Comparisons.Rows {
		for _, cell := range row.Cells {
			if cell.TimestampSeconds != nil && cell.ArtifactID == "" {
				t.Error("matching did not retain inspectable proposed row images")
			}
		}
	}
	plan.Rows = plan.Rows[:1]
	plan.Anchors = []api.ComparisonAnchor{{
		SourceID:      "other",
		MainSeconds:   1,
		SourceSeconds: 1.1,
	}}
	anchored, err := b.matchWorkflowComparisons(t.Context(), retained, plan)
	if err != nil {
		t.Fatal(err)
	}
	if cell := anchored.Rows[0].Cells[1]; cell.TimestampSeconds == nil || *cell.TimestampSeconds != 1.1 {
		t.Fatal("manual anchor between coarse samples was not resolved")
	}
	removed, err := captured.Clone()
	if err != nil {
		t.Fatal(err)
	}
	removed.Sources, removed.Rows = removed.Sources[:1], nil
	_, removedPrivate, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained,
		api.ComparisonInstructions{Action: "configure", Plan: removed}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := removedPrivate.(workflowMediaPrivateArtifacts).ComparisonSources["other"]; exists {
		t.Error("removing a column retained a video handle reusable by a new column")
	}
	b.resolver = workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{VideoPath: secondPath}}
	restored, restoredPrivate, err := b.RestoreComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 2}, api.TrackerReleaseProjectionSet{}, snapshot, retained, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := restoredPrivate.(workflowMediaPrivateArtifacts).ComparisonSources["main"]; exists {
		t.Error("restoration retained the previous main video's handle")
	}
	for _, action := range []string{"match", "capture"} {
		_, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 2}, api.TrackerReleaseProjectionSet{}, &restored, restoredPrivate,
			api.ComparisonInstructions{Action: action, Plan: *restored.Comparisons}, nil, time.Now())
		if err == nil || action == "match" && !strings.Contains(err.Error(), "configure this column again") {
			t.Errorf("%s used a stale main video: %v", action, err)
		}
	}
}
