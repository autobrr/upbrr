// Package graph performs deterministic, bounded DVD menu discovery over cloned
// pure-Go VM state and reconciles reachable results with the IFO inventory.
package graph

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
	"github.com/autobrr/upbrr/internal/dvdvideo/nav"
	"github.com/autobrr/upbrr/internal/dvdvideo/vm"
)

const (
	// DefaultMaxItems is the default maximum number of selected menu screens.
	DefaultMaxItems = 6
	// DefaultMaxStates is the default bound on distinct VM states.
	DefaultMaxStates = 4096
	// DefaultMaxDepth is the default bound on one navigation branch.
	DefaultMaxDepth = 64
	// DefaultMaxButtons is the default bound on evaluated button commands.
	DefaultMaxButtons = 16_384
	// MaxMenuItems is the hard upper bound on selected menu screens.
	MaxMenuItems = 32
)

// ErrDiscovery identifies invalid options or incomplete DVD menu graph setup.
var ErrDiscovery = errors.New("DVD menu discovery failed")

// Discovery identifies how a visible menu was found.
type Discovery string

// Menu discovery sources emitted in [Screen] values.
const (
	// DiscoveryReachable marks a screen reached through deterministic VM navigation.
	DiscoveryReachable Discovery = "reachable"
	// DiscoveryStructural marks an interactive screen found only in the IFO inventory.
	DiscoveryStructural Discovery = "structural"
)

// Coordinate is one exact FFmpeg-addressable menu location.
type Coordinate struct {
	// Kind selects the manager or title-set menu domain.
	Kind ifo.Kind
	// VTS is zero for the manager domain or the one-based title-set number.
	VTS int
	// LanguageUnit is the one-based menu language-unit index.
	LanguageUnit int
	// PGC is the one-based menu program-chain index.
	PGC int
	// Program is the one-based program index within PGC.
	Program int
	// Cell is the one-based cell containing Program.
	Cell int
	// MenuID is the authored low-nibble menu identifier.
	MenuID uint8
}

// LiveState is NAV/SPU state resolved for one coordinate and VM checkpoint.
type LiveState struct {
	// Visible reports that button, SPU, highlight, still, or entry evidence identifies a menu screen.
	Visible bool
	// Buttons contains the selected live PCI display group's authored controls.
	Buttons []nav.Button
	// Fingerprint hashes live NAV timing and available SPU pixels for deduplication.
	Fingerprint []byte
	// HasOverlay reports that active menu subpicture state was decoded.
	HasOverlay bool
	// HasHighlight reports that a default selected-button highlight was resolved.
	HasHighlight bool
	// AuthoredStill reports that the PGC or selected cell has nonzero still time.
	AuthoredStill bool
	// AuthoredEntry reports that the first program is an authored entry PGC.
	AuthoredEntry bool
}

// Resolver reads live NAV/SPU state without controlling traversal.
type Resolver interface {
	// Resolve returns live state for one inventory-validated coordinate and register snapshot.
	Resolve(ctx context.Context, coordinate Coordinate, pgc ifo.ProgramChain, registers vm.Registers) (LiveState, error)
}

// Logger receives path-free traversal diagnostics. It intentionally mirrors
// only the log levels used by the DVD engine.
type Logger interface {
	// Tracef records near-complete VM action flow.
	Tracef(format string, args ...any)
	// Debugf records inventory, classification, branch, and deduplication decisions.
	Debugf(format string, args ...any)
	// Infof records top-level progress when used by an embedding service.
	Infof(format string, args ...any)
	// Warnf records distinct incomplete-coverage conditions.
	Warnf(format string, args ...any)
}

// Options are independent deterministic traversal/storage budgets.
type Options struct {
	// Language selects the preferred two-byte DVD language code.
	Language string
	// MaxItems bounds selected screens; zero uses DefaultMaxItems.
	MaxItems int
	// MaxStates bounds distinct VM states; zero uses DefaultMaxStates.
	MaxStates int
	// MaxDepth bounds one traversal branch; zero uses DefaultMaxDepth.
	MaxDepth int
	// MaxButtons bounds evaluated button commands; zero uses DefaultMaxButtons.
	MaxButtons int
	// Registers is cloned as the initial VM state for every seed.
	Registers vm.Registers
	// VM controls deterministic command execution for traversal.
	VM vm.Options
	// Progress receives synchronous path-free counter snapshots when non-nil.
	Progress func(Progress)
	// Logger receives optional path-free diagnostics.
	Logger Logger
}

// Progress reports bounded discovery counters without disc-identifying data.
type Progress struct {
	// Inventoried is the number of structural menu programs.
	Inventoried int
	// VisitedStates is the number of distinct VM states evaluated.
	VisitedStates int
	// VisitedButtons is the number of button commands evaluated.
	VisitedButtons int
	// Screens is the number of distinct menu screens selected.
	Screens int
	// Warnings is the number of distinct coverage warnings.
	Warnings int
}

// Warning is a stable graph coverage warning.
type Warning struct {
	// Code is a stable machine-readable coverage identifier.
	Code string
	// Message is a path-free user-facing description.
	Message string
}

