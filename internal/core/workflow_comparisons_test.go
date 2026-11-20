// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

func (r comparisonPictureTypeRenderer) SampleComparisonSource(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	frames, err := r.comparisonFrameRenderer.SampleComparisonSource(ctx, request)
	fixtureComparisonPictureTypes(frames, request)
	if err != nil {
		return nil, fmt.Errorf("sample comparison fixture: %w", err)
	}
	return frames, nil
}

func (r comparisonPictureTypeRenderer) AnalyzeComparisonSource(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	frames, err := r.comparisonFrameRenderer.AnalyzeComparisonSource(ctx, request)
	fixtureComparisonPictureTypes(frames, request)
	if err != nil {
		return nil, fmt.Errorf("analyze comparison fixture: %w", err)
	}
	return frames, nil
}

func fixtureComparisonPictureTypes(frames []screenshots.ComparisonSample, request screenshots.ComparisonSampleRequest) {
	for i := range frames {
		frames[i].PictureType = "B"
		if request.Source.StartTimeSeconds < 1 && math.Abs(frames[i].RelativeTimestampSeconds*2-math.Round(frames[i].RelativeTimestampSeconds*2)) < 1e-6 {
			frames[i].PictureType = "I"
		}
	}
}

func TestComparisonApprovalBindsRetainedPixelsAcrossRestart(t *testing.T) {
	media, retained := testComparisonDescriptionMedia(t, false)
	media.Comparisons.ApprovedFingerprint = ""
	b := workflowMediaBuilder{media: &mediaModule{}}
	if err := verifyWorkflowComparisons(t.Context(), media, retained, nil); err == nil {
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
	if err := verifyWorkflowComparisons(t.Context(), updated, restored, nil); err != nil {
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

func TestComparisonRestoreKeepsCompatiblePublishedLinkWithoutLocalScreenshot(t *testing.T) {
	t.Parallel()

	media, retained := testComparisonDescriptionMedia(t, false)
	media.WorkflowID = "workflow"
	media.Release = api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	media.ReleaseRef = api.ReleaseRef{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.mkv"), Generation: 1}
	media.AutomaticScreenshotSelection = true
	media.FailedHosts = []string{"imgbox"}
	projections := api.TrackerReleaseProjectionSet{WorkflowID: media.WorkflowID, Release: media.Release}
	b := workflowMediaBuilder{media: &mediaModule{}}
	scope, err := workflowMediaHostAccountScope(b.config, "pixhost")
	if err != nil {
		t.Fatal(err)
	}
	const id = "hosted-ordinary"
	const source = "missing-ordinary"
	const url = "https://images.invalid/screenshot.png"
	media.Artifacts = append(media.Artifacts, api.MediaArtifact{
		ID:                id,
		Kind:              api.MediaArtifactHostedImage,
		Purpose:           api.ScreenshotPurposeFinal,
		Selected:          true,
		Source:            source,
		SourceUnavailable: true,
		Host:              "pixhost",
		URL:               url,
	})
	retained.HostedSources[id] = source
	retained.HostedImages[id] = api.UploadedImageLink{
		Host:         "pixhost",
		AccountScope: scope,
		ImgURL:       url,
	}
	retained.imageHostingScope, err = workflowMediaHostAccountScope(b.config, "")
	if err != nil {
		t.Fatal(err)
	}
	_, payload, err := retained.MarshalPrivateResource()
	if err != nil {
		t.Fatal(err)
	}
	retained, err = decodeWorkflowMediaPrivateArtifacts(b, payload)
	if err != nil {
		t.Fatal(err)
	}
	restored, value, err := b.RestoreComparisons(t.Context(), media.ReleaseRef, projections, media, retained, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(restored.Artifacts, func(artifact api.MediaArtifact) bool { return artifact.ID == id })
	restoredPrivate, ok := value.(workflowMediaPrivateArtifacts)
	if !ok || index < 0 || restored.Artifacts[index].URL != url || !restored.Artifacts[index].SourceUnavailable ||
		restored.ImageRequirementsPrepared || !restored.AutomaticScreenshotSelection || restoredPrivate.HostedSources[id] != source {
		t.Fatalf("restoration lost the published URL or treated it as a local screenshot: %#v", restored.Artifacts)
	}
	if !slices.Equal(restored.FailedHosts, media.FailedHosts) {
		t.Fatalf("restoration made a failed host eligible for automatic retry: %#v", restored.FailedHosts)
	}

	binding := api.PreparedMediaBinding{
		SourcePath:               media.ReleaseRef.SourcePath,
		PreparedMediaFingerprint: "prepared-media",
		PreparedGeneration:       media.ReleaseRef.Generation,
		CompatibilityKey:         api.MediaCompatibilityKey(workflowMediaTestSHA256("verified source")),
	}
	missing := api.ScreenshotImage{
		Path:    filepath.Join(t.TempDir(), "missing.png"),
		Purpose: api.ScreenshotPurposeFinal,
	}
	fingerprint, err := workflowMediaReuseCaptureFingerprint(b.config, binding.CompatibilityKey, api.MediaArtifactScreenshot, missing)
	if err != nil {
		t.Fatal(err)
	}
	link := retained.HostedImages[id]
	link.ImagePath = missing.Path
	retained.HostedImages[id] = link
	retained.ArtifactImages[source] = missing
	// The prior URL was selected even though the local screenshot was not.
	media.Artifacts[len(media.Artifacts)-1].SourceUnavailable = false
	media.Artifacts = append(media.Artifacts, api.MediaArtifact{
		ID:      source,
		Kind:    api.MediaArtifactScreenshot,
		Purpose: api.ScreenshotPurposeFinal,
	})
	repo := &reusableWorkflowMediaRepository{assets: []api.ReusableMediaAsset{{
		Binding:            binding,
		CompatibilityKey:   binding.CompatibilityKey,
		CaptureFingerprint: fingerprint,
		ContentSHA256:      workflowMediaTestSHA256("missing screenshot bytes"),
		Kind:               api.MediaArtifactScreenshot,
		Image:              missing,
		HostedLinks:        []api.UploadedImageLink{link},
	}}}
	b.resolver = workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{MediaBinding: binding}}
	b.media.repo = repo
	b.media.mediaReuse = repo
	restored, _, err = b.RestoreComparisons(t.Context(), media.ReleaseRef, projections, media, retained, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	index = slices.IndexFunc(restored.Artifacts, func(artifact api.MediaArtifact) bool { return artifact.ID == id })
	if index < 0 || !restored.Artifacts[index].Selected || !restored.Artifacts[index].SourceUnavailable ||
		restored.Artifacts[index].URL != url || countMediaArtifacts(restored.Artifacts, api.MediaArtifactScreenshot) != 0 {
		t.Fatalf("newly missing screenshot lost or promoted its published URL: %#v", restored.Artifacts)
	}
	b.config.ImageHosting.Host1 = "ptpimg"
	restored, _, err = b.RestoreComparisons(t.Context(), media.ReleaseRef, projections, media, retained, time.Now())
	if err != nil || len(restored.FailedHosts) != 0 ||
		slices.ContainsFunc(restored.Artifacts, func(artifact api.MediaArtifact) bool { return artifact.ID == id }) {
		t.Fatalf("restoration retained a link from a prior host scope: %#v, %v", restored.Artifacts, err)
	}
}

func TestComparisonInheritedToneMapChangeRetainsFramesForRecapture(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("native HDR comparison fixture requires ffmpeg")
	}
	root := t.TempDir()
	video := filepath.Join(root, "Example.HDR.mkv")
	executable, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "nullsrc=size=128x72:rate=25:duration=16", "-vf", "geq=lum='96+35*sin(X/9+N/9)+25*cos(Y/11)':cb=128:cr=128,zscale=primariesin=bt709:transferin=bt709:matrixin=bt709:rangein=limited:primaries=bt2020:transfer=smpte2084:matrix=bt2020nc:range=limited,format=yuv420p10le", "-c:v", "ffv1", video)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("author textured HDR fixture: %v %s", err, output)
	}
	encode := filepath.Join(root, "Example.HDR.Encode.mkv")
	contents, err := os.ReadFile(video)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(encode, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "app.sqlite")}}
	cfg.ScreenshotHandling.TonemapAlgorithm = "hable"
	b := workflowMediaBuilder{
		config:             cfg,
		media:              &mediaModule{},
		comparisonRenderer: screenshots.NewService(cfg, api.NopLogger{}, root, nil),
		resolver:           workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{VideoPath: video}},
	}
	release := api.ReleaseRef{SourcePath: video, Generation: 1}
	projections := api.TrackerReleaseProjectionSet{}
	plan := api.ComparisonPlan{
		MainSourceID: "main",
		Preferences:  api.ComparisonPreferences{Count: 1},
		Sources: []api.ComparisonSource{
			{ID: "main", Label: "Inherited"},
			{
				ID:      "other",
				Label:   "Explicit off",
				ToneMap: new(false),
			},
		},
		Rows: []api.ComparisonRow{{ID: "1", Cells: []api.ComparisonCell{
			{SourceID: "main", TimestampSeconds: new(2.0)},
			{SourceID: "other", TimestampSeconds: new(2.0)},
		}}},
	}
	configured, private, err := b.EditComparisons(t.Context(), release, projections, nil, nil,
		api.ComparisonInstructions{
			Action:       "configure",
			Plan:         plan,
			VideoSources: []api.ComparisonVideoSelection{{ID: "other", Path: encode}},
		}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	captured, private, err := b.EditComparisons(t.Context(), release, projections, &configured, private,
		api.ComparisonInstructions{Action: "capture", Plan: *configured.Comparisons}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	approved, private, err := b.EditComparisons(t.Context(), release, projections, &captured, private,
		api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: captured.Comparisons.Fingerprint}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _, err := b.RestoreComparisons(t.Context(), release, projections, approved, private, time.Now())
	if err != nil || unchanged.Comparisons.Fingerprint != approved.Comparisons.Fingerprint {
		t.Fatalf("unchanged inherited settings discarded the reviewed captures: %v", err)
	}
	capturedPrivate, ok := private.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatal("comparison capture returned an incompatible private resource")
	}
	_, payload, err := capturedPrivate.MarshalPrivateResource()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("renderer settings invalidate pixels across restart and direct capture", func(t *testing.T) {
		changed := b
		changed.config.ScreenshotHandling.OverlayTextSize = 32
		changed.comparisonRenderer = screenshots.NewService(changed.config, api.NopLogger{}, root, nil)
		restarted, err := decodeWorkflowMediaPrivateArtifacts(changed, payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyWorkflowComparisons(t.Context(), approved, restarted, &changed.config.ScreenshotHandling); err == nil {
			t.Fatal("changed renderer settings retained hosting authority")
		}
		descriptionBuilder := workflowDescriptionBuilder{config: cfg}
		if _, err := descriptionBuilder.resolveWorkflowDescriptionMedia(t.Context(), api.UploadSubject{}, restarted, approved); err != nil {
			t.Fatalf("compatible comparisons could not reach description preparation: %v", err)
		}
		descriptionBuilder.config = changed.config
		if _, err := descriptionBuilder.resolveWorkflowDescriptionMedia(t.Context(), api.UploadSubject{}, restarted, approved); err == nil ||
			!strings.Contains(err.Error(), "comparison rendering settings changed") {
			t.Fatalf("changed renderer settings retained description authority: %v", err)
		}
		if _, _, err := changed.EditComparisons(t.Context(), release, projections, &approved, restarted,
			api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: approved.Comparisons.Fingerprint}, nil, time.Now()); err == nil {
			t.Fatal("changed renderer settings permitted approval")
		}
		restored, _, err := changed.RestoreComparisons(t.Context(), release, projections, approved, restarted, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for c, cell := range restored.Comparisons.Rows[0].Cells {
			before := approved.Comparisons.Rows[0].Cells[c]
			if cell.ArtifactID != "" || cell.ContentSHA256 != "" || cell.ObservedIdentity == nil || *cell.ObservedIdentity != *before.ObservedIdentity {
				t.Fatal("renderer settings invalidation retained pixels or lost native identity")
			}
		}
		// Exercise capture's own reuse boundary, without restoring first.
		media := approved
		capturePlan, err := approved.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		plan, err := changed.captureWorkflowComparisons(t.Context(), release, &media, &restarted, capturePlan, changed.comparisonProgress(t.Context(), 1), false)
		if err != nil {
			t.Fatal(err)
		}
		for c, cell := range plan.Rows[0].Cells {
			before := approved.Comparisons.Rows[0].Cells[c]
			if cell.ArtifactID == "" || cell.ContentSHA256 == before.ContentSHA256 || *cell.ObservedIdentity != *before.ObservedIdentity || restarted.ComparisonRenderSettings[cell.ArtifactID] == "" {
				t.Fatal("capture reused old renderer settings or changed native identity")
			}
		}
	})
	t.Run("restored canvas policy requires recapture", func(t *testing.T) {
		legacy := approved
		plan, err := approved.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		plan.SizeReferenceSourceID, plan.ResizeSmaller = "other", true
		legacy.Comparisons = &plan
		restored, _, err := b.RestoreComparisons(t.Context(), release, projections, legacy, private, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for c, cell := range restored.Comparisons.Rows[0].Cells {
			before := approved.Comparisons.Rows[0].Cells[c]
			if cell.ArtifactID != "" || cell.ContentSHA256 != "" || cell.ObservedIdentity == nil || *cell.ObservedIdentity != *before.ObservedIdentity {
				t.Fatal("canvas policy reset accepted old pixels or lost native identity")
			}
		}
	})
	b.config.ScreenshotHandling.ToneMap = true
	b.comparisonRenderer = screenshots.NewService(b.config, api.NopLogger{}, root, nil)
	restarted, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyWorkflowComparisons(t.Context(), approved, restarted, &b.config.ScreenshotHandling); err == nil {
		t.Fatal("changed inherited setting retained hosting authority after restart")
	}
	if _, _, err := b.EditComparisons(t.Context(), release, projections, &approved, restarted,
		api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: approved.Comparisons.Fingerprint}, nil, time.Now()); err == nil {
		t.Fatal("stale inherited pixels could be approved again")
	}
	restored, private, err := b.RestoreComparisons(t.Context(), release, projections, approved, restarted, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before := approved.Comparisons.Rows[0].Cells
	after := restored.Comparisons.Rows[0].Cells
	if after[0].ArtifactID != "" || after[0].ContentSHA256 != "" || after[0].ObservedPTS == nil ||
		*after[0].ObservedPTS != *before[0].ObservedPTS || after[0].PictureType != before[0].PictureType || after[1].ArtifactID != before[1].ArtifactID {
		t.Fatal("restoration kept stale pixels, lost decoded evidence or invalidated the explicit override")
	}
	configured, private, err = b.EditComparisons(t.Context(), release, projections, &restored, private,
		api.ComparisonInstructions{Action: "configure", Plan: *restored.Comparisons}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	recaptured, private, err := b.EditComparisons(t.Context(), release, projections, &configured, private,
		api.ComparisonInstructions{Action: "capture", Plan: *configured.Comparisons}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	after = recaptured.Comparisons.Rows[0].Cells
	recapturedPrivate, ok := private.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatal("comparison recapture returned an incompatible private resource")
	}
	images := recapturedPrivate.ArtifactImages
	if after[0].ArtifactID == "" || after[0].ContentSHA256 == before[0].ContentSHA256 || *after[0].ObservedPTS != *before[0].ObservedPTS ||
		!images[after[0].ArtifactID].Processing.Tonemapped || after[1].ArtifactID != before[1].ArtifactID || recaptured.Comparisons.Sources[0].ToneMap != nil ||
		recaptured.Comparisons.ApprovedFingerprint != "" {
		t.Fatal("capture skipped invalidated pixels, changed the selected frame or overwrote the inherited/explicit choices")
	}
	preview, err := b.PreviewComparisonFrame(t.Context(), release, recaptured, private, "main", *after[0].TimestampSeconds, 0, nil)
	if err != nil || !preview.Processing.Tonemapped || *preview.ObservedPTS != *after[0].ObservedPTS {
		t.Fatalf("recaptured pixels and current preview disagree: %v", err)
	}
	t.Run("saved approval rejects same-size edit beyond content sample", func(t *testing.T) {
		reloaded, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyWorkflowComparisons(t.Context(), approved, reloaded, &cfg.ScreenshotHandling); err != nil {
			t.Fatalf("unchanged approved comparison lost authority after reload: %v", err)
		}
		before, err := resolveComparisonFileVideo(t.Context(), encode, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) <= int(api.SourceContentIdentitySampleBytes) {
			t.Fatal("fixture must contain a byte beyond the sample")
		}
		contents[len(contents)-1] ^= 1
		if err := os.WriteFile(encode, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := before.file.ModTime().Add(2 * time.Second)
		if err := os.Chtimes(encode, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		changed, err := resolveComparisonFileVideo(t.Context(), encode, nil)
		if err != nil || before.legacyFingerprint != changed.legacyFingerprint {
			t.Fatalf("fixture changed the prefix fingerprint: %v", err)
		}
		if err := verifyWorkflowComparisons(t.Context(), approved, reloaded, &cfg.ScreenshotHandling); !errors.Is(err, api.ErrActiveInputChanged) {
			t.Fatalf("reloaded approval accepted changed secondary video: %v", err)
		}
	})
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
	command := exec.CommandContext(t.Context(), executable, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=96x64:rate=10:duration=12", "-vf", "setpts=PTS+5/TB", "-c:v", "ffv1", mainPath)
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
			requested := plan
			if legacy {
				prior = &api.MediaArtifactSet{Comparisons: &plan}
			} else {
				requested.Preferences.Count = 0
				requested.Preferences.DarkCount = new(1)
			}
			configured, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, prior, retained,
				api.ComparisonInstructions{
					Action:       "configure",
					Plan:         requested,
					VideoSources: []api.ComparisonVideoSelection{{ID: "other", Path: secondPath}},
				}, nil, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !legacy && configured.Comparisons.Preferences.Count != 4 {
				t.Fatal("default row count was not retained with the scene target")
			}
			for _, source := range configured.Comparisons.Sources {
				if !source.Overlay || source.ToneMap != nil || source.Capabilities == nil || source.Capabilities.ProgrammeSelectionRequired || source.Capabilities.ToneMappingAvailable {
					t.Fatalf("legacy=%t video defaults changed intent: %+v", legacy, source)
				}
			}
		}
	})
	matched, err := b.matchWorkflowComparisons(t.Context(), &retained, plan, b.comparisonProgress(t.Context(), 5))
	if err != nil {
		t.Fatal(err)
	}
	matchedPrivate := cloneWorkflowMediaPrivateArtifacts(retained)
	t.Run("both applied preview handles retain their correspondence after row renumbering", func(t *testing.T) {
		private := cloneWorkflowMediaPrivateArtifacts(matchedPrivate)
		request, err := plan.Clone()
		if err != nil {
			t.Fatal(err)
		}
		request.Rows = []api.ComparisonRow{{ID: "row-explicit", Cells: []api.ComparisonCell{{SourceID: "main", TimestampSeconds: new(1.0)}, {SourceID: "other", TimestampSeconds: new(1.2)}}}}
		request.Anchors = []api.ComparisonAnchor{{
			SourceID:      "other",
			MainSeconds:   1,
			SourceSeconds: 1.2,
		}}
		snapshot := api.MediaArtifactSet{Comparisons: &request}
		selections := make([]api.ComparisonFrameSelection, 0, len(request.Rows[0].Cells))
		previews := make([]releaseworkflow.MediaPreviewContent, 0, len(request.Rows[0].Cells))
		for _, cell := range request.Rows[0].Cells {
			preview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, private, cell.SourceID, *cell.TimestampSeconds, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			previews = append(previews, preview)
			selections = append(selections, api.ComparisonFrameSelection{RowID: "row-explicit", SourceID: cell.SourceID})
		}
		configured, resource, err := b.ApplyComparisonFrames(t.Context(), snapshot, private, selections, previews)
		if err != nil {
			t.Fatal(err)
		}
		var ok bool
		private, ok = resource.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("missing applied comparison sources")
		}
		result := *configured.Comparisons
		for range 2 {
			result, err = b.matchWorkflowComparisons(t.Context(), &private, result, b.comparisonProgress(t.Context(), 5))
			if err != nil {
				t.Fatal(err)
			}
			cell := result.Rows[0].Cells[1]
			if cell.ObservedIdentity == nil || *cell.ObservedIdentity != *previews[1].ObservedIdentity || cell.Confidence != nil {
				t.Fatalf("explicit native pair lost after matching: %+v", cell)
			}
		}
		// A capture between the timestamp edit and applying just the main
		// preview cannot attach the old secondary pin to a different main.
		release := api.ReleaseRef{SourcePath: mainPath, Generation: 1}
		prior := api.MediaArtifactSet{Comparisons: &result}
		edit, err := result.Clone()
		if err != nil {
			t.Fatal(err)
		}
		edit.Rows[0].Cells[0].TimestampSeconds = new(1.3)
		edit.Anchors = []api.ComparisonAnchor{{
			SourceID:      "other",
			MainSeconds:   1.3,
			SourceSeconds: 1.2,
		}}
		edited, resource, err := b.EditComparisons(t.Context(), release, api.TrackerReleaseProjectionSet{}, &prior, private, api.ComparisonInstructions{Action: "configure", Plan: edit}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		captured, resource, err := b.EditComparisons(t.Context(), release, api.TrackerReleaseProjectionSet{}, &edited, resource, api.ComparisonInstructions{Action: "capture", Plan: *edited.Comparisons}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		preview, err := b.PreviewComparisonFrame(t.Context(), release, captured, resource, "main", 1.3, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		selected, resource, err := b.ApplyComparisonFrames(t.Context(), captured, resource, []api.ComparisonFrameSelection{{RowID: captured.Comparisons.Rows[0].ID, SourceID: "main"}}, []releaseworkflow.MediaPreviewContent{preview})
		if err != nil {
			t.Fatal(err)
		}
		oldImage := captured.Comparisons.Rows[0].Cells[0]
		kept := selected.Comparisons.Rows[0].Cells[0]
		if oldImage.ArtifactID == "" || kept.ArtifactID != oldImage.ArtifactID || kept.ContentSHA256 != oldImage.ContentSHA256 {
			t.Fatal("applying the displayed main preview discarded its compatible captured PNG")
		}
		private, ok = resource.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("missing comparison sources")
		}
		result, err = b.matchWorkflowComparisons(t.Context(), &private, *selected.Comparisons, b.comparisonProgress(t.Context(), 5))
		if err != nil {
			t.Fatal(err)
		}
		other := result.Rows[0].Cells[1]
		if other.Confidence == nil || other.TimestampSeconds == nil || *other.TimestampSeconds != 1.3 {
			t.Fatalf("a captured main inherited the old secondary correspondence: %+v", other)
		}
	})
	t.Run("manual time between native pictures", func(t *testing.T) {
		retained := cloneWorkflowMediaPrivateArtifacts(matchedPrivate)
		requested, err := plan.Clone()
		if err != nil {
			t.Fatal(err)
		}
		requested.Rows = requested.Rows[:1]
		requested.Rows[0].Cells[0].TimestampSeconds = new(1.05)
		resolved, err := b.matchWorkflowComparisons(t.Context(), &retained, requested, b.comparisonProgress(t.Context(), 5))
		if err != nil {
			t.Fatal(err)
		}
		cell := resolved.Rows[0].Cells[0]
		if cell.TimestampSeconds == nil || math.Abs(*cell.TimestampSeconds-1.1) > 1e-6 || cell.ObservedIdentity == nil || cell.ObservedPTS == nil ||
			cell.ObservedIdentity.PTS != *cell.ObservedPTS || cell.Confidence != nil {
			t.Fatalf("manual time did not retain the inspected native picture: %+v", cell)
		}
		requested.Anchors = []api.ComparisonAnchor{{
			SourceID:      "other",
			MainSeconds:   1.1,
			SourceSeconds: 1.2,
		}}
		anchored, err := b.matchWorkflowComparisons(t.Context(), &retained, requested, b.comparisonProgress(t.Context(), 5))
		if err != nil || len(anchored.Rows) != 1 {
			t.Fatalf("native anchor duplicated the requested manual moment: rows=%d err=%v", len(anchored.Rows), err)
		}
		other := anchored.Rows[0].Cells[1]
		if other.TimestampSeconds == nil || *other.TimestampSeconds != 1.1 || other.Confidence == nil {
			t.Fatalf("manual time ignored its inspected native correspondence: %+v", other)
		}
		requested.Preferences.SelectionWindow = &api.ComparisonSelectionWindow{
			Excluded: []api.ComparisonTimeInterval{{Start: 1.075, End: 1.125}},
		}
		_, err = b.matchWorkflowComparisons(t.Context(), &retained, requested, b.comparisonProgress(t.Context(), 5))
		failure, ok := api.AsOperationFailure(err)
		if !ok || failure.ComparisonReason != "protected_time" {
			t.Fatalf("manual time bypassed the exclusion of its actual native picture: %v", err)
		}
	})
	for _, stopAfter := range []int{1, 2} {
		t.Run(fmt.Sprintf("manual matching cancellation after row %d retains native work", stopAfter), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			decoding := make(map[string]bool)
			ctx = api.WithWorkflowProgressReporter(ctx, func(update api.WorkflowProgressUpdate) {
				if update.Phase == "comparisons_manual_matching" && update.Kind == "comparison_window" && update.Status == api.StageStatusRunning {
					decoding[update.Label] = true
				}
				if update.Phase == "comparisons_manual_matching" && !update.ItemOnly && update.Completed >= 250*stopAfter {
					cancel()
				}
			})
			prior := api.MediaArtifactSet{Comparisons: &plan}
			updated, resource, err := b.EditComparisons(ctx, api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &prior, retained,
				api.ComparisonInstructions{
					Action:            "match",
					Plan:              plan,
					CaptureAfterMatch: new(false),
				}, nil, time.Now())
			incomplete, ok := errors.AsType[*releaseworkflow.ComparisonIncompleteError](err)
			if !ok || incomplete.Reason != "match_cancelled" || updated.Comparisons == nil || len(updated.Comparisons.Rows) != stopAfter || updated.Comparisons.ApprovedFingerprint != "" {
				t.Fatalf("manual matching discarded completed native rows: %+v err=%v", updated.Comparisons, err)
			}
			for column := 1; column <= 2; column++ {
				if !decoding[fmt.Sprintf("Column %d, row %d", column, stopAfter)] {
					t.Fatalf("manual matching did not report the active row and column: %v", decoding)
				}
			}
			private, ok := resource.(workflowMediaPrivateArtifacts)
			if !ok {
				t.Fatal("manual matching discarded private replay")
			}
			if err := verifyComparisonCells(*updated.Comparisons, private, false, &cfg.ScreenshotHandling); err != nil {
				t.Fatal(err)
			}
			for r, row := range updated.Comparisons.Rows {
				for c, cell := range row.Cells {
					original := matched.Rows[r].Cells[c]
					if cell.ObservedIdentity == nil || original.ObservedIdentity == nil || *cell.ObservedIdentity != *original.ObservedIdentity {
						t.Fatal("manual interruption changed a completed native picture")
					}
				}
			}
		})
	}
	t.Run("refresh old counter metadata preserves correspondence", func(t *testing.T) {
		old := cloneWorkflowMediaPrivateArtifacts(retained)
		source := old.ComparisonSources["main"]
		source.Source.FrameCount, source.Source.VideoDurationSeconds = 0, 0
		old.ComparisonSources["main"] = source
		updated, resource, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{},
			&api.MediaArtifactSet{Comparisons: &matched}, old, api.ComparisonInstructions{Action: "configure", Plan: matched}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		refreshed, ok := resource.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("comparison metadata resource has wrong type")
		}
		facts := refreshed.ComparisonSources["main"].Source
		if facts.FrameCount != 120 || updated.Comparisons.Rows[0].Cells[0].ObservedPTS == nil || *updated.Comparisons.Rows[0].Cells[0].ObservedPTS != *matched.Rows[0].Cells[0].ObservedPTS {
			t.Fatal("metadata refresh lost counts or existing correspondence")
		}
	})
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
	policyPlan.Rows = nil
	for i := range policyPlan.Sources {
		policyPlan.Sources[i].PreferBFrames = true
		policyPlan.Sources[i].AllowedFrameTypes = []string{"B"}
	}
	policyBuilder := b
	policyBuilder.comparisonRenderer = comparisonPictureTypeRenderer{renderer}
	policyMatched, err := policyBuilder.matchWorkflowComparisons(t.Context(), &retained, policyPlan, b.comparisonProgress(t.Context(), 5))
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
	automatic, err = b.matchWorkflowComparisons(t.Context(), &retained, automatic, b.comparisonProgress(t.Context(), 5))
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
	automatic, err = b.matchWorkflowComparisons(t.Context(), &retained, automatic, b.comparisonProgress(t.Context(), 5))
	if err != nil || automatic.Preferences.Count != 3 || len(automatic.Rows) < 1 || len(automatic.Rows) > 3 || len(automatic.Rows) < 3 && len(automatic.Warnings) == 0 {
		t.Fatalf("automatic rows ignored changed selection preferences: rows=%d err=%v", len(automatic.Rows), err)
	}
	corrected, err := automatic.Clone()
	if err != nil {
		t.Fatal(err)
	}
	corrected.Preferences.DarkCount, corrected.Preferences.MotionCount = new(2), new(2)
	corrected.Rows[0].Cells[0].TimestampSeconds, corrected.Rows[0].Cells[0].Confidence = new(1.0), nil
	corrected, err = b.matchWorkflowComparisons(t.Context(), &retained, corrected, b.comparisonProgress(t.Context(), 5))
	if err != nil || len(corrected.Rows) != 1 || corrected.SceneMix == nil || corrected.SceneMix.RequestedDark != 2 ||
		corrected.SceneMix.RequestedMotion != 2 || len(corrected.Warnings) == 0 {
		t.Fatalf("corrected main moment could not rematch with unmet scene targets: plan=%+v err=%v", corrected, err)
	}
	if cell := corrected.Rows[0].Cells[0]; cell.TimestampSeconds == nil || math.Abs(*cell.TimestampSeconds-1) > 1e-7 || cell.Confidence != nil {
		t.Fatalf("rematching changed the manual main moment: %+v", cell)
	}
	retained = matchedPrivate
	snapshot := api.MediaArtifactSet{}
	parallel := b
	parallel.config.ScreenshotHandling.FFmpegLimit = true
	parallel.config.ScreenshotHandling.ProcessLimit = 2
	concurrent := &comparisonConcurrentCaptureRenderer{comparisonFrameRenderer: renderer, gate: make(chan struct{})}
	parallel.comparisonRenderer = concurrent
	captured, err := parallel.captureWorkflowComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, &snapshot, &retained, matched, b.comparisonProgress(t.Context(), 1), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Rows) != 2 {
		t.Fatalf("rows=%d", len(captured.Rows))
	}
	if concurrent.peak != 2 || concurrent.interleaved {
		t.Fatal("capture did not join concurrent rows before advancing inputs")
	}
	for r, row := range captured.Rows {
		for c, cell := range row.Cells {
			if snapshot.Artifacts[r*len(row.Cells)+c].ID != cell.ArtifactID {
				t.Fatal("worker completion reordered public comparison artifacts")
			}
		}
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
	if err := verifyWorkflowComparisons(t.Context(), snapshot, retained, nil); err != nil {
		t.Fatal(err)
	}
	t.Run("old captions require recapture without losing decoded identity", func(t *testing.T) {
		old := cloneWorkflowMediaPrivateArtifacts(retained)
		cell := captured.Rows[0].Cells[0]
		delete(old.ComparisonCaptions, cell.ArtifactID)
		if err := verifyWorkflowComparisons(t.Context(), snapshot, old, nil); err == nil {
			t.Fatal("old caption pixels retained upload authority")
		}
		updated, err := captured.Clone()
		if err != nil {
			t.Fatal(err)
		}
		invalidateOutdatedComparisonRendering(&updated, old, b.config.ScreenshotHandling)
		current := updated.Rows[0].Cells[0]
		if current.ArtifactID != "" || current.ContentSHA256 != "" || current.ObservedPTS == nil || *current.ObservedPTS != *cell.ObservedPTS ||
			updated.Rows[0].Cells[1].ArtifactID != captured.Rows[0].Cells[1].ArtifactID {
			t.Fatal("caption invalidation lost the decoded frame or discarded an unchanged image")
		}
	})
	plainPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	overlaySnapshot, err := snapshot.Clone()
	if err != nil {
		t.Fatal(err)
	}
	overlaySnapshot.Comparisons.Sources[0].Overlay = false
	overlayPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, overlaySnapshot, retained, "main", 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plainPreview.Bytes, overlayPreview.Bytes) || *plainPreview.ObservedPTS != *overlayPreview.ObservedPTS {
		t.Error("a legacy overlay opt-out disabled the required comparison label or changed frame")
	}
	t.Run("interrupted capture retains completed cells and selected witnesses across restart", func(t *testing.T) {
		pending, err := captured.Clone()
		if err != nil {
			t.Fatal(err)
		}
		for r := range pending.Rows {
			for c := range pending.Rows[r].Cells {
				pending.Rows[r].Cells[c].ArtifactID = ""
				pending.Rows[r].Cells[c].ContentSHA256 = ""
			}
		}
		stopped := b
		stopped.config.ScreenshotHandling.FFmpegLimit = true
		stopped.config.ScreenshotHandling.ProcessLimit = 1
		stopped.comparisonRenderer = &comparisonCaptureStopRenderer{comparisonFrameRenderer: renderer}
		// A digest may already be referenced by an earlier approved snapshot.
		// Reusing it before a sibling fails must not unlink the shared image.
		fatal := errors.New("synthetic capture integrity failure")
		stopped.comparisonRenderer = &comparisonCaptureStopRenderer{comparisonFrameRenderer: renderer, terminal: errors.Join(context.Canceled, fatal)}
		oldImage := retained.ArtifactImages[captured.Rows[0].Cells[0].ArtifactID]
		before, err := os.ReadFile(oldImage.Path)
		if err != nil {
			t.Fatal(err)
		}
		rejected, rejectedPrivate, err := stopped.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained, api.ComparisonInstructions{Action: "capture", Plan: pending}, nil, time.Now())
		after, readErr := os.ReadFile(oldImage.Path)
		if !errors.Is(err, fatal) || rejectedPrivate != nil || rejected.Comparisons != nil || readErr != nil || !bytes.Equal(before, after) {
			t.Fatalf("fatal batch published a partial snapshot or lost a shared image: %v / %v", err, readErr)
		}
		stopped.comparisonRenderer = &comparisonCaptureStopRenderer{comparisonFrameRenderer: renderer}
		partial, resource, err := stopped.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained, api.ComparisonInstructions{Action: "capture", Plan: pending}, nil, time.Now())
		if _, ok := errors.AsType[*releaseworkflow.ComparisonIncompleteError](err); !ok || partial.Comparisons == nil {
			t.Fatalf("capture stop lost its typed retained outcome: %v", err)
		}
		if partial.Comparisons.Rows[0].Cells[0].ArtifactID == "" || partial.Comparisons.Rows[0].Cells[1].ArtifactID != "" || partial.Comparisons.Rows[0].Cells[1].ObservedIdentity == nil || partial.Comparisons.ApprovedFingerprint != "" {
			t.Fatal("capture stop lost a completed image, unmatched witness, or withheld review")
		}
		originalID := partial.Comparisons.Rows[0].Cells[0].ArtifactID
		private, ok := resource.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("missing private comparison resources")
		}
		_, payload, err := private.MarshalPrivateResource()
		if err != nil {
			t.Fatal(err)
		}
		hydrated, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
		if err != nil {
			t.Fatal(err)
		}
		restored, restoredPrivate, err := b.RestoreComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, partial, hydrated, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		resumed, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &restored, restoredPrivate, api.ComparisonInstructions{Action: "capture", Plan: *restored.Comparisons}, nil, time.Now())
		if err != nil || resumed.Comparisons.Rows[0].Cells[0].ArtifactID != originalID || len(comparisons.Validate(*resumed.Comparisons)) != 0 {
			t.Fatalf("restart failed to resume native captures: %v", err)
		}
	})
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
		request.Sources[1].Label = "Encode [invalid]"
		request.Sources[1].Imported, request.Sources[1].Overlay = true, false
		for _, action := range []string{"match", "capture"} {
			_, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, &snapshot, retained,
				api.ComparisonInstructions{Action: action, Plan: request}, nil, time.Now())
			failure, ok := api.AsOperationFailure(err)
			if !ok || failure.ComparisonReason != "invalid_column_label" || !strings.Contains(failure.Message, "Column 2") {
				t.Fatalf("%s did not reject the invalid label before video work: %v", action, err)
			}
		}
		request.Sources[1].Label = captured.Sources[1].Label
		request.Sources[1].Imported, request.Sources[1].Overlay = false, true
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
		if _, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, &snapshot, retained, api.ComparisonInstructions{Action: "configure", Plan: request}, nil, time.Now()); err == nil {
			t.Fatal("conflicting new canvas intent was accepted")
		}
		request.SizeReferenceSourceID = "main"
		request.Sources[0].Label += " Updated"
		request.Sources[1].Label += " Updated"

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
				if image.Width != 96 || image.Height != 64 || cell.ObservedPTS == nil || *cell.ObservedPTS != *previous.ObservedPTS {
					t.Fatalf("render did not use private reference size or changed source frame: size=%dx%d cell=%+v", image.Width, image.Height, cell)
				}
			}
		}
		preview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, updated, private, "main", 1, 0, nil)
		if err != nil || preview.Width != 96 || preview.Height != 64 || *preview.ObservedPTS != *plainPreview.ObservedPTS {
			t.Fatalf("preview did not share capture choices: size=%dx%d err=%v", preview.Width, preview.Height, err)
		}
		previewBeforeRestart := preview
		sourceSize := *updated.Comparisons
		sourceSize.ScreenshotSizing = api.ComparisonSizingSource
		release := api.ReleaseRef{SourcePath: mainPath, Generation: 1}
		sizingSnapshot, sizingPrivate, err := b.EditComparisons(t.Context(), release, api.TrackerReleaseProjectionSet{}, &updated, private,
			api.ComparisonInstructions{Action: "configure", Plan: sourceSize}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		cell := sizingSnapshot.Comparisons.Rows[0].Cells[1]
		if cell.ArtifactID != "" || cell.ObservedPTS == nil || *cell.ObservedPTS != *updated.Comparisons.Rows[0].Cells[1].ObservedPTS {
			t.Fatal("sizing-mode change retained stale pixels or discarded the matched frame")
		}
		sizingPreview, err := b.PreviewComparisonFrame(t.Context(), release, sizingSnapshot, sizingPrivate, "other", *cell.TimestampSeconds, 0, nil)
		if err != nil || sizingPreview.Width != 192 || sizingPreview.Height != 128 || *sizingPreview.ObservedPTS != *cell.ObservedPTS {
			t.Fatalf("source-size preview changed geometry or frame: %+v err=%v", sizingPreview, err)
		}
		sized, sizingPrivate, err := b.EditComparisons(t.Context(), release, api.TrackerReleaseProjectionSet{}, &sizingSnapshot, sizingPrivate,
			api.ComparisonInstructions{Action: "capture", Plan: *sizingSnapshot.Comparisons}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		sizedPrivate, ok := sizingPrivate.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("missing private comparison resources after recapture")
		}
		newCell := sized.Comparisons.Rows[0].Cells[1]
		image := sizedPrivate.ArtifactImages[newCell.ArtifactID]
		if image.Width != sizingPreview.Width || image.Height != sizingPreview.Height || *newCell.ObservedPTS != *cell.ObservedPTS {
			t.Fatalf("recapture changed preview geometry or frame: %dx%d %+v", image.Width, image.Height, newCell)
		}
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
		preview, err = b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, wire, restored, "main", 1, 0, nil)
		if err != nil || preview.Width != 96 || preview.Height != 64 || wire.Comparisons.SizeReferenceSourceID != "main" ||
			wire.Comparisons.Sources[0].ToneMap == nil || *wire.Comparisons.Sources[0].ToneMap || !bytes.Equal(preview.Bytes, previewBeforeRestart.Bytes) {
			t.Fatalf("restart lost comparison render settings: %v", err)
		}
		removed, _ := wire.Comparisons.Clone()
		removed.Sources = removed.Sources[:1]
		for r := range removed.Rows {
			removed.Rows[r].Cells = removed.Rows[r].Cells[:1]
		}
		reduced, reducedPrivate, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &wire, restored,
			api.ComparisonInstructions{Action: "configure", Plan: removed}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		readded, _ := reduced.Comparisons.Clone()
		readded.Sources = append(readded.Sources, request.Sources[1])
		for r := range readded.Rows {
			readded.Rows[r].Cells = append(readded.Rows[r].Cells, api.ComparisonCell{SourceID: "other", TimestampSeconds: readded.Rows[r].Cells[0].TimestampSeconds})
		}
		rebound, reboundPrivate, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &reduced, reducedPrivate,
			api.ComparisonInstructions{
				Action:       "configure",
				Plan:         readded,
				VideoSources: []api.ComparisonVideoSelection{{ID: "other", Path: mainPath}},
			}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for r, row := range rebound.Comparisons.Rows {
			cell := row.Cells[0]
			if cell.ArtifactID != "" || cell.ContentSHA256 != "" || cell.ObservedPTS == nil || *cell.ObservedPTS != *reduced.Comparisons.Rows[r].Cells[0].ObservedPTS {
				t.Fatal("re-added reference retained old pixels or lost dependent frame identity")
			}
		}
		resized, resizedPrivate, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &rebound, reboundPrivate,
			api.ComparisonInstructions{Action: "capture", Plan: *rebound.Comparisons}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		resizedArtifacts, ok := resizedPrivate.(workflowMediaPrivateArtifacts)
		if !ok {
			t.Fatal("resized comparison private resource has the wrong type")
		}
		for _, row := range resized.Comparisons.Rows {
			for _, cell := range row.Cells {
				image := resizedArtifacts.ArtifactImages[cell.ArtifactID]
				if image.Width != 96 || image.Height != 64 {
					t.Fatalf("re-added reference left a mixed canvas: %dx%d", image.Width, image.Height)
				}
			}
		}
		request.SizeReferenceSourceID = "removed"
		if _, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, &snapshot, retained, api.ComparisonInstructions{Action: "configure", Plan: request}, nil, time.Now()); err == nil {
			t.Fatal("a missing reference bypassed the main canvas policy")
		}
	})
	nextPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", plainPreview.TimestampSeconds, 1, &plainPreview)
	if err != nil {
		t.Fatal(err)
	}
	previousPreview, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", nextPreview.TimestampSeconds, -1, &nextPreview)
	if err != nil {
		t.Fatal(err)
	}
	if nextPreview.TimestampSeconds <= plainPreview.TimestampSeconds || *previousPreview.ObservedPTS != *plainPreview.ObservedPTS {
		t.Error("decoded frame stepping skipped or repeated its source clock")
	}
	cursor := (plainPreview.TimestampSeconds + nextPreview.TimestampSeconds) / 2
	for direction, want := range map[int]*int64{-1: plainPreview.ObservedPTS, 1: nextPreview.ObservedPTS} {
		stepped, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", cursor, direction, nil)
		if err != nil || stepped.ObservedPTS == nil || *stepped.ObservedPTS != *want {
			t.Fatalf("fresh step %d skipped its adjacent native picture: pts=%v err=%v", direction, stepped.ObservedPTS, err)
		}
	}
	typed, err := captured.Clone()
	if err != nil {
		t.Fatal(err)
	}
	typed.Sources[1].AllowedFrameTypes = []string{"B"}
	typeChanged, typePrivate, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained,
		api.ComparisonInstructions{Action: "capture", Plan: typed}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cell := typeChanged.Comparisons.Rows[0].Cells[1]; cell.PictureType != "I" || !strings.Contains(cell.Warning, "B") {
		t.Error("changed type preference silently reused an incompatible frame")
	}
	typeChanged.Comparisons.Rows[0].Cells[1].Warning = "Review this correspondence. " + typeChanged.Comparisons.Rows[0].Cells[1].Warning
	allowed, err := typeChanged.Comparisons.Clone()
	if err != nil {
		t.Fatal(err)
	}
	allowed.Sources[1].AllowedFrameTypes = []string{"I"}
	typeAllowed, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &typeChanged, typePrivate,
		api.ComparisonInstructions{Action: "capture", Plan: allowed}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cell := typeAllowed.Comparisons.Rows[0].Cells[1]; cell.PictureType != "I" || cell.ArtifactID == "" || cell.Warning != "Review this correspondence." {
		t.Errorf("recaptured allowed frame retained a stale type warning or lost its correspondence warning: %+v", cell)
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
	inspecting := false
	captureCtx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) {
		if update.Kind == "comparison_window" && update.Status == api.StageStatusRunning && update.Label == "Column 1, row 1" {
			inspecting = true
		}
	})
	fresh, private, err := b.EditComparisons(captureCtx, api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &snapshot, retained, api.ComparisonInstructions{Action: "capture", Plan: edited}, nil, time.Now())
	if err != nil {
		t.Fatalf("orphan recapture: %v; cause=%v", err, errors.Unwrap(err))
	}
	if !inspecting {
		t.Fatal("manual recapture did not report the row and column under inspection")
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
		if update.Total != 1000 || update.Completed < previous || update.Completed == 1000 && captureCount < 4 {
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
	if previous != 1000 || !phases["comparisons_manual_matching"] || !phases["comparisons_capture"] || captureCount != 4 {
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
	anchorPrivate := cloneWorkflowMediaPrivateArtifacts(retained)
	anchored, err := b.matchWorkflowComparisons(t.Context(), &anchorPrivate, plan, b.comparisonProgress(t.Context(), 5))
	if err != nil {
		t.Fatal(err)
	}
	if cell := anchored.Rows[0].Cells[1]; cell.TimestampSeconds == nil || *cell.TimestampSeconds != 1 || cell.Confidence == nil {
		t.Fatalf("timestamp-only hint bypassed native correspondence: %+v", cell)
	}
	t.Run("saved anchor without a manual row", func(t *testing.T) {
		requested, err := plan.Clone()
		if err != nil {
			t.Fatal(err)
		}
		requested.Rows[0].Cells[0].Confidence = new(.99)
		requested.Rows[0].Cells[0].TimestampSeconds = new(2.0)
		resolved, err := b.matchWorkflowComparisons(t.Context(), &anchorPrivate, requested, b.comparisonProgress(t.Context(), 5))
		if err != nil || len(resolved.Rows) != 1 {
			t.Fatalf("saved anchor was ignored: rows=%d err=%v", len(resolved.Rows), err)
		}
		main, other := resolved.Rows[0].Cells[0], resolved.Rows[0].Cells[1]
		if main.TimestampSeconds == nil || *main.TimestampSeconds != 1 || main.Confidence != nil ||
			other.TimestampSeconds == nil || *other.TimestampSeconds != 1 || other.Confidence == nil {
			t.Fatalf("saved correspondence lost its explicit moments: %+v", resolved.Rows[0])
		}
	})
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
	t.Run("capture checks source version after final progress", func(t *testing.T) {
		info, err := os.Stat(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chtimes(mainPath, info.ModTime(), info.ModTime()); err != nil {
				t.Error(err)
			}
		})
		changed := false
		ctx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) {
			if !update.ItemOnly && update.Phase == "comparisons_capture" && update.Completed == update.Total {
				modified := info.ModTime().Add(2 * time.Second)
				if err := os.Chtimes(mainPath, modified, modified); err != nil {
					t.Fatal(err)
				}
				changed = true
			}
		})
		_, _, err = b.EditComparisons(ctx, api.ReleaseRef{SourcePath: mainPath, Generation: 1}, api.TrackerReleaseProjectionSet{}, &fresh, private,
			api.ComparisonInstructions{Action: "capture", Plan: *fresh.Comparisons}, nil, time.Now())
		if !changed || !errors.Is(err, api.ErrActiveInputChanged) {
			t.Fatalf("capture published after late source mutation: changed=%t err=%v", changed, err)
		}
	})
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

