// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	dv "github.com/Audionut/go-dovi-extract/extract"
	"github.com/Audionut/go-hdr10-plus/extract"

	"github.com/autobrr/upbrr/pkg/api"
)

const producerBytes = 128 << 20

// ExtractNativeFile owns the authorized local input and closes it on cancellation.
// The shared reader preserves read-ahead errors and reports one source traversal.
func (s *Session) ExtractNativeFile(ctx context.Context, path string, formats api.HDRAnalysisFormats) (*NativeExtraction, error) {
	return s.ExtractNativeFileWithRetained(ctx, path, formats, 0)
}

// ExtractNativeFileWithRetained charges another source against the same operation budget.
func (s *Session) ExtractNativeFileWithRetained(
	ctx context.Context,
	path string,
	formats api.HDRAnalysisFormats,
	otherRetainedBytes int64,
) (*NativeExtraction, error) {
	if otherRetainedBytes < 0 || otherRetainedBytes >= MKVRetainedBytes-producerBytes {
		return nil, extract.ErrResourceLimit
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open native HDR input: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect native HDR input: %w", err)
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = file.Close(); close(closed) })
	reader := newFileReader(ctx, file, info.Size(), s.service.logger, func(progress Progress) { s.service.reportProgress(ctx, progress) })
	result, readErr := s.extractNative(ctx, reader, formats, otherRetainedBytes)
	if !stop() {
		<-closed
	}
	closeErr := file.Close()
	if ctx.Err() != nil {
		readErr = ctx.Err()
	}
	if err := errors.Join(readErr, reader.source.readErr, closeErr); err != nil {
		if result != nil {
			result.Release()
		}
		return nil, fmt.Errorf("read native HDR input: %w", err)
	}
	return result, nil
}

// NativeExtraction retains independent parser outcomes from one physical stream.
// A nil format result with a nil error means that format was not requested.
// ScanComplete alone establishes normal source completion, never metadata validity.
type NativeExtraction struct {
	otherRetainedBytes int64
	HDR10Plus          *extract.Extraction
	DolbyVision        *dv.Extraction
	HDR10PlusError     error
	DolbyVisionError   error
	ScanComplete       bool
	TrackID            uint64
}

// Release relinquishes metadata storage. No aliases may be used afterward.
func (r *NativeExtraction) Release() {
	if r == nil {
		return
	}
	r.HDR10Plus.Release()
	r.DolbyVision.Release()
	r.HDR10Plus = nil
	r.DolbyVision = nil
}

// ExtractNative reads a caller-owned raw HEVC, Matroska, MP4 or TS/M2TS input under the held
// admission slot. The caller must close its input, including on cancellation.
// Individual parser failures remain in the result; shared producer failures are
// returned and invalidate both unfinished results. The caller owns Release.
func (s *Session) ExtractNative(ctx context.Context, reader io.ReadSeeker, formats api.HDRAnalysisFormats) (*NativeExtraction, error) {
	return s.extractNative(ctx, reader, formats, 0)
}

func (s *Session) extractNative(
	ctx context.Context,
	reader io.ReadSeeker,
	formats api.HDRAnalysisFormats,
	otherRetainedBytes int64,
) (*NativeExtraction, error) {
	if formats == "" {
		formats = api.HDRAnalysisFormatsAuto
	}
	if formats != api.HDRAnalysisFormatsAuto && formats != api.HDRAnalysisFormatsBoth && formats != api.HDRAnalysisFormatsHDR10Plus &&
		formats != api.HDRAnalysisFormatsDolbyVision {
		return nil, errors.New("HDR format selection is invalid")
	}
	fanout := &nativeConsumers{result: &NativeExtraction{otherRetainedBytes: otherRetainedBytes}}
	active := 2
	if formats == api.HDRAnalysisFormatsHDR10Plus || formats == api.HDRAnalysisFormatsDolbyVision {
		active = 1
	}
	allowance := (int64(MKVRetainedBytes-producerBytes) - otherRetainedBytes) / int64(active)
	if allowance < producerBytes {
		// The stream libraries' default index/block limits require at least
		// this much room. Insufficient remaining quota is a resource outcome,
		// not an invalid parser configuration.
		return nil, extract.ErrResourceLimit
	}
	s.service.progress(ctx, "reading_metadata")
	err := extract.Walk(
		ctx,
		reader,
		extract.Options{Limits: extract.Limits{MaxRetainedBytes: producerBytes, MaxFrames: MaxFrames}},
		func(info extract.SourceInfo) (extract.Consumer, error) {
			fanout.result.TrackID = info.Key.TrackID
			if formats != api.HDRAnalysisFormatsDolbyVision {
				fanout.hdr, fanout.result.HDR10PlusError = extract.NewStream(extract.StreamOptions{
					Key:              info.Key,
					Framing:          info.Framing,
					LengthSize:       info.LengthSize,
					CodecConfig:      info.CodecConfig,
					PresentationEdit: info.Edit,
					Limits:           extract.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: allowance},
				})
			}
			if formats != api.HDRAnalysisFormatsHDR10Plus {
				var edit *dv.PresentationEdit
				if info.Edit != nil {
					edit = &dv.PresentationEdit{
						Start: dv.Timestamp{
							Value:     info.Edit.Start.Value,
							Timescale: info.Edit.Start.Timescale,
							Valid:     info.Edit.Start.Valid,
						},
						Duration: dv.Timestamp{
							Value:     info.Edit.Duration.Value,
							Timescale: info.Edit.Duration.Timescale,
							Valid:     info.Edit.Duration.Valid,
						},
						Offset: dv.Timestamp{
							Value:     info.Edit.Offset.Value,
							Timescale: info.Edit.Offset.Timescale,
							Valid:     info.Edit.Offset.Valid,
						},
					}
				}
				framing := dv.AnnexB
				if info.Framing == extract.LengthPrefixed {
					framing = dv.LengthPrefixed
				}
				fanout.dv, fanout.result.DolbyVisionError = dv.NewStream(dv.StreamOptions{
					Key:              dv.StreamKey{TrackID: info.Key.TrackID},
					Framing:          framing,
					LengthSize:       info.LengthSize,
					CodecConfig:      info.CodecConfig,
					PresentationEdit: edit,
					Limits:           dv.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: allowance},
				})
			}
			if fanout.hdr == nil && fanout.dv == nil {
				return nil, errNoConsumers
			}
			return fanout, nil
		},
	)
	if err != nil {
		fanout.abort(err)
		if errors.Is(err, errNoConsumers) && ctx.Err() == nil {
			// All requested parsers rejected the stream. Preserve their typed
			// outcomes without treating this deliberate stop as source EOF.
			return fanout.result, nil
		}
		return fanout.result, fmt.Errorf("read shared HDR source: %w", err)
	}
	fanout.result.ScanComplete = true
	if fanout.hdr != nil {
		clip, err := fanout.hdr.Finish(ctx)
		if err == nil {
			fanout.result.HDR10Plus, err = extract.FinalizeStandalone(ctx, clip)
		}
		fanout.result.HDR10PlusError = err
	}
	if fanout.dv != nil {
		fanout.result.DolbyVision, fanout.result.DolbyVisionError = fanout.dv.Finish(ctx)
	}
	if err := ctx.Err(); err != nil {
		fanout.result.Release()
		return fanout.result, fmt.Errorf("finalize shared HDR source: %w", err)
	}
	return fanout.result, nil
}