// Screen is one semantically distinct visible authored menu state.
type Screen struct {
	// Coordinate is the exact inventory location used for frame rendering.
	Coordinate Coordinate
	// Discovery records whether navigation or structural inventory found the screen.
	Discovery Discovery
	// Identity is the deterministic semantic screen fingerprint.
	Identity [32]byte
	// Registers is the cloned VM checkpoint used to resolve live state again.
	Registers vm.Registers
	// Live is the state used to classify and deduplicate the screen.
	Live LiveState
}

// Result reports bounded discovery and structural reconciliation.
type Result struct {
	// Screens contains distinct selected screens in deterministic discovery order.
	Screens []Screen
	// Inventoried is the number of structural menu programs.
	Inventoried int
	// VisitedStates is the number of distinct VM states evaluated.
	VisitedStates int
	// VisitedButtons is the number of button commands evaluated.
	VisitedButtons int
	// Complete reports full traversal without warnings or item truncation.
	Complete bool
	// Partial reports incomplete traversal or live-state classification coverage.
	Partial bool
	// Truncated reports that MaxItems excluded one or more eligible screens.
	Truncated bool
	// Warnings contains deduplicated path-free coverage diagnostics.
	Warnings []Warning
}

type node struct {
	coordinate Coordinate
	pgc        ifo.ProgramChain
}

type state struct {
	node      node
	registers vm.Registers
	depth     int
	runPre    bool
	utility   bool
}

