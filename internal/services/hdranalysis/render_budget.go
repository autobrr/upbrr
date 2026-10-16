// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"fmt"

	hdr "github.com/Audionut/go-hdr10-plus"
	"github.com/Audionut/go-hdr10-plus/extract"

	"github.com/autobrr/upbrr/pkg/api"
)

const nativeRenderBytes = 64 << 20

// hdrPlotModelBytes bounds PlotMetadata's deep copy for the pinned library:
// a 56-byte Frame, a separately allocated three-component MaxSCL (16 bytes),
// and distribution values rounded up to a 16-byte allocation boundary.
func hdrPlotModelBytes(extraction *extract.Extraction) (int64, error) {
	if extraction == nil || len(extraction.Frames) == 0 || len(extraction.Frames) > MaxFrames {
		return 0, extract.ErrResourceLimit
	}
	bytes := int64(len(extraction.Frames)) * 72
	for _, frame := range extraction.Frames {
		if uint64(frame.PayloadIndex) >= uint64(len(extraction.Payloads)) {
			return 0, hdr.ErrInvalidMetadata
		}
		count := len(extraction.Payloads[frame.PayloadIndex].Distributions)
		if count > 15 {
			return 0, hdr.ErrInvalidMetadata
		}
		bytes += int64((count*4 + 15) / 16 * 16)
	}
	return bytes, nil
}

func hdrRenderBytes(extraction *extract.Extraction) (int64, error) {
	model, err := hdrPlotModelBytes(extraction)
	if err != nil {
		return 0, err
	}
	// 40 MiB covers the 3000x1200 RGBA canvas, both 2800x940 vector
	// buffers, font/glyph scratch and streaming PNG/flate buffers. Caption
	// validation occurs before rendering and fits this same scratch allowance.
	// The renderer additionally allocates two float64 samples per frame.
	return max(nativeRenderBytes, model+int64(len(extraction.Frames))*16+40<<20), nil
}

func validateHDRRenderBudget(extraction *extract.Extraction) error {
	renderBytes, err := hdrRenderBytes(extraction)
	if err != nil {
		return err
	}
	if renderBytes > MaxRestoreBytes || extraction.RetainedBytes() > MaxRestoreBytes-renderBytes {
		return extract.ErrResourceLimit
	}
	return nil
}

// RenderNative charges both retained formats, other live results and rendering
// storage against the operation budget before either renderer allocates a copy.
func (s *Session) RenderNative(ctx context.Context, evidence *NativeEvidence, format api.HDRNativeFormat,
	peak api.HDRPeakSource, title, target string, otherRetainedBytes int64,
) error {
	used, err := evidence.AccountedBytes()
	if err != nil {
		return err
	}
	renderBytes := int64(nativeRenderBytes)
	if evidence != nil && format == api.HDRNativeHDR10Plus {
		renderBytes, err = hdrRenderBytes(evidence.HDR10Plus)
		if err != nil {
			return err
		}
	}
	if evidence == nil || renderBytes > MaxRestoreBytes || otherRetainedBytes < 0 || otherRetainedBytes > MaxRestoreBytes-renderBytes ||
		used > MaxRestoreBytes-renderBytes-otherRetainedBytes {
		return extract.ErrResourceLimit
	}
	switch format {
	case api.HDRNativeHDR10Plus:
		return s.service.renderWithBinding(ctx, evidence.HDR10Plus, peak, title, target, true)
	case api.HDRNativeDolbyVision:
		return s.renderDolbyVision(ctx, evidence.DolbyVision, title, target)
	default:
		return fmt.Errorf("render native HDR format: %w", hdr.ErrInvalidOptions)
	}
}