var errNoConsumers = errors.New("no active HDR metadata collectors")

type nativeConsumers struct {
	hdr    *extract.Stream
	dv     *dv.Stream
	result *NativeExtraction
}

func (f *nativeConsumers) abort(cause error) {
	if f.hdr != nil {
		f.hdr.Abort(cause)
		f.result.HDR10PlusError = cause
		f.hdr = nil
	}
	if f.dv != nil {
		f.dv.Abort(cause)
		f.result.DolbyVisionError = cause
		f.dv = nil
	}
}

func (f *nativeConsumers) Push(ctx context.Context, chunk extract.Chunk) error {
	if f.hdr != nil {
		if err := f.hdr.Push(ctx, chunk); err != nil {
			f.hdr.Abort(err)
			f.result.HDR10PlusError = err
			f.hdr = nil
		}
	}
	if f.dv != nil {
		if err := f.pushDV(ctx, chunk); err != nil {
			f.dv.Abort(err)
			f.result.DolbyVisionError = err
			f.dv = nil
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("deliver HDR metadata: %w", err)
	}
	if f.hdr == nil && f.dv == nil {
		return errNoConsumers
	}
	return nil
}

func (f *nativeConsumers) Configure(ctx context.Context, data []byte) error {
	if f.hdr != nil {
		if err := f.hdr.Configure(ctx, data); err != nil {
			f.hdr.Abort(err)
			f.result.HDR10PlusError, f.hdr = err, nil
		}
	}
	if f.dv != nil {
		if err := f.dv.Configure(ctx, data); err != nil {
			f.dv.Abort(err)
			f.result.DolbyVisionError, f.dv = err, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("configure HDR collectors: %w", err)
	}
	if f.hdr == nil && f.dv == nil {
		return errNoConsumers
	}
	return nil
}

func (f *nativeConsumers) pushDV(ctx context.Context, chunk extract.Chunk) error {
	// The shared file producer delivers at most one marker per callback. Map
	// each library's enum explicitly; PES boundaries are never sample boundaries.
	var markers [1]dv.Marker
	if len(chunk.Markers) > len(markers) {
		return dv.ErrUnsupportedInput
	}
	if len(chunk.Markers) == 1 {
		m := chunk.Markers[0]
		var kind dv.MarkerKind
		switch m.Kind {
		case extract.SampleStart:
			kind = dv.SampleStart
		case extract.PESStart:
			kind = dv.PESStart
		case extract.SourceEnd:
			kind = dv.SourceEnd
		case extract.Discontinuity:
			kind = dv.Discontinuity
		default:
			return dv.ErrUnsupportedInput
		}
		markers[0] = dv.Marker{
			ESOffset:          m.ESOffset,
			Kind:              kind,
			SuppressOutput:    m.SuppressOutput,
			Modulo33:          m.Modulo33,
			HasSourcePosition: m.HasSourcePosition,
			PacketIndex:       m.PacketIndex,
			SourceOffset:      m.SourceOffset,
			PESHeader:         m.PESHeader,
			PTS: dv.Timestamp{
				Value:     m.PTS.Value,
				Timescale: m.PTS.Timescale,
				Valid:     m.PTS.Valid,
			},
			DTS: dv.Timestamp{
				Value:     m.DTS.Value,
				Timescale: m.DTS.Timescale,
				Valid:     m.DTS.Valid,
			},
		}
	}
	if err := f.dv.Push(ctx, dv.Chunk{
		Data:     chunk.Data,
		ESOffset: chunk.ESOffset,
		Markers:  markers[:len(chunk.Markers)],
	}); err != nil {
		return fmt.Errorf("collect Dolby Vision metadata: %w", err)
	}
	return nil
}
