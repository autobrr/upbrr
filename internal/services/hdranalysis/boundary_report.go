// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	MaxBoundaryMeasurementBytes   = 16 << 20
	MaxTrackAudioMeasurementBytes = 96 << 20
)

// HDRAudioAuthority binds a private report to the original prepared inventory.
// StreamOrdinal is an audio-stream ordinal verified against that inventory.
type HDRAudioAuthority struct {
	SourceFingerprint   string `json:"sourceFingerprint"`
	ResourceID          string `json:"resourceId"`
	ManifestFingerprint string `json:"manifestFingerprint"`
	TrackID             string `json:"trackId"`
	StreamOrdinal       int    `json:"streamOrdinal"`
	Ordinal             int    `json:"ordinal"`
	Codec               string `json:"codec"`
	ChannelLayout       string `json:"channelLayout"`
	SampleRate          int    `json:"sampleRate"`
	Channels            int    `json:"channels"`
	DRCDisabled         bool   `json:"drcDisabled"`
}

func (a HDRAudioAuthority) Validate() error {
	if a.SourceFingerprint == "" || len(a.SourceFingerprint) > 512 || a.ResourceID == "" || len(a.ResourceID) > 128 ||
		a.ManifestFingerprint == "" || len(a.ManifestFingerprint) > 512 || a.TrackID == "" || len(a.TrackID) > 128 ||
		a.Ordinal < 1 || a.StreamOrdinal != a.Ordinal-1 || len(a.Codec) > 128 || len(a.ChannelLayout) > 128 ||
		a.SampleRate < 0 || a.SampleRate > 768000 || a.Channels < 0 || a.Channels > 64 {
		return errors.New("invalid prepared HDR audio authority")
	}
	return nil
}

