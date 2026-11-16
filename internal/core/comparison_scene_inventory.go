// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

const comparisonInventoryLimit int64 = 128 << 20

// comparisonSceneInventory is an immutable private reference. Clones cloned only
// this small manifest; selected-only witness pruning never touches the file.
type comparisonSceneInventory struct {
	Path, SHA256, SourceProfile string
	Bytes                       int64
	Compressed                  bool
	QuickWindows                int
	Covered                     []api.ComparisonTimeInterval
	Eligible                    []api.ComparisonTimeInterval
	NativeFrames                int64
	Summary                     api.ComparisonAnalysisSummary
}

func cloneComparisonInventory(value *comparisonSceneInventory) *comparisonSceneInventory {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Covered = slices.Clone(value.Covered)
	cloned.Eligible = slices.Clone(value.Eligible)
	cloned.Summary.Excluded = append([]api.ComparisonTimeInterval(nil), value.Summary.Excluded...)
	cloned.Summary.Reasons = append([]string(nil), value.Summary.Reasons...)
	return &cloned
}

func comparisonInventoryForSelection(
	value *comparisonSceneInventory,
	window api.ComparisonTimeInterval,
	excluded []api.ComparisonTimeInterval,
) *comparisonSceneInventory {
	selected := cloneComparisonInventory(value)
	if selected.Summary.Complete && selected.Summary.RankingComplete {
		// Complete discovery has no failure reasons. Older attempts could have
		// attached matching diagnostics through the shared public summary.
		selected.Summary.Reasons = nil
	}
	selected.Summary.Selection = window
	selected.Summary.Excluded = slices.Clone(excluded)
	selected.Summary.EligibleSeconds = comparisonEligibleCoverage(selected.Eligible, window, excluded)
	return selected
}

func (b workflowMediaBuilder) comparisonInventoryOwner(retained workflowMediaPrivateArtifacts, plan api.ComparisonPlan) (string, error) {
	root, err := db.Subdir(b.config.MainSettings.DBPath, "tmp")
	if err != nil {
		return "", fmt.Errorf("comparison scene inventory: %w", err)
	}
	sourcePath := retained.screenshotSubject.SourcePath
	if sourcePath == "" {
		sourcePath = retained.ComparisonSources[plan.MainSourceID].Path
		if sourcePath == "" {
			sourcePath = retained.ComparisonSources[plan.MainSourceID].Source.Path
		}
	}
	return comparisonScratchDir(root, sourcePath, plan.MainSourceID)
}

func (b workflowMediaBuilder) comparisonSceneProfile(retained workflowMediaPrivateArtifacts, plan api.ComparisonPlan) (string, error) {
	source := retained.ComparisonSources[plan.MainSourceID]
	profile, err := api.CanonicalWorkflowFingerprint(struct {
		Version, Mode, Source, Main, Executable string
		Facts                                   screenshots.ComparisonSource
		MatchAspect                             float64
		Libplacebo                              bool
		Algorithm                               string
		Desat                                   float64
	}{comparisons.SceneProfile, plan.Preferences.ScanMode, source.Fingerprint, plan.MainSourceID, retained.ComparisonAnalysisExecutable, source.Source, comparisonMatchAspect(retained, plan), b.config.ScreenshotHandling.UseLibplacebo, b.config.ScreenshotHandling.TonemapAlgorithm, b.config.ScreenshotHandling.Desat})
	return string(profile), err
}

func comparisonMatchAspect(retained workflowMediaPrivateArtifacts, plan api.ComparisonPlan) float64 {
	aspect := 0.0
	for _, source := range plan.Sources {
		width, height := retained.ComparisonSources[source.ID].Source.DisplaySize()
		if width > 0 && height > 0 {
			aspect = max(aspect, float64(width)/float64(height))
		}
	}
	return aspect
}

func readComparisonInventory(ctx context.Context, ref *comparisonSceneInventory, owner string, visit func(screenshots.ComparisonSample) bool) error {
	return readComparisonInventoryRange(ctx, ref, owner, nil, visit)
}

