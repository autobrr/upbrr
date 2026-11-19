// Copyright (c) 2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
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

var comparisonDecoderStates sync.Map // comparisonDecoderKey -> comparisonDecoderState

type comparisonDecoderKey struct{ source, backend string }
type comparisonDecoderState uint8

const (
	comparisonDecoderReady comparisonDecoderState = iota + 1
	comparisonDecoderUnsupported
)

const comparisonSoftwareDecoder = "software"

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

func comparisonHardwareDownload(source ComparisonSource) string {
	var surface string
	switch source.NativePixelFormat {
	case "yuv420p":
		surface = "nv12"
	case "yuv420p10le":
		surface = "p010le"
	default:
		return ""
	}
	filter := "hwdownload,format=" + surface + ",format=" + source.NativePixelFormat + ","
	// Download to the inspected native layout and restore known chroma siting.
	if slices.Contains([]string{"left", "center", "topleft", "top", "bottomleft", "bottom"}, source.NativeChromaLocation) {
		filter += "setparams=chroma_location=" + source.NativeChromaLocation + ","
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

var comparisonCodecHardware sync.Map // comparisonCodecHardwareKey -> []string

type comparisonCodecHardwareKey struct{ executable, codec string }

// FFmpeg may silently run a software decoder when the codec has no matching
// hardware implementation. Query the decoder's advertised input devices once.
func comparisonCodecHardwareDevices(ctx context.Context, executable string, key comparisonCodecHardwareKey) ([]string, error) {
	if value, ok := comparisonCodecHardware.Load(key); ok {
		if devices, valid := value.([]string); valid {
			return devices, nil
		}
	}
	runner := finiteComparisonRunner{base: commandRunner{}, outputLimit: 256 << 10}
	result, err := runner.Run(ctx, executable, []string{"-hide_banner", "-h", "decoder=" + key.codec}, "")
	if err != nil {
		return nil, err
	}
	var devices []string
	for line := range strings.SplitSeq(string(result.Stdout), "\n") {
		if names, ok := strings.CutPrefix(strings.TrimSpace(line), "Supported hardware devices:"); ok {
			devices = strings.Fields(names)
			break
		}
	}
	comparisonCodecHardware.Store(key, devices)
	return devices, nil
}

func comparisonDecoders(
	ctx context.Context,
	runner Runner,
	executable string,
	source ComparisonSource,
	softwareOnly bool,
) ([]comparisonDecoder, error) {
	if softwareOnly || !nativeComparisonRunner(runner) || source.NativeCodec == "" {
		return nil, nil
	}
	download := comparisonHardwareDownload(source)
	if download == "" {
		return nil, nil
	}
	capability, err := inspectComparisonCapabilities(ctx, executable)
	if err != nil {
		return nil, err
	}
	backends := comparisonHardwareCandidates(runtime.GOOS, capability.hardware)
	if len(backends) == 0 {
		return nil, nil
	}
	advertised, err := comparisonCodecHardwareDevices(ctx, executable, comparisonCodecHardwareKey{capability.hash, source.NativeCodec})
	if err != nil {
		return nil, err
	}
	backends = slices.DeleteFunc(backends, func(backend string) bool { return !slices.Contains(advertised, backend) })
	if len(backends) == 0 {
		return nil, nil
	}
	device, err := comparisonDeviceIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if device.fingerprint == "" {
		return nil, nil
	}
	var candidates []comparisonDecoder
	for _, backend := range backends {
		candidate := comparisonDecoder{backend: backend, download: download}
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
	if len(candidates) == 0 {
		return nil, nil
	}
	signature := source.InputFingerprint
	if signature == "" {
		stat, err := os.Stat(source.Path)
		if err != nil {
			return nil, fmt.Errorf("identify comparison decoder source: %w", err)
		}
		fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
			Path           string
			Size, Modified int64
		}{source.Path, stat.Size(), stat.ModTime().UnixNano()})
		if err != nil {
			return nil, fmt.Errorf("fingerprint comparison decoder source: %w", err)
		}
		signature = string(fingerprint)
	}
	key, err := api.CanonicalWorkflowFingerprint(struct {
		Executable, Device, Source, Codec, Profile, Format string
		Width, Height                                      int
	}{capability.hash, device.fingerprint, signature, source.NativeCodec, source.CodecProfile, source.NativePixelFormat, source.Width, source.Height})
	if err != nil {
		return nil, fmt.Errorf("fingerprint comparison decoder capability: %w", err)
	}
	for i := range candidates {
		candidates[i].key = string(key)
	}
	return candidates, nil
}

func runComparisonHardwareDecode(ctx context.Context, runner Runner, executable string, args []string, dir string) (CommandResult, error) {
	decodeCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := time.AfterFunc(3*time.Second, func() { cancel(errComparisonHardwareInitialization) })
	defer timer.Stop()
	decodeCtx = context.WithValue(decodeCtx, comparisonFirstNativeKey{}, func() { timer.Stop() })
	result, err := runner.Run(decodeCtx, executable, args, dir)
	if err != nil {
		return result, fmt.Errorf("run hardware comparison decoder: %w", err)
	}
	return result, nil
}

func (s *Service) runComparisonCaptureDecoder(
	ctx context.Context,
	runner Runner,
	executable string,
	args []string,
	dir string,
	source ComparisonSource,
	surface string,
	displayGeometry bool,
) (CommandResult, error) {
	filterIndex := slices.Index(args, "-vf")
	useLibplacebo := filterIndex >= 0 && strings.Contains(args[filterIndex+1], "libplacebo=")
	decoders, err := comparisonDecoders(ctx, runner, executable, source, useLibplacebo || displayGeometry && source.ScreenshotMatrix)
	if err != nil {
		return CommandResult{}, err
	}
	result, err := s.runComparisonDecode(ctx, runner, executable, args, dir, decoders, nil)
	if err == nil && result.ExitCode == 0 && displayGeometry && result.comparisonBackend != comparisonSoftwareDecoder &&
		comparisonDisplayMatrix.Match(result.Stderr) {
		// Hardware frames can carry the rotation through to output metadata.
		// Software decoding applies the display matrix before geometry filters.
		result, err = s.runComparisonDecode(ctx, runner, executable, args, dir, nil, nil)
	}
	if err == nil {
		logging.FromContext(ctx, s.logger).Debugf("comparisons: phase=capture_decode decoder=%s surface=%s", result.comparisonBackend, surface)
	}
	return result, err
}

// Only a hardware availability failure permits another decoder to try the
// same command. The caller verifies native evidence before using the result.
func (s *Service) runComparisonDecode(
	ctx context.Context,
	runner Runner,
	executable string,
	args []string,
	dir string,
	decoders []comparisonDecoder,
	request *ComparisonSampleRequest,
) (CommandResult, error) {
	run := func(decoder comparisonDecoder, initialize bool) (CommandResult, error) {
		if ctx.Err() != nil {
			return CommandResult{}, context.Cause(ctx)
		}
		if request != nil {
			if err := request.admitDecode(request.DurationSeconds); err != nil {
				return CommandResult{}, err
			}
		}
		if initialize {
			// Native decode can begin long before trim or exact-frame selection yields output.
			decoder.download += "showinfo@comparison_decoder_init=checksum=0,"
		}
		command := comparisonHardwareCommand(args, decoder)
		var result CommandResult
		var err error
		if initialize {
			result, err = runComparisonHardwareDecode(ctx, runner, executable, command, dir)
		} else {
			result, err = runner.Run(ctx, executable, command, dir)
		}
		backend := decoder.backend
		if backend == "" {
			backend = comparisonSoftwareDecoder
		}
		result.comparisonBackend = backend
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
	for _, decoder := range decoders {
		key := comparisonDecoderKey{decoder.key, decoder.backend}
		state, _ := comparisonDecoderStates.Load(key)
		if state == comparisonDecoderUnsupported {
			continue
		}
		result, err := run(decoder, state != comparisonDecoderReady)
		if err == nil {
			comparisonDecoderStates.LoadOrStore(key, comparisonDecoderReady)
			return result, nil
		}
		unsupported := errors.Is(err, errComparisonHardwareUnsupported)
		if !unsupported && !errors.Is(err, errComparisonHardwareUnavailable) && !errors.Is(err, errComparisonHardwareInitialization) {
			return result, err
		}
		if ctx.Err() != nil {
			return CommandResult{}, fmt.Errorf("comparison decoder fallback canceled: %w", context.Cause(ctx))
		}
		if unsupported {
			comparisonDecoderStates.Store(key, comparisonDecoderUnsupported)
		} else {
			comparisonDecoderStates.CompareAndDelete(key, comparisonDecoderReady)
		}
		if request != nil && request.analysis != nil {
			request.analysis.reset(ctx)
		}
		logging.FromContext(ctx, s.logger).Debugf("comparisons: phase=decoder backend=%s decision=fallback reason=hardware_unavailable", decoder.backend)
	}
	return run(comparisonDecoder{}, false)
}
