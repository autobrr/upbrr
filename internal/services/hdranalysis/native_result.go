// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"strconv"

	dovi "github.com/Audionut/go-dovi-extract"
	"github.com/Audionut/go-hdr10-plus/extract"

	"github.com/autobrr/upbrr/pkg/api"
)

// NativeEvidence owns the validated metadata used for persistence and rendering.
// Public outcomes contain no private paths or dependency error strings.
type NativeEvidence struct {
	reportSummary      *nativeReportDVSummary
	otherRetainedBytes int64
	HDR10Plus          *extract.Extraction
	Timeline           []TimelineItem
	DolbyVision        *dovi.Analysis
	Results            []api.HDRFormatResult
}

// RetainReportSummary preserves the compact structural export before releasing
// native parser storage for a later picture-analysis phase.
func (e *NativeEvidence) RetainReportSummary() error {
	if e.DolbyVision == nil {
		return nil
	}
	summary, err := e.summarizeDV()
	if err != nil {
		return err
	}
	e.reportSummary = &nativeReportDVSummary{Summary: summary, TrackID: strconv.FormatUint(summary.TrackID, 10)}
	return nil
}

// RetainedBytes returns a conservative bound for simultaneously held evidence.
func (e *NativeEvidence) RetainedBytes() (int64, error) {
	if e == nil {
		return 0, nil
	}
	used := e.HDR10Plus.RetainedBytes() + int64(len(e.Timeline))*4096
	if e.DolbyVision != nil {
		bytes, err := e.DolbyVision.RetainedBytes(dovi.AnalysisOptions{MaxRecords: MaxFrames, MaxBytes: MaxRestoreBytes})
		if err != nil {
			return 0, fmt.Errorf("bound Dolby Vision evidence: %w", err)
		}
		used += bytes
	}
	return used, nil
}

// AccountedBytes also includes other live sources reserved when this evidence
// was extracted or restored. Paired decoders count that reservation only on A;
// B's reservation already includes A and must not be added a second time.
func (e *NativeEvidence) AccountedBytes() (int64, error) {
	used, err := e.RetainedBytes()
	if err != nil || e == nil {
		return used, err
	}
	if e.otherRetainedBytes < 0 || e.otherRetainedBytes > MaxRestoreBytes || used > MaxRestoreBytes-e.otherRetainedBytes {
		return 0, extract.ErrResourceLimit
	}
	return used + e.otherRetainedBytes, nil
}

// Release drops parsed HDR10+ and Dolby Vision storage. Timeline and summary
// observations remain; callers must stop using aliases to the released models.
func (e *NativeEvidence) Release() {
	if e == nil {
		return
	}
	e.HDR10Plus.Release()
	e.HDR10Plus = nil
	e.DolbyVision = nil
}

// FinishSharedCapture replaces the completed capture's scratch reservation with
// the caller's remaining live-source reservation. Parser storage must already
// be released; call only after every capture/audio consumer has joined.
func (e *NativeEvidence) FinishSharedCapture(otherBytes int64) error {
	if e == nil || e.HDR10Plus != nil || e.DolbyVision != nil || otherBytes < 0 || otherBytes > MaxRestoreBytes {
		return extract.ErrResourceLimit
	}
	e.otherRetainedBytes = otherBytes
	return nil
}

// SelectFormats projects a complete capture onto an operation without reading
// its source. A capture missing a requested parser cannot satisfy that request.
func (e *NativeEvidence) SelectFormats(formats api.HDRAnalysisFormats) error {
	formats, err := formats.Normalize()
	if err != nil {
		return fmt.Errorf("select retained native formats: %w", err)
	}
	if len(e.Results) != 2 {
		return errors.New("native capture has no complete outcomes")
	}
	for i := range e.Results {
		needed := i == 0 && formats != api.HDRAnalysisFormatsDolbyVision || i == 1 && formats != api.HDRAnalysisFormatsHDR10Plus
		if needed && e.Results[i].State == api.HDRExtractionNotAnalyzed {
			return errors.New("native capture lacks requested format capability")
		}
	}
	for i := range e.Results {
		needed := i == 0 && formats != api.HDRAnalysisFormatsDolbyVision || i == 1 && formats != api.HDRAnalysisFormatsHDR10Plus
		if !needed {
			e.Results[i] = api.HDRFormatResult{
				Format:     e.Results[i].Format,
				TrackID:    e.Results[i].TrackID,
				Advertised: e.Results[i].Advertised,
				State:      api.HDRExtractionNotAnalyzed,
				Coverage:   api.HDRCoverageUnknown,
				Checks:     []api.HDRCheckResult{},
			}
			if i == 0 {
				e.HDR10Plus.Release()
				e.HDR10Plus = nil
			} else {
				e.DolbyVision = nil
			}
		}
		e.Results[i].Requested = formats == api.HDRAnalysisFormatsBoth || i == 0 && formats == api.HDRAnalysisFormatsHDR10Plus ||
			i == 1 && formats == api.HDRAnalysisFormatsDolbyVision
	}
	return nil
}

