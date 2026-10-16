// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package audioanalysis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/autobrr/upbrr/internal/services/ffmpegstream"
	"github.com/autobrr/upbrr/pkg/api"
)

// Batch attaches the ordinary audio processors to a shared media decode.
// The caller owns Close and may call Finish only after every output has ended.
type Batch struct {
	// CorrectiveDecode records a verified audio-only retry when measured
	// duration changes the existing spectrogram geometry.
	CorrectiveDecode bool
	service          *Service
	decoder          decoder
	path             string
	root             string
	attempt          string
	analyses         []*trackAnalysis
	variants         []api.AudioAnalysisVariant
	failures         map[int]error
	prepared         bool
	released         bool
	finished         bool
}

// PrepareSelectedBatch binds up to four prepared tracks to the current stream
// inventory. Selection order must follow source audio ordinals.
func (s *Service) PrepareSelectedBatch(ctx context.Context, subject api.AudioAnalysisSubject, selected []string, root, attempt string) (*Batch, error) {
	if len(selected) == 0 || len(selected) > 4 || root == "" {
		return nil, errors.New("shared audio requires one to four prepared tracks and an output root")
	}
	decoder, err := s.analysisDecoder()
	if err != nil {
		return nil, err
	}
	streams, err := decoder.Inspect(ctx, subject.VideoPath)
	if err != nil {
		return nil, fmt.Errorf("inspect prepared audio inventory: %w", err)
	}
	if _, err := bindTracks(subject, nil, streams); err != nil {
		return nil, err
	}
	var bindings, supported []trackBinding
	failures := make(map[int]error)
	for i, id := range selected {
		var prepared *api.MediaTrackFacts
		for _, track := range subject.Tracks {
			if track.Kind == api.MediaTrackAudio && track.ID == id {
				prepared = &track
				break
			}
		}
		if prepared == nil {
			return nil, errors.New("shared audio selection is not in the prepared inventory")
		}
		if i > 0 && prepared.Ordinal <= bindings[i-1].track.Ordinal {
			return nil, errors.New("shared audio selection is not in source order")
		}
		bound, err := bindTracks(subject, []string{id}, streams)
		binding := trackBinding{track: *prepared, audioOrdinal: prepared.Ordinal - 1}
		if err == nil {
			binding = bound[0]
			if audioCodecFamily(binding.track.Codec) == "" {
				err = audioAnalysisError(api.AudioAnalysisFailureUnsupportedCodec,
					"the selected audio codec is not supported by shared analysis",
					errors.New("shared audio codec family is unknown"))
			} else {
				_, err = boundedAudioFrames(binding)
			}
		}
		bindings = append(bindings, binding)
		if err != nil {
			failures[i] = err
		} else {
			supported = append(supported, binding)
		}
	}
	if err := batchMemory(supported); err != nil {
		return nil, err
	}
	if err := acquireAnalysisSlot(ctx); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		releaseAnalysisSlot()
		return nil, fmt.Errorf("create prepared audio output root: %w", err)
	}
	variants := []api.AudioAnalysisVariant{api.AudioAnalysisWaveform, api.AudioAnalysisSpectrogram, api.AudioAnalysisStats}
	batch := &Batch{
		service:  s,
		decoder:  decoder,
		path:     subject.VideoPath,
		root:     root,
		attempt:  attempt,
		variants: variants,
		failures: failures,
		prepared: true,
	}
	for i, binding := range bindings {
		selectedVariants := variants
		if failures[i] != nil {
			selectedVariants = nil
		}
		batch.analyses = append(batch.analyses, newTrackAnalysis(binding, selectedVariants))
	}
	return batch, nil
}

