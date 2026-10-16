// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestRequestedHDRUsableRetainedAudioAndNativeRequirements(t *testing.T) {
	for _, test := range []struct {
		name    string
		options *api.HDRDecodedOptions
		state   api.HDRDecodedState
		owned   bool
		sibling bool
		status  api.StageStatus
		want    bool
	}{
		{"complete audio", &api.HDRDecodedOptions{Audio: true}, api.HDRDecodedComplete, true, false, api.StageStatusPartial, true},
		{"partial audio", &api.HDRDecodedOptions{Audio: true}, api.HDRDecodedInconclusive, true, false, api.StageStatusPartial, true},
		{"automatic includes audio", &api.HDRDecodedOptions{Automatic: true}, api.HDRDecodedComplete, true, false, api.StageStatusPartial, true},
		{"native only still requires native", nil, api.HDRDecodedComplete, true, false, api.StageStatusPartial, false},
		{"failed audio", &api.HDRDecodedOptions{Audio: true}, api.HDRDecodedFailed, true, false, api.StageStatusPartial, false},
		{"unowned audio", &api.HDRDecodedOptions{Audio: true}, api.HDRDecodedComplete, false, false, api.StageStatusPartial, false},
		{"missing sibling", &api.HDRDecodedOptions{Audio: true}, api.HDRDecodedComplete, true, true, api.StageStatusPartial, false},
		{"canceled", &api.HDRDecodedOptions{Audio: true}, api.HDRDecodedComplete, true, false, api.StageStatusCanceled, false},
		{"interrupted", &api.HDRDecodedOptions{Audio: true}, api.HDRDecodedComplete, true, false, api.StageStatusInterrupted, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := api.HDRAnalysisTargetResult{
				Artifacts: []api.HDRTypedArtifact{{
					ID:   "stats",
					Kind: api.HDRArtifactAudioStats,
					MIME: "text/plain",
				}},
				Decoded: []api.HDRDecodedResult{{
					Mode:     api.HDRDecodedAudio,
					State:    test.state,
					Measured: 1,
				}},
			}
			if test.owned {
				target.Decoded[0].Artifacts = []api.PublicResourceID{"stats"}
			}
			result := api.HDRAnalysisResult{
				ProfileVersion: api.HDRAnalysisProfileV2,
				Status:         test.status,
				Decoded:        test.options,
				Targets:        []api.HDRAnalysisTargetResult{target},
			}
			if test.sibling {
				result.Targets = append(result.Targets, api.HDRAnalysisTargetResult{})
			}
			if got := requestedHDRUsable(&result); got != test.want {
				t.Fatalf("usable=%v want=%v", got, test.want)
			}
		})
	}
}

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
