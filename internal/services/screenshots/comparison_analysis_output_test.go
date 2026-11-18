// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/autobrr/upbrr/internal/comparisons"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

func TestComparisonCompactOutputsJoinNativeClocksAndRejectIncompleteData(t *testing.T) {
	motion := bytes.Repeat([]byte{32}, 272*32)
	stats := bytes.Repeat([]byte{128}, 1024*32*3/2)
	line := func(name string, index, pts, width int, format string, data []byte) string {
		return fmt.Sprintf("[%s @ synthetic] config in time_base: 1/1000, frame_rate: 10/1\n[%s @ synthetic] n: %d pts: %d pts_time:%.3f duration:300 fmt:%s sar:1/1 s:%dx32 i:P type:B checksum:%08X\n", name, name, index, pts, float64(pts)/1000, format, width, comparisonRasterChecksum(data))
	}
	for _, test := range []struct {
		name       string
		statsFirst bool
		rational   bool
		change     string
	}{
		{name: "motion provenance first"}, {name: "sparse provenance first", statsFirst: true},
		{name: "independent rational VFR clocks", rational: true},
		{name: "truncated sparse", change: "truncate"}, {name: "extra sparse frame", change: "extra"},
		{name: "missing output provenance", change: "missing"}, {name: "duplicate sparse clock", change: "duplicate"},
		{name: "changed sparse clock", change: "clock"}, {name: "wrong output geometry", change: "geometry"},
		{name: "changed raw witness", change: "witness"},
		{name: "cancellation cannot hide corruption", change: "cancel_corruption"},
		{name: "cancellation cannot hide native clock failure", change: "cancel_provenance"},
		{name: "clean cancellation with missing output provenance", change: "cancel_missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var native, motions, sparse, metrics strings.Builder
			for i, pts := range []int{0, 300, 750} {
				native.WriteString(line("Parsed_showinfo_1", i, pts, 64, "yuv420p", []byte{1}))
				motions.WriteString(line("showinfo@comparison_motion", i, pts, 272, "gray", motion))
				statsPTS, width := pts, 1024
				if i == 1 && test.change == "duplicate" {
					statsPTS = 0
				}
				if i == 1 && test.change == "clock" {
					statsPTS++
				}
				if test.change == "geometry" {
					width = 1022
				}
				if test.change != "cancel_missing" && (test.change != "missing" || i == 0) {
					record := line("showinfo@comparison_stats", i, statsPTS, width, "yuv420p", stats)
					if test.rational {
						record = strings.Replace(record, "time_base: 1/1000", "time_base: 1/90000", 1)
						record = strings.Replace(record, fmt.Sprintf("pts: %d ", statsPTS), fmt.Sprintf("pts: %d ", statsPTS*90), 1)
					}
					sparse.WriteString(record)
				}
				fmt.Fprintf(&metrics, "[metadata@comparison_cut @ synthetic] frame:%d pts:%d pts_time:%.3f\n[metadata@comparison_cut @ synthetic] lavfi.scd.score=0\n", i, pts, float64(pts)/1000)
			}
			stderr := native.String() + metrics.String() + motions.String() + sparse.String()
			if test.change == "cancel_corruption" {
				stderr = "error while decoding\n" + stderr
			}
			if test.change == "cancel_provenance" {
				stderr = strings.Replace(stderr, "time_base: 1/1000", "time_base: 0/0", 1)
			}
			if test.statsFirst {
				stderr = native.String() + metrics.String() + sparse.String() + motions.String()
			}
			result := CommandResult{
				Stderr:       []byte(stderr),
				Stdout:       bytes.Repeat(motion, 3),
				SparseOutput: bytes.Repeat(stats, 3),
			}
			switch test.change {
			case "truncate":
				result.SparseOutput = result.SparseOutput[:len(result.SparseOutput)-1]
			case "extra":
				result.SparseOutput = append(result.SparseOutput, stats...)
			case "witness":
				result.Stdout[0]++
			}
			stream := &comparisonRasterStream{
				height:       32,
				motionHeight: 32,
				maxSparse:    4,
			}
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			stream.reset(parent)
			ctx := context.WithValue(parent, comparisonRasterStreamKey{}, stream)
			runner := finiteComparisonRunner{base: comparisonInspectionRunner(func(context.Context, string, []string, string) (CommandResult, error) {
				if strings.HasPrefix(test.change, "cancel_") {
					cancel()
				}
				return result, nil
			})}
			_, err := runner.Run(ctx, "synthetic", nil, "")
			if (err != nil) != (test.change != "") {
				t.Fatalf("change=%s error=%v", test.change, err)
			}
			if test.change == "cancel_missing" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation became a missing-output failure: %v", err)
			}
			if strings.HasPrefix(test.change, "cancel_") && test.change != "cancel_missing" && errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation hid an observed evidence failure: %v", err)
			}
			if test.change == "cancel_corruption" && !errors.Is(err, internalerrors.ErrFrameCorruption) {
				t.Fatalf("corruption lost its failure category: %v", err)
			}
			if err == nil && (len(stream.measurements) != 3 || stream.measurements[0].Sparse == nil || stream.measurements[1].Sparse == nil || stream.measurements[2].Time != .75) {
				t.Fatal("independent output arrival changed native association")
			}
			if test.rational {
				// Move the first native frame a third of a microsecond away
				// from output zero, then beyond the rounding allowance.
				diagnostic := strings.Replace(stderr, "time_base: 1/1000", "time_base: 1/3000000", 1)
				request := ComparisonSampleRequest{analysis: stream, Source: ComparisonSource{InputFingerprint: "synthetic"}}
				result.Stderr = []byte(strings.Replace(diagnostic, "pts: 0 ", "pts: 1 ", 1))
				samples, err := analysisSamples(result, request, nil)
				if err != nil || len(samples) != 3 || samples[0].Identity == nil || samples[0].Identity.PTS != 1 || samples[0].Identity.TimeBaseDenominator != 3000000 {
					t.Fatalf("rounded output lost native identity: %v", err)
				}
				result.Stderr = []byte(strings.Replace(diagnostic, "pts: 0 ", "pts: 4 ", 1))
				if _, err := analysisSamples(result, request, nil); err == nil {
					t.Fatal("analysis accepted output beyond clock rounding tolerance")
				}
			}
			select {
			case <-stream.done:
			default:
				t.Fatal("an output reader or coordinator survived the finite runner")
			}
		})
	}
}

func TestComparisonCancelledSparseFactsDoNotBecomeMissingMotion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		stream := &comparisonRasterStream{
			height:       32,
			motionHeight: 32,
			maxSparse:    3,
		}
		stream.reset(ctx)
		stream.start(ctx, cancel)
		stream.facts <- comparisonRasterFact{
			observation: ComparisonFrameObservation{TimeBaseNumerator: 1, TimeBaseDenominator: 1000},
			spatial:     &comparisons.SceneSpatialMeasurement{},
		}
		// The sparse fact is pending and the coordinator waits for motion.
		synctest.Wait()
		cancel(context.Canceled)
		close(stream.facts)
		<-stream.done
		if err := stream.failure(context.Cause(ctx), nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled sparse output became missing-motion evidence: %v", err)
		}
	})
}