// PrepareBatch selects the same primary program track as standalone analysis.
// It preserves all channels, native sample rate, decoder DRC policy and all
// three existing output variants. A file with no audio returns a nil batch.
func (s *Service) PrepareBatch(ctx context.Context, path, root, attempt string) (*Batch, error) {
	decoder, err := s.analysisDecoder()
	if err != nil {
		return nil, err
	}
	streams, err := decoder.Inspect(ctx, path)
	if err != nil {
		if failure, ok := api.AsAudioAnalysisFailure(err); ok && failure.Code == api.AudioAnalysisFailureNoAudio {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect shared audio: %w", err)
	}
	if len(streams) == 0 {
		return nil, nil
	}
	ordinal := 0
	for i, stream := range streams {
		if !stream.secondaryTitle && !isSecondaryAudioTitle(stream.title) {
			ordinal = i
			break
		}
	}
	stream := streams[ordinal]
	if stream.channels < 1 || stream.channels > 8 || stream.sampleRate <= 0 {
		return nil, errors.New("primary audio has an unsupported channel or sample-rate mapping")
	}
	if err := acquireAnalysisSlot(ctx); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		releaseAnalysisSlot()
		return nil, fmt.Errorf("create shared audio output root: %w", err)
	}
	variants := []api.AudioAnalysisVariant{api.AudioAnalysisWaveform, api.AudioAnalysisSpectrogram, api.AudioAnalysisStats}
	binding := trackBinding{
		audioOrdinal:    ordinal,
		durationSeconds: stream.durationSeconds,
		track: api.MediaTrackFacts{
			ID:            fmt.Sprintf("audio-%d", ordinal+1),
			Kind:          api.MediaTrackAudio,
			Ordinal:       ordinal + 1,
			Title:         stream.title,
			Codec:         stream.codec,
			ChannelLayout: stream.layout,
			Channels:      stream.channels,
			SampleRate:    stream.sampleRate,
		},
	}
	return &Batch{
		service:  s,
		decoder:  decoder,
		path:     path,
		root:     root,
		attempt:  attempt,
		analyses: []*trackAnalysis{newTrackAnalysis(binding, variants)},
		variants: variants,
	}, nil
}

// Close releases the audio admission slot once, including canceled batches.
func (b *Batch) Close() {
	if b != nil && !b.released {
		b.released = true
		releaseAnalysisSlot()
	}
}

// InputArguments retains the original decoder's disabled DRC setting.
func (b *Batch) InputArguments() []string {
	var arguments []string
	if b != nil {
		for i, analysis := range b.analyses {
			if analysis != nil && b.failures[i] == nil && DecoderDRCDisabled(analysis.binding.track.Codec) {
				arguments = append(arguments, fmt.Sprintf("-drc_scale:a:%d", analysis.binding.audioOrdinal), "0")
			}
		}
	}
	return arguments
}

// Outputs supplies native-rate float PCM to the existing streaming consumers.
func (b *Batch) Outputs(ctx context.Context) []ffmpegstream.Output {
	if b == nil {
		return nil
	}
	outputs := make([]ffmpegstream.Output, 0, len(b.analyses)-len(b.failures))
	for i, analysis := range b.analyses {
		if b.failures[i] != nil {
			continue
		}
		outputs = append(outputs, ffmpegstream.Output{
			Arguments: []string{"-map", fmt.Sprintf("0:a:%d", analysis.binding.audioOrdinal), "-vn", "-sn", "-dn",
				"-c:a", "pcm_f32le", "-f", "f32le", "-flush_packets", "0"},
			Consume: func(reader io.Reader) error { return analysis.consume(ctx, b.service.logger, reader) },
		})
	}
	return outputs
}

// Finish publishes existing audio outputs. If actual duration changes SoX
// geometry, it performs the required verified corrective audio-only decode.
func (b *Batch) Finish(ctx context.Context, outputErrors []error, processError error) ([]TrackResult, error) {
	var results []TrackResult
	err := b.FinishEach(ctx, outputErrors, processError, func(result TrackResult) error {
		results = append(results, result)
		return nil
	})
	return results, err
}

// DecodeEach runs an audio-only continuation batch, then publishes each track
// independently. It does not retry an incomplete batch or reread metadata.
func (b *Batch) DecodeEach(ctx context.Context, publish func(TrackResult) error) error {
	if b == nil || b.released || b.finished || publish == nil {
		return errors.New("audio-only batch lacks completion authority")
	}
	request := decodeRequest{path: b.path, resourceLimits: api.AudioAnalysisResourceLimits{DecoderThreads: api.AudioAnalysisDefaultDecoderThreads}}
	var consumers []func(io.Reader) error
	for i, analysis := range b.analyses {
		if b.failures[i] != nil {
			continue
		}
		request.streams = append(request.streams, decodeStreamRequest{audioOrdinal: analysis.binding.audioOrdinal, codec: analysis.binding.track.Codec})
		consumers = append(consumers, func(reader io.Reader) error { return analysis.consume(ctx, b.service.logger, reader) })
	}
	outputErrors := make([]error, len(consumers))
	var processError error
	if len(consumers) > 0 {
		err := b.decoder.Decode(ctx, request, consumers)
		if err != nil {
			completed, failed := completedDecodeStreams(err, len(consumers))
			if failed {
				processError = err
			}
			for i := range outputErrors {
				if !completed[i] {
					outputErrors[i] = err
				}
			}
		}
	}
	return b.FinishEach(ctx, outputErrors, processError, publish)
}

