// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"fmt"
	"math"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSceneSectionPrefersTypeAgreementWithoutRelaxingPictureProof(t *testing.T) {
	for _, test := range []struct {
		name               string
		preferredScore     float64
		corrupt, fallbackI bool
		wantSeconds        float64
		allI               bool
		staged             bool
		wide               bool
		commonI, explicitI bool
	}{
		{
			name:           "within score band",
			preferredScore: .90,
			wantSeconds:    3.4,
		},
		{
			name:           "outside score band",
			preferredScore: .80,
			wantSeconds:    3,
		},
		{
			name:           "wrong native picture",
			preferredScore: .90,
			corrupt:        true,
			wantSeconds:    3,
		},
		{
			name:           "fallback excludes I",
			preferredScore: .90,
			fallbackI:      true,
			wantSeconds:    3.4,
		},
		{
			name:           "all I codec retains type agreement",
			preferredScore: .90,
			allI:           true,
			wantSeconds:    3,
		},
		{
			name:           "successive preferences keep original quality baseline",
			preferredScore: .90,
			staged:         true,
			wantSeconds:    3.4,
		},
		{
			name:           "agreement beyond the seed second",
			preferredScore: .90,
			wide:           true,
			wantSeconds:    5.5,
		},
		{
			name:           "automatic common I cannot outrank native B or P",
			preferredScore: .80,
			commonI:        true,
			wantSeconds:    3.8,
		},
		{
			name:           "explicit common I remains authoritative",
			preferredScore: .80,
			commonI:        true,
			explicitI:      true,
			wantSeconds:    3,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			main := SampledSource{ID: "main", PreferBFrames: true}
			target := SampledSource{ID: "target", PreferBFrames: true}
			if test.explicitI {
				main.PreferBFrames, target.PreferBFrames = false, false
				main.AllowedTypes, target.AllowedTypes = []string{"B", "I", "P"}, []string{"B", "I", "P"}
			}
			for i := range 61 {
				preferredIndex := 34
				if test.wide {
					preferredIndex = 55
				}
				frame := sampleFrame(uint64(i+40), float64(i)/10, "B")
				frame.FrameID, frame.Occurrence, frame.NativeSequence = fmt.Sprintf("main-%d", i), "file-main", true
				frame.Evidence = &api.ComparisonSceneEvidence{
					Profile:  SceneProfile,
					Eligible: true,
					Dark:     api.ComparisonSceneCategory{State: "verified", Score: .5},
				}
				if i == 30 {
					frame.Evidence.Dark.Score = .95
				}
				if i == preferredIndex {
					frame.Evidence.Dark.Score = test.preferredScore
				}
				main.Frames = append(main.Frames, frame)
				other := frame
				other.FrameID, other.Occurrence, other.TimestampSeconds, other.FrameType = fmt.Sprintf("target-%d", i), "file-target", frame.TimestampSeconds+10, "P"
				// Secondary scene metadata cannot rank or veto a native picture.
				other.Evidence = &api.ComparisonSceneEvidence{Eligible: false, Dark: api.ComparisonSceneCategory{State: "verified", Score: 1000}}
				if i == preferredIndex {
					other.FrameType = "B"
					if test.corrupt {
						other.Luma = sampleFrame(999, 0, "B").Luma
					}
				}
				if i == 30 && test.fallbackI {
					other.FrameType = "I"
				}
				if test.allI {
					main.Frames[len(main.Frames)-1].FrameType, other.FrameType = "I", "I"
				}
				if test.staged || test.commonI {
					m := &main.Frames[len(main.Frames)-1]
					switch i {
					case 30:
						m.FrameType, other.FrameType, m.Evidence.Dark.Score = "B", "P", .99
						if test.commonI {
							m.FrameType, other.FrameType = "I", "I"
						}
					case 34:
						m.FrameType, other.FrameType = "P", "P"
					case 38:
						m.FrameType, other.FrameType, m.Evidence.Dark.Score = "B", "B", .81
					}
				}
				target.Frames = append(target.Frames, other)
			}
			result, err := MatchSceneSection(t.Context(), []SampledSource{main, target}, "main", main.Frames[30], 1, map[string]PlaybackScale{"target": {1, 1}}, nil)
			if err != nil || len(result.Rows) != 1 || !result.Rows[0].Complete {
				t.Fatalf("native section failed: %+v %v", result, err)
			}
			row := result.Rows[0]
			if math.Abs(row.MainSeconds-test.wantSeconds) > 1e-9 {
				t.Fatalf("wrong quality/type trade-off: %+v", row)
			}
			if math.Abs(row.Frames[1].TimestampSeconds-row.MainSeconds-10) > 1e-9 || result.AchievedDark != 1 {
				t.Fatal("scene selection changed the verified native mapping or lost the main qualifier")
			}
			if test.fallbackI && row.Frames[1].FrameType == "I" {
				t.Fatal("an I picture became the automatic mixed-type fallback")
			}
		})
	}
}

