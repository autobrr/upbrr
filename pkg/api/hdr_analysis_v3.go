// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"
)

const HDRAnalysisProfileV3 = "hdr-analysis-v3"

// RequiresV3 reports whether the request needs the track-aware bounded profile.
func (o HDRDecodedOptions) RequiresV3() bool {
	return o.BoundarySync || o.AllAudioTracks || o.VideoDecoderThreads != 0
}

// IsNativeHDRProfile identifies profiles with independent native format outcomes.
func IsNativeHDRProfile(profile string) bool {
	return profile == HDRAnalysisProfileV2 || profile == HDRAnalysisProfileV3
}

// MatchesFilename accepts the fixed artifact name or, for audio, a hashed track name.
func (kind HDRArtifactKind) MatchesFilename(name string) bool {
	plain := (HDRTypedArtifact{Kind: kind}).Filename()
	if name == plain && plain != "" {
		return true
	}
	if kind != HDRArtifactAudioStats && kind != HDRArtifactAudioWaveform && kind != HDRArtifactAudioSpectrogram {
		return false
	}
	if len(name) != len("track-")+64+1+len(plain) || !strings.HasPrefix(name, "track-") || !strings.HasSuffix(name, "-"+plain) {
		return false
	}
	_, err := hex.DecodeString(name[len("track-") : len("track-")+64])
	return err == nil
}

// HDRDecodedModes returns the public terminal-outcome order.
func HDRDecodedModes() []HDRDecodedMode {
	return []HDRDecodedMode{
		HDRDecodedAutomatic,
		HDRDecodedBoundarySync,
		HDRDecodedAudio,
		HDRDecodedSceneSync,
		HDRDecodedActiveArea,
		HDRDecodedGrade,
		HDRDecodedColour,
	}
}

// HDRAudioTrackResult distinguishes complete PCM measurement from artifact publication.
// Artifacts contains only successful variants belonging to this exact prepared track.
type HDRAudioTrackResult struct {
	TrackID       string                `json:"trackId"`
	Ordinal       int                   `json:"ordinal"`
	Language      string                `json:"language,omitempty"`
	Codec         string                `json:"codec"`
	ChannelLayout string                `json:"channelLayout"`
	State         HDRDecodedState       `json:"state"`
	PCMMeasured   bool                  `json:"pcmMeasured"`
	Channels      int                   `json:"channels"`
	SampleRate    int                   `json:"sampleRate"`
	SampleFrames  int64                 `json:"sampleFrames"`
	Artifacts     []PublicResourceID    `json:"artifacts"`
	Failure       *AudioAnalysisFailure `json:"failure,omitempty"`
}

