// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

type fakeDecoder struct {
	streams []inspectedStream
	pcm     []byte
	decodes int
	err     error
}

func (d *fakeDecoder) Inspect(context.Context, string) ([]inspectedStream, error) {
	return append([]inspectedStream(nil), d.streams...), d.err
}

func (d *fakeDecoder) Decode(ctx context.Context, _ decodeRequest, consume func(io.Reader) error) error {
	d.decodes++
	if d.err != nil {
		return d.err
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("fake decoder canceled: %w", ctx.Err())
	default:
	}
	return consume(bytes.NewReader(d.pcm))
}

func TestServiceStreamsTwoPassWaveformAndPublishesPNG(t *testing.T) {
	frames := make([][]float32, 2048)
	for frame := range frames {
		frames[frame] = []float32{float32(mathSin(frame, 32)), float32(mathSin(frame, 64))}
	}
	decoder := &fakeDecoder{
		streams: []inspectedStream{{
			codec:      "pcm",
			sampleRate: 48_000,
			layout:     "stereo",
			channels:   2,
		}},
		pcm: encodePCM(frames),
	}
	service := newService(api.NopLogger{}, decoder)
	track := api.MediaTrackFacts{
		ID:                  "track_one",
		Kind:                api.MediaTrackAudio,
		ResourceID:          "media_one",
		ManifestFingerprint: "manifest",
		Ordinal:             1,
		Codec:               "pcm",
		ChannelLayout:       "stereo",
		Channels:            2,
		SampleRate:          48_000,
	}
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	result, err := service.Analyze(t.Context(), api.AudioAnalysisSubject{
		Release:             release,
		SourcePath:          release.SourcePath,
		VideoPath:           "input.mkv",
		ResourceID:          track.ResourceID,
		ManifestFingerprint: track.ManifestFingerprint,
		PrimaryTrackID:      track.ID,
		Tracks:              []api.MediaTrackFacts{track},
	}, api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     track.ResourceID,
		Selection:      api.AudioAnalysisSelectionPrimary,
		TrackIDs:       []string{track.ID},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}, "attempt_test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if decoder.decodes != 2 {
		t.Fatalf("decode passes = %d, want 2", decoder.decodes)
	}
	if result.Public.Status != api.StageStatusCompleted || len(result.Artifacts) != 1 {
		t.Fatalf("result = %#v", result)
	}
	artifact := result.Artifacts[0]
	if info, err := os.Stat(artifact.Path); err != nil || info.Size() == 0 {
		t.Fatalf("artifact stat = %v, %v", info, err)
	}
}

func TestClassifyTopLevelFailureDistinguishesCancellationAndDeadline(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
		code api.AudioAnalysisFailureCode
	}{
		{
			name: "canceled",
			err:  context.Canceled,
			code: api.AudioAnalysisFailureCanceled,
		},
		{
			name: "deadline",
			err:  context.DeadlineExceeded,
			code: api.AudioAnalysisFailureInterrupted,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure, ok := api.AsAudioAnalysisFailure(classifyTopLevelFailure(test.err))
			if !ok || failure.Code != test.code {
				t.Fatalf("failure = %#v, want code %s", failure, test.code)
			}
		})
	}
}

func TestServiceValidateSelectionRejectsUnsupportedSelectedTrackBeforeDecode(t *testing.T) {
	subject := audioSubjectForBinding([]string{"1", "2"})
	subject.Tracks[1].Channels = 12
	subject.Tracks[1].ChannelLayout = "12 channels"
	decoder := &fakeDecoder{streams: []inspectedStream{
		{
			codec:      "flac",
			sampleRate: 48_000,
			layout:     "stereo",
			channels:   2,
		},
		{
			codec:      "flac",
			sampleRate: 48_000,
			layout:     "12 channels",
			channels:   12,
		},
	}}
	service := newService(api.NopLogger{}, decoder)
	published := 0
	service.publishPNG = func(string, string, image.Image) error {
		published++
		return nil
	}
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	err := service.ValidateSelection(t.Context(), subject, api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     "resource-1",
		Selection:      api.AudioAnalysisSelectionSelected,
		TrackIDs:       []string{"track-1", "track-2"},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	})
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureUnsupportedLayout {
		t.Fatalf("validation error = %v, failure = %#v", err, failure)
	}
	if decoder.decodes != 0 || published != 0 {
		t.Fatalf("validation decoded %d times and published %d images", decoder.decodes, published)
	}
}