// Discover traverses reachable entry PGCs first, then classifies visible
// unreached inventory entries as structural coverage. Progress callbacks run
// synchronously, and cancellation returns the counters accumulated so far.
func Discover(ctx context.Context, disc ifo.Disc, resolver Resolver, opts Options) (Result, error) {
	if resolver == nil {
		return Result{}, fmt.Errorf("%w: nil live-state resolver", ErrDiscovery)
	}
	if opts.MaxItems == 0 {
		opts.MaxItems = DefaultMaxItems
	}
	if opts.MaxItems < 0 || opts.MaxItems > MaxMenuItems {
		return Result{}, fmt.Errorf("%w: item limit must be between 1 and %d", ErrDiscovery, MaxMenuItems)
	}
	if opts.MaxStates == 0 {
		opts.MaxStates = DefaultMaxStates
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	if opts.MaxButtons == 0 {
		opts.MaxButtons = DefaultMaxButtons
	}
	if opts.MaxStates < 0 || opts.MaxDepth < 0 || opts.MaxButtons < 0 {
		return Result{}, fmt.Errorf("%w: negative traversal limit", ErrDiscovery)
	}

	nodes, seeds := inventory(disc, opts.Language)
	result := Result{Inventoried: len(nodes)}
	if opts.Logger != nil {
		opts.Logger.Debugf(
			"DVD menus: discovery started language=%s inventoried=%d seeds=%d max_items=%d max_states=%d max_depth=%d max_buttons=%d",
			opts.Language,
			len(nodes),
			len(seeds),
			opts.MaxItems,
			opts.MaxStates,
			opts.MaxDepth,
			opts.MaxButtons,
		)
	}
	reportProgress(opts.Progress, result)
	if len(nodes) == 0 {
		return result, fmt.Errorf("%w: no menu program chains", ErrDiscovery)
	}
	lookup := make(map[nodeKey]node, len(nodes))
	for _, item := range nodes {
		if _, exists := lookup[key(item.coordinate)]; !exists {
			lookup[key(item.coordinate)] = item
		}
	}
	queue := make([]state, 0, len(seeds))
	fallbackSeed := len(seeds) == 1 && seeds[0].pgc.EntryID&0x80 == 0
	for _, seed := range seeds {
		queue = append(queue, state{
			node:      seed,
			registers: opts.Registers.Clone(),
			runPre:    true,
			utility:   fallbackSeed && seed.coordinate.Kind == ifo.KindManager && seed.coordinate.MenuID == 0,
		})
	}
	seen := make(map[[32]byte]struct{})
	reachedCoordinates := make(map[Coordinate]struct{})
	classifiedCoordinates := make(map[Coordinate]struct{})
	screenIDs := make(map[[32]byte]struct{})
	controlLayouts := make(map[[32]byte][][]nav.Button)
	limitSkipped := 0
	utilityScreens := 0
	structuralTransitions := 0
	structuralNonInteractive := 0
	structuralDuplicates := 0
	structuralVisible := 0
	structuralFailures := 0

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrDiscovery, err)
		}
		current := queue[0]
		queue = queue[1:]
		if current.depth > opts.MaxDepth {
			if markPartial(&result, "depth_limit", "menu branch depth limit reached") {
				logTraversalWarning(opts.Logger, "depth_limit", current.node.coordinate)
			}
			continue
		}
		identity := traversalIdentity(current.node.coordinate, current.registers, current.runPre)
		if _, ok := seen[identity]; ok {
			if opts.Logger != nil {
				opts.Logger.Debugf(
					"DVD menus: state skipped decision=dedupe reason=state_identity domain=%s vts=%d language_unit=%d menu_id=%d pgc=%d pg=%d cell=%d state=%x",
					domainLabel(current.node.coordinate.Kind),
					current.node.coordinate.VTS,
					current.node.coordinate.LanguageUnit,
					current.node.coordinate.MenuID,
					current.node.coordinate.PGC,
					current.node.coordinate.Program,
					current.node.coordinate.Cell,
					identity[:6],
				)
			}
			continue
		}
		if len(seen) >= opts.MaxStates {
			if markPartial(&result, "state_limit", "menu state limit reached") {
				logTraversalWarning(opts.Logger, "state_limit", current.node.coordinate)
			}
			break
		}
		seen[identity] = struct{}{}
		result.VisitedStates++
		if opts.Logger != nil {
			opts.Logger.Tracef(
				"DVD menus: state visiting domain=%s vts=%d language_unit=%d menu_id=%d pgc=%d pg=%d cell=%d depth=%d run_pre=%t state=%x queue=%d",
				domainLabel(current.node.coordinate.Kind),
				current.node.coordinate.VTS,
				current.node.coordinate.LanguageUnit,
				current.node.coordinate.MenuID,
				current.node.coordinate.PGC,
				current.node.coordinate.Program,
				current.node.coordinate.Cell,
				current.depth,
				current.runPre,
				identity[:6],
				len(queue),
			)
		}
		reportProgress(opts.Progress, result)

		registers := current.registers.Clone()
		if current.runPre {
			preResult, err := vm.Execute(current.node.pgc.PreCommands, &registers, opts.VM)
			if err != nil {
				if markPartial(&result, "pre_command", "menu pre-command failed") {
					logTraversalWarning(opts.Logger, "pre_command", current.node.coordinate)
				}
				continue
			}
			if opts.Logger != nil {
				opts.Logger.Tracef(
					"DVD menus: action evaluated action=pre commands=%d executed=%d has_link=%t link=%d",
					len(current.node.pgc.PreCommands),
					preResult.Executed,
					preResult.HasLink,
					preResult.Link.Command,
				)
			}
			if preResult.HasLink {
				target, terminal, ok := resolveLink(current.node, preResult.Link, lookup)
				if ok {
					if opts.Logger != nil {
						opts.Logger.Debugf(
							"DVD menus: branch queued action=pre decision=follow link=%d target_domain=%s target_vts=%d target_pgc=%d target_pg=%d",
							preResult.Link.Command,
							domainLabel(target.coordinate.Kind),
							target.coordinate.VTS,
							target.coordinate.PGC,
							target.coordinate.Program,
						)
					}
					queue = append(queue, transition(current, target, registers))
					continue
				}
				if !terminal {
					if markPartial(&result, "unsupported_pre_link", "menu pre-command target was not resolved") {
						logTraversalWarning(opts.Logger, "unsupported_pre_link", current.node.coordinate)
					}
				} else if opts.Logger != nil {
					opts.Logger.Debugf("DVD menus: branch stopped action=pre decision=terminal link=%d", preResult.Link.Command)
				}
				continue
			}
		}

		live, err := resolver.Resolve(ctx, current.node.coordinate, current.node.pgc, registers.Clone())
		if err != nil {
			if markPartial(&result, "live_state", "menu NAV/SPU state could not be resolved") {
				logTraversalWarning(opts.Logger, "live_state", current.node.coordinate)
			}
			continue
		}
		if opts.Logger != nil {
			opts.Logger.Debugf(
				"DVD menus: state resolved domain=%s vts=%d language_unit=%d menu_id=%d pgc=%d pg=%d cell=%d visible=%t buttons=%d overlay=%t highlight=%t authored_still=%t authored_entry=%t fingerprint=%s",
				domainLabel(current.node.coordinate.Kind),
				current.node.coordinate.VTS,
				current.node.coordinate.LanguageUnit,
				current.node.coordinate.MenuID,
				current.node.coordinate.PGC,
				current.node.coordinate.Program,
				current.node.coordinate.Cell,
				live.Visible,
				len(live.Buttons),
				live.HasOverlay,
				live.HasHighlight,
				live.AuthoredStill,
				live.AuthoredEntry,
				fingerprintPrefix(live.Fingerprint),
			)
		}
		reachedCoordinates[current.node.coordinate] = struct{}{}
		classifiedCoordinates[current.node.coordinate] = struct{}{}
		if live.Visible && !current.utility {
			screenID := semanticIdentity(current.node.coordinate, registers, live)
			controlID, hasControls := controlIdentity(current.node.coordinate, live)
			_, duplicateScreen := screenIDs[screenID]
			duplicateControls := hasEquivalentControls(controlLayouts, controlID, live.Buttons)
			switch {
			case duplicateScreen:
				if opts.Logger != nil {
					opts.Logger.Debugf(
						"DVD menus: screen skipped decision=dedupe reason=semantic_identity domain=%s vts=%d pgc=%d pg=%d screen=%x",
						domainLabel(current.node.coordinate.Kind),
						current.node.coordinate.VTS,
						current.node.coordinate.PGC,
						current.node.coordinate.Program,
						screenID[:6],
					)
				}
			case hasControls && duplicateControls:
				if opts.Logger != nil {
					opts.Logger.Debugf(
						"DVD menus: screen skipped decision=dedupe reason=control_identity domain=%s vts=%d pgc=%d pg=%d controls=%x",
						domainLabel(current.node.coordinate.Kind),
						current.node.coordinate.VTS,
						current.node.coordinate.PGC,
						current.node.coordinate.Program,
						controlID[:6],
					)
				}
			default:
				if opts.MaxItems > 0 && len(result.Screens) >= opts.MaxItems {
					result.Truncated = true
					limitSkipped++
				} else {
					screenIDs[screenID] = struct{}{}
					if hasControls {
						rememberControlLayout(controlLayouts, controlID, live.Buttons)
					}
					result.Screens = append(result.Screens, Screen{
						Coordinate: current.node.coordinate,
						Discovery:  DiscoveryReachable,
						Identity:   screenID,
						Registers:  registers.Clone(),
						Live:       live,
					})
					if opts.Logger != nil {
						opts.Logger.Debugf(
							"DVD menus: screen selected discovery=reachable domain=%s vts=%d language_unit=%d menu_id=%d pgc=%d pg=%d cell=%d buttons=%d screen=%x stored=%d",
							domainLabel(current.node.coordinate.Kind),
							current.node.coordinate.VTS,
							current.node.coordinate.LanguageUnit,
							current.node.coordinate.MenuID,
							current.node.coordinate.PGC,
							current.node.coordinate.Program,
							current.node.coordinate.Cell,
							len(live.Buttons),
							screenID[:6],
							len(result.Screens),
						)
					}
				}
			}
		} else if live.Visible {
			utilityScreens++
		}

		for _, button := range live.Buttons {
			if result.VisitedButtons >= opts.MaxButtons {
				if markPartial(&result, "button_limit", "menu button limit reached") {
					logTraversalWarning(opts.Logger, "button_limit", current.node.coordinate)
				}
				break
			}
			result.VisitedButtons++
			branchRegisters := registers.Clone()
			buttonResult, buttonErr := vm.Execute([]vm.Command{button.Command}, &branchRegisters, opts.VM)
			if buttonErr != nil {
				if markPartial(&result, "button_command", "menu button command failed") {
					logTraversalWarning(opts.Logger, "button_command", current.node.coordinate)
				}
				continue
			}
			if opts.Logger != nil {
				opts.Logger.Tracef(
					"DVD menus: action evaluated action=button button=%d executed=%d has_link=%t link=%d registers_changed=%t",
					button.Number,
					buttonResult.Executed,
					buttonResult.HasLink,
					buttonResult.Link.Command,
					branchRegisters != registers,
				)
			}
			if buttonResult.HasLink {
				target, terminal, ok := resolveLink(current.node, buttonResult.Link, lookup)
				switch {
				case ok:
					if opts.Logger != nil {
						opts.Logger.Debugf(
							"DVD menus: branch queued action=button button=%d decision=follow link=%d target_domain=%s target_vts=%d target_pgc=%d target_pg=%d",
							button.Number,
							buttonResult.Link.Command,
							domainLabel(target.coordinate.Kind),
							target.coordinate.VTS,
							target.coordinate.PGC,
							target.coordinate.Program,
						)
					}
					queue = append(queue, transition(current, target, branchRegisters))
				case !terminal:
					if markPartial(&result, "unsupported_button_link", "menu button target was not resolved") {
						logTraversalWarning(opts.Logger, "unsupported_button_link", current.node.coordinate)
					}
				case opts.Logger != nil:
					opts.Logger.Debugf("DVD menus: branch stopped action=button button=%d decision=terminal link=%d", button.Number, buttonResult.Link.Command)
				}
				continue
			}
			if branchRegisters != registers {
				queue = append(queue, state{
					node:      current.node,
					registers: branchRegisters,
					depth:     current.depth + 1,
					utility:   current.utility,
				})
			}
		}
		reportProgress(opts.Progress, result)

		postRegisters := registers.Clone()
		postResult, postErr := vm.Execute(current.node.pgc.PostCommands, &postRegisters, opts.VM)
		if postErr == nil && opts.Logger != nil {
			opts.Logger.Tracef(
				"DVD menus: action evaluated action=post commands=%d executed=%d has_link=%t link=%d",
				len(current.node.pgc.PostCommands),
				postResult.Executed,
				postResult.HasLink,
				postResult.Link.Command,
			)
		}
		switch {
		case postErr != nil:
			if markPartial(&result, "post_command", "menu post-command failed") {
				logTraversalWarning(opts.Logger, "post_command", current.node.coordinate)
			}
		case postResult.HasLink:
			target, terminal, ok := resolveLink(current.node, postResult.Link, lookup)
			switch {
			case ok:
				if opts.Logger != nil {
					opts.Logger.Debugf(
						"DVD menus: branch queued action=post decision=follow link=%d target_domain=%s target_vts=%d target_pgc=%d target_pg=%d",
						postResult.Link.Command,
						domainLabel(target.coordinate.Kind),
						target.coordinate.VTS,
						target.coordinate.PGC,
						target.coordinate.Program,
					)
				}
				queue = append(queue, transition(current, target, postRegisters))
			case !terminal:
				if markPartial(&result, "unsupported_post_link", "menu post-command target was not resolved") {
					logTraversalWarning(opts.Logger, "unsupported_post_link", current.node.coordinate)
				}
			case opts.Logger != nil:
				opts.Logger.Debugf("DVD menus: branch stopped action=post decision=terminal link=%d", postResult.Link.Command)
			}
		case current.node.pgc.Next != 0:
			if target, ok := lookup[nodeKey{
				kind:     current.node.coordinate.Kind,
				vts:      current.node.coordinate.VTS,
				language: current.node.coordinate.LanguageUnit,
				pgc:      int(current.node.pgc.Next),
			}]; ok {
				if opts.Logger != nil {
					opts.Logger.Debugf(
						"DVD menus: branch queued action=next_pgc decision=follow target_domain=%s target_vts=%d target_pgc=%d",
						domainLabel(target.coordinate.Kind),
						target.coordinate.VTS,
						target.coordinate.PGC,
					)
				}
				queue = append(queue, state{
					node:      target,
					registers: postRegisters,
					depth:     current.depth + 1,
					runPre:    true,
				})
			}
		}
	}

	for _, item := range nodes {
		if _, ok := reachedCoordinates[item.coordinate]; ok {
			continue
		}
		live, err := resolver.Resolve(ctx, item.coordinate, item.pgc, opts.Registers.Clone())
		if err != nil {
			structuralFailures++
			if markPartial(&result, "structural_state", "structural menu candidate could not be classified") {
				logTraversalWarning(opts.Logger, "structural_state", item.coordinate)
			}
			continue
		}
		classifiedCoordinates[item.coordinate] = struct{}{}
		if !live.Visible {
			structuralTransitions++
			continue
		}
		// A zero-button screen is valid only when navigation actually reaches it.
		// Structural still/entry/SPU metadata alone also describes transitions,
		// warnings, and other authored non-menu video.
		if len(live.Buttons) == 0 {
			structuralNonInteractive++
			continue
		}
		screenID := semanticIdentity(item.coordinate, opts.Registers, live)
		if _, ok := screenIDs[screenID]; ok {
			structuralDuplicates++
			continue
		}
		controlID, hasControls := controlIdentity(item.coordinate, live)
		if hasControls && hasEquivalentControls(controlLayouts, controlID, live.Buttons) {
			structuralDuplicates++
			if opts.Logger != nil {
				opts.Logger.Debugf(
					"DVD menus: screen skipped decision=dedupe reason=control_identity domain=%s vts=%d pgc=%d pg=%d controls=%x",
					domainLabel(item.coordinate.Kind),
					item.coordinate.VTS,
					item.coordinate.PGC,
					item.coordinate.Program,
					controlID[:6],
				)
			}
			continue
		}
		structuralVisible++
		if markPartial(&result, "structural_only", "visible menu was not reached through navigation") {
			logTraversalWarning(opts.Logger, "structural_only", item.coordinate)
		}
		if opts.MaxItems > 0 && len(result.Screens) >= opts.MaxItems {
			result.Truncated = true
			limitSkipped++
			continue
		}
		screenIDs[screenID] = struct{}{}
		if hasControls {
			rememberControlLayout(controlLayouts, controlID, live.Buttons)
		}
		result.Screens = append(result.Screens, Screen{
			Coordinate: item.coordinate,
			Discovery:  DiscoveryStructural,
			Identity:   screenID,
			Registers:  opts.Registers.Clone(),
			Live:       live,
		})
		if opts.Logger != nil {
			opts.Logger.Debugf(
				"DVD menus: screen selected discovery=structural domain=%s vts=%d language_unit=%d menu_id=%d pgc=%d pg=%d cell=%d buttons=%d screen=%x stored=%d",
				domainLabel(item.coordinate.Kind),
				item.coordinate.VTS,
				item.coordinate.LanguageUnit,
				item.coordinate.MenuID,
				item.coordinate.PGC,
				item.coordinate.Program,
				item.coordinate.Cell,
				len(live.Buttons),
				screenID[:6],
				len(result.Screens),
			)
		}
		reportProgress(opts.Progress, result)
	}
	result.Complete = !result.Partial && !result.Truncated && len(classifiedCoordinates) == len(nodes)
	if opts.Logger != nil {
		opts.Logger.Debugf(
			"DVD menus: structural reconciliation complete classified=%d transitions=%d noninteractive=%d utility=%d duplicates=%d visible_unreached=%d failures=%d",
			len(classifiedCoordinates),
			structuralTransitions,
			structuralNonInteractive,
			utilityScreens,
			structuralDuplicates,
			structuralVisible,
			structuralFailures,
		)
		if limitSkipped > 0 {
			opts.Logger.Debugf("DVD menus: storage limit applied max_items=%d additional_menu_candidates=%d", opts.MaxItems, limitSkipped)
		}
		opts.Logger.Debugf(
			"DVD menus: discovery complete inventoried=%d classified=%d reached=%d states=%d buttons=%d screens=%d partial=%t truncated=%t warnings=%d",
			result.Inventoried,
			len(classifiedCoordinates),
			len(reachedCoordinates),
			result.VisitedStates,
			result.VisitedButtons,
			len(result.Screens),
			result.Partial,
			result.Truncated,
			len(result.Warnings),
		)
	}
	reportProgress(opts.Progress, result)
	if len(result.Screens) == 0 {
		return result, fmt.Errorf("%w: no visible menu screens", ErrDiscovery)
	}
	return result, nil
}