func (r HDRDecodedResult) validateV3() error {
	if !slices.Contains([]HDRCoverage{HDRCoverageComplete, HDRCoveragePartial, HDRCoverageUnknown}, r.Coverage) {
		return errors.New("v3 decoded outcome requires execution coverage")
	}
	legacy := r
	legacy.Coverage, legacy.AudioTracks = "", nil
	if r.Mode == HDRDecodedBoundarySync {
		if r.Selected > 216 || r.Profile != "hdr-boundary-pq16-v1" && r.Profile != "hdr-boundary-pq16-colour-v2" {
			return errors.New("invalid bounded boundary outcome")
		}
		legacy.Mode = HDRDecodedSceneSync
	}
	if r.Mode == HDRDecodedAudio {
		if r.Selected > 64 || len(r.AudioTracks) > 64 || len(r.Artifacts) > 192 {
			return errors.New("audio outcome exceeds track bounds")
		}
		legacy.Artifacts = nil
	} else if r.AudioTracks != nil {
		return errors.New("non-audio outcome contains audio tracks")
	}
	if err := legacy.Validate(); err != nil {
		return err
	}
	if r.State == HDRDecodedComplete && r.Coverage != HDRCoverageComplete {
		return errors.New("complete outcome has incomplete execution coverage")
	}
	if r.Coverage == HDRCoverageComplete &&
		(r.Selected == 0 || r.Measured != r.Selected || r.State != HDRDecodedComplete && r.State != HDRDecodedInconclusive) {
		return errors.New("complete execution coverage lacks completed selected work")
	}
	seenArtifacts := make(map[PublicResourceID]bool)
	for _, id := range r.Artifacts {
		if id == "" || seenArtifacts[id] {
			return errors.New("duplicate or empty decoded artifact")
		}
		seenArtifacts[id] = true
	}
	if r.AudioTracks == nil {
		return nil
	} // Private per-track reports precede public aggregation.
	if len(r.AudioTracks) != r.Selected {
		return errors.New("audio selection differs from track outcomes")
	}
	seenTracks := make(map[string]bool)
	owned := make(map[PublicResourceID]bool)
	measured, previousOrdinal, complete := 0, 0, true
	for _, track := range r.AudioTracks {
		if strings.TrimSpace(track.TrackID) == "" || len(track.TrackID) > 128 || seenTracks[track.TrackID] ||
			track.Ordinal <= previousOrdinal || len(track.Language) > 128 || len(track.Codec) > 128 || len(track.ChannelLayout) > 128 || len(track.Artifacts) > 3 {
			return errors.New("invalid ordered audio track authority")
		}
		seenTracks[track.TrackID], previousOrdinal = true, track.Ordinal
		if !slices.Contains(
			[]HDRDecodedState{
				HDRDecodedComplete,
				HDRDecodedInconclusive,
				HDRDecodedFailed,
				HDRDecodedUnsupported,
				HDRDecodedNotRun,
				HDRDecodedCanceled,
				HDRDecodedInterrupted,
			},
			track.State,
		) {
			return errors.New("audio track is not terminal")
		}
		if track.Failure != nil {
			if err := validateAudioAnalysisFailure(*track.Failure); err != nil {
				return err
			}
		}
		if track.PCMMeasured {
			if track.Channels < 1 || track.Channels > 8 || track.SampleRate < 1 || track.SampleRate > 768000 || track.SampleFrames <= 0 ||
				track.SampleFrames > int64(track.SampleRate)*24*60*60 || track.State != HDRDecodedComplete && track.State != HDRDecodedInconclusive {
				return errors.New("audio PCM completion lacks valid sample authority")
			}
			measured++
		} else if len(track.Artifacts) != 0 || track.SampleFrames != 0 || track.State == HDRDecodedComplete {
			return errors.New("unmeasured audio track owns measured output")
		}
		trackComplete := track.PCMMeasured && track.State == HDRDecodedComplete && len(track.Artifacts) == 3 && track.Failure == nil
		if track.State == HDRDecodedComplete && !trackComplete {
			return errors.New("completed audio track lacks every variant")
		}
		complete = complete && trackComplete
		for _, id := range track.Artifacts {
			if !seenArtifacts[id] || owned[id] {
				return errors.New("audio artifact has ambiguous track ownership")
			}
			owned[id] = true
		}
	}
	if measured != r.Measured || len(owned) != len(r.Artifacts) || r.Coverage == HDRCoverageComplete && !complete {
		return errors.New("audio aggregate disagrees with track completion")
	}
	return nil
}

func (a HDRTypedArtifact) validateV3(formats []HDRFormatResult) error {
	if a.ID == "" || len(a.MeasurementFingerprint) != 64 || len(a.RenderFingerprint) != 64 {
		return errors.New("v3 artifact lacks measurement or rendering identity")
	}
	for _, fingerprint := range []string{a.MeasurementFingerprint, a.RenderFingerprint} {
		if _, err := hex.DecodeString(fingerprint); err != nil {
			return errors.New("v3 artifact fingerprint is not a SHA-256 digest")
		}
	}
	if a.Kind.DecodedMode() == HDRDecodedAudio {
		if strings.TrimSpace(a.AudioTrackID) == "" || len(a.AudioTrackID) > 128 {
			return errors.New("audio artifact lacks track identity")
		}
	} else if a.AudioTrackID != "" {
		return errors.New("non-audio artifact contains track identity")
	}
	if a.Kind.DecodedMode() == HDRDecodedBoundarySync {
		format := HDRNativeDolbyVision
		if a.Kind == HDRArtifactBoundaryHDR10PlusSceneSheet {
			format = HDRNativeHDR10Plus
		}
		if a.Format != format || a.MIME != "image/png" || a.Width != 1800 || a.Height != 900 {
			return errors.New("invalid boundary sheet")
		}
		for _, result := range formats {
			// Auto selection completes native extraction without setting Requested.
			if result.Format == format && result.State == HDRExtractionComplete && result.Validate() == nil {
				return nil
			}
		}
		return errors.New("boundary sheet lacks matching native format")
	}
	a.Profile, a.AudioTrackID, a.MeasurementFingerprint, a.RenderFingerprint = HDRAnalysisProfileV2, "", "", ""
	return a.Validate(formats)
}

