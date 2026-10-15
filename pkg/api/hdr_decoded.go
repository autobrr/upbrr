// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"slices"
	"strings"
)

// HDRDecodedSourceEvidence is per-file probe evidence. Decoder output must
// independently confirm geometry with codec cropping and rotation disabled.
// Empty colour or rational fields mean unknown, never a default.
type HDRDecodedSourceEvidence struct {
	DolbyVisionProfile string `json:"dolbyVisionProfile,omitempty"`
	VideoStreams       int    `json:"videoStreams"`
	// VideoStreamOrdinal selects the unique HEVC among non-attached video streams.
	VideoStreamOrdinal     int    `json:"videoStreamOrdinal,omitempty"`
	RawHEVC                bool   `json:"rawHEVC"`
	Codec                  string `json:"codec"`
	Width                  int    `json:"width"`
	Height                 int    `json:"height"`
	BitDepth               int    `json:"bitDepth"`
	PixelAspectNumerator   int    `json:"pixelAspectNumerator"`
	PixelAspectDenominator int    `json:"pixelAspectDenominator"`
	Range                  string `json:"range"`
	Matrix                 string `json:"matrix"`
	Transfer               string `json:"transfer"`
	Primaries              string `json:"primaries"`
	FrameRateNumerator     string `json:"frameRateNumerator,omitempty"`
	FrameRateDenominator   string `json:"frameRateDenominator,omitempty"`
}

// HDRDecodedMode identifies a sampled picture check, separate from native metadata extraction.
type HDRDecodedMode string

const (
	HDRDecodedAutomatic    HDRDecodedMode = "automatic"
	HDRDecodedBoundarySync HDRDecodedMode = "boundary_sync"
	HDRDecodedAudio        HDRDecodedMode = "audio"
	HDRDecodedSceneSync    HDRDecodedMode = "scene_sync"
	HDRDecodedActiveArea   HDRDecodedMode = "active_area"
	HDRDecodedGrade        HDRDecodedMode = "grade"
	HDRDecodedColour       HDRDecodedMode = "colour"
)

// HDRDecodedOptions selects primary-source checks and optional paired-source measurements.
// The zero value is invalid; at least one check must be selected.
type HDRDecodedOptions struct {
	Automatic           bool `json:"automatic,omitempty"`
	BoundarySync        bool `json:"boundarySync,omitempty"`
	AllAudioTracks      bool `json:"allAudioTracks,omitempty"`
	VideoDecoderThreads int  `json:"videoDecoderThreads,omitempty"`
	// Audio requests complete primary audio alongside native metadata without picture decoding.
	// Automatic already includes audio; Normalize removes a redundant Audio selection.
	Audio bool `json:"audio,omitempty"`
	// Sampled reduces picture measurement while retaining complete native metadata and audio.
	// False preserves existing full-frame requests and retained measurement identities.
	Sampled    bool                  `json:"sampled,omitempty"`
	SceneSync  bool                  `json:"sceneSync"`
	ActiveArea bool                  `json:"activeArea"`
	Comparison *HDRComparisonOptions `json:"comparison,omitempty"`
}

// HDRComparisonOptions binds paired measurements to an explicitly selected source B.
// Measurements use the same crop on both sources without resizing or automatic alignment.
type HDRComparisonOptions struct {
	// SourceID is the private, generation-bound comparison handle, not a path.
	SourceID string `json:"sourceId"`
	// FrameOffset maps primary index A to B = A + offset. Nil is invalid; zero is explicit alignment.
	FrameOffset *int    `json:"frameOffset"`
	Crop        HDRCrop `json:"crop"`
	Grade       bool    `json:"grade"`
	Colour      bool    `json:"colour"`
}

