package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/dvdvideo/graph"
	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
	"github.com/autobrr/upbrr/internal/dvdvideo/nav"
	"github.com/autobrr/upbrr/internal/dvdvideo/spu"
	"github.com/autobrr/upbrr/internal/dvdvideo/vm"
)

// ErrLiveState identifies missing, invalid, or unresolvable menu VOB/NAV/SPU state.
var ErrLiveState = errors.New("DVD menu live state unavailable")

type resolverKey struct {
	coordinate graph.Coordinate
	registers  vm.Registers
}

type resolvedState struct {
	live          graph.LiveState
	buttons       []nav.Button
	overlay       spu.Overlay
	hasOverlay    bool
	highlight     *spu.Highlight
	palette       [16]uint32
	target        time.Duration
	scanTruncated bool
}

// directoryResolver maps validated graph coordinates to manager/title-set menu
// VOBs and caches resolved live state by coordinate and register snapshot.
type directoryResolver struct {
	files  map[int]string
	scan   nav.CellScanOptions
	cache  map[resolverKey]resolvedState
	chains map[coordinateKey]ifo.ProgramChain
}

type coordinateKey struct {
	kind     ifo.Kind
	vts      int
	language int
	pgc      int
}

// newDirectoryResolver inventories manager and title-set menu VOB paths beneath
// one extracted VIDEO_TS directory and rejects duplicate or symlinked entries.
func newDirectoryResolver(root string, scan nav.CellScanOptions) (*directoryResolver, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("%w: read VIDEO_TS directory", ErrLiveState)
	}
	files := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		upper := strings.ToUpper(entry.Name())
		if entry.Type()&os.ModeSymlink != 0 &&
			(upper == "VIDEO_TS.VOB" || len(upper) == len("VTS_01_0.VOB") && strings.HasPrefix(upper, "VTS_") && strings.HasSuffix(upper, "_0.VOB")) {
			return nil, fmt.Errorf("%w: menu VOB symlink", ErrLiveState)
		}
		switch {
		case upper == "VIDEO_TS.VOB":
			if _, duplicate := files[0]; duplicate {
				return nil, fmt.Errorf("%w: duplicate VMG menu VOB", ErrLiveState)
			}
			files[0] = filepath.Join(root, entry.Name())
		case len(upper) == len("VTS_01_0.VOB") && strings.HasPrefix(upper, "VTS_") && strings.HasSuffix(upper, "_0.VOB"):
			vts, parseErr := strconv.Atoi(upper[4:6])
			if parseErr == nil && vts >= 1 && vts <= 99 {
				if _, duplicate := files[vts]; duplicate {
					return nil, fmt.Errorf("%w: duplicate VTS menu VOB", ErrLiveState)
				}
				files[vts] = filepath.Join(root, entry.Name())
			}
		}
	}
	return &directoryResolver{
		files:  files,
		scan:   scan,
		cache:  make(map[resolverKey]resolvedState),
		chains: make(map[coordinateKey]ifo.ProgramChain),
	}, nil
}

// Resolve returns cached or freshly scanned live state for an inventory-validated
// graph coordinate and VM register snapshot.
func (r *directoryResolver) Resolve(ctx context.Context, coordinate graph.Coordinate, pgc ifo.ProgramChain, registers vm.Registers) (graph.LiveState, error) {
	detail, err := r.resolve(ctx, coordinate, pgc, registers)
	if err != nil {
		return graph.LiveState{}, err
	}
	r.chains[coordinateLookupKey(coordinate)] = pgc
	return detail.live, nil
}

func (r *directoryResolver) programChain(coordinate graph.Coordinate) ifo.ProgramChain {
	return r.chains[coordinateLookupKey(coordinate)]
}

func coordinateLookupKey(coordinate graph.Coordinate) coordinateKey {
	return coordinateKey{
		kind:     coordinate.Kind,
		vts:      coordinate.VTS,
		language: coordinate.LanguageUnit,
		pgc:      coordinate.PGC,
	}
}

