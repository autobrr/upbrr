// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	dv "github.com/Audionut/go-dovi-extract/extract"
	"github.com/Audionut/go-hdr10-plus/extract"
	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"

	"github.com/autobrr/upbrr/pkg/api"
)

// NativeDiscOutcome retains independent metadata outcomes from one BDInfo run.
// Release must follow consumption of every requested playlist.
type NativeDiscOutcome struct {
	*bridge.Outcome
	Formats            api.HDRAnalysisFormats
	DolbyVision        []DVPlaylistResult
	OmittedDVPlaylists bool
}

// DVPlaylistResult retains one selected playlist's extraction or independent parser failure.
type DVPlaylistResult struct {
	Name       string
	Extraction *dv.Extraction
	Err        error
}

// Release discards remaining playlist results and returns their retained charges.
// Callers must stop using aliases to those results; repeated release is harmless.
func (o *NativeDiscOutcome) Release() {
	if o == nil {
		return
	}
	if o.Outcome != nil {
		for _, p := range o.Playlists {
			p.Extraction.Release()
		}
	}
	for _, p := range o.DolbyVision {
		p.Extraction.Release()
	}
	o.DolbyVision = nil
	o.Outcome = nil
}

// RetainedBytes includes remaining playlist extractions and the producer/report
// allowance while this outcome still owns results from a completed scan.
func (o *NativeDiscOutcome) RetainedBytes() int64 {
	if o == nil || o.Outcome == nil {
		return 0
	}
	var used int64
	for _, playlist := range o.Playlists {
		used += playlist.Extraction.RetainedBytes()
	}
	for _, playlist := range o.DolbyVision {
		used += playlist.Extraction.RetainedBytes()
	}
	return used + producerBytes
}

