// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestRequestedHDRDefaultsOnlySingleEligibleTarget(t *testing.T) {
	first, second := api.HDRTargetID("first", ""), api.HDRTargetID("second", "")
	supported := api.HDRAnalysisTarget{ID: first, Supported: true}
	unsupported := api.HDRAnalysisTarget{ID: second, Reason: "MediaInfo must confirm HDR10+ on a unique HEVC video track."}
	for _, test := range []struct {
		name     string
		targets  []api.HDRAnalysisTarget
		explicit []string
		want     []string
	}{
		{
			name:    "single eligible",
			targets: []api.HDRAnalysisTarget{supported},
			want:    []string{first},
		},
		{
			name:    "mixed eligible first",
			targets: []api.HDRAnalysisTarget{supported, unsupported},
			want:    []string{first},
		},
		{
			name:    "mixed eligible last",
			targets: []api.HDRAnalysisTarget{unsupported, supported},
			want:    []string{first},
		},
		{name: "single unsupported", targets: []api.HDRAnalysisTarget{unsupported}},
		{name: "empty inventory"},
		{name: "all unsupported", targets: []api.HDRAnalysisTarget{unsupported, {ID: first}}},
		{name: "multiple eligible", targets: []api.HDRAnalysisTarget{supported, {ID: second, Supported: true}}},
		{
			name:     "explicit subset",
			targets:  []api.HDRAnalysisTarget{supported, {ID: second, Supported: true}},
			explicit: []string{second},
			want:     []string{second},
		},
		{
			name:     "explicit source order",
			targets:  []api.HDRAnalysisTarget{supported, {ID: second, Supported: true}},
			explicit: []string{first, second},
			want:     []string{first, second},
		},
		{
			name:     "explicit unsupported remains resolver owned",
			targets:  []api.HDRAnalysisTarget{supported, unsupported},
			explicit: []string{second},
			want:     []string{second},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := CommandResult{Release: &api.ReleaseSnapshot{
				Release: api.PreparedRelease{Generation: 7, Source: api.SourceManifest{SourcePath: "Synthetic.Pack"}},
				Display: api.PreparedReleaseDisplay{HDRTargets: test.targets},
			}}
			got, err := requestedHDRInstructions(current, &api.HDRAnalysisRequest{TargetIDs: test.explicit})
			if test.want == nil {
				failure, ok := api.AsHDRAnalysisFailure(err)
				if !ok || failure.Code != api.HDRAnalysisFailureInvalidSelection {
					t.Fatalf("ambiguous or unsupported implicit selection=%#v err=%v", got, err)
				}
				if !slices.ContainsFunc(test.targets, func(target api.HDRAnalysisTarget) bool { return target.Supported }) &&
					(!strings.Contains(failure.Message, "no prepared HDR targets are eligible") ||
						(len(test.targets) > 0 && !strings.Contains(failure.Message, test.targets[0].Reason))) {
					t.Fatalf("ineligible selection lost its reason: %#v", failure)
				}
				stageFailure, ok := api.AsHDRAnalysisFailure(requestedHDRFailure("hdr-selection-required", current, &api.HDRAnalysisRequest{TargetIDs: test.explicit}))
				if !ok || stageFailure != failure {
					t.Fatalf("continuation lost selection failure: %#v", stageFailure)
				}
				return
			}
			if err != nil || !slices.Equal(got.TargetIDs, test.want) || got.Release != (api.ReleaseRef{SourcePath: "Synthetic.Pack", Generation: 7}) {
				t.Fatalf("instructions=%#v err=%v, want targets=%v", got, err, test.want)
			}
		})
	}
}
