// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// AppendAudioOutcome folds one prepared track's independently rendered variants
// into the target's single ordered audio outcome. The caller supplies a report
// with validated AudioAuthority and publishes each selected track exactly once.
func AppendAudioOutcome(outcome *api.HDRAnalysisTargetResult, report *MeasurementReport, rendered api.HDRDecodedResult) {
	authority := report.AudioAuthority
	track := api.HDRAudioTrackResult{
		TrackID:       authority.TrackID,
		Ordinal:       authority.Ordinal,
		Codec:         authority.Codec,
		ChannelLayout: authority.ChannelLayout,
		SampleRate:    authority.SampleRate,
		Channels:      authority.Channels,
		State:         rendered.State,
		PCMMeasured:   report.Result.Measured == 1,
		Artifacts:     rendered.Artifacts,
	}
	if report.Audio != nil {
		track.Language, track.SampleFrames, track.Failure = report.Audio.Track.Language, report.Audio.Track.SampleFrames, report.Audio.Track.Failure
	}
	if track.PCMMeasured && (len(track.Artifacts) != 3 || track.Failure != nil || track.State != api.HDRDecodedComplete) {
		track.State = api.HDRDecodedInconclusive
	}
	position := slices.IndexFunc(outcome.Decoded, func(result api.HDRDecodedResult) bool { return result.Mode == api.HDRDecodedAudio })
	if position < 0 {
		outcome.Decoded = append(outcome.Decoded, api.HDRDecodedResult{
			Mode:        api.HDRDecodedAudio,
			Profile:     api.AudioAnalysisProfileVersion,
			State:       api.HDRDecodedComplete,
			Coverage:    api.HDRCoverageComplete,
			AudioTracks: []api.HDRAudioTrackResult{},
		})
		position = len(outcome.Decoded) - 1
	}
	aggregate := &outcome.Decoded[position]
	terminal := aggregate.State
	trackTerminal := rendered.State
	if report.Audio != nil {
		if report.Audio.Track.Status == api.StageStatusCanceled {
			trackTerminal = api.HDRDecodedCanceled
		}
		if report.Audio.Track.Status == api.StageStatusInterrupted {
			trackTerminal = api.HDRDecodedInterrupted
		}
	}
	if trackTerminal == api.HDRDecodedCanceled || trackTerminal == api.HDRDecodedInterrupted && terminal != api.HDRDecodedCanceled {
		terminal = trackTerminal
	}
	aggregate.AudioTracks = append(aggregate.AudioTracks, track)
	slices.SortFunc(aggregate.AudioTracks, func(a, b api.HDRAudioTrackResult) int { return a.Ordinal - b.Ordinal })
	aggregate.Selected++
	if track.PCMMeasured {
		aggregate.Measured++
	}
	aggregate.Artifacts = append(aggregate.Artifacts, track.Artifacts...)
	if track.State != api.HDRDecodedComplete {
		aggregate.State, aggregate.Coverage = api.HDRDecodedInconclusive, api.HDRCoveragePartial
	}
	if aggregate.Measured == 0 {
		aggregate.State = api.HDRDecodedFailed
		if !slices.ContainsFunc(aggregate.AudioTracks, func(track api.HDRAudioTrackResult) bool { return track.State != api.HDRDecodedUnsupported }) {
			aggregate.State = api.HDRDecodedUnsupported
		}
	} else if aggregate.Coverage == api.HDRCoveragePartial {
		aggregate.State = api.HDRDecodedInconclusive
	}
	if terminal == api.HDRDecodedCanceled || terminal == api.HDRDecodedInterrupted {
		aggregate.State, aggregate.Coverage, aggregate.Reason = terminal, api.HDRCoveragePartial, string(terminal)
	}
}

// CompletePreparedAudio records terminal outcomes for selected tracks interrupted before publication.
func CompletePreparedAudio(capture *PreparedAudioCapture, outcome *api.HDRAnalysisTargetResult, canceled bool) {
	if capture == nil {
		return
	}
	for _, id := range capture.Selected {
		alreadyPublished := slices.ContainsFunc(outcome.Decoded, func(result api.HDRDecodedResult) bool {
			return result.Mode == api.HDRDecodedAudio &&
				slices.ContainsFunc(result.AudioTracks, func(track api.HDRAudioTrackResult) bool { return track.TrackID == id })
		})
		if alreadyPublished {
			continue
		}
		for _, track := range capture.Subject.Tracks {
			if track.ID != id {
				continue
			}
			authority := HDRAudioAuthority{
				TrackID:       track.ID,
				Ordinal:       track.Ordinal,
				Codec:         track.Codec,
				ChannelLayout: track.ChannelLayout,
				Channels:      track.Channels,
				SampleRate:    track.SampleRate,
			}
			result := api.HDRDecodedResult{State: api.HDRDecodedFailed, Reason: "audio_analysis_failed"}
			failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureDecode, Message: "Audio measurement did not complete."}
			if canceled {
				result.State, result.Reason = api.HDRDecodedCanceled, "canceled"
				failure = api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureCanceled, Message: "Audio measurement was canceled."}
			}
			AppendAudioOutcome(outcome, &MeasurementReport{AudioAuthority: &authority,
				Audio: &AutomaticAudio{Track: api.AudioAnalysisTrackResult{Language: strings.Join(track.Languages, ", "), Failure: &failure}}}, result)
		}
	}
}