func reportProgress(reporter func(Progress), result Result) {
	if reporter == nil {
		return
	}
	reporter(Progress{
		Inventoried:    result.Inventoried,
		VisitedStates:  result.VisitedStates,
		VisitedButtons: result.VisitedButtons,
		Screens:        len(result.Screens),
		Warnings:       len(result.Warnings),
	})
}

// transition advances a branch and preserves utility filtering only while it
// remains within the same PGC.
func transition(current state, target node, registers vm.Registers) state {
	return state{
		node:      target,
		registers: registers,
		depth:     current.depth + 1,
		runPre:    key(current.node.coordinate) != key(target.coordinate),
		utility:   current.utility && key(current.node.coordinate) == key(target.coordinate),
	}
}

// inventory returns deterministic program-level graph nodes and authored entry
// seeds for the selected manager/title-set language units.
func inventory(disc ifo.Disc, language string) ([]node, []node) {
	files := append([]ifo.File{disc.Manager}, disc.TitleSets...)
	var nodes []node
	var seeds []node
	for _, file := range files {
		languageIndex := selectLanguage(file.Languages, language)
		if languageIndex < 0 {
			continue
		}
		unit := file.Languages[languageIndex]
		for _, pgc := range unit.ProgramChains {
			if pgc.Programs == 0 || pgc.Cells == 0 {
				continue
			}
			for program := 1; program <= int(pgc.Programs); program++ {
				item := node{coordinate: Coordinate{
					Kind:         file.Kind,
					VTS:          file.VTS,
					LanguageUnit: languageIndex + 1,
					PGC:          pgc.Number,
					Program:      program,
					Cell:         cellForProgram(pgc, program),
					MenuID:       pgc.MenuID,
				}, pgc: pgc}
				nodes = append(nodes, item)
				if program == 1 && pgc.EntryID&0x80 != 0 {
					seeds = append(seeds, item)
				}
			}
		}
	}
	sort.SliceStable(seeds, func(i, j int) bool {
		left, right := menuPriority(seeds[i].coordinate.MenuID), menuPriority(seeds[j].coordinate.MenuID)
		if left != right {
			return left < right
		}
		if seeds[i].coordinate.VTS != seeds[j].coordinate.VTS {
			return seeds[i].coordinate.VTS < seeds[j].coordinate.VTS
		}
		return seeds[i].coordinate.PGC < seeds[j].coordinate.PGC
	})
	if len(seeds) == 0 && len(nodes) != 0 {
		seeds = append(seeds, nodes[0])
	}
	return nodes, seeds
}

