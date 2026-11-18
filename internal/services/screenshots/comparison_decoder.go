// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

var errComparisonHardwareInitialization = errors.New("comparison hardware initialization unavailable")

type comparisonDecoder struct {
	backend, download, key string
	device, initDevice     string
}

type comparisonDeviceGeneration struct{ fingerprint, d3dVendor, vaapiDevice string }

type comparisonDecoderDecision struct {
	done    chan struct{}
	decoder comparisonDecoder
	err     error
}

var comparisonDecoderDecisions = struct {
	sync.Mutex
	values map[string]*comparisonDecoderDecision
}{values: map[string]*comparisonDecoderDecision{}}

func nativeComparisonRunner(r Runner) bool {
	if finite, ok := r.(finiteComparisonRunner); ok {
		return nativeComparisonRunner(finite.base)
	}
	_, ok := r.(commandRunner)
	return ok
}

func comparisonHardwareCandidates(platform string, compiled []string) []string {
	candidates := []string{"cuda"}
	switch platform {
	case "windows":
		candidates = append(candidates, "d3d11va")
	case "linux":
		candidates = append(candidates, "vaapi")
	case "darwin":
		candidates = []string{"videotoolbox"}
	default:
		return nil
	}
	return slices.DeleteFunc(candidates, func(candidate string) bool { return !slices.Contains(compiled, candidate) })
}

func comparisonHardwareCommand(args []string, decoder comparisonDecoder) []string {
	if decoder.backend == "" {
		return slices.Clone(args)
	}
	command := slices.Clone(args)
	for i, arg := range command {
		if arg == "-vf" && i+1 < len(command) {
			command[i+1] = decoder.download + command[i+1]
		}
		if arg == "-filter_complex" && i+1 < len(command) {
			// The original filter observes native pictures before splitting.
			_, body, ok := strings.Cut(command[i+1], "]")
			if ok {
				prefix := strings.TrimSuffix(command[i+1], body)
				command[i+1] = prefix + decoder.download + body
			}
		}
	}
	for i, arg := range command {
		if arg == "-i" {
			options := []string{"-hwaccel", decoder.backend, "-hwaccel_output_format", comparisonHardwareSurface(decoder.backend)}
			if decoder.device != "" {
				options = append(options, "-hwaccel_device", decoder.device)
			}
			command = slices.Insert(command, i, options...)
			if decoder.initDevice != "" {
				command = append([]string{"-init_hw_device", decoder.initDevice}, command...)
			}
			return command
		}
	}
	return command
}

func comparisonHardwareSurface(backend string) string {
	if backend == "d3d11va" {
		return "d3d11"
	}
	if backend == "videotoolbox" {
		return "videotoolbox_vld"
	}
	return backend
}

func comparisonHardwareDownload(reference ComparisonFrameObservation) string {
	var surface string
	switch reference.PixelFormat {
	case "yuv420p":
		surface = "nv12"
	case "yuv420p10le":
		surface = "p010le"
	default:
		return ""
	}
	filter := "hwdownload,format=" + surface + ",format=" + reference.PixelFormat + ","
	// Restore only a positively observed source fact, then require both native
	// planes and normalized witnesses to equal the software reference.
	if slices.Contains([]string{"left", "center", "topleft", "top", "bottomleft", "bottom"}, reference.ChromaLocation) {
		filter += "setparams=chroma_location=" + reference.ChromaLocation + ","
	}
	return filter
}

func (r ComparisonSampleRequest) admitDecode(seconds float64) error {
	if err := r.Budget.admitMovieSeconds(seconds); err != nil {
		return err
	}
	if r.Budget != nil && r.encodedAttemptBytes > 0 {
		return r.Budget.admit(r.encodedAttemptBytes)
	}
	return nil
}

