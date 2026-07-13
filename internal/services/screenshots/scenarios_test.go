// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/pkg/api"
)

// The screenshot lifecycle is: Plan proposes baseline slots -> Capture writes
// files named after their slot and timestamp -> the user promotes some into
// final selections -> Delete retires files. Every test below is one user-visible
// step of that lifecycle. The invariant they collectively guard is that a slot is
// "done" only while a file exists whose timestamp still matches the slot, and
// that a final selection the user picked survives a plan reload.

// Manual frames 120/360/600 at 24fps land on slots 0/1/2 at 5s/15s/25s.
const (
	scenarioFrame0 = 120
	scenarioFrame1 = 360
	scenarioFrame2 = 600
)

type scenarioFixture struct {
	root   string
	tmpDir string
	base   string
	meta   api.PreparedMetadata
}

// newScenarioFixture builds a release whose baseline is three manual frames, so
// slot timestamps are exact and tests do not depend on the auto-spacing formula.
func newScenarioFixture(t *testing.T) (*scenarioFixture, *Service) {
	t.Helper()
	root := t.TempDir()
	mediaInfoPath := writeScenarioMediaInfo(t, root, "", "24.000")

	meta := api.PreparedMetadata{
		SourcePath:        filepath.Join(root, "movie.mkv"),
		MediaInfoJSONPath: mediaInfoPath,
		ScreenshotOverrides: api.ScreenshotOverrides{
			ManualFrames: []int{scenarioFrame0, scenarioFrame1, scenarioFrame2},
		},
	}
	tmpDir, _, err := paths.ReleaseTempDir(root, meta, meta.SourcePath)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir release temp: %v", err)
	}
	return &scenarioFixture{
			root:   root,
			tmpDir: tmpDir,
			base:   screenshotBaseName(meta),
			meta:   meta,
		},
		NewServiceWithRepo(config.Config{}, api.NopLogger{}, root, nil, nil)
}

func writeScenarioMediaInfo(t *testing.T, root string, duration string, frameRate string) string {
	t.Helper()
	tracks := `{"@type":"Video","FrameRate":"` + frameRate + `"}`
	if duration != "" {
		tracks = `{"@type":"General","Duration":"` + duration + `"},` + tracks
	}
	mediaInfoPath := filepath.Join(root, "mediainfo.json")
	payload := []byte(`{"media":{"track":[` + tracks + `]}}`)
	if err := os.WriteFile(mediaInfoPath, payload, 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}
	return mediaInfoPath
}

// writeScenarioCapture plants the file Capture would have written for a slot.
func (f *scenarioFixture) writeScenarioCapture(t *testing.T, index int, timestampSeconds float64) string {
	t.Helper()
	imagePath := filepath.Join(f.tmpDir, buildScreenshotFilename(f.base, index, timestampSeconds, api.ScreenshotPurposeFinal))
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write capture: %v", err)
	}
	return imagePath
}

func (f *scenarioFixture) writeScenarioFile(t *testing.T, name string) string {
	t.Helper()
	imagePath := filepath.Join(f.tmpDir, name)
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return imagePath
}

func existingPaths(images []api.ScreenshotImage) []string {
	paths := make([]string, 0, len(images))
	for _, image := range images {
		paths = append(paths, image.Path)
	}
	return paths
}

// scenarioRepo serves stored final selections back to Plan.
type scenarioRepo struct {
	api.MetadataRepository
	finals   []api.ScreenshotFinalSelection
	screens  []api.Screenshot
	trackers []api.TrackerMetadata

	savedScreens  []api.Screenshot
	savedTrackers []api.TrackerMetadata
	deletedScreen []string
	deletedFinal  []string
}

func (r *scenarioRepo) ListScreenshotsByPath(context.Context, string) ([]api.Screenshot, error) {
	return r.screens, nil
}

func (r *scenarioRepo) ListFinalSelections(context.Context, string) ([]api.ScreenshotFinalSelection, error) {
	return r.finals, nil
}

func (r *scenarioRepo) SaveScreenshot(_ context.Context, shot api.Screenshot) error {
	r.savedScreens = append(r.savedScreens, shot)
	return nil
}