func selectLanguage(units []ifo.LanguageUnit, requested string) int {
	for index := range units {
		if requested != "" && units[index].Code == requested {
			return index
		}
	}
	for index := range units {
		if units[index].Code == "en" {
			return index
		}
	}
	if len(units) != 0 {
		return 0
	}
	return -1
}

func menuPriority(menuID uint8) int {
	switch menuID {
	case 2: // Title
		return 0
	case 3: // Root
		return 1
	case 7: // Part/chapter
		return 2
	case 5: // Audio
		return 3
	case 4: // Subpicture
		return 4
	case 6: // Angle
		return 5
	default:
		return 6
	}
}

type nodeKey struct {
	kind     ifo.Kind
	vts      int
	language int
	pgc      int
}

func key(coordinate Coordinate) nodeKey {
	return nodeKey{
		kind:     coordinate.Kind,
		vts:      coordinate.VTS,
		language: coordinate.LanguageUnit,
		pgc:      coordinate.PGC,
	}
}

// resolveLink maps a VM transition to an inventoried node. The second result
// reports a terminal command; the third reports a resolved target.
func resolveLink(current node, link vm.Link, lookup map[nodeKey]node) (node, bool, bool) {
	coordinate := current.coordinate
	var targetPGC int
	switch link.Command {
	case vm.LinkNoLink:
		return current, false, false
	case vm.LinkPGCN:
		targetPGC = int(link.Data1)
	case vm.LinkNextPGC:
		targetPGC = int(current.pgc.Next)
	case vm.LinkPreviousPGC:
		targetPGC = int(current.pgc.Previous)
	case vm.LinkGoUpPGC:
		targetPGC = int(current.pgc.GoUp)
	case vm.LinkTopCell:
		return current, false, true
	case vm.LinkTopProgram:
		coordinate.Cell = cellForProgram(current.pgc, coordinate.Program)
		return node{coordinate: coordinate, pgc: current.pgc}, false, true
	case vm.LinkTopPGC:
		coordinate.Program = 1
		coordinate.Cell = cellForProgram(current.pgc, 1)
		return node{coordinate: coordinate, pgc: current.pgc}, false, true
	case vm.LinkNextCell:
		coordinate.Cell++
		coordinate.Program = programForCell(current.pgc, coordinate.Cell)
		return node{coordinate: coordinate, pgc: current.pgc}, false, coordinate.Cell <= int(current.pgc.Cells)
	case vm.LinkPreviousCell:
		coordinate.Cell--
		coordinate.Program = programForCell(current.pgc, coordinate.Cell)
		return node{coordinate: coordinate, pgc: current.pgc}, false, coordinate.Cell > 0
	case vm.LinkNextProgram:
		coordinate.Program++
		coordinate.Cell = cellForProgram(current.pgc, coordinate.Program)
		return node{coordinate: coordinate, pgc: current.pgc}, false, coordinate.Program <= int(current.pgc.Programs)
	case vm.LinkPreviousProgram:
		coordinate.Program--
		coordinate.Cell = cellForProgram(current.pgc, coordinate.Program)
		return node{coordinate: coordinate, pgc: current.pgc}, false, coordinate.Program > 0
	case vm.LinkPGN:
		coordinate.Program = int(link.Data1)
		coordinate.Cell = cellForProgram(current.pgc, coordinate.Program)
		return node{coordinate: coordinate, pgc: current.pgc}, false, coordinate.Program > 0 && coordinate.Program <= int(current.pgc.Programs)
	case vm.LinkCN:
		coordinate.Cell = int(link.Data1)
		coordinate.Program = programForCell(current.pgc, coordinate.Cell)
		return node{coordinate: coordinate, pgc: current.pgc}, false, coordinate.Cell > 0 && coordinate.Cell <= int(current.pgc.Cells)
	case vm.JumpVMGMMenu, vm.CallVMGMMenu:
		return findMenu(lookup, ifo.KindManager, 0, int(link.Data1))
	case vm.JumpVTSM:
		return findMenu(lookup, ifo.KindTitleSet, int(link.Data1), int(link.Data3))
	case vm.CallVTSM:
		return findMenu(lookup, ifo.KindTitleSet, current.coordinate.VTS, int(link.Data1))
	case vm.JumpVMGMPGC, vm.CallVMGMPGC:
		coordinate.Kind = ifo.KindManager
		coordinate.VTS = 0
		targetPGC = int(link.Data1)
	case vm.Exit, vm.JumpTT, vm.JumpVTSTT, vm.JumpVTSPTT, vm.JumpFirstPlay, vm.CallFirstPlay, vm.LinkResume, vm.LinkPTTN, vm.LinkTailPGC:
		return node{}, true, false
	case vm.LinkReserved4, vm.LinkReserved8, vm.LinkReserved14, vm.LinkReserved15:
		return node{}, false, false
	default:
		return node{}, false, false
	}
	if targetPGC == 0 {
		return node{}, false, false
	}
	coordinate.PGC = targetPGC
	target, ok := lookup[key(coordinate)]
	return target, false, ok
}