// HDRCrop is a rectangle in each source's coded-pixel coordinates.
// Width and height must be positive even values; decoding checks actual raster bounds.
type HDRCrop struct {
	Left   int `json:"left"`
	Top    int `json:"top"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Normalize validates the selection and returns an independent copy of its comparison options.
func (o HDRDecodedOptions) Normalize() (HDRDecodedOptions, error) {
	return o.normalize(false)
}

func (o HDRDecodedOptions) normalize(deferred bool) (HDRDecodedOptions, error) {
	if err := o.validate(deferred); err != nil {
		return HDRDecodedOptions{}, err
	}
	if o.Comparison != nil {
		comparison := *o.Comparison
		comparison.FrameOffset = new(*comparison.FrameOffset)
		o.Comparison = &comparison
	}
	if o.Automatic {
		o.Audio, o.SceneSync, o.ActiveArea = false, false, false
	}
	return o, nil
}

// HDRDecodedRetry grants one explicit new measurement pass for eligible modes
// in the current retained attempt. It never grants a native source retry.
type HDRDecodedRetry struct {
	AnalysisAttemptID string           `json:"analysisAttemptId"`
	Modes             []HDRDecodedMode `json:"modes"`
}

// Validate requires a bounded attempt ID and distinct modes present in options.
// The workflow separately checks current-attempt ownership and retry eligibility.
func (r HDRDecodedRetry) Validate(options *HDRDecodedOptions) error {
	maximum := 6
	if options != nil && options.RequiresV3() {
		maximum = 7
	}
	if options == nil || strings.TrimSpace(r.AnalysisAttemptID) == "" || len(r.AnalysisAttemptID) > 128 || len(r.Modes) == 0 || len(r.Modes) > maximum {
		return errors.New("decoded retry requires retained attempt and requested modes")
	}
	seen := make(map[HDRDecodedMode]bool, len(r.Modes))
	for _, mode := range r.Modes {
		if !options.Requested(mode) || seen[mode] {
			return errors.New("decoded retry contains duplicate or unrequested modes")
		}
		seen[mode] = true
	}
	return nil
}

// AnalysisStatus includes optional measurement outcomes without changing the
// independently reusable native-format outcomes.
func (t HDRAnalysisTargetResult) AnalysisStatus() StageStatus {
	status := t.NativeStatus()
	for _, result := range t.Decoded {
		if result.Mode == HDRDecodedAudio && result.State == HDRDecodedNotRun && result.Reason == "no_audio" {
			continue
		}
		if result.Coverage != "" {
			if result.State == HDRDecodedCanceled {
				return StageStatusCanceled
			}
			if result.State == HDRDecodedInterrupted {
				return StageStatusInterrupted
			}
			if (result.Coverage != HDRCoverageComplete || result.Reason == "render_unavailable" || result.Reason == "render_identity_unavailable") &&
				status == StageStatusCompleted {
				status = StageStatusPartial
			}
			if result.Measured > 0 && status == StageStatusFailed {
				status = StageStatusPartial
			}
			continue
		}
		if status == StageStatusFailed && (result.State == HDRDecodedComplete ||
			result.Mode == HDRDecodedAudio && result.State == HDRDecodedInconclusive && result.Measured > 0) {
			status = StageStatusPartial
		}
		if result.State == HDRDecodedCanceled {
			return StageStatusCanceled
		}
		if result.State == HDRDecodedInterrupted {
			return StageStatusInterrupted
		}
		if (result.State != HDRDecodedComplete || result.Reason == "render_unavailable" || result.Reason == "render_identity_unavailable") &&
			status == StageStatusCompleted {
			status = StageStatusPartial
		}
	}
	return status
}

// DecodedMode returns the owning picture check, or empty for native plots and reports.
func (k HDRArtifactKind) DecodedMode() HDRDecodedMode {
	switch k {
	case HDRArtifactBoundaryDVSceneSheet, HDRArtifactBoundaryHDR10PlusSceneSheet:
		return HDRDecodedBoundarySync
	case HDRArtifactPicturePlot, HDRArtifactReferencePicturePlot, HDRArtifactDVOverlay, HDRArtifactHDR10PlusOverlay,
		HDRArtifactDVDifference, HDRArtifactHDR10PlusDifference, HDRArtifactDVSceneSheet,
		HDRArtifactHDR10PlusSceneSheet, HDRArtifactAutomaticArea, HDRArtifactAutomaticGrade, HDRArtifactAutomaticColour, HDRArtifactChromaticity:
		return HDRDecodedAutomatic
	case HDRArtifactAudioWaveform, HDRArtifactAudioSpectrogram, HDRArtifactAudioStats:
		return HDRDecodedAudio
	case HDRArtifactHDR10PlusPlot, HDRArtifactDolbyVisionL1Plot, HDRArtifactStructuralReport:
		return ""
	case HDRArtifactSceneContactSheet:
		return HDRDecodedSceneSync
	case HDRArtifactActiveAreaContactSheet:
		return HDRDecodedActiveArea
	case HDRArtifactGradePlot:
		return HDRDecodedGrade
	case HDRArtifactColourPlot:
		return HDRDecodedColour
	default:
		return ""
	}
}

// ImageDimensions returns the fixed raster size, or zeroes for non-image and unknown kinds.
func (k HDRArtifactKind) ImageDimensions() (int, int) {
	//nolint:exhaustive // Automatic image families override the existing dimensions below.
	switch k {
	case HDRArtifactPicturePlot, HDRArtifactReferencePicturePlot, HDRArtifactDVOverlay, HDRArtifactHDR10PlusOverlay:
		return 1800, 720
	case HDRArtifactDVDifference, HDRArtifactHDR10PlusDifference, HDRArtifactAutomaticGrade:
		return 1800, 800
	case HDRArtifactAutomaticColour:
		return 2000, 650
	case HDRArtifactChromaticity:
		return 2300, 620
	case HDRArtifactDVSceneSheet, HDRArtifactHDR10PlusSceneSheet, HDRArtifactBoundaryDVSceneSheet, HDRArtifactBoundaryHDR10PlusSceneSheet:
		return 1800, 900
	}
	if k.DecodedMode() != "" {
		return 1200, 900
	}
	if k == HDRArtifactHDR10PlusPlot || k == HDRArtifactDolbyVisionL1Plot {
		return HDRAnalysisWidth, HDRAnalysisHeight
	}
	return 0, 0
}

// Validate checks selection and mapping bounds without opening either source or resolving its handle.
func (o HDRDecodedOptions) Validate() error { return o.validate(false) }

func (o HDRDecodedOptions) validate(deferred bool) error {
	if o.BoundarySync && (o.Automatic || o.Sampled || o.SceneSync || o.Comparison != nil) {
		return errors.New("boundary synchronization cannot include automatic, sampled, legacy scene or comparison checks")
	}
	if o.AllAudioTracks && !o.Automatic && !o.Audio {
		return errors.New("all audio tracks requires audio analysis")
	}
	if o.VideoDecoderThreads < 0 || o.VideoDecoderThreads > 64 {
		return errors.New("video decoder threads must be between 0 and 64")
	}
	if o.VideoDecoderThreads != 0 && !o.Automatic && !o.BoundarySync && !o.SceneSync && !o.ActiveArea && o.Comparison == nil {
		return errors.New("video decoder threads requires picture analysis")
	}
	if o.Sampled && !o.Automatic {
		return errors.New("sampled picture measurement requires automatic HDR analysis")
	}
	if o.Audio && !o.Automatic && !o.BoundarySync && (o.SceneSync || o.ActiveArea || o.Comparison != nil) {
		return errors.New("HDR audio-only analysis cannot include picture checks or a comparison source")
	}
	if !o.Automatic && !o.BoundarySync && !o.Audio && !o.SceneSync && !o.ActiveArea && o.Comparison == nil {
		return errors.New("decoded HDR options require at least one check")
	}
	if c := o.Comparison; c != nil {
		if (!deferred && c.SourceID == "") || len(c.SourceID) > 128 || c.FrameOffset == nil || *c.FrameOffset < -500000 || *c.FrameOffset > 500000 ||
			(!c.Grade && !c.Colour) {
			return errors.New("HDR comparison requires a source, explicit bounded frame offset and check")
		}
		if c.Crop.Left < 0 || c.Crop.Top < 0 || c.Crop.Left > 8192 || c.Crop.Top > 4320 ||
			c.Crop.Width < 2 || c.Crop.Height < 2 || c.Crop.Width > 8192-c.Crop.Left || c.Crop.Height > 4320-c.Crop.Top ||
			c.Crop.Width%2 != 0 || c.Crop.Height%2 != 0 {
			return errors.New("HDR comparison requires an even, bounded common crop")
		}
	}
	return nil
}

// Requested reports whether mode is selected; unknown modes return false.
func (o HDRDecodedOptions) Requested(mode HDRDecodedMode) bool {
	switch mode {
	case HDRDecodedBoundarySync:
		return o.BoundarySync
	case HDRDecodedAutomatic:
		return o.Automatic
	case HDRDecodedAudio:
		return o.Automatic || o.Audio
	case HDRDecodedSceneSync:
		return !o.Automatic && o.SceneSync
	case HDRDecodedActiveArea:
		return !o.Automatic && o.ActiveArea
	case HDRDecodedGrade:
		return !o.Automatic && o.Comparison != nil && o.Comparison.Grade
	case HDRDecodedColour:
		return !o.Automatic && o.Comparison != nil && o.Comparison.Colour
	default:
		return false
	}
}

// HDRDecodedState describes measurement completion, not a pass/fail verdict on the media.
type HDRDecodedState string

const (
	HDRDecodedComplete     HDRDecodedState = "complete"
	HDRDecodedFailed       HDRDecodedState = "failed"
	HDRDecodedCanceled     HDRDecodedState = "canceled"
	HDRDecodedInterrupted  HDRDecodedState = "interrupted"
	HDRDecodedUnsupported  HDRDecodedState = "unsupported"
	HDRDecodedInconclusive HDRDecodedState = "inconclusive"
	HDRDecodedNotRun       HDRDecodedState = "not_run"
)

// HDRDecodedResult describes one bounded sampling pass and its retained artifact IDs.
// Complete requires coverage of every selected sample, not every picture in the source.
type HDRDecodedResult struct {
	Coverage    HDRCoverage           `json:"coverage,omitempty"`
	AudioTracks []HDRAudioTrackResult `json:"audioTracks,omitempty"`
	Checks      []HDRCheckResult      `json:"checks,omitempty"`
	Mode        HDRDecodedMode        `json:"mode"`
	State       HDRDecodedState       `json:"state"`
	Reason      string                `json:"reason,omitempty"`
	Profile     string                `json:"profile"`
	Selected    int                   `json:"selected"`
	Measured    int                   `json:"measured"`
	Artifacts   []PublicResourceID    `json:"artifacts"`
}

func (r HDRDecodedResult) Validate() error {
	if r.Coverage != "" || r.AudioTracks != nil || r.Mode == HDRDecodedBoundarySync {
		return r.validateV3()
	}
	if !slices.Contains(
		[]HDRDecodedMode{HDRDecodedAutomatic, HDRDecodedAudio, HDRDecodedSceneSync, HDRDecodedActiveArea, HDRDecodedGrade, HDRDecodedColour},
		r.Mode,
	) ||
		!slices.Contains(
			[]HDRDecodedState{
				HDRDecodedComplete,
				HDRDecodedFailed,
				HDRDecodedCanceled,
				HDRDecodedInterrupted,
				HDRDecodedUnsupported,
				HDRDecodedInconclusive,
				HDRDecodedNotRun,
			},
			r.State,
		) {
		return errors.New("invalid decoded HDR outcome")
	}
	maximum, artifacts := 600, 2
	switch r.Mode {
	case HDRDecodedAutomatic:
		maximum, artifacts = 500000, 16
	case HDRDecodedAudio:
		artifacts = 3
	case HDRDecodedSceneSync, HDRDecodedActiveArea, HDRDecodedGrade, HDRDecodedColour:
	case HDRDecodedBoundarySync:
		return errors.New("boundary outcome requires v3 coverage")
	}
	if len(r.Reason) > 256 || len(r.Profile) > 64 || r.Selected < 0 || r.Selected > maximum || r.Measured < 0 || r.Measured > r.Selected ||
		len(r.Artifacts) > artifacts ||
		len(r.Checks) > 32 {
		return errors.New("decoded HDR outcome exceeds bounds")
	}
	for _, check := range r.Checks {
		if check.Code == "" || len(check.Code) > 128 || check.Message == "" || len(check.Message) > 1024 ||
			!slices.Contains(
				[]HDRCheckState{HDRCheckPass, HDRCheckWarning, HDRCheckFail, HDRCheckInconclusive, HDRCheckNotRun, HDRCheckUnsupported},
				check.State,
			) {
			return errors.New("invalid decoded HDR check")
		}
	}
	if r.State == HDRDecodedComplete && (r.Profile == "" || r.Measured == 0 || r.Measured != r.Selected) {
		return errors.New("complete decoded HDR result requires complete measurement coverage")
	}
	return nil
}