func (r *scenarioRepo) DeleteScreenshot(_ context.Context, imagePath string) error {
	r.deletedScreen = append(r.deletedScreen, imagePath)
	return nil
}

func (r *scenarioRepo) DeleteFinalSelection(_ context.Context, imagePath string) error {
	r.deletedFinal = append(r.deletedFinal, imagePath)
	return nil
}

func (r *scenarioRepo) ListTrackerMetadataByPath(context.Context, string) ([]api.TrackerMetadata, error) {
	return r.trackers, nil
}

func (r *scenarioRepo) SaveTrackerMetadata(_ context.Context, record api.TrackerMetadata) error {
	r.savedTrackers = append(r.savedTrackers, record)
	return nil
}

// --- Plan: baseline ---------------------------------------------------------

func TestPlanBaselineForFreshRelease(t *testing.T) {
	fixture, service := newScenarioFixture(t)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.SuggestedSelections) != 3 {
		t.Fatalf("expected 3 baseline slots, got %#v", plan.SuggestedSelections)
	}
	for slot, selection := range plan.SuggestedSelections {
		if selection.Index != slot {
			t.Fatalf("expected zero-based slot indices, got %#v", plan.SuggestedSelections)
		}
		if selection.Source != "manual" {
			t.Fatalf("expected manual source for frame overrides, got %#v", selection)
		}
	}
	if len(plan.ExistingScreenshots) != 0 || len(plan.FinalSelections) != 0 {
		t.Fatalf("fresh release must have nothing captured, got %#v / %#v", plan.ExistingScreenshots, plan.FinalSelections)
	}
}

func TestPlanAutoBaselineIgnoresCountWhenManualFramesGiven(t *testing.T) {
	fixture, service := newScenarioFixture(t)

	// count=10 but the user pinned 3 frames: the pinned frames win.
	plan, err := service.Plan(context.Background(), fixture.meta, 10)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.SuggestedSelections) != 3 {
		t.Fatalf("manual frames must override the requested count, got %#v", plan.SuggestedSelections)
	}
}

func TestPlanAddsOneSlotForDiscReleases(t *testing.T) {
	root := t.TempDir()
	mediaInfoPath := writeScenarioMediaInfo(t, root, "100", "25.000")
	videoTS := filepath.Join(root, "VIDEO_TS")
	if err := os.MkdirAll(videoTS, 0o700); err != nil {
		t.Fatalf("mkdir VIDEO_TS: %v", err)
	}
	if err := os.WriteFile(filepath.Join(videoTS, "VTS_01_1.VOB"), []byte(strings.Repeat("c", 99)), 0o600); err != nil {
		t.Fatalf("write VOB: %v", err)
	}
	meta := api.PreparedMetadata{SourcePath: root, DiscType: "DVD", MediaInfoJSONPath: mediaInfoPath}
	service := NewService(config.Config{}, api.NopLogger{}, t.TempDir(), nil)

	plan, err := service.Plan(context.Background(), meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Disc releases reserve an extra slot for the menu capture.
	if len(plan.SuggestedSelections) != 4 {
		t.Fatalf("expected 3 requested + 1 disc slot, got %#v", plan.SuggestedSelections)
	}
}

func TestPlanRequiresManualFramesWithoutTiming(t *testing.T) {
	root := t.TempDir()
	mediaInfoPath := writeScenarioMediaInfo(t, root, "", "0")
	meta := api.PreparedMetadata{
		SourcePath:        filepath.Join(root, "movie.mkv"),
		MediaInfoJSONPath: mediaInfoPath,
	}
	service := NewService(config.Config{}, api.NopLogger{}, root, nil)

	plan, err := service.Plan(context.Background(), meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.RequiresManualFrames {
		t.Fatalf("expected manual frames to be required without duration or frame rate, got %#v", plan)
	}
	if len(plan.SuggestedSelections) != 0 {
		t.Fatalf("expected no baseline without timing, got %#v", plan.SuggestedSelections)
	}
}

// --- Plan: which files count as "already captured" --------------------------

func TestPlanReportsMatchingCaptureAsExisting(t *testing.T) {
	fixture, service := newScenarioFixture(t)
	existing := fixture.writeScenarioCapture(t, 1, 15)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.SuggestedSelections) != 3 {
		t.Fatalf("baseline must stay whole so any slot can be reshot, got %#v", plan.SuggestedSelections)
	}
	if len(plan.ExistingScreenshots) != 1 || plan.ExistingScreenshots[0].Index != 1 || plan.ExistingScreenshots[0].Path != existing {
		t.Fatalf("expected slot 1 reported as captured, got %#v", plan.ExistingScreenshots)
	}
}

func TestPlanIgnoresCaptureWhoseTimestampNoLongerMatchesItsSlot(t *testing.T) {
	fixture, service := newScenarioFixture(t)
	// Slot 1 is 15s. This file claims slot 1 but was shot at 90s: the user
	// retimed the slot, so the stale file must not mark it done.
	fixture.writeScenarioCapture(t, 1, 90)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.ExistingScreenshots) != 0 {
		t.Fatalf("retimed slot must not count as captured, got %#v", plan.ExistingScreenshots)
	}
}

