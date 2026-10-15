// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

const (
	HDRAnalysisProfileV2  = "hdr-analysis-v2"
	HDRExtractionSchemaV2 = "hdr-extraction-v2"
)

// HDRAnalysisFormats controls collectors in one shared source traversal.
type HDRAnalysisFormats string

const (
	HDRAnalysisFormatsAuto        HDRAnalysisFormats = "auto"
	HDRAnalysisFormatsHDR10Plus   HDRAnalysisFormats = "hdr10plus"
	HDRAnalysisFormatsDolbyVision HDRAnalysisFormats = "dolby-vision"
	HDRAnalysisFormatsBoth        HDRAnalysisFormats = "both"
)

// Normalize maps an empty selection to auto and rejects unknown collector selections.
func (f HDRAnalysisFormats) Normalize() (HDRAnalysisFormats, error) {
	if f == "" {
		f = HDRAnalysisFormatsAuto
	}
	switch f {
	case HDRAnalysisFormatsAuto, HDRAnalysisFormatsHDR10Plus, HDRAnalysisFormatsDolbyVision, HDRAnalysisFormatsBoth:
		return f, nil
	default:
		return "", errors.New("HDR format selection is invalid")
	}
}

// HDRNativeFormat labels metadata without conflating their measurements.
type HDRNativeFormat string

const (
	HDRNativeHDR10Plus   HDRNativeFormat = "hdr10plus"
	HDRNativeDolbyVision HDRNativeFormat = "dolby_vision"
)

// HDRExtractionState records one collector's outcome independently of its sibling format.
type HDRExtractionState string

const (
	HDRExtractionComplete    HDRExtractionState = "complete"
	HDRExtractionAbsent      HDRExtractionState = "absent"
	HDRExtractionUnsupported HDRExtractionState = "unsupported"
	HDRExtractionFailed      HDRExtractionState = "failed"
	HDRExtractionCanceled    HDRExtractionState = "canceled"
	HDRExtractionInterrupted HDRExtractionState = "interrupted"
	HDRExtractionNotAnalyzed HDRExtractionState = "not_analyzed"
)

// HDRCoverage describes metadata presence across the retained output timeline.
// A completed scan may still have partial or absent metadata coverage.
type HDRCoverage string

const (
	HDRCoverageComplete HDRCoverage = "complete"
	HDRCoveragePartial  HDRCoverage = "partial"
	HDRCoverageAbsent   HDRCoverage = "absent"
	HDRCoverageUnknown  HDRCoverage = "unknown"
)

type HDRCheckState string

const (
	HDRCheckPass         HDRCheckState = "pass"
	HDRCheckWarning      HDRCheckState = "warning"
	HDRCheckFail         HDRCheckState = "fail"
	HDRCheckInconclusive HDRCheckState = "inconclusive"
	HDRCheckNotRun       HDRCheckState = "not_run"
	HDRCheckUnsupported  HDRCheckState = "unsupported"
)

// HDRCheckResult is a structured observation about metadata or an associated artifact.
type HDRCheckResult struct {
	Code    string        `json:"code"`
	State   HDRCheckState `json:"state"`
	Message string        `json:"message"`
}

// HDRFormatResult separates successful extraction from coverage and check findings.
// TrackID is a decimal string so browser consumers never round native identities.
type HDRFormatResult struct {
	Format     HDRNativeFormat     `json:"format"`
	State      HDRExtractionState  `json:"state"`
	Coverage   HDRCoverage         `json:"coverage"`
	Requested  bool                `json:"requested"`
	Advertised bool                `json:"advertised"`
	TrackID    string              `json:"trackId,omitempty"`
	Frames     int                 `json:"frames"`
	Retained   int                 `json:"retained"`
	Observed   int                 `json:"observed"`
	Suppressed int                 `json:"suppressed"`
	Missing    int                 `json:"missing"`
	L1Samples  int                 `json:"l1Samples"`
	Scenes     int                 `json:"scenes"`
	Profile    string              `json:"profile,omitempty"`
	Checks     []HDRCheckResult    `json:"checks"`
	Failure    *HDRAnalysisFailure `json:"failure,omitempty"`
}

type HDRArtifactKind string

