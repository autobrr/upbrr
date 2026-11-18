// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonInventoryReadCounter struct {
	io.ReaderAt
	bytes int64
}

func (r *comparisonInventoryReadCounter) ReadAt(data []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(data, offset)
	r.bytes += int64(n)
	return n, err //nolint:wrapcheck // This transparent counter preserves the io.ReaderAt EOF contract.
}

func TestComparisonInventoryLimitKeepsReadableCompletedFrames(t *testing.T) {
	var output bytes.Buffer
	ref := &comparisonSceneInventory{}
	encoder := comparisonInventoryEncoder{destination: &output, ref: ref}
	for index := range 256 {
		if err := encoder.write(screenshots.ComparisonSample{FrameID: fmt.Sprintf("main-%d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	// Model the final committed batch exhausting the on-disk allowance. The
	// following member must be discarded whole, leaving prior frames readable.
	ref.Bytes = comparisonInventoryLimit - 1
	if err := encoder.write(screenshots.ComparisonSample{FrameID: "uncommitted"}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.flush(); !errors.Is(err, errComparisonInventoryFull) {
		t.Fatalf("inventory bound was not enforced: %v", err)
	}
	reader, err := gzip.NewReader(&output)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	completed, err := io.ReadAll(reader)
	if err != nil || bytes.Count(completed, []byte{'\n'}) != 256 || bytes.Contains(completed, []byte("uncommitted")) {
		t.Fatalf("inventory cap damaged completed native frames: %v", err)
	}
}

func TestComparisonDeepInventoryCompletesReranksAndRestores(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Example.mkv")
	if err := os.WriteFile(path, []byte("synthetic source identity"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := comparisonVideoFingerprint(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	b := workflowMediaBuilder{config: config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "app.sqlite")}}, media: &mediaModule{}}
	retained := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{"main": {
		Path:        path,
		Fingerprint: fingerprint,
		Source: screenshots.ComparisonSource{
			Path:                 path,
			InputFingerprint:     fingerprint,
			Width:                512,
			Height:               288,
			DurationSeconds:      24,
			VideoDurationSeconds: 24,
			HDRFacts:             api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatSDR}},
		},
	}}, screenshotSubject: api.ScreenshotSubject{SourcePath: path}}
	plan := api.ComparisonPlan{
		MainSourceID: "main",
		Sources:      []api.ComparisonSource{{ID: "main"}},
		Preferences: api.ComparisonPreferences{
			Count:     1,
			FlatCount: 1,
			ScanMode:  "deep",
		},
	}
	window, excluded, err := comparisons.SelectionRange(plan.Preferences, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	decode := func(_ context.Context, start, duration float64, deep bool) ([]screenshots.ComparisonSample, error) {
		calls++
		if !deep || duration > 34 {
			return nil, errors.New("discovery escaped bounded main chunks")
		}
		var frames []screenshots.ComparisonSample
		for index := 0; index < int(math.Round(duration*25)); index++ {
			time := start + float64(index)/25
			identity := api.ComparisonFrameIdentity{
				Version:             1,
				InputFingerprint:    fingerprint,
				Occurrence:          "file-0",
				StreamID:            "video-0",
				PTS:                 int64(math.Round(time * 1000)),
				TimeBaseNumerator:   1,
				TimeBaseDenominator: 1000,
			}
			time = float64(identity.PTS) / float64(identity.TimeBaseDenominator)
			key, _ := api.CanonicalWorkflowFingerprint(identity)
			frames = append(frames, screenshots.ComparisonSample{
				AnalysisAnchor: index == 50,
				FrameID:        string(key),
				Identity:       &identity,
				Replay: &screenshots.ComparisonFrameReplay{
					Identity: identity,
					Version:  2,
					File: &screenshots.ComparisonFileInterval{
						PacketPosition: new(identity.PTS * 32),
						Start:          start,
						Duration:       duration,
						Slot:           index,
						FirstPTS:       int64(math.Round(start * 1000)),
						LastPTS:        int64(math.Round((start+duration)*1000)) - 40,
					},
				},
				PTS:                      identity.PTS,
				DurationTicks:            40,
				TimeBaseNumerator:        1,
				TimeBaseDenominator:      1000,
				RelativeTimestampSeconds: time,
				PictureType:              "I",
				Pixels:                   make([]byte, 256),
				Evidence: &api.ComparisonSceneEvidence{
					Profile:       comparisons.SceneProfile,
					Eligible:      true,
					Motion:        api.ComparisonSceneCategory{State: "verified", Score: .5},
					Dark:          api.ComparisonSceneCategory{State: "verified", Score: .5},
					Flat:          api.ComparisonSceneCategory{State: "verified", Score: time / 24},
					MovingSupport: time / 24,
					Mean:          (24 - time) / 24,
				},
			})
		}
		for i, f := range frames {
			if i > 0 {
				prev := frames[i-1]
				f.Replay.File.Previous = &screenshots.ComparisonFileNeighbor{PTS: prev.PTS, Position: *prev.Replay.File.PacketPosition}
			}
			if i+1 < len(frames) {
				next := frames[i+1]
				f.Replay.File.Next = &screenshots.ComparisonFileNeighbor{PTS: next.PTS, Position: *next.Replay.File.PacketPosition}
			}
		}

		return frames, nil
	}
	ref, err := b.discoverComparisonScenes(t.Context(), &retained, plan, window, excluded, decode, b.comparisonProgress(t.Context(), 5))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !ref.Summary.Complete || !ref.Summary.RankingComplete || ref.Summary.CoveredSeconds != 24 {
		t.Fatalf("incomplete main discovery: calls=%d summary=%+v", calls, ref.Summary)
	}
	_, payload, err := retained.MarshalPrivateResource()
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > 16<<10 {
		t.Fatal("private clones duplicated the inventory")
	}
	decoded, err := decodeWorkflowMediaPrivateArtifacts(b, payload)
	if err != nil {
		t.Fatal(err)
	}
	cloned := cloneWorkflowMediaPrivateArtifacts(decoded)
	if cloned.ComparisonInventory == decoded.ComparisonInventory || cloned.ComparisonInventory.Path != ref.Path {
		t.Fatal("inventory reference clone is mutable or lost its file")
	}
	if len(ref.Blocks) == 0 || !slices.Equal(cloned.ComparisonInventory.Blocks, ref.Blocks) ||
		cloned.ComparisonInventory.IndexSHA256 != ref.IndexSHA256 {
		t.Fatal("restart lost the native batch directory")
	}
	indexClone := cloneComparisonInventory(ref)
	indexClone.Blocks[0].SHA256 = "changed"
	if ref.Blocks[0].SHA256 == "changed" {
		t.Fatal("inventory clones share mutable batch metadata")
	}
	plan.Preferences.FlatCount = 0
	// An earlier version attached a stopped matching attempt to complete scan
	// facts. Reuse must report only discovery facts for the new attempt.
	cloned.ComparisonInventory.Summary.Reasons = []string{"match_deadline"}
	reused, err := b.discoverComparisonScenes(t.Context(), &cloned, plan, window, excluded, decode, b.comparisonProgress(t.Context(), 5))
	if err != nil || calls != 2 {
		t.Fatal("a count change repeated complete main discovery")
	}
	if len(reused.Summary.Reasons) != 0 {
		t.Fatal("a new attempt retained historical matching failure")
	}
	owner, err := b.comparisonInventoryOwner(cloned, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int64{1, 64 << 20} {
		index, reserved, err := comparisonInventoryCandidates(t.Context(), ref, owner, budget)
		if err != nil || reserved > budget || budget == 1 && (index != nil || reserved != 0) {
			t.Fatalf("ranking exceeded its shared allowance: bytes=%d budget=%d err=%v", reserved, budget, err)
		}
		for category := range 3 {
			visited := 0
			targets := [3]int{}
			targets[category] = 1
			chosen, _, ok := comparisons.SelectScene(func(yield func(comparisons.FrameSample) bool) {
				if index != nil {
					for _, frame := range index {
						visited++
						if !yield(frame.sample()) {
							break
						}
					}
					return
				}
				if err := readComparisonInventory(t.Context(), ref, owner, func(frame screenshots.ComparisonSample) bool {
					visited++
					return yield(comparisonSamples([]screenshots.ComparisonSample{frame})[0])
				}); err != nil {
					t.Fatal(err)
				}
			}, map[string]bool{}, nil, targets, [3]int{}, targets, 1)
			if visited != 600 || !ref.Compressed || !ok || math.Abs(chosen.TimestampSeconds-23.96) > 1e-9 {
				t.Fatalf("bounded ranking lost late native evidence: category=%d visited=%d chosen=%+v", category, visited, chosen)
			}
		}
	}
	var windowFrames []screenshots.ComparisonSample
	if err := readComparisonInventoryRange(t.Context(), decoded.ComparisonInventory, owner, &api.ComparisonTimeInterval{Start: 12, End: 13}, func(frame screenshots.ComparisonSample) bool {
		windowFrames = append(windowFrames, frame)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(windowFrames) != 25 || windowFrames[0].RelativeTimestampSeconds != 12 || windowFrames[24].RelativeTimestampSeconds != 12.96 ||
		windowFrames[0].Replay == nil || windowFrames[0].Replay.File == nil || windowFrames[0].Identity.PTS != 12000 {
		t.Fatal("bounded inventory read lost native pictures or their replay witnesses")
	}
	compressedInventory, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	lookupWindow := api.ComparisonTimeInterval{Start: 12, End: 13}
	counted := &comparisonInventoryReadCounter{ReaderAt: bytes.NewReader(compressedInventory)}
	var directFrames []screenshots.ComparisonSample
	if err := readIndexedComparisonInventory(t.Context(), counted, ref, lookupWindow, func(frame screenshots.ComparisonSample) bool {
		directFrames = append(directFrames, frame)
		return true
	}); err != nil || counted.bytes != ref.Blocks[1].Bytes || counted.bytes >= ref.Bytes || !reflect.DeepEqual(directFrames, windowFrames) {
		t.Fatalf("native window read escaped its one required batch: bytes=%d total=%d frames=%d err=%v", counted.bytes, ref.Bytes, len(directFrames), err)
	}
	t.Run("legacy inventory acquires indexed access without main decoding", func(t *testing.T) {
		legacyPrivate := cloneWorkflowMediaPrivateArtifacts(cloned)
		legacyPrivate.ComparisonInventory.Blocks = nil
		legacyPrivate.ComparisonInventory.IndexSHA256 = ""
		before := calls
		indexed, err := b.discoverComparisonScenes(t.Context(), &legacyPrivate, plan, window, excluded, decode, b.comparisonProgress(t.Context(), 5))
		if err != nil || calls != before || !slices.Equal(indexed.Blocks, ref.Blocks) || indexed.IndexSHA256 != ref.IndexSHA256 {
			t.Fatalf("legacy index rebuild lost evidence or repeated main discovery: calls=%d err=%v", calls-before, err)
		}
	})
	t.Run("cancelled reuse preserves complete inventory", func(t *testing.T) {
		for _, deadline := range []bool{false, true} {
			private := cloneWorkflowMediaPrivateArtifacts(cloned)
			private.ComparisonInventory.Blocks = nil
			private.ComparisonInventory.IndexSHA256 = ""
			original := private.ComparisonInventory
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Time{})
			} else {
				cancel()
			}
			before := calls
			_, err := b.discoverComparisonScenes(ctx, &private, plan, window, excluded, decode, b.comparisonProgress(t.Context(), 5))
			cancel()
			if (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) || private.ComparisonInventory != original || calls != before {
				t.Fatalf("cancelled reuse replaced complete scan: deadline=%t err=%v", deadline, err)
			}
			if _, err := b.discoverComparisonScenes(t.Context(), &private, plan, window, excluded, decode, b.comparisonProgress(t.Context(), 5)); err != nil || calls != before {
				t.Fatalf("retry repeated complete main discovery: %v", err)
			}
		}
	})
	t.Run("batch and directory corruption remain rejected", func(t *testing.T) {
		changedIndex := cloneComparisonInventory(ref)
		changedIndex.Blocks[1].End++
		if err := readComparisonInventoryRange(t.Context(), changedIndex, owner, &lookupWindow, func(screenshots.ComparisonSample) bool { return true }); err == nil {
			t.Fatal("changed directory escaped its bound checksum")
		}
		sealComparisonInventoryIndex(changedIndex)
		if err := readIndexedComparisonInventory(t.Context(), bytes.NewReader(compressedInventory), changedIndex, lookupWindow, func(screenshots.ComparisonSample) bool { return true }); err == nil {
			t.Fatal("batch contents disagreed with declared native bounds")
		}
		changedBatch := slices.Clone(compressedInventory)
		changedBatch[ref.Blocks[1].Offset+ref.Blocks[1].Bytes/2] ^= 1
		if err := readIndexedComparisonInventory(t.Context(), bytes.NewReader(changedBatch), ref, lookupWindow, func(screenshots.ComparisonSample) bool { return true }); err == nil {
			t.Fatal("changed selected native batch escaped verification")
		}
		// A section lookup never reads an unrelated batch. Full validation at
		// reuse/publication still rejects corruption outside that section.
		unrelated := slices.Clone(compressedInventory)
		unrelated[0] ^= 1
		if err := os.WriteFile(ref.Path, unrelated, 0o600); err != nil {
			t.Fatal(err)
		}
		selected := *windowFrames[0].Identity
		selected.PTS = 14000
		selectedID, _ := api.CanonicalWorkflowFingerprint(selected)
		anchor, frames, err := comparisonInventoryAnchorWindow(t.Context(), ref, owner, string(selectedID), 14)
		if err != nil || anchor.Replay == nil || anchor.Identity.PTS != 14000 || len(frames) != 100 {
			t.Fatalf("section lookup traversed an unrelated batch: %v", err)
		}
		if _, _, err := comparisonInventoryAnchorWindow(t.Context(), ref, owner, "missing", 14); err == nil {
			t.Fatal("missing selected identity acquired anchor authority")
		}
		if err := readComparisonInventory(t.Context(), ref, owner, func(screenshots.ComparisonSample) bool { return true }); err == nil {
			t.Fatal("whole inventory validation accepted unrelated corruption")
		}
		if err := os.WriteFile(ref.Path, compressedInventory, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	priorPlan, err := plan.Clone()
	if err != nil {
		t.Fatal(err)
	}
	priorPlan.Sources = []api.ComparisonSource{{ID: "main", Fingerprint: fingerprint}, {ID: "target", Fingerprint: fingerprint}}
	priorPlan.Analysis, priorPlan.SceneMix = &ref.Summary, &api.ComparisonSceneMix{}
	changed, err := priorPlan.Clone()
	if err != nil {
		t.Fatal(err)
	}
	changed.Preferences.SelectionWindow = &api.ComparisonSelectionWindow{
		Start:    new(5.0),
		End:      new(20.0),
		Excluded: []api.ComparisonTimeInterval{{Start: 10, End: 12}},
	}
	t.Run("changed intent clears an empty prior analysis", func(t *testing.T) {
		pending, err := changed.Clone()
		if err != nil {
			t.Fatal(err)
		}
		retainComparisonCellFacts(&pending, &api.MediaArtifactSet{Comparisons: &priorPlan})
		if pending.Analysis != nil || pending.SceneMix != nil {
			t.Fatal("an empty prior row set retained stale selection analysis")
		}
	})
	for _, beforeDiscovery := range []bool{true, false} {
		name := "during calibration"
		if beforeDiscovery {
			name = "before discovery"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			renderer := comparisonStoppedInventoryRenderer{}
			if beforeDiscovery {
				renderer.stop = cancel
			} else {
				ctx = api.WithWorkflowProgressReporter(ctx, func(update api.WorkflowProgressUpdate) {
					if update.Phase == "comparisons_alignment_calibration" {
						cancel()
					}
				})
			}
			builder := b
			builder.comparisonRenderer = renderer
			private := cloneWorkflowMediaPrivateArtifacts(cloned)
			private.ComparisonSources["target"] = private.ComparisonSources["main"]
			updated, resource, err := builder.EditComparisons(ctx, api.ReleaseRef{SourcePath: path, Generation: 1}, api.TrackerReleaseProjectionSet{}, &api.MediaArtifactSet{Comparisons: &priorPlan}, private,
				api.ComparisonInstructions{Action: "match", Plan: changed}, nil, time.Time{})
			incomplete, ok := errors.AsType[*releaseworkflow.ComparisonIncompleteError](err)
			if !ok || incomplete.Reason != "match_cancelled" || updated.Comparisons == nil || updated.Comparisons.Analysis == nil {
				t.Fatalf("early stop lost verified discovery: %+v err=%v", updated.Comparisons, err)
			}
			analysis := updated.Comparisons.Analysis
			protected := []api.ComparisonTimeInterval{{Start: 10, End: 12}}
			if analysis.Selection != (api.ComparisonTimeInterval{Start: 5, End: 20}) || !slices.Equal(analysis.Excluded, protected) ||
				!slices.Equal(updated.Comparisons.Preferences.SelectionWindow.Excluded, protected) || math.Abs(analysis.EligibleSeconds-13) > 1e-9 || analysis.CoveredSeconds != 24 {
				t.Fatalf("early stop restored obsolete selection protection: %+v", analysis)
			}
			saved, ok := resource.(workflowMediaPrivateArtifacts)
			if !ok || saved.ComparisonInventory == nil || !slices.Equal(saved.ComparisonInventory.Summary.Excluded, protected) || calls != 2 || ref.Summary.Selection != window || len(ref.Summary.Excluded) != 0 {
				t.Fatal("selection projection changed the original inventory or repeated discovery")
			}
			if !slices.Contains(analysis.Reasons, "match_cancelled") || len(saved.ComparisonInventory.Summary.Reasons) != 0 {
				t.Fatal("matching cancellation contaminated reusable discovery facts")
			}
			resumed, resumeErr := b.discoverComparisonScenes(t.Context(), &saved, changed, analysis.Selection, protected, decode, b.comparisonProgress(t.Context(), 5))
			if resumeErr != nil || len(resumed.Summary.Reasons) != 0 || calls != 2 {
				t.Fatal("stopped matching forced discovery or leaked into the next attempt")
			}
		})
	}
	t.Run("inconclusive matching leaves discovery reusable", func(t *testing.T) {
		private := cloneWorkflowMediaPrivateArtifacts(cloned)
		private.ComparisonSources["target"] = private.ComparisonSources["main"]
		builder := b
		builder.comparisonRenderer = comparisonStoppedInventoryRenderer{emptySamples: true}
		matched, matchErr := builder.matchWorkflowComparisons(t.Context(), &private, priorPlan, builder.comparisonProgress(t.Context(), 5))
		if matchErr != nil || len(matched.Rows) != 0 || matched.Analysis == nil || len(matched.Analysis.Reasons) == 0 {
			t.Fatalf("inconclusive matching did not report its attempt: %v", matchErr)
		}
		if len(private.ComparisonInventory.Summary.Reasons) != 0 {
			t.Fatal("inconclusive correspondence contaminated reusable discovery facts")
		}
	})
	t.Run("incomplete Deep discovery publishes no automatic rows", func(t *testing.T) {
		for _, callerCancelled := range []bool{false, true} {
			t.Run(fmt.Sprintf("caller_cancelled_%t", callerCancelled), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				reason := "discovery_incomplete"
				if callerCancelled {
					reason = "match_cancelled"
				}
				private := cloneWorkflowMediaPrivateArtifacts(cloned)
				private.ComparisonInventory = nil
				private.ComparisonSources["target"] = private.ComparisonSources["main"]
				builder := b
				builder.comparisonRenderer = comparisonStoppedInventoryRenderer{analysis: func(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
					if request.StartSeconds >= 7 {
						if callerCancelled {
							cancel()
							return nil, context.Canceled
						}
						return nil, context.DeadlineExceeded
					}
					return decode(ctx, request.StartSeconds, request.DurationSeconds, true)
				}}
				prior := api.MediaArtifactSet{Comparisons: &priorPlan}
				updated, resource, err := builder.EditComparisons(ctx, api.ReleaseRef{SourcePath: path, Generation: 1}, api.TrackerReleaseProjectionSet{}, &prior, private,
					api.ComparisonInstructions{Action: "match", Plan: priorPlan}, nil, time.Now())
				incomplete, ok := errors.AsType[*releaseworkflow.ComparisonIncompleteError](err)
				if !ok || updated.Comparisons == nil || updated.Comparisons.Analysis == nil {
					t.Fatalf("partial discovery lost its typed publication: %v", err)
				}
				if incomplete.Reason != reason || len(updated.Comparisons.Rows) != 0 ||
					updated.Comparisons.Analysis.Complete || updated.Comparisons.Analysis.RankingComplete || updated.Comparisons.Analysis.CoveredSeconds != 8 ||
					updated.Comparisons.ApprovedFingerprint != "" || len(updated.Failures) != 1 {
					t.Fatalf("partial discovery gained automatic selection or lost its publication: reason=%+v analysis=%+v failures=%d err=%v", incomplete, updated.Comparisons.Analysis, len(updated.Failures), err)
				}
				saved, ok := resource.(workflowMediaPrivateArtifacts)
				if !ok || saved.ComparisonInventory == nil || saved.ComparisonInventory.NativeFrames != 200 {
					t.Fatal("partial native inventory was discarded")
				}
				_, payload, err := saved.MarshalPrivateResource()
				if err != nil {
					t.Fatal(err)
				}
				restored, err := decodeWorkflowMediaPrivateArtifacts(builder, payload)
				if err != nil || restored.ComparisonInventory == nil || restored.ComparisonInventory.Summary.Complete {
					t.Fatal("restart lost or completed partial discovery")
				}
				if _, _, err := builder.EditComparisons(t.Context(), api.ReleaseRef{}, api.TrackerReleaseProjectionSet{}, &updated, restored,
					api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: updated.Comparisons.Fingerprint}, nil, time.Now()); err == nil {
					t.Fatal("incomplete Deep discovery was approved")
				}
				builder.comparisonRenderer = comparisonStoppedInventoryRenderer{emptySamples: true, analysis: func(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
					return decode(ctx, request.StartSeconds, request.DurationSeconds, true)
				}}
				resumed, _, err := builder.EditComparisons(t.Context(), api.ReleaseRef{SourcePath: path, Generation: 1}, api.TrackerReleaseProjectionSet{}, &updated, restored,
					api.ComparisonInstructions{
						Action:            "match",
						Plan:              *updated.Comparisons,
						CaptureAfterMatch: new(false),
					}, nil, time.Now())
				if err != nil || resumed.Comparisons == nil || !resumed.Comparisons.Analysis.Complete || len(resumed.Failures) != 0 {
					t.Fatalf("completed discovery retained its earlier stopped outcome: %+v err=%v", resumed.Comparisons, err)
				}
			})
		}
	})
	t.Run("matching geometry binds inventory reuse", func(t *testing.T) {
		private := cloneWorkflowMediaPrivateArtifacts(cloned)
		private.ComparisonSources["target"] = private.ComparisonSources["main"]
		before := calls
		if _, err := b.discoverComparisonScenes(t.Context(), &private, priorPlan, window, excluded, decode, b.comparisonProgress(t.Context(), 5)); err != nil || calls != before {
			t.Fatal("unchanged common geometry repeated main discovery")
		}
		source := private.ComparisonSources["target"]
		source.Source.Width *= 2
		private.ComparisonSources["target"] = source
		changed, err := b.discoverComparisonScenes(t.Context(), &private, priorPlan, window, excluded, decode, b.comparisonProgress(t.Context(), 5))
		if err != nil || calls != before+2 || changed.SourceProfile == ref.SourceProfile || !changed.Summary.Complete {
			t.Fatalf("wider target reused differently normalized main pictures: calls=%d err=%v", calls-before, err)
		}
	})
	// Changing an omitted native frame must still invalidate the whole-file
	// digest, even when the selected window itself is unchanged.
	compressed, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	decodedInventory, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	plain := *ref
	plain.Blocks, plain.IndexSHA256 = nil, ""
	plain.Compressed = false
	plain.Bytes = int64(len(decodedInventory))
	plain.SHA256 = fmt.Sprintf("%x", sha256.Sum256(decodedInventory))
	if err := os.WriteFile(ref.Path, decodedInventory, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := readComparisonInventoryRange(t.Context(), &plain, owner, &api.ComparisonTimeInterval{Start: 12, End: 13}, func(screenshots.ComparisonSample) bool { return true }); err != nil {
		t.Fatal(err)
	}
	alteredInventory := bytes.Replace(decodedInventory, []byte("file-0"), []byte("file-1"), 1)
	if err := os.WriteFile(ref.Path, alteredInventory, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := readComparisonInventoryRange(t.Context(), &plain, owner, &api.ComparisonTimeInterval{Start: 12, End: 13}, func(screenshots.ComparisonSample) bool { return true }); err == nil {
		t.Fatal("changed witness outside the selected window escaped digest validation")
	}
	if err := os.WriteFile(ref.Path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := readComparisonInventory(t.Context(), ref, owner, func(screenshots.ComparisonSample) bool { return true }); err == nil {
		t.Fatal("corrupt retained inventory was trusted")
	}
	t.Run("Quick retains native choices between sparse measurements", func(t *testing.T) {
		quickPlan := plan
		quickPlan.Preferences.ScanMode = "fast"
		quickPrivate := cloneWorkflowMediaPrivateArtifacts(retained)
		search := &comparisonQuickSearch{}
		stronger, conflicting := false, false
		clippedID := ""
		var clippedTime float64
		discover := func() (*comparisonSceneInventory, error) {
			return b.discoverComparisonScenes(t.Context(), &quickPrivate, quickPlan, window, excluded,
				func(ctx context.Context, start, seconds float64, _ bool) ([]screenshots.ComparisonSample, error) {
					frames, err := decode(ctx, start, seconds, true)
					for i := range frames {
						f := &frames[i]
						if _, ok := search.seen[f.FrameID]; conflicting && ok {
							f.DurationTicks++
						}
						if !stronger {
							f.Replay.File.Unenclosed = !f.AnalysisAnchor
						} else if prior, known := search.seen[f.FrameID]; known && prior.replay.MissingBoundary() {
							if clippedID == "" {
								clippedID = f.FrameID
								clippedTime = f.RelativeTimestampSeconds
							}
							f.Replay.File.Unenclosed = clippedID != f.FrameID
							if !f.Replay.File.Unenclosed {
								f.Evidence.Eligible = false
							}
						}
					}
					return frames, err
				},
				b.comparisonProgress(t.Context(), 5), search)
		}
		quick, err := discover()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		if err := readComparisonInventory(t.Context(), quick, owner, func(frame screenshots.ComparisonSample) bool {
			found = found || !frame.AnalysisAnchor && frame.Identity != nil && frame.Replay != nil && frame.Evidence.Eligible
			return true
		}); err != nil || !found {
			t.Fatalf("Quick discarded already analyzed native alternatives: %v", err)
		}
		stronger = true
		for range 3 {
			before := quick.QuickWindows
			quick, err = discover()
			if err != nil || quick.QuickWindows != before+1 {
				t.Fatalf("Quick prefix did not advance: %v", err)
			}
		}
		ids := func(ref *comparisonSceneInventory) []string {
			t.Helper()
			var keys []string
			if err := readComparisonInventory(t.Context(), ref, owner, func(f screenshots.ComparisonSample) bool { keys = append(keys, f.FrameID); return true }); err != nil {
				t.Fatal(err)
			}
			return keys
		}
		indexed, _, err := comparisonInventoryCandidates(t.Context(), quick, owner, 64<<20)
		if err != nil {
			t.Fatal(err)
		}
		var streamed []comparisonSceneCandidate
		if err = streamQuickRanking(t.Context(), quick, owner, func(candidate comparisonSceneCandidate) bool {
			streamed = append(streamed, candidate)
			return true
		}); err != nil || !reflect.DeepEqual(streamed, indexed) {
			t.Fatalf("streamed Quick evidence differs from chronological ranking: %v", err)
		}
		want := ids(quick)
		if len(want) != len(search.seen) {
			t.Fatal("expanded inventory encoded duplicate native keys")
		}
		parts := slices.Clone(search.parts)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if err := search.finish(cancelled, owner, &quickPrivate); err != nil {
			t.Fatal(err)
		}
		anchor, _, err := comparisonInventoryAnchorWindow(t.Context(), quickPrivate.ComparisonInventory, owner, clippedID, clippedTime)
		if err != nil || anchor.Replay.MissingBoundary() || !anchor.Evidence.Eligible {
			t.Fatalf("restored overlap lost the stronger address or replaced classification: %v", err)
		}
		if !slices.Equal(want, ids(quickPrivate.ComparisonInventory)) {
			t.Fatal("detached handoff changed the complete prefix")
		}
		for _, part := range parts[1:] {
			if _, err := os.Stat(part.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("temporary Quick member survived handoff")
			}
		}
		if _, err := os.Stat(parts[0].Path); err != nil {
			t.Fatal("base archive lost during handoff")
		}
		search = &comparisonQuickSearch{}
		base, err := discover()
		if err != nil {
			t.Fatal(err)
		}
		conflicting = true
		_, err = discover()
		if failure, ok := api.AsOperationFailure(err); !ok || failure.ComparisonReason != "conflicting_frame_witness" || quickPrivate.ComparisonInventory != base {
			t.Fatalf("different native duration changed Quick coverage: %v", err)
		}
		conflicting = false
		if _, err = discover(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(search.parts[1].Path, []byte("damaged"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err = search.finish(t.Context(), owner, &quickPrivate); err == nil || quickPrivate.ComparisonInventory != base {
			t.Fatal("damaged segment replaced sealed base")
		}
		if len(ids(base)) == 0 {
			t.Fatal("base unavailable after rollback")
		}
	})
}

func TestComparisonQuickSectionExpandsOnlyThroughEligibleMainScene(t *testing.T) {
	frame := func(index int) screenshots.ComparisonSample {
		seconds := float64(index) / 25
		identity := api.ComparisonFrameIdentity{
			Version:             1,
			InputFingerprint:    "main-source",
			Occurrence:          "main",
			PTS:                 int64(index * 40),
			TimeBaseNumerator:   1,
			TimeBaseDenominator: 1000,
		}
		return screenshots.ComparisonSample{
			Replay: &screenshots.ComparisonFrameReplay{
				Version:  2,
				Identity: identity,
				File: &screenshots.ComparisonFileInterval{
					Duration:       72,
					Slot:           index,
					PacketPosition: new(int64(index + 1)),
				},
			},
			FrameID:                  fmt.Sprintf("main-%d", index),
			RelativeTimestampSeconds: seconds,
			PTS:                      int64(index * 40),
			TimeBaseNumerator:        1,
			TimeBaseDenominator:      1000,
			DurationTicks:            40,
			Identity:                 &identity,
			Evidence: &api.ComparisonSceneEvidence{
				Profile:  comparisons.SceneProfile,
				Eligible: !(seconds >= 22 && seconds < 22.2 || seconds >= 44 && seconds < 44.2),
				Dark:     api.ComparisonSceneCategory{State: "verified", Score: .9},
			},
		}
	}
	inventory := &comparisonSceneInventory{Summary: api.ComparisonAnalysisSummary{
		DurationSeconds: 72,
		Selection:       api.ComparisonTimeInterval{Start: 10, End: 60},
		RankingComplete: true,
	}}
	calls := 0
	frames, err := scoreComparisonSection(t.Context(), inventory, frame(825), nil, func(_ context.Context, start, seconds float64) ([]screenshots.ComparisonSample, error) {
		calls++
		if seconds > 10 || start < 9 || start+seconds > 61 {
			t.Fatal("surrounding scan escaped bounded main selection")
		}
		var result []screenshots.ComparisonSample
		for i := int(math.Round(start * 25)); i < int(math.Round((start+seconds)*25)); i++ {
			result = append(result, frame(i))
		}
		return result, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(frames) != 545 || math.Abs(frames[0].RelativeTimestampSeconds-22.2) > 1e-9 ||
		math.Abs(frames[len(frames)-1].RelativeTimestampSeconds-43.96) > 1e-9 {
		t.Fatalf("surrounding native scene did not stop at its transitions: calls=%d frames=%d", calls, len(frames))
	}
	if inventory.Summary.CoveredSeconds <= 8 || inventory.Summary.EligibleSeconds <= 8 || !inventory.Summary.RankingComplete {
		t.Fatal("scored scene expansion was absent from coverage")
	}
	t.Run("widening rejects an archived clock from another physical stretch", func(t *testing.T) {
		for _, index := range []int{824, 825} {
			seed, other := frame(825), frame(824)
			archived := map[string]comparisonKnownPicture{other.FrameID: comparisonRecord(other.ComparisonFrameObservation)}
			_, err := scoreComparisonSection(t.Context(), cloneComparisonInventory(inventory), seed, archived, func(context.Context, float64, float64) ([]screenshots.ComparisonSample, error) {
				fresh := []screenshots.ComparisonSample{frame(824), frame(825)}
				fresh[index-824].Replay.File.PacketPosition = new(int64(5000))
				return fresh, nil
			})
			failure, ok := api.AsOperationFailure(err)
			if !ok || failure.ComparisonReason != "conflicting_frame_witness" {
				t.Fatalf("accepted a different packet for the archived picture %d: %v", index, err)
			}
		}
	})
}

type comparisonStoppedInventoryRenderer struct {
	comparisonFrameRenderer
	stop         context.CancelFunc
	emptySamples bool
	analysis     func(context.Context, screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error)
}

func (r comparisonStoppedInventoryRenderer) AnalyzeComparisonSource(ctx context.Context, request screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	if r.analysis != nil {
		return r.analysis(ctx, request)
	}
	return nil, errors.New("unexpected main analysis after inventory reuse")
}

func (r comparisonStoppedInventoryRenderer) ComparisonAnalysisExecutableIdentity(ctx context.Context) (string, error) {
	if r.stop != nil {
		r.stop()
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("identify interrupted comparison analysis: %w", err)
	}
	return "", nil
}

func (r comparisonStoppedInventoryRenderer) SampleComparisonSource(ctx context.Context, _ screenshots.ComparisonSampleRequest) ([]screenshots.ComparisonSample, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("sample interrupted comparison inventory: %w", err)
	}
	if r.emptySamples {
		return nil, nil
	}
	return nil, errors.New("unexpected media decoding after inventory reuse")
}

func TestComparisonDeepInventoryPreservesPartialCoverage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Example.mkv")
	b := workflowMediaBuilder{config: config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "app.sqlite")}}, media: &mediaModule{}}
	retained := workflowMediaPrivateArtifacts{ComparisonSources: map[string]workflowComparisonSource{"main": {
		Path:        path,
		Fingerprint: "original",
		Source: screenshots.ComparisonSource{
			Path:            path,
			Width:           512,
			Height:          288,
			DurationSeconds: 24,
			HDRFacts:        api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatSDR}},
		},
	}}, screenshotSubject: api.ScreenshotSubject{SourcePath: path}}
	plan := api.ComparisonPlan{MainSourceID: "main", Preferences: api.ComparisonPreferences{Count: 1, ScanMode: "deep"}}
	window, excluded, err := comparisons.SelectionRange(plan.Preferences, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		frames  []screenshots.ComparisonSample
		err     error
		covered float64
	}{
		{"failed chunk", nil, context.DeadlineExceeded, 0},
		{"nonempty truncated chunk", []screenshots.ComparisonSample{{
			TimeBaseNumerator:   1,
			TimeBaseDenominator: 1000,
			DurationTicks:       40,
		}}, nil, .04},
	} {
		t.Run(test.name, func(t *testing.T) {
			ref, err := b.discoverComparisonScenes(t.Context(), &retained, plan, window, excluded, func(context.Context, float64, float64, bool) ([]screenshots.ComparisonSample, error) {
				return test.frames, test.err
			}, b.comparisonProgress(t.Context(), 5))
			if err != nil || ref.Summary.Complete || ref.Summary.RankingComplete || math.Abs(ref.Summary.CoveredSeconds-test.covered) > 1e-9 || len(ref.Summary.Reasons) == 0 {
				t.Fatalf("partial discovery claimed complete ranking: %+v %v", ref, err)
			}
		})
	}
}