func TestPlanIgnoresCaptureOutsideTheBaseline(t *testing.T) {
	fixture, service := newScenarioFixture(t)
	// Left over from a run that asked for more screenshots than this one does.
	fixture.writeScenarioCapture(t, 7, 300)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.ExistingScreenshots) != 0 {
		t.Fatalf("orphan slot must not count as captured, got %#v", plan.ExistingScreenshots)
	}
}

func TestPlanIgnoresPreviewAndDiscMenuFilesAsCaptures(t *testing.T) {
	fixture, service := newScenarioFixture(t)
	fixture.writeScenarioFile(t, fixture.base+"-preview-01-ss_00015000-123456.png")
	fixture.writeScenarioFile(t, fixture.base+"-dvd-menu-01-123456.png")

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.ExistingScreenshots) != 0 {
		t.Fatalf("preview and menu files are not slot captures, got %#v", plan.ExistingScreenshots)
	}
}

// A deleted screenshot leaves its database row behind, and the slot must still come
// back as pending so the user can regenerate it.
func TestPlanTreatsDeletedCaptureAsRegenerable(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	deleted := filepath.Join(fixture.tmpDir, buildScreenshotFilename(fixture.base, 1, 15, api.ScreenshotPurposeFinal))
	repo := &scenarioRepo{screens: []api.Screenshot{{SourcePath: fixture.meta.SourcePath, ImagePath: deleted, Timestamp: 15}}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.SuggestedSelections) != 3 {
		t.Fatalf("deleted slot must remain in the baseline, got %#v", plan.SuggestedSelections)
	}
	if len(plan.ExistingScreenshots) != 0 {
		t.Fatalf("a database row without a file on disk is not a capture, got %#v", plan.ExistingScreenshots)
	}
}

// --- Plan: final selections survive a reload --------------------------------

