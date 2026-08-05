// Package engine coordinates bounded DVD menu inventory, navigation-state
// discovery, exact FFmpeg background decoding, and Go subpicture composition.
package engine

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/dvdvideo/graph"
	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
	"github.com/autobrr/upbrr/internal/dvdvideo/nav"
	"github.com/autobrr/upbrr/internal/dvdvideo/render"
	"github.com/autobrr/upbrr/internal/dvdvideo/spu"
)

const (
	// Version identifies the bundled pure-Go DVD menu engine implementation.
	Version = "phase0a-1"
	// DefaultMaxMenuItems is the default selected-screen limit.
	DefaultMaxMenuItems = graph.DefaultMaxItems
	// DefaultProcessTimeout bounds each FFmpeg probe or frame operation.
	DefaultProcessTimeout = 30 * time.Second
	blackPixelThreshold   = 3
)

// ErrCapture identifies invalid setup or a failed DVD menu capture stage.
var ErrCapture = errors.New("DVD menu capture failed")

// Options bound traversal, menu-VOB scanning, and each FFmpeg operation.
type Options struct {
	// Traversal controls deterministic graph discovery and selection limits.
	Traversal graph.Options
	// Scan controls bounded NAV and subpicture reads for each menu cell.
	Scan nav.CellScanOptions
	// ProcessTimeout bounds each FFmpeg probe and frame decode; zero uses the default.
	ProcessTimeout time.Duration
	// Deinterlace enables frame deinterlacing before SPU composition.
	Deinterlace bool
	// Capability reuses a prior successful FFmpeg probe when non-nil.
	Capability *render.Capability
	// Progress receives synchronous path-free phase and counter updates.
	Progress func(Progress)
	// Logger receives optional path-free discovery and rendering diagnostics.
	Logger graph.Logger
}

// Progress reports the current engine phase and bounded capture counters.
type Progress struct {
	// Phase is discovering or capturing.
	Phase string
	// Inventoried is the number of structural menu programs.
	Inventoried int
	// VisitedStates is the number of distinct VM states evaluated.
	VisitedStates int
	// VisitedButtons is the number of button commands evaluated.
	VisitedButtons int
	// Captured is the number of composed non-black frames retained.
	Captured int
	// Warnings is the number of distinct coverage warnings.
	Warnings int
}

// Capture is one composed, non-black menu screen and its engine coordinate.
type Capture struct {
	// Coordinate is the exact inventory location rendered by FFmpeg.
	Coordinate graph.Coordinate
	// Discovery records whether navigation or structural inventory found the screen.
	Discovery graph.Discovery
	// Image is the owned composed menu frame.
	Image *image.NRGBA
	// HasOverlay reports that decoded SPU state was composited.
	HasOverlay bool
	// HasHighlight reports that a selected-button highlight was composited.
	HasHighlight bool
}

// Result reports capture output and bounded coverage state without local paths.
type Result struct {
	// EngineVersion identifies the engine implementation that produced the result.
	EngineVersion string
	// Capability is the successful path-free FFmpeg probe result.
	Capability render.Capability
	// Captures contains composed non-black menu frames in selection order.
	Captures []Capture
	// Inventoried is the number of structural menu programs.
	Inventoried int
	// Selected is the number of distinct screens selected for rendering.
	Selected int
	// VisitedStates is the number of distinct VM states evaluated.
	VisitedStates int
	// VisitedButtons is the number of button commands evaluated.
	VisitedButtons int
	// Complete reports that every selected screen rendered without coverage warnings.
	Complete bool
	// Partial reports incomplete discovery, live-state, or rendering coverage.
	Partial bool
	// Truncated reports that the traversal item limit excluded eligible screens.
	Truncated bool
	// Warnings contains deduplicated path-free coverage diagnostics.
	Warnings []graph.Warning
}

