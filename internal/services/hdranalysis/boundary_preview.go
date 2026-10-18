// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"os"
	"slices"

	"github.com/autobrr/upbrr/pkg/api"
)

// colourBoundaryPairs runs only after luma scoring has selected the displayed pairs.
// Each replay is independently prepared, proved and charged against the same VCL cap.
func colourBoundaryPairs(ctx context.Context, decoder pictureDecoder, input DecodedInput, plan *BoundaryPlan,
	options api.HDRDecodedOptions, directory string, otherBytes int64,
) {
	wanted := make(map[int]bool)
	for _, format := range plan.report.Formats {
		for _, thumb := range format.Thumbnails {
			wanted[thumb.Index] = true
		}
	}
	for i := range plan.report.Windows {
		window := &plan.report.Windows[i]
		var indices []int
		for index := window.First; index <= window.Last; index++ {
			if wanted[index] {
				indices = append(indices, index)
			}
		}
		if len(indices) == 0 || ctx.Err() != nil {
			continue
		}
		// One selected window at a time keeps both scratch storage and colour work bounded.
		path, proof, err := decoder.prepareBoundaryClip(ctx, input, plan, *window, directory)
		prepare := BoundaryInvocation{
			RequestedThreads: options.VideoDecoderThreads,
			ReservedBytes:    boundaryPreparationBytes,
			State:            "unsupported",
		}
		if err != nil {
			window.Invocations = append(window.Invocations, prepare)
			window.Reason = "colour_clip_unavailable"
			continue
		}
		prepare.State, prepare.ClipSHA256, prepare.CompressedBytes, prepare.NALUnits = "complete", proof.sha256, proof.bytes, proof.nals
		window.Invocations = append(window.Invocations, prepare)
		_, threads, reservation, err := boundaryConcurrency(proof.geometry, options.VideoDecoderThreads, otherBytes)
		if err != nil || proof.vcl > boundaryMaxVCL-plan.report.ChargedVCL {
			_ = os.Remove(path)
			window.Reason = "colour_preview_resource_limit"
			continue
		}
		plan.report.ChargedVCL += proof.vcl
		result := decoder.decodeBoundaryPictures(ctx, input, plan, *window, path, proof, options.VideoDecoderThreads, threads, reservation, indices)
		_ = os.Remove(path)
		if result.err == nil && !slices.Equal(result.invocation.PTS, window.Invocations[1].PTS) {
			result.invocation.State = "failed"
		}
		window.Invocations = append(window.Invocations, result.invocation)
		if result.invocation.State != "complete" {
			window.Reason = "colour_preview_unproved"
			continue
		}
		for f := range plan.report.Formats {
			for t := range plan.report.Formats[f].Thumbnails {
				thumb := &plan.report.Formats[f].Thumbnails[t]
				if rgb, ok := result.previews[thumb.Index]; ok {
					thumb.RGB = slices.Clone(rgb)
					delete(wanted, thumb.Index)
				}
			}
		}
	}
}