func (r *MeasurementReport) validateV2() error {
	if r.Dependency != NativeDependencyFingerprint || len(r.Decoder.Version) > 200 || len(r.Decoder.Fingerprint) > 64 || len(r.Processing) > 3 ||
		r.Result.Coverage == "" || r.Result.AudioTracks != nil {
		return errors.New("invalid v2 measurement envelope")
	}
	if r.Decoder.RequestedThreads < 0 || r.Decoder.RequestedThreads > 64 || len(r.Decoder.EffectiveThreads) > 2 ||
		r.Decoder.RequestedThreads != 0 && r.Decoder.RequestedThreads != r.Options.VideoDecoderThreads ||
		r.Result.Mode == api.HDRDecodedAudio && (r.Decoder.RequestedThreads != 0 || len(r.Decoder.EffectiveThreads) != 0) {
		return errors.New("invalid decoder thread receipt")
	}
	for _, threads := range r.Decoder.EffectiveThreads {
		if threads < 1 || threads > 64 || r.Decoder.RequestedThreads > 0 && threads > r.Decoder.RequestedThreads {
			return errors.New("invalid effective decoder thread count")
		}
	}
	if err := r.Options.Validate(); err != nil {
		return fmt.Errorf("validate boundary report: %w", err)
	}
	if !r.Options.Requested(r.Result.Mode) {
		return errors.New("v2 measurement mode was not requested")
	}
	if err := r.Result.Validate(); err != nil {
		return fmt.Errorf("validate boundary report: %w", err)
	}
	for _, source := range r.Sources {
		if len(source) > 512 {
			return errors.New("v2 source identity exceeds bounds")
		}
	}
	for _, track := range r.Tracks {
		if len(track) > 20 {
			return errors.New("v2 native track identity exceeds bounds")
		}
	}
	for _, processing := range r.Processing {
		if len(processing) > 1024 {
			return errors.New("v2 processing description exceeds bounds")
		}
	}
	if r.Raster[0] < 0 || r.Raster[0] > 8192 || r.Raster[1] < 0 || r.Raster[1] > 4320 {
		return errDecodedMapping
	}
	if r.Result.Mode == api.HDRDecodedBoundarySync {
		return r.validateBoundary()
	}
	if r.Boundary != nil {
		return errors.New("non-boundary measurement contains boundary data")
	}
	if r.Result.Mode == api.HDRDecodedAudio &&
		(r.Automatic != nil || len(r.Selected) != 0 || len(r.Scene)+len(r.Area)+len(r.Grade)+len(r.Thumbnails)+len(r.Chromaticity[0])+len(r.Chromaticity[1]) != 0) {
		return errors.New("per-track audio report contains picture evidence")
	}
	if r.AudioAuthority != nil {
		if r.Result.Mode != api.HDRDecodedAudio || r.Result.Selected != 1 || r.AudioAuthority.SourceFingerprint != r.Sources[0] {
			return errors.New("audio authority differs from measurement")
		}
		if err := r.AudioAuthority.Validate(); err != nil {
			return fmt.Errorf("validate boundary report: %w", err)
		}
		if r.Audio != nil && (r.Audio.Track.TrackID != r.AudioAuthority.TrackID || r.Audio.Track.Ordinal != r.AudioAuthority.Ordinal ||
			r.Audio.Track.Codec != r.AudioAuthority.Codec || r.Audio.Track.ChannelLayout != r.AudioAuthority.ChannelLayout ||
			r.Audio.Track.SampleRate != r.AudioAuthority.SampleRate || r.Audio.Track.Channels != r.AudioAuthority.Channels) {
			return errors.New("retained audio differs from prepared track")
		}
	} else if r.Result.Mode == api.HDRDecodedAudio && r.Result.Measured > 0 {
		return errors.New("measured v2 audio lacks prepared authority")
	}
	legacy := *r
	legacy.Schema = "hdr-measurement-v1"
	legacy.Decoder.RequestedThreads, legacy.Decoder.EffectiveThreads = 0, nil
	legacy.Result.Coverage = ""
	legacy.AudioAuthority = nil
	if r.AudioAuthority != nil && r.Result.Measured == 0 && r.Audio != nil {
		track := r.Audio.Track
		if len(r.Audio.Images) != 0 || r.Audio.Stats != "" || len(track.Artifacts) != 0 || track.SampleFrames != 0 || track.Duration != 0 ||
			len(track.Title) > 1024 {
			return errors.New("unmeasured audio owns measured evidence")
		}
		public := r.Result
		public.AudioTracks = []api.HDRAudioTrackResult{{
			TrackID:       track.TrackID,
			Ordinal:       track.Ordinal,
			Codec:         track.Codec,
			ChannelLayout: track.ChannelLayout,
			Language:      track.Language,
			Channels:      track.Channels,
			SampleRate:    track.SampleRate,
			State:         r.Result.State,
			Failure:       track.Failure,
		}}
		if err := public.Validate(); err != nil {
			return fmt.Errorf("validate unmeasured audio track: %w", err)
		}
		legacy.Audio = nil
	}
	legacy.AreaSamples = nil
	legacy.AreaIntervalsTotal = 0
	legacy.Options.AllAudioTracks, legacy.Options.VideoDecoderThreads = false, 0
	if r.Options.BoundarySync {
		switch r.Result.Mode { //nolint:exhaustive // Only area and audio are boundary siblings.
		case api.HDRDecodedActiveArea:
			if len(r.Area) > 24 || r.AreaIntervalsTotal < len(r.Area) || r.AreaIntervalsTotal > MaxFrames || len(r.AreaSamples) > 120 || len(r.Selected) != 1 ||
				len(r.Selected[0]) > 120 ||
				r.Result.Measured != len(r.AreaSamples) ||
				r.Result.Profile != BoundaryAreaProfile {
				return errors.New("invalid boundary area coverage")
			}
			for i, sample := range r.AreaSamples {
				if !slices.Contains(r.Selected[0], sample.Index) || i > 0 && sample.Index <= r.AreaSamples[i-1].Index || !validBoundaryTimestamp(sample.PTS) {
					return errors.New("invalid area sample identity")
				}
				for _, border := range sample.Borders {
					if border < 0 || border > 8192 {
						return errDecodedMapping
					}
				}
			}
			legacy.Result.Profile = AreaMeasurementProfile
			legacy.Options = api.HDRDecodedOptions{ActiveArea: true}
		case api.HDRDecodedAudio:
			legacy.Options = api.HDRDecodedOptions{Audio: true}
		default:
			return errors.New("invalid boundary sibling mode")
		}
	} else if len(r.AreaSamples) != 0 || r.AreaIntervalsTotal != 0 {
		return errors.New("legacy mode contains boundary area evidence")
	}
	return legacy.Validate()
}

func validBoundaryTimestamp(stamp MeasurementTimestamp) bool {
	if !stamp.Known || len(stamp.Value) > 21 || len(stamp.Timescale) > 20 {
		return false
	}
	_, err := automaticTime(stamp)
	return err == nil
}