// ScanNativeDisc delivers each physical primary HEVC stream synchronously to
// both selected parsers. Parser failures do not suppress the ordinary report or
// stop the other parser. No collector opens or rereads a source file.
func (s *Session) ScanNativeDisc(ctx context.Context, options bd.Options, formats api.HDRAnalysisFormats,
	otherRetainedBytes int64, playlistNames ...string,
) (*NativeDiscOutcome, error) {
	formats, err := formats.Normalize()
	if err != nil {
		return nil, fmt.Errorf("normalize disc HDR formats: %w", err)
	}
	if options.VideoConsumer != nil {
		return nil, errors.New("native disc scan already has a video consumer")
	}
	if len(playlistNames) > api.HDRAnalysisMaxTargets {
		return nil, errors.New("too many native disc playlists")
	}
	selected := make(map[string]bool, len(playlistNames))
	for _, name := range playlistNames {
		name, err = api.NormalizeHDRPlaylist(name)
		if err != nil || selected[name] {
			return nil, errors.New("native disc playlists must be valid and unique")
		}
		selected[name] = true
	}
	active := 2
	if formats == api.HDRAnalysisFormatsHDR10Plus || formats == api.HDRAnalysisFormatsDolbyVision {
		active = 1
	}
	if otherRetainedBytes < 0 || otherRetainedBytes > DiscScanRetainedBytes-producerBytes {
		return nil, extract.ErrResourceLimit
	}
	allowance := (int64(DiscScanRetainedBytes-producerBytes) - otherRetainedBytes) / int64(active)
	if allowance < producerBytes {
		return nil, extract.ErrResourceLimit
	}
	var hdr *bridge.Collector
	if formats != api.HDRAnalysisFormatsDolbyVision {
		hdr, err = bridge.NewCollector(ctx, bridge.Options{
			BDInfo:               options,
			Limits:               extract.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: allowance},
			MaxScanRetainedBytes: allowance,
		})
		if err != nil {
			return nil, fmt.Errorf("prepare HDR disc collector: %w", err)
		}
		defer hdr.Release()
	}
	budget, err := dv.NewMemoryBudget(allowance)
	if err != nil {
		return nil, fmt.Errorf("prepare Dolby Vision disc budget: %w", err)
	}
	collectors := make(map[dv.StreamKey]*discConsumers)
	var mu sync.Mutex
	exhausted := false
	defer func() {
		for _, c := range collectors {
			c.dv.Abort(dv.ErrIncomplete)
			c.clip.Release()
		}
	}()
	options.VideoConsumer = func(callCtx context.Context, info video.StreamInfo) (video.Consumer, error) {
		if info.Codec != video.HEVC {
			return nil, nil
		}
		primary := false
		for _, occurrence := range info.Occurrences {
			primary = primary || occurrence.Mapping.Role == video.Primary && occurrence.Mapping.EntryType == 1
		}
		if !primary {
			return nil, nil
		}
		key := dv.StreamKey{
			SourceID: options.Path,
			ClipID:   info.Source.Path,
			TrackID:  uint64(info.PID),
		}
		mu.Lock()
		defer mu.Unlock()
		if _, exists := collectors[key]; exists {
			return nil, fmt.Errorf("duplicate native disc stream: %w", dv.ErrInvalidBitstream)
		}
		c := &discConsumers{ctx: ctx, budget: budget}
		registered := false
		if hdr != nil {
			c.hdr, c.hdrErr = hdr.Consumer(callCtx, info)
		}
		if formats != api.HDRAnalysisFormatsHDR10Plus {
			// Registered stream outcomes remain charged until the session ends.
			_, c.dvErr = budget.Reserve(4096 + int64(len(key.SourceID)+len(key.ClipID)))
			if c.dvErr == nil {
				registered = true
				c.dv, c.dvErr = dv.NewStream(dv.StreamOptions{
					Key:    key,
					Budget: budget,
					Limits: dv.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: allowance},
				})
			}
			if c.dvErr != nil {
				exhausted = exhausted || errors.Is(c.dvErr, dv.ErrResourceLimit)
			}
		}
		// Failure to reserve a registry entry cannot grow the failure map.
		if registered {
			collectors[key] = c
		}
		if c.hdr == nil && c.dv == nil {
			return nil, errors.Join(c.hdrErr, c.dvErr)
		}
		return c, nil
	}
	options.IncludeTimeline = true
	s.service.progress(ctx, "reading_metadata")
	report, scanErr := bd.Run(ctx, options)
	out := &NativeDiscOutcome{Outcome: &bridge.Outcome{Report: &report}, Formats: formats}
	// Bound retained assemblies to the requested targets while preserving the
	// complete ordinary report and its source collection evidence.
	assemblyReport := report
	assemblyReport.Timelines = nil
	for _, timeline := range report.Timelines {
		if len(selected) > 0 && !selected[strings.ToUpper(timeline.Name)] {
			continue
		}
		if len(assemblyReport.Timelines) == api.HDRAnalysisMaxTargets {
			out.OmittedDVPlaylists = true
			break
		}
		assemblyReport.Timelines = append(assemblyReport.Timelines, timeline)
	}
	if hdr != nil {
		out.Outcome, err = hdr.Complete(&assemblyReport)
		if err != nil {
			return nil, fmt.Errorf("complete HDR disc collection: %w", err)
		}
		out.Report = &report
	}
	if formats != api.HDRAnalysisFormatsHDR10Plus {
		for _, timeline := range assemblyReport.Timelines {
			result := DVPlaylistResult{Name: timeline.Name}
			result.Extraction, result.Err = assembleDiscDV(ctx, options.Path, timeline, collectors, exhausted, budget, allowance)
			out.DolbyVision = append(out.DolbyVision, result)
		}
	}
	if scanErr != nil {
		return out, fmt.Errorf("native disc scan: %w", scanErr)
	}
	return out, nil
}

type discConsumers struct {
	ctx    context.Context
	hdr    video.Consumer
	hdrErr error
	dv     *dv.Stream
	dvErr  error
	clip   *dv.Clip
	budget *dv.MemoryBudget
	end    uint64
}

func (c *discConsumers) failDV(err error) {
	if c.dvErr == nil {
		c.dvErr = err
		c.dv.Abort(err)
	}
}

func (c *discConsumers) outcome() error {
	if c.hdr != nil && c.hdrErr == nil || c.dv != nil && c.dvErr == nil {
		return nil
	}
	return errors.Join(c.hdrErr, c.dvErr)
}