// ValidateSummary recomputes observations from the full validated sections.
func (e *NativeEvidence) ValidateSummary() error {
	if len(e.Results) != 2 {
		return errors.New("native HDR summary is missing")
	}
	h, d := e.Results[0], e.Results[1]
	if e.HDR10Plus != nil {
		if h.Frames != len(e.HDR10Plus.Frames) || h.Retained != len(e.HDR10Plus.Payloads) || h.Scenes != len(e.HDR10Plus.SceneStarts) ||
			h.Profile != e.HDR10Plus.Profile {
			return errors.New("HDR10+ summary differs from retained metadata")
		}
	}
	if e.DolbyVision != nil {
		summary, err := e.summarizeDV()
		if err != nil {
			return fmt.Errorf("recompute Dolby Vision summary: %w", err)
		}
		counts, err := nativeSummaryCounts(summary.Counts)
		if err != nil {
			return err
		}
		if d.Frames != counts[0] || d.Retained != counts[1] || d.Observed != counts[2] || d.Suppressed != counts[3] || d.Missing != counts[4] ||
			d.L1Samples != counts[5] ||
			d.Coverage != api.HDRCoverage(summary.Coverage) ||
			d.Profile != nativeDVProfiles(summary) ||
			d.Scenes != len(summary.SceneMarkers) {
			return errors.New("dolby vision summary differs from retained metadata")
		}
	}
	return nil
}

// Evidence converts the DV model with an allowance that overlaps both collector
// partitions inside the 3 GiB ceiling. It transfers the retained HDR result.
func (r *NativeExtraction) Evidence(formats api.HDRAnalysisFormats, advertised api.HDRSourceEvidence) (*NativeEvidence, error) {
	formats, err := formats.Normalize()
	if err != nil {
		return nil, fmt.Errorf("normalize native formats: %w", err)
	}
	e := &NativeEvidence{HDR10Plus: r.HDR10Plus, otherRetainedBytes: r.otherRetainedBytes}
	r.HDR10Plus = nil
	h := nativeFormatOutcome(api.HDRNativeHDR10Plus, r.HDR10PlusError, r.TrackID)
	d := nativeFormatOutcome(api.HDRNativeDolbyVision, r.DolbyVisionError, r.TrackID)
	h.Advertised, d.Advertised = advertised.HDR10Plus, advertised.DolbyVision
	h.Requested = formats == api.HDRAnalysisFormatsBoth || formats == api.HDRAnalysisFormatsHDR10Plus
	d.Requested = formats == api.HDRAnalysisFormatsBoth || formats == api.HDRAnalysisFormatsDolbyVision
	if e.HDR10Plus != nil {
		h.State, h.Coverage = api.HDRExtractionComplete, api.HDRCoverageComplete
		h.Frames, h.Retained, h.Observed, h.Scenes, h.Profile = len(
			e.HDR10Plus.Frames,
		), len(
			e.HDR10Plus.Payloads,
		), len(
			e.HDR10Plus.Payloads,
		), len(
			e.HDR10Plus.SceneStarts,
		), e.HDR10Plus.Profile
	}
	if r.DolbyVision != nil {
		// Charge simultaneously retained results before allocating a conversion
		// copy. The DV library includes temporary flattening in its copy budget.
		remaining := int64(MaxRestoreBytes) - e.HDR10Plus.RetainedBytes() - r.DolbyVision.RetainedBytes() - r.otherRetainedBytes
		if remaining <= 0 {
			err = dovi.ErrLimit
		} else {
			e.DolbyVision, err = r.DolbyVision.Analysis(dovi.AnalysisOptions{MaxRecords: MaxFrames, MaxBytes: remaining})
		}
		if err != nil {
			d = nativeFormatOutcome(api.HDRNativeDolbyVision, err, r.TrackID)
			d.Advertised = advertised.DolbyVision
			d.Requested = formats == api.HDRAnalysisFormatsBoth || formats == api.HDRAnalysisFormatsDolbyVision
		} else {
			r.DolbyVision.Release()
			r.DolbyVision = nil
			summary, summaryErr := e.summarizeDV()
			counts, countErr := nativeSummaryCounts(summary.Counts)
			if summaryErr = errors.Join(summaryErr, countErr); summaryErr != nil {
				e.DolbyVision = nil
				d = nativeFormatOutcome(api.HDRNativeDolbyVision, summaryErr, r.TrackID)
				d.Advertised = advertised.DolbyVision
				d.Requested = formats == api.HDRAnalysisFormatsBoth || formats == api.HDRAnalysisFormatsDolbyVision
			} else {
				d.State, d.Coverage = api.HDRExtractionComplete, api.HDRCoverage(summary.Coverage)
				d.Frames, d.Retained, d.Observed, d.Suppressed, d.Missing, d.L1Samples = counts[0], counts[1], counts[2], counts[3], counts[4], counts[5]
				d.Scenes, d.Profile = len(summary.SceneMarkers), nativeDVProfiles(summary)
				d.Checks = append(d.Checks, nativeDVChecks(e.DolbyVision, summary)...)
				if summary.Coverage == dovi.CoverageAbsent {
					d.State = api.HDRExtractionAbsent
				}
				if d.State == api.HDRExtractionComplete && d.L1Samples == 0 {
					d.Checks = append(d.Checks, api.HDRCheckResult{
						Code:    "l1_unavailable",
						State:   api.HDRCheckUnsupported,
						Message: "No L1 metadata is present; structural evidence remains available.",
					})
				}
				if summary.Coverage == dovi.CoveragePartial {
					d.Checks = append(d.Checks, api.HDRCheckResult{
						Code:    "sparse_metadata",
						State:   api.HDRCheckWarning,
						Message: "Some output pictures have no Dolby Vision metadata.",
					})
				}
			}
		}
		r.DolbyVision.Release()
		r.DolbyVision = nil
	}
	for _, result := range []*api.HDRFormatResult{&h, &d} {
		if result.State == api.HDRExtractionComplete {
			result.Checks = append(result.Checks, api.HDRCheckResult{
				Code:    "native_structure",
				State:   api.HDRCheckPass,
				Message: "Native parsing completed. This does not establish picture synchronization or grade matching.",
			})
		}
		if result.Advertised && result.State == api.HDRExtractionAbsent {
			result.Checks = append(result.Checks, api.HDRCheckResult{
				Code:    "advertised_metadata_absent",
				State:   api.HDRCheckWarning,
				Message: "The complete native scan did not find the metadata advertised during preparation.",
			})
		}
	}
	e.Results = []api.HDRFormatResult{h, d}
	return e, nil
}