// resolve scans one authored cell, selects live NAV/SPU/highlight state, and
// computes the exact program-relative frame target.
func (r *directoryResolver) resolve(ctx context.Context, coordinate graph.Coordinate, pgc ifo.ProgramChain, registers vm.Registers) (resolvedState, error) {
	if err := ctx.Err(); err != nil {
		return resolvedState{}, fmt.Errorf("%w: %w", ErrLiveState, err)
	}
	key := resolverKey{coordinate: coordinate, registers: registers}
	if cached, ok := r.cache[key]; ok {
		return cached, nil
	}
	if coordinate.Cell < 1 || coordinate.Cell > len(pgc.CellPlayback) || coordinate.Cell > len(pgc.CellPositions) {
		return resolvedState{}, fmt.Errorf("%w: invalid cell coordinate", ErrLiveState)
	}
	menuVOB, ok := r.files[coordinate.VTS]
	if !ok {
		return resolvedState{}, fmt.Errorf("%w: menu VOB missing vts=%d", ErrLiveState, coordinate.VTS)
	}
	file, err := os.Open(menuVOB)
	if err != nil {
		return resolvedState{}, fmt.Errorf("%w: open menu VOB", ErrLiveState)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return resolvedState{}, fmt.Errorf("%w: inspect menu VOB", ErrLiveState)
	}
	if info.IsDir() || info.Size() <= 0 {
		return resolvedState{}, fmt.Errorf("%w: invalid menu VOB", ErrLiveState)
	}
	cell := pgc.CellPlayback[coordinate.Cell-1]
	scan, err := nav.ScanCell(file, info.Size(), cell.FirstSector, cell.LastSector, r.scan)
	if err != nil {
		return resolvedState{}, fmt.Errorf("%w: scan menu cell", ErrLiveState)
	}
	packet, firstPTS, err := selectNAVPacket(scan.Packets, pgc.CellPositions[coordinate.Cell-1])
	if err != nil {
		return resolvedState{}, err
	}
	buttons := selectButtonGroup(packet.PCI)
	highlight := selectHighlight(packet.PCI, buttons, registers)
	targetPTS := packet.PCI.HighlightStartPTS
	if targetPTS == 0 {
		targetPTS = packet.PCI.VOBUStartPTS
	}
	overlay, hasOverlay := selectOverlay(scan.SPUFragments, pgc, registers, uint64(targetPTS))
	target, err := targetDuration(pgc, coordinate, firstPTS, packet.PCI.VOBUStartPTS)
	if err != nil {
		return resolvedState{}, err
	}
	fingerprint := liveFingerprint(packet, overlay, hasOverlay)
	authoredStill := authoredStillState(pgc, coordinate)
	authoredEntry := pgc.EntryID&0x80 != 0 && coordinate.Program == 1
	visible := visibleMenuState(pgc, coordinate, buttons, hasOverlay, highlight != nil)
	detail := resolvedState{
		live: graph.LiveState{
			Visible:       visible,
			Buttons:       buttons,
			Fingerprint:   fingerprint,
			HasOverlay:    hasOverlay,
			HasHighlight:  highlight != nil,
			AuthoredStill: authoredStill,
			AuthoredEntry: authoredEntry,
		},
		buttons:       buttons,
		overlay:       overlay,
		hasOverlay:    hasOverlay,
		highlight:     highlight,
		palette:       pgc.Palette,
		target:        target,
		scanTruncated: scan.Truncated,
	}
	r.cache[key] = detail
	return detail, nil
}

// visibleMenuState accepts live controls/SPU evidence plus authored still or
// entry evidence used for reachable zero-button menu screens.
func visibleMenuState(pgc ifo.ProgramChain, coordinate graph.Coordinate, buttons []nav.Button, hasOverlay, hasHighlight bool) bool {
	if len(buttons) != 0 || hasOverlay || hasHighlight {
		return true
	}
	if authoredStillState(pgc, coordinate) {
		return true
	}
	return pgc.EntryID&0x80 != 0 && coordinate.Program == 1
}

