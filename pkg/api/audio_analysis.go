// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// AudioAnalysisProfileVersion identifies the fixed numerical and raster profile.
const AudioAnalysisProfileVersion = "audio-analysis-v2"

const (
	// AudioAnalysisStatsMaxBytes bounds the retained amplitude report.
	AudioAnalysisStatsMaxBytes = 64 << 10

	// AudioAnalysisDefaultDecoderThreads is used when a request leaves DecoderThreads at zero.
	AudioAnalysisDefaultDecoderThreads = 2
	// AudioAnalysisMaxDecoderThreads is the largest accepted decoder thread request.
	AudioAnalysisMaxDecoderThreads = 16
)

// AudioAnalysisSelectionMode records how an ordered stable-track selection was requested.
type AudioAnalysisSelectionMode string

const (
	AudioAnalysisSelectionPrimary  AudioAnalysisSelectionMode = "primary"
	AudioAnalysisSelectionAll      AudioAnalysisSelectionMode = "all"
	AudioAnalysisSelectionSelected AudioAnalysisSelectionMode = "selected"
)

// AudioAnalysisVariant identifies one retained local analysis output.
type AudioAnalysisVariant string

const (
	AudioAnalysisWaveform    AudioAnalysisVariant = "waveform"
	AudioAnalysisSpectrogram AudioAnalysisVariant = "spectrogram"
	AudioAnalysisStats       AudioAnalysisVariant = "stats"
)

// AudioAnalysisResourceLimits bounds decoder work for one attempt.
type AudioAnalysisResourceLimits struct {
	// DecoderThreads requests FFmpeg decoder threads; zero uses the default.
	DecoderThreads int `json:"decoderThreads,omitempty"`
}

func (l AudioAnalysisResourceLimits) normalize() (AudioAnalysisResourceLimits, error) {
	result := l
	if result.DecoderThreads == 0 {
		result.DecoderThreads = AudioAnalysisDefaultDecoderThreads
	}
	if result.DecoderThreads < 1 || result.DecoderThreads > AudioAnalysisMaxDecoderThreads {
		return AudioAnalysisResourceLimits{}, fmt.Errorf("audio analysis decoder threads must be between 1 and %d", AudioAnalysisMaxDecoderThreads)
	}
	return result, nil
}

// AudioAnalysisFailureCode is a stable track- or variant-level failure classification.
type AudioAnalysisFailureCode string

const (
	AudioAnalysisFailureInvalidSelection    AudioAnalysisFailureCode = "invalid_selection"
	AudioAnalysisFailureAmbiguousBinding    AudioAnalysisFailureCode = "ambiguous_binding"
	AudioAnalysisFailureStaleSource         AudioAnalysisFailureCode = "stale_source"
	AudioAnalysisFailureNoAudio             AudioAnalysisFailureCode = "no_audio"
	AudioAnalysisFailureUnsupportedSource   AudioAnalysisFailureCode = "unsupported_source"
	AudioAnalysisFailureUnsupportedLayout   AudioAnalysisFailureCode = "unsupported_layout"
	AudioAnalysisFailureUnsupportedCodec    AudioAnalysisFailureCode = "unsupported_codec"
	AudioAnalysisFailureFFmpegUnavailable   AudioAnalysisFailureCode = "ffmpeg_unavailable"
	AudioAnalysisFailureDecode              AudioAnalysisFailureCode = "decode_failed"
	AudioAnalysisFailureMalformedPCM        AudioAnalysisFailureCode = "malformed_pcm"
	AudioAnalysisFailureOutput              AudioAnalysisFailureCode = "output_failed"
	AudioAnalysisFailureResourceUnavailable AudioAnalysisFailureCode = "resource_unavailable"
	AudioAnalysisFailureInterrupted         AudioAnalysisFailureCode = "interrupted"
	AudioAnalysisFailureCanceled            AudioAnalysisFailureCode = "canceled"
)

// AudioAnalysisFailure is safe for persisted workflow state and browser transport.
type AudioAnalysisFailure struct {
	Code    AudioAnalysisFailureCode `json:"code"`
	Message string                   `json:"message"`
}