func TestPlanKeepsFinalSelectionsInSavedOrder(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	slot0 := fixture.writeScenarioCapture(t, 0, 5)
	slot1 := fixture.writeScenarioCapture(t, 1, 15)
	slot2 := fixture.writeScenarioCapture(t, 2, 25)

	// The user dragged slot 2's shot to the front. Order is the list position,
	// not the capture slot, so a reload must not confuse the two.
	repo := &scenarioRepo{finals: []api.ScreenshotFinalSelection{
		{SourcePath: fixture.meta.SourcePath, ImagePath: slot2, Order: 0},
		{SourcePath: fixture.meta.SourcePath, ImagePath: slot0, Order: 1},
		{SourcePath: fixture.meta.SourcePath, ImagePath: slot1, Order: 2},
	}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := existingPaths(plan.FinalSelections); len(got) != 3 {
		t.Fatalf("reordering final selections must not drop them, got %#v", got)
	}
	if plan.FinalSelections[0].Path != slot2 {
		t.Fatalf("expected the reordered pick to stay first, got %#v", existingPaths(plan.FinalSelections))
	}
}

func TestPlanKeepsDiscMenuFinalSelection(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	slot0 := fixture.writeScenarioCapture(t, 0, 5)
	// Menu captures carry no timestamp in their filename and are not baseline
	// slots, so a timestamp/slot filter must not evict them.
	menu := fixture.writeScenarioFile(t, fixture.base+"-dvd-menu-01-123456.png")

	repo := &scenarioRepo{finals: []api.ScreenshotFinalSelection{
		{SourcePath: fixture.meta.SourcePath, ImagePath: menu, Order: 0, Source: api.ScreenshotSelectionSourceMenu},
		{SourcePath: fixture.meta.SourcePath, ImagePath: slot0, Order: 1},
	}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.FinalSelections) != 2 {
		t.Fatalf("disc menu selection must survive a plan reload, got %#v", existingPaths(plan.FinalSelections))
	}
	if plan.FinalSelections[0].Path != menu || plan.FinalSelections[0].Purpose != api.ScreenshotPurposeMenu {
		t.Fatalf("expected the menu image kept and tagged, got %#v", plan.FinalSelections[0])
	}
}

func TestPlanDropsFinalSelectionWhoseFrameLeftTheBaseline(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	slot0 := fixture.writeScenarioCapture(t, 0, 5)
	// Shot at 90s, which is no longer any slot in the baseline.
	stale := fixture.writeScenarioCapture(t, 1, 90)

	repo := &scenarioRepo{finals: []api.ScreenshotFinalSelection{
		{SourcePath: fixture.meta.SourcePath, ImagePath: slot0, Order: 0},
		{SourcePath: fixture.meta.SourcePath, ImagePath: stale, Order: 1},
	}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.FinalSelections) != 1 || plan.FinalSelections[0].Path != slot0 {
		t.Fatalf("expected only the in-baseline pick kept, got %#v", existingPaths(plan.FinalSelections))
	}
}

func TestPlanSkipsFinalSelectionOutsideTempDir(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.png")
	if err := os.WriteFile(outside, []byte("png"), 0o600); err != nil {
		t.Fatalf("write outside image: %v", err)
	}
	repo := &scenarioRepo{finals: []api.ScreenshotFinalSelection{
		{SourcePath: fixture.meta.SourcePath, ImagePath: outside, Order: 0},
	}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.FinalSelections) != 0 {
		t.Fatalf("a selection outside the release temp dir must be ignored, got %#v", existingPaths(plan.FinalSelections))
	}
}

// --- Capture ----------------------------------------------------------------

func newCaptureService(t *testing.T, fixture *scenarioFixture, repo api.MetadataRepository) *Service {
	t.Helper()
	ffmpegRoot := t.TempDir()
	if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
		t.Fatalf("write bundled ffmpeg: %v", err)
	}
	t.Chdir(ffmpegRoot)
	runner := &writeOutputRunner{payload: testPNGBytes(t, color.RGBA{R: 180, G: 180, B: 180, A: 255})}
	return NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, runner, repo)
}

func TestCaptureRejectsEmptySelections(t *testing.T) {
	fixture, service := newScenarioFixture(t)

	_, err := service.Capture(context.Background(), fixture.meta, nil, api.ScreenshotPurposeFinal)
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input for an empty capture request, got %v", err)
	}
}

func TestCaptureReportsInvalidTimestampWithoutFailingTheBatch(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	service := newCaptureService(t, fixture, nil)

	result, err := service.Capture(context.Background(), fixture.meta, []api.ScreenshotSelection{
		{Index: 0, TimestampSeconds: 5},
		{Index: 1}, // no timestamp and no frame: unusable
	}, api.ScreenshotPurposeFinal)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(result.Images) != 1 || result.Images[0].Index != 0 {
		t.Fatalf("expected the usable selection to still be captured, got %#v", result.Images)
	}
	if len(result.Errors) != 1 || result.Errors[0].Index != 1 {
		t.Fatalf("expected the unusable selection reported as an error, got %#v", result.Errors)
	}
}

func TestCaptureDerivesTimestampFromFrameNumber(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	service := newCaptureService(t, fixture, nil)

	result, err := service.Capture(context.Background(), fixture.meta, []api.ScreenshotSelection{
		{Index: 0, Frame: scenarioFrame1}, // 360 @ 24fps = 15s
	}, api.ScreenshotPurposeFinal)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(result.Images) != 1 || result.Images[0].TimestampSeconds != 15 {
		t.Fatalf("expected frame 360 at 24fps to resolve to 15s, got %#v", result.Images)
	}
	if !strings.Contains(filepath.Base(result.Images[0].Path), "ss_00015000") {
		t.Fatalf("expected the timestamp encoded in the filename, got %q", result.Images[0].Path)
	}
}