func TestComparisonMainCanvasUsesPrivateDimensions(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		mainWidth, mainHeight, width, height int
	}{
		{"cropped source", 1920, 1080, 1920, 1040}, {"UHD source", 1920, 1080, 3840, 2160}, {"smaller source", 1920, 1080, 1280, 720}, {"portrait main", 720, 1280, 1920, 1080},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := api.ComparisonPlan{MainSourceID: "main", Sources: []api.ComparisonSource{{
				ID:     "main",
				Label:  "Main",
				Width:  1,
				Height: 1,
			}, {
				ID:     "other",
				Label:  "Other",
				Width:  1,
				Height: 1,
			}}}
			private := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{"main": {Source: screenshots.ComparisonSource{Width: tc.mainWidth, Height: tc.mainHeight}}, "other": {Source: screenshots.ComparisonSource{Width: tc.width, Height: tc.height}}}}
			request, err := comparisonCaptureRequest(plan, private, "other", 1)
			if err != nil || request.OutputWidth != tc.mainWidth || request.OutputHeight != tc.mainHeight || request.ResizeOnly || !request.ShowResolution {
				t.Fatalf("incorrect main canvas request: %+v %v", request, err)
			}
		})
	}
}

func TestComparisonRotatedSizingUsesDecodedGeometry(t *testing.T) {
	ffmpeg, err := screenshots.ResolveFFmpegExecutable()
	if err != nil {
		t.Skip("rotated comparison requires FFmpeg")
	}
	capabilities := func(args ...string) string {
		t.Helper()
		output, err := exec.CommandContext(t.Context(), ffmpeg, append([]string{"-hide_banner"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("read FFmpeg fixture capabilities: %v %s", err, output)
		}
		return string(output)
	}
	encoders := capabilities("-encoders")
	if !strings.Contains(capabilities("-h", "full"), "-display_rotation") || !strings.Contains(encoders, " mpeg4 ") {
		t.Skip("rotated comparison fixtures need FFmpeg display rotation and the MPEG-4 encoder")
	}
	root := t.TempDir()
	mainPath, rotatedPath := filepath.Join(root, "Main.mp4"), filepath.Join(root, "Rotated.mp4")
	run := func(args ...string) {
		t.Helper()
		if output, err := exec.CommandContext(t.Context(), ffmpeg, append([]string{"-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("author rotated comparison source: %v %s", err, output)
		}
	}
	run("-f", "lavfi", "-i", "testsrc2=size=320x180:rate=10:duration=2", "-c:v", "mpeg4", mainPath)
	run("-display_rotation", "90", "-i", mainPath, "-c", "copy", rotatedPath)
	renderer := screenshots.NewService(config.Config{}, api.NopLogger{}, root, nil)
	plan := api.ComparisonPlan{
		MainSourceID: "main",
		Sources: []api.ComparisonSource{{
			ID:      "main",
			Label:   "Source",
			Overlay: true,
		}, {
			ID:      "other",
			Label:   "Rotated",
			Overlay: true,
		}},
	}
	retained := workflowMediaPrivateArtifacts{ComparisonSources: make(map[string]workflowComparisonSource)}
	for id, path := range map[string]string{"main": mainPath, "other": rotatedPath} {
		source, err := renderer.InspectComparisonSource(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		source.InputFingerprint, err = comparisonVideoFingerprint(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		retained.ComparisonSources[id] = workflowComparisonSource{Source: source, Fingerprint: source.InputFingerprint}
	}
	for _, tc := range []struct {
		mainID        string
		sizing        string
		width, height int
	}{
		{"main", api.ComparisonSizingFit, 320, 180},
		{"main", api.ComparisonSizingSource, 180, 320},
		{"main", api.ComparisonSizingResize, 101, 180},
		{"main", api.ComparisonSizingCrop, 320, 180},
		{"other", api.ComparisonSizingResize, 180, 320},
		{"other", api.ComparisonSizingFit, 180, 320},
		{"other", api.ComparisonSizingCrop, 180, 320},
	} {
		t.Run(tc.mainID+"/"+tc.sizing, func(t *testing.T) {
			plan.MainSourceID = tc.mainID
			plan.ScreenshotSizing = tc.sizing
			request, err := comparisonCaptureRequest(plan, retained, "other", 1)
			if err != nil {
				t.Fatal(err)
			}
			if !request.ShowResolution {
				t.Fatal("the different display dimensions did not enable the resolution caption")
			}
			preview, err := renderer.ComparisonPreview(t.Context(), request)
			if err != nil || preview.Width != tc.width || preview.Height != tc.height {
				t.Fatalf("rotated preview = %dx%d, want %dx%d: %v", preview.Width, preview.Height, tc.width, tc.height, err)
			}
			request.Replay, request.TimestampSeconds = preview.Replay, preview.RelativeTimestampSeconds
			captured, err := renderer.ComparisonCapture(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateComparisonCapture(comparisonCaptureJob{request: request}, captured); err != nil {
				t.Fatal(err)
			}
			if tc.mainID == "main" && tc.sizing == api.ComparisonSizingResize {
				stretched := captured
				stretched.Width, stretched.Height = 320, 180
				if request.ValidOutputSize(stretched) {
					t.Fatal("resize accepted stretched pixels that only fit the main bounds")
				}
			}
			if tc.mainID == "main" && tc.sizing == api.ComparisonSizingFit {
				img, err := png.Decode(bytes.NewReader(captured.ImageBytes))
				if err != nil {
					t.Fatal(err)
				}
				r, g, b, _ := img.At(0, 90).RGBA()
				if r|g|b != 0 {
					t.Fatal("Fit did not pad the portrait image with black")
				}
			}
			if !preview.Replay.SamePicture(captured.Replay) || !bytes.Equal(preview.ImageBytes, captured.ImageBytes) {
				t.Fatal("rotated capture changed the exact preview picture")
			}
		})
	}
	t.Run("legacy preview refreshes the rotated main canvas", func(t *testing.T) {
		legacy := cloneWorkflowMediaPrivateArtifacts(retained)
		main := legacy.ComparisonSources["other"]
		main.Source.ScreenshotWidth, main.Source.ScreenshotHeight = 0, 0
		main.Source.NativePixelFormat = ""
		main.Source.ScreenshotTurn, main.Source.ScreenshotMatrix = false, false
		legacy.ComparisonSources["other"] = main
		legacyPlan, err := plan.Clone()
		if err != nil {
			t.Fatal(err)
		}
		legacyPlan.MainSourceID, legacyPlan.ScreenshotSizing = "other", ""
		for i := range legacyPlan.Sources {
			source := &legacyPlan.Sources[i]
			source.Fingerprint = legacy.ComparisonSources[source.ID].Fingerprint
		}
		builder := workflowMediaBuilder{comparisonRenderer: renderer}
		preview, err := builder.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, api.MediaArtifactSet{Comparisons: &legacyPlan}, legacy, "main", 1, 0, nil)
		if err != nil || preview.Width != 180 || preview.Height != 320 {
			t.Fatalf("legacy Fit preview = %dx%d, want the 180x320 display canvas: %v", preview.Width, preview.Height, err)
		}
	})
	t.Run("rotated active picture is measured and cropped in display orientation", func(t *testing.T) {
		codedPath, turnedPath := filepath.Join(root, "Bars.mp4"), filepath.Join(root, "TurnedBars.mp4")
		run("-f", "lavfi", "-i", "testsrc2=size=320x140:rate=10:duration=2", "-vf", "pad=320:180:0:20", "-c:v", "mpeg4", codedPath)
		run("-display_rotation", "90", "-i", codedPath, "-c", "copy", turnedPath)
		source, err := renderer.InspectComparisonSource(t.Context(), turnedPath)
		if err != nil {
			t.Fatal(err)
		}
		budget := &screenshots.ComparisonDecodeBudget{RemainingEncodedBytes: 64 << 20, MovieSecondsLimit: 6}
		crop, err := renderer.DetectComparisonActivePicture(t.Context(), source, 0, 1.5, budget)
		if err != nil || crop == nil || !crop.ValidFor(180, 320) || crop.Height <= 180 || crop.Width >= 180 {
			t.Fatalf("oriented crop = %+v, err = %v", crop, err)
		}
		frames, err := renderer.AnalyzeComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
			Source:          source,
			DurationSeconds: 1.5,
			MatchCrop:       crop,
			Budget:          budget,
		})
		if err != nil || len(frames) == 0 {
			t.Fatalf("analyze using oriented crop: %d frames, %v", len(frames), err)
		}
		calibration := comparisonCalibration{
			Profile: "rotated-source",
			Scale:   comparisons.PlaybackScale{Numerator: 1, Denominator: 1},
			Crop:    crop,
		}
		if _, reusable := calibration.prediction("rotated-source", source); !reusable {
			t.Fatal("warm matching rejected its valid rotated crop")
		}
	})
	t.Run("half-turn applies the display transform", func(t *testing.T) {
		halfPath := filepath.Join(root, "HalfTurn.mp4")
		run("-display_rotation", "180", "-i", mainPath, "-c", "copy", halfPath)
		half, err := renderer.InspectComparisonSource(t.Context(), halfPath)
		if err != nil {
			t.Fatal(err)
		}
		request := screenshots.ComparisonCaptureRequest{
			Source:           retained.ComparisonSources["main"].Source,
			TimestampSeconds: 1,
			ScreenshotSizing: api.ComparisonSizingSource,
		}
		plain, err := renderer.ComparisonPreview(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		request.Source = half
		turned, err := renderer.ComparisonPreview(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		original, err := png.Decode(bytes.NewReader(plain.ImageBytes))
		if err != nil {
			t.Fatal(err)
		}
		rotated, err := png.Decode(bytes.NewReader(turned.ImageBytes))
		if err != nil {
			t.Fatal(err)
		}
		x, y := 32, 48
		opposite := original.At(plain.Width-1-x, plain.Height-1-y)
		r, g, b, _ := rotated.At(x, y).RGBA()
		wantR, wantG, wantB, _ := opposite.RGBA()
		delta := max(math.Abs(float64(r)-float64(wantR)), math.Abs(float64(g)-float64(wantG)), math.Abs(float64(b)-float64(wantB)))
		if original.At(x, y) == opposite || delta > 4*257 {
			t.Fatalf("the half-turn screenshot pixel = %v, want %v from the other corner, original = %v", rotated.At(x, y), opposite, original.At(x, y))
		}
	})
	for _, tc := range []struct {
		name, first, second string
		width, height       int
	}{
		{"quarter-turn reversal", "insert:rotate=90", "insert:rotate=-90", 180, 320},
		{"late half-turn", "remove", "insert:rotate=180", 320, 180},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(encoders, " libx264 ") ||
				!strings.Contains(capabilities("-h", "bsf=h264_metadata"), "display_orientation") {
				t.Skip("frame-side orientation fixtures need libx264 and the h264_metadata bitstream filter")
			}
			fixtureRoot := t.TempDir()
			listPath, orientedPath := filepath.Join(fixtureRoot, "List.txt"), filepath.Join(fixtureRoot, "Oriented.mp4")
			for i, orientation := range []string{tc.first, tc.second} {
				part := filepath.Join(fixtureRoot, fmt.Sprintf("Part%d.mp4", i))
				run("-f", "lavfi", "-i", "smptebars=size=320x180:rate=10:duration=0.5", "-c:v", "libx264", "-g", "1", "-bf", "0",
					"-bsf:v", "h264_metadata=display_orientation="+orientation, part)
			}
			if err := os.WriteFile(listPath, []byte("file 'Part0.mp4'\nfile 'Part1.mp4'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			run("-f", "concat", "-i", listPath, "-c", "copy", orientedPath)
			inspected, err := renderer.InspectComparisonSource(t.Context(), orientedPath)
			if err != nil {
				t.Fatal(err)
			}
			if inspected.ScreenshotMatrix != (tc.first != "remove") {
				t.Fatal("the fixture did not establish the expected initial display metadata")
			}
			inspected.InputFingerprint, err = comparisonVideoFingerprint(t.Context(), orientedPath)
			if err != nil {
				t.Fatal(err)
			}
			request := screenshots.ComparisonCaptureRequest{
				Source:           inspected,
				TimestampSeconds: .3,
				ScreenshotSizing: api.ComparisonSizingSource,
			}
			preview, err := renderer.ComparisonPreview(t.Context(), request)
			if err != nil || preview.Width != tc.width || preview.Height != tc.height || math.Abs(preview.RelativeTimestampSeconds-.3) > 1e-9 {
				t.Fatalf("frame-side preview changed geometry or time: %dx%d at %.3f seconds: %v", preview.Width, preview.Height, preview.RelativeTimestampSeconds, err)
			}
			request.Replay = preview.Replay
			captured, err := renderer.ComparisonCapture(t.Context(), request)
			if err != nil || !preview.Replay.SamePicture(captured.Replay) || !bytes.Equal(preview.ImageBytes, captured.ImageBytes) {
				t.Fatalf("frame-side capture did not replay the preview: %v", err)
			}
			if request.Replay.File.PacketPosition == nil {
				t.Fatal("frame-side replay lacks its original native packet address")
			}
			request.Replay.File.PacketPosition = new(*request.Replay.File.PacketPosition + 1)
			_, err = renderer.ComparisonCapture(t.Context(), request)
			if failure, ok := api.AsOperationFailure(err); !ok || failure.ComparisonReason != "replay_packet_changed" {
				t.Fatalf("matching PTS accepted a different frame-side packet: %v", err)
			}
			request.TimestampSeconds, request.Replay = .6, nil
			turned, err := renderer.ComparisonPreview(t.Context(), request)
			if err != nil || turned.Width != tc.width || turned.Height != tc.height || math.Abs(turned.RelativeTimestampSeconds-.6) > 1e-9 {
				t.Fatalf("second orientation changed frame geometry or time: %dx%d at %.3f seconds: %v", turned.Width, turned.Height, turned.RelativeTimestampSeconds, err)
			}
			firstImage, err := png.Decode(bytes.NewReader(preview.ImageBytes))
			if err != nil {
				t.Fatal(err)
			}
			secondImage, err := png.Decode(bytes.NewReader(turned.ImageBytes))
			if err != nil {
				t.Fatal(err)
			}
			x, y := 32, 48
			opposite := firstImage.At(preview.Width-1-x, preview.Height-1-y)
			r, g, b, _ := secondImage.At(x, y).RGBA()
			wantR, wantG, wantB, _ := opposite.RGBA()
			delta := max(math.Abs(float64(r)-float64(wantR)), math.Abs(float64(g)-float64(wantG)), math.Abs(float64(b)-float64(wantB)))
			if firstImage.At(x, y) == opposite || delta > 4*257 {
				t.Fatal("the later screenshot did not use its own frame-side orientation")
			}
		})
	}
}

func TestComparisonManualMatchingSharesLetterboxedPictureArea(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("native manual matching requires FFmpeg")
	}
	root := t.TempDir()
	const movingPattern = "geq=lum='96+40*sin((X+Y+3*N+30*sin(N*0.05))*0.13)+24*cos((X-2*Y+2*N+60*cos(N*0.039))*0.11)':cb=128:cr=128"
	paths := []string{filepath.Join(root, "Example.Letterboxed.mkv"), filepath.Join(root, "Example.Cropped.mkv"), filepath.Join(root, "Example.Other.Letterboxed.mkv")}
	for i, path := range paths {
		filter := movingPattern
		if i != 1 {
			filter += ",pad=160:120:0:20:black"
		}
		command := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "nullsrc=size=160x80:rate=25:duration=12", "-vf", filter, "-c:v", "ffv1", path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("author manual picture-area fixture: %v %s", err, output)
		}
	}
	cfg := config.Config{}
	renderer := screenshots.NewService(cfg, api.NopLogger{}, root, nil)
	b := workflowMediaBuilder{
		config:             cfg,
		media:              &mediaModule{},
		comparisonRenderer: renderer,
	}
	private := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{}}
	plan := api.ComparisonPlan{MainSourceID: "main", Preferences: api.ComparisonPreferences{Count: 1}}
	for i, path := range paths[:2] {
		id := []string{"main", "other"}[i]
		source, err := renderer.InspectComparisonSource(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := comparisonVideoFingerprint(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		source.InputFingerprint = fingerprint
		private.ComparisonSources[id] = workflowComparisonSource{Source: source, Fingerprint: fingerprint}
		plan.Sources = append(plan.Sources, api.ComparisonSource{
			ID:          id,
			Label:       id,
			Fingerprint: fingerprint,
		})
	}
	matched, err := b.matchManualComparisonMoments(t.Context(), private, plan, comparisons.MatchOptions{Count: 1, MainMoments: []float64{9}}, b.comparisonProgress(t.Context(), 2))
	if err != nil || len(matched.Rows) != 1 || len(matched.Rows[0].Cells) != 2 {
		t.Fatalf("manual common-picture matching failed: %+v %v", matched.Rows, err)
	}
	for _, cell := range matched.Rows[0].Cells {
		if cell.TimestampSeconds == nil || math.Abs(*cell.TimestampSeconds-9) > 1e-6 || cell.ObservedIdentity == nil {
			t.Fatal("manual geometry normalization lost the corresponding native moment")
		}
	}
	t.Run("calibrated playback rate on an unanchored all-I secondary", func(t *testing.T) {
		path := filepath.Join(root, "Example.Retimed.mkv")
		cmd := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "nullsrc=size=160x80:rate=30:duration=10", "-vf", movingPattern, "-c:v", "ffv1", "-g", "1", path)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("author retimed secondary: %v %s", err, output)
		}
		source, err := renderer.InspectComparisonSource(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		source.InputFingerprint = "synthetic-retimed"
		retained := cloneWorkflowMediaPrivateArtifacts(private)
		retained.ComparisonAnalysisExecutable = "synthetic-executable"
		secondary := workflowComparisonSource{Source: source, Fingerprint: source.InputFingerprint}
		retained.ComparisonSources["other"] = secondary
		selected, err := plan.Clone()
		if err != nil {
			t.Fatal(err)
		}
		selected.Sources[1].Fingerprint = source.InputFingerprint
		selected.Sources[1].PreferBFrames = true
		profile, err := b.comparisonCalibrationProfile(retained, selected, "other")
		if err != nil {
			t.Fatal(err)
		}
		// The predicted center is past the secondary's end. Its search
		// window still overlaps the independently verifiable row at 8.6s.
		secondary.Calibration = &comparisonCalibration{
			Profile:       profile,
			Scale:         comparisons.PlaybackScale{Numerator: 5, Denominator: 6},
			OffsetSeconds: 4,
		}
		retained.ComparisonSources["other"] = secondary
		result, err := b.matchManualComparisonMoments(t.Context(), retained, selected, comparisons.MatchOptions{Count: 1, MainMoments: []float64{10.32}}, b.comparisonProgress(t.Context(), 2))
		if err != nil || len(result.Rows) != 1 || len(result.Rows[0].Cells) != 2 {
			t.Fatalf("retimed secondary did not match: %+v %v", result.Rows, err)
		}
		cell := result.Rows[0].Cells[1]
		if cell.ObservedIdentity == nil || cell.TimestampSeconds == nil || math.Abs(*cell.TimestampSeconds-8.6) > 1e-6 || cell.PictureType != "I" {
			t.Fatalf("wrong retimed secondary picture: %+v", cell)
		}
		// Now only the final 0.4 seconds overlap the search. The 8.6s
		// picture cannot enter that search through the all-I evidence scan.
		secondary.Calibration.OffsetSeconds = 7
		_, err = b.matchManualComparisonMoments(t.Context(), retained, selected, comparisons.MatchOptions{Count: 1, MainMoments: []float64{10.32}}, b.comparisonProgress(t.Context(), 2))
		if failure, ok := api.AsOperationFailure(err); !ok || failure.ComparisonReason != "manual_correspondence_inconclusive" {
			t.Fatalf("correspondence escaped the search window: %v", err)
		}
	})
	t.Run("both sources letterboxed without a retained prediction", func(t *testing.T) {
		both := cloneWorkflowMediaPrivateArtifacts(private)
		selected, err := plan.Clone()
		if err != nil {
			t.Fatal(err)
		}
		source, err := renderer.InspectComparisonSource(t.Context(), paths[2])
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := comparisonVideoFingerprint(t.Context(), paths[2])
		if err != nil {
			t.Fatal(err)
		}
		source.InputFingerprint = fingerprint
		both.ComparisonSources["other"] = workflowComparisonSource{Source: source, Fingerprint: fingerprint}
		selected.Sources[1].Fingerprint = fingerprint
		matched, err := b.matchManualComparisonMoments(t.Context(), both, selected, comparisons.MatchOptions{Count: 1, MainMoments: []float64{9}}, b.comparisonProgress(t.Context(), 2))
		if err != nil || len(matched.Rows) != 1 || len(matched.Rows[0].Cells) != 2 {
			t.Fatalf("manual crop bootstrap failed: %+v %v", matched.Rows, err)
		}
		for _, cell := range matched.Rows[0].Cells {
			if cell.TimestampSeconds == nil || math.Abs(*cell.TimestampSeconds-9) > 1e-6 || cell.ObservedIdentity == nil {
				t.Fatal("crop bootstrap changed the verified native moment")
			}
		}
		t.Run("selected no-crop fallback discards bootstrap pixels", func(t *testing.T) {
			selectedProbe := false
			wrapped := comparisonManualCropRenderer{comparisonFrameRenderer: renderer, detect: func(ctx context.Context, source screenshots.ComparisonSource, start, seconds float64, budget *screenshots.ComparisonDecodeBudget) (*screenshots.ComparisonCrop, error) {
				if source.InputFingerprint == fingerprint && start >= 7 {
					selectedProbe = true
					return nil, api.NewOperationError(api.OperationFailure{Code: api.OperationFailureInvalidSource, ComparisonReason: "transport_initialization"}, nil)
				}
				return renderer.DetectComparisonActivePicture(ctx, source, start, seconds, budget)
			}}
			builder := b
			builder.comparisonRenderer = wrapped
			_, err := builder.matchManualComparisonMoments(t.Context(), cloneWorkflowMediaPrivateArtifacts(both), selected, comparisons.MatchOptions{Count: 1, MainMoments: []float64{9}}, builder.comparisonProgress(t.Context(), 2))
			failure, ok := api.AsOperationFailure(err)
			if !selectedProbe || !ok || failure.ComparisonReason != "manual_correspondence_inconclusive" {
				t.Fatalf("bootstrap crop survived the selected no-crop fallback: selected=%t error=%v", selectedProbe, err)
			}
		})
	})
	for _, test := range []struct {
		name, reason string
		cause        error
		optional     bool
	}{
		{name: "unrelated initialization is never inspected", optional: true},
		{
			name:     "optional DVD initialization",
			reason:   "dvd_random_access_initialization",
			optional: true,
		},
		{
			name:     "optional transport initialization",
			reason:   "transport_initialization",
			optional: true,
		},
		{
			name:   "source mutation remains fatal",
			reason: "source_changed",
			cause:  api.ErrActiveInputChanged,
		},
		{name: "cancellation remains fatal", cause: context.Canceled},
		{
			name:   "invalid geometry remains fatal",
			reason: "matching_crop",
			cause:  errors.New("synthetic invalid geometry"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := plan
			selected.Anchors = []api.ComparisonAnchor{{
				SourceID:      "other",
				MainSeconds:   9,
				SourceSeconds: 9,
			}}
			calls := 0
			wrapped := comparisonManualCropRenderer{comparisonFrameRenderer: renderer, detect: func(ctx context.Context, source screenshots.ComparisonSource, start, seconds float64, budget *screenshots.ComparisonDecodeBudget) (*screenshots.ComparisonCrop, error) {
				calls++
				if start < 7 || start > 9 {
					t.Fatalf("inspected an unrelated manual neighborhood: start=%.3f", start)
				}
				if test.reason != "" || test.cause != nil {
					return nil, api.NewOperationError(api.OperationFailure{Code: api.OperationFailureInvalidSource, ComparisonReason: test.reason}, test.cause)
				}
				return renderer.DetectComparisonActivePicture(ctx, source, start, seconds, budget)
			}}
			builder := b
			builder.comparisonRenderer = wrapped
			result, err := builder.matchManualComparisonMoments(t.Context(), cloneWorkflowMediaPrivateArtifacts(private), selected, comparisons.MatchOptions{Count: 1, MainMoments: []float64{9}}, builder.comparisonProgress(t.Context(), 2))
			if test.optional {
				if err != nil || len(result.Rows) != 1 || calls != 2 {
					t.Fatalf("selected manual neighborhood could not match: rows=%d probes=%d error=%v", len(result.Rows), calls, err)
				}
			} else if err == nil || calls != 1 || test.cause != nil && !errors.Is(err, test.cause) {
				t.Fatalf("manual crop hid an integrity failure: probes=%d error=%v", calls, err)
			}
		})
	}
}

type comparisonManualCropRenderer struct {
	comparisonFrameRenderer
	detect func(context.Context, screenshots.ComparisonSource, float64, float64, *screenshots.ComparisonDecodeBudget) (*screenshots.ComparisonCrop, error)
}

func (r comparisonManualCropRenderer) DetectComparisonActivePicture(ctx context.Context, source screenshots.ComparisonSource, start, seconds float64, budget *screenshots.ComparisonDecodeBudget) (*screenshots.ComparisonCrop, error) {
	return r.detect(ctx, source, start, seconds, budget)
}

type comparisonWindowRecorder struct {
	comparisonFrameRenderer
	durations        []float64
	strides          []float64
	firstModes       map[string]string
	matchingRows     bool
	rowAnalysis      int
	failFingerprint  string
	failErr          error
	preferExcluded   *api.ComparisonTimeInterval
	flatPeak         *float64
	flatVerified     bool
	emptyFingerprint string
	analysisFailure  error
	analysisFailures int
	cropFailurePath  string
	cropFailures     int
}

func (r *comparisonWindowRecorder) DetectComparisonActivePicture(
	_ context.Context, source screenshots.ComparisonSource, _, _ float64, _ *screenshots.ComparisonDecodeBudget,
) (*screenshots.ComparisonCrop, error) {
	if r.cropFailurePath != "" && source.Path == r.cropFailurePath {
		r.cropFailures++
		return nil, screenshots.ErrComparisonNormalizationUnavailable
	}
	return nil, nil
}

func (r *comparisonWindowRecorder) AnalyzeComparisonSource(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	if r.analysisFailure != nil {
		r.analysisFailures++
		return nil, r.analysisFailure
	}
	if r.matchingRows {
		r.rowAnalysis++
	}
	analyzer, ok := r.comparisonFrameRenderer.(interface {
		AnalyzeComparisonSource(context.Context, screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error)
	})
	if !ok {
		return nil, errors.New("recorded renderer cannot analyze scenes")
	}
	frames, err := analyzer.AnalyzeComparisonSource(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("record comparison analysis: %w", err)
	}
	if r.preferExcluded != nil {
		for i := range frames {
			evidence := *frames[i].Evidence
			evidence.Eligible = true
			evidence.Dark = api.ComparisonSceneCategory{State: "verified", Score: .8}
			if seconds := frames[i].RelativeTimestampSeconds; seconds >= r.preferExcluded.Start && seconds <= r.preferExcluded.End {
				evidence.Dark.Score = 1
			}
			frames[i].Evidence = &evidence
		}
	}
	if r.flatPeak != nil {
		for i := range frames {
			evidence := *frames[i].Evidence
			evidence.Eligible = true
			evidence.Flat = api.ComparisonSceneCategory{State: "rejected", Score: 1 - math.Abs(frames[i].RelativeTimestampSeconds-*r.flatPeak)/120}
			if r.flatVerified {
				evidence.Flat.State = "verified"
			}
			frames[i].Evidence = &evidence
		}
	}
	return frames, nil
}

func (r *comparisonWindowRecorder) SampleComparisonSource(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	if r.emptyFingerprint != "" && request.Source.InputFingerprint == r.emptyFingerprint {
		return nil, nil
	}
	if r.failFingerprint != "" && request.Source.InputFingerprint == r.failFingerprint {
		if r.failErr != nil {
			return nil, r.failErr
		}
		return nil, screenshots.ErrComparisonFastInconclusive
	}
	if r.durations == nil {
		r.strides = nil
		r.firstModes = map[string]string{}
	}
	if _, seen := r.firstModes[request.Source.InputFingerprint]; !seen && request.Renderer != nil {
		r.firstModes[request.Source.InputFingerprint] = request.Renderer.Mode
	}
	r.durations = append(r.durations, request.DurationSeconds)
	r.strides = append(r.strides, request.StrideSeconds)
	frames, err := r.comparisonFrameRenderer.SampleComparisonSource(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("record comparison window: %w", err)
	}
	return frames, nil
}

func (r *comparisonWindowRecorder) usedFullScan() bool { return slices.Contains(r.strides, .5) }

func (r *comparisonWindowRecorder) ComparisonAnalysisExecutableIdentity(ctx context.Context) (string, error) {
	inspector, ok := r.comparisonFrameRenderer.(interface {
		ComparisonAnalysisExecutableIdentity(context.Context) (string, error)
	})
	if !ok {
		return "", nil
	}
	identity, err := inspector.ComparisonAnalysisExecutableIdentity(ctx)
	if err != nil {
		return "", fmt.Errorf("record comparison executable: %w", err)
	}
	return identity, nil
}

func TestComparisonBoundedAlignmentUsesNativeNonzeroClocks(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("native alignment requires FFmpeg")
	}
	root := t.TempDir()
	paths := []string{filepath.Join(root, "Example.Reference.mkv"), filepath.Join(root, "Example.Padded.mkv"), filepath.Join(root, "Example.Trimmed.mkv"), filepath.Join(root, "Example.HDR.mkv"), filepath.Join(root, "Example.Cropped.mkv")}
	run := func(args ...string) {
		t.Helper()
		if output, err := exec.CommandContext(t.Context(), ffmpeg, append([]string{"-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("synthetic alignment fixture: %v %s", err, output)
		}
	}
	run("-f", "lavfi", "-i", "nullsrc=size=64x48:rate=10:duration=90", "-vf", "geq=lum='48+mod((X+N)*13+Y*7+mod((X+N)*Y,123),160)':cb=128:cr=128,setpts=PTS+5/TB", "-c:v", "ffv1", paths[0])
	run("-i", paths[0], "-vf", "setpts=PTS-STARTPTS,tpad=start=30:start_mode=add:color=black,setpts=N/(10*TB)", "-c:v", "ffv1", paths[1])
	run("-copyts", "-i", paths[0], "-vf", "trim=start=7,setpts=PTS-STARTPTS", "-c:v", "ffv1", paths[2])
	run("-i", paths[1], "-vf",
		"zscale=primariesin=bt709:transferin=bt709:matrixin=bt709:rangein=limited:primaries=bt2020:transfer=smpte2084:matrix=bt2020nc:range=limited,format=yuv420p10le",
		"-c:v", "ffv1", paths[3])
	run("-copyts", "-i", paths[0], "-vf", "crop=64:44:0:2", "-c:v", "ffv1", paths[4])
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "app.sqlite")}}
	renderer := &comparisonWindowRecorder{comparisonFrameRenderer: screenshots.NewService(cfg, api.NopLogger{}, root, nil)}
	private := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{}, ArtifactImages: map[api.PublicResourceID]api.ScreenshotImage{}}
	plan := api.ComparisonPlan{MainSourceID: "main", Preferences: api.ComparisonPreferences{Count: 2}}
	for i, path := range paths {
		source, err := renderer.InspectComparisonSource(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		id := []string{"main", "positive", "negative", "hdr", "cropped"}[i]
		if id == "hdr" && (len(source.HDRFacts.Formats) == 0 || source.HDRFacts.Formats[0] == api.HDRFormatSDR) {
			t.Fatalf("native HDR fixture was not inspected as HDR: %+v", source.HDRFacts)
		}
		fingerprint, err := comparisonVideoFingerprint(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		source.InputFingerprint = fingerprint
		private.ComparisonSources[id] = workflowComparisonSource{Source: source, Fingerprint: fingerprint}
		plan.Sources = append(plan.Sources, api.ComparisonSource{
			ID:                id,
			Label:             id,
			PreferBFrames:     true,
			AllowedFrameTypes: []string{"B"},
			Fingerprint:       fingerprint,
			Overlay:           true,
		})
	}
	b := workflowMediaBuilder{
		config:             cfg,
		media:              &mediaModule{},
		comparisonRenderer: renderer,
	}
	var phases []string
	var updates []api.WorkflowProgressUpdate
	var windowLabels []string
	ctx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) {
		if update.Phase != "" {
			phases = append(phases, update.Phase)
			renderer.matchingRows = update.Phase == "comparisons_alignment_rows"
		}
		if !update.ItemOnly {
			updates = append(updates, update)
		}
		if update.Phase == "comparisons_alignment_rows" && update.Kind == "comparison_window" && update.Status == api.StageStatusRunning {
			windowLabels = append(windowLabels, update.Label)
		}
	})
	checkProgress := func(t *testing.T, complete bool) {
		t.Helper()
		previous := 0
		for _, update := range updates {
			if update.Total != 1000 || update.Completed < previous || update.Completed > update.Total {
				t.Fatalf("inconsistent command progress after %d: %+v", previous, update)
			}
			previous = update.Completed
		}
		if (previous == 1000) != complete {
			t.Fatalf("command completion=%d, want complete=%t", previous, complete)
		}
	}
	progress := b.comparisonProgress(ctx, 5)
	renderer.cropFailurePath = paths[0]
	matched, err := b.matchWorkflowComparisons(ctx, &private, plan, progress)
	renderer.cropFailurePath = ""
	if err != nil || len(matched.Rows) != 2 {
		t.Fatalf("bounded match rows=%+v err=%v", matched.Rows, err)
	}
	if renderer.cropFailures != 1 {
		t.Fatalf("expected one recoverable main crop probe, got %d", renderer.cropFailures)
	}
	if progress.completed < 700 || progress.completed >= 1000 {
		t.Fatalf("Quick Scan matching left too much or no progress for capture: %d", progress.completed)
	}
	if !slices.ContainsFunc(windowLabels, func(label string) bool {
		return strings.HasPrefix(label, "Column ") && strings.HasSuffix(label, ", row 2")
	}) {
		t.Fatalf("Quick Scan did not report a column and the active row: %v", windowLabels)
	}
	if renderer.rowAnalysis != 0 {
		t.Fatal("general native rows repeated main scene analysis")
	}
	for _, duration := range renderer.durations {
		if duration > 65 {
			t.Fatalf("eligible sources triggered a full scan: windows=%v facts=%+v", renderer.durations, private.ComparisonSources)
		}
	}
	for _, row := range matched.Rows {
		for i, want := range []float64{0, 3, -2, 3, 0} {
			cell := row.Cells[i]
			if cell.TimestampSeconds == nil || cell.ObservedPTS == nil || math.Abs(*cell.TimestampSeconds-*row.Cells[0].TimestampSeconds-want) > .001 {
				t.Fatalf("wrong source-relative correspondence: %+v", row)
			}
		}
	}
	t.Run("Deep native refinement honors protected times over scene scores", func(t *testing.T) {
		before := *renderer
		t.Cleanup(func() { *renderer = before })
		guarded := cloneWorkflowMediaPrivateArtifacts(private)
		boundaryPlan, err := plan.Clone()
		if err != nil {
			t.Fatal(err)
		}
		excluded := api.ComparisonTimeInterval{Start: 40, End: 50}
		renderer.preferExcluded = &excluded
		t.Cleanup(func() { renderer.preferExcluded = nil })
		boundaryPlan.Preferences = api.ComparisonPreferences{
			Count:     1,
			DarkCount: new(1),
			ScanMode:  "deep",
			SelectionWindow: &api.ComparisonSelectionWindow{
				Start:    new(30.0),
				End:      new(60.0),
				Excluded: []api.ComparisonTimeInterval{excluded},
			},
		}
		protected, err := b.matchWorkflowComparisons(t.Context(), &guarded, boundaryPlan, b.comparisonProgress(t.Context(), 5))
		if err != nil || len(protected.Rows) != 1 {
			t.Fatalf("protected native selection: rows=%d err=%v", len(protected.Rows), err)
		}
		for _, row := range protected.Rows {
			seconds := *row.Cells[0].TimestampSeconds
			if seconds < 30 || seconds > 60 || seconds >= excluded.Start && seconds <= excluded.End {
				t.Fatalf("native refinement escaped protected selection: %.6f", seconds)
			}
		}
		media := api.MediaArtifactSet{}
		if _, err := b.captureWorkflowComparisons(t.Context(), api.ReleaseRef{SourcePath: paths[0], Generation: 1}, &media, &guarded, protected, b.comparisonProgress(t.Context(), 1), false); err != nil || len(media.Artifacts) != len(paths) {
			t.Fatalf("protected native rows could not capture: images=%d err=%v", len(media.Artifacts), err)
		}
	})
	nativePreferences := api.ComparisonPreferences{
		Count:     2,
		FlatCount: 1,
		SelectionWindow: &api.ComparisonSelectionWindow{
			Start:    new(6.0),
			End:      new(108.0),
			Excluded: []api.ComparisonTimeInterval{{Start: 0, End: 7.2}, {Start: 9.5, End: 58.7}, {Start: 61.3, End: 120}},
		},
	}
	for _, test := range []struct {
		name          string
		intra, budget bool
		preferences   api.ComparisonPreferences
	}{
		{
			name:        "automatic I-only sources remain usable near programme start",
			intra:       true,
			preferences: api.ComparisonPreferences{Count: 1, SelectionWindow: &api.ComparisonSelectionWindow{Start: new(.5), End: new(3.3)}},
		},
		{name: "B P native references span Quick discovery passes", preferences: nativePreferences},
		{
			name:        "wider Quick scoring exhaustion retains verified rows",
			budget:      true,
			preferences: nativePreferences,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := *renderer
			t.Cleanup(func() { *renderer = before })
			shortPaths := []string{filepath.Join(t.TempDir(), "Example.Native.mkv"), filepath.Join(t.TempDir(), "Example.Native.Copy.mkv")}
			if test.intra {
				run("-i", paths[0], "-vf", "trim=duration=9,setpts=PTS-STARTPTS", "-c:v", "ffv1", shortPaths[0])
			} else {
				run("-f", "lavfi", "-i", "nullsrc=size=64x48:rate=10:duration=120", "-vf", "geq=lum='48+mod((X+N)*13+Y*7+mod((X+N)*Y,123),160)':cb=128:cr=128", "-c:v", "mpeg4", "-bf", "2", "-g", "50", "-q:v", "2", shortPaths[0])
				renderer.flatPeak = new(60.0)
			}
			run("-i", shortPaths[0], "-c:v", "copy", shortPaths[1])
			shortPrivate := workflowMediaPrivateArtifacts{
				ComparisonSources: map[string]workflowComparisonSource{},
				ArtifactImages:    map[api.PublicResourceID]api.ScreenshotImage{},
				screenshotSubject: api.ScreenshotSubject{SourcePath: shortPaths[0]},
			}
			shortPlan := api.ComparisonPlan{MainSourceID: "main", Preferences: test.preferences}
			if test.budget {
				shortPlan.Preferences.FlatCount = 2
				renderer.flatVerified = true
			}
			for i, path := range shortPaths {
				source, err := renderer.InspectComparisonSource(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				fingerprint, err := comparisonVideoFingerprint(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				source.InputFingerprint = fingerprint
				id := []string{"main", "copy"}[i]
				shortPrivate.ComparisonSources[id] = workflowComparisonSource{
					Source:      source,
					Path:        path,
					Fingerprint: fingerprint,
				}
				shortPlan.Sources = append(shortPlan.Sources, api.ComparisonSource{
					ID:                id,
					Label:             id,
					Overlay:           true,
					PreferBFrames:     true,
					AllowedFrameTypes: []string{"B"},
					Fingerprint:       fingerprint,
				})
			}
			workCtx := t.Context()
			wantRows := test.preferences.Count
			if test.budget {
				wantRows = 1
				workCtx = api.WithWorkflowProgressReporter(workCtx, func(update api.WorkflowProgressUpdate) {
					if update.Kind == "comparison_row" && update.Status == api.StageStatusCompleted {
						renderer.emptyFingerprint = shortPrivate.ComparisonSources["copy"].Fingerprint
						renderer.analysisFailure = screenshots.ErrComparisonFastInconclusive
					}
				})
			}
			selected, err := b.matchWorkflowComparisons(workCtx, &shortPrivate, shortPlan, b.comparisonProgress(workCtx, 5))
			if err != nil || len(selected.Rows) != wantRows {
				t.Fatalf("native region selection: rows=%d err=%v", len(selected.Rows), err)
			}
			for _, row := range selected.Rows {
				for _, cell := range row.Cells {
					permitted := cell.PictureType == "I" && test.intra || !test.intra && (cell.PictureType == "B" || cell.PictureType == "P")
					if !permitted || cell.ObservedIdentity == nil {
						t.Fatal("native source lost picture-type permission or identity")
					}
				}
			}
			if test.budget {
				if selected.Analysis == nil || selected.Analysis.RankingComplete || renderer.analysisFailures != 1 || !slices.Contains(selected.Analysis.Reasons, "native_decode_budget_exhausted") {
					t.Fatalf("wider scoring did not stop with truthful partial state: analysis=%+v failures=%d", selected.Analysis, renderer.analysisFailures)
				}
				_, payload, err := shortPrivate.MarshalPrivateResource()
				if err != nil {
					t.Fatal(err)
				}
				shortPrivate, err = decodeWorkflowMediaPrivateArtifacts(b, payload)
				if err != nil || shortPrivate.ComparisonInventory == nil || shortPrivate.ComparisonInventory.Summary.RankingComplete {
					t.Fatalf("restart lost incomplete scoring state: %v", err)
				}
				renderer.emptyFingerprint, renderer.analysisFailure = "", nil
			}
			media := api.MediaArtifactSet{}
			if _, err := b.captureWorkflowComparisons(t.Context(), api.ReleaseRef{SourcePath: shortPaths[0], Generation: 1}, &media, &shortPrivate, selected, b.comparisonProgress(t.Context(), 1), false); err != nil || len(media.Artifacts) != 2*wantRows {
				t.Fatalf("native region capture: images=%d err=%v", len(media.Artifacts), err)
			}
		})
	}
	found := false
	for _, phase := range phases {
		if phase == "comparisons_alignment_rows" {
			found = true
		}
	}
	if !found {
		t.Fatal("independent offset validation did not report progress")
	}
	t.Run("calibration survives restart without granting frame authority", func(t *testing.T) {
		coldSeconds := 0.0
		for _, seconds := range renderer.durations {
			coldSeconds += seconds
		}
		_, payload, err := private.MarshalPrivateResource()
		if err != nil {
			t.Fatal(err)
		}
		restarted, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
		if err != nil {
			t.Fatal(err)
		}
		for id, source := range restarted.ComparisonSources {
			if id != plan.MainSourceID && source.Calibration == nil {
				t.Fatalf("lost calibration for %s", id)
			}
		}
		configured, err := plan.Clone()
		if err != nil {
			t.Fatal(err)
		}
		builder := b
		builder.resolver = workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{SourcePath: paths[0], VideoPath: paths[0]}}
		if err := builder.configureComparisonVideos(t.Context(), api.ReleaseRef{SourcePath: paths[0], Generation: 1}, &restarted, &configured, nil); err != nil {
			t.Fatal(err)
		}
		renderer.durations = nil
		reused, err := builder.matchWorkflowComparisons(t.Context(), &restarted, configured, builder.comparisonProgress(t.Context(), 5))
		if err != nil || len(reused.Rows) != len(matched.Rows) || len(renderer.durations) == 0 {
			t.Fatalf("restart did not perform fresh row matching: rows=%d err=%v", len(reused.Rows), err)
		}
		warmSeconds := 0.0
		for _, seconds := range renderer.durations {
			warmSeconds += seconds
		}
		if warmSeconds >= coldSeconds {
			t.Fatalf("restart decoded calibration again: cold_seconds=%.3f warm_seconds=%.3f", coldSeconds, warmSeconds)
		}
		for id, source := range restarted.ComparisonSources {
			if id != plan.MainSourceID && renderer.firstModes[source.Fingerprint] != "" {
				t.Fatal("calibration reuse pinned a previous attempt's renderer fallback")
			}
		}
		for i, row := range reused.Rows {
			for j, cell := range row.Cells {
				if cell.ObservedPTS == nil || *cell.ObservedPTS != *matched.Rows[i].Cells[j].ObservedPTS {
					t.Fatal("calibration reuse changed the selected native picture")
				}
			}
		}
		for _, source := range restarted.ComparisonSources {
			if source.Calibration != nil {
				source.Calibration.OffsetSeconds += 1000
			}
		}
		var wrongUpdates []api.WorkflowProgressUpdate
		wrongCtx := api.WithWorkflowProgressReporter(t.Context(), func(update api.WorkflowProgressUpdate) {
			wrongUpdates = append(wrongUpdates, update)
		})
		wrongPlan := plan
		// Leave decode allowance for repeated discovery after failed native proofs.
		wrongPlan.Preferences.SelectionWindow = &api.ComparisonSelectionWindow{Start: new(20.0), End: new(50.0)}
		wrong, err := b.matchWorkflowComparisons(wrongCtx, &restarted, wrongPlan, b.comparisonProgress(wrongCtx, 5))
		if len(wrong.Rows) != 0 || err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a cached prediction authorized unverified pictures: rows=%d err=%v", len(wrong.Rows), err)
		}
		wire, err := wrong.Clone()
		if err != nil || wire.Rows == nil {
			t.Fatalf("zero matches must serialize as an empty row array: %v", err)
		}
		discoveries, previous := 0, 0
		previousPhase := ""
		for _, update := range wrongUpdates {
			if update.ItemOnly {
				continue
			}
			if update.Phase == "comparisons_main_discovery" && update.Phase != previousPhase {
				discoveries++
			}
			if update.Completed < previous || update.Completed >= update.Total {
				t.Fatalf("expanded Quick Scan completed before capture: %+v", update)
			}
			previous = update.Completed
			previousPhase = update.Phase
		}
		if discoveries < 3 {
			t.Fatalf("fixture did not exercise repeated Quick Scan expansion: discoveries=%d analysis=%+v", discoveries, wrong.Analysis)
		}
	})
	media := api.MediaArtifactSet{}
	captured, err := b.captureWorkflowComparisons(ctx, api.ReleaseRef{SourcePath: paths[0], Generation: 1}, &media, &private, matched, progress, false)
	if err != nil || len(media.Artifacts) != 10 {
		t.Fatalf("bounded captures=%d err=%v", len(media.Artifacts), err)
	}
	for r, row := range matched.Rows {
		for c, cell := range row.Cells {
			width, height := private.ComparisonSources[matched.MainSourceID].Source.DisplaySize()
			image := private.ArtifactImages[captured.Rows[r].Cells[c].ArtifactID]
			if image.Width != width || image.Height != height {
				t.Fatal("capture did not use the main display canvas")
			}
			if *cell.ObservedPTS != *captured.Rows[r].Cells[c].ObservedPTS {
				t.Fatal("capture changed the matched native picture")
			}
		}
	}
	t.Run("bounded command progress", func(t *testing.T) { checkProgress(t, true) })
	t.Run("automatic captures follow analysis intent", func(t *testing.T) {
		prior := media
		prior.Comparisons = &captured
		builder := b
		builder.resolver = workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{SourcePath: paths[0], VideoPath: paths[0]}}
		for _, change := range []string{"mode", "range", "count", "render"} {
			t.Run(change, func(t *testing.T) {
				requested, err := captured.Clone()
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "mode":
					requested.Preferences.ScanMode = "deep"
				case "range":
					moment := *requested.Rows[0].Cells[0].TimestampSeconds
					requested.Preferences.SelectionWindow = &api.ComparisonSelectionWindow{Excluded: []api.ComparisonTimeInterval{{Start: moment - .1, End: moment + .1}}}
				case "count":
					requested.Preferences.Count++
				case "render":
					requested.Sources[0].Label += " HD"
				}
				updated, resource, err := builder.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: paths[0], Generation: 1}, api.TrackerReleaseProjectionSet{}, &prior, private,
					api.ComparisonInstructions{Action: "configure", Plan: requested}, nil, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if change == "mode" || change == "range" {
					wire, err := updated.Comparisons.Clone()
					if err != nil {
						t.Fatal(err)
					}
					for _, row := range wire.Rows {
						if row.Cells == nil {
							t.Fatal("cleared cells must serialize as an empty array")
						}
					}
					for _, row := range updated.Comparisons.Rows {
						if len(row.Cells) != 0 || row.Evidence != nil || row.Scene != "" {
							t.Fatal("changed analysis retained automatic moments as captures or manual pins")
						}
					}
					if _, _, err := b.EditComparisons(t.Context(), api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, &updated, resource,
						api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: updated.Comparisons.Fingerprint}, nil, time.Now()); err == nil {
						t.Fatal("changed analysis approved old automatic images")
					}
					return
				}
				cell := updated.Comparisons.Rows[0].Cells[0]
				if cell.ObservedIdentity == nil || cell.Confidence == nil || updated.Comparisons.Rows[0].Evidence == nil ||
					(change == "count" && cell.ArtifactID != captured.Rows[0].Cells[0].ArtifactID) || (change == "render" && cell.ArtifactID != "") {
					t.Fatal("count/render edits lost valid native evidence or retained stale rendering")
				}
			})
		}
		protected, err := captured.Clone()
		if err != nil {
			t.Fatal(err)
		}
		moment := *protected.Rows[0].Cells[0].TimestampSeconds
		protected.Preferences.SelectionWindow = &api.ComparisonSelectionWindow{Excluded: []api.ComparisonTimeInterval{{Start: moment - .1, End: moment + .1}}}
		failure, ok := api.AsOperationFailure(verifyComparisonCells(protected, private, true, nil))
		if !ok || failure.ComparisonReason != "protected_time" {
			t.Fatal("approval/hosting omitted the private protected-time guard")
		}
		protected.Preferences.SelectionWindow = nil
		protected.Rows[0].Scene, protected.Rows[0].Evidence = "", nil
		if err := verifyComparisonCells(protected, private, true, nil); err == nil {
			t.Fatal("automatic pixels without scene evidence passed review")
		}
	})
	t.Run("cancelled capture entry publishes matched native witnesses", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		prior := api.MediaArtifactSet{Comparisons: &matched}
		updated, resource, err := b.EditComparisons(ctx, api.ReleaseRef{SourcePath: paths[0], Generation: 1}, api.TrackerReleaseProjectionSet{}, &prior, private,
			api.ComparisonInstructions{Action: "capture", Plan: matched}, nil, time.Now())
		incomplete, ok := errors.AsType[*releaseworkflow.ComparisonIncompleteError](err)
		if !ok || incomplete.Reason != "capture_cancelled" || updated.Comparisons == nil || len(updated.Comparisons.Rows) != len(matched.Rows) || updated.Comparisons.ApprovedFingerprint != "" {
			t.Fatalf("capture entry discarded matched rows or retained approval: %+v err=%v", updated.Comparisons, err)
		}
		retained, ok := resource.(workflowMediaPrivateArtifacts)
		if !ok || len(updated.Failures) != 1 || updated.Failures[0].Failure.ComparisonReason != "capture_cancelled" {
			t.Fatalf("capture entry lost its publishable incomplete outcome: %+v", updated.Failures)
		}
		for r, row := range updated.Comparisons.Rows {
			for c, cell := range row.Cells {
				original := matched.Rows[r].Cells[c]
				if cell.ObservedIdentity == nil || original.ObservedIdentity == nil || *cell.ObservedIdentity != *original.ObservedIdentity {
					t.Fatal("capture entry changed a matched native picture")
				}
				key, err := api.CanonicalWorkflowFingerprint(*cell.ObservedIdentity)
				if _, found := retained.ComparisonSources[cell.SourceID].Witnesses[string(key)]; err != nil || !found {
					t.Fatal("capture entry discarded a matched replay witness")
				}
			}
		}
	})
	for _, stop := range []struct {
		after   int
		budget  bool
		failure error
		reason  string
	}{
		{1, false, nil, "match_cancelled"},
		{2, false, nil, "match_cancelled"},
		{1, true, nil, "native_decode_budget_exhausted"},
		{1, false, screenshots.ErrComparisonWorkerFailed, "match_worker_failed"},
		{1, false, screenshots.ErrComparisonNormalizationUnavailable, "match_worker_failed"},
		{1, false, errors.New("invalid native picture identity"), ""},
	} {
		t.Run(fmt.Sprintf("matching stop after row %d budget %t failure %v", stop.after, stop.budget, stop.failure), func(t *testing.T) {
			stopAfter := stop.after
			t.Cleanup(func() { renderer.failFingerprint, renderer.failErr = "", nil })
			interrupted := cloneWorkflowMediaPrivateArtifacts(private)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			completed := 0
			ctx = api.WithWorkflowProgressReporter(ctx, func(update api.WorkflowProgressUpdate) {
				if update.Kind == "comparison_row" && update.Status == api.StageStatusCompleted {
					completed++
					if completed == stopAfter {
						if stop.budget || stop.failure != nil {
							renderer.failFingerprint = interrupted.ComparisonSources[plan.MainSourceID].Fingerprint
							renderer.failErr = stop.failure
						} else {
							cancel()
						}
					}
				}
			})
			var partial api.ComparisonPlan
			var workerSnapshot api.MediaArtifactSet
			var err error
			if errors.Is(stop.failure, screenshots.ErrComparisonWorkerFailed) {
				prior := api.MediaArtifactSet{Comparisons: &plan, Failures: []api.WorkflowFailure{{Failure: api.OperationFailure{
					Code:             api.OperationFailureMissingPrerequisite,
					Operation:        api.OperationKindMedia,
					Recovery:         api.OperationRecoveryRetry,
					ComparisonReason: "unrelated_media_issue",
					Message:          "A separate issue remains.",
				}}}}
				next, resource, workerErr := b.EditComparisons(ctx, api.ReleaseRef{SourcePath: paths[0], Generation: 1}, api.TrackerReleaseProjectionSet{}, &prior, interrupted,
					api.ComparisonInstructions{
						Action:            "match",
						Plan:              plan,
						CaptureAfterMatch: new(false),
					}, nil, time.Now())
				workerSnapshot, err = next, workerErr
				if next.Comparisons != nil {
					partial = *next.Comparisons
				}
				if saved, ok := resource.(workflowMediaPrivateArtifacts); ok {
					interrupted = saved
				}
			} else {
				partial, err = b.matchWorkflowComparisons(ctx, &interrupted, plan, b.comparisonProgress(ctx, 5))
			}
			incomplete, ok := errors.AsType[*releaseworkflow.ComparisonIncompleteError](err)
			if stop.reason == "" {
				if err == nil || ok || len(partial.Rows) != 0 {
					t.Fatalf("invalid native evidence published partial rows: rows=%d err=%v", len(partial.Rows), err)
				}
				return
			}
			stoppedCorrectly := ok && incomplete.Reason == stop.reason
			if stop.budget {
				stoppedCorrectly = err == nil && partial.Analysis != nil && slices.Contains(partial.Analysis.Reasons, "native_decode_budget_exhausted")
			}
			if completed != stopAfter || !stoppedCorrectly || len(partial.Rows) != stopAfter || partial.ApprovedFingerprint != "" {
				t.Fatalf("matching cancellation discarded verified work: rows=%d err=%v", len(partial.Rows), err)
			}
			_, payload, err := interrupted.MarshalPrivateResource()
			if err != nil {
				t.Fatal(err)
			}
			restored, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
			if err != nil {
				t.Fatal(err)
			}
			renderer.durations = nil
			renderer.failFingerprint, renderer.failErr = "", nil
			media := api.MediaArtifactSet{}
			captured, err := b.captureWorkflowComparisons(t.Context(), api.ReleaseRef{SourcePath: paths[0], Generation: 1}, &media, &restored, partial, b.comparisonProgress(t.Context(), 1), false)
			if err != nil || len(media.Artifacts) != stopAfter*len(paths) || len(renderer.durations) != 0 {
				t.Fatalf("restart lost replay or repeated matching: captures=%d windows=%v err=%v", len(media.Artifacts), renderer.durations, err)
			}
			for r, row := range captured.Rows {
				for i, cell := range row.Cells {
					if cell.ObservedIdentity == nil || partial.Rows[r].Cells[i].ObservedIdentity == nil || *cell.ObservedIdentity != *partial.Rows[r].Cells[i].ObservedIdentity {
						t.Fatal("restart changed the retained native picture")
					}
				}
			}
			if errors.Is(stop.failure, screenshots.ErrComparisonWorkerFailed) {
				workerSnapshot.Comparisons, workerSnapshot.Artifacts = &captured, media.Artifacts
				retried, _, retryErr := b.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: paths[0], Generation: 1}, api.TrackerReleaseProjectionSet{}, &workerSnapshot, restored,
					api.ComparisonInstructions{
						Action:            "match",
						Plan:              captured,
						CaptureAfterMatch: new(false),
					}, nil, time.Now())
				if retryErr != nil || retried.Comparisons == nil || len(retried.Comparisons.Rows) != plan.Preferences.Count ||
					len(retried.Failures) != 1 || retried.Failures[0].Failure.ComparisonReason != "unrelated_media_issue" {
					t.Fatalf("successful worker retry retained its failure or lost another issue: failures=%+v err=%v", retried.Failures, retryErr)
				}
			}
		})
	}
	_, payload, err := private.MarshalPrivateResource()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
	if err != nil || restored.ComparisonSources["main"].Source.FrameCount != 900 {
		t.Fatalf("restart lost caption metadata: %v", err)
	}
	// Scene quotas use main-only discovery and bounded secondary matching.
	plan.Preferences.DarkCount = new(1)
	renderer.durations = nil
	if _, err := b.matchWorkflowComparisons(t.Context(), &private, plan, b.comparisonProgress(t.Context(), 5)); err != nil {
		t.Fatal(err)
	}
	if renderer.usedFullScan() {
		t.Fatalf("scene quota triggered forbidden secondary discovery: %v", renderer.durations)
	}
	plan.Preferences.DarkCount = nil
	t.Run("unsupported normalization returns an explicit bounded limitation", func(t *testing.T) {
		changed := cloneWorkflowMediaPrivateArtifacts(private)
		source := changed.ComparisonSources["hdr"]
		source.Source.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatDolbyVision}, DolbyVisionProfile: "5"}
		changed.ComparisonSources["hdr"] = source
		renderer.durations = nil
		logger := &recordingMediaLogger{}
		builder := b
		builder.media = &mediaModule{logger: logger}
		unavailable, err := builder.matchWorkflowComparisons(t.Context(), &changed, plan, builder.comparisonProgress(t.Context(), 5))
		if err != nil || len(unavailable.Rows) != 0 || unavailable.Analysis == nil || !slices.Contains(unavailable.Analysis.Reasons, "normalization_unavailable") {
			t.Fatalf("unsupported normalization did not preserve an explicit incomplete outcome: %+v %v", unavailable.Analysis, err)
		}
		full := renderer.usedFullScan()
		if full || logger.countLevelContaining("INFO", "decision=inconclusive reason=normalization_unavailable column=4") != 1 {
			t.Fatalf("normalization failure did not explain its bounded limitation: windows=%v logs=%+v", renderer.durations, logger.entries)
		}
	})
	for _, tc := range []struct {
		name, filter string
		sourceIndex  int
		bounded      bool
	}{
		{"varying cadence", "select='not(eq(mod(n,10),5))',setpts=PTS-STARTPTS", 1, false},
		{"timing edit between regions", "select='not(between(t,45,46))',setpts=N/(10*TB)", 1, true},
		{"empty calibration window", "select='not(between(t,19,27))'", 0, true},
		{"empty row windows", "select='not(between(t,32.8,37.1)+between(t,62.8,67.1))'", 0, true},
		{"unverified first row", "negate=enable='between(t,15,50)'", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, "Example.Variant.mkv")
			run("-y", "-copyts", "-i", paths[0], "-vf", tc.filter, "-fps_mode", "vfr", "-c:v", "ffv1", path)
			source, err := renderer.InspectComparisonSource(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, err := comparisonVideoFingerprint(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			changed := cloneWorkflowMediaPrivateArtifacts(private)
			changed.ComparisonSources[plan.Sources[tc.sourceIndex].ID] = workflowComparisonSource{Source: source, Fingerprint: fingerprint}
			choices, _ := plan.Clone()
			choices.Sources[tc.sourceIndex].Fingerprint = fingerprint
			renderer.durations = nil
			updates = nil
			progress := b.comparisonProgress(ctx, 5)
			matched, err := b.matchWorkflowComparisons(ctx, &changed, choices, progress)
			if err != nil {
				if tc.bounded {
					t.Fatal(err)
				}
				if _, ok := errors.AsType[*api.OperationError](err); !ok {
					t.Fatalf("untyped bounded failure: %v", err)
				}
			}
			full := renderer.usedFullScan()
			if full {
				t.Fatalf("a bounded failure triggered whole-source sampling: %v", renderer.durations)
			}
			checkProgress(t, false)
			if tc.sourceIndex == 0 && len(matched.Rows) > 0 {
				media := api.MediaArtifactSet{}
				if _, err := b.captureWorkflowComparisons(ctx, api.ReleaseRef{SourcePath: path, Generation: 1}, &media, &changed, matched, progress, true); err != nil || len(media.Artifacts) == 0 {
					t.Fatalf("bounded alternate captures=%d err=%v", len(media.Artifacts), err)
				}
				checkProgress(t, true)
			}
		})
	}
	updates = nil
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := b.matchWorkflowComparisons(canceled, &private, plan, b.comparisonProgress(canceled, 5)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	checkProgress(t, false)
	if err := os.WriteFile(paths[2], []byte("changed synthetic source"), 0o600); err != nil {
		t.Fatal(err)
	}
	renderer.durations = nil
	if _, err := b.matchWorkflowComparisons(t.Context(), &private, plan, b.comparisonProgress(t.Context(), 5)); !errors.Is(err, api.ErrActiveInputChanged) || len(renderer.durations) != 0 {
		t.Fatalf("changed source reached decoder: windows=%v err=%v", renderer.durations, err)
	}
}

