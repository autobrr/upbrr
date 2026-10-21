// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	dv "github.com/Audionut/go-dovi-extract/extract"
	"github.com/Audionut/go-hdr10-plus/extract"
	"github.com/at-wat/ebml-go"

	"github.com/autobrr/upbrr/pkg/api"
)

// ffmpegMetadata consumes FFmpeg's live, video-only Matroska output. The
// existing EBML library owns container and lacing syntax; the existing native
// collectors own HEVC, presentation reordering, RPU and HDR10+ validation.
func ffmpegMetadata(ctx context.Context, reader io.Reader, otherBytes int64, progress func(uint64)) (*NativeExtraction, error) {
	result := &NativeExtraction{otherRetainedBytes: otherBytes}
	fanout := &nativeConsumers{result: result}
	defer func() {
		if !result.ScanComplete {
			fanout.abort(extract.ErrIncomplete)
		}
	}()
	var document struct {
		EBML struct {
			DocType string `ebml:"EBMLDocType"`
		}
		Segment struct {
			Info   struct{ TimecodeScale uint64 }
			Tracks struct {
				TrackEntry struct {
					TrackNumber  uint64
					TrackType    uint64
					CodecID      string
					CodecPrivate []byte
				}
			}
			Cluster struct {
				Timecode    uint64
				SimpleBlock ebml.Block
				BlockGroup  struct{ Block ebml.Block }
			}
		}
	}
	var consumerErr error
	var offset uint64
	var lastProgress time.Time
	tracks, trackEntries, clusters := 0, 0, 0
	input := &metadataPipeReader{
		ctx:     ctx,
		reader:  reader,
		failure: &consumerErr,
	}
	hook := func(element *ebml.Element) {
		if consumerErr != nil {
			return
		}
		switch element.Name {
		case "TrackEntry":
			trackEntries++
		case "Tracks":
			tracks++
			t := document.Segment.Tracks.TrackEntry
			if tracks != 1 || trackEntries != 1 || t.TrackNumber == 0 || t.TrackType != 1 || t.CodecID != "V_MPEGH/ISO/HEVC" || len(t.CodecPrivate) < 23 ||
				len(t.CodecPrivate) > 1<<20 {
				consumerErr = extract.ErrUnsupportedInput
				return
			}
			result.TrackID = t.TrackNumber
			allowance := (int64(MaxRestoreBytes-producerBytes) - otherBytes) / 2
			if allowance < producerBytes {
				consumerErr = extract.ErrResourceLimit
				return
			}
			lengthSize := int(t.CodecPrivate[21]&3) + 1
			fanout.hdr, result.HDR10PlusError = extract.NewStream(extract.StreamOptions{
				Key:         extract.StreamKey{TrackID: t.TrackNumber},
				Framing:     extract.LengthPrefixed,
				LengthSize:  lengthSize,
				CodecConfig: t.CodecPrivate,
				Limits:      extract.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: allowance},
			})
			fanout.dv, result.DolbyVisionError = dv.NewStream(dv.StreamOptions{
				Key:         dv.StreamKey{TrackID: t.TrackNumber},
				Framing:     dv.LengthPrefixed,
				LengthSize:  lengthSize,
				CodecConfig: t.CodecPrivate,
				Limits:      dv.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: allowance},
			})
		case "SimpleBlock", "Block":
			block, ok := element.Value.(ebml.Block)
			if !ok || tracks != 1 || block.TrackNumber != result.TrackID || len(block.Data) != 1 || block.Invisible {
				consumerErr = extract.ErrUnsupportedInput
				return
			}
			scale := document.Segment.Info.TimecodeScale
			if scale == 0 {
				scale = 1_000_000 // Matroska's specified default, also used by FFmpeg.
			}
			clock := document.Segment.Cluster.Timecode
			if clock > math.MaxInt64 || scale > 1_000_000_000 || int64(clock) > math.MaxInt64-int64(max(0, int(block.Timecode))) {
				consumerErr = extract.ErrInvalidBitstream
				return
			}
			timestamp := int64(clock) + int64(block.Timecode)
			if timestamp > math.MaxInt64/int64(scale) || timestamp < math.MinInt64/int64(scale) {
				consumerErr = extract.ErrInvalidBitstream
				return
			}
			data := block.Data[0]
			marker := extract.Marker{
				Kind:     extract.SampleStart,
				ESOffset: offset,
				PTS: extract.Timestamp{
					Value:     timestamp * int64(scale),
					Timescale: 1_000_000_000,
					Valid:     true,
				},
			}
			err := fanout.Push(ctx, extract.Chunk{
				Data:     data,
				ESOffset: offset,
				Markers:  []extract.Marker{marker},
			})
			if err != nil && !errors.Is(err, errNoConsumers) {
				consumerErr = err
			}
			offset += uint64(len(data))
			if progress != nil && time.Since(lastProgress) >= 2*time.Second {
				lastProgress = time.Now()
				progress(offset)
			}
		case "Cluster":
			clusters++
		}
	}
	err := ebml.Unmarshal(input, &document, ebml.WithElementReadHooks(hook), ebml.WithMaxLeafElementSize(64<<20))
	if err := errors.Join(err, consumerErr); err != nil {
		return result, fmt.Errorf("read shared metadata stream: %w", err)
	}
	if document.EBML.DocType != "matroska" || tracks != 1 || clusters == 0 {
		return result, extract.ErrIncomplete
	}
	result.ScanComplete = true
	if fanout.hdr != nil {
		clip, err := fanout.hdr.Finish(ctx)
		if err == nil {
			result.HDR10Plus, err = extract.FinalizeStandalone(ctx, clip)
		}
		result.HDR10PlusError = err
	}
	if fanout.dv != nil {
		result.DolbyVision, result.DolbyVisionError = fanout.dv.Finish(ctx)
	}
	return result, nil
}

type metadataPipeReader struct {
	ctx     context.Context
	reader  io.Reader
	failure *error
}

func (r *metadataPipeReader) Read(value []byte) (int, error) {
	if err := errors.Join(r.ctx.Err(), *r.failure); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(value)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("read metadata pipe: %w", err)
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	return n, nil
}

func selectAutomaticFormats(native *NativeExtraction, formats api.HDRAnalysisFormats) {
	switch formats {
	case api.HDRAnalysisFormatsHDR10Plus:
		native.DolbyVision.Release()
		native.DolbyVision, native.DolbyVisionError = nil, nil
	case api.HDRAnalysisFormatsDolbyVision:
		native.HDR10Plus.Release()
		native.HDR10Plus, native.HDR10PlusError = nil, nil
	case api.HDRAnalysisFormatsAuto, api.HDRAnalysisFormatsBoth:
	}
}