func TestServiceUsesPerChannelSampleFramesAcrossNativeLayoutsAndRates(t *testing.T) {
	const frameCount = 257
	for _, test := range []struct {
		name       string
		channels   int
		sampleRate int
		layout     string
	}{
		{
			name:       "mono-44.1k",
			channels:   1,
			sampleRate: 44_100,
			layout:     "mono",
		},
		{
			name:       "stereo-48k",
			channels:   2,
			sampleRate: 48_000,
			layout:     "stereo",
		},
		{
			name:       "5.1-side-48k",
			channels:   6,
			sampleRate: 48_000,
			layout:     "5.1(side)",
		},
		{
			name:       "7.1-96k",
			channels:   8,
			sampleRate: 96_000,
			layout:     "7.1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := make([][]float32, frameCount)
			for frame := range frames {
				frames[frame] = make([]float32, test.channels)
				for channel := range test.channels {
					frames[frame][channel] = float32(math.Sin(2 * math.Pi * float64((channel+1)*frame) / 32))
				}
			}
			decoder := &fakeDecoder{
				streams: []inspectedStream{{
					codec:      "pcm",
					sampleRate: test.sampleRate,
					layout:     test.layout,
					channels:   test.channels,
				}},
				pcm: encodePCM(frames),
			}
			service := newService(api.NopLogger{}, decoder)
			track := api.MediaTrackFacts{
				ID:                  "track_one",
				Kind:                api.MediaTrackAudio,
				ResourceID:          "media_one",
				ManifestFingerprint: "manifest",
				Ordinal:             1,
				Codec:               "pcm",
				ChannelLayout:       test.layout,
				Channels:            test.channels,
				SampleRate:          test.sampleRate,
			}
			release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
			result, err := service.Analyze(t.Context(), api.AudioAnalysisSubject{
				Release:             release,
				SourcePath:          release.SourcePath,
				VideoPath:           "input.mkv",
				ResourceID:          track.ResourceID,
				ManifestFingerprint: track.ManifestFingerprint,
				PrimaryTrackID:      track.ID,
				Tracks:              []api.MediaTrackFacts{track},
			}, api.AudioAnalysisInstructions{
				Release:        release,
				ResourceID:     track.ResourceID,
				Selection:      api.AudioAnalysisSelectionPrimary,
				TrackIDs:       []string{track.ID},
				Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
				ProfileVersion: api.AudioAnalysisProfileVersion,
			}, "attempt_"+strings.ReplaceAll(test.name, ".", "_"), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			got := result.Public
			if got.SampleFrames != frameCount || math.Abs(got.Duration-float64(frameCount)/float64(test.sampleRate)) > 1e-12 {
				t.Fatalf("decoded facts = frames:%d duration:%v", got.SampleFrames, got.Duration)
			}
			if got.Artifacts[0].Width != 1812 || got.Artifacts[0].Height != test.channels*waveformPanelHeight+waveformAxisHeight {
				t.Fatalf("waveform dimensions = %dx%d", got.Artifacts[0].Width, got.Artifacts[0].Height)
			}
		})
	}
}