type comparisonCaptureStopRenderer struct {
	terminal error
	comparisonFrameRenderer
	calls int
}

type comparisonConcurrentCaptureRenderer struct {
	comparisonFrameRenderer
	mu           sync.Mutex
	gate         chan struct{}
	active, peak int
	source       string
	interleaved  bool
}

func (r *comparisonConcurrentCaptureRenderer) ComparisonCapture(ctx context.Context, request screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
	r.mu.Lock()
	if r.active > 0 && r.source != request.Source.Path {
		r.interleaved = true
	}
	r.source = request.Source.Path
	r.active++
	r.peak = max(r.peak, r.active)
	if r.active == 2 && r.gate != nil {
		close(r.gate)
		r.gate = nil
	}
	gate := r.gate
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.active--; r.mu.Unlock() }()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return screenshots.ComparisonFrame{}, fmt.Errorf("capture overlap interrupted: %w", ctx.Err())
		}
	}
	frame, err := r.comparisonFrameRenderer.ComparisonCapture(ctx, request)
	if err != nil {
		return screenshots.ComparisonFrame{}, fmt.Errorf("capture overlap fixture: %w", err)
	}
	return frame, nil
}

func (r *comparisonCaptureStopRenderer) ComparisonCapture(ctx context.Context, request screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
	r.calls++
	if r.calls == 2 {
		if r.terminal != nil {
			return screenshots.ComparisonFrame{}, r.terminal
		}
		return screenshots.ComparisonFrame{}, context.DeadlineExceeded
	}
	frame, err := r.comparisonFrameRenderer.ComparisonCapture(ctx, request)
	if err != nil {
		return frame, fmt.Errorf("capture comparison fixture: %w", err)
	}
	return frame, nil
}
