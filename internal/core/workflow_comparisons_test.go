// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"fmt"
	"math"
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

// comparisonPictureTypeRenderer models a GOP whose coarse target samples are I
// pictures but intervening decoded pictures are B. Native pixels and PTS are
// supplied by the existing renderer; only the picture-type fixture is scripted.
type comparisonPictureTypeRenderer struct {
	comparisonFrameRenderer
}

func (r comparisonPictureTypeRenderer) SampleComparisonSource(ctx context.Context, source screenshots.ComparisonSource,
	start, duration, stride float64) ([]screenshots.ComparisonSample, error) {
	frames, err := r.comparisonFrameRenderer.SampleComparisonSource(ctx, source, start, duration, stride)
	for i := range frames {
		frames[i].PictureType = "B"
		if source.StartTimeSeconds < 1 && math.Abs(frames[i].RelativeTimestampSeconds*2-math.Round(frames[i].RelativeTimestampSeconds*2)) < 1e-6 {
			frames[i].PictureType = "I"
		}
	}
	if err != nil {
		return nil, fmt.Errorf("sample comparison fixture: %w", err)
	}
	return frames, nil
}

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
	replace := func(t *testing.T, request api.ComparisonPlan) api.MediaArtifactSet {
		t.Helper()
		request.Rows[0].Cells[0].ArtifactID = "next-replacement"
		next, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: filepath.Join(root, "Example.Movie.mkv"), Generation: 1}, api.TrackerReleaseProjectionSet{}, &updated, private,
			api.ComparisonInstructions{Action: "configure", Plan: request}, []releaseworkflow.StagedMediaAttachment{{
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
		return next
	}
	t.Run("numeric layout aliases", func(t *testing.T) {
		request, err := updated.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		request.Rows[0].ID = "0" + request.Rows[0].ID
		request.MainSourceID = "0" + request.MainSourceID
		for i := range request.Sources {
			request.Sources[i].ID = "0" + request.Sources[i].ID
		}
		for r := range request.Rows {
			for c := range request.Rows[r].Cells {
				request.Rows[r].Cells[c].SourceID = "0" + request.Rows[r].Cells[c].SourceID
			}
		}
		next := replace(t, request)
		if len(next.Comparisons.Rows) != 2 || next.Comparisons.Rows[0].ID != updated.Comparisons.Rows[0].ID ||
			len(next.Comparisons.Sources) != 3 || next.Comparisons.MainSourceID != updated.Comparisons.MainSourceID || len(next.Comparisons.Problems) != 0 {
			t.Fatalf("numeric aliases changed retained layout: %+v", next.Comparisons)
		}
	})
	t.Run("incomplete row serialization", func(t *testing.T) {
		incomplete, err := updated.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		incomplete.Rows = append(incomplete.Rows,
			api.ComparisonRow{ID: "unmapped", Cells: []api.ComparisonCell{{SourceID: "1"}, {SourceID: "2"}, {SourceID: "3"}}},
			api.ComparisonRow{ID: "empty", Cells: []api.ComparisonCell{}},
		)
		incomplete.Sources = append(incomplete.Sources, api.ComparisonSource{
			ID:       "empty-column",
			Label:    "Pending",
			Imported: true,
		})
		next := replace(t, incomplete)
		wire, err := next.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		if len(wire.Rows) != 4 || wire.Rows[2].ID != "unmapped" || len(wire.Rows[2].Cells) != 3 || wire.Rows[3].ID != "empty" || wire.Rows[3].Cells == nil ||
			len(wire.Sources) != 4 || len(wire.Problems) == 0 {
			t.Fatalf("replacement discarded or nulled incomplete review intent: %+v", wire)
		}
	})
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
	command = exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-i", mainPath, "-vf", "setpts=PTS-STARTPTS,scale=192:128", "-c:v", "ffv1", secondPath)
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
			Overlay:     true,
			Fingerprint: mainFingerprint,
		}, {
			ID:          "other",
			Label:       "Encode",
			Overlay:     true,
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
	t.Run("new video defaults and retained tone-mapping intent", func(t *testing.T) {
		for _, legacy := range []bool{false, true} {
			var prior *api.MediaArtifactSet
			if legacy {
				prior = &api.MediaArtifactSet{Comparisons: &plan}
			}
			configured, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, prior, retained,
				api.ComparisonInstructions{
					Action:       "configure",
					Plan:         plan,
					VideoSources: []api.ComparisonVideoSelection{{ID: "other", Path: secondPath}},
				}, nil, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range configured.Comparisons.Sources {
				if !source.Overlay || legacy && source.ToneMap != nil || !legacy && (source.ToneMap == nil || *source.ToneMap) {
					t.Fatalf("legacy=%t video defaults changed intent: %+v", legacy, source)
				}
			}
		}
	})
	matched, err := b.matchWorkflowComparisons(t.Context(), retained, plan, b.comparisonProgress(t.Context(), 5))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("retain resolved cells in incomplete rows", func(t *testing.T) {
		partial, err := matched.Clone()
		if err != nil {
			t.Fatal(err)
		}
		partial.Rows = partial.Rows[:1]
		partial.Sources[0].PreferBFrames, partial.Sources[0].AllowedFrameTypes = true, []string{"B"}
		partial.Rows[0].Cells[1] = api.ComparisonCell{SourceID: "other", Warning: "no_visual_match"}
		private := cloneWorkflowMediaPrivateArtifacts(retained)
		media := api.MediaArtifactSet{}
		partial, err = b.captureWorkflowComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, &media, &private,
			partial, b.comparisonProgress(t.Context(), 1), true)
		if err != nil || partial.Rows[0].Cells[0].ArtifactID == "" || partial.Rows[0].Cells[1].ArtifactID != "" || len(comparisons.Validate(partial)) == 0 {
			t.Fatalf("resolved pixels were lost or an unresolved column gained capture authority: %+v err=%v", partial, err)
		}
		if strings.Contains(partial.Rows[0].Cells[0].Warning, "Rematch") {
			t.Fatal("an allowed default picture-type fallback was reported as incompatible")
		}
	})
	policyPlan, err := plan.Clone()
	if err != nil {
		t.Fatal(err)
	}
	for i := range policyPlan.Sources {
		policyPlan.Sources[i].PreferBFrames = true
		policyPlan.Sources[i].AllowedFrameTypes = []string{"B"}
	}
	policyBuilder := b
	policyBuilder.comparisonRenderer = comparisonPictureTypeRenderer{renderer}
	policyMatched, err := policyBuilder.matchWorkflowComparisons(t.Context(), retained, policyPlan, b.comparisonProgress(t.Context(), 5))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range policyMatched.Rows {
		for _, cell := range row.Cells {
			if cell.TimestampSeconds == nil || cell.PictureType != "B" {
				t.Fatalf("coarse I prevented nearby B refinement: %+v", row)
			}
		}
	}
	automatic, err := plan.Clone()
	if err != nil {
		t.Fatal(err)
	}
	automatic.Rows = nil
	automatic, err = b.matchWorkflowComparisons(t.Context(), retained, automatic, b.comparisonProgress(t.Context(), 5))
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
	automatic, err = b.matchWorkflowComparisons(t.Context(), retained, automatic, b.comparisonProgress(t.Context(), 5))
	if err != nil || len(automatic.Rows) != 3 {
		t.Fatalf("automatic rows ignored changed selection preferences: rows=%d err=%v", len(automatic.Rows), err)
	}
	corrected, err := automatic.Clone()
	if err != nil {
		t.Fatal(err)
	}
	corrected.Preferences.DarkCount, corrected.Preferences.MotionCount = new(2), new(2)
	corrected.Rows[0].Cells[0].TimestampSeconds, corrected.Rows[0].Cells[0].Confidence = new(1.0), nil
	corrected, err = b.matchWorkflowComparisons(t.Context(), retained, corrected, b.comparisonProgress(t.Context(), 5))
	if err != nil || len(corrected.Rows) != 1 || corrected.SceneMix == nil || corrected.SceneMix.RequestedDark != 2 ||
		corrected.SceneMix.RequestedMotion != 2 || len(corrected.Warnings) == 0 {
		t.Fatalf("corrected main moment could not rematch with unmet scene targets: plan=%+v err=%v", corrected, err)
	}
	if cell := corrected.Rows[0].Cells[0]; cell.TimestampSeconds == nil || math.Abs(*cell.TimestampSeconds-1) > 1e-7 || cell.Confidence != nil {
		t.Fatalf("rematching changed the manual main moment: %+v", cell)
	}
	snapshot := api.MediaArtifactSet{}
	captured, err := b.captureWorkflowComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, &snapshot, &retained, matched, b.comparisonProgress(t.Context(), 1), false)
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
	t.Run("old captions require recapture without losing decoded identity", func(t *testing.T) {
		old := cloneWorkflowMediaPrivateArtifacts(retained)
		cell := captured.Rows[0].Cells[0]
		delete(old.ComparisonCaptions, cell.ArtifactID)
		if err := verifyWorkflowComparisons(t.Context(), snapshot, old); err == nil {
			t.Fatal("old caption pixels retained upload authority")
		}
		updated, err := captured.Clone()
		if err != nil {
			t.Fatal(err)
		}
		invalidateOutdatedComparisonCaptions(&updated, old)
		current := updated.Rows[0].Cells[0]
		if current.ArtifactID != "" || current.ContentSHA256 != "" || current.ObservedPTS == nil || *current.ObservedPTS != *cell.ObservedPTS ||
			updated.Rows[0].Cells[1].ArtifactID != captured.Rows[0].Cells[1].ArtifactID {
			t.Fatal("caption invalidation lost the decoded frame or discarded an unchanged image")
		}
	})
	plainPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	overlaySnapshot, err := snapshot.Clone()
	if err != nil {
		t.Fatal(err)
	}
	overlaySnapshot.Comparisons.Sources[0].Overlay = false
	overlayPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, overlaySnapshot, retained, "main", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plainPreview.Bytes, overlayPreview.Bytes) || *plainPreview.ObservedPTS != *overlayPreview.ObservedPTS {
		t.Error("a legacy overlay opt-out disabled the required comparison label or changed frame")
	}
	t.Run("render settings bind captures previews and retained review", func(t *testing.T) {
		request, err := captured.Clone()
		if err != nil {
			t.Fatal(err)
		}
		label := request.Sources[0].Label
		request.Sources[0].Label = ""
		if _, err := comparisonCaptureRequest(request, retained, "main", 1); err == nil {
			t.Fatal("an empty video label bypassed the mandatory comparison overlay")
		}
		request.Sources[0].Label = label
		legacy, err := snapshot.Clone()
		if err != nil {
			t.Fatal(err)
		}
		legacy.Comparisons.Sources[0].Overlay = false
		upgraded, err := legacy.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		upgraded.Sources[0].Overlay = true
		retainComparisonCellFacts(&upgraded, &legacy)
		for r, row := range upgraded.Rows {
			cell := row.Cells[0]
			if cell.ArtifactID != "" || cell.ContentSHA256 != "" || cell.ObservedPTS == nil || *cell.ObservedPTS != *captured.Rows[r].Cells[0].ObservedPTS {
				t.Fatal("mandatory overlay migration discarded saved decoded frame identity")
			}
		}
		request.SizeReferenceSourceID = "other"
		request.Sources[0].ToneMap = new(false)
		// Public dimensions cannot choose the rendered size instead of private facts.
		request.Sources[1].Width, request.Sources[1].Height = 1, 1
		invalidated, err := request.Clone()
		if err != nil {
			t.Fatal(err)
		}
		retainComparisonCellFacts(&invalidated, &snapshot)
		for r, row := range invalidated.Rows {
			for c, cell := range row.Cells {
				if cell.ArtifactID != "" || cell.ContentSHA256 != "" || cell.ObservedPTS == nil || *cell.ObservedPTS != *captured.Rows[r].Cells[c].ObservedPTS {
					t.Fatal("render-setting invalidation retained pixels or discarded decoded identity")
				}
			}
		}
		updated, private, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained,
			api.ComparisonInstructions{Action: "capture", Plan: request}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if updated.Comparisons.ApprovedFingerprint != "" || updated.Comparisons.Fingerprint == captured.Fingerprint {
			t.Fatal("render setting changes retained approval")
		}
		kept, ok := private.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("comparison private resource has the wrong type")
		}
		for r, row := range updated.Comparisons.Rows {
			for c, cell := range row.Cells {
				image := kept.ArtifactImages[cell.ArtifactID]
				previous := captured.Rows[r].Cells[c]
				if image.Width != 192 || image.Height != 128 || cell.ObservedPTS == nil || *cell.ObservedPTS != *previous.ObservedPTS {
					t.Fatalf("render did not use private reference size or changed source frame: size=%dx%d cell=%+v", image.Width, image.Height, cell)
				}
			}
		}
		preview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, updated, private, "main", 1, 0)
		if err != nil || preview.Width != 192 || preview.Height != 128 || *preview.ObservedPTS != *plainPreview.ObservedPTS {
			t.Fatalf("preview did not share capture choices: size=%dx%d err=%v", preview.Width, preview.Height, err)
		}
		previewBeforeRestart := preview
		_, payload, err := kept.MarshalPrivateResource()
		if err != nil {
			t.Fatal(err)
		}
		restored, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := updated.Clone()
		if err != nil {
			t.Fatal(err)
		}
		preview, err = b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, wire, restored, "main", 1, 0)
		if err != nil || preview.Width != 192 || preview.Height != 128 || wire.Comparisons.SizeReferenceSourceID != "other" ||
			wire.Comparisons.Sources[0].ToneMap == nil || *wire.Comparisons.Sources[0].ToneMap || !bytes.Equal(preview.Bytes, previewBeforeRestart.Bytes) {
			t.Fatalf("restart lost comparison render settings: %v", err)
		}
		request.SizeReferenceSourceID = "removed"
		if _, err := comparisonCaptureRequest(request, retained, "main", 1); err == nil {
			t.Fatal("missing size-reference source accepted")
		}
	})
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
	var updates []api.WorkflowProgressUpdate
	progressCtx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) { updates = append(updates, update) })
	replaced, replacedPrivate, err := b.EditComparisons(progressCtx, api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &fresh, private,
		api.ComparisonInstructions{
			Action:       "match",
			Plan:         *fresh.Comparisons,
			VideoSources: []api.ComparisonVideoSelection{{ID: "other", Path: mainPath}},
		}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	windowStates := map[string]api.StageStatus{}
	previous := 0
	phases := map[string]bool{}
	captureCount := 0
	for _, update := range updates {
		if update.ItemOnly {
			if update.Kind == "comparison_window" {
				windowStates[update.ItemID] = update.Status
			}
			if update.Kind == "comparison_source" && update.Completed > 0 && !strings.Contains(update.Message, "%") {
				t.Fatalf("source progress is not visible in the workflow item: %+v", update)
			}
			if update.Kind == "comparison_cell" && update.Status == api.StageStatusCompleted {
				captureCount++
			}
			continue
		}
		if update.Total != 5000 || update.Completed < previous || update.Completed == 5000 && captureCount < 4 {
			t.Fatalf("comparison progress finished early or regressed: update=%+v captures=%d previous=%d", update, captureCount, previous)
		}
		previous = update.Completed
		phases[update.Phase] = true
	}
	for id, status := range windowStates {
		if status != api.StageStatusCompleted {
			t.Fatalf("finished comparison window remains active: %s status=%s", id, status)
		}
	}
	if previous != 5000 || len(phases) != 5 || captureCount != 4 {
		t.Fatalf("incomplete comparison progress: phases=%v captures=%d completed=%d", phases, captureCount, previous)
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
	anchored, err := b.matchWorkflowComparisons(t.Context(), retained, plan, b.comparisonProgress(t.Context(), 5))
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
	three, err := captured.Clone()
	if err != nil {
		t.Fatal(err)
	}
	three.Sources = append(three.Sources, api.ComparisonSource{ID: "extra", Label: "Extra"})
	extra, extraPrivate, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained,
		api.ComparisonInstructions{
			Action:       "configure",
			Plan:         three,
			VideoSources: []api.ComparisonVideoSelection{{ID: "extra", Path: secondPath}},
		}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reduced, err := extra.Comparisons.Clone()
	if err != nil {
		t.Fatal(err)
	}
	reduced.Sources = reduced.Sources[:2]
	for _, action := range []string{"match", "capture"} {
		_, nextPrivate, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &extra, extraPrivate,
			api.ComparisonInstructions{Action: action, Plan: reduced}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := nextPrivate.(workflowMediaPrivateArtifacts).ComparisonSources["extra"]; exists {
			t.Errorf("%s retained a removed column handle", action)
		}
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