func TestSceneTypePreferenceAcrossSections(t *testing.T) {
	row := func(score float64, kind string) MatchedRow {
		return MatchedRow{
			Complete: true,
			Evidence: &api.ComparisonSceneEvidence{Dark: api.ComparisonSceneCategory{Score: score}},
			Frames:   []FrameMatch{{FrameType: "B"}, {FrameType: kind}},
		}
	}
	best := row(.95, "P")
	allI := row(.95, "I")
	allI.Frames[0].FrameType = "I"
	for i := range allI.Frames {
		allI.Frames[i].TypeFallback = true
	}
	if !PreferSceneRow(row(.9, "B"), allI, 1) || PreferSceneRow(allI, row(.9, "B"), 1) {
		t.Fatal("coarse I pictures hid a comparable preferred B agreement")
	}
	if !PreferSceneRow(row(.86, "B"), best, 1) || PreferSceneRow(row(.84, "B"), best, 1) ||
		PreferSceneRow(row(.8, "P"), best, 1) || !PreferSceneRow(row(.96, "P"), best, 1) {
		t.Fatal("cross-section preference failed to preserve the ten-point scene-quality limit")
	}
}

func TestSceneSectionHonorsExplicitMixedPictureTypes(t *testing.T) {
	for _, explicitSource := range []string{"main", "target"} {
		for _, policy := range []struct {
			name  string
			types []string
			auto  bool
		}{
			{name: "I only", types: []string{"I"}},
			{name: "I and P", types: []string{"I", "P"}},
			{name: "B and I", types: []string{"B", "I"}},
			{name: "any explicit type"},
			{name: "automatic excludes mixed I", auto: true},
		} {
			t.Run(explicitSource+"/"+policy.name, func(t *testing.T) {
				sources := []SampledSource{{ID: "main", PreferBFrames: true}, {ID: "target", PreferBFrames: true}}
				for i := range sources {
					if sources[i].ID == explicitSource {
						sources[i].PreferBFrames, sources[i].AllowedTypes = policy.auto, policy.types
					}
					for index := range 61 {
						frame := sampleFrame(uint64(index+40), float64(index)/10+float64(i)*10, "P")
						frame.FrameID, frame.Occurrence, frame.NativeSequence = fmt.Sprintf("%s-%d", sources[i].ID, index), sources[i].ID, true
						if sources[i].ID == explicitSource {
							frame.FrameType = "I"
						}
						if i == 0 {
							frame.Evidence = &api.ComparisonSceneEvidence{
								Profile:  SceneProfile,
								Eligible: true,
								Dark:     api.ComparisonSceneCategory{State: "verified", Score: .95},
							}
						}
						sources[i].Frames = append(sources[i].Frames, frame)
					}
				}
				result, err := MatchSceneSection(t.Context(), sources, "main", sources[0].Frames[30], 1, map[string]PlaybackScale{"target": {1, 1}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if policy.auto {
					if len(result.Rows) != 0 {
						t.Fatal("automatic matching admitted a mixed I picture")
					}
					return
				}
				if len(result.Rows) != 1 || !result.Rows[0].Complete || result.AchievedDark != 1 ||
					result.Rows[0].MainSeconds < 0 || result.Rows[0].MainSeconds > 6 ||
					math.Abs(result.Rows[0].Frames[1].TimestampSeconds-result.Rows[0].MainSeconds-10) > 1e-9 {
					t.Fatalf("explicit picture policy lost a native correspondence: %+v", result)
				}
			})
		}
	}
}