func (s *Service) comparisonDecoder(
	ctx context.Context,
	executable string,
	args []string,
	filter string,
	request ComparisonSampleRequest,
	mode string,
) (comparisonDecoder, error) {
	if mode == "libplacebo" || request.softwareDecodeOnly || !nativeComparisonRunner(s.runner) || request.Source.NativePixelFormat == "" {
		return comparisonDecoder{}, nil
	}
	if comparisonHardwareDownload(ComparisonFrameObservation{PixelFormat: request.Source.NativePixelFormat}) == "" {
		return comparisonDecoder{}, nil
	}
	surface := "sampling-file"
	if request.analysis != nil {
		surface = "analysis-file"
	}
	if request.Source.Input != nil {
		surface += fmt.Sprintf("-authored/%s/%d", request.decodeOccurrence, request.decodeCodec)
	}
	key, candidates, err := comparisonDecoderInputs(ctx, executable, request.Source, surface)
	if err != nil {
		return comparisonDecoder{}, err
	}
	if len(candidates) == 0 {
		return comparisonDecoder{}, nil
	}
	return s.admitComparisonDecoder(ctx, key, func() (comparisonDecoder, error) {
		probeRequest := request
		probeRequest.analysis = nil
		probeRequest.DurationSeconds = min(request.DurationSeconds, .5+request.decodePrerollSeconds)
		normalization, err := s.comparisonNormalizationFilter(probeRequest, mode)
		if err != nil {
			return comparisonDecoder{}, err
		}
		probe := slices.Clone(args)
		probeFilter := filter
		if request.Source.Input == nil {
			// Bound both references by the same native clock. Input -t is
			// relative to the preceding keyframe with noaccurate_seek and can
			// otherwise truncate hardware's copy of this exact neighborhood.
			_, tail, ok := strings.Cut(filter, ",")
			if !ok || !strings.HasPrefix(filter, "trim=") {
				return comparisonDecoder{}, errors.New("comparison decoder probe lacks a bounded file window")
			}
			start := request.Source.StartTimeSeconds + request.StartSeconds
			probeFilter = fmt.Sprintf("trim=start=%.9f:end=%.9f,%s", start, start+probeRequest.DurationSeconds, tail)
		} else {
			for i, arg := range probe {
				if arg == "-i" {
					probe = slices.Insert(probe, i, "-t", strconv.FormatFloat(probeRequest.DurationSeconds, 'f', 9, 64))
					break
				}
			}
		}
		probe = append(
			probe,
			"-vf",
			probeFilter+normalization+",showinfo@comparison_output",
			"-fps_mode",
			"passthrough",
			"-frames:v",
			"50000",
			"-pix_fmt",
			"gray",
			"-f",
			"rawvideo",
			"-",
		)
		if err := request.admitDecode(probeRequest.DurationSeconds); err != nil {
			return comparisonDecoder{}, err
		}
		runner := finiteComparisonRunner{
			base:           commandRunner{},
			outputLimit:    50000 * 256,
			retentionLimit: request.RetentionLimitBytes,
		}
		reference, err := runner.Run(ctx, executable, probe, "")
		if err != nil {
			return comparisonDecoder{}, err
		}
		observed, err := comparisonObservations(reference.Stderr)
		if err != nil || len(observed) == 0 {
			return comparisonDecoder{}, errors.New("comparison decoder reference lacks native provenance")
		}
		for _, frame := range observed {
			if err := validateComparisonNativeFacts(request.Source, frame); err != nil {
				return comparisonDecoder{}, err
			}
		}
		download := comparisonHardwareDownload(observed[0])
		if download == "" {
			return comparisonDecoder{}, nil
		}
		for _, candidate := range candidates {
			if err := request.admitDecode(probeRequest.DurationSeconds); err != nil {
				return comparisonDecoder{}, err
			}
			started := time.Now()
			candidate.download = download
			backend := candidate.backend
			hardwareProbe := comparisonHardwareProbeCommand(probe, candidate)
			if request.Source.Input == nil {
				for i, arg := range hardwareProbe {
					if arg == "-ss" {
						hardwareProbe = slices.Insert(hardwareProbe, i, "-noaccurate_seek")
						break
					}
				}
			}
			result, err := runComparisonHardwareProbe(ctx, runner, executable, hardwareProbe, "")
			if err != nil {
				if comparisonTerminalFailure(err) {
					return comparisonDecoder{}, err
				}
				if ctx.Err() != nil {
					return comparisonDecoder{}, context.Cause(ctx)
				}
				if !errors.Is(err, errComparisonHardwareInitialization) && !errors.Is(err, ErrComparisonWorkerFailed) &&
					!errors.Is(err, errComparisonHardwareUnavailable) {
					return comparisonDecoder{}, err
				}
				logging.FromContext(
					ctx,
					s.logger,
				).Debugf(
					"comparisons: phase=decoder backend=%s decision=unavailable probe_seconds=%.3f",
					backend,
					time.Since(started).Seconds(),
				)
				continue
			}
			if !comparisonDecoderEquivalent(reference, result) {
				logging.FromContext(
					ctx,
					s.logger,
				).Debugf(
					"comparisons: phase=decoder backend=%s decision=ineligible reason=canonical_output_mismatch probe_seconds=%.3f",
					backend,
					time.Since(started).Seconds(),
				)
				continue
			}
			logging.FromContext(
				ctx,
				s.logger,
			).Debugf(
				"comparisons: phase=decoder backend=%s decision=admitted probe_seconds=%.3f",
				backend,
				time.Since(started).Seconds(),
			)
			return candidate, nil
		}
		return comparisonDecoder{}, nil
	})
}

