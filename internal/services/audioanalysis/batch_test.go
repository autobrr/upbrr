// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSharedBatchSelectsPrimaryAndVerifiesCorrectivePCM(t *testing.T) {
	const rate = 48000
	pcm := bytes.Repeat([]byte{0, 0, 128, 62}, rate*10)
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "changed PCM"}[changed], func(t *testing.T) {
			decoder := &fakeDecoder{
				streams: []inspectedStream{
					{
						title:      "Commentary",
						channels:   1,
						sampleRate: rate,
						layout:     "mono",
						codec:      "pcm",
					},
					{
						channels:   1,
						sampleRate: rate,
						layout:     "mono",
						codec:      "eac3",
					},
				},
				pcm:     pcm,
				decodes: 1,
			}
			if changed {
				decoder.retryPCM = bytes.Repeat([]byte{0, 0, 0, 63}, rate*10)
			}
			batch, err := newService(api.NopLogger{}, decoder).PrepareBatch(t.Context(), "Synthetic.Audio.mkv", t.TempDir(), "shared")
			if err != nil {
				t.Fatal(err)
			}
			defer batch.Close()
			outputs := batch.Outputs(t.Context())
			if len(outputs) != 1 || !slices.Contains(outputs[0].Arguments, "0:a:1") || !slices.Equal(batch.InputArguments(), []string{"-drc_scale:a:1", "0"}) {
				t.Fatal("primary-track selection or DRC policy changed")
			}
			consumeErr := outputs[0].Consume(bytes.NewReader(pcm))
			if !errors.Is(consumeErr, errSpectrogramGeometryChanged) {
				t.Fatalf("unknown-duration geometry: %v", consumeErr)
			}
			results, err := batch.Finish(t.Context(), []error{consumeErr}, nil)
			if !batch.CorrectiveDecode || decoder.decodes != 2 || len(results) != 1 {
				t.Fatalf("corrective decode was not tracked: %v", err)
			}
			if changed {
				failure, ok := api.AsAudioAnalysisFailure(err)
				if !ok || failure.Code != api.AudioAnalysisFailureStaleSource || len(results[0].Artifacts) != 0 {
					t.Fatal("changed corrective PCM was published")
				}
			} else if err != nil || results[0].Public.Status != api.StageStatusCompleted || results[0].Public.SampleFrames != rate*10 || len(results[0].Artifacts) != 3 {
				t.Fatalf("verified audio outputs differ: %+v %v", results, err)
			}
		})
	}
}

func TestPreparedBatchKeepsTrackAuthorityAndIsolatesDecodeFailure(t *testing.T) {
	const rate = 8000
	streams := []inspectedStream{
		{
			channels:        1,
			sampleRate:      rate,
			layout:          "mono",
			codec:           "pcm",
			durationSeconds: 1,
		},
		{
			channels:        1,
			sampleRate:      rate,
			layout:          "mono",
			codec:           "eac3",
			durationSeconds: 1,
		},
	}
	subject := api.AudioAnalysisSubject{VideoPath: "Synthetic.MultiAudio.mkv", ResourceID: "resource"}
	for i, id := range []string{"prepared-main", "prepared-alternate"} {
		subject.Tracks = append(subject.Tracks, api.MediaTrackFacts{
			ID:            id,
			ResourceID:    subject.ResourceID,
			Kind:          api.MediaTrackAudio,
			Ordinal:       i + 1,
			Codec:         streams[i].codec,
			Channels:      1,
			SampleRate:    rate,
			ChannelLayout: "mono",
		})
	}
	service := newService(api.NopLogger{}, &fakeDecoder{streams: streams})
	batch, err := service.PrepareSelectedBatch(t.Context(), subject, []string{"prepared-main", "prepared-alternate"}, t.TempDir(), "prepared")
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Close()
	if !slices.Equal(batch.InputArguments(), []string{"-drc_scale:a:1", "0"}) {
		t.Fatal("later DRC-capable track lost decoder policy")
	}
	outputs := batch.Outputs(t.Context())
	if !slices.Contains(outputs[0].Arguments, "0:a:0") || !slices.Contains(outputs[1].Arguments, "0:a:1") {
		t.Fatal("prepared track ordinal changed")
	}
	consumeErr := outputs[1].Consume(bytes.NewReader(bytes.Repeat([]byte{0, 0, 128, 62}, rate)))
	var ids []string
	err = batch.FinishEach(t.Context(), []error{errors.New("synthetic failed stream"), consumeErr}, nil, func(result TrackResult) error {
		ids = append(ids, result.Public.TrackID)
		if len(ids) == 1 && (result.Public.Status != api.StageStatusFailed || len(result.Artifacts) != 0) {
			t.Fatal("failed decode published artifacts")
		}
		if len(ids) == 2 && (result.Public.Status != api.StageStatusCompleted || result.Public.SampleFrames != rate || batch.analyses[0] != nil) {
			t.Fatal("sibling decode failed or previous analysis was retained")
		}
		return nil
	})
	if err != nil || !slices.Equal(ids, []string{"prepared-main", "prepared-alternate"}) {
		t.Fatalf("incremental outcomes: %v %v", ids, err)
	}
	if err := batch.FinishEach(t.Context(), []error{nil, nil}, nil, func(TrackResult) error { return nil }); err == nil {
		t.Fatal("batch completed twice")
	}
	if _, err := service.PrepareSelectedBatch(t.Context(), subject, []string{"prepared-alternate", "prepared-main"}, t.TempDir(), "wrong-order"); err == nil {
		t.Fatal("out-of-order inventory accepted")
	}
}