// FinishEach publishes and releases each track before proceeding to the next.
// The callback must retain any evidence it needs before returning.
func (b *Batch) FinishEach(ctx context.Context, outputErrors []error, processError error, publish func(TrackResult) error) error {
	if b == nil {
		return nil
	}
	if len(outputErrors) != len(b.analyses)-len(b.failures) || b.released || b.finished || publish == nil {
		b.service.logger.Warnf("audioanalysis: state=blocked phase=shared_completion expected=%d received=%d", len(b.analyses), len(outputErrors))
		return errors.New("shared audio batch has invalid completion authority")
	}
	b.finished = true
	defer clear(b.analyses)
	outputIndex := 0
	for i, analysis := range b.analyses {
		err := b.failures[i]
		corrected := false
		if err == nil {
			err = errors.Join(outputErrors[outputIndex], processError)
			outputIndex++
		}
		if analysis.needsGeometryRetry && processError == nil && ctx.Err() == nil {
			if b.prepared {
				err = batchCorrectionMemory(b.analyses, b.failures, analysis)
			} else {
				err = nil
			}
			if err == nil {
				b.CorrectiveDecode = true
				corrected = true
				b.service.logger.Infof(
					"audioanalysis: state=retrying phase=decode track_ordinal=%d reason=spectrogram_geometry",
					analysis.binding.track.Ordinal,
				)
				analysis, err = b.service.retryGeometry(ctx, b.decoder, b.path, analysis, b.variants,
					api.AudioAnalysisResourceLimits{DecoderThreads: api.AudioAnalysisDefaultDecoderThreads})
			}
		}
		if err != nil {
			failure := failureForError(err)
			b.service.logger.Warnf("audioanalysis: state=failed phase=shared_decode track_ordinal=%d code=%s", analysis.binding.track.Ordinal, failure.Code)
			analysis.public.Status, analysis.public.Failure = api.StageStatusFailed, &failure
			if publishErr := publish(TrackResult{Public: analysis.public, CorrectiveDecode: corrected}); publishErr != nil {
				return publishErr
			}
			b.analyses[i] = nil
			if ctx.Err() != nil || failure.Code == api.AudioAnalysisFailureStaleSource {
				return err
			}
			continue
		}
		result, err := b.service.publishTrack(b.root, b.attempt, b.variants, analysis)
		result.PCMComplete = analysis.frames > 0
		result.CorrectiveDecode = corrected
		if publishErr := publish(result); publishErr != nil {
			return errors.Join(err, publishErr)
		}
		b.analyses[i] = nil
		if err != nil {
			b.service.logger.Warnf("audioanalysis: state=failed phase=shared_publish track_ordinal=%d", analysis.binding.track.Ordinal)
			return err
		}
	}
	return nil
}

func (s *Service) retryGeometry(ctx context.Context, decoder decoder, path string, original *trackAnalysis,
	variants []api.AudioAnalysisVariant, limits api.AudioAnalysisResourceLimits,
) (*trackAnalysis, error) {
	retry := newTrackAnalysis(original.binding, variants)
	retry.totalFrames = original.frames
	request := decodeRequest{
		path:           path,
		resourceLimits: limits,
		streams:        []decodeStreamRequest{{audioOrdinal: original.binding.audioOrdinal, codec: original.binding.track.Codec}},
	}
	err := decoder.Decode(ctx, request, []func(io.Reader) error{
		func(reader io.Reader) error { return retry.consume(ctx, s.logger, reader) },
	})
	if err == nil && (retry.frames != original.frames || retry.digest != original.digest) ||
		errors.Is(err, errSpectrogramGeometryChanged) && retry.frames != original.frames {
		err = audioAnalysisError(api.AudioAnalysisFailureStaleSource, "decoded audio changed between analysis passes", errDecodedSourceChanged)
	}
	return retry, err
}
