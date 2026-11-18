// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"

	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// ComparisonFrameReplay is durable private evidence for an observed picture.
// Public identities select nothing unless this server-owned witness is retained.
type ComparisonFrameReplay struct {
	DecoderPTS      int64
	Identity        api.ComparisonFrameIdentity
	TitleTime90     int64
	Window          comparisonDiscWindow
	PictureChecksum string
	PixelFormat     string
}

type comparisonDiscWindow struct {
	Codec                                     uint8
	TransportTablePID                         uint16
	RandomAccess90                            int64
	InitializationSHA256, EncodedSHA256       string
	Spans                                     []filesystem.ComparisonSpan
	Format, StreamID, Occurrence              string
	SourceBase90, TitleBase90, Start90, End90 int64
	PrerollSeconds                            float64
	ProbeBytes                                int64
}

func comparisonDiscFailure(reason string) error {
	return api.NewOperationError(api.OperationFailure{
		Code:             api.OperationFailureInvalidSource,
		Operation:        api.OperationKindMedia,
		Recovery:         api.OperationRecoveryEditInput,
		ComparisonReason: reason,
		Message:          "The selected programme cannot provide an exact bounded comparison frame (" + reason + ").",
	}, filesystem.ErrComparisonIndex)
}

func discWindows(source ComparisonSource, start, end float64) ([]comparisonDiscWindow, error) {
	input := source.Input
	if input == nil || input.Version != 1 || !comparisonFinite(start) || !comparisonFinite(end) || start < 0 || end <= start || end-start > 64+1e-9 ||
		end >= float64(math.MaxInt64)/90000-1 {
		return nil, comparisonDiscFailure("window_limit")
	}
	start90 := int64(math.Round(start * 90000))
	end90 := start90 + int64(math.Round((end-start)*90000))
	if input.Kind == "dvd" {
		if input.DVD == nil {
			return nil, comparisonDiscFailure("title_mapping")
		}
		reader, err := filesystem.OpenDVDTitle(input.Root, input.DVD.VTS)
		if err != nil {
			return nil, fmt.Errorf("comparison disc window: %w", err)
		}
		defer reader.Close()
		windows, err := filesystem.ResolveDVDWindows(reader, *input.DVD, start90, min(end90, input.DVD.Duration90))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", comparisonDiscFailure("dvd_nav_clock"), err)
		}
		var out []comparisonDiscWindow
		for _, window := range windows {
			out = append(out, comparisonDiscWindow{
				PrerollSeconds: float64(window.Preroll90) / 90000,
				Spans:          window.Spans,
				Format:         "mpeg",
				StreamID:       "0x1e0",
				Occurrence:     fmt.Sprintf("dvd-cell-%d", window.Cell),
				SourceBase90:   window.SourceOrigin90,
				TitleBase90:    window.TitleOffset90,
				Start90:        window.Start90,
				End90:          window.End90,
				ProbeBytes:     int64(window.NAVProbes) * 2048,
			})
		}
		return out, nil
	}
	if input.Kind != "bluray" {
		return nil, comparisonDiscFailure("disc_format")
	}
	var windows []comparisonDiscWindow
	for _, item := range input.Occurrences {
		if item.Offset45 > 1<<48 || item.In45 >= item.Out45 || item.STC.EndPacket > 1<<32 {
			return nil, comparisonDiscFailure("occurrence_bounds")
		}
		offset, duration := int64(item.Offset45)*2, int64(item.Out45-item.In45)*2
		if start90 >= offset+duration || end90 <= offset {
			continue
		}
		from := item.ClockIn90 + max(0, start90-offset)
		to := item.ClockIn90 + min(duration, end90-offset)
		var entries []filesystem.CLPIEntry
		for _, entry := range item.CPI {
			if entry.Packet >= item.STC.StartPacket && uint64(entry.Packet) < item.STC.EndPacket {
				entries = append(entries, entry)
			}
		}
		if len(entries) == 0 {
			return nil, comparisonDiscFailure("cpi_stc_mapping")
		}
		// CPI clock values retain their 32-bit epoch. A discontinuity cannot
		// authorize a binary search through a differently ordered clock domain.
		for i := 1; i < len(entries); i++ {
			if entries[i].PTS45 < entries[i-1].PTS45 {
				return nil, comparisonDiscFailure("cpi_clock_wrap")
			}
		}
		begin := max(sort.Search(len(entries), func(i int) bool { return entries[i].PTS45*2 > from-90000 })-1, 0)
		if entries[begin].PTS45*2 > from || from-entries[begin].PTS45*2 > 10*90000 {
			return nil, comparisonDiscFailure("preroll_limit")
		}
		stop := sort.Search(len(entries), func(i int) bool { return entries[i].PTS45*2 > to+90000 })
		endPacket := item.STC.EndPacket
		if stop < len(entries) {
			endPacket = uint64(entries[stop].Packet)
		}
		startPacket := uint64(entries[begin].Packet)
		if endPacket <= startPacket || (endPacket-startPacket)*192 > 2<<30 {
			return nil, comparisonDiscFailure("encoded_span_limit")
		}
		// Retain original transport tables from the selected STC's beginning;
		// the payload interval itself starts at an indexed codec random access.
		initEnd := min(uint64(item.STC.StartPacket)+1024, startPacket)
		if startPacket > 1<<32 || endPacket > 1<<32 || initEnd > 1<<32 || initEnd < uint64(item.STC.StartPacket) {
			return nil, comparisonDiscFailure("packet_bounds")
		}
		probePackets := initEnd - uint64(item.STC.StartPacket)
		if probePackets > 1024 {
			return nil, comparisonDiscFailure("initialization_limit")
		}
		var spans []filesystem.ComparisonSpan
		if initEnd > uint64(item.STC.StartPacket) {
			spans = append(spans, filesystem.ComparisonSpan{
				Path:  item.Path,
				Start: int64(item.STC.StartPacket) * 192,
				End:   int64(initEnd) * 192,
			})
		}
		spans = append(spans, filesystem.ComparisonSpan{
			Path:  item.Path,
			Start: int64(startPacket) * 192,
			End:   int64(endPacket) * 192,
		})
		windows = append(windows, comparisonDiscWindow{
			Codec:          uint8(item.Mapping.Codec),
			RandomAccess90: entries[begin].PTS45 * 2,
			Spans:          spans,
			Format:         "mpegts",
			StreamID:       fmt.Sprintf("0x%x", item.Mapping.PID),
			Occurrence:     fmt.Sprintf("bluray-item-%d-atc-%d-stc-%d", item.Index, item.STC.ATCIndex, item.STC.ID),
			SourceBase90:   item.ClockIn90,
			TitleBase90:    offset,
			Start90:        from,
			End90:          to,
			PrerollSeconds: float64(from-entries[begin].PTS45*2) / 90000,
			ProbeBytes:     int64(probePackets) * 192,
		})
	}
	return windows, nil
}