func cellForProgram(pgc ifo.ProgramChain, program int) int {
	if program > 0 && program <= len(pgc.ProgramMap) && pgc.ProgramMap[program-1] != 0 {
		return int(pgc.ProgramMap[program-1])
	}
	return max(program, 1)
}

func programForCell(pgc ifo.ProgramChain, cell int) int {
	program := 1
	for index, firstCell := range pgc.ProgramMap {
		if int(firstCell) > cell {
			break
		}
		program = index + 1
	}
	return program
}

func findMenu(lookup map[nodeKey]node, kind ifo.Kind, vts, menuID int) (node, bool, bool) {
	var matches []node
	for _, item := range lookup {
		if item.coordinate.Kind == kind && item.coordinate.VTS == vts && int(item.coordinate.MenuID) == menuID {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return node{}, false, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].coordinate.PGC < matches[j].coordinate.PGC })
	return matches[0], false, true
}

func stateIdentity(coordinate Coordinate, registers vm.Registers) [32]byte {
	hash := sha256.New()
	writeCoordinate(hash, coordinate)
	var buffer [2]byte
	for _, value := range registers.General {
		binary.BigEndian.PutUint16(buffer[:], value)
		hash.Write(buffer[:])
	}
	for _, value := range registers.System {
		binary.BigEndian.PutUint16(buffer[:], value)
		hash.Write(buffer[:])
	}
	for _, mode := range registers.CounterMode {
		if mode {
			hash.Write([]byte{1})
		} else {
			hash.Write([]byte{0})
		}
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func traversalIdentity(coordinate Coordinate, registers vm.Registers, runPre bool) [32]byte {
	state := stateIdentity(coordinate, registers)
	hash := sha256.New()
	hash.Write(state[:])
	if runPre {
		hash.Write([]byte{1})
	} else {
		hash.Write([]byte{0})
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

// semanticIdentity hashes coordinate/register/live state for exact screen
// deduplication within deterministic traversal.
func semanticIdentity(coordinate Coordinate, registers vm.Registers, live LiveState) [32]byte {
	state := stateIdentity(coordinate, registers)
	hash := sha256.New()
	hash.Write(state[:])
	hash.Write(live.Fingerprint)
	for _, button := range live.Buttons {
		hash.Write(button.Command[:])
		var data [12]byte
		binary.BigEndian.PutUint16(data[0:2], button.XStart)
		binary.BigEndian.PutUint16(data[2:4], button.YStart)
		binary.BigEndian.PutUint16(data[4:6], button.XEnd)
		binary.BigEndian.PutUint16(data[6:8], button.YEnd)
		data[8], data[9], data[10], data[11] = button.Up, button.Down, button.Left, button.Right
		hash.Write(data[:])
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

// controlIdentity hashes menu scope plus button commands/navigation while
// deliberately excluding background fingerprints and exact rectangles.
func controlIdentity(coordinate Coordinate, live LiveState) ([32]byte, bool) {
	if len(live.Buttons) == 0 {
		return [32]byte{}, false
	}
	hash := sha256.New()
	var scope [14]byte
	scope[0] = byte(coordinate.Kind)
	binary.BigEndian.PutUint32(scope[1:5], coordinateValue(coordinate.VTS))
	binary.BigEndian.PutUint32(scope[5:9], coordinateValue(coordinate.LanguageUnit))
	scope[9] = coordinate.MenuID
	binary.BigEndian.PutUint32(scope[10:14], coordinateValue(len(live.Buttons)))
	hash.Write(scope[:])
	for _, button := range live.Buttons {
		hash.Write(button.Command[:])
		var data [7]byte
		data[0] = button.Number
		data[1] = button.ColorGroup
		if button.AutoAction {
			data[2] = 1
		}
		data[3], data[4], data[5], data[6] = button.Up, button.Down, button.Left, button.Right
		hash.Write(data[:])
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, true
}

func hasEquivalentControls(layouts map[[32]byte][][]nav.Button, identity [32]byte, buttons []nav.Button) bool {
	for _, known := range layouts[identity] {
		if buttonLayoutsClose(known, buttons) {
			return true
		}
	}
	return false
}

func rememberControlLayout(layouts map[[32]byte][][]nav.Button, identity [32]byte, buttons []nav.Button) {
	layouts[identity] = append(layouts[identity], append([]nav.Button(nil), buttons...))
}

// buttonLayoutsClose accepts equivalent ordered button rectangles shifted by
// at most eight pixels on each edge.
func buttonLayoutsClose(left, right []nav.Button) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if coordinateDistance(left[index].XStart, right[index].XStart) > 8 ||
			coordinateDistance(left[index].YStart, right[index].YStart) > 8 ||
			coordinateDistance(left[index].XEnd, right[index].XEnd) > 8 ||
			coordinateDistance(left[index].YEnd, right[index].YEnd) > 8 {
			return false
		}
	}
	return true
}

func coordinateDistance(left, right uint16) uint16 {
	if left >= right {
		return left - right
	}
	return right - left
}

type hashWriter interface{ Write([]byte) (int, error) }

func writeCoordinate(hash hashWriter, coordinate Coordinate) {
	var data [24]byte
	data[0] = byte(coordinate.Kind)
	binary.BigEndian.PutUint32(data[1:5], coordinateValue(coordinate.VTS))
	binary.BigEndian.PutUint32(data[5:9], coordinateValue(coordinate.LanguageUnit))
	binary.BigEndian.PutUint32(data[9:13], coordinateValue(coordinate.PGC))
	binary.BigEndian.PutUint32(data[13:17], coordinateValue(coordinate.Program))
	binary.BigEndian.PutUint32(data[17:21], coordinateValue(coordinate.Cell))
	data[21] = coordinate.MenuID
	_, _ = hash.Write(data[:22])
}

func coordinateValue(value int) uint32 {
	if value < 0 || uint64(value) > math.MaxUint32 {
		panic("DVD menu coordinate outside uint32 range")
	}
	return uint32(value)
}

func markPartial(result *Result, code, message string) bool {
	result.Partial = true
	for _, warning := range result.Warnings {
		if warning.Code == code {
			return false
		}
	}
	result.Warnings = append(result.Warnings, Warning{Code: code, Message: message})
	return true
}

func logTraversalWarning(logger Logger, code string, coordinate Coordinate) {
	if logger == nil {
		return
	}
	logger.Warnf(
		"DVD menus: traversal warning code=%s detail=%q domain=%s vts=%d language_unit=%d pgc=%d pg=%d cell=%d",
		code,
		traversalWarningDetail(code),
		domainLabel(coordinate.Kind),
		coordinate.VTS,
		coordinate.LanguageUnit,
		coordinate.PGC,
		coordinate.Program,
		coordinate.Cell,
	)
}

func traversalWarningDetail(code string) string {
	switch code {
	case "depth_limit":
		return "menu branch depth limit reached"
	case "state_limit":
		return "menu state limit reached"
	case "pre_command":
		return "menu pre-command failed"
	case "unsupported_pre_link":
		return "menu pre-command target was not resolved"
	case "live_state":
		return "menu NAV/SPU state could not be resolved"
	case "button_limit":
		return "menu button limit reached"
	case "button_command":
		return "menu button command failed"
	case "unsupported_button_link":
		return "menu button target was not resolved"
	case "post_command":
		return "menu post-command failed"
	case "unsupported_post_link":
		return "menu post-command target was not resolved"
	case "structural_only":
		return "visible menu was not reached through navigation"
	case "structural_state":
		return "structural menu candidate could not be classified"
	}
	return "DVD menu traversal warning"
}

func domainLabel(kind ifo.Kind) string {
	switch kind {
	case ifo.KindManager:
		return "vmg"
	case ifo.KindTitleSet:
		return "vtsm"
	}
	return "unknown"
}

func fingerprintPrefix(value []byte) string {
	if len(value) > 6 {
		value = value[:6]
	}
	return hex.EncodeToString(value)
}