func comparisonDecoderInputs(ctx context.Context, executable string, source ComparisonSource, surface string) (string, []comparisonDecoder, error) {
	if err := comparisonAnalysisCapabilityFor(ctx, executable); err != nil {
		return "", nil, err
	}
	stat, err := os.Stat(executable) // #nosec G703 -- Resolved local FFmpeg executable, independent of the source media path.
	if err != nil {
		return "", nil, fmt.Errorf("identify comparison decoder executable: %w", err)
	}
	value, _ := comparisonAnalysisCapabilities.Load(comparisonCapabilityIdentity{executable, stat.Size(), stat.ModTime().UnixNano()})
	capability, ok := value.(comparisonAnalysisCapability)
	if !ok {
		return "", nil, errors.New("comparison executable changed during decoder admission")
	}
	backends := comparisonHardwareCandidates(runtime.GOOS, capability.hardware)
	if len(backends) == 0 {
		return "", nil, nil
	}
	device, err := comparisonDeviceIdentity(ctx)
	if err != nil {
		return "", nil, err
	}
	if device.fingerprint == "" {
		return "", nil, nil
	}
	var candidates []comparisonDecoder
	for _, backend := range backends {
		candidate := comparisonDecoder{backend: backend}
		switch backend {
		case "cuda":
			candidate.device = "0"
		case "d3d11va":
			candidate.device = "0"
			if device.d3dVendor != "" {
				candidate.device = "upbrrdecode"
				candidate.initDevice = "d3d11va=upbrrdecode:,vendor_id=" + device.d3dVendor
			}
		case "vaapi":
			if device.vaapiDevice == "" {
				continue
			}
			candidate.device = device.vaapiDevice
		}
		candidates = append(candidates, candidate)
	}
	signature := source.InputFingerprint
	if signature == "" {
		stat, err := os.Stat(source.Path)
		if err != nil {
			return "", nil, fmt.Errorf("identify comparison decoder source: %w", err)
		}
		fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
			Path           string
			Size, Modified int64
		}{source.Path, stat.Size(), stat.ModTime().UnixNano()})
		if err != nil {
			return "", nil, fmt.Errorf("fingerprint comparison decoder source: %w", err)
		}
		signature = string(fingerprint)
	}
	key, err := api.CanonicalWorkflowFingerprint(struct {
		Executable, Device, Surface, Policy, Source, Codec, Profile, Format, Colour, Chroma, Interlace string
		Width, Height                                                                                  int
		HDR                                                                                            api.HDRFacts
	}{capability.hash, device.fingerprint, surface, ComparisonAnalysisPolicyVersion, signature, source.Codec, source.CodecProfile, source.NativePixelFormat, source.NativeColour, source.NativeChromaLocation, source.Interlace, source.Width, source.Height, source.HDRFacts})
	if err != nil {
		return "", nil, fmt.Errorf("fingerprint comparison decoder capability: %w", err)
	}
	return string(key), candidates, nil
}