func (e *NativeEvidence) summarizeDV() (dovi.Summary, error) {
	used, err := e.DolbyVision.RetainedBytes(dovi.AnalysisOptions{MaxRecords: MaxFrames, MaxBytes: MaxRestoreBytes})
	if err != nil {
		return dovi.Summary{}, fmt.Errorf("account Dolby Vision evidence: %w", err)
	}
	remaining := int64(MaxRestoreBytes) - used - e.HDR10Plus.RetainedBytes() - e.otherRetainedBytes
	if remaining <= 0 {
		return dovi.Summary{}, dovi.ErrLimit
	}
	summary, err := dovi.Summarize(e.DolbyVision, dovi.SummaryOptions{MaxRecords: MaxFrames, MaxBytes: remaining})
	if err != nil {
		return dovi.Summary{}, fmt.Errorf("summarize bounded Dolby Vision evidence: %w", err)
	}
	return summary, nil
}

func nativeFormatOutcome(format api.HDRNativeFormat, err error, track uint64) api.HDRFormatResult {
	r := api.HDRFormatResult{
		Format:   format,
		State:    api.HDRExtractionNotAnalyzed,
		Coverage: api.HDRCoverageUnknown,
		TrackID:  strconv.FormatUint(track, 10),
		Checks:   []api.HDRCheckResult{},
	}
	if err == nil {
		return r
	}
	if errors.Is(err, extract.ErrNoMetadata) {
		r.State, r.Coverage = api.HDRExtractionAbsent, api.HDRCoverageAbsent
		return r
	}
	r.State = api.HDRExtractionFailed
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		r.State = api.HDRExtractionInterrupted
	case errors.Is(err, context.Canceled):
		r.State = api.HDRExtractionCanceled
	case errors.Is(err, extract.ErrUnsupportedInput), errors.Is(err, extract.ErrUnsupportedMetadata), errors.Is(err, dovi.ErrUnsupported):
		r.State = api.HDRExtractionUnsupported
	}
	failure, _ := api.AsHDRAnalysisFailure(Classify(err))
	r.Failure = &failure
	return r
}

func (s *Session) renderDolbyVision(ctx context.Context, analysis *dovi.Analysis, title, target string) error {
	s.service.progress(ctx, "rendering_dolby_vision")
	image, err := dovi.Render(ctx, analysis, dovi.PlotOptions{
		Title:         title,
		MaxBytes:      48 << 20,
		MaxInputBytes: MaxRestoreBytes,
		MaxRecords:    MaxFrames,
	})
	if err != nil {
		return fmt.Errorf("render Dolby Vision L1: %w", err)
	}
	s.service.progress(ctx, "saving_dolby_vision")
	if err := PublishFile(ctx, target, func(out io.Writer) error { return png.Encode(out, image) }); err != nil {
		return fmt.Errorf("publish Dolby Vision L1: %w", err)
	}
	return nil
}

func nativeSummaryCounts(counts dovi.Counts) ([6]int, error) {
	var result [6]int
	for i, value := range [6]uint64{counts.OutputPictures, counts.RetainedRPUs, counts.ObservedRPUs, counts.SuppressedRPUs, counts.MissingPictures, counts.L1Samples} {
		if value > MaxFrames {
			return result, dovi.ErrLimit
		}
		result[i] = int(value)
	}
	return result, nil
}