const (
	HDRArtifactPicturePlot                 HDRArtifactKind = "picture_brightness_plot"
	HDRArtifactReferencePicturePlot        HDRArtifactKind = "reference_brightness_plot"
	HDRArtifactDVOverlay                   HDRArtifactKind = "dolby_vision_overlay"
	HDRArtifactHDR10PlusOverlay            HDRArtifactKind = "hdr10plus_overlay"
	HDRArtifactDVDifference                HDRArtifactKind = "dolby_vision_difference"
	HDRArtifactHDR10PlusDifference         HDRArtifactKind = "hdr10plus_difference"
	HDRArtifactDVSceneSheet                HDRArtifactKind = "dolby_vision_scene_sheet"
	HDRArtifactHDR10PlusSceneSheet         HDRArtifactKind = "hdr10plus_scene_sheet"
	HDRArtifactBoundaryDVSceneSheet        HDRArtifactKind = "boundary_dolby_vision_scene_sheet"
	HDRArtifactBoundaryHDR10PlusSceneSheet HDRArtifactKind = "boundary_hdr10plus_scene_sheet"
	HDRArtifactAutomaticArea               HDRArtifactKind = "automatic_active_area"
	HDRArtifactAutomaticGrade              HDRArtifactKind = "automatic_grade_difference"
	HDRArtifactAutomaticColour             HDRArtifactKind = "automatic_colour_bias"
	HDRArtifactChromaticity                HDRArtifactKind = "chromaticity_plot"
	HDRArtifactAudioWaveform               HDRArtifactKind = "audio_waveform"
	HDRArtifactAudioSpectrogram            HDRArtifactKind = "audio_spectrogram"
	HDRArtifactAudioStats                  HDRArtifactKind = "audio_stats"
	HDRArtifactHDR10PlusPlot               HDRArtifactKind = "hdr10plus_plot"
	HDRArtifactSceneContactSheet           HDRArtifactKind = "scene_contact_sheet"
	HDRArtifactActiveAreaContactSheet      HDRArtifactKind = "active_area_contact_sheet"
	HDRArtifactGradePlot                   HDRArtifactKind = "grade_plot"
	HDRArtifactColourPlot                  HDRArtifactKind = "colour_plot"
	HDRArtifactDolbyVisionL1Plot           HDRArtifactKind = "dolby_vision_l1_plot"
	HDRArtifactStructuralReport            HDRArtifactKind = "structural_report"
)

// HDRTypedArtifact identifies a retained image or structural JSON report without exposing its path.
// Only selectable image kinds may be included in tracker descriptions.
type HDRTypedArtifact struct {
	AudioTrackID           string           `json:"audioTrackId,omitempty"`
	MeasurementFingerprint string           `json:"measurementFingerprint,omitempty"`
	RenderFingerprint      string           `json:"renderFingerprint,omitempty"`
	ID                     PublicResourceID `json:"id"`
	Kind                   HDRArtifactKind  `json:"kind"`
	Format                 HDRNativeFormat  `json:"format,omitempty"`
	Width                  int              `json:"width"`
	Height                 int              `json:"height"`
	MIME                   string           `json:"mime"`
	Profile                string           `json:"profile"`
}

// HDRImageSelection distinguishes default inclusion (a nil selection) from an
// explicit empty selection. IDs always belong to the exact retained result.
type HDRImageSelection struct {
	ArtifactIDs  []PublicResourceID `json:"artifactIds"`
	RecoveryNote string             `json:"recoveryNote,omitempty"`
}

// Filename uses only backend-owned artifact kinds.
func (a HDRTypedArtifact) Filename() string {
	if a.AudioTrackID != "" {
		plain := a
		plain.AudioTrackID = ""
		return fmt.Sprintf("track-%x-%s", sha256.Sum256([]byte(a.AudioTrackID)), plain.Filename())
	}
	switch a.Kind {
	case HDRArtifactBoundaryDVSceneSheet, HDRArtifactBoundaryHDR10PlusSceneSheet:
		return string(a.Kind) + ".png"
	case HDRArtifactAudioStats:
		return "audio-stats.txt"
	case HDRArtifactPicturePlot, HDRArtifactReferencePicturePlot, HDRArtifactDVOverlay, HDRArtifactHDR10PlusOverlay,
		HDRArtifactDVDifference, HDRArtifactHDR10PlusDifference, HDRArtifactDVSceneSheet,
		HDRArtifactHDR10PlusSceneSheet, HDRArtifactAutomaticArea, HDRArtifactAutomaticGrade,
		HDRArtifactAutomaticColour, HDRArtifactChromaticity, HDRArtifactAudioWaveform, HDRArtifactAudioSpectrogram:
		return string(a.Kind) + ".png"
	case HDRArtifactHDR10PlusPlot:
		return "hdr10plus.png"
	case HDRArtifactDolbyVisionL1Plot:
		return "dolby-vision-l1.png"
	case HDRArtifactStructuralReport:
		return "hdr-analysis.json"
	case HDRArtifactSceneContactSheet:
		return "hdr-scene-sync.png"
	case HDRArtifactActiveAreaContactSheet:
		return "hdr-active-area.png"
	case HDRArtifactGradePlot:
		return "hdr-grade.png"
	case HDRArtifactColourPlot:
		return "hdr-colour.png"
	default:
		return "hdr-analysis"
	}
}