// stageDiscWindow copies bounded original spans without remuxing or changing
// timestamps. The owning release supplies ScratchDir; source trees stay read-only.
func stageDiscWindow(ctx context.Context, source ComparisonSource, window *comparisonDiscWindow) (string, func(), error) {
	if source.Input == nil || source.ScratchDir == "" {
		return "", nil, comparisonDiscFailure("artifact_owner")
	}
	if err := os.MkdirAll(source.ScratchDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("comparison disc window: %w", err)
	}
	file, err := os.CreateTemp(source.ScratchDir, "comparison-window-*")
	if err != nil {
		return "", nil, fmt.Errorf("comparison disc window: %w", err)
	}
	cleanup := func() { _ = file.Close(); _ = os.Remove(file.Name()) }
	ok := false
	defer func() {
		if !ok {
			cleanup()
		}
	}()
	var total int64
	digest := sha256.New()
	for _, span := range window.Spans {
		if err := ctx.Err(); err != nil {
			return "", nil, fmt.Errorf("comparison disc window: %w", err)
		}
		if span.Start < 0 || span.End <= span.Start || span.End-span.Start > 2<<30-total || !pathing.IsWithinRoot(source.Input.Root, span.Path) {
			return "", nil, comparisonDiscFailure("encoded_span_limit")
		}
		info, err := os.Lstat(span.Path)
		if err != nil || !info.Mode().IsRegular() || span.End > info.Size() {
			return "", nil, comparisonDiscFailure("source_changed")
		}
		original, err := pathing.OpenFileWithinRoot(source.Input.Root, span.Path, os.O_RDONLY, 0)
		if err != nil {
			return "", nil, fmt.Errorf("comparison disc window: %w", err)
		}
		opened, statErr := original.Stat()
		if statErr != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() || !opened.ModTime().Equal(info.ModTime()) {
			_ = original.Close()
			return "", nil, comparisonDiscFailure("source_changed")
		}
		reader := io.NewSectionReader(original, span.Start, span.End-span.Start)
		buffer := make([]byte, 64<<10)
		for {
			if err := ctx.Err(); err != nil {
				_ = original.Close()
				return "", nil, fmt.Errorf("comparison disc window: %w", err)
			}
			n, readErr := reader.Read(buffer)
			if n > 0 {
				_, _ = digest.Write(buffer[:n])
				if _, err := file.Write(buffer[:n]); err != nil {
					_ = original.Close()
					return "", nil, fmt.Errorf("comparison disc window: %w", err)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = original.Close()
				return "", nil, fmt.Errorf("comparison disc window: %w", readErr)
			}
		}
		after, statErr := original.Stat()
		current, pathErr := os.Lstat(span.Path)
		_ = original.Close()
		if statErr != nil || pathErr != nil || !os.SameFile(opened, after) || !os.SameFile(after, current) || after.Size() != opened.Size() ||
			!after.ModTime().Equal(opened.ModTime()) ||
			current.Size() != after.Size() ||
			!current.ModTime().Equal(after.ModTime()) {
			return "", nil, comparisonDiscFailure("source_changed")
		}
		total += span.End - span.Start
	}
	if err := file.Close(); err != nil {
		return "", nil, fmt.Errorf("comparison disc window: %w", err)
	}
	encoded := hex.EncodeToString(digest.Sum(nil))
	if window.EncodedSHA256 != "" && window.EncodedSHA256 != encoded {
		return "", nil, comparisonDiscFailure("encoded_replay_changed")
	}
	window.EncodedSHA256 = encoded
	if window.Format == "mpegts" {
		window.ProbeBytes = 0
	}
	if err := comparisonTransportTablesOnly(ctx, source, file.Name(), window); err != nil {
		return "", nil, err
	}
	if err := validateComparisonInitialization(ctx, file.Name(), window); err != nil {
		return "", nil, err
	}
	ok = true
	return file.Name(), cleanup, nil
}

func bindDiscObservation(source ComparisonSource, window comparisonDiscWindow, observation *ComparisonFrameObservation) error {
	if err := validateComparisonNativeFacts(source, *observation); err != nil {
		return err
	}
	if observation.TimeBaseNumerator != 1 || observation.TimeBaseDenominator != 90000 || observation.PictureChecksum == "" {
		return comparisonDiscFailure("original_clock_witness")
	}
	decoderPTS := observation.PTS
	observation.PTS = comparisonDiscClockNear(decoderPTS, window.Start90)
	observation.TimestampSeconds = float64(observation.PTS) / 90000
	if observation.PTS < window.Start90 || observation.PTS >= window.End90 {
		return comparisonDiscFailure("frame_outside_occurrence")
	}
	identity := api.ComparisonFrameIdentity{
		Version:             1,
		InputFingerprint:    source.InputFingerprint,
		Occurrence:          window.Occurrence,
		StreamID:            window.StreamID,
		PTS:                 observation.PTS,
		TimeBaseNumerator:   1,
		TimeBaseDenominator: 90000,
	}
	titleTime := window.TitleBase90 + observation.PTS - window.SourceBase90
	key, err := api.CanonicalWorkflowFingerprint(identity)
	if err != nil {
		return fmt.Errorf("comparison disc window: %w", err)
	}
	observation.FrameID = string(key)
	observation.Identity = &identity
	observation.RelativeTimestampSeconds = float64(titleTime) / 90000
	observation.Replay = &ComparisonFrameReplay{
		DecoderPTS:      decoderPTS,
		Identity:        identity,
		TitleTime90:     titleTime,
		Window:          window,
		PictureChecksum: observation.PictureChecksum,
		PixelFormat:     observation.PixelFormat,
	}
	return nil
}

func (s *Service) sampleDiscComparison(ctx context.Context, request ComparisonSampleRequest) ([]ComparisonSample, error) {
	if request.DurationSeconds > 64 || request.DurationSeconds <= 0 || request.StrideSeconds <= 0 ||
		!request.NativeFrames && math.Ceil(request.DurationSeconds/request.StrideSeconds)+1 > 50000 {
		return nil, comparisonDiscFailure("window_limit")
	}
	windows, err := discWindows(request.Source, request.StartSeconds, request.StartSeconds+request.DurationSeconds)
	if err != nil {
		return nil, err
	}
	executable, err := resolveFFmpeg()
	if err != nil {
		return nil, err
	}
	var samples []ComparisonSample
	for _, window := range windows {
		if request.Budget != nil {
			var admitted int64
			for _, span := range window.Spans {
				if span.End < span.Start || span.End-span.Start > request.Budget.RemainingEncodedBytes-admitted {
					return nil, ErrComparisonFastInconclusive
				}
				admitted += span.End - span.Start
			}
			if err := request.Budget.admit(admitted); err != nil {
				return nil, err
			}
		}
		path, cleanup, err := stageDiscWindow(ctx, request.Source, &window)
		if err != nil {
			return nil, err
		}
		inputURL, urlErr := comparisonSubfileInput(path)
		if urlErr != nil {
			cleanup()
			return nil, urlErr
		}
		args := []string{
			"-hide_banner",
			"-threads", "4", "-filter_threads", "1",
			"-loglevel",
			"info",
			"-copyts",
			"-protocol_whitelist",
			"file,subfile",
			"-f",
			window.Format,
			"-i",
			inputURL,
			"-map",
			"0:i:" + window.StreamID,
			"-an",
			"-sn",
			"-dn",
		}
		filter := comparisonDiscWindowFilter(window) + ",showinfo"
		service := *s
		service.runner = finiteComparisonRunner{
			base:           s.runner,
			outputLimit:    50000 * 256,
			retentionLimit: request.RetentionLimitBytes,
		}
		windowContext := context.WithValue(ctx, comparisonSampleProgressKey{}, &comparisonSampleProgress{
			ctx:      ctx,
			start:    float64(window.Start90) / 90000,
			duration: float64(window.End90-window.Start90) / 90000,
		})
		physicalRequest := request
		physicalRequest.DurationSeconds = float64(window.End90-window.Start90)/90000 + window.PrerollSeconds
		physicalRequest.decodePrerollSeconds = window.PrerollSeconds
		physicalRequest.decodeOccurrence = window.Occurrence
		physicalRequest.decodeCodec = window.Codec
		for _, span := range window.Spans {
			physicalRequest.encodedAttemptBytes += span.End - span.Start
		}
		result, runErr := service.runComparisonSamples(windowContext, executable, args, filter, physicalRequest)
		cleanup()
		if runErr != nil {
			return nil, runErr
		}
		if request.analysis != nil {
			analyzed, err := analysisSamples(result, request, &window)
			if err != nil {
				return nil, err
			}
			samples = append(samples, analyzed...)
			continue
		}
		observations, err := comparisonObservations(result.Stderr)
		if err != nil || len(observations) > 50000 {
			return nil, comparisonDiscFailure("observation_count")
		}
		if comparisonNormalizesPicture(request) {
			output, err := comparisonObservationsFor(result.Stderr, comparisonOutputShowInfoLine)
			if err != nil || len(output)*256 != len(result.Stdout) {
				return nil, comparisonDiscFailure("normalized_observation_count")
			}
			var selected []ComparisonFrameObservation
			position := 0
			for _, frame := range output {
				for position < len(observations) && observations[position].TimestampSeconds < frame.TimestampSeconds-1e-6 {
					position++
				}
				if position >= len(observations) || math.Abs(observations[position].TimestampSeconds-frame.TimestampSeconds) > 1e-6 {
					return nil, comparisonDiscFailure("normalization_changed_identity")
				}
				selected = append(selected, observations[position])
				position++
			}
			observations = selected
		}
		if len(result.Stdout) != len(observations)*256 {
			return nil, comparisonDiscFailure("observation_count")
		}
		seen := map[string]bool{}
		for i, observation := range observations {
			if err := bindDiscObservation(request.Source, window, &observation); err != nil {
				return nil, err
			}
			if seen[observation.FrameID] {
				return nil, comparisonDiscFailure("ambiguous_native_pts")
			}
			seen[observation.FrameID] = true
			if !request.NativeFrames && len(samples) > 0 &&
				observation.RelativeTimestampSeconds-samples[len(samples)-1].RelativeTimestampSeconds < request.StrideSeconds-1e-9 {
				continue
			}
			samples = append(samples, ComparisonSample{
				ComparisonFrameObservation: observation,
				Pixels:                     bytes.Clone(result.Stdout[i*256 : (i+1)*256]),
				NativeSequence:             request.NativeFrames,
			})
		}
	}
	if len(samples) > 50000 {
		return nil, comparisonDiscFailure("observation_limit")
	}
	slices.SortFunc(samples, func(a, b ComparisonSample) int {
		return cmp.Compare(a.RelativeTimestampSeconds, b.RelativeTimestampSeconds)
	})
	return samples, nil
}

// InspectResolvedComparisonSource obtains facts from a bounded selected-title
// decode. It requires neither ffprobe nor upload metadata/report collection.
func (s *Service) InspectResolvedComparisonSource(
	ctx context.Context,
	input filesystem.ResolvedComparisonInput,
	fingerprint, scratch string,
) (ComparisonSource, error) {
	source := ComparisonSource{
		Path:             input.Root,
		Input:            &input,
		InputFingerprint: fingerprint,
		ScratchDir:       scratch,
	}
	if input.Kind == "dvd" && input.DVD != nil {
		source.DurationSeconds = float64(input.DVD.Duration90) / 90000
	}
	if input.Kind == "bluray" && len(input.Occurrences) > 0 {
		last := input.Occurrences[len(input.Occurrences)-1]
		source.DurationSeconds = float64(last.Offset45+uint64(last.Out45-last.In45)) / 45000
	}
	if source.DurationSeconds <= 0 {
		return source, comparisonDiscFailure("authored_duration")
	}
	source.VideoDurationSeconds = source.DurationSeconds
	frames, err := s.sampleDiscComparison(ctx, ComparisonSampleRequest{
		Source:          source,
		StartSeconds:    0,
		DurationSeconds: min(1, source.DurationSeconds),
		StrideSeconds:   .002,
	})
	if err != nil {
		return source, err
	}
	if len(frames) < 2 {
		return source, comparisonDiscFailure("source_facts")
	}
	first := frames[0]
	source.NativePixelFormat = first.PixelFormat
	source.NativeChromaLocation = first.ChromaLocation
	source.NativeColour = first.Colour
	source.SARNumerator, source.SARDenominator = first.SARNumerator, first.SARDenominator
	source.Interlace = first.Interlace
	source.Width, source.Height = first.Width, first.Height
	if first.SARNumerator <= 0 || first.SARDenominator <= 0 || source.Width <= 0 || source.Height <= 0 {
		return source, comparisonDiscFailure("source_geometry")
	}
	source.WidthScale = float64(first.SARNumerator) / float64(first.SARDenominator)
	source.HeightScale = 1
	windows, err := discWindows(source, 0, min(1, source.DurationSeconds))
	if err != nil || len(windows) == 0 {
		return source, comparisonDiscFailure("source_facts")
	}
	path, cleanup, err := stageDiscWindow(ctx, source, &windows[0])
	if err != nil {
		return source, err
	}
	service := *s
	service.runner = finiteComparisonRunner{base: s.runner, outputLimit: 64 << 20}
	inspected, inspectErr := service.InspectComparisonSource(ctx, path)
	cleanup()
	if inspectErr != nil {
		return source, inspectErr
	}
	source.HDRFacts = inspected.HDRFacts
	// Nominal rate is decoder-header evidence; native display intervals remain
	// authoritative for matching and may alternate in a 90 kHz clock.
	if first.FrameRateNumerator > 0 && first.FrameRateDenominator > 0 {
		source.FrameRateNumerator = first.FrameRateNumerator
		source.FrameRateDenominator = first.FrameRateDenominator
		source.FrameRate = float64(first.FrameRateNumerator) / float64(first.FrameRateDenominator)
	}
	for _, frame := range frames {
		if frame.Width != first.Width || frame.Height != first.Height || frame.PixelFormat != first.PixelFormat || frame.SARNumerator != first.SARNumerator ||
			frame.SARDenominator != first.SARDenominator || frame.Colour != first.Colour {
			return source, comparisonDiscFailure("changing_format")
		}
	}
	return source, nil
}

type exactDiscCaptureRunner struct {
	service      *Service
	source       ComparisonSource
	softwareOnly bool
	base         Runner
	window       comparisonDiscWindow
	replay       ComparisonFrameReplay
}

func (r exactDiscCaptureRunner) Run(ctx context.Context, name string, args []string, dir string) (CommandResult, error) {
	command := slices.Clone(args)
	// The physical locator already supplies preroll. Even -ss 0 can change
	// timestamp wrapping or discard a negative source-clock picture.
	for i := 0; i+1 < len(command); {
		if command[i] == "-ss" {
			command = slices.Delete(command, i, i+2)
		} else {
			i++
		}
	}
	command = slices.Insert(command, len(command)-1, "-fps_mode", "passthrough")
	for i, arg := range command {
		if arg == "-i" && i+1 < len(command) {
			url, err := comparisonSubfileInput(command[i+1])
			if err != nil {
				return CommandResult{}, err
			}
			command[i+1] = url
		}
	}
	for i, arg := range command {
		if arg == "-i" {
			command = append(command[:i], append([]string{"-f", r.window.Format}, command[i:]...)...)
			break
		}
	}
	for i, arg := range command {
		if arg == "-map" && i+1 < len(command) {
			command[i+1] = "0:i:" + r.window.StreamID
		}
		if arg == "-vf" && i+1 < len(command) {
			command[i+1] = fmt.Sprintf("format=pix_fmts=%s,select='eq(pts,%d)',", r.replay.PixelFormat, r.replay.DecoderPTS) + command[i+1]
		}
	}
	runner := finiteComparisonRunner{base: r.base, outputLimit: 64 << 20}
	var result CommandResult
	var err error
	if r.service != nil {
		result, err = r.service.runComparisonCaptureDecoder(
			ctx,
			runner,
			name,
			command,
			dir,
			r.source,
			r.softwareOnly,
			fmt.Sprintf("capture-authored/%s/%d", r.window.Occurrence, r.window.Codec),
		)
	} else {
		result, err = runner.Run(ctx, name, command, dir)
	}
	observed, parseErr := comparisonObservations(result.Stderr)
	if err == nil &&
		(parseErr != nil || len(observed) != 1 || observed[0].PTS != r.replay.DecoderPTS || observed[0].PictureChecksum != r.replay.PictureChecksum) {
		return CommandResult{}, comparisonDiscFailure("replay_picture_changed")
	}
	return result, err
}

func (s *Service) captureDiscComparison(ctx context.Context, request ComparisonCaptureRequest) (ComparisonFrame, error) {
	ctx, _ = comparisonProcessCounter(ctx, nil)
	if request.Replay == nil {
		return ComparisonFrame{}, comparisonDiscFailure("inspect_frame_required")
	}
	replay := request.Replay
	if replay.Identity.InputFingerprint != request.Source.InputFingerprint || replay.Identity.Version != 1 ||
		replay.Identity.Occurrence != replay.Window.Occurrence ||
		replay.PictureChecksum == "" {
		return ComparisonFrame{}, comparisonDiscFailure("stale_frame_witness")
	}
	window := replay.Window
	path, cleanup, err := stageDiscWindow(ctx, request.Source, &window)
	if err != nil {
		return ComparisonFrame{}, err
	}
	defer cleanup()
	service := *s
	toneMap := s.cfg.ScreenshotHandling.ToneMap
	if request.ToneMap != nil {
		toneMap = *request.ToneMap
	}
	service.runner = exactDiscCaptureRunner{
		service:      s,
		source:       request.Source,
		softwareOnly: toneMap && request.Source.HDRFacts.SupportsToneMapping() && s.cfg.ScreenshotHandling.UseLibplacebo,
		base:         s.runner,
		window:       replay.Window,
		replay:       *replay,
	}
	fileRequest := request
	fileRequest.Replay = nil
	fileRequest.Source.Input = nil
	fileRequest.Source.Path = path
	fileRequest.Source.StartTimeSeconds = float64(replay.Window.SourceBase90-replay.Window.TitleBase90-(replay.Identity.PTS-replay.DecoderPTS)) / 90000
	fileRequest.TimestampSeconds = 0
	frame, err := service.ComparisonCapture(ctx, fileRequest)
	if err != nil {
		return ComparisonFrame{}, err
	}
	if err := bindDiscObservation(request.Source, replay.Window, &frame.ComparisonFrameObservation); err != nil {
		return ComparisonFrame{}, err
	}
	if frame.Replay.TitleTime90 != replay.TitleTime90 {
		return ComparisonFrame{}, comparisonDiscFailure("replay_title_clock_changed")
	}
	frame.RequestedTimestampSeconds = request.TimestampSeconds
	return frame, nil
}

func comparisonDiscClockNear(value, expected int64) int64 {
	const modulus = int64(1 << 33)
	value %= modulus
	if value < 0 {
		value += modulus
	}
	result := (expected/modulus)*modulus + value
	if result-expected > modulus/2 {
		result -= modulus
	}
	if expected-result > modulus/2 {
		result += modulus
	}
	return result
}

func comparisonDiscWindowFilter(window comparisonDiscWindow) string {
	const modulus = int64(1 << 33)
	start, end := window.Start90%modulus, window.End90%modulus
	if start < 0 {
		start += modulus
	}
	if end < 0 {
		end += modulus
	}
	clock := fmt.Sprintf("mod(pts+%d,%d)", modulus, modulus)
	if start < end {
		return fmt.Sprintf("select='gte(%s,%d)*lt(%s,%d)'", clock, start, clock, end)
	}
	return fmt.Sprintf("select='gte(%s,%d)+lt(%s,%d)'", clock, start, clock, end)
}