func (s *Service) admitComparisonDecoder(ctx context.Context, key string, probe func() (comparisonDecoder, error)) (comparisonDecoder, error) {
	for {
		comparisonDecoderDecisions.Lock()
		if entry, ok := comparisonDecoderDecisions.values[key]; ok {
			comparisonDecoderDecisions.Unlock()
			select {
			case <-entry.done:
				// A failed owner's attempt is not a receipt for an active waiter.
				// Its own probe must use its own context and work allowance.
				if ctx.Err() == nil && (errors.Is(entry.err, context.Canceled) || errors.Is(entry.err, context.DeadlineExceeded) ||
					errors.Is(entry.err, ErrComparisonFastInconclusive)) {
					continue
				}
				return entry.decoder, entry.err
			case <-ctx.Done():
				select {
				case <-entry.done:
					if comparisonTerminalFailure(entry.err) {
						return comparisonDecoder{}, entry.err
					}
				default:
				}
				return comparisonDecoder{}, fmt.Errorf("wait for comparison decoder admission: %w", context.Cause(ctx))
			}
		}
		entry := &comparisonDecoderDecision{done: make(chan struct{})}
		comparisonDecoderDecisions.values[key] = entry
		comparisonDecoderDecisions.Unlock()
		decoder, err := probe()
		decoder.key = key
		comparisonDecoderDecisions.Lock()
		entry.decoder, entry.err = decoder, err
		// Cancellation, exhaustion and integrity errors never become a negative
		// hardware capability receipt for an unrelated later operation.
		if err != nil {
			delete(comparisonDecoderDecisions.values, key)
		}
		close(entry.done)
		comparisonDecoderDecisions.Unlock()
		return decoder, err
	}
}

func pinComparisonSoftware(decoder comparisonDecoder) {
	if decoder.key == "" {
		return
	}
	comparisonDecoderDecisions.Lock()
	defer comparisonDecoderDecisions.Unlock()
	entry := &comparisonDecoderDecision{done: make(chan struct{})}
	close(entry.done)
	comparisonDecoderDecisions.values[decoder.key] = entry
}

func runComparisonHardwareProbe(ctx context.Context, runner Runner, executable string, args []string, dir string) (CommandResult, error) {
	probeCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := time.AfterFunc(3*time.Second, func() { cancel(errComparisonHardwareInitialization) })
	defer timer.Stop()
	probeCtx = context.WithValue(probeCtx, comparisonFirstNativeKey{}, func() { timer.Stop() })
	result, err := runner.Run(probeCtx, executable, args, dir)
	if err != nil {
		return result, fmt.Errorf("initialize comparison decoder: %w", err)
	}
	return result, nil
}

func comparisonHardwareProbeCommand(args []string, decoder comparisonDecoder) []string {
	command := comparisonHardwareCommand(args, decoder)
	for i, arg := range command {
		if arg == "-vf" && i+1 < len(command) {
			command[i+1] = strings.Replace(command[i+1], decoder.download, decoder.download+"showinfo@comparison_decoder_init,", 1)
		}
	}
	return command
}