func TestServiceRejectsChangedSecondPass(t *testing.T) {
	decoder := &changingDecoder{
		streams: []inspectedStream{
			{
				codec:      "pcm",
				sampleRate: 48_000,
				layout:     "mono",
				channels:   1,
			},
			{
				codec:      "pcm",
				sampleRate: 48_000,
				layout:     "mono",
				channels:   1,
			},
		},
	}
	service := newService(api.NopLogger{}, decoder)
	publishCalls := 0
	service.publishPNG = func(string, string, image.Image) error {
		publishCalls++
		return nil
	}
	firstTrack := api.MediaTrackFacts{
		ID:                  "track_one",
		Kind:                api.MediaTrackAudio,
		ResourceID:          "media_one",
		ManifestFingerprint: "manifest",
		Ordinal:             1,
		Channels:            1,
		SampleRate:          48_000,
	}
	secondTrack := firstTrack
	secondTrack.ID = "track_two"
	secondTrack.Ordinal = 2
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	result, err := service.Analyze(t.Context(), api.AudioAnalysisSubject{
		Release:             release,
		SourcePath:          release.SourcePath,
		VideoPath:           "input.mkv",
		ResourceID:          firstTrack.ResourceID,
		ManifestFingerprint: firstTrack.ManifestFingerprint,
		PrimaryTrackID:      firstTrack.ID,
		Tracks:              []api.MediaTrackFacts{firstTrack, secondTrack},
	}, api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     firstTrack.ResourceID,
		Selection:      api.AudioAnalysisSelectionSelected,
		TrackIDs:       []string{firstTrack.ID},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}, "attempt_changed", t.TempDir())
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureStaleSource {
		t.Fatalf("error = %v, failure = %#v", err, failure)
	}
	if decoder.passes != 2 || publishCalls != 0 || result.Public.Status != api.StageStatusFailed ||
		result.Public.Failure == nil || result.Public.Failure.Code != api.AudioAnalysisFailureStaleSource {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceRejectsSameLengthSampleChangeBetweenPasses(t *testing.T) {
	decoder := &changingSamplesDecoder{
		streams: []inspectedStream{{
			codec:      "pcm",
			sampleRate: 48_000,
			layout:     "mono",
			channels:   1,
		}},
	}
	service := newService(api.NopLogger{}, decoder)
	track := api.MediaTrackFacts{
		ID:                  "track_one",
		Kind:                api.MediaTrackAudio,
		ResourceID:          "media_one",
		ManifestFingerprint: "manifest",
		Ordinal:             1,
		Channels:            1,
		SampleRate:          48_000,
	}
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	result, err := service.Analyze(t.Context(), api.AudioAnalysisSubject{
		Release:             release,
		SourcePath:          release.SourcePath,
		VideoPath:           "input.mkv",
		ResourceID:          track.ResourceID,
		ManifestFingerprint: track.ManifestFingerprint,
		PrimaryTrackID:      track.ID,
		Tracks:              []api.MediaTrackFacts{track},
	}, api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     track.ResourceID,
		Selection:      api.AudioAnalysisSelectionPrimary,
		TrackIDs:       []string{track.ID},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}, "attempt_changed_samples", t.TempDir())
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureStaleSource {
		t.Fatalf("error = %v, failure = %#v", err, failure)
	}
	if result.Public.Status != api.StageStatusFailed || result.Public.Failure == nil ||
		result.Public.Failure.Code != api.AudioAnalysisFailureStaleSource {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceRetainsSiblingAfterVariantLocalPublishFailure(t *testing.T) {
	decoder := &fakeDecoder{
		streams: []inspectedStream{{
			codec:      "pcm",
			sampleRate: 48_000,
			layout:     "mono",
			channels:   1,
		}},
		pcm: encodePCM([][]float32{{0.25}, {-0.25}, {0.5}, {-0.5}}),
	}
	service := newService(api.NopLogger{}, decoder)
	publishCalls := 0
	service.publishPNG = func(root string, path string, rendered image.Image) error {
		publishCalls++
		if strings.HasSuffix(path, string(api.AudioAnalysisWaveform)+".png") {
			return errors.New("synthetic variant-local encode failure")
		}
		return writePNGAtomic(root, path, rendered)
	}
	track := api.MediaTrackFacts{
		ID:                  "track_one",
		Kind:                api.MediaTrackAudio,
		ResourceID:          "media_one",
		ManifestFingerprint: "manifest",
		Ordinal:             1,
		Codec:               "pcm",
		ChannelLayout:       "mono",
		Channels:            1,
		SampleRate:          48_000,
	}
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	result, err := service.Analyze(t.Context(), api.AudioAnalysisSubject{
		Release:             release,
		SourcePath:          release.SourcePath,
		VideoPath:           "input.mkv",
		ResourceID:          track.ResourceID,
		ManifestFingerprint: track.ManifestFingerprint,
		PrimaryTrackID:      track.ID,
		Tracks:              []api.MediaTrackFacts{track},
	}, api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     track.ResourceID,
		Selection:      api.AudioAnalysisSelectionPrimary,
		TrackIDs:       []string{track.ID},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform, api.AudioAnalysisSpectrogram},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}, "attempt_variant_partial", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if publishCalls != 2 || result.Public.Status != api.StageStatusPartial ||
		len(result.Public.Artifacts) != 2 || len(result.Artifacts) != 1 ||
		result.Public.Artifacts[0].Status != api.StageStatusFailed ||
		result.Public.Artifacts[1].Status != api.StageStatusCompleted {
		t.Fatalf("publish calls = %d, result = %#v", publishCalls, result)
	}
}

func TestServiceReturnsServiceWidePublishFailure(t *testing.T) {
	decoder := &fakeDecoder{
		streams: []inspectedStream{
			{
				codec:      "pcm",
				sampleRate: 48_000,
				layout:     "mono",
				channels:   1,
			},
			{
				codec:      "pcm",
				sampleRate: 48_000,
				layout:     "mono",
				channels:   1,
			},
		},
		pcm: encodePCM([][]float32{{0.25}, {-0.25}}),
	}
	service := newService(api.NopLogger{}, decoder)
	publishCalls := 0
	service.publishPNG = func(root string, path string, rendered image.Image) error {
		publishCalls++
		if err := os.RemoveAll(root); err != nil {
			return fmt.Errorf("remove synthetic output root: %w", err)
		}
		return writePNGAtomic(root, path, rendered)
	}
	tracks := []api.MediaTrackFacts{
		{
			ID:                  "track_one",
			Kind:                api.MediaTrackAudio,
			ResourceID:          "media_one",
			ManifestFingerprint: "manifest",
			Ordinal:             1,
			Codec:               "pcm",
			ChannelLayout:       "mono",
			Channels:            1,
			SampleRate:          48_000,
		},
		{
			ID:                  "track_two",
			Kind:                api.MediaTrackAudio,
			ResourceID:          "media_one",
			ManifestFingerprint: "manifest",
			Ordinal:             2,
			Codec:               "pcm",
			ChannelLayout:       "mono",
			Channels:            1,
			SampleRate:          48_000,
		},
	}
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	result, err := service.Analyze(t.Context(), api.AudioAnalysisSubject{
		Release:             release,
		SourcePath:          release.SourcePath,
		VideoPath:           "input.mkv",
		ResourceID:          "media_one",
		ManifestFingerprint: "manifest",
		PrimaryTrackID:      "track_one",
		Tracks:              tracks,
	}, api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     "media_one",
		Selection:      api.AudioAnalysisSelectionSelected,
		TrackIDs:       []string{"track_one"},
		Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}, "attempt_storage_stop", t.TempDir())
	failure, typed := api.AsAudioAnalysisFailure(err)
	if !typed || failure.Code != api.AudioAnalysisFailureResourceUnavailable || publishCalls != 1 ||
		decoder.decodes != 2 || result.Public.TrackID != "track_one" {
		t.Fatalf("error = %v, failure = %#v, publish calls = %d, decodes = %d, result = %#v",
			err, failure, publishCalls, decoder.decodes, result)
	}
}