// HDRSourceEvidence records advertised formats separately from native capability.
// UniqueHEVC is selection evidence, not a MediaInfo array index or native track ID.
type HDRSourceEvidence struct {
	UniqueHEVC  bool                      `json:"uniqueHEVC"`
	HDR10Plus   bool                      `json:"hdr10plus"`
	DolbyVision bool                      `json:"dolbyVision"`
	Decoded     *HDRDecodedSourceEvidence `json:"decoded,omitempty"`
}

func (r HDRFormatResult) Validate() error {
	if r.Format != HDRNativeHDR10Plus && r.Format != HDRNativeDolbyVision {
		return errors.New("HDR result format is invalid")
	}
	if !slices.Contains(
		[]HDRExtractionState{
			HDRExtractionComplete,
			HDRExtractionAbsent,
			HDRExtractionUnsupported,
			HDRExtractionFailed,
			HDRExtractionCanceled,
			HDRExtractionInterrupted,
			HDRExtractionNotAnalyzed,
		},
		r.State,
	) {
		return errors.New("HDR extraction state is invalid")
	}
	if !slices.Contains([]HDRCoverage{HDRCoverageComplete, HDRCoveragePartial, HDRCoverageAbsent, HDRCoverageUnknown}, r.Coverage) {
		return errors.New("HDR coverage is invalid")
	}
	if r.TrackID != "" {
		if _, err := strconv.ParseUint(r.TrackID, 10, 64); err != nil {
			return errors.New("HDR track identity is invalid")
		}
	}
	for _, n := range []int{r.Frames, r.Retained, r.Observed, r.Suppressed, r.Missing, r.L1Samples, r.Scenes} {
		if n < 0 || n > 500000 {
			return errors.New("HDR counts exceed bounds")
		}
	}
	if r.State == HDRExtractionComplete && (r.Frames == 0 || r.Coverage == HDRCoverageUnknown || r.Coverage == HDRCoverageAbsent || r.Failure != nil) {
		return errors.New("completed HDR format requires native evidence")
	}
	if r.State == HDRExtractionAbsent && (r.Coverage != HDRCoverageAbsent || r.Retained != 0) {
		return errors.New("HDR absence contradicts native evidence")
	}
	if r.Failure != nil {
		if err := r.Failure.Validate(); err != nil {
			return err
		}
	}
	if len(r.Checks) > 128 {
		return errors.New("too many HDR checks")
	}
	for _, check := range r.Checks {
		if check.Code == "" || check.Message == "" ||
			!slices.Contains(
				[]HDRCheckState{HDRCheckPass, HDRCheckWarning, HDRCheckFail, HDRCheckInconclusive, HDRCheckNotRun, HDRCheckUnsupported},
				check.State,
			) {
			return errors.New("HDR check is invalid")
		}
	}
	return nil
}

// NativeStatus folds requested supported work; opportunistic verified absence
// does not make a successful single-format source partial.
func (t HDRAnalysisTargetResult) NativeStatus() StageStatus {
	success, unavailable := false, false
	for _, r := range t.Formats {
		for _, check := range r.Checks {
			if check.Code == "render_canceled" {
				return StageStatusCanceled
			}
			if check.Code == "render_interrupted" {
				return StageStatusInterrupted
			}
		}
		if r.State == HDRExtractionCanceled {
			return StageStatusCanceled
		}
		if r.State == HDRExtractionInterrupted {
			return StageStatusInterrupted
		}
		if r.State == HDRExtractionComplete {
			success = true
			for _, c := range r.Checks {
				if c.State == HDRCheckUnsupported {
					unavailable = true
				}
			}
		} else if r.State != HDRExtractionNotAnalyzed && (r.Requested || r.Advertised || r.State == HDRExtractionFailed) {
			unavailable = true
		}
	}
	if success && unavailable {
		return StageStatusPartial
	}
	if success {
		return StageStatusCompleted
	}
	return StageStatusFailed
}