func TestCapturePersistsFinalsButNotPreviews(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	repo := &scenarioRepo{}
	service := newCaptureService(t, fixture, repo)
	selections := []api.ScreenshotSelection{{Index: 0, TimestampSeconds: 5}}

	preview, err := service.Capture(context.Background(), fixture.meta, selections, api.ScreenshotPurposePreview)
	if err != nil {
		t.Fatalf("capture preview: %v", err)
	}
	if !strings.Contains(filepath.Base(preview.Images[0].Path), "-preview-") {
		t.Fatalf("expected a preview filename, got %q", preview.Images[0].Path)
	}
	if len(repo.savedScreens) != 0 {
		t.Fatalf("previews are throwaway and must not be persisted, got %#v", repo.savedScreens)
	}

	if _, err := service.Capture(context.Background(), fixture.meta, selections, api.ScreenshotPurposeFinal); err != nil {
		t.Fatalf("capture final: %v", err)
	}
	if len(repo.savedScreens) != 1 {
		t.Fatalf("expected the final capture persisted, got %#v", repo.savedScreens)
	}
}

// A preview file never lands in ExistingScreenshots, so promoting one leaves its
// slot pending server-side. The clients compensate; this pins the server contract.
func TestPlanDoesNotTreatPromotedPreviewAsSlotCapture(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	previewPath := fixture.writeScenarioFile(t, buildScreenshotFilename(fixture.base, 1, 15, api.ScreenshotPurposePreview))
	repo := &scenarioRepo{finals: []api.ScreenshotFinalSelection{
		{SourcePath: fixture.meta.SourcePath, ImagePath: previewPath, Order: 0},
	}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.ExistingScreenshots) != 0 {
		t.Fatalf("a promoted preview is not a slot capture, got %#v", plan.ExistingScreenshots)
	}
	if len(plan.FinalSelections) != 1 || plan.FinalSelections[0].Path != previewPath {
		t.Fatalf("a promoted preview must survive as a final selection, got %#v", existingPaths(plan.FinalSelections))
	}
}

// --- Delete -----------------------------------------------------------------

func TestDeleteRemovesFileAndDatabaseRows(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	repo := &scenarioRepo{}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)
	target := fixture.writeScenarioCapture(t, 1, 15)

	if err := service.Delete(context.Background(), fixture.meta, target); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected the file gone from disk, stat err = %v", err)
	}
	if len(repo.deletedScreen) != 1 || len(repo.deletedFinal) != 1 {
		t.Fatalf("expected both the screenshot row and the final selection removed, got %#v / %#v", repo.deletedScreen, repo.deletedFinal)
	}
}

// The GUI reloads after deleting, so a second delete of the same path is normal.
func TestDeleteIsIdempotentForMissingFiles(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	repo := &scenarioRepo{}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)
	target := filepath.Join(fixture.tmpDir, buildScreenshotFilename(fixture.base, 1, 15, api.ScreenshotPurposeFinal))

	if err := service.Delete(context.Background(), fixture.meta, target); err != nil {
		t.Fatalf("deleting an already-missing file must not fail: %v", err)
	}
	if len(repo.deletedScreen) != 1 {
		t.Fatalf("expected the database row cleaned even when the file was gone, got %#v", repo.deletedScreen)
	}
}

func TestDeleteRejectsPathsOutsideTheReleaseTempDir(t *testing.T) {
	fixture, service := newScenarioFixture(t)
	sibling := fixture.tmpDir + ".evil"
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}

	cases := map[string]string{
		"empty":              "",
		"whitespace":         "   ",
		"traversal":          filepath.Join(fixture.tmpDir, "..", "escape.png"),
		"sibling prefix dir": filepath.Join(sibling, "shot.png"),
		"absolute elsewhere": filepath.Join(t.TempDir(), "shot.png"),
		"not an image":       filepath.Join(fixture.tmpDir, "notes.txt"),
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			if err := service.Delete(context.Background(), fixture.meta, target); !errors.Is(err, internalerrors.ErrInvalidInput) {
				t.Fatalf("expected invalid input, got %v", err)
			}
		})
	}
}

// --- Tracker images ---------------------------------------------------------