// AudioAnalysisError carries one stable, frontend-safe audio-analysis failure
// while retaining its private cause for in-process classification.
type AudioAnalysisError struct {
	Failure AudioAnalysisFailure
	cause   error
}

// NewAudioAnalysisError returns a typed audio-analysis error when failure is
// valid. Invalid failure values are converted to a generic resource failure.
func NewAudioAnalysisError(failure AudioAnalysisFailure, cause error) error {
	if err := validateAudioAnalysisFailure(failure); err != nil {
		failure = AudioAnalysisFailure{
			Code:    AudioAnalysisFailureResourceUnavailable,
			Message: "audio analysis resources are unavailable",
		}
	}
	return &AudioAnalysisError{Failure: failure, cause: cause}
}

func (e *AudioAnalysisError) Error() string {
	if e == nil {
		return ""
	}
	return e.Failure.Message
}

// Unwrap preserves the private cause for errors.Is and errors.As checks.
func (e *AudioAnalysisError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// AsAudioAnalysisFailure extracts a stable audio-analysis failure from err.
func AsAudioAnalysisFailure(err error) (AudioAnalysisFailure, bool) {
	var typed *AudioAnalysisError
	if !errors.As(err, &typed) || typed == nil || validateAudioAnalysisFailure(typed.Failure) != nil {
		return AudioAnalysisFailure{}, false
	}
	return typed.Failure, true
}

// AudioAnalysisInstructions bind one exact prepared resource and ordered track selection.
type AudioAnalysisInstructions struct {
	Release        ReleaseRef                  `json:"release"`
	ResourceID     string                      `json:"resourceId"`
	Selection      AudioAnalysisSelectionMode  `json:"selection"`
	TrackIDs       []string                    `json:"trackIds"`
	Variants       []AudioAnalysisVariant      `json:"variants"`
	ProfileVersion string                      `json:"profileVersion"`
	ResourceLimits AudioAnalysisResourceLimits `json:"resourceLimits,omitempty"`
}

// Normalize validates and returns detached deterministic instructions.
func (i AudioAnalysisInstructions) Normalize() (AudioAnalysisInstructions, error) {
	result := i
	result.ResourceID = strings.TrimSpace(result.ResourceID)
	result.ProfileVersion = strings.TrimSpace(result.ProfileVersion)
	if result.ProfileVersion == "" {
		result.ProfileVersion = AudioAnalysisProfileVersion
	}
	limits, err := result.ResourceLimits.normalize()
	if err != nil {
		return AudioAnalysisInstructions{}, err
	}
	result.ResourceLimits = limits
	if result.Release.Generation == 0 || strings.TrimSpace(result.Release.SourcePath) == "" {
		return AudioAnalysisInstructions{}, errors.New("audio analysis release reference is required")
	}
	if result.ResourceID == "" {
		return AudioAnalysisInstructions{}, errors.New("audio analysis resource ID is required")
	}
	switch result.Selection {
	case AudioAnalysisSelectionPrimary, AudioAnalysisSelectionAll, AudioAnalysisSelectionSelected:
	default:
		return AudioAnalysisInstructions{}, errors.New("audio analysis selection mode is invalid")
	}
	seenTracks := make(map[string]struct{}, len(result.TrackIDs))
	result.TrackIDs = slices.Clone(result.TrackIDs)
	for index := range result.TrackIDs {
		result.TrackIDs[index] = strings.TrimSpace(result.TrackIDs[index])
		if result.TrackIDs[index] == "" {
			return AudioAnalysisInstructions{}, errors.New("audio analysis track IDs must not be empty")
		}
		if _, duplicate := seenTracks[result.TrackIDs[index]]; duplicate {
			return AudioAnalysisInstructions{}, errors.New("audio analysis track IDs must be unique")
		}
		seenTracks[result.TrackIDs[index]] = struct{}{}
	}
	if len(result.TrackIDs) == 0 {
		return AudioAnalysisInstructions{}, errors.New("audio analysis requires at least one track")
	}
	seenVariants := make(map[AudioAnalysisVariant]struct{}, len(result.Variants))
	result.Variants = slices.Clone(result.Variants)
	for _, variant := range result.Variants {
		switch variant {
		case AudioAnalysisWaveform, AudioAnalysisSpectrogram, AudioAnalysisStats:
		default:
			return AudioAnalysisInstructions{}, errors.New("audio analysis output variant is invalid")
		}
		if _, duplicate := seenVariants[variant]; duplicate {
			return AudioAnalysisInstructions{}, errors.New("audio analysis variants must be unique")
		}
		seenVariants[variant] = struct{}{}
	}
	if len(result.Variants) == 0 {
		return AudioAnalysisInstructions{}, errors.New("audio analysis requires at least one output variant")
	}
	if result.ProfileVersion != AudioAnalysisProfileVersion {
		return AudioAnalysisInstructions{}, errors.New("audio analysis profile is unsupported")
	}
	return result, nil
}

// AudioAnalysisSubject is the private exact-generation input used by the decoder.
type AudioAnalysisSubject struct {
	Release             ReleaseRef
	SourcePath          string
	VideoPath           string
	SourceFingerprint   string
	ResourceID          string
	ManifestFingerprint string
	PrimaryTrackID      string
	Tracks              []MediaTrackFacts
}

// AudioAnalysisArtifact is one opaque, locally retained image or amplitude report.
type AudioAnalysisArtifact struct {
	ID      PublicResourceID      `json:"id"`
	Variant AudioAnalysisVariant  `json:"variant"`
	Status  StageStatus           `json:"status"`
	Width   int                   `json:"width,omitempty"`
	Height  int                   `json:"height,omitempty"`
	Text    string                `json:"text,omitempty"`
	Failure *AudioAnalysisFailure `json:"failure,omitempty"`
}

// AudioAnalysisTrackResult reports the exact decoded frame and format facts for one track.
type AudioAnalysisTrackResult struct {
	TrackID       string                  `json:"trackId"`
	Ordinal       int                     `json:"ordinal"`
	Title         string                  `json:"title,omitempty"`
	Codec         string                  `json:"codec,omitempty"`
	Language      string                  `json:"language,omitempty"`
	ChannelLayout string                  `json:"channelLayout,omitempty"`
	Channels      int                     `json:"channels"`
	SampleRate    int                     `json:"sampleRate"`
	SampleFrames  int64                   `json:"sampleFrames"`
	Duration      float64                 `json:"durationSeconds"`
	Status        StageStatus             `json:"status"`
	Artifacts     []AudioAnalysisArtifact `json:"artifacts"`
	Failure       *AudioAnalysisFailure   `json:"failure,omitempty"`
}

// AudioAnalysisResult is one immutable generation-bound analysis attempt.
type AudioAnalysisResult struct {
	ID                  AudioAnalysisResultID       `json:"id"`
	WorkflowID          WorkflowID                  `json:"workflowId"`
	Revision            WorkflowRevision            `json:"revision"`
	Release             ReleaseRef                  `json:"release"`
	ResourceID          string                      `json:"resourceId"`
	ManifestFingerprint string                      `json:"manifestFingerprint"`
	AttemptID           string                      `json:"attemptId"`
	Selection           AudioAnalysisSelectionMode  `json:"selection"`
	TrackIDs            []string                    `json:"trackIds"`
	Variants            []AudioAnalysisVariant      `json:"variants"`
	ProfileVersion      string                      `json:"profileVersion"`
	ResourceLimits      AudioAnalysisResourceLimits `json:"resourceLimits"`
	Status              StageStatus                 `json:"status"`
	Tracks              []AudioAnalysisTrackResult  `json:"tracks"`
	CreatedAt           time.Time                   `json:"createdAt" ts_type:"string"`
	CompletedAt         *time.Time                  `json:"completedAt,omitempty" ts_type:"string"`
	ExpiresAt           time.Time                   `json:"expiresAt" ts_type:"string"`
}

// Validate verifies public identity, authority, and terminal result shape.
func (r AudioAnalysisResult) Validate() error {
	if err := validateSnapshotIdentity(string(r.ID), r.Revision, r.CreatedAt); err != nil {
		return fmt.Errorf("audio analysis: %w", err)
	}
	if strings.TrimSpace(string(r.WorkflowID)) == "" || r.Release.Generation == 0 || strings.TrimSpace(r.Release.SourcePath) == "" {
		return errors.New("audio analysis requires workflow and release identity")
	}
	if strings.TrimSpace(r.ResourceID) == "" || strings.TrimSpace(r.ManifestFingerprint) == "" || strings.TrimSpace(r.AttemptID) == "" {
		return errors.New("audio analysis requires resource, manifest, and attempt identity")
	}
	if _, err := (AudioAnalysisInstructions{
		Release:        r.Release,
		ResourceID:     r.ResourceID,
		Selection:      r.Selection,
		TrackIDs:       r.TrackIDs,
		Variants:       r.Variants,
		ProfileVersion: r.ProfileVersion,
		ResourceLimits: r.ResourceLimits,
	}).Normalize(); err != nil {
		return fmt.Errorf("audio analysis selection: %w", err)
	}
	if !r.ExpiresAt.After(r.CreatedAt) {
		return errors.New("audio analysis expiry must follow creation")
	}
	if r.CompletedAt == nil {
		return errors.New("terminal audio analysis requires completion time")
	}
	if r.CompletedAt.Before(r.CreatedAt) || r.CompletedAt.After(r.ExpiresAt) {
		return errors.New("audio analysis completion time is outside its retention interval")
	}
	if len(r.Tracks) != len(r.TrackIDs) {
		return errors.New("audio analysis result must contain one result per selected track")
	}
	successes := 0
	failures := 0
	seenArtifacts := make(map[PublicResourceID]struct{})
	for index, track := range r.Tracks {
		if strings.TrimSpace(track.TrackID) == "" || track.TrackID != r.TrackIDs[index] || track.Ordinal <= 0 || track.Channels < 0 ||
			track.Channels > 8 || track.SampleRate < 0 || track.SampleFrames < 0 || track.Duration < 0 {
			return fmt.Errorf("audio analysis track %d has invalid identity or decoded facts", index+1)
		}
		if track.SampleRate == 0 && (track.SampleFrames != 0 || track.Duration != 0) {
			return fmt.Errorf("audio analysis track %d has duration without a sample rate", track.Ordinal)
		}
		wantDuration := float64(0)
		if track.SampleRate > 0 {
			wantDuration = float64(track.SampleFrames) / float64(track.SampleRate)
		}
		if difference := track.Duration - wantDuration; difference < -1e-9 || difference > 1e-9 {
			return fmt.Errorf("audio analysis track %d duration does not match its sample-frame count", track.Ordinal)
		}
		if track.Failure != nil {
			if err := validateAudioAnalysisFailure(*track.Failure); err != nil {
				return fmt.Errorf("audio analysis track %d failure: %w", track.Ordinal, err)
			}
		}
		seenVariants := make(map[AudioAnalysisVariant]struct{}, len(track.Artifacts))
		trackSuccesses := 0
		trackFailures := 0
		for _, artifact := range track.Artifacts {
			if !slices.Contains(r.Variants, artifact.Variant) {
				return fmt.Errorf("audio analysis track %d contains an unrequested artifact", track.Ordinal)
			}
			if _, duplicate := seenVariants[artifact.Variant]; duplicate {
				return fmt.Errorf("audio analysis track %d contains a duplicate artifact variant", track.Ordinal)
			}
			seenVariants[artifact.Variant] = struct{}{}
			switch artifact.Status {
			case StageStatusCompleted:
				if artifact.ID == "" || artifact.Failure != nil {
					return fmt.Errorf("audio analysis track %d completed artifact is invalid", track.Ordinal)
				}
				if artifact.Variant == AudioAnalysisStats {
					if strings.TrimSpace(artifact.Text) == "" || len(artifact.Text) > AudioAnalysisStatsMaxBytes || artifact.Width != 0 ||
						artifact.Height != 0 {
						return fmt.Errorf("audio analysis track %d completed statistics are invalid", track.Ordinal)
					}
				} else if artifact.Width <= 0 || artifact.Height <= 0 || artifact.Text != "" {
					return fmt.Errorf("audio analysis track %d completed image is invalid", track.Ordinal)
				}
				if _, duplicate := seenArtifacts[artifact.ID]; duplicate {
					return errors.New("audio analysis artifact IDs must be unique")
				}
				seenArtifacts[artifact.ID] = struct{}{}
				successes++
				trackSuccesses++
			case StageStatusFailed:
				if artifact.Failure == nil {
					return fmt.Errorf("audio analysis track %d failed artifact requires failure detail", track.Ordinal)
				}
				if err := validateAudioAnalysisFailure(*artifact.Failure); err != nil {
					return fmt.Errorf("audio analysis track %d artifact failure: %w", track.Ordinal, err)
				}
				failures++
				trackFailures++
			case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale,
				StageStatusPartial, StageStatusSkipped, StageStatusRunning, StageStatusExecuted, StageStatusInterrupted,
				StageStatusCanceled, StageStatusUnavailable:
				return fmt.Errorf("audio analysis track %d artifact has invalid terminal status %q", track.Ordinal, artifact.Status)
			default:
				return fmt.Errorf("audio analysis track %d artifact has unknown status %q", track.Ordinal, artifact.Status)
			}
		}
		switch track.Status {
		case StageStatusCompleted:
			if track.Channels <= 0 || track.SampleRate <= 0 || len(track.Artifacts) != len(r.Variants) ||
				trackSuccesses != len(r.Variants) || track.Failure != nil {
				return fmt.Errorf("audio analysis track %d completed without every requested artifact", track.Ordinal)
			}
		case StageStatusPartial:
			if track.Channels <= 0 || track.SampleRate <= 0 || len(track.Artifacts) != len(r.Variants) ||
				trackSuccesses == 0 || trackFailures == 0 {
				return fmt.Errorf("audio analysis track %d partial result is incomplete", track.Ordinal)
			}
		case StageStatusFailed:
			if trackSuccesses != 0 || (track.Failure == nil && trackFailures == 0) {
				return fmt.Errorf("audio analysis track %d failed without failure detail", track.Ordinal)
			}
			if len(track.Artifacts) == 0 {
				failures++
			}
		case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale,
			StageStatusSkipped, StageStatusRunning, StageStatusExecuted, StageStatusInterrupted, StageStatusCanceled,
			StageStatusUnavailable:
			return fmt.Errorf("audio analysis track %d has invalid terminal status %q", track.Ordinal, track.Status)
		default:
			return fmt.Errorf("audio analysis track %d has unknown status %q", track.Ordinal, track.Status)
		}
	}
	switch r.Status {
	case StageStatusCompleted:
		if successes == 0 || failures != 0 {
			return errors.New("completed audio analysis requires only successful artifacts")
		}
	case StageStatusPartial:
		if successes == 0 || failures == 0 {
			return errors.New("partial audio analysis requires successful and failed work")
		}
	case StageStatusFailed:
		if failures == 0 || successes != 0 {
			return errors.New("failed audio analysis requires only failed work")
		}
	case StageStatusCanceled, StageStatusInterrupted, StageStatusUnavailable:
	case StageStatusPending, StageStatusQueued, StageStatusReady, StageStatusBlocked, StageStatusStale,
		StageStatusSkipped, StageStatusRunning, StageStatusExecuted:
		return errors.New("audio analysis result must be terminal")
	default:
		return fmt.Errorf("audio analysis result has unknown status %q", r.Status)
	}
	return nil
}

func validateAudioAnalysisFailure(failure AudioAnalysisFailure) error {
	switch failure.Code {
	case AudioAnalysisFailureInvalidSelection,
		AudioAnalysisFailureAmbiguousBinding,
		AudioAnalysisFailureStaleSource,
		AudioAnalysisFailureNoAudio,
		AudioAnalysisFailureUnsupportedSource,
		AudioAnalysisFailureUnsupportedLayout,
		AudioAnalysisFailureUnsupportedCodec,
		AudioAnalysisFailureFFmpegUnavailable,
		AudioAnalysisFailureDecode,
		AudioAnalysisFailureMalformedPCM,
		AudioAnalysisFailureOutput,
		AudioAnalysisFailureResourceUnavailable,
		AudioAnalysisFailureInterrupted,
		AudioAnalysisFailureCanceled:
	default:
		return errors.New("failure code is invalid")
	}
	if strings.TrimSpace(failure.Message) == "" {
		return errors.New("failure message is required")
	}
	return nil
}
