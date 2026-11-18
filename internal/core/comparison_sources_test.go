// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
				ID:          "main",
				Label:       "Main",
				Fingerprint: fingerprint,
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
		Identity:        identity,
		PictureChecksum: "12345678",
		Decoder:         "cuda",
	}}
	retained.ComparisonSources["main"] = main
	for _, tc := range []struct {
		name, source, checksum string
		seconds                float64
		invalidated            bool
	}{
		{"changed main", "main", "12345678", 2, true},
		{"same main", "main", "12345678", 1, false},
		{"same timestamp changed picture", "main", "87654321", 1, true},
		{"changed secondary", "other", "12345678", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := identity
			observed.PTS = int64(tc.seconds * 90000)
			witness, err := json.Marshal(screenshots.ComparisonFrameReplay{Identity: observed, PictureChecksum: tc.checksum})
			if err != nil {
				t.Fatal(err)
			}
			b := workflowMediaBuilder{comparisonRenderer: comparisonSelectionRenderer{checksum: tc.checksum}}
			selected, _, err := b.ApplyComparisonFrames(t.Context(), snapshot, retained,
				[]api.ComparisonFrameSelection{{RowID: "row", SourceID: tc.source}},
				[]releaseworkflow.MediaPreviewContent{{
					TimestampSeconds:  tc.seconds,
					ObservedIdentity:  &observed,
					ComparisonWitness: witness,
				}})
			if err != nil {
				t.Fatal(err)
			}
			plan := selected.Comparisons
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
	t.Run("manual match revalidates the inspected picture", func(t *testing.T) {
		b := workflowMediaBuilder{comparisonRenderer: comparisonSelectionRenderer{checksum: "87654321", checkDecoder: func(decoder string) {
			if decoder != "cuda" {
				t.Fatal("manual eligibility did not use the saved decoder")
			}
		}}}
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
		observed := screenshots.ComparisonFrameObservation{Identity: &identity, PictureChecksum: "87654321"}
		expected, err := comparisonManualReplayAt(plan, main, "other", 1, 1)
		if err != nil || expected.PreferredDecoder() != "cuda" || verifyManualComparisonWitness(expected, observed) == nil {
			t.Fatalf("saved secondary anchor lost its decoder or accepted a differently decoded picture: %v", err)
		}
		expected, err = comparisonManualReplayAt(plan, main, "other", 1, 2)
		if err != nil || expected != nil {
			t.Fatalf("a new timestamp-only anchor was treated as pinned: %v", err)
		}
	})
	t.Run("matched cell preview replays the retained picture", func(t *testing.T) {
		renderer := comparisonSelectionRenderer{preview: func(req screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
			if req.Replay == nil || req.Replay.Decoder != "cuda" || req.Replay.PictureChecksum != "12345678" || req.Replay.Identity != identity {
				t.Fatal("matched cell preview did not receive its saved native witness")
			}
			return screenshots.ComparisonFrame{Identity: &identity, PictureChecksum: "87654321"}, nil
		}}
		b := workflowMediaBuilder{comparisonRenderer: renderer}
		_, err := b.PreviewComparisonFrame(t.Context(), api.ReleaseRef{}, snapshot, retained, "main", 1, 0)
		if failure, ok := api.AsOperationFailure(err); !ok || failure.ComparisonReason != "conflicting_frame_witness" {
			t.Fatalf("matched cell preview returned a different native picture: %v", err)
		}
	})
}

func TestComparisonSelectedRowsKeepTheirOwnReplayWitness(t *testing.T) {
	sample := func(id, checksum string) screenshots.ComparisonSample {
		return screenshots.ComparisonSample{FrameID: id, PictureChecksum: checksum}
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
	retain("first", sample("first", "12345678"), sample("second", "11111111"))
	retain("second", sample("first", "87654321"), sample("second", "22222222"))
	kept := observations["source"]
	if len(kept) != 2 || kept[0].PictureChecksum != "12345678" || kept[1].PictureChecksum != "22222222" {
		t.Fatalf("an overlapping window replaced a selected picture: %+v", kept)
	}
}

// The source-selection test supplies inspected eligibility; native measurement
// and title/transition rejection are exercised by the decoder integration cases.
type comparisonSelectionRenderer struct {
	comparisonFrameRenderer
	checksum     string
	preview      func(screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error)
	checkDecoder func(string)
}

func (rnr comparisonSelectionRenderer) AnalyzeComparisonSource(_ context.Context, r screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	if rnr.checkDecoder != nil {
		rnr.checkDecoder(r.PreferredDecoder)
	}
	frames := make([]screenshots.ComparisonSample, 0, 2)
	for _, seconds := range []float64{1, 2} {
		frames = append(frames, screenshots.ComparisonSample{
			RelativeTimestampSeconds: seconds,
			PictureChecksum:          rnr.checksum,
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
	return frames, nil
}

func (rnr comparisonSelectionRenderer) ComparisonPreview(_ context.Context, request screenshots.ComparisonCaptureRequest) (screenshots.ComparisonFrame, error) {
	return rnr.preview(request)
}