func (r HDRAnalysisResult) validateV3Result() error {
	artifacts := make(map[PublicResourceID]bool)
	for i, target := range r.Targets {
		if target.TargetID != r.TargetIDs[i] || target.Artifact != nil || target.Failure != nil || target.Frames != 0 || target.Scenes != 0 ||
			target.Profile != "" ||
			len(target.Decoded) > 7 ||
			len(target.Artifacts) > 216 ||
			target.Status != target.AnalysisStatus() {
			return errors.New("invalid v3 target shape or status")
		}
		if err := r.Formats.ValidateResults(target.Formats); err != nil {
			return err
		}
		for _, format := range target.Formats {
			if err := format.Validate(); err != nil {
				return err
			}
		}
		seenModes := make(map[HDRDecodedMode]bool)
		artifactOwners := make(map[PublicResourceID]HDRDecodedMode)
		trackOwners := make(map[PublicResourceID]string)
		lastMode := -1
		for _, decoded := range target.Decoded {
			order := slices.Index(HDRDecodedModes(), decoded.Mode)
			if r.Decoded == nil || !r.Decoded.Requested(decoded.Mode) || order <= lastMode || seenModes[decoded.Mode] || decoded.Coverage == "" {
				return errors.New("v3 modes differ from ordered request")
			}
			lastMode, seenModes[decoded.Mode] = order, true
			if err := decoded.validateV3(); err != nil {
				return err
			}
			if decoded.Mode == HDRDecodedAudio && len(decoded.AudioTracks) != decoded.Selected {
				return errors.New("public audio outcome lacks track summaries")
			}
			for _, id := range decoded.Artifacts {
				if artifactOwners[id] != "" || decoded.Measured == 0 || decoded.State != HDRDecodedComplete && decoded.State != HDRDecodedInconclusive {
					return errors.New("artifact lacks successful measurement authority")
				}
				artifactOwners[id] = decoded.Mode
			}
			for _, track := range decoded.AudioTracks {
				for _, id := range track.Artifacts {
					trackOwners[id] = track.TrackID
				}
			}
		}
		for _, mode := range HDRDecodedModes() {
			if r.Decoded != nil && r.Decoded.Requested(mode) != seenModes[mode] {
				return errors.New("requested v3 mode has no terminal outcome")
			}
		}
		kinds := make(map[string]bool)
		audioCount, otherCount := 0, 0
		for _, artifact := range target.Artifacts {
			key := string(artifact.Kind) + "\x00" + string(artifact.Format) + "\x00" + artifact.AudioTrackID
			if artifacts[artifact.ID] || kinds[key] || artifact.Profile != HDRAnalysisProfileV3 {
				return errors.New("duplicate or incompatible v3 artifact")
			}
			if err := artifact.Validate(target.Formats); err != nil {
				return err
			}
			artifacts[artifact.ID], kinds[key] = true, true
			if mode := artifact.Kind.DecodedMode(); mode != "" {
				if artifactOwners[artifact.ID] != mode {
					return errors.New("v3 artifact lacks owning outcome")
				}
				delete(artifactOwners, artifact.ID)
			}
			if artifact.Kind.DecodedMode() == HDRDecodedAudio {
				audioCount++
				if trackOwners[artifact.ID] != artifact.AudioTrackID {
					return errors.New("audio artifact belongs to another track")
				}
			} else {
				otherCount++
			}
		}
		if len(artifactOwners) != 0 || audioCount > 192 || otherCount > 24 {
			return errors.New("v3 artifact count or reference mismatch")
		}
	}
	if r.Status != NativeHDRAggregate(r.Targets) {
		return errors.New("v3 aggregate disagrees with targets")
	}
	return nil
}
