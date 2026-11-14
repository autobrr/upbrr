package api

import "testing"

func TestComparisonSelectionsRequireDiscoveredDefaultAngleAuthority(t *testing.T) {
	valid := func() ComparisonInstructions {
		return ComparisonInstructions{Action: "configure", VideoSources: []ComparisonVideoSelection{{
			ID:   "dvd",
			Path: "Example.DVD",
			Programme: &ComparisonProgrammeSelection{
				Kind:                 "dvd",
				DiscID:               "disc-1",
				CandidateID:          "title-1",
				DiscoveryFingerprint: "discovery-1",
				Angle:                1,
			},
		}}}
	}
	for _, test := range []struct {
		name      string
		change    func(*ComparisonInstructions)
		wantError bool
	}{
		{"discovered title", func(*ComparisonInstructions) {}, false},
		{"legacy file", func(i *ComparisonInstructions) { i.VideoSources[0].Programme = nil }, false},
		{"missing discovery binding", func(i *ComparisonInstructions) { i.VideoSources[0].Programme.DiscoveryFingerprint = "" }, true},
		{"alternate angle", func(i *ComparisonInstructions) { i.VideoSources[0].Programme.Angle = 2 }, true},
		{"unknown programme kind", func(i *ComparisonInstructions) { i.VideoSources[0].Programme.Kind = "decoder-arguments" }, true},
		{"duplicate columns", func(i *ComparisonInstructions) { i.VideoSources = append(i.VideoSources, i.VideoSources[0]) }, true},
		{"approval cannot select frames", func(i *ComparisonInstructions) {
			i.VideoSources = nil
			i.Action = "approve"
			i.ExpectedFingerprint = "review"
			i.FrameSelections = []ComparisonFrameSelection{{
				RowID:     "row-1",
				SourceID:  "dvd",
				PreviewID: "preview-1",
			}}
		}, true},
		{"inspection applies to one cell", func(i *ComparisonInstructions) {
			i.FrameSelections = []ComparisonFrameSelection{{
				RowID:     "row-1",
				SourceID:  "dvd",
				PreviewID: "preview-1",
			}}
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			instructions := valid()
			test.change(&instructions)
			if err := instructions.Validate(); (err != nil) != test.wantError {
				t.Fatalf("validation error=%v expected_error=%t", err, test.wantError)
			}
		})
	}
}