func comparisonDecoderEquivalent(reference, candidate CommandResult) bool {
	a, aErr := comparisonObservations(reference.Stderr)
	b, bErr := comparisonObservations(candidate.Stderr)
	if aErr != nil || bErr != nil || len(a) == 0 || len(a) != len(b) || !bytes.Equal(reference.Stdout, candidate.Stdout) {
		return false
	}
	for i := range a {
		if a[i].PictureChecksum == "" || a[i].PlaneChecksums == "" || a[i].PixelFormat == "" || a[i].Colour == "" || a[i].ChromaLocation == "" || a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Service) runComparisonCaptureDecoder(
	ctx context.Context,
	runner Runner,
	executable string,
	args []string,
	dir string,
	source ComparisonSource,
	softwareOnly bool,
	surface string,
) (CommandResult, error) {
	var decoder comparisonDecoder
	if !softwareOnly && nativeComparisonRunner(runner) && source.NativePixelFormat != "" &&
		comparisonHardwareDownload(ComparisonFrameObservation{PixelFormat: source.NativePixelFormat}) != "" {
		key, candidates, err := comparisonDecoderInputs(ctx, executable, source, surface)
		if err != nil {
			return CommandResult{}, err
		}
		if len(candidates) > 0 {
			decoder, err = s.admitComparisonDecoder(ctx, key, func() (comparisonDecoder, error) {
				reference, err := runner.Run(ctx, executable, args, dir)
				if err != nil {
					return comparisonDecoder{}, fmt.Errorf("comparison capture reference: %w", err)
				}
				observed, err := comparisonObservations(reference.Stderr)
				if err != nil || len(observed) != 1 {
					return comparisonDecoder{}, errors.New("comparison capture reference lacks native provenance")
				}
				if err := validateComparisonNativeFacts(source, observed[0]); err != nil {
					return comparisonDecoder{}, err
				}
				download := comparisonHardwareDownload(observed[0])
				if download == "" {
					return comparisonDecoder{}, nil
				}
				for _, candidate := range candidates {
					candidate.download = download
					result, probeErr := runComparisonHardwareProbe(ctx, runner, executable, comparisonHardwareProbeCommand(args, candidate), dir)
					if probeErr != nil {
						if comparisonTerminalFailure(probeErr) {
							return comparisonDecoder{}, fmt.Errorf("comparison capture hardware probe: %w", probeErr)
						}
						if ctx.Err() != nil {
							return comparisonDecoder{}, context.Cause(ctx)
						}
						if errors.Is(probeErr, errComparisonHardwareInitialization) || errors.Is(probeErr, ErrComparisonWorkerFailed) ||
							errors.Is(probeErr, errComparisonHardwareUnavailable) {
							continue
						}
						return comparisonDecoder{}, fmt.Errorf("comparison capture hardware probe: %w", probeErr)
					}
					if comparisonDecoderEquivalent(reference, result) {
						return candidate, nil
					}
				}
				return comparisonDecoder{}, nil
			})
			if err != nil {
				return CommandResult{}, err
			}
		}
	}
	result, effective, err := s.runComparisonDecode(ctx, runner, executable, args, dir, decoder, nil)
	backend := effective.backend
	if backend == "" {
		backend = "software"
	}
	logging.FromContext(ctx, s.logger).Debugf("comparisons: phase=capture_decode decoder=%s surface=%s", backend, surface)
	return result, err
}

// Both comparison sampling and exact capture share this fallback boundary.
// Only a typed device outcome permits a same-command software retry; process,
// source, provenance, corruption, cancellation and admission failures stay closed.
func (s *Service) runComparisonDecode(
	ctx context.Context,
	runner Runner,
	executable string,
	args []string,
	dir string,
	decoder comparisonDecoder,
	request *ComparisonSampleRequest,
) (CommandResult, comparisonDecoder, error) {
	run := func(command []string) (CommandResult, error) {
		if ctx.Err() != nil {
			return CommandResult{}, context.Cause(ctx)
		}
		if request != nil {
			if err := request.admitDecode(request.DurationSeconds); err != nil {
				return CommandResult{}, err
			}
		}
		result, err := runner.Run(ctx, executable, command, dir)
		if request != nil && request.analysis != nil {
			request.analysis.totalMotionBytes += request.analysis.motion.bytes
			request.analysis.totalSparseBytes += request.analysis.sparse.bytes
		}
		if corruption := ffmpegFrameCorruptionError(result.Stderr); corruption != nil {
			return result, corruption
		}
		if comparisonTerminalFailure(err) {
			return result, fmt.Errorf("comparison decode evidence: %w", err)
		}
		if ctx.Err() != nil {
			return result, context.Cause(ctx)
		}
		if err != nil {
			return result, fmt.Errorf("comparison decode attempt: %w", err)
		}
		return result, nil
	}
	result, err := run(comparisonHardwareCommand(args, decoder))
	if decoder.backend == "" || !errors.Is(err, errComparisonHardwareUnavailable) || ctx.Err() != nil {
		return result, decoder, err
	}
	pinComparisonSoftware(decoder)
	if request != nil && request.analysis != nil {
		request.analysis.reset(ctx)
	}
	logger := logging.FromContext(ctx, s.logger)
	logger.Debugf("comparisons: phase=decoder backend=%s decision=software_retry reason=device_unavailable", decoder.backend)
	started := time.Now()
	result, err = run(args)
	logger.Debugf("comparisons: phase=decoder backend=software decision=retry_completed elapsed_seconds=%.3f", time.Since(started).Seconds())
	decoder.backend = ""
	return result, decoder, err
}