func (t HDRAnalysisTargetResult) validateNative(artifacts map[PublicResourceID]bool) error {
	if t.Artifact != nil || t.Failure != nil || t.Frames != 0 || t.Scenes != 0 || t.Profile != "" {
		return errors.New("v2 HDR target contains legacy authoritative fields")
	}
	if len(t.Formats) != 2 || t.Formats[0].Format != HDRNativeHDR10Plus || t.Formats[1].Format != HDRNativeDolbyVision {
		return errors.New("HDR format outcomes must use deterministic order")
	}
	for _, r := range t.Formats {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	if t.Status != t.AnalysisStatus() {
		return errors.New("HDR target aggregate disagrees with formats")
	}
	if len(t.Artifacts) > 24 || len(t.Decoded) > 6 {
		return errors.New("too many native HDR artifacts")
	}
	kinds := make(map[HDRArtifactKind]bool)
	for _, a := range t.Artifacts {
		if a.ID == "" || artifacts[a.ID] || kinds[a.Kind] || a.Profile != HDRAnalysisProfileV2 {
			return errors.New("HDR artifact identity is invalid")
		}
		artifacts[a.ID] = true
		kinds[a.Kind] = true
		if err := a.Validate(t.Formats); err != nil {
			return err
		}
		if mode := a.Kind.DecodedMode(); mode != "" {
			valid := false
			for _, decoded := range t.Decoded {
				valid = valid || decoded.Mode == mode && (decoded.State == HDRDecodedComplete || decoded.State == HDRDecodedInconclusive) &&
					decoded.Measured > 0 && slices.Contains(decoded.Artifacts, a.ID)
			}
			if !valid {
				return errors.New("decoded image lacks retained measurement authority")
			}
		}
	}
	return nil
}

// Validate verifies media properties and the completed metadata authority behind
// an image. Reports have no format-specific image authority.
func (a HDRTypedArtifact) Validate(formats []HDRFormatResult) error {
	if a.Profile == HDRAnalysisProfileV3 {
		return a.validateV3(formats)
	}
	if a.AudioTrackID != "" || a.MeasurementFingerprint != "" || a.RenderFingerprint != "" || a.Kind.DecodedMode() == HDRDecodedBoundarySync {
		return errors.New("legacy HDR artifact contains v3 authority")
	}
	if a.ID == "" || a.Profile != HDRAnalysisProfileV2 {
		return errors.New("HDR artifact identity is invalid")
	}
	if a.Kind == HDRArtifactStructuralReport {
		if a.MIME != "application/json" || a.Width != 0 || a.Height != 0 || a.Format != "" {
			return errors.New("HDR report artifact is invalid")
		}
		return nil
	}
	if a.Kind.DecodedMode() != "" {
		if a.Kind == HDRArtifactAudioStats {
			if a.Format != "" || a.MIME != "text/plain" || a.Width != 0 || a.Height != 0 {
				return errors.New("audio statistics artifact is invalid")
			}
			return nil
		}
		if a.Kind == HDRArtifactAudioWaveform || a.Kind == HDRArtifactAudioSpectrogram {
			if a.Format != "" || a.MIME != "image/png" || a.Width < 1 || a.Width > 8192 || a.Height < 1 || a.Height > 8192 {
				return errors.New("audio image properties are invalid")
			}
			return nil
		}
		width, height := a.Kind.ImageDimensions()
		if a.Format != "" || a.MIME != "image/png" || a.Width != width || a.Height != height {
			return errors.New("decoded HDR image properties are invalid")
		}
		return nil
	}
	want := HDRNativeHDR10Plus
	if a.Kind == HDRArtifactDolbyVisionL1Plot {
		want = HDRNativeDolbyVision
	} else if a.Kind != HDRArtifactHDR10PlusPlot {
		return errors.New("HDR artifact kind is invalid")
	}
	if a.Format != want || a.MIME != "image/png" || a.Width != HDRAnalysisWidth || a.Height != HDRAnalysisHeight {
		return errors.New("HDR image properties are invalid")
	}
	for _, format := range formats {
		if format.Format == want && format.State == HDRExtractionComplete && format.Validate() == nil {
			return nil
		}
	}
	return errors.New("HDR image has no completed format authority")
}

func (r HDRAnalysisResult) validateNativeResult() error {
	artifacts := make(map[PublicResourceID]bool)
	for i, t := range r.Targets {
		if t.TargetID != r.TargetIDs[i] {
			return errors.New("HDR target order is invalid")
		}
		if err := t.validateNative(artifacts); err != nil {
			return err
		}
		if err := r.Formats.ValidateResults(t.Formats); err != nil {
			return err
		}
		seen := make(map[HDRDecodedMode]bool)
		for _, decoded := range t.Decoded {
			if decoded.Coverage != "" || decoded.AudioTracks != nil || decoded.Mode == HDRDecodedBoundarySync {
				return errors.New("legacy HDR outcome contains v3 authority")
			}
			if r.Decoded == nil || !r.Decoded.Requested(decoded.Mode) || seen[decoded.Mode] {
				return errors.New("decoded outcome contradicts requested modes")
			}
			if err := decoded.Validate(); err != nil {
				return err
			}
			seen[decoded.Mode] = true
			for _, id := range decoded.Artifacts {
				found := false
				for _, artifact := range t.Artifacts {
					found = found || artifact.ID == id && artifact.Kind.DecodedMode() == decoded.Mode
				}
				if !found {
					return errors.New("decoded outcome references an unavailable image")
				}
			}
		}
		if r.Decoded != nil {
			for _, mode := range []HDRDecodedMode{HDRDecodedAutomatic, HDRDecodedAudio, HDRDecodedSceneSync, HDRDecodedActiveArea, HDRDecodedGrade, HDRDecodedColour} {
				if r.Decoded.Requested(mode) != seen[mode] {
					return errors.New("requested decoded mode has no terminal outcome")
				}
			}
		}
	}
	if r.Status != NativeHDRAggregate(r.Targets) {
		return errors.New("HDR aggregate disagrees with target results")
	}
	return nil
}

// ValidateResults binds outcomes to the selected collectors for both retained
// extraction manifests and public analysis results.
func (f HDRAnalysisFormats) ValidateResults(results []HDRFormatResult) error {
	selection, err := f.Normalize()
	if err != nil {
		return err
	}
	if len(results) != 2 || results[0].Format != HDRNativeHDR10Plus || results[1].Format != HDRNativeDolbyVision {
		return errors.New("HDR format outcomes must use deterministic order")
	}
	for i, result := range results {
		selected := selection == HDRAnalysisFormatsAuto || selection == HDRAnalysisFormatsBoth ||
			i == 0 && selection == HDRAnalysisFormatsHDR10Plus || i == 1 && selection == HDRAnalysisFormatsDolbyVision
		if result.Requested != (selected && selection != HDRAnalysisFormatsAuto) || selected == (result.State == HDRExtractionNotAnalyzed) {
			return errors.New("HDR outcome contradicts format selection")
		}
	}
	return nil
}

func NativeHDRAggregate(targets []HDRAnalysisTargetResult) StageStatus {
	success, all := false, len(targets) > 0
	for _, t := range targets {
		if t.Status == StageStatusCanceled {
			return StageStatusCanceled
		}
		if t.Status == StageStatusInterrupted {
			return StageStatusInterrupted
		}
		success = success || t.Status == StageStatusCompleted || t.Status == StageStatusPartial
		all = all && t.Status == StageStatusCompleted
	}
	if all {
		return StageStatusCompleted
	}
	if success {
		return StageStatusPartial
	}
	return StageStatusFailed
}

// DescriptionImages projects native plots and successful primary-audio images.
// Picture checks remain opt-in; reports are never hosted.
func (t HDRAnalysisTargetResult) DescriptionImages() []HDRTypedArtifact {
	if t.Artifact != nil && t.Status == StageStatusCompleted {
		return []HDRTypedArtifact{{
			ID:      t.Artifact.ID,
			Kind:    HDRArtifactHDR10PlusPlot,
			Format:  HDRNativeHDR10Plus,
			Width:   t.Artifact.Width,
			Height:  t.Artifact.Height,
			MIME:    "image/png",
			Profile: HDRAnalysisProfileVersion,
		}}
	}
	var images []HDRTypedArtifact
	for _, artifact := range t.Artifacts {
		if artifact.MIME != "image/png" {
			continue
		}
		if artifact.Kind.DecodedMode() == HDRDecodedAudio && t.successfulDecodedArtifact(artifact) {
			images = append(images, artifact)
			continue
		}
		if artifact.Kind.DecodedMode() != "" {
			continue
		}
		for _, format := range t.Formats {
			if format.Format == artifact.Format && format.State == HDRExtractionComplete {
				images = append(images, artifact)
				break
			}
		}
	}
	slices.SortStableFunc(images, func(a, b HDRTypedArtifact) int {
		return t.audioArtifactOrdinal(a.AudioTrackID) - t.audioArtifactOrdinal(b.AudioTrackID)
	})
	return images
}

// SelectableImages also includes successful picture checks for explicit selection.
func (t HDRAnalysisTargetResult) SelectableImages() []HDRTypedArtifact {
	images := t.DescriptionImages()
	for _, artifact := range t.Artifacts {
		mode := artifact.Kind.DecodedMode()
		if mode == "" || mode == HDRDecodedAudio || artifact.MIME != "image/png" {
			continue
		}
		if t.successfulDecodedArtifact(artifact) {
			images = append(images, artifact)
		}
	}
	return images
}

func (t HDRAnalysisTargetResult) successfulDecodedArtifact(artifact HDRTypedArtifact) bool {
	mode := artifact.Kind.DecodedMode()
	return mode != "" && slices.ContainsFunc(t.Decoded, func(decoded HDRDecodedResult) bool {
		if decoded.Mode != mode || decoded.Measured <= 0 || decoded.State != HDRDecodedComplete && decoded.State != HDRDecodedInconclusive ||
			!slices.Contains(decoded.Artifacts, artifact.ID) {
			return false
		}
		if artifact.Profile == HDRAnalysisProfileV3 && mode == HDRDecodedAudio {
			return slices.ContainsFunc(decoded.AudioTracks, func(track HDRAudioTrackResult) bool {
				return track.TrackID == artifact.AudioTrackID && track.PCMMeasured &&
					(track.State == HDRDecodedComplete || track.State == HDRDecodedInconclusive) && slices.Contains(track.Artifacts, artifact.ID)
			})
		}
		return true
	})
}

// AudioStatistics identifies retained text from a successful primary-audio result.
func (t HDRAnalysisTargetResult) AudioStatistics() (HDRTypedArtifact, bool) {
	for _, artifact := range t.AudioStatisticsEntries() {
		if artifact.AudioTrackID == "" {
			return artifact, true
		}
	}
	return HDRTypedArtifact{}, false
}

// AudioStatisticsEntries returns every successful selected track's statistics
// in source order. Legacy primary-only results retain their original ordering.
func (t HDRAnalysisTargetResult) AudioStatisticsEntries() []HDRTypedArtifact {
	var statistics []HDRTypedArtifact
	for _, artifact := range t.Artifacts {
		if artifact.Kind == HDRArtifactAudioStats && artifact.MIME == "text/plain" && t.successfulDecodedArtifact(artifact) {
			statistics = append(statistics, artifact)
		}
	}
	slices.SortStableFunc(statistics, func(a, b HDRTypedArtifact) int {
		return t.audioArtifactOrdinal(a.AudioTrackID) - t.audioArtifactOrdinal(b.AudioTrackID)
	})
	return statistics
}

func (t HDRAnalysisTargetResult) audioArtifactOrdinal(id string) int {
	for _, result := range t.Decoded {
		for _, track := range result.AudioTracks {
			if track.TrackID == id {
				return track.Ordinal
			}
		}
	}
	return 0
}

// CanIncludeImages reports usable description images or primary-audio statistics.
func (r HDRAnalysisResult) CanIncludeImages() bool {
	if !IsNativeHDRProfile(r.ProfileVersion) {
		return r.Status == StageStatusCompleted
	}
	if r.Status != StageStatusCompleted && r.Status != StageStatusPartial {
		return false
	}
	for _, target := range r.Targets {
		if len(target.SelectableImages()) > 0 || len(target.AudioStatisticsEntries()) > 0 {
			return true
		}
	}
	return false
}

// CanIncludeDefaultImages reflects native plots and primary-audio outputs.
// Picture-check images remain eligible through an explicit artifact selection.
func (r HDRAnalysisResult) CanIncludeDefaultImages() bool {
	if !IsNativeHDRProfile(r.ProfileVersion) {
		return r.CanIncludeImages()
	}
	if r.Status != StageStatusCompleted && r.Status != StageStatusPartial {
		return false
	}
	for _, target := range r.Targets {
		if len(target.DescriptionImages()) > 0 || len(target.AudioStatisticsEntries()) > 0 {
			return true
		}
	}
	return false
}
