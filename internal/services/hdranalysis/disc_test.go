// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Audionut/go-hdr10-plus/extract"
	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

func discOutcomeFixture() *bridge.Outcome {
	source := video.Source{
		Path:        "BDMV/STREAM/00001.M2TS",
		Kind:        "m2ts",
		LogicalClip: "00001.M2TS",
	}
	mapping := video.Mapping{
		PID:       4113,
		Codec:     video.HEVC,
		Role:      video.Primary,
		EntryType: 1,
	}
	stc := video.STCSequence{
		ID:                0,
		StartPacket:       0,
		EndPacket:         100,
		HasEndPacket:      true,
		PresentationEnd45: 90000,
	}
	return &bridge.Outcome{Report: &bd.Result{
		Timelines: []bd.PlaylistTimeline{{
			Name:       "00001.MPLS",
			Complete:   true,
			Scanned:    true,
			Duration45: 45000,
			Items: []bd.PlayItem{{
				Index:               0,
				In45:                0,
				Out45:               45000,
				Duration45:          45000,
				ConnectionCondition: 1,
				Angles: []bd.Angle{{
					Source: source,
					STC:    &stc,
					Video:  []video.Mapping{mapping},
				}},
			}},
		}},
		Collection: []video.StreamResult{{
			Info: video.StreamInfo{
				Source: source,
				PID:    4113,
				Codec:  video.HEVC,
				Occurrences: []video.Occurrence{{
					Playlist: "00001.MPLS",
					Mapping:  mapping,
					STC:      &stc,
				}},
			},
			Status:   video.Complete,
			CleanEOF: true,
		}},
	}, Playlists: []bridge.PlaylistResult{{Name: "00001.MPLS", Extraction: testExtraction()}}}
}

func TestDiscExtractionAuthorityAndRetainedClockEvidence(t *testing.T) {
	outcome := discOutcomeFixture()
	sidecar, err := DiscExtraction(outcome, "00001.MPLS")
	if err != nil || len(sidecar.Timeline) != 1 || sidecar.Timeline[0].Mapping.PID != 4113 {
		t.Fatalf("disc authority=%#v %v", sidecar, err)
	}
	sidecar.Identity = ExtractionIdentity{
		SourceFingerprint: "source",
		TargetID:          testIdentity().TargetID,
		SelectionPolicy:   "primary_hevc_angle_zero",
		ResolvedTrackID:   1,
	}
	var output bytes.Buffer
	if err := WriteSidecar(t.Context(), &output, sidecar); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadSidecar(t.Context(), &output, sidecar.Identity)
	if err != nil || restored.Timeline[0].STC.EndPacket != 100 || restored.Timeline[0].Out45 != 45000 {
		t.Fatalf("clock evidence=%#v %v", restored, err)
	}
	cases := map[string]func(*bridge.Outcome){
		"metadata discovery": func(o *bridge.Outcome) { o.Report.Timelines[0].Scanned = false },
		"alternate angle":    func(o *bridge.Outcome) { o.Report.Timelines[0].SelectedAngle = 1 },
		"secondary stream":   func(o *bridge.Outcome) { o.Report.Timelines[0].Items[0].Angles[0].Video[0].Role = video.Secondary },
		"ssif":               func(o *bridge.Outcome) { o.Report.Timelines[0].Items[0].Angles[0].Source.Kind = "ssif" },
		"missing stc":        func(o *bridge.Outcome) { o.Report.Timelines[0].Items[0].Angles[0].STC = nil },
		"missing collection": func(o *bridge.Outcome) { o.Report.Collection = nil },
		"unclean eof":        func(o *bridge.Outcome) { o.Report.Collection[0].CleanEOF = false },
		"declined budget":    func(o *bridge.Outcome) { o.DeclinedAfterExhaustion = 1 },
		"omitted assembly":   func(o *bridge.Outcome) { o.OmittedPlaylistResults = 1 },
		"corrupt timeline":   func(o *bridge.Outcome) { o.Report.Timelines[0].Items[0].Offset45 = 10 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o := discOutcomeFixture()
			mutate(o)
			if _, err := DiscExtraction(o, "00001.MPLS"); err == nil {
				t.Fatal("unsupported authority accepted")
			}
		})
	}
}

func TestDiscVerifiedAbsenceRequiresCleanCollection(t *testing.T) {
	outcome := discOutcomeFixture()
	outcome.Report.Collection[0].Status = video.Failed
	outcome.Report.Collection[0].Err = extract.ErrNoMetadata
	outcome.Playlists[0].Extraction = nil
	outcome.Playlists[0].Err = extract.ErrNoMetadata
	sidecar, err := DiscExtraction(outcome, "00001.MPLS")
	if !errors.Is(err, extract.ErrNoMetadata) || len(sidecar.Timeline) != 1 {
		t.Fatalf("absence=%#v %v", sidecar, err)
	}
	outcome.Report.Collection[0].CleanEOF = false
	if _, err := DiscExtraction(outcome, "00001.MPLS"); !errors.Is(err, extract.ErrIncomplete) {
		t.Fatalf("unclean absence=%v", err)
	}
}