// A window read follows the complete witness validation used to build the
// ranking index. It still hashes and bounds the entire stream against that
// reference, but only materializes native witnesses inside the requested window.
func readComparisonInventoryRange(
	ctx context.Context,
	ref *comparisonSceneInventory,
	owner string,
	window *api.ComparisonTimeInterval,
	visit func(screenshots.ComparisonSample) bool,
) error {
	if ref == nil || ref.Bytes < 0 || ref.Bytes > comparisonInventoryLimit || !pathing.IsWithinRoot(owner, ref.Path) {
		return errors.New("comparison inventory is unavailable")
	}
	file, err := os.Open(ref.Path)
	if err != nil {
		return fmt.Errorf("comparison scene inventory: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.Size() != ref.Bytes {
		return errors.New("comparison inventory size changed")
	}
	hash := sha256.New()
	input := io.TeeReader(io.LimitReader(file, comparisonInventoryLimit+1), hash)
	if ref.Compressed && ref.Bytes > 0 {
		compressed, err := gzip.NewReader(input)
		if err != nil {
			return fmt.Errorf("open compressed comparison inventory: %w", err)
		}
		defer compressed.Close()
		input = compressed
	}
	scanner := bufio.NewScanner(io.LimitReader(input, comparisonInventoryExpandedLimit+1))
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	active := true
	decodedBytes := int64(0)
	for scanner.Scan() {
		decodedBytes += int64(len(scanner.Bytes()) + 1)
		if decodedBytes > comparisonInventoryExpandedLimit {
			return errors.New("comparison inventory expanded size exceeds its limit")
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("comparison scene inventory: %w", err)
		}
		if window != nil {
			var header struct{ RelativeTimestampSeconds float64 }
			if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
				return errors.New("comparison inventory is corrupt")
			}
			if header.RelativeTimestampSeconds < window.Start || header.RelativeTimestampSeconds >= window.End {
				continue
			}
		}
		var frame screenshots.ComparisonSample
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return errors.New("comparison inventory is corrupt")
		}
		if frame.Evidence == nil || frame.Evidence.Profile != comparisons.SceneProfile || frame.Identity == nil || frame.Replay == nil ||
			frame.Replay.Identity != *frame.Identity ||
			frame.FrameID == "" {
			return errors.New("comparison inventory witness is incomplete")
		}
		if active {
			active = visit(frame)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("comparison scene inventory: %w", err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != ref.SHA256 {
		return errors.New("comparison inventory content changed")
	}
	return nil
}

type comparisonSceneRankCategory struct {
	State                 string
	Score, SupportSeconds float64
}

// Ranking does not need native replay, full analysis regions or support ranges.
// Keep just the facts consumed by SelectScene; the chosen native section supplies
// its complete evidence again before matching or publication.
type comparisonSceneCandidate struct {
	FrameID, Occurrence                                                  string
	TimestampSeconds                                                     float64
	Luma                                                                 []byte
	Eligible                                                             bool
	Motion, Dark, Flat                                                   comparisonSceneRankCategory
	MovingSupport, ReliableSupport, Mean, BlurredContrast, FlatDeviation float64
}

func (c comparisonSceneCandidate) sample() comparisons.FrameSample {
	category := func(rank comparisonSceneRankCategory) api.ComparisonSceneCategory {
		return api.ComparisonSceneCategory{
			State:          rank.State,
			Score:          rank.Score,
			SupportSeconds: rank.SupportSeconds,
		}
	}
	return comparisons.FrameSample{
		FrameID:          c.FrameID,
		Occurrence:       c.Occurrence,
		TimestampSeconds: c.TimestampSeconds,
		Luma:             c.Luma,
		Evidence: &api.ComparisonSceneEvidence{
			Profile:         comparisons.SceneProfile,
			Eligible:        c.Eligible,
			Motion:          category(c.Motion),
			Dark:            category(c.Dark),
			Flat:            category(c.Flat),
			MovingSupport:   c.MovingSupport,
			ReliableSupport: c.ReliableSupport,
			Mean:            c.Mean,
			BlurredContrast: c.BlurredContrast,
			FlatDeviation:   c.FlatDeviation,
		},
	}
}

// Cache ranking facts once, without retaining every frame's replay or regions.
// Larger titles keep the streaming selector rather than growing this index.
func comparisonInventoryCandidates(ctx context.Context, ref *comparisonSceneInventory, owner string, limit int64) ([]comparisonSceneCandidate, int64, error) {
	var candidates []comparisonSceneCandidate
	var size int64
	full := false
	err := readComparisonInventory(ctx, ref, owner, func(frame screenshots.ComparisonSample) bool {
		sample := comparisonSamples([]screenshots.ComparisonSample{frame})[0]
		if !frame.AnalysisAnchor {
			sample.Luma = nil
		}
		evidence := sample.Evidence
		category := func(value api.ComparisonSceneCategory) comparisonSceneRankCategory {
			return comparisonSceneRankCategory{
				State:          value.State,
				Score:          value.Score,
				SupportSeconds: value.SupportSeconds,
			}
		}
		candidate := comparisonSceneCandidate{
			FrameID:          sample.FrameID,
			Occurrence:       sample.Occurrence,
			TimestampSeconds: sample.TimestampSeconds,
			Luma:             sample.Luma,
			Eligible:         evidence.Eligible,
			Motion:           category(evidence.Motion),
			Dark:             category(evidence.Dark),
			Flat:             category(evidence.Flat),
			MovingSupport:    evidence.MovingSupport,
			ReliableSupport:  evidence.ReliableSupport,
			Mean:             evidence.Mean,
			BlurredContrast:  evidence.BlurredContrast,
			FlatDeviation:    evidence.FlatDeviation,
		}
		// Covers the fixed record, slice capacity growth and allocation padding,
		// plus its owned strings and the sparse native correspondence rasters.
		size += int64(320 + len(candidate.FrameID) + len(candidate.Occurrence) + len(candidate.Luma) +
			len(candidate.Motion.State) + len(candidate.Dark.State) + len(candidate.Flat.State))
		if size > limit {
			full = true
			candidates = nil
			return false
		}
		candidates = append(candidates, candidate)
		return true
	})
	if err != nil {
		return nil, 0, err
	}
	if full {
		return nil, 0, nil
	}
	slices.SortFunc(candidates, func(a, b comparisonSceneCandidate) int {
		if a.TimestampSeconds != b.TimestampSeconds {
			return cmp.Compare(a.TimestampSeconds, b.TimestampSeconds)
		}
		return cmp.Compare(a.FrameID, b.FrameID)
	})
	return candidates, size, nil
}

func (b workflowMediaBuilder) discoverComparisonScenes(
	ctx context.Context,
	retained *workflowMediaPrivateArtifacts,
	plan api.ComparisonPlan,
	window api.ComparisonTimeInterval,
	excluded []api.ComparisonTimeInterval,
	decode func(context.Context, float64, float64, bool) ([]screenshots.ComparisonSample, error),
	progress *comparisonProgress,
	expandQuick ...bool,
) (*comparisonSceneInventory, error) {
	profile, err := b.comparisonSceneProfile(*retained, plan)
	if err != nil {
		return nil, err
	}
	owner, err := b.comparisonInventoryOwner(*retained, plan)
	if err != nil {
		return nil, err
	}
	if old := retained.ComparisonInventory; old != nil && old.SourceProfile == profile && old.Summary.Complete && old.Summary.RankingComplete &&
		plan.Preferences.ScanMode == "deep" {
		if err := readComparisonInventory(ctx, old, owner, func(screenshots.ComparisonSample) bool { return true }); err == nil {
			reused := comparisonInventoryForSelection(old, window, excluded)
			retained.ComparisonInventory = reused
			progress.logger.Infof("comparisons: phase=main_discovery decision=reused mode=deep covered_seconds=%.3f", old.Summary.CoveredSeconds)
			return reused, nil
		}
	}
	if err := os.MkdirAll(owner, 0o700); err != nil {
		return nil, fmt.Errorf("comparison scene inventory: %w", err)
	}
	file, err := os.CreateTemp(owner, "scene-inventory-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("comparison scene inventory: %w", err)
	}
	defer file.Close()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(file.Name())
		}
	}()
	writer := bufio.NewWriterSize(file, 64<<10)
	hash := sha256.New()
	sink := io.MultiWriter(writer, hash)
	duration := retained.ComparisonSources[plan.MainSourceID].Source.VideoDurationSeconds
	if duration <= 0 {
		duration = retained.ComparisonSources[plan.MainSourceID].Source.DurationSeconds
	}
	deep := plan.Preferences.ScanMode == "deep"
	ref := &comparisonSceneInventory{
		Path:          file.Name(),
		Compressed:    true,
		SourceProfile: profile,
		Summary: api.ComparisonAnalysisSummary{
			Version:         1,
			Profile:         comparisons.SceneProfile,
			ScanMode:        plan.Preferences.ScanMode,
			DurationSeconds: duration,
			Selection:       window,
			Excluded:        excluded,
		},
	}
	encoder := comparisonInventoryEncoder{destination: sink, ref: ref}
	type interval struct{ start, end, payloadStart, payloadEnd float64 }
	var intervals []interval
	if deep {
		if duration/8 > float64(math.MaxInt-1) {
			return nil, comparisonSelectionFailure(
				api.OperationFailureInvalidSource,
				"duration_limit",
				"The main timeline exceeds bounded comparison indexing.",
			)
		}
	} else {
		// The second distributed pass expands discovery within the same shared
		// main allowance. It never expands protected selection bounds.
		for pass := range 2 {
			for i := range 12 {
				center := window.Start + (window.End-window.Start)*(float64(i)+.5+float64(pass)*.5)/12
				start, end := max(window.Start, center-2), min(window.End, center+2)
				intervals = append(intervals, interval{start, end, start, end})
			}
		}
	}
	expanding := len(expandQuick) > 0 && expandQuick[0] && !deep
	old := retained.ComparisonInventory
	if expanding {
		if old == nil || old.SourceProfile != profile || old.QuickWindows < 12 || old.QuickWindows >= len(intervals) || !old.Summary.RankingComplete {
			return nil, screenshots.ErrComparisonFastInconclusive
		}
		intervals = intervals[old.QuickWindows : old.QuickWindows+1]
		ref.QuickWindows = old.QuickWindows
		ref.Summary = old.Summary
		ref.Summary.Reasons = nil
		ref.Summary.CoveredSeconds, ref.Summary.EligibleSeconds = 0, 0
	}
	total := len(intervals)
	if deep {
		total = int(math.Ceil(duration / 8))
	}
	progress.begin("main_discovery", total)
	var covered []api.ComparisonTimeInterval
	var eligible []api.ComparisonTimeInterval
	tick, lastDuration := 1e-9, 0.0
	var seen map[string]bool
	if !deep {
		seen = map[string]bool{}
	}
	if expanding {
		covered = slices.Clone(old.Covered)
		eligible = slices.Clone(old.Eligible)
		ref.NativeFrames = old.NativeFrames
		var writeErr error
		if err := readComparisonInventory(ctx, old, owner, func(frame screenshots.ComparisonSample) bool {
			seen[comparisonObservationID(frame.ComparisonFrameObservation)] = true
			err := encoder.write(frame)
			writeErr = err
			return err == nil
		}); err != nil {
			return nil, err
		}
		if writeErr != nil {
			return nil, writeErr
		}
	}
	var visitedCandidates []comparisons.FrameSample
	for index := 0; index < total; index++ {
		var region interval
		if deep {
			start := float64(index) * 8
			end := min(duration, start+8)
			region = interval{max(0, start-1), min(duration, end+1), start, end}
		} else {
			region = intervals[index]
		}
		if err := ctx.Err(); err != nil {
			ref.Summary.Reasons = append(ref.Summary.Reasons, "discovery_deadline")
			break
		}
		seconds := region.end - region.start
		ref.Summary.RequestedSeconds += seconds
		frames, err := decode(ctx, region.start, seconds, deep)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, screenshots.ErrComparisonFastInconclusive) ||
				errors.Is(err, screenshots.ErrComparisonNormalizationUnavailable) {
				ref.Summary.Reasons = append(ref.Summary.Reasons, "discovery_incomplete")
				break
			}
			return nil, err
		}
		if len(frames) == 0 {
			ref.Summary.Reasons = append(ref.Summary.Reasons, "missing_main_interval")
			break
		}
		for _, frame := range frames {
			t := frame.RelativeTimestampSeconds
			if frame.TimeBaseNumerator <= 0 || frame.TimeBaseDenominator <= 0 || frame.DurationTicks <= 0 {
				if !slices.Contains(ref.Summary.Reasons, "missing_native_duration") {
					ref.Summary.Reasons = append(ref.Summary.Reasons, "missing_native_duration")
				}
				continue
			}
			quantum := float64(frame.TimeBaseNumerator) / float64(frame.TimeBaseDenominator)
			tick = max(tick, quantum)
			span := api.ComparisonTimeInterval{Start: max(region.payloadStart, t), End: min(region.payloadEnd, t+float64(frame.DurationTicks)*quantum)}
			if span.End <= span.Start {
				continue
			}
			covered = appendComparisonCoverage(covered, span, quantum)
			if frame.Evidence != nil && frame.Evidence.Eligible {
				eligible = appendComparisonCoverage(eligible, span, quantum)
			}
			if t >= region.payloadStart && t < region.payloadEnd {
				ref.NativeFrames++
				lastDuration = float64(frame.DurationTicks) * quantum
			}
		}
		if !deep {
			ref.QuickWindows++
		}
		for _, frame := range frames {
			t := frame.RelativeTimestampSeconds
			if t < region.payloadStart || t >= region.payloadEnd || frame.Evidence == nil || !deep && !frame.AnalysisAnchor {
				continue
			}
			if !deep {
				frame = refineComparisonSceneAnchor(frame, frames, plan)
			}
			if frame.RelativeTimestampSeconds < region.payloadStart || frame.RelativeTimestampSeconds >= region.payloadEnd {
				continue
			}
			if !deep {
				id := comparisonObservationID(frame.ComparisonFrameObservation)
				if seen[id] {
					continue
				}
				seen[id] = true
			}
			if err := encoder.write(frame); errors.Is(err, errComparisonInventoryFull) {
				ref.Summary.Reasons = append(ref.Summary.Reasons, "inventory_limit")
				break
			} else if err != nil {
				return nil, fmt.Errorf("comparison scene inventory: %w", err)
			}
			if !deep {
				visitedCandidates = append(visitedCandidates, comparisonSamples([]screenshots.ComparisonSample{frame})[0])
			}
		}
		progress.update(index+1, total)
		if len(ref.Summary.Reasons) > 0 {
			break
		}
		if !deep && index == 11 {
			dark, motion, _ := comparisons.SceneCounts(plan.Preferences, plan.Preferences.Count)
			targets := [3]int{motion, dark, plan.Preferences.FlatCount}
			achieved := [3]int{}
			used := map[string]bool{}
			var selected []comparisons.MatchedRow
			for range plan.Preferences.Count {
				frame, _, ok := comparisons.SelectScene(
					slices.Values(visitedCandidates),
					used,
					selected,
					targets,
					achieved,
					targets,
					(window.End-window.Start)/float64(max(1, plan.Preferences.Count*3)),
				)
				if !ok {
					break
				}
				used[frame.FrameID] = true
				selected = append(selected, comparisons.MatchedRow{MainSeconds: frame.TimestampSeconds, Complete: true})
				for category := range 3 {
					if comparisons.SceneQualified(frame.Evidence, category) {
						achieved[category]++
					}
				}
			}
			spread := 0.0
			if len(selected) > 1 {
				lo, hi := selected[0].MainSeconds, selected[0].MainSeconds
				for _, row := range selected {
					lo = min(lo, row.MainSeconds)
					hi = max(hi, row.MainSeconds)
				}
				spread = hi - lo
			}
			if len(selected) == plan.Preferences.Count && achieved[0] >= targets[0] && achieved[1] >= targets[1] && achieved[2] >= targets[2] &&
				(len(selected) == 1 || spread >= (window.End-window.Start)*.5) {
				break
			}
		}
	}
	progress.complete()
	// Requested work counts overlaps; coverage counts each visited interval once.
	if err := encoder.flush(); errors.Is(err, errComparisonInventoryFull) {
		ref.Summary.Reasons = append(ref.Summary.Reasons, "inventory_limit")
	} else if err != nil {
		return nil, err
	}
	unique := mergeComparisonCoverage(covered, tick)
	ref.Covered = slices.Clone(unique)
	ref.Eligible = mergeComparisonCoverage(eligible, tick)
	for _, interval := range unique {
		ref.Summary.CoveredSeconds += interval.End - interval.Start
	}
	ref.Summary.EligibleSeconds = comparisonEligibleCoverage(ref.Eligible, window, excluded)
	metadataFrames := retained.ComparisonSources[plan.MainSourceID].Source.FrameCount
	endTolerance := tick
	if metadataFrames > 0 && metadataFrames == ref.NativeFrames {
		// Metadata duration can round beyond the final observed display end.
		// Equal native frame counts admit that final rounding, never an interior gap.
		endTolerance = max(tick, lastDuration)
	}
	ref.Summary.Complete = deep && len(ref.Summary.Reasons) == 0 && len(unique) == 1 && unique[0].Start <= tick &&
		unique[0].End >= duration-endTolerance && (metadataFrames == 0 || metadataFrames == ref.NativeFrames)
	if deep && !ref.Summary.Complete && len(ref.Summary.Reasons) == 0 {
		ref.Summary.Reasons = append(ref.Summary.Reasons, "missing_main_interval")
	}
	ref.Summary.RankingComplete = len(ref.Summary.Reasons) == 0
	if !deep {
		ref.Summary.Reasons = append(ref.Summary.Reasons, "bounded_discovery")
	}
	if err := writer.Flush(); err != nil {
		return nil, fmt.Errorf("comparison scene inventory: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("comparison scene inventory: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("comparison scene inventory: %w", err)
	}
	ref.SHA256 = hex.EncodeToString(hash.Sum(nil))
	destination := filepath.Join(owner, "scene-inventory-"+ref.SHA256+".jsonl.gz")
	if _, err := os.Stat(destination); err == nil {
		existing := *ref
		existing.Path = destination
		existing.SHA256 = ref.SHA256
		if err := readComparisonInventory(ctx, &existing, owner, func(screenshots.ComparisonSample) bool { return true }); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// Keep the closed prefix for the caller's detached verification;
				// cancelled deduplication must not discard completed native work.
				keep = true
				retained.ComparisonInventory = ref
				return ref, nil
			}
			return nil, err
		}
		if err := os.Remove(ref.Path); err != nil {
			return nil, fmt.Errorf("comparison scene inventory: %w", err)
		}
	} else if err := os.Rename(ref.Path, destination); err != nil {
		return nil, fmt.Errorf("comparison scene inventory: %w", err)
	}
	ref.Path = destination
	keep = true
	retained.ComparisonInventory = ref
	return ref, nil
}