func TestPreparedBatchUnsupportedTrackDoesNotDiscardSupportedSibling(t *testing.T) {
	streams := []inspectedStream{{
		channels:   12,
		sampleRate: 48000,
		codec:      "pcm",
	}, {
		channels:        1,
		sampleRate:      8000,
		codec:           "pcm",
		durationSeconds: 1,
	}}
	subject := api.AudioAnalysisSubject{VideoPath: "Synthetic.MixedLayout.mkv", Tracks: []api.MediaTrackFacts{
		{
			ID:         "unsupported",
			Kind:       api.MediaTrackAudio,
			Ordinal:    1,
			Channels:   12,
			SampleRate: 48000,
			Codec:      "pcm",
		},
		{
			ID:         "supported",
			Kind:       api.MediaTrackAudio,
			Ordinal:    2,
			Channels:   1,
			SampleRate: 8000,
			Codec:      "pcm",
		},
	}}
	batch, err := newService(api.NopLogger{}, &fakeDecoder{streams: streams}).PrepareSelectedBatch(t.Context(), subject,
		[]string{"unsupported", "supported"}, t.TempDir(), "layouts")
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Close()
	outputs := batch.Outputs(t.Context())
	if len(outputs) != 1 || !slices.Contains(outputs[0].Arguments, "0:a:1") || batch.analyses[0].waveform != nil {
		t.Fatal("unsupported track allocated DSP or changed the healthy stream mapping")
	}
	consumeErr := outputs[0].Consume(bytes.NewReader(bytes.Repeat([]byte{0, 0, 128, 62}, 8000)))
	results, err := batch.Finish(t.Context(), []error{consumeErr}, nil)
	if err != nil || len(results) != 2 || results[0].Public.TrackID != "unsupported" || results[0].PCMComplete ||
		results[0].Public.Failure == nil || results[0].Public.Failure.Code != api.AudioAnalysisFailureUnsupportedLayout ||
		results[1].Public.TrackID != "supported" || !results[1].PCMComplete || len(results[1].Artifacts) != 3 {
		t.Fatalf("track isolation: %+v %v", results, err)
	}
}

func TestPreparedBatchUnknownCodecPreservesSupportedSelection(t *testing.T) {
	streams := []inspectedStream{{
		channels:        1,
		sampleRate:      8000,
		codec:           "pcm",
		durationSeconds: 1,
	},
		{
			channels:        1,
			sampleRate:      8000,
			codec:           "tta",
			durationSeconds: 1,
		}}
	subject := api.AudioAnalysisSubject{VideoPath: "Synthetic.MixedCodecs.mkv", Tracks: []api.MediaTrackFacts{
		{
			ID:         "primary",
			Kind:       api.MediaTrackAudio,
			Ordinal:    1,
			Codec:      "pcm",
			Channels:   1,
			SampleRate: 8000,
		},
		{
			ID:         "alternate",
			Kind:       api.MediaTrackAudio,
			Ordinal:    2,
			Codec:      "TTA",
			Channels:   1,
			SampleRate: 8000,
		},
	}}
	service := newService(api.NopLogger{}, &fakeDecoder{streams: streams})
	for _, selected := range [][]string{{"primary"}, {"primary", "alternate"}} {
		batch, err := service.PrepareSelectedBatch(t.Context(), subject, selected, t.TempDir(), "codecs")
		if err != nil {
			t.Fatal(err)
		}
		outputs := batch.Outputs(t.Context())
		if len(outputs) != 1 || !slices.Contains(outputs[0].Arguments, "0:a:0") {
			t.Fatal("unknown alternate codec blocked the supported primary")
		}
		consumeErr := outputs[0].Consume(bytes.NewReader(bytes.Repeat([]byte{0, 0, 128, 62}, 8000)))
		results, err := batch.Finish(t.Context(), []error{consumeErr}, nil)
		batch.Close()
		if err != nil || len(results) != len(selected) || !results[0].PCMComplete {
			t.Fatalf("supported track did not complete: %+v %v", results, err)
		}
		if len(results) == 2 && (results[1].PCMComplete || results[1].Public.Failure == nil ||
			results[1].Public.Failure.Code != api.AudioAnalysisFailureUnsupportedCodec) {
			t.Fatalf("unknown codec did not retain an isolated unsupported outcome: %+v", results[1])
		}
	}
	subject.Tracks[1].Codec = "different"
	if _, err := service.PrepareSelectedBatch(t.Context(), subject, []string{"primary"}, t.TempDir(), "mismatch"); err == nil {
		t.Fatal("different unknown codec identities accepted")
	}
}
