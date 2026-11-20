// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

func comparisonVideoFingerprint(ctx context.Context, path string) (string, error) {
	video, err := resolveComparisonFileVideo(ctx, path, nil)
	return video.fingerprint, err
}

func TestComparisonPublicationRejectsMutationOutsideFingerprintSample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Example.mkv")
	const sampleSize = api.SourceContentIdentitySampleBytes
	data := make([]byte, sampleSize+1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := resolveComparisonFileVideo(t.Context(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Use the legacy digest to exercise the operation-local pin independently.
	fingerprint := before.legacyFingerprint
	plan := api.ComparisonPlan{Sources: []api.ComparisonSource{{ID: "main", Fingerprint: fingerprint}}, MainSourceID: "main"}
	retained := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{"main": {Path: path, Fingerprint: fingerprint}}}
	retained.ComparisonSources, err = verifiedComparisonVideos(t.Context(), plan, retained)
	if err != nil {
		t.Fatal(err)
	}
	modified := retained.ComparisonSources["main"].Source.FileIdentity.ModTime().Add(time.Second)
	data[sampleSize] = 1
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	after, err := resolveComparisonFileVideo(t.Context(), path, nil)
	if err != nil || after.legacyFingerprint != fingerprint {
		t.Fatalf("fixture changed the sampled fingerprint: %v", err)
	}
	if err := verifyComparisonVideos(t.Context(), plan, retained); !errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("operation accepted a modified file with the same sampled fingerprint: %v", err)
	}
}