func (c *discConsumers) Consume(ctx context.Context, chunk video.Chunk) error {
	if c.hdr != nil && c.hdrErr == nil {
		c.hdrErr = c.hdr.Consume(ctx, chunk)
	}
	if c.dv != nil && c.dvErr == nil {
		charge, err := c.budget.Reserve(int64(len(chunk.Starts)) * 192)
		if err != nil {
			c.failDV(err)
			return c.outcome()
		}
		defer charge.Release()
		markers := make([]dv.Marker, len(chunk.Starts))
		for i, start := range chunk.Starts {
			if len(start.Header) == 0 || start.Segment != 0 || start.PTS >= 1<<33 || start.DTS >= 1<<33 {
				c.failDV(dv.ErrUnsupportedInput)
				return c.outcome()
			}
			markers[i] = dv.Marker{
				ESOffset:          start.Offset,
				Kind:              dv.PESStart,
				Modulo33:          true,
				PacketIndex:       start.PacketIndex,
				SourceOffset:      start.SourceOffset,
				HasSourcePosition: true,
				PESHeader:         start.Header,
				PTS: dv.Timestamp{
					Value:     int64(start.PTS),
					Timescale: 90000,
					Valid:     start.HasPTS,
				},
				DTS: dv.Timestamp{
					Value:     int64(start.DTS),
					Timescale: 90000,
					Valid:     start.HasDTS,
				},
			}
		}
		if err := c.dv.Push(ctx, dv.Chunk{
			ESOffset: chunk.Offset,
			Data:     chunk.Data,
			Markers:  markers,
		}); err != nil {
			c.failDV(err)
		}
	}
	c.end = chunk.Offset + uint64(len(chunk.Data))
	return c.outcome()
}

func (c *discConsumers) Discontinuity(ctx context.Context, event video.Discontinuity) error {
	if c.hdr != nil && c.hdrErr == nil {
		c.hdrErr = c.hdr.Discontinuity(ctx, event)
	}
	c.failDV(dv.ErrIncomplete)
	return c.outcome()
}

func (c *discConsumers) Finish(end video.End) error {
	if c.hdr != nil && c.hdrErr == nil {
		c.hdrErr = c.hdr.Finish(end)
	}
	if c.dv != nil && c.dvErr == nil {
		if !end.CleanEOF || end.SourcePacketCount == 0 {
			c.failDV(errors.Join(dv.ErrIncomplete, end.Err))
		} else {
			marker := []dv.Marker{{
				ESOffset:          c.end,
				Kind:              dv.SourceEnd,
				PacketIndex:       end.SourcePacketCount,
				HasSourcePosition: true,
			}}
			if err := c.dv.Push(c.ctx, dv.Chunk{ESOffset: c.end, Markers: marker}); err != nil {
				c.failDV(err)
			} else {
				c.clip, c.dvErr = c.dv.FinishClip(c.ctx)
			}
		}
	}
	return c.outcome()
}