// trackerFixture wires a release whose tracker has one already-uploaded image, and
// returns the local path that image is mirrored to.
func trackerFixture(t *testing.T, fixture *scenarioFixture) (api.TrackerMetadata, string) {
	t.Helper()
	record := api.TrackerMetadata{
		SourcePath: fixture.meta.SourcePath,
		Tracker:    "UTP",
		ImageURLs:  []string{"https://images.example/foo.png"},
	}
	fixture.meta.TrackerData = []api.TrackerMetadata{record}
	mirrored := filepath.Join(fixture.tmpDir, "utp", "foo_01.png")
	if err := os.MkdirAll(filepath.Dir(mirrored), 0o700); err != nil {
		t.Fatalf("mkdir tracker dir: %v", err)
	}
	if err := os.WriteFile(mirrored, []byte("png"), 0o600); err != nil {
		t.Fatalf("write tracker image: %v", err)
	}
	return record, mirrored
}

func TestPlanLinksTrackerImagesAndAutoSelectsThem(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	_, mirrored := trackerFixture(t, fixture)
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, &scenarioRepo{})

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.TrackerImageLinks) != 1 || plan.TrackerImageLinks[0].Path != mirrored {
		t.Fatalf("expected the uploaded image linked to its local mirror, got %#v", plan.TrackerImageLinks)
	}
	// A linked image is already accounted for, so it is not offered again as a loose file.
	if len(plan.ExistingTrackerScreenshots) != 0 {
		t.Fatalf("a linked tracker image must not also be listed as unlinked, got %#v", existingPaths(plan.ExistingTrackerScreenshots))
	}
	if len(plan.FinalSelections) != 1 || plan.FinalSelections[0].Path != mirrored {
		t.Fatalf("tracker images are auto-included in final selections, got %#v", existingPaths(plan.FinalSelections))
	}
}

func TestPlanKeepsTrackerImageInItsSavedPosition(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	_, mirrored := trackerFixture(t, fixture)
	slot0 := fixture.writeScenarioCapture(t, 0, 5)

	// The user put the tracker image first. It has no timestamp and no slot, so a
	// reload must keep it where they put it rather than re-appending it at the end.
	repo := &scenarioRepo{finals: []api.ScreenshotFinalSelection{
		{SourcePath: fixture.meta.SourcePath, ImagePath: mirrored, Order: 0},
		{SourcePath: fixture.meta.SourcePath, ImagePath: slot0, Order: 1},
	}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)

	plan, err := service.Plan(context.Background(), fixture.meta, 3)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got := existingPaths(plan.FinalSelections); len(got) != 2 || got[0] != mirrored {
		t.Fatalf("expected the tracker image kept in first position, got %#v", got)
	}
}

func TestDeleteDropsTheTrackerImageURLItMirrors(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	record, mirrored := trackerFixture(t, fixture)
	repo := &scenarioRepo{trackers: []api.TrackerMetadata{record}}
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, repo)

	if err := service.Delete(context.Background(), fixture.meta, mirrored); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(repo.savedTrackers) != 1 {
		t.Fatalf("expected the tracker record rewritten, got %#v", repo.savedTrackers)
	}
	if len(repo.savedTrackers[0].ImageURLs) != 0 {
		t.Fatalf("deleting the mirrored file must drop its uploaded URL, got %#v", repo.savedTrackers[0].ImageURLs)
	}
}

// --- SaveFinalSelections ----------------------------------------------------

func TestSaveFinalSelectionsRejectsUntrustedPaths(t *testing.T) {
	fixture, _ := newScenarioFixture(t)
	service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, fixture.root, nil, &scenarioRepo{})

	cases := map[string][]api.ScreenshotImage{
		"empty path":    {{Path: ""}},
		"outside tmp":   {{Path: filepath.Join(t.TempDir(), "shot.png")}},
		"bad extension": {{Path: filepath.Join(fixture.tmpDir, "shot.exe")}},
	}
	for name, images := range cases {
		t.Run(name, func(t *testing.T) {
			if err := service.SaveFinalSelections(context.Background(), fixture.meta, images); !errors.Is(err, internalerrors.ErrInvalidInput) {
				t.Fatalf("expected invalid input, got %v", err)
			}
		})
	}
}