func authoredStillState(pgc ifo.ProgramChain, coordinate graph.Coordinate) bool {
	if pgc.StillTime != 0 {
		return true
	}
	return coordinate.Cell > 0 && coordinate.Cell <= len(pgc.CellPlayback) && pgc.CellPlayback[coordinate.Cell-1].StillTime != 0
}

// selectNAVPacket filters packets to the authored VOB/cell position and returns
// the earliest packet plus its PTS baseline.
func selectNAVPacket(packets []nav.Packet, position ifo.CellPosition) (nav.Packet, uint32, error) {
	var matches []nav.Packet
	for _, packet := range packets {
		if position.VOBID != 0 && packet.DSI.VOBID != 0 && packet.DSI.VOBID != position.VOBID {
			continue
		}
		if position.CellID != 0 && packet.DSI.CellID != 0 && packet.DSI.CellID != position.CellID {
			continue
		}
		matches = append(matches, packet)
	}
	if len(matches) == 0 {
		return nav.Packet{}, 0, fmt.Errorf("%w: no matching NAV packet", ErrLiveState)
	}
	selected := matches[0]
	for _, packet := range matches {
		if packet.PCI.HighlightStatus != 0 || packet.PCI.ButtonCount != 0 {
			selected = packet
			break
		}
	}
	return selected, matches[0].PCI.VOBUStartPTS, nil
}

func selectButtonGroup(pci nav.PCI) []nav.Button {
	for _, group := range pci.ButtonGroups {
		if len(group) != 0 {
			return append([]nav.Button(nil), group...)
		}
	}
	return nil
}

// selectHighlight resolves the selected button from SPRM 8, forced selection,
// or the first live button, then applies its authored display-group palette.
func selectHighlight(pci nav.PCI, buttons []nav.Button, registers vm.Registers) *spu.Highlight {
	if len(buttons) == 0 {
		return nil
	}
	buttonNumber := pci.ForcedSelectButton & 0x3f
	if buttonNumber == 0 {
		buttonNumber = uint8(registers.System[8] & 0x3f)
	}
	if buttonNumber == 0 {
		buttonNumber = buttons[0].Number
	}
	for _, button := range buttons {
		if button.Number != buttonNumber || button.ColorGroup == 0 || button.ColorGroup > nav.MaxButtonGroups {
			continue
		}
		palette := pci.ButtonColors[button.ColorGroup-1][0]
		return &spu.Highlight{
			Bounds:  image.Rect(int(button.XStart), int(button.YStart), int(button.XEnd)+1, int(button.YEnd)+1),
			Palette: palette,
		}
	}
	return nil
}

type timedSPUPacket struct {
	packet spu.Packet
	pts    uint64
	hasPTS bool
}

// selectOverlay reassembles preferred subpicture streams and selects the latest
// decodable menu overlay active at targetPTS.
func selectOverlay(fragments []nav.SPUFragment, pgc ifo.ProgramChain, registers vm.Registers, targetPTS uint64) (spu.Overlay, bool) {
	var reassembler spu.Reassembler
	var streamPTS [32]uint64
	var streamHasPTS [32]bool
	var packets []timedSPUPacket
	for _, fragment := range fragments {
		index := fragment.StreamID - 0x20
		if fragment.HasPTS {
			streamPTS[index] = fragment.PTS
			streamHasPTS[index] = true
		}
		complete, err := reassembler.Push(fragment.StreamID, fragment.Data)
		if err != nil {
			continue
		}
		for _, packet := range complete {
			packets = append(packets, timedSPUPacket{
				packet: packet,
				pts:    streamPTS[index],
				hasPTS: streamHasPTS[index],
			})
			streamHasPTS[index] = false
		}
	}
	allowed := preferredSPUStreams(pgc, registers)
	for _, streamID := range allowed {
		for _, packet := range packets {
			if packet.packet.StreamID != streamID {
				continue
			}
			date := ^uint16(0)
			if packet.hasPTS {
				date = 0
				if targetPTS >= packet.pts {
					delta := (targetPTS - packet.pts) / 1024
					date = boundedControlDate(delta)
				}
			}
			overlay, err := spu.Decode(packet.packet.Data, pgc.Palette, date)
			if err == nil {
				return overlay, true
			}
		}
	}
	return spu.Overlay{}, false
}