func TestLegacyComparisonGeometryRejectsReplacementBeforePublication(t *testing.T) {
	for _, action := range []string{"preview", "restore"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			retained := workflowMediaPrivateArtifacts{ComparisonSources: make(map[string]workflowComparisonSource)}
			plan := api.ComparisonPlan{MainSourceID: "main", ScreenshotSizing: api.ComparisonSizingFit}
			contents := []byte("synthetic comparison source")
			for _, id := range []string{"main", "other"} {
				path := filepath.Join(root, id+".mkv")
				if err := os.WriteFile(path, contents, 0o600); err != nil {
					t.Fatal(err)
				}
				fingerprint, err := comparisonVideoFingerprint(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				retained.ComparisonSources[id] = workflowComparisonSource{
					Path:        path,
					Fingerprint: fingerprint,
					Source:      screenshots.ComparisonSource{Path: path},
				}
				plan.Sources = append(plan.Sources, api.ComparisonSource{
					ID:          id,
					Label:       id,
					Fingerprint: fingerprint,
				})
			}
			main := retained.ComparisonSources["main"].Path
			replaceMain := func() {
				if err := os.Rename(main, filepath.Join(root, "original.mkv")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(main, contents, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			renderer := comparisonDisplayRenderer{}
			if action == "preview" {
				renderer.preview = func(req screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
					if req.OutputWidth != 180 || req.OutputHeight != 320 {
						t.Fatalf("preview did not use the refreshed main canvas: %+v", req)
					}
					replaceMain()
					return screenshots.ComparisonFrame{}, nil
				}
			} else {
				renderer.onDisplay = func(source screenshots.ComparisonSource) {
					if source.Path != main {
						replaceMain()
					}
				}
			}
			builder := workflowMediaBuilder{media: &mediaModule{}, comparisonRenderer: renderer}
			media := api.MediaArtifactSet{Comparisons: &plan}
			var err error
			if action == "preview" {
				_, err = builder.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, media, retained, "other", 1, 0, nil)
			} else {
				_, _, err = builder.RestoreComparisons(t.Context(), api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, media, retained, time.Now())
			}
			if !errors.Is(err, api.ErrActiveInputChanged) {
				t.Fatalf("%s accepted geometry from the replaced main file: %v", action, err)
			}
		})
	}
}

type comparisonDisplayRenderer struct {
	comparisonSelectionRenderer
	onDisplay func(screenshots.ComparisonSource)
}

func (r comparisonDisplayRenderer) InspectComparisonDisplay(_ context.Context, source screenshots.ComparisonSource) (screenshots.ComparisonSource, error) {
	if r.onDisplay != nil {
		r.onDisplay(source)
	}
	source.ScreenshotWidth, source.ScreenshotHeight = 180, 320
	return source, nil
}

func TestComparisonConfigureAndApplyShareFileAdmission(t *testing.T) {
	root := t.TempDir()
	main, other := filepath.Join(root, "Main.mkv"), filepath.Join(root, "Other.mkv")
	content := []byte("synthetic comparison source")
	for _, path := range []string{main, other} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := workflowMediaBuilder{
		media:              &mediaModule{},
		comparisonRenderer: comparisonConfigurationRenderer{},
		resolver:           workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{VideoPath: main}},
	}
	release := api.ReleaseRef{SourcePath: main, Generation: 1}
	selections := []api.ComparisonFrameSelection{{
		RowID:     "row",
		SourceID:  "main",
		PreviewID: "preview",
	}}
	configured, private, err := b.EditComparisons(t.Context(), release, api.TrackerReleaseProjectionSet{}, nil, nil, api.ComparisonInstructions{
		Action:          "configure",
		VideoSources:    []api.ComparisonVideoSelection{{ID: "other", Path: other}},
		FrameSelections: selections,
		Plan: api.ComparisonPlan{
			MainSourceID: "main",
			Sources:      []api.ComparisonSource{{ID: "main", Label: "Main"}, {ID: "other", Label: "Other"}},
			Rows:         []api.ComparisonRow{{ID: "row", Cells: []api.ComparisonCell{{SourceID: "main"}, {SourceID: "other"}}}},
		},
	}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(main, filepath.Join(root, "Original.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = b.ApplyComparisonFrames(t.Context(), configured, private, selections, []releaseworkflow.MediaPreviewContent{{}})
	if !errors.Is(err, api.ErrActiveInputChanged) {
		t.Fatalf("frame application accepted a replacement after configuration: %v", err)
	}
}

func TestComparisonFrameEditsDiscardObsoleteMatchTypeWarnings(t *testing.T) {
	matched := applyComparisonMatches(
		api.ComparisonPlan{MainSourceID: "main", Sources: []api.ComparisonSource{{ID: "main"}, {ID: "other", PreferBFrames: true}}},
		comparisons.MatchResult{Rows: []comparisons.MatchedRow{{Frames: []comparisons.FrameMatch{
			{
				SourceID:         "main",
				Resolved:         true,
				TimestampSeconds: 1,
				FrameType:        "B",
			},
			{
				SourceID:         "other",
				Resolved:         true,
				TimestampSeconds: 1,
				FrameType:        "P",
				TypeFallback:     true,
				Reason:           "requested_picture_type_unavailable_at_match",
			},
		}}}}, nil, comparisons.MatchOptions{},
	)
	matchWarning := matched.Rows[0].Cells[1].Warning
	const correspondence = "Review this correspondence."
	matched.Rows[0].Cells[1].Warning = correspondence + " " + matchWarning
	for _, change := range []string{"same moment", "timestamp", "picture preference"} {
		t.Run(change, func(t *testing.T) {
			edited, err := matched.Clone()
			if err != nil {
				t.Fatal(err)
			}
			want := correspondence
			switch change {
			case "timestamp":
				edited.Rows[0].Cells[1].TimestampSeconds = new(2.0)
			case "picture preference":
				edited.Sources[1].PreferBFrames = false
				edited.Sources[1].AllowedFrameTypes = []string{"I"}
			default:
				want = correspondence + " " + matchWarning
			}
			retainComparisonCellFacts(&edited, &api.MediaArtifactSet{Comparisons: &matched})
			if got := edited.Rows[0].Cells[1].Warning; got != want {
				t.Fatalf("retained warning = %q, want %q", got, want)
			}
		})
	}
	t.Run("manual correction after no compatible frame was found", func(t *testing.T) {
		unresolved := applyComparisonMatches(matched, comparisons.MatchResult{Rows: []comparisons.MatchedRow{{Frames: []comparisons.FrameMatch{
			{SourceID: "other", Reason: "no_compatible_picture_type"},
		}}}}, nil, comparisons.MatchOptions{})
		previous := &api.MediaArtifactSet{Comparisons: &unresolved}
		unchanged, err := unresolved.Clone()
		if err != nil {
			t.Fatal(err)
		}
		retainComparisonCellFacts(&unchanged, previous)
		cell := &unchanged.Rows[0].Cells[0]
		if cell.Warning != "no_compatible_picture_type" {
			t.Fatalf("unresolved frame discarded its diagnostic: %+v", cell)
		}
		cell.TimestampSeconds = new(2.0)
		retainComparisonCellFacts(&unchanged, previous)
		if cell.Warning != "" {
			t.Fatalf("manual correction retained an obsolete incompatibility: %q", cell.Warning)
		}
	})
}

func TestComparisonReconfigurationInvalidatesChangedMainAnalysis(t *testing.T) {
	for _, changed := range []string{"unchanged", "main", "other"} {
		t.Run(changed, func(t *testing.T) {
			root := t.TempDir()
			paths := map[string]string{"main": filepath.Join(root, "Example.Main.mkv"), "other": filepath.Join(root, "Example.Other.mkv")}
			renderer := comparisonConfigurationRenderer{}
			retained := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{}}
			plan := api.ComparisonPlan{MainSourceID: "main", Preferences: api.ComparisonPreferences{Count: 1, ScanMode: "deep"}}
			for _, id := range []string{"main", "other"} {
				if err := os.WriteFile(paths[id], []byte("synthetic "+id), 0o600); err != nil {
					t.Fatal(err)
				}
				fingerprint, err := comparisonVideoFingerprint(t.Context(), paths[id])
				if err != nil {
					t.Fatal(err)
				}
				facts, err := renderer.InspectComparisonSource(t.Context(), paths[id])
				if err != nil {
					t.Fatal(err)
				}
				facts.InputFingerprint = fingerprint
				retained.ComparisonSources[id] = workflowComparisonSource{
					Path:        paths[id],
					Fingerprint: fingerprint,
					Source:      facts,
				}
				plan.Sources = append(plan.Sources, api.ComparisonSource{ID: id, Fingerprint: fingerprint})
			}
			plan.Analysis = &api.ComparisonAnalysisSummary{
				Profile:         comparisons.SceneProfile,
				Complete:        true,
				RankingComplete: true,
				CoveredSeconds:  24,
			}
			plan.SceneMix = &api.ComparisonSceneMix{RequestedDark: 1, AchievedDark: 1}
			plan.Rows = []api.ComparisonRow{{
				ID:    "row",
				Scene: "dark",
				Evidence: &api.ComparisonSceneEvidence{
					Profile:  comparisons.SceneProfile,
					Eligible: true,
					Dark:     api.ComparisonSceneCategory{State: "verified"},
				},
				Cells: []api.ComparisonCell{{SourceID: "main", TimestampSeconds: new(1.0)}, {SourceID: "other", TimestampSeconds: new(1.0)}},
			}}
			retained.ComparisonInventory = &comparisonSceneInventory{SourceProfile: "retained-main-scan", Summary: *plan.Analysis}
			requested, err := plan.Clone()
			if err != nil {
				t.Fatal(err)
			}
			retainComparisonCellFacts(&requested, &api.MediaArtifactSet{Comparisons: &plan})
			if requested.Analysis == nil || requested.Rows[0].Evidence == nil {
				t.Fatal("fixture did not restore prior same-intent discovery facts")
			}
			private := cloneWorkflowMediaPrivateArtifacts(retained)
			if changed != "unchanged" {
				if err := os.WriteFile(paths[changed], []byte("replacement synthetic source binding"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Exercise the source-binding owner directly. Main DVD title changes
			// supply this same changed fingerprint within one prepared disc.
			b := workflowMediaBuilder{comparisonRenderer: renderer, resolver: workflowMediaResolverFake{screenshotSubject: &api.ScreenshotSubject{VideoPath: paths["main"]}}}
			if err := b.configureComparisonVideos(t.Context(), api.ReleaseRef{SourcePath: paths["main"], Generation: 1}, &private, &requested, nil); err != nil {
				t.Fatal(err)
			}
			if changed == "main" {
				if requested.Analysis != nil || requested.Rows[0].Evidence != nil || requested.Rows[0].Scene != "" || private.ComparisonInventory != nil {
					t.Fatal("main reconfiguration retained discovery from a different source binding")
				}
			} else if requested.Analysis == nil || requested.Rows[0].Evidence == nil || requested.Rows[0].Scene != "dark" || private.ComparisonInventory == nil {
				t.Fatal("unchanged main binding lost reusable discovery facts")
			}
			if plan.Analysis == nil || plan.Rows[0].Evidence == nil || retained.ComparisonInventory == nil {
				t.Fatal("reconfiguration mutated the prior retained generation")
			}
		})
	}
}

type comparisonConfigurationRenderer struct{ comparisonFrameRenderer }

func (comparisonConfigurationRenderer) InspectComparisonSource(_ context.Context, path string, _ ...os.FileInfo) (screenshots.ComparisonSource, error) {
	return screenshots.ComparisonSource{
		Path:                 path,
		Width:                512,
		Height:               288,
		ScreenshotWidth:      512,
		ScreenshotHeight:     288,
		DurationSeconds:      24,
		VideoDurationSeconds: 24,
		FrameCount:           600,
		FrameRate:            25,
		FrameRateNumerator:   25,
		FrameRateDenominator: 1,
	}, nil
}

func TestComparisonFrameSelectionInvalidatesOnlyChangedMainMeasurements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Example.Video.mkv")
	if err := os.WriteFile(path, []byte("synthetic comparison source"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := comparisonVideoFingerprint(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	identity := api.ComparisonFrameIdentity{
		Version:             1,
		InputFingerprint:    fingerprint,
		PTS:                 90000,
		TimeBaseNumerator:   1,
		TimeBaseDenominator: 90000,
	}
	snapshot := api.MediaArtifactSet{Comparisons: &api.ComparisonPlan{
		MainSourceID: "main",
		Sources: []api.ComparisonSource{
			{
				ID:                "main",
				Label:             "Main",
				Fingerprint:       fingerprint,
				AllowedFrameTypes: []string{"B"},
			},
			{
				ID:          "other",
				Label:       "Other",
				Fingerprint: fingerprint,
			},
		},
		Rows: []api.ComparisonRow{{
			ID:    "row",
			Scene: "dark,motion",
			Evidence: &api.ComparisonSceneEvidence{
				Profile:  comparisons.SceneProfile,
				Eligible: true,
				Dark:     api.ComparisonSceneCategory{State: "verified"},
				Motion:   api.ComparisonSceneCategory{State: "verified"},
			},
			Cells: []api.ComparisonCell{
				{
					SourceID:         "main",
					TimestampSeconds: new(1.0),
					ObservedIdentity: &identity,
				},
				{
					SourceID:         "other",
					TimestampSeconds: new(1.0),
					Confidence:       new(.99),
					ArtifactID:       "retained-other",
				},
			},
		}},
		SceneMix: &api.ComparisonSceneMix{
			RequestedDark:   1,
			RequestedMotion: 1,
			AchievedDark:    1,
			AchievedMotion:  1,
		},
	}}
	retained := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{}}
	for _, id := range []string{"main", "other"} {
		retained.ComparisonSources[id] = workflowComparisonSource{
			Source: screenshots.ComparisonSource{
				Path:                 path,
				InputFingerprint:     fingerprint,
				DurationSeconds:      10,
				Width:                1920,
				Height:               1080,
				ScreenshotWidth:      1920,
				ScreenshotHeight:     1080,
				VideoDurationSeconds: 10,
			}, Fingerprint: fingerprint,
		}
	}
	key, err := api.CanonicalWorkflowFingerprint(identity)
	if err != nil {
		t.Fatal(err)
	}
	main := retained.ComparisonSources["main"]
	main.Witnesses = map[string]screenshots.ComparisonFrameReplay{string(key): {
		Identity: identity,
		Version:  2,
		File: &screenshots.ComparisonFileInterval{
			Start:    0,
			Duration: 4,
			Slot:     1,
			FirstPTS: 0,
			LastPTS:  270000,
		},
	}}
	retained.ComparisonSources["main"] = main
	cell := &snapshot.Comparisons.Rows[0].Cells[0]
	cell.ArtifactID, cell.ContentSHA256 = "retained-main", strings.Repeat("a", 64)
	cell.PictureType = "I"
	rendering, err := comparisonRenderingFingerprint(*snapshot.Comparisons, retained, "main", config.ScreenshotHandlingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	retained.ArtifactImages = map[api.PublicResourceID]api.ScreenshotImage{cell.ArtifactID: {ContentSHA256: cell.ContentSHA256, Purpose: api.ScreenshotPurposeComparison}}
	retained.ComparisonRenderSettings = map[api.PublicResourceID]api.WorkflowFingerprint{cell.ArtifactID: rendering}
	retained.ComparisonCaptions = map[api.PublicResourceID]int{cell.ArtifactID: comparisonSourceCaptionVersion(main)}

	for _, tc := range []struct {
		name, source string
		slotDelta    int
		seconds      float64
		invalidated  bool
	}{
		{"changed main", "main", 0, 2, true},
		{"same main", "main", 0, 1, false},
		{"reinspected different slot", "main", 1, 1, true},

		{"changed secondary", "other", 0, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := identity
			observed.PTS = int64(tc.seconds * 90000)
			witness, err := json.Marshal(screenshots.ComparisonFrameReplay{
				Identity: observed,
				Version:  2,
				File: &screenshots.ComparisonFileInterval{
					Start:    0,
					Duration: 4,
					Slot:     int(tc.seconds) + tc.slotDelta,
					FirstPTS: 0,
					LastPTS:  270000,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			b := workflowMediaBuilder{comparisonRenderer: comparisonSelectionRenderer{mismatch: tc.slotDelta != 0}}
			selected, _, err := b.ApplyComparisonFrames(t.Context(), snapshot, retained,
				[]api.ComparisonFrameSelection{{RowID: "row", SourceID: tc.source}},
				[]releaseworkflow.MediaPreviewContent{{
					TimestampSeconds:  tc.seconds,
					ObservedIdentity:  &observed,
					ComparisonWitness: witness,
					PictureType:       "I",
				}})
			if err != nil {
				t.Fatal(err)
			}
			plan := selected.Comparisons
			if tc.source == "main" && (plan.Rows[0].Cells[0].ArtifactID != "") == tc.invalidated {
				t.Fatal("PNG reuse did not follow the verified physical main picture")
			}
			if tc.source == "main" && !tc.invalidated && !strings.Contains(plan.Rows[0].Cells[0].Warning, "actual type is I") {
				t.Fatal("PNG reuse lost the incompatible picture-type warning")
			}
			if tc.invalidated {
				if plan.Rows[0].Scene != "" || plan.SceneMix != nil || plan.Rows[0].Cells[1].Confidence != nil {
					t.Fatalf("new main picture retained old measurements: %+v", plan)
				}
			} else if plan.Rows[0].Scene != "dark,motion" || plan.SceneMix == nil || plan.SceneMix.AchievedDark != 1 || plan.SceneMix.AchievedMotion != 1 {
				t.Fatalf("unchanged main picture lost scene measurements: %+v", plan)
			}
			if tc.source == "main" && (plan.Rows[0].Cells[1].ArtifactID != "retained-other" ||
				!tc.invalidated && plan.Rows[0].Cells[1].Confidence == nil) {
				t.Fatal("main selection discarded unaffected secondary pixels or confidence")
			}
			if snapshot.Comparisons.Rows[0].Scene == "" || snapshot.Comparisons.Rows[0].Cells[1].Confidence == nil {
				t.Fatal("selection mutated the previous snapshot")
			}
		})
	}
	t.Run("reapplying the unchanged main retains its newly inspected guard", func(t *testing.T) {
		updated, err := snapshot.Clone()
		if err != nil {
			t.Fatal(err)
		}
		updated.Comparisons.Rows[0].Evidence = nil
		witness, err := json.Marshal(main.Witnesses[string(key)])
		if err != nil {
			t.Fatal(err)
		}
		selections := []api.ComparisonFrameSelection{{RowID: "row", SourceID: "main"}}
		contents := []releaseworkflow.MediaPreviewContent{{
			TimestampSeconds:  1,
			ObservedIdentity:  &identity,
			ComparisonWitness: witness,
		}}
		b := workflowMediaBuilder{comparisonRenderer: comparisonSelectionRenderer{}}
		verified, resource, err := b.ApplyComparisonFrames(t.Context(), updated, retained, selections, contents)
		if err != nil {
			t.Fatal(err)
		}
		row := verified.Comparisons.Rows[0]
		if row.Scene != "" || verified.Comparisons.SceneMix != nil || row.Cells[1].Confidence == nil {
			t.Fatal("fresh eligibility retained unverified scene categories or discarded the unchanged correspondence")
		}
		// The second apply must succeed even though a fresh decode would disagree.
		b.comparisonRenderer = comparisonSelectionRenderer{mismatch: true}
		_, _, err = b.ApplyComparisonFrames(t.Context(), verified, resource, selections, contents)
		if err != nil {
			t.Fatalf("the unchanged main picture lost its verified guard: %v", err)
		}
	})
	t.Run("replacing the physical main clears old pairings independently of selection order", func(t *testing.T) {
		plan, err := snapshot.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		third := plan.Sources[1]
		third.ID = "third"
		plan.Sources = append(plan.Sources, third)
		plan.Rows[0].Cells = append(plan.Rows[0].Cells, api.ComparisonCell{SourceID: "third"})
		private := cloneWorkflowMediaPrivateArtifacts(retained)
		other := private.ComparisonSources["other"]
		other.AppliedPairs = map[string]comparisonAppliedPair{"row": {Main: main.Witnesses[string(key)], Secondary: main.Witnesses[string(key)]}}
		private.ComparisonSources["other"] = other
		private.ComparisonSources["third"] = private.ComparisonSources["main"]
		witness := cloneComparisonWitness(main.Witnesses[string(key)])
		witness.File.Slot++
		data, err := json.Marshal(witness)
		if err != nil {
			t.Fatal(err)
		}
		content := releaseworkflow.MediaPreviewContent{
			TimestampSeconds:  1,
			ObservedIdentity:  &identity,
			ComparisonWitness: data,
		}
		builder := workflowMediaBuilder{comparisonRenderer: comparisonSelectionRenderer{mismatch: true}}
		_, resource, err := builder.ApplyComparisonFrames(t.Context(), api.MediaArtifactSet{Comparisons: &plan}, private, []api.ComparisonFrameSelection{{RowID: "row", SourceID: "third"}, {RowID: "row", SourceID: "main"}}, []releaseworkflow.MediaPreviewContent{content, content})
		if err != nil {
			t.Fatal(err)
		}
		updated, ok := resource.(workflowMediaPrivateArtifacts)
		if !ok || len(updated.ComparisonSources["other"].AppliedPairs) != 0 || len(updated.ComparisonSources["third"].AppliedPairs) != 1 {
			t.Fatal("main replacement retained a stale manual pair or discarded the pair selected together")
		}
	})
	t.Run("historical witness referenced by a second row cannot be redirected", func(t *testing.T) {
		plan, err := snapshot.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		plan.Rows = append(plan.Rows, api.ComparisonRow{ID: "another", Cells: []api.ComparisonCell{plan.Rows[0].Cells[0]}})
		source := main
		current := main.Witnesses[string(key)]
		source.Witnesses = map[string]screenshots.ComparisonFrameReplay{string(key): {Identity: identity}}
		_, err = (workflowMediaBuilder{}).storeComparisonWitness(t.Context(), plan, 0, "main", &source, &current)
		if failure, ok := api.AsOperationFailure(err); !ok || failure.ComparisonReason != "conflicting_frame_witness" || source.Witnesses[string(key)].Version != 0 {
			t.Fatalf("shared historical address was overwritten: %v", err)
		}
	})
	t.Run("stepping verifies the physical saved anchor", func(t *testing.T) {
		b := workflowMediaBuilder{comparisonRenderer: comparisonSelectionRenderer{mismatch: true}}
		_, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", 1, 1, nil)
		if failure, ok := api.AsOperationFailure(err); !ok || failure.ComparisonReason != "conflicting_frame_witness" {
			t.Fatalf("stepped from the wrong native occurrence: %v", err)
		}
	})
	t.Run("manual match revalidates the inspected picture", func(t *testing.T) {
		b := workflowMediaBuilder{comparisonRenderer: comparisonSelectionRenderer{mismatch: true}}
		options := comparisons.MatchOptions{Count: 1, MainMoments: []float64{1}}
		_, err := b.matchManualComparisonMoments(t.Context(), retained, *snapshot.Comparisons, options, b.comparisonProgress(t.Context(), 2))
		if failure, ok := api.AsOperationFailure(err); !ok || failure.ComparisonReason != "conflicting_frame_witness" {
			t.Fatalf("rematching accepted a different native picture at the saved timestamp: %v", err)
		}
	})
	t.Run("corrected time does not pin the previous picture", func(t *testing.T) {
		plan, err := snapshot.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		plan.Rows[0].Cells[0].TimestampSeconds = new(2.0)
		expected, err := comparisonManualReplayAt(plan, main, "main", 2, 2)
		if err != nil || expected != nil {
			t.Fatalf("the previous picture remained pinned after correcting its time: %v", err)
		}
	})
	t.Run("saved secondary anchor revalidates its picture", func(t *testing.T) {
		secondary := snapshot.Comparisons.Rows[0].Cells[1]
		secondary.ObservedIdentity = &identity
		secondary.Confidence = nil
		plan, err := snapshot.Comparisons.Clone()
		if err != nil {
			t.Fatal(err)
		}
		plan.Rows[0].Cells[1] = secondary
		observed := screenshots.ComparisonFrameObservation{Identity: &identity}
		expected, err := comparisonManualReplayAt(plan, main, "other", 1, 1)
		if err != nil || expected.File == nil || verifyManualComparisonWitness(expected, observed) == nil {
			t.Fatalf("saved secondary anchor lost its decoder or accepted a differently decoded picture: %v", err)
		}
		expected, err = comparisonManualReplayAt(plan, main, "other", 1, 2)
		if err != nil || expected != nil {
			t.Fatalf("a new timestamp-only anchor was treated as pinned: %v", err)
		}
	})
	t.Run("matched cell preview replays the retained picture", func(t *testing.T) {
		renderer := comparisonSelectionRenderer{preview: func(req screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
			if req.Replay == nil || req.Replay.File == nil || req.Replay.Identity != identity {
				t.Fatal("matched cell preview did not receive its saved native witness")
			}
			return screenshots.ComparisonFrame{Identity: &identity}, nil
		}}
		b := workflowMediaBuilder{comparisonRenderer: renderer}
		_, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", 1, 0, nil)
		if failure, ok := api.AsOperationFailure(err); !ok || failure.ComparisonReason != "conflicting_frame_witness" {
			t.Fatalf("matched cell preview returned a different native picture: %v", err)
		}
	})
}

func TestComparisonSelectedRowsKeepTheirOwnReplayWitness(t *testing.T) {
	sample := func(id string, slot int) screenshots.ComparisonSample {
		return screenshots.ComparisonSample{FrameID: id, Replay: &screenshots.ComparisonFrameReplay{File: &screenshots.ComparisonFileInterval{Slot: slot}}}
	}
	observations := map[string][]screenshots.ComparisonSample{}
	retain := func(id string, frames ...screenshots.ComparisonSample) {
		row := comparisons.MatchedRow{Frames: []comparisons.FrameMatch{{
			SourceID: "source",
			FrameID:  id,
			Resolved: true,
		}}}
		retainComparisonRowSamples(observations, map[string][]screenshots.ComparisonSample{"source": frames}, row)
	}
	retain("first", sample("first", 7), sample("second", 1))
	retain("second", sample("first", 8), sample("second", 2))
	kept := observations["source"]
	if len(kept) != 2 || kept[0].Replay.File.Slot != 7 || kept[1].Replay.File.Slot != 2 {
		t.Fatalf("an overlapping window replaced a selected picture: %+v", kept)
	}
}

// The source-selection test supplies inspected eligibility; native measurement
// and title/transition rejection are exercised by the decoder integration cases.
type comparisonSelectionRenderer struct {
	comparisonFrameRenderer
	preview  func(screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error)
	mismatch bool
}

func (rnr comparisonSelectionRenderer) AnalyzeComparisonSource(_ context.Context, r screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	frames := make([]screenshots.ComparisonSample, 0, 2)
	for _, seconds := range []float64{1, 2} {
		frames = append(frames, screenshots.ComparisonSample{
			RelativeTimestampSeconds: seconds,
			Identity: &api.ComparisonFrameIdentity{
				Version:             1,
				InputFingerprint:    r.Source.InputFingerprint,
				PTS:                 int64(seconds * 90000),
				TimeBaseNumerator:   1,
				TimeBaseDenominator: 90000,
			},
			Evidence: &api.ComparisonSceneEvidence{Profile: comparisons.SceneProfile, Eligible: true},
		})
	}
	for i := range frames {
		slot := i + 1
		if rnr.mismatch {
			slot++
		}
		frames[i].Replay = &screenshots.ComparisonFrameReplay{
			Version:  2,
			Identity: *frames[i].Identity,
			File: &screenshots.ComparisonFileInterval{
				Start:    0,
				Duration: 4,
				Slot:     slot,
				FirstPTS: 0,
				LastPTS:  270000,
			},
		}
	}
	return frames, nil
}

func (rnr comparisonSelectionRenderer) SampleComparisonSource(ctx context.Context, r screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	return rnr.AnalyzeComparisonSource(ctx, r)
}

func (rnr comparisonSelectionRenderer) ComparisonPreview(_ context.Context, request screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
	return rnr.preview(request)
}
