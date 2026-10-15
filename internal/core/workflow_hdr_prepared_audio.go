// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func preparedHDRAudioAuthority(subject api.AudioAnalysisSubject, track api.MediaTrackFacts) hdranalysis.HDRAudioAuthority {
	return hdranalysis.HDRAudioAuthority{
		SourceFingerprint:   subject.SourceFingerprint,
		ResourceID:          subject.ResourceID,
		ManifestFingerprint: subject.ManifestFingerprint,
		TrackID:             track.ID,
		Ordinal:             track.Ordinal,
		StreamOrdinal:       track.Ordinal - 1,
		Codec:               track.Codec,
		ChannelLayout:       track.ChannelLayout,
		Channels:            track.Channels,
		SampleRate:          track.SampleRate,
		DRCDisabled:         audioanalysis.DecoderDRCDisabled(track.Codec),
	}
}

func (b workflowHDRAnalysisBuilder) prepareHDRAudioCapture(ctx context.Context, session *hdranalysis.Session,
	subject api.HDRAnalysisSubject, target api.HDRAnalysisTargetSubject, instructions api.HDRAnalysisInstructions,
	native *releaseworkflow.HDRExtractionRecord, store *releaseworkflow.HDRMeasurementStore,
	outcome *api.HDRAnalysisTargetResult, resource hdrManagedResource, attempt string, index int, renderFingerprint string,
) (*hdranalysis.PreparedAudioCapture, error) {
	if instructions.ProfileVersion != api.HDRAnalysisProfileV3 || instructions.Decoded == nil || !instructions.Decoded.Requested(api.HDRDecodedAudio) {
		return nil, nil
	}
	terminal := func(state api.HDRDecodedState, reason string) (*hdranalysis.PreparedAudioCapture, error) {
		coverage := api.HDRCoverageUnknown
		if state == api.HDRDecodedNotRun {
			coverage = api.HDRCoverageComplete
		}
		outcome.Decoded = append(outcome.Decoded, api.HDRDecodedResult{
			Mode:     api.HDRDecodedAudio,
			Profile:  api.AudioAnalysisProfileVersion,
			State:    state,
			Coverage: coverage,
			Reason:   reason,
		})
		return &hdranalysis.PreparedAudioCapture{Subject: api.AudioAnalysisSubject{VideoPath: target.Path, SourceFingerprint: subject.SourceFingerprint},
			Publish: func(*hdranalysis.MeasurementReport) error {
				return errors.New("empty audio scope cannot publish a track")
			}}, nil
	}
	if target.Audio == nil || target.Audio.SourceFingerprint != subject.SourceFingerprint || target.Audio.VideoPath != target.Path {
		return terminal(api.HDRDecodedUnsupported, "prepared_audio_scope_unavailable")
	}
	selected := target.Audio.Tracks
	if len(selected) == 0 {
		return terminal(api.HDRDecodedNotRun, "no_audio")
	}
	if !instructions.Decoded.AllAudioTracks {
		position := slices.IndexFunc(selected, func(track api.MediaTrackFacts) bool { return track.ID == target.Audio.PrimaryTrackID })
		if position < 0 {
			return terminal(api.HDRDecodedUnsupported, "prepared_primary_audio_unavailable")
		}
		selected = selected[position : position+1]
	}
	if len(selected) > 64 {
		capture, err := terminal(api.HDRDecodedUnsupported, "audio_track_limit")
		outcome.Decoded[len(outcome.Decoded)-1].Checks = []api.HDRCheckResult{{
			Code:    "audio_track_limit",
			State:   api.HDRCheckUnsupported,
			Message: fmt.Sprintf("The request selected %d audio tracks; the supported maximum is 64. No subset was analyzed.", len(selected)),
		}}
		return capture, err
	}
	capture := &hdranalysis.PreparedAudioCapture{Subject: *target.Audio}
	render := func(report *hdranalysis.MeasurementReport) {
		renderHDRMeasurements(
			ctx,
			session,
			subject.Title,
			attempt,
			index,
			[]*hdranalysis.MeasurementReport{report},
			outcome,
			resource,
			*native,
			renderFingerprint,
		)
	}
	capture.Publish = func(report *hdranalysis.MeasurementReport) error {
		if store == nil || store.Publish == nil || native.ID == "" || report.AudioAuthority == nil {
			return errors.New("prepared audio lacks native checkpoint authority")
		}
		identity, err := hdranalysis.AudioMeasurementIdentity(report)
		if err != nil {
			return fmt.Errorf("identify prepared audio: %w", err)
		}
		id := attempt + "-" + strconv.Itoa(index+1) + "-audio-track-" + strconv.Itoa(report.AudioAuthority.Ordinal)
		record, owned, err := b.retainMeasurement(ctx, *native, identity, id, report)
		if err != nil {
			return err
		}
		if err := store.Publish(record, owned); err != nil {
			return fmt.Errorf("publish prepared HDR audio: %w", err)
		}
		render(report)
		return nil
	}
	var decoder string
	var decoderErr error
	decoderChecked := false
	for _, track := range selected {
		authority := preparedHDRAudioAuthority(*target.Audio, track)
		scope, err := hdrAudioScopeFingerprint(authority)
		if err != nil {
			return nil, err
		}
		var previous releaseworkflow.HDRMeasurementRecord
		if store != nil {
			for _, record := range store.Records {
				if record.Release == instructions.Release && record.TargetID == target.Target.ID && record.AudioScopeFingerprint == scope &&
					(record.Sequence > previous.Sequence || record.Sequence == previous.Sequence && record.ID > previous.ID) {
					previous = record
				}
			}
		}
		retry := instructions.DecodedRetry != nil && slices.Contains(instructions.DecodedRetry.Modes, api.HDRDecodedAudio)
		if retry && store != nil && store.RetryAfterSequence != nil && previous.Sequence > *store.RetryAfterSequence {
			retry = false
		}
		if previous.ID == "" || retry && previous.Result.State != api.HDRDecodedComplete {
			capture.Selected = append(capture.Selected, track.ID)
			continue
		}
		if previous.Decoder != "" && !decoderChecked {
			decoder, decoderErr = hdranalysis.CurrentDecoderFingerprint(ctx)
			decoderChecked = true
		}
		if previous.Decoder != "" && decoderErr == nil && decoder != previous.Decoder {
			capture.Selected = append(capture.Selected, track.ID)
			continue
		}
		report, restoreErr := restoreHDRMeasurement(ctx, previous, store.Resources[previous.ID])
		if restoreErr == nil && previous.Decoder != "" {
			restoreErr = decoderErr
		}
		if restoreErr == nil && (report.AudioAuthority == nil || report.AudioAuthority.TrackID != track.ID ||
			track.SampleRate > 0 && report.AudioAuthority.SampleRate != track.SampleRate || track.Channels > 0 && report.AudioAuthority.Channels != track.Channels ||
			track.Codec != "" && report.AudioAuthority.Codec != track.Codec || track.ChannelLayout != "" && report.AudioAuthority.ChannelLayout != track.ChannelLayout) {
			restoreErr = releaseworkflow.ErrPrivateResourceIntegrity
		}
		if restoreErr != nil && retry {
			capture.Selected = append(capture.Selected, track.ID)
			continue
		}
		if restoreErr != nil {
			report = &hdranalysis.MeasurementReport{AudioAuthority: &authority, Result: api.HDRDecodedResult{
				Mode:     api.HDRDecodedAudio,
				Profile:  api.AudioAnalysisProfileVersion,
				State:    api.HDRDecodedFailed,
				Selected: 1,
				Coverage: api.HDRCoverageUnknown,
				Reason:   "resource_unavailable",
			}}
		}
		render(report)
	}
	return capture, nil
}
