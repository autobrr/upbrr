// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"github.com/autobrr/upbrr/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

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

func (comparisonConfigurationRenderer) InspectComparisonSource(_ context.Context, path string) (screenshots.ComparisonSource, error) {
	return screenshots.ComparisonSource{
		Path:                 path,
		Width:                512,
		Height:               288,
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