func preferredSPUStreams(pgc ifo.ProgramChain, registers vm.Registers) []uint8 {
	logical := int(registers.System[2] & 0x1f)
	order := make([]int, 0, len(pgc.SubpictureControl))
	if logical < len(pgc.SubpictureControl) {
		order = append(order, logical)
	}
	for index := range pgc.SubpictureControl {
		if index != logical {
			order = append(order, index)
		}
	}
	var streams []uint8
	seen := make(map[uint8]struct{})
	for _, index := range order {
		control := pgc.SubpictureControl[index]
		if control&0x8000_0000 == 0 {
			continue
		}
		for _, shift := range []uint{24, 16, 8, 0} {
			streamID := uint8(0x20 + (control>>shift)&0x1f)
			if _, ok := seen[streamID]; ok {
				continue
			}
			seen[streamID] = struct{}{}
			streams = append(streams, streamID)
		}
	}
	if len(streams) == 0 {
		for streamID := uint8(0x20); streamID <= 0x3f; streamID++ {
			streams = append(streams, streamID)
		}
	}
	return streams
}

func boundedControlDate(value uint64) uint16 {
	if value > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(value)
}

// targetDuration converts preceding cell durations and the selected NAV PTS
// delta into an FFmpeg seek offset within the chosen program.
func targetDuration(pgc ifo.ProgramChain, coordinate graph.Coordinate, firstPTS, selectedPTS uint32) (time.Duration, error) {
	if coordinate.Program < 1 || coordinate.Program > int(pgc.Programs) || coordinate.Cell < 1 || coordinate.Cell > int(pgc.Cells) {
		return 0, fmt.Errorf("%w: invalid program or cell", ErrLiveState)
	}
	firstCell := coordinate.Program
	if coordinate.Program <= len(pgc.ProgramMap) {
		firstCell = int(pgc.ProgramMap[coordinate.Program-1])
	}
	if firstCell < 1 || firstCell > coordinate.Cell {
		return 0, fmt.Errorf("%w: invalid program cell mapping", ErrLiveState)
	}
	var target time.Duration
	for cell := firstCell; cell < coordinate.Cell; cell++ {
		duration, err := pgc.CellPlayback[cell-1].PlaybackTime.Duration()
		if err != nil {
			return 0, fmt.Errorf("%w: invalid cell duration", ErrLiveState)
		}
		target += duration
	}
	if selectedPTS >= firstPTS {
		target += time.Duration(selectedPTS-firstPTS) * time.Second / 90_000
	}
	return target, nil
}

// liveFingerprint hashes NAV timing/selection state and available overlay
// pixels without incorporating local paths or disc-identifying text.
func liveFingerprint(packet nav.Packet, overlay spu.Overlay, hasOverlay bool) []byte {
	hash := sha256.New()
	var data [16]byte
	binary.BigEndian.PutUint32(data[0:4], packet.PCI.VOBUStartPTS)
	binary.BigEndian.PutUint32(data[4:8], packet.PCI.HighlightStartPTS)
	binary.BigEndian.PutUint16(data[8:10], packet.DSI.VOBID)
	data[10] = packet.DSI.CellID
	data[11] = packet.PCI.ForcedSelectButton
	_, _ = hash.Write(data[:12])
	if hasOverlay {
		_, _ = hash.Write(overlay.Pixels)
		_, _ = hash.Write([]byte(overlay.Bounds.String()))
	}
	return hash.Sum(nil)
}