// CaptureDirectory inventories and captures menus from an extracted VIDEO_TS
// directory. The caller controls the FFmpeg executable and process runner.
// It returns partial counters and warnings when discovery or rendering fails.
func CaptureDirectory(ctx context.Context, root string, runner render.Runner, executable string, opts Options) (Result, error) {
	if strings.TrimSpace(root) == "" {
		return Result{}, fmt.Errorf("%w: empty VIDEO_TS source", ErrCapture)
	}
	if runner == nil || strings.TrimSpace(executable) == "" {
		return Result{}, fmt.Errorf("%w: FFmpeg unavailable", ErrCapture)
	}
	if opts.ProcessTimeout == 0 {
		opts.ProcessTimeout = DefaultProcessTimeout
	}
	if opts.ProcessTimeout < 0 {
		return Result{}, fmt.Errorf("%w: negative process timeout", ErrCapture)
	}
	if opts.Traversal.MaxItems == 0 {
		opts.Traversal.MaxItems = DefaultMaxMenuItems
	}
	if opts.Traversal.MaxItems < 0 || opts.Traversal.MaxItems > graph.MaxMenuItems {
		return Result{}, fmt.Errorf("%w: menu item limit must be between 1 and %d", ErrCapture, graph.MaxMenuItems)
	}
	if opts.Logger != nil {
		opts.Logger.Debugf(
			"DVD menus: engine preflight started engine_version=%s max_items=%d process_timeout_ms=%d deinterlace=%t",
			Version,
			opts.Traversal.MaxItems,
			opts.ProcessTimeout.Milliseconds(),
			opts.Deinterlace,
		)
	}

	disc, err := ifo.InspectDirectory(root)
	if err != nil {
		return Result{}, fmt.Errorf("%w: DVD inventory unavailable", ErrCapture)
	}
	if opts.Logger != nil {
		recovered := 0
		if disc.Manager.Recovered {
			recovered++
		}
		for _, titleSet := range disc.TitleSets {
			if titleSet.Recovered {
				recovered++
			}
		}
		opts.Logger.Debugf(
			"DVD menus: inventory ready manager=%t title_sets=%d recovered_ifos=%d",
			disc.Manager.Kind == ifo.KindManager,
			len(disc.TitleSets),
			recovered,
		)
	}
	var capability render.Capability
	if opts.Capability != nil {
		capability = *opts.Capability
		if !capability.Available {
			return Result{}, fmt.Errorf("%w: %w", ErrCapture, render.ErrCapability)
		}
	} else {
		probeCtx, cancelProbe := context.WithTimeout(ctx, opts.ProcessTimeout)
		capability, err = render.Probe(probeCtx, runner, executable)
		cancelProbe()
		if err != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrCapture, err)
		}
	}
	if opts.Logger != nil {
		opts.Logger.Debugf("DVD menus: FFmpeg capability ready dvdvideo=%t options=%d", capability.Available, len(capability.Options))
	}
	resolver, err := newDirectoryResolver(root, opts.Scan)
	if err != nil {
		return Result{}, fmt.Errorf("%w: menu source unavailable", ErrCapture)
	}
	if opts.Logger != nil {
		opts.Logger.Debugf("DVD menus: menu sources ready vob_sets=%d", len(resolver.files))
	}
	return captureDisc(ctx, root, disc, resolver, runner, executable, capability, opts)
}