func appendComparisonCoverage(intervals []api.ComparisonTimeInterval, span api.ComparisonTimeInterval, tick float64) []api.ComparisonTimeInterval {
	if len(intervals) > 0 && span.Start >= intervals[len(intervals)-1].Start && span.Start <= intervals[len(intervals)-1].End+tick+1e-9 {
		intervals[len(intervals)-1].End = max(intervals[len(intervals)-1].End, span.End)
		return intervals
	}
	return append(intervals, span)
}

func mergeComparisonCoverage(intervals []api.ComparisonTimeInterval, tick float64) []api.ComparisonTimeInterval {
	slices.SortFunc(intervals, func(a, b api.ComparisonTimeInterval) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return 0
	})
	var merged []api.ComparisonTimeInterval
	for _, span := range intervals {
		merged = appendComparisonCoverage(merged, span, tick)
	}
	return merged
}

func comparisonEligibleCoverage(intervals []api.ComparisonTimeInterval, window api.ComparisonTimeInterval, excluded []api.ComparisonTimeInterval) float64 {
	total := 0.0
	for _, span := range intervals {
		start, end := max(window.Start, span.Start), min(window.End, span.End)
		if end <= start {
			continue
		}
		seconds := end - start
		for _, skip := range excluded {
			seconds -= max(0, min(end, skip.End)-max(start, skip.Start))
		}
		total += max(0, seconds)
	}
	return total
}

