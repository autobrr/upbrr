// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicateAudioEvidenceUsesPerTrackFacts(t *testing.T) {
	t.Parallel()

	codecs, codecsComplete, channels, channelsComplete := duplicateAudioEvidence(api.MediaFacts{
		TrackCoverageComplete: true,
		Audio:                 "DDP 5.1",
		Channels:              "5.1",
		Tracks: []api.MediaTrackFacts{
			{
				Kind:          api.MediaTrackAudio,
				Codec:         "E-AC-3",
				Channels:      6,
				ChannelsKnown: true,
			},
			{
				Kind:          api.MediaTrackAudio,
				Codec:         "AAC",
				Channels:      2,
				ChannelsKnown: true,
			},
			{Kind: api.MediaTrackSubtitle, Codec: "PGS"},
		},
	})
	if got, want := codecs, []string{"E-AC-3", "AAC"}; !slices.Equal(got, want) {
		t.Fatalf("codecs = %#v, want %#v", got, want)
	}
	if got, want := channels, []string{"6", "2"}; !slices.Equal(got, want) {
		t.Fatalf("channels = %#v, want %#v", got, want)
	}
	if !codecsComplete || !channelsComplete {
		t.Fatalf("completeness = %t/%t, want complete", codecsComplete, channelsComplete)
	}
}

func TestDuplicateAudioEvidenceMarksPartialEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		media            api.MediaFacts
		wantCodecs       []string
		wantCodecsDone   bool
		wantChannels     []string
		wantChannelsDone bool
	}{
		{
			name: "coverage incomplete",
			media: api.MediaFacts{
				TrackCoverageComplete: false,
				Tracks: []api.MediaTrackFacts{{
					Kind:          api.MediaTrackAudio,
					Codec:         "AAC",
					Channels:      2,
					ChannelsKnown: true,
				}},
			},
			wantCodecs:   []string{"AAC"},
			wantChannels: []string{"2"},
		},
		{
			name: "track codec missing",
			media: api.MediaFacts{
				TrackCoverageComplete: true,
				Tracks: []api.MediaTrackFacts{
					{
						Kind:          api.MediaTrackAudio,
						Codec:         "AAC",
						Channels:      2,
						ChannelsKnown: true,
					},
					{
						Kind:          api.MediaTrackAudio,
						ChannelsKnown: false,
						ChannelLayout: "5.1",
					},
				},
			},
			wantCodecs:       []string{"AAC"},
			wantChannels:     []string{"2", "5.1"},
			wantChannelsDone: true,
			wantCodecsDone:   false,
		},
		{
			name: "no audio tracks",
			media: api.MediaFacts{
				TrackCoverageComplete: true,
				Tracks:                []api.MediaTrackFacts{{Kind: api.MediaTrackSubtitle, Codec: "PGS"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			codecs, codecsComplete, channels, channelsComplete := duplicateAudioEvidence(test.media)
			if got, want := codecs, test.wantCodecs; !slices.Equal(got, want) {
				t.Fatalf("codecs = %#v, want %#v", got, want)
			}
			if got, want := channels, test.wantChannels; !slices.Equal(got, want) {
				t.Fatalf("channels = %#v, want %#v", got, want)
			}
			if codecsComplete != test.wantCodecsDone || channelsComplete != test.wantChannelsDone {
				t.Fatalf("completeness = %t/%t, want %t/%t", codecsComplete, channelsComplete, test.wantCodecsDone, test.wantChannelsDone)
			}
		})
	}
}