// captureDisc resolves selected graph screens again, decodes their exact
// background frames, composites live SPU state, and retains non-black output.
func captureDisc(
	ctx context.Context,
	root string,
	disc ifo.Disc,
	resolver *directoryResolver,
	runner render.Runner,
	executable string,
	capability render.Capability,
	opts Options,
) (Result, error) {
	opts.Traversal.Logger = opts.Logger
	opts.Traversal.Progress = func(update graph.Progress) {
		reportProgress(opts.Progress, Progress{
			Phase:          "discovering",
			Inventoried:    update.Inventoried,
			VisitedStates:  update.VisitedStates,
			VisitedButtons: update.VisitedButtons,
			Warnings:       update.Warnings,
		})
	}
	discovered, err := graph.Discover(ctx, disc, resolver, opts.Traversal)
	result := Result{
		EngineVersion:  Version,
		Capability:     capability,
		Inventoried:    discovered.Inventoried,
		Selected:       len(discovered.Screens),
		VisitedStates:  discovered.VisitedStates,
		VisitedButtons: discovered.VisitedButtons,
		Partial:        discovered.Partial,
		Truncated:      discovered.Truncated,
		Warnings:       append([]graph.Warning(nil), discovered.Warnings...),
	}
	if err != nil {
		return result, fmt.Errorf("%w: menu discovery", ErrCapture)
	}
	if opts.Logger != nil {
		opts.Logger.Debugf(
			"DVD menus: rendering started selected=%d inventoried=%d states=%d buttons=%d",
			len(discovered.Screens),
			discovered.Inventoried,
			discovered.VisitedStates,
			discovered.VisitedButtons,
		)
	}
	reportEngineProgress(opts.Progress, result)

	for index, screen := range discovered.Screens {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("%w: %w", ErrCapture, err)
		}
		if opts.Logger != nil {
			opts.Logger.Debugf(
				"DVD menus: render started index=%d discovery=%s domain=%s vts=%d language_unit=%d menu_id=%d pgc=%d pg=%d cell=%d buttons=%d",
				index+1,
				screen.Discovery,
				engineDomainLabel(screen.Coordinate.Kind),
				screen.Coordinate.VTS,
				screen.Coordinate.LanguageUnit,
				screen.Coordinate.MenuID,
				screen.Coordinate.PGC,
				screen.Coordinate.Program,
				screen.Coordinate.Cell,
				len(screen.Live.Buttons),
			)
		}
		detail, resolveErr := resolver.resolve(ctx, screen.Coordinate, resolver.programChain(screen.Coordinate), screen.Registers)
		if resolveErr != nil {
			if markPartial(&result, "live_state", "menu NAV/SPU state could not be resolved") {
				logEngineWarning(opts.Logger, "live_state", screen.Coordinate)
			}
			reportEngineProgress(opts.Progress, result)
			continue
		}
		if detail.scanTruncated {
			if markPartial(&result, "nav_scan_limit", "menu NAV/SPU sector scan limit reached") {
				logEngineWarning(opts.Logger, "nav_scan_limit", screen.Coordinate)
			}
		}
		if opts.Logger != nil {
			opts.Logger.Tracef(
				"DVD menus: render state index=%d target_ms=%d scan_truncated=%t overlay=%t highlight=%t buttons=%d",
				index+1,
				detail.target.Milliseconds(),
				detail.scanTruncated,
				detail.hasOverlay,
				detail.highlight != nil,
				len(detail.buttons),
			)
		}

		frameCtx, cancelFrame := context.WithTimeout(ctx, opts.ProcessTimeout)
		background, frameErr := render.DecodeFrame(frameCtx, runner, executable, render.FrameRequest{
			SourcePath:   root,
			VTS:          screen.Coordinate.VTS,
			LanguageUnit: screen.Coordinate.LanguageUnit,
			PGC:          screen.Coordinate.PGC,
			Program:      screen.Coordinate.Program,
			Target:       detail.target,
			Deinterlace:  opts.Deinterlace,
		})
		cancelFrame()
		if frameErr != nil {
			if markPartial(&result, "frame_decode", "inventory-selected menu frame could not be decoded") {
				logEngineWarning(opts.Logger, "frame_decode", screen.Coordinate)
			}
			reportEngineProgress(opts.Progress, result)
			continue
		}
		if opts.Logger != nil {
			bounds := background.Bounds()
			opts.Logger.Debugf(
				"DVD menus: frame decoded index=%d width=%d height=%d target_ms=%d",
				index+1,
				bounds.Dx(),
				bounds.Dy(),
				detail.target.Milliseconds(),
			)
		}

		composed := copyImage(background)
		if detail.hasOverlay {
			composed = spu.Composite(background, detail.overlay, detail.palette, detail.highlight)
		} else if markPartial(&result, "spu_unavailable", "menu subpicture state was not available") {
			logEngineWarning(opts.Logger, "spu_unavailable", screen.Coordinate)
		}
		if len(detail.buttons) != 0 && detail.highlight == nil {
			if markPartial(&result, "highlight_unavailable", "default menu highlight state was not available") {
				logEngineWarning(opts.Logger, "highlight_unavailable", screen.Coordinate)
			}
		}
		if imageIsBlack(composed) {
			if markPartial(&result, "black_frame", "decoded menu frame was black") {
				logEngineWarning(opts.Logger, "black_frame", screen.Coordinate)
			}
			reportEngineProgress(opts.Progress, result)
			continue
		}
		result.Captures = append(result.Captures, Capture{
			Coordinate:   screen.Coordinate,
			Discovery:    screen.Discovery,
			Image:        composed,
			HasOverlay:   detail.hasOverlay,
			HasHighlight: detail.highlight != nil,
		})
		if opts.Logger != nil {
			opts.Logger.Debugf(
				"DVD menus: capture stored index=%d discovery=%s overlay=%t highlight=%t captured=%d",
				index+1,
				screen.Discovery,
				detail.hasOverlay,
				detail.highlight != nil,
				len(result.Captures),
			)
		}
		reportEngineProgress(opts.Progress, result)
	}

	result.Complete = discovered.Complete && !result.Partial && !result.Truncated && len(result.Captures) == len(discovered.Screens)
	if opts.Logger != nil {
		opts.Logger.Debugf(
			"DVD menus: rendering complete selected=%d captured=%d partial=%t truncated=%t warnings=%d",
			len(discovered.Screens),
			len(result.Captures),
			result.Partial,
			result.Truncated,
			len(result.Warnings),
		)
	}
	if len(result.Captures) == 0 {
		return result, fmt.Errorf("%w: no menu frames captured", ErrCapture)
	}
	return result, nil
}