func assembleDiscDV(ctx context.Context, source string, timeline bd.PlaylistTimeline, collectors map[dv.StreamKey]*discConsumers,
	exhausted bool, budget *dv.MemoryBudget, allowance int64,
) (*dv.Extraction, error) {
	if len(timeline.Items) > 8192 {
		return nil, dv.ErrUnsupportedInput
	}
	charge, err := budget.Reserve(int64(len(timeline.Items)) * 2048)
	if err != nil {
		return nil, fmt.Errorf("reserve Dolby Vision playlist: %w", err)
	}
	defer charge.Release()
	hdrPlaylist, err := bridge.Timeline(source, timeline)
	if err != nil {
		return nil, fmt.Errorf("validate Dolby Vision playlist: %w", err)
	}
	playlist := dv.Playlist{ID: hdrPlaylist.ID}
	clips := make(map[dv.StreamKey]*dv.Clip)
	for _, occurrence := range hdrPlaylist.Occurrences {
		key := dv.StreamKey{
			SourceID: occurrence.Key.SourceID,
			ClipID:   occurrence.Key.ClipID,
			TrackID:  occurrence.Key.TrackID,
		}
		c := collectors[key]
		if c == nil && exhausted {
			return nil, dv.ErrResourceLimit
		}
		if c == nil || c.clip == nil && c.dvErr == nil {
			return nil, dv.ErrIncomplete
		}
		if c.dvErr != nil {
			return nil, c.dvErr
		}
		clips[key] = c.clip
		connection := dv.ConnectionReset
		if occurrence.Connection == extract.ConnectionSeamless {
			connection = dv.ConnectionSeamless
		}
		playlist.Occurrences = append(playlist.Occurrences, dv.Occurrence{
			Key:           key,
			Angle:         occurrence.Angle,
			In:            occurrence.In,
			Out:           occurrence.Out,
			PlaylistStart: occurrence.PlaylistStart,
			Connection:    connection,
			ClockOffset: dv.Timestamp{
				Value:     occurrence.ClockOffset.Value,
				Timescale: occurrence.ClockOffset.Timescale,
				Valid:     occurrence.ClockOffset.Valid,
			},
			Packets: &dv.PacketRange{
				Start: occurrence.Packets.Start,
				End:   occurrence.Packets.End,
				ClockAnchor: dv.Timestamp{
					Value:     occurrence.Packets.ClockAnchor.Value,
					Timescale: occurrence.Packets.ClockAnchor.Timescale,
					Valid:     occurrence.Packets.ClockAnchor.Valid,
				},
			},
		})
	}
	result, err := dv.Assemble(ctx, playlist, clips, dv.AssemblyOptions{Budget: budget, Limits: dv.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: allowance}})
	if err != nil {
		return nil, fmt.Errorf("assemble Dolby Vision playlist: %w", err)
	}
	return result, nil
}

// TakePlaylist transfers one completed playlist out of the scan owner.
func (o *NativeDiscOutcome) TakePlaylist(name string, otherRetainedBytes int64) (*NativeExtraction, []TimelineItem, error) {
	if o == nil || o.Outcome == nil || o.Report == nil {
		return nil, nil, extract.ErrIncomplete
	}
	validationBytes := otherRetainedBytes
	for _, playlist := range o.DolbyVision {
		validationBytes += playlist.Extraction.RetainedBytes()
	}
	sidecar, hdrErr := discExtractionWithRetained(o.Outcome, name, validationBytes)
	r := &NativeExtraction{
		otherRetainedBytes: otherRetainedBytes + producerBytes,
		HDR10Plus:          sidecar.Extraction,
		HDR10PlusError:     hdrErr,
		DolbyVisionError:   dv.ErrIncomplete,
	}
	if o.OmittedDVPlaylists {
		r.DolbyVisionError = dv.ErrResourceLimit
	}
	for i, p := range o.Playlists {
		if strings.EqualFold(p.Name, name) {
			if r.HDR10Plus == nil {
				p.Extraction.Release()
			}
			o.Playlists[i].Extraction = nil
		} else {
			r.otherRetainedBytes += p.Extraction.RetainedBytes()
		}
	}
	for i, p := range o.DolbyVision {
		if strings.EqualFold(p.Name, name) {
			r.DolbyVision, r.DolbyVisionError = p.Extraction, p.Err
			o.DolbyVision[i].Extraction = nil
		} else {
			r.otherRetainedBytes += p.Extraction.RetainedBytes()
		}
	}
	if r.HDR10Plus != nil {
		r.TrackID = r.HDR10Plus.Frames[0].Stream.TrackID
	}
	if r.DolbyVision != nil {
		if len(sidecar.Timeline) == 0 {
			var timelineErr error
			sidecar.Timeline, _, timelineErr = discTimeline(o.Report, name)
			if timelineErr != nil {
				r.Release()
				return nil, nil, timelineErr
			}
		}
		if r.TrackID == 0 {
			r.TrackID = r.DolbyVision.Pictures[0].Stream.TrackID
		}
		r.ScanComplete = true
	}
	r.ScanComplete = r.ScanComplete || r.HDR10Plus != nil || errors.Is(hdrErr, extract.ErrNoMetadata)
	if o.Formats == api.HDRAnalysisFormatsHDR10Plus {
		r.DolbyVisionError = nil
	}
	if o.Formats == api.HDRAnalysisFormatsDolbyVision {
		r.HDR10PlusError = nil
	}
	return r, sidecar.Timeline, nil
}