func (r *MeasurementReport) validateBoundary() error {
	if r.Boundary == nil || r.Audio != nil || r.AudioAuthority != nil || r.Automatic != nil || len(r.Scene) != 0 || len(r.Area) != 0 ||
		len(r.AreaSamples) != 0 ||
		r.AreaIntervalsTotal != 0 ||
		len(r.Grade) != 0 ||
		len(r.Thumbnails) != 0 ||
		len(r.Selected) != 0 ||
		len(r.Chromaticity[0])+len(r.Chromaticity[1]) != 0 ||
		r.Result.Profile != BoundaryMeasurementProfile {
		return errors.New("boundary measurement has conflicting bodies")
	}
	b := r.Boundary
	if len(b.Formats) > 2 || len(b.SelectedFrameIndices) > boundaryMaxFrames || len(b.Windows) > boundaryMaxWindows || b.ChargedVCL < 0 ||
		b.ChargedVCL > boundaryMaxVCL {
		return errors.New("boundary report exceeds work bounds")
	}
	for i, index := range b.SelectedFrameIndices {
		if index < 0 || index >= MaxFrames || i > 0 && index <= b.SelectedFrameIndices[i-1] {
			return errors.New("invalid boundary frame union")
		}
	}
	selected, measured := 0, 0
	for i, format := range b.Formats {
		if format.Format != api.HDRNativeHDR10Plus && format.Format != api.HDRNativeDolbyVision ||
			i > 0 && (format.Format == b.Formats[i-1].Format || format.Format == api.HDRNativeHDR10Plus) ||
			len(format.NumericCandidates) > 60 ||
			len(format.VisualCandidates) > 48 ||
			len(format.Observations) > 108 ||
			len(format.Thumbnails) > 24 ||
			format.EligibleCandidates < 0 ||
			format.EligibleCandidates > MaxFrames ||
			format.MissingNumericStrata < 0 ||
			format.MissingNumericStrata > 60 ||
			format.MissingVisualStrata < 0 ||
			format.MissingVisualStrata > 12 ||
			len(format.Reason) > 256 ||
			!slices.Contains([]api.HDRCoverage{api.HDRCoverageComplete, api.HDRCoveragePartial, api.HDRCoverageUnknown}, format.Coverage) {
			return errors.New("invalid boundary format bounds")
		}
		for j, index := range format.NumericCandidates {
			if index < 4 || index >= MaxFrames-3 || j > 0 && index <= format.NumericCandidates[j-1] {
				return errors.New("invalid numeric boundary candidates")
			}
		}
		visuals := make(map[int]bool)
		lastStratum, lastRank := -1, -1
		for _, visual := range format.VisualCandidates {
			if visual.Candidate < 4 || visual.Candidate >= MaxFrames-3 || visuals[visual.Candidate] || visual.Stratum < 0 || visual.Stratum > 11 ||
				visual.Rank < 0 ||
				visual.Rank > 3 ||
				visual.Stratum < lastStratum ||
				visual.Stratum == lastStratum && visual.Rank != lastRank+1 ||
				visual.Stratum != lastStratum && visual.Rank != 0 {
				return errors.New("invalid ranked visual candidates")
			}
			visuals[visual.Candidate] = true
			lastStratum, lastRank = visual.Stratum, visual.Rank
		}
		union := boundaryCandidates(format)
		if format.Selected != len(union) || format.Measured != len(format.Observations) ||
			format.Coverage == api.HDRCoverageComplete && format.Measured != format.Selected {
			return errors.New("boundary format coverage differs from evidence")
		}
		for _, candidate := range union {
			for frame := candidate - 4; frame <= candidate+3; frame++ {
				if !slices.Contains(b.SelectedFrameIndices, frame) {
					return errors.New("boundary window absent from frame union")
				}
			}
		}
		observed := make(map[int]bool)
		for j, observation := range format.Observations {
			if !slices.Contains(union, observation.Candidate) || j > 0 && observation.Candidate <= format.Observations[j-1].Candidate ||
				!validBoundaryTimestamp(observation.PTS) {
				return errors.New("invalid boundary observation identity")
			}
			if err := validateSceneObservation(observation, MaxFrames); err != nil {
				return fmt.Errorf("validate boundary report: %w", err)
			}
			proved := slices.ContainsFunc(b.Windows, func(window BoundaryWindow) bool {
				return observation.Candidate-4 >= window.First && observation.Candidate+3 <= window.Last && len(window.Invocations) >= 2 &&
					window.Invocations[1].State == "complete" && slices.ContainsFunc(window.Invocations[1].PTS, func(stamp MeasurementTimestamp) bool {
					a, errA := automaticTime(stamp)
					c, errC := automaticTime(observation.PTS)
					if errA != nil || errC != nil {
						return false
					}
					ms, err := boundaryMilliseconds(c)
					return err == nil && a.rational().Cmp(big.NewRat(ms, 1000)) == 0
				})
			})
			if !proved {
				return errors.New("boundary observation lacks successful window evidence")
			}
			observed[observation.Candidate] = true
		}
		if len(format.Thumbnails)%2 != 0 {
			return errors.New("incomplete boundary image pair")
		}
		pairs := make(map[int]bool)
		strata := make(map[int]bool)
		for j := 0; j < len(format.Thumbnails); j += 2 {
			before, after := format.Thumbnails[j], format.Thumbnails[j+1]
			if !visuals[before.Candidate] || !observed[before.Candidate] || pairs[before.Candidate] || after.Candidate != before.Candidate ||
				before.Index != before.Candidate-1 ||
				after.Index != before.Candidate {
				return errors.New("boundary sheet pair changed metadata identity")
			}
			pairs[before.Candidate] = true
			for _, candidate := range format.VisualCandidates {
				if candidate.Candidate == before.Candidate {
					if strata[candidate.Stratum] || !candidate.Attempted {
						return errors.New("boundary sheet repeats or did not attempt a visual stratum")
					}
					strata[candidate.Stratum] = true
				}
			}
			for _, thumb := range []MeasurementThumbnail{before, after} {
				if thumb.Width != sceneWidth || thumb.Height != sceneHeight || len(thumb.Luma) != sceneWidth*sceneHeight ||
					len(thumb.RGB) != 0 && len(thumb.RGB) != sceneWidth*sceneHeight*3 {
					return errors.New("invalid boundary thumbnail pixels")
				}
				if len(thumb.RGB) > 0 && !slices.ContainsFunc(b.Windows, func(window BoundaryWindow) bool {
					return before.Candidate-4 >= window.First && before.Candidate+3 <= window.Last && len(window.Invocations) == 4 &&
						window.Invocations[3].State == "complete"
				}) {
					return errors.New("boundary colour thumbnail lacks proved replay")
				}
			}
		}
		selected += format.Selected
		measured += format.Measured
	}
	charged := 0
	for i, window := range b.Windows {
		if window.First < 0 || window.Last < window.First || window.Last >= MaxFrames || i > 0 && window.First <= b.Windows[i-1].Last ||
			len(window.Invocations) > 4 ||
			len(window.Reason) > 256 {
			return errors.New("invalid boundary window receipt")
		}
		for index, invocation := range window.Invocations {
			if invocation.State == "complete" || index%2 == 1 {
				digest, err := hex.DecodeString(invocation.ClipSHA256)
				if err != nil || len(digest) != 32 || invocation.CompressedBytes <= 0 || invocation.CompressedBytes > 64<<20 {
					return errors.New("sparse invocation lacks finite clip identity")
				}
			}
			if invocation.VCLUnits < 0 || invocation.VCLUnits > 512 || invocation.NALUnits < 0 || invocation.NALUnits > 4096 ||
				invocation.ExposedPictures < 0 ||
				invocation.ExposedPictures > invocation.VCLUnits ||
				len(invocation.PTS) > 512 ||
				invocation.RequestedThreads < 0 ||
				invocation.RequestedThreads > 64 ||
				invocation.EffectiveThreads < 0 ||
				invocation.EffectiveThreads > 64 ||
				invocation.ReservedBytes < 0 ||
				invocation.ReservedBytes > MaxRestoreBytes ||
				!slices.Contains([]string{"complete", "failed", "canceled", "unsupported"}, invocation.State) {
				return errors.New("invalid sparse invocation bounds")
			}
			if index%2 == 0 && (invocation.VCLUnits != 0 || invocation.ExposedPictures != 0 || len(invocation.PTS) != 0 || invocation.EffectiveThreads != 0) ||
				index%2 == 1 && (window.Invocations[index-1].State != "complete" || invocation.VCLUnits == 0 || invocation.EffectiveThreads == 0 ||
					invocation.ExposedPictures != len(invocation.PTS) || invocation.NALUnits != window.Invocations[index-1].NALUnits ||
					invocation.ClipSHA256 != window.Invocations[index-1].ClipSHA256 || invocation.CompressedBytes != window.Invocations[index-1].CompressedBytes) {
				return errors.New("sparse invocation lacks ordered preparation/decode authority")
			}
			if index >= 2 && window.Invocations[1].State != "complete" || index == 3 && invocation.State == "complete" &&
				!slices.Equal(invocation.PTS, window.Invocations[1].PTS) {
				return errors.New("colour replay differs from proved luma window")
			}
			for j, stamp := range invocation.PTS {
				if !validBoundaryTimestamp(stamp) {
					return errors.New("invalid sparse timestamp receipt")
				}
				if j > 0 {
					current, _ := automaticTime(stamp)
					previous, _ := automaticTime(invocation.PTS[j-1])
					if current.rational().Cmp(previous.rational()) <= 0 {
						return errors.New("sparse timestamps are not in presentation order")
					}
				}
			}
			charged += invocation.VCLUnits
		}
	}
	if charged != b.ChargedVCL || selected != r.Result.Selected || measured != r.Result.Measured {
		return errors.New("boundary work accounting differs from outcome")
	}
	return nil
}