func reportEngineProgress(reporter func(Progress), result Result) {
	reportProgress(reporter, Progress{
		Phase:          "capturing",
		Inventoried:    result.Inventoried,
		VisitedStates:  result.VisitedStates,
		VisitedButtons: result.VisitedButtons,
		Captured:       len(result.Captures),
		Warnings:       len(result.Warnings),
	})
}

func reportProgress(reporter func(Progress), update Progress) {
	if reporter != nil {
		reporter(update)
	}
}

func copyImage(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	result := image.NewNRGBA(bounds)
	draw.Draw(result, bounds, source, bounds.Min, draw.Src)
	return result
}

// imageIsBlack reports whether every non-transparent pixel stays within the
// engine's near-black threshold.
func imageIsBlack(source image.Image) bool {
	bounds := source.Bounds()
	if bounds.Empty() {
		return true
	}
	threshold := uint32(blackPixelThreshold * 257)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := source.At(x, y).RGBA()
			if a != 0 && (r > threshold || g > threshold || b > threshold) {
				return false
			}
		}
	}
	return true
}

// markPartial records one warning code and reports whether it was newly added.
func markPartial(result *Result, code, message string) bool {
	result.Partial = true
	result.Complete = false
	for _, warning := range result.Warnings {
		if warning.Code == code {
			return false
		}
	}
	result.Warnings = append(result.Warnings, graph.Warning{Code: code, Message: message})
	return true
}

func logEngineWarning(logger graph.Logger, code string, coordinate graph.Coordinate) {
	if logger == nil {
		return
	}
	logger.Warnf(
		"DVD menus: render warning code=%s detail=%q domain=%s vts=%d language_unit=%d pgc=%d pg=%d cell=%d",
		code,
		renderWarningDetail(code),
		engineDomainLabel(coordinate.Kind),
		coordinate.VTS,
		coordinate.LanguageUnit,
		coordinate.PGC,
		coordinate.Program,
		coordinate.Cell,
	)
}

func renderWarningDetail(code string) string {
	switch code {
	case "live_state":
		return "menu NAV/SPU state could not be resolved"
	case "nav_scan_limit":
		return "menu NAV/SPU sector scan limit reached"
	case "frame_decode":
		return "inventory-selected menu frame could not be decoded"
	case "spu_unavailable":
		return "menu subpicture state was not available"
	case "highlight_unavailable":
		return "default menu highlight state was not available"
	case "black_frame":
		return "decoded menu frame was black"
	}
	return "DVD menu render warning"
}

func engineDomainLabel(kind ifo.Kind) string {
	switch kind {
	case ifo.KindManager:
		return "vmg"
	case ifo.KindTitleSet:
		return "vtsm"
	}
	return "unknown"
}
