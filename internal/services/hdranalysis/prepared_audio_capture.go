// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

// PreparedAudioCapture supplies the exact prepared selection requiring new PCM
// measurement. Publish must retain/render one report before returning; reports
// are not accumulated across tracks. CheckpointNative runs before Publish and
// must retain native evidence before releasing any parser storage it no longer needs.
type PreparedAudioCapture struct {
	Subject          api.AudioAnalysisSubject
	Selected         []string
	CheckpointNative func(*NativeExtraction, []*MeasurementReport) error
	Publish          func(*MeasurementReport) error
}

type preparedAudioCapture struct {
	PreparedAudioCapture
	pending      []string
	current      []string
	checkpointed bool
	traversals   int
	corrections  int
}

// MeasurePreparedAudio measures newly selected tracks using audio-only batches
// when a workflow already owns compatible native metadata. It never rereads or
// regenerates that metadata.
func (s *Session) MeasurePreparedAudio(ctx context.Context, input DecodedInput, options api.HDRDecodedOptions,
	directory string, otherBytes int64, capture PreparedAudioCapture,
) error {
	if err := validatePreparedAudioCapture(input, capture); err != nil {
		return fmt.Errorf("capture prepared audio: %w", err)
	}
	if otherBytes < 0 || otherBytes > MaxRestoreBytes-audioanalysis.SharedBatchMemoryBytes-128<<20 {
		return errors.New("prepared audio continuation exceeds shared memory")
	}
	if err := options.Validate(); err != nil {
		return fmt.Errorf("capture prepared audio: %w", err)
	}
	root, err := os.MkdirTemp(directory, ".hdr-audio-")
	if err != nil {
		return fmt.Errorf("capture prepared audio: %w", err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	fingerprint, err := CurrentDecoderFingerprint(ctx)
	if err != nil {
		return fmt.Errorf("capture prepared audio: %w", err)
	}
	prepared := &preparedAudioCapture{PreparedAudioCapture: capture, pending: append([]string(nil), capture.Selected...)}
	defer prepared.reportTraversals(ctx, s.service)
	return prepared.remaining(ctx, audioanalysis.NewService(s.service.logger), root, input, options, DecoderIdentity{Fingerprint: fingerprint})
}

// AnalyzePreparedAutomatic shares the first bounded audio batch with native
// capture and runs remaining batches as audio-only traversals. Existing DSP and
// verified geometry correction are unchanged.
func (s *Session) AnalyzePreparedAutomatic(ctx context.Context, primary DecodedInput, reference *DecodedInput,
	options api.HDRDecodedOptions, formats api.HDRAnalysisFormats, directory string, otherBytes int64, capture PreparedAudioCapture,
) (*NativeExtraction, []*MeasurementReport, error) {
	if err := validatePreparedAudioCapture(primary, capture); err != nil {
		return nil, nil, err
	}
	if capture.CheckpointNative == nil {
		return nil, nil, errors.New("prepared HDR audio lacks native publication authority")
	}
	prepared := &preparedAudioCapture{PreparedAudioCapture: capture, pending: append([]string(nil), capture.Selected...)}
	defer prepared.reportTraversals(ctx, s.service)
	native, reports, err := s.analyzeAutomatic(ctx, primary, reference, options, formats, directory, otherBytes, prepared, true)
	if err == nil && native != nil && !prepared.checkpointed {
		// Header/backend failures may still yield a complete native-only capture.
		// They do not authorize a second source traversal or fabricated PCM.
		var pictureReports []*MeasurementReport
		for _, report := range reports {
			if report.Result.Mode != api.HDRDecodedAudio {
				pictureReports = append(pictureReports, report)
			}
		}
		reports = pictureReports
		if err = prepared.checkpoint(native, reports); err == nil {
			prepared.current, prepared.pending = prepared.pending, nil
			err = prepared.finish(ctx, primary, options, DecoderIdentity{}, nil, errDecodedMapping, nil, nil)
		}
	}
	return native, reports, err
}

func (p *preparedAudioCapture) reportTraversals(ctx context.Context, service *Service) {
	service.reportProgress(ctx, Progress{Phase: "audio_capture_finished", Message: fmt.Sprintf(
		"Audio capture finished: %d additional audio-only batch traversals and %d corrective traversals started; inspect per-track outcomes for completion.",
		p.traversals, p.corrections)})
}

func validatePreparedAudioCapture(input DecodedInput, capture PreparedAudioCapture) error {
	if capture.Publish == nil || len(capture.Selected) > 64 || capture.Subject.VideoPath != input.Path ||
		capture.Subject.SourceFingerprint != input.Fingerprint ||
		len(capture.Selected) > 0 && (capture.Subject.ResourceID == "" || capture.Subject.ManifestFingerprint == "") {
		return errors.New("prepared HDR audio lacks source or publication authority")
	}
	lastOrdinal := 0
	for _, id := range capture.Selected {
		found := false
		for _, track := range capture.Subject.Tracks {
			if track.Kind == api.MediaTrackAudio && track.ID == id && track.ResourceID == capture.Subject.ResourceID && track.Ordinal > lastOrdinal {
				lastOrdinal, found = track.Ordinal, true
				break
			}
		}
		if !found {
			return errors.New("prepared HDR audio selection is not in exact source order")
		}
	}
	return nil
}

func (p *preparedAudioCapture) checkpoint(native *NativeExtraction, reports []*MeasurementReport) error {
	if err := p.CheckpointNative(native, reports); err != nil {
		return fmt.Errorf("capture prepared audio: %w", err)
	}
	p.checkpointed = true
	return nil
}

func (p *preparedAudioCapture) prepare(ctx context.Context, service *audioanalysis.Service, directory string) (*audioanalysis.Batch, error) {
	if len(p.pending) == 0 {
		p.current = nil
		return nil, nil
	}
	count := min(4, len(p.pending))
	for {
		p.current = p.pending[:count]
		batch, err := service.PrepareSelectedBatch(ctx, p.Subject, p.current, directory, "prepared-hdr")
		if errors.Is(err, audioanalysis.ErrBatchMemory) && count > 1 {
			count--
			continue
		}
		p.pending = p.pending[count:]
		if err != nil {
			return batch, fmt.Errorf("prepare audio batch: %w", err)
		}
		return batch, nil
	}
}

func (p *preparedAudioCapture) publish(input DecodedInput, options api.HDRDecodedOptions, decoder DecoderIdentity, track audioanalysis.TrackResult) error {
	if track.CorrectiveDecode {
		p.corrections++
	}
	authority := HDRAudioAuthority{
		SourceFingerprint:   p.Subject.SourceFingerprint,
		ResourceID:          p.Subject.ResourceID,
		ManifestFingerprint: p.Subject.ManifestFingerprint,
		TrackID:             track.Public.TrackID,
		Ordinal:             track.Public.Ordinal,
		StreamOrdinal:       track.Public.Ordinal - 1,
		Codec:               track.Public.Codec,
		ChannelLayout:       track.Public.ChannelLayout,
		SampleRate:          track.Public.SampleRate,
		Channels:            track.Public.Channels,
		DRCDisabled:         audioanalysis.DecoderDRCDisabled(track.Public.Codec),
	}
	report, err := preparedAudioReport(input, options, authority, decoder, track)
	if err != nil {
		return fmt.Errorf("capture prepared audio: %w", err)
	}
	report.Processing = []string{
		fmt.Sprintf("prepared audio; audio-only batch traversals started=%d; corrective traversals started=%d; complete native-rate PCM and existing DSP",
			p.traversals, p.corrections),
	}
	return p.Publish(report)
}

func (p *preparedAudioCapture) finish(ctx context.Context, input DecodedInput, options api.HDRDecodedOptions, decoder DecoderIdentity,
	batch *audioanalysis.Batch, prepareErr error, outputs []error, processErr error,
) error {
	if prepareErr != nil {
		failure, ok := api.AsAudioAnalysisFailure(prepareErr)
		if !ok {
			failure = api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureDecode, Message: "The selected audio track could not be measured."}
		}
		for _, id := range p.current {
			for _, track := range p.Subject.Tracks {
				if track.ID != id {
					continue
				}
				result := audioanalysis.TrackResult{Public: api.AudioAnalysisTrackResult{
					TrackID:       id,
					Ordinal:       track.Ordinal,
					Codec:         track.Codec,
					ChannelLayout: track.ChannelLayout,
					Channels:      track.Channels,
					SampleRate:    track.SampleRate,
					Language:      strings.Join(track.Languages, ", "),
					Failure:       &failure,
					Status:        api.StageStatusFailed,
				}}
				if err := p.publish(input, options, decoder, result); err != nil {
					return fmt.Errorf("capture prepared audio: %w", err)
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("finish prepared audio: %w", err)
		}
		return nil
	}
	if batch == nil {
		return nil
	}
	if err := batch.FinishEach(
		ctx,
		outputs,
		processErr,
		func(track audioanalysis.TrackResult) error { return p.publish(input, options, decoder, track) },
	); err != nil {
		return fmt.Errorf("finish audio batch: %w", err)
	}
	return nil
}

func (p *preparedAudioCapture) remaining(ctx context.Context, service *audioanalysis.Service, directory string,
	input DecodedInput, options api.HDRDecodedOptions, decoder DecoderIdentity,
) error {
	for len(p.pending) > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("capture prepared audio: %w", err)
		}
		batch, err := p.prepare(ctx, service, directory)
		if err != nil {
			if err := p.finish(ctx, input, options, decoder, nil, err, nil, nil); err != nil {
				return fmt.Errorf("capture prepared audio: %w", err)
			}
			continue
		}
		if len(batch.Outputs(ctx)) > 0 {
			p.traversals++
		}
		err = batch.DecodeEach(ctx, func(track audioanalysis.TrackResult) error { return p.publish(input, options, decoder, track) })
		batch.Close()
		if err != nil {
			return fmt.Errorf("capture prepared audio: %w", err)
		}
	}
	return nil
}