func comparisonDiscoveryAllowance(duration float64) time.Duration {
	return time.Duration(min(28800.0, max(1800.0, 4*duration)) * float64(time.Second))
}

// Refine while the original native measurements and replay witnesses are still
// present. This avoids decoding again merely to select a neighbouring B picture.
func refineComparisonSceneAnchor(
	anchor screenshots.ComparisonSample,
	frames []screenshots.ComparisonSample,
	plan api.ComparisonPlan,
) screenshots.ComparisonSample {
	if anchor.Evidence == nil || !anchor.Evidence.Eligible {
		return anchor
	}
	var types []string
	for _, source := range plan.Sources {
		if source.ID != plan.MainSourceID {
			continue
		}
		types = source.AllowedFrameTypes
		if source.PreferBFrames {
			types = []string{"B", "P", "I"}
		}
	}
	if len(types) == 0 {
		return anchor
	}
	best, distance := len(types), math.Inf(1)
	chosen := anchor
	for _, frame := range frames {
		delta := math.Abs(frame.RelativeTimestampSeconds - anchor.RelativeTimestampSeconds)
		rank := slices.Index(types, frame.PictureType)
		if rank < 0 || rank > best || rank == best && delta >= distance || delta > .15 || frame.Evidence == nil || !frame.Evidence.Eligible {
			continue
		}
		compatible := true
		for category := range 3 {
			if comparisons.SceneQualified(anchor.Evidence, category) && !comparisons.SceneQualified(frame.Evidence, category) {
				compatible = false
			}
		}
		if compatible {
			chosen, best, distance = frame, rank, delta
		}
	}
	return chosen
}