type changingDecoder struct {
	streams []inspectedStream
	passes  int
}

type changingSamplesDecoder struct {
	streams []inspectedStream
	passes  int
}

func (d *changingSamplesDecoder) Inspect(context.Context, string) ([]inspectedStream, error) {
	return d.streams, nil
}

func (d *changingSamplesDecoder) Decode(_ context.Context, _ decodeRequest, consume func(io.Reader) error) error {
	d.passes++
	value := float32(0.25)
	if d.passes == 2 {
		value = 0.5
	}
	return consume(bytes.NewReader(encodePCM([][]float32{{value}, {-value}})))
}

func (d *changingDecoder) Inspect(context.Context, string) ([]inspectedStream, error) {
	return d.streams, nil
}

func (d *changingDecoder) Decode(_ context.Context, _ decodeRequest, consume func(io.Reader) error) error {
	d.passes++
	count := 2
	if d.passes == 2 {
		count = 3
	}
	frames := make([][]float32, count)
	for index := range frames {
		frames[index] = []float32{float32(index)}
	}
	return consume(bytes.NewReader(encodePCM(frames)))
}

func TestServiceHonorsCancellationBeforeDecode(t *testing.T) {
	analysisSlots <- struct{}{}
	analysisSlots <- struct{}{}
	defer func() {
		<-analysisSlots
		<-analysisSlots
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	service := newService(api.NopLogger{}, &fakeDecoder{})
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	_, err := service.Analyze(ctx, api.AudioAnalysisSubject{}, api.AudioAnalysisInstructions{
		Release:    release,
		ResourceID: "media",
		Selection:  api.AudioAnalysisSelectionPrimary,
		TrackIDs:   []string{"track"},
		Variants:   []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
	}, "attempt_cancel", t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestServiceClassifiesDeadlineWhileWaitingForAdmission(t *testing.T) {
	analysisSlots <- struct{}{}
	analysisSlots <- struct{}{}
	defer func() {
		<-analysisSlots
		<-analysisSlots
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	service := newService(api.NopLogger{}, &fakeDecoder{})
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	_, err := service.Analyze(ctx, api.AudioAnalysisSubject{}, api.AudioAnalysisInstructions{
		Release:    release,
		ResourceID: "media",
		Selection:  api.AudioAnalysisSelectionPrimary,
		TrackIDs:   []string{"track"},
		Variants:   []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
	}, "attempt_deadline", t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	failure, ok := api.AsAudioAnalysisFailure(err)
	if !ok || failure.Code != api.AudioAnalysisFailureInterrupted {
		t.Fatalf("failure = %#v, want interrupted", failure)
	}
}

type blockingDecoder struct {
	stream  inspectedStream
	started chan struct{}
}

func (d blockingDecoder) Inspect(context.Context, string) ([]inspectedStream, error) {
	return []inspectedStream{d.stream}, nil
}

func (d blockingDecoder) Decode(ctx context.Context, _ decodeRequest, _ func(io.Reader) error) error {
	close(d.started)
	<-ctx.Done()
	return fmt.Errorf("blocking decoder canceled: %w", ctx.Err())
}

func TestServiceCancellationInterruptsStreamingDecode(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	decoder := blockingDecoder{
		stream: inspectedStream{
			codec:      "flac",
			sampleRate: 48_000,
			layout:     "mono",
			channels:   1,
		},
		started: make(chan struct{}),
	}
	service := newService(api.NopLogger{}, decoder)
	attemptRoot := t.TempDir()
	release := api.ReleaseRef{SourcePath: "Example.Release.2026.mkv", Generation: 1}
	track := api.MediaTrackFacts{
		ID:                  "track-1",
		Kind:                api.MediaTrackAudio,
		ResourceID:          "resource-1",
		ManifestFingerprint: "manifest-1",
		Ordinal:             1,
		Codec:               "flac",
		Channels:            1,
		SampleRate:          48_000,
	}
	type outcome struct {
		result TrackResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := service.Analyze(ctx, api.AudioAnalysisSubject{
			Release:             release,
			SourcePath:          release.SourcePath,
			VideoPath:           "input.mkv",
			ResourceID:          "resource-1",
			ManifestFingerprint: "manifest-1",
			PrimaryTrackID:      "track-1",
			Tracks:              []api.MediaTrackFacts{track},
		}, api.AudioAnalysisInstructions{
			Release:        release,
			ResourceID:     "resource-1",
			Selection:      api.AudioAnalysisSelectionPrimary,
			TrackIDs:       []string{"track-1"},
			Variants:       []api.AudioAnalysisVariant{api.AudioAnalysisWaveform},
			ProfileVersion: api.AudioAnalysisProfileVersion,
		}, "attempt-cancel-stream", attemptRoot)
		done <- outcome{result: result, err: err}
	}()
	<-decoder.started
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled service error = %v", got.err)
		}
		failure, typed := api.AsAudioAnalysisFailure(got.err)
		if !typed || failure.Code != api.AudioAnalysisFailureCanceled ||
			got.result.Public.Status != api.StageStatusFailed || got.result.Public.Failure == nil ||
			got.result.Public.Failure.Code != api.AudioAnalysisFailureCanceled {
			t.Fatalf("canceled count-pass result = %#v, failure = %#v", got.result, failure)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("streaming decode did not stop within two seconds")
	}
}

func mathSin(frame int, period int) float64 {
	return math.Sin(2 * math.Pi * float64(frame%period) / float64(period))
}
