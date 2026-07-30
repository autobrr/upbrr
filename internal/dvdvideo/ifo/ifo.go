// Package ifo parses the bounded DVD-Video menu structures needed for menu
// inventory and VM traversal.
//
// Structure layouts are derived from VideoLAN libdvdread 7.0.1
// src/dvdread/ifo_types.h and src/ifo_read.c (commit
// c7f373951bae9642e1ce1fbb2cd02f92c09756e0), GPL-2.0-or-later.
package ifo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/dvdvideo/vm"
)

const (
	sectorSize       = 2048
	pgcSize          = 236
	maxIFOBytes      = 64 << 20
	maxLanguageUnits = 99
	maxProgramChains = 999
)

var (
	// ErrInvalidIFO identifies malformed, inconsistent, or unsupported IFO data.
	ErrInvalidIFO = errors.New("invalid DVD IFO")
	// ErrTooLarge identifies an IFO that exceeds the parser's bounded input size.
	ErrTooLarge = errors.New("DVD IFO exceeds size limit")
)

// Kind identifies the Video Manager or a Video Title Set IFO.
type Kind uint8

// IFO domain kinds accepted by [Parse].
const (
	// KindManager identifies VIDEO_TS.IFO or its backup.
	KindManager Kind = iota + 1
	// KindTitleSet identifies a VTS_nn_0.IFO or its backup.
	KindTitleSet
)

// Disc is the structurally inventoried menu metadata from one VIDEO_TS tree.
type Disc struct {
	// Manager contains the video-manager menu metadata.
	Manager File
	// TitleSets contains parsed title-set menu metadata ordered by VTS number.
	TitleSets []File
}

// File contains one IFO's menu language units and program chains.
type File struct {
	// Kind identifies whether this file is the manager or a title set.
	Kind Kind
	// VTS is zero for the manager and 1 through 99 for a title set.
	VTS int
	// Recovered reports that parsing fell back from the IFO to its BUP copy.
	Recovered bool
	// FirstPlay is the optional manager first-play program chain.
	FirstPlay *ProgramChain
	// Languages contains the authored menu language units.
	Languages []LanguageUnit
	// SourceName is the selected IFO or BUP base name, never its local path.
	SourceName string
}

// LanguageUnit is one authored menu language table.
type LanguageUnit struct {
	// Code is the authored two-byte DVD language code.
	Code string
	// Extension is the raw authored language-unit extension byte.
	Extension uint8
	// Exists is the raw authored menu-existence mask.
	Exists uint8
	// ProgramChains contains the language unit's parsed menu PGCs.
	ProgramChains []ProgramChain
}

// DVDTime is the authored four-byte BCD playback time.
type DVDTime struct {
	// Hour is the BCD-encoded hour byte.
	Hour uint8
	// Minute is the BCD-encoded minute byte.
	Minute uint8
	// Second is the BCD-encoded second byte.
	Second uint8
	// Frame contains BCD frame count plus the authored frame-rate bits.
	Frame uint8
}

// Duration converts DVD BCD time and frame-rate bits into wall-clock time.
func (value DVDTime) Duration() (time.Duration, error) {
	hour, err := decodeBCD(value.Hour)
	if err != nil {
		return 0, fmt.Errorf("%w: playback hour", ErrInvalidIFO)
	}
	minute, err := decodeBCD(value.Minute)
	if err != nil || minute > 59 {
		return 0, fmt.Errorf("%w: playback minute", ErrInvalidIFO)
	}
	second, err := decodeBCD(value.Second)
	if err != nil || second > 59 {
		return 0, fmt.Errorf("%w: playback second", ErrInvalidIFO)
	}
	frames, err := decodeBCD(value.Frame & 0x3f)
	if err != nil {
		return 0, fmt.Errorf("%w: playback frame", ErrInvalidIFO)
	}

	duration := time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute + time.Duration(second)*time.Second
	switch value.Frame & 0xc0 {
	case 0:
		if frames != 0 {
			return 0, fmt.Errorf("%w: playback frame rate", ErrInvalidIFO)
		}
	case 0x40:
		if frames >= 25 {
			return 0, fmt.Errorf("%w: PAL playback frame", ErrInvalidIFO)
		}
		duration += time.Duration(frames) * time.Second / 25
	case 0xc0:
		if frames >= 30 {
			return 0, fmt.Errorf("%w: NTSC playback frame", ErrInvalidIFO)
		}
		duration += time.Duration(frames) * 1001 * time.Second / 30_000
	default:
		return 0, fmt.Errorf("%w: playback frame rate", ErrInvalidIFO)
	}
	return duration, nil
}

func decodeBCD(value uint8) (int, error) {
	if value>>4 > 9 || value&0x0f > 9 {
		return 0, ErrInvalidIFO
	}
	return int(value>>4)*10 + int(value&0x0f), nil
}

// ProgramChain is the bounded structural data needed by the graph and renderer.
type ProgramChain struct {
	// Number is the one-based PGC table index; zero identifies first-play data.
	Number int
	// EntryID is the authored entry/menu identifier byte.
	EntryID uint8
	// MenuID is the low four bits of EntryID.
	MenuID uint8
	// Programs is the authored program count.
	Programs uint8
	// Cells is the authored cell count.
	Cells uint8
	// PlaybackTime is the PGC's authored duration.
	PlaybackTime DVDTime
	// Next is the same-language next-PGC number, or zero when absent.
	Next uint16
	// Previous is the same-language previous-PGC number, or zero when absent.
	Previous uint16
	// GoUp is the same-language parent-PGC number, or zero when absent.
	GoUp uint16
	// PlaybackMode is the raw authored playback-mode byte.
	PlaybackMode uint8
	// StillTime is the authored PGC still duration byte.
	StillTime uint8
	// AudioControl contains the eight authored audio stream-control words.
	AudioControl [8]uint16
	// SubpictureControl contains the 32 authored subpicture stream-control words.
	SubpictureControl [32]uint32
	// Palette contains the 16 authored YCrCb subpicture colors.
	Palette [16]uint32
	// PreCommands execute before PGC playback during traversal.
	PreCommands []vm.Command
	// PostCommands execute after PGC playback during traversal.
	PostCommands []vm.Command
	// CellCommands contains the PGC's authored cell command table.
	CellCommands []vm.Command
	// ProgramMap maps one-based programs to one-based starting cells.
	ProgramMap []uint8
	// CellPlayback contains sector ranges and timing for each cell.
	CellPlayback []CellPlayback
	// CellPositions maps each cell to its VOB and cell identifiers.
	CellPositions []CellPosition
}

// CellPlayback describes one authored menu cell and its sector bounds.
type CellPlayback struct {
	// Flags contains the raw authored cell playback flags.
	Flags uint16
	// StillTime is the authored cell still duration byte.
	StillTime uint8
	// CommandNumber is the authored cell-command table index.
	CommandNumber uint8
	// PlaybackTime is the cell's authored duration.
	PlaybackTime DVDTime
	// FirstSector is the cell's inclusive first sector in the menu VOB.
	FirstSector uint32
	// FirstILVUEndSector is the authored first interleaved-unit end sector.
	FirstILVUEndSector uint32
	// LastVOBUStartSector is the start sector of the cell's final VOBU.
	LastVOBUStartSector uint32
	// LastSector is the cell's inclusive final sector in the menu VOB.
	LastSector uint32
}

// CellPosition maps a PGC cell to its VOB and cell identifiers.
type CellPosition struct {
	// VOBID is the authored VOB identifier.
	VOBID uint16
	// CellID is the authored cell identifier within the VOB.
	CellID uint8
}

// InspectDirectory inventories VIDEO_TS.IFO and every VTS_nn_0.IFO. A matching
// BUP is used when the primary IFO is missing, unreadable, or invalid.
func InspectDirectory(root string) (disc Disc, err error) {
	defer func() {
		if recover() != nil {
			disc = Disc{}
			err = fmt.Errorf("%w: parser panic", ErrInvalidIFO)
		}
	}()
	entries, err := os.ReadDir(root)
	if err != nil {
		return Disc{}, fmt.Errorf("read VIDEO_TS directory: %w", err)
	}
	names := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		upper := strings.ToUpper(entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			if upper == "VIDEO_TS.IFO" || upper == "VIDEO_TS.BUP" {
				return Disc{}, fmt.Errorf("%w: manager IFO/BUP symlink", ErrInvalidIFO)
			}
			if _, relevant := titleSetNumber(upper); relevant {
				return Disc{}, fmt.Errorf("%w: title-set IFO/BUP symlink", ErrInvalidIFO)
			}
			continue
		}
		if _, duplicate := names[upper]; duplicate {
			return Disc{}, fmt.Errorf("%w: duplicate %s", ErrInvalidIFO, upper)
		}
		names[upper] = entry.Name()
	}

	manager, err := inspectPair(root, names, "VIDEO_TS.IFO", "VIDEO_TS.BUP", KindManager, 0)
	if err != nil {
		return Disc{}, err
	}
	disc = Disc{Manager: manager}
	seenTitleSets := make(map[int]struct{})
	for upperName := range names {
		vts, ok := titleSetNumber(upperName)
		if !ok {
			continue
		}
		if _, seen := seenTitleSets[vts]; seen {
			continue
		}
		seenTitleSets[vts] = struct{}{}
		ifoName := fmt.Sprintf("VTS_%02d_0.IFO", vts)
		bupName := fmt.Sprintf("VTS_%02d_0.BUP", vts)
		file, fileErr := inspectPair(root, names, ifoName, bupName, KindTitleSet, vts)
		if fileErr != nil {
			return Disc{}, fileErr
		}
		disc.TitleSets = append(disc.TitleSets, file)
	}
	sort.Slice(disc.TitleSets, func(i, j int) bool { return disc.TitleSets[i].VTS < disc.TitleSets[j].VTS })
	return disc, nil
}

func titleSetNumber(name string) (int, bool) {
	if len(name) != len("VTS_01_0.IFO") || !strings.HasPrefix(name, "VTS_") ||
		(!strings.HasSuffix(name, "_0.IFO") && !strings.HasSuffix(name, "_0.BUP")) {
		return 0, false
	}
	vts, err := strconv.Atoi(name[4:6])
	return vts, err == nil && vts > 0 && vts <= 99
}

func inspectPair(root string, names map[string]string, ifoName, bupName string, kind Kind, vts int) (File, error) {
	var primaryErr error
	if primary, ok := names[ifoName]; ok {
		file, err := parseFile(filepath.Join(root, primary), kind, vts)
		if err == nil {
			return file, nil
		}
		primaryErr = err
	} else {
		primaryErr = fmt.Errorf("%w: missing %s", ErrInvalidIFO, ifoName)
	}
	backup, ok := names[bupName]
	if !ok {
		return File{}, primaryErr
	}
	file, backupErr := parseFile(filepath.Join(root, backup), kind, vts)
	if backupErr != nil {
		return File{}, errors.Join(primaryErr, backupErr)
	}
	file.Recovered = true
	return file, nil
}

func parseFile(path string, kind Kind, vts int) (File, error) {
	data, err := readBounded(path)
	if err != nil {
		return File{}, err
	}
	file, err := Parse(data, kind, vts)
	if err != nil {
		return File{}, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	file.SourceName = filepath.Base(path)
	return file, nil
}

func readBounded(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open DVD IFO: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIFOBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read DVD IFO: %w", err)
	}
	if len(data) > maxIFOBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}

// Parse parses a single VMGI or VTSI byte stream without panicking. It rejects
// invalid kind/VTS combinations, out-of-bounds tables, and oversized input.
func Parse(data []byte, kind Kind, vts int) (File, error) {
	if len(data) > maxIFOBytes {
		return File{}, ErrTooLarge
	}
	reader := boundedReader{data: data}
	file := File{Kind: kind, VTS: vts}
	var identifier string
	var pgciSector uint32
	var err error
	switch kind {
	case KindManager:
		identifier = "DVDVIDEO-VMG"
		pgciSector, err = reader.u32(200, "VMGM PGCI sector")
		if err == nil {
			var firstPlay uint32
			firstPlay, err = reader.u32(132, "first-play PGC offset")
			if err == nil && firstPlay != 0 {
				var pgc ProgramChain
				pgc, err = reader.programChain(int(firstPlay), 0, 0)
				file.FirstPlay = &pgc
			}
		}
	case KindTitleSet:
		if vts <= 0 || vts > 99 {
			return File{}, fmt.Errorf("%w: VTS number %d", ErrInvalidIFO, vts)
		}
		identifier = "DVDVIDEO-VTS"
		pgciSector, err = reader.u32(208, "VTSM PGCI sector")
	default:
		return File{}, fmt.Errorf("%w: unknown IFO kind %d", ErrInvalidIFO, kind)
	}
	if err != nil {
		return File{}, err
	}
	header, err := reader.bytes(0, len(identifier), "IFO identifier")
	if err != nil || !bytes.Equal(header, []byte(identifier)) {
		return File{}, fmt.Errorf("%w: identifier", ErrInvalidIFO)
	}
	if pgciSector == 0 {
		return file, nil
	}
	if pgciSector > maxIFOBytes/sectorSize {
		return File{}, fmt.Errorf("%w: PGCI sector outside file", ErrInvalidIFO)
	}
	sector, err := boundedUint32(pgciSector, "PGCI sector")
	if err != nil {
		return File{}, err
	}
	base := sector * sectorSize
	if base > len(data) {
		return File{}, fmt.Errorf("%w: PGCI sector outside file", ErrInvalidIFO)
	}
	file.Languages, err = reader.languageUnits(base)
	if err != nil {
		return File{}, err
	}
	return file, nil
}

type boundedReader struct{ data []byte }

func (r boundedReader) bytes(offset, size int, label string) ([]byte, error) {
	if offset < 0 || size < 0 || uint64(offset)+uint64(size) > uint64(len(r.data)) {
		return nil, fmt.Errorf("%w: %s outside file", ErrInvalidIFO, label)
	}
	return r.data[offset : offset+size], nil
}

func (r boundedReader) u16(offset int, label string) (uint16, error) {
	data, err := r.bytes(offset, 2, label)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(data), nil
}

func (r boundedReader) u32(offset int, label string) (uint32, error) {
	data, err := r.bytes(offset, 4, label)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(data), nil
}

func (r boundedReader) languageUnits(base int) ([]LanguageUnit, error) {
	count, err := r.u16(base, "language-unit count")
	if err != nil {
		return nil, err
	}
	if count > maxLanguageUnits {
		return nil, fmt.Errorf("%w: language-unit count %d", ErrInvalidIFO, count)
	}
	lastByte, err := r.u32(base+4, "PGCI last byte")
	if err != nil {
		return nil, err
	}
	lastOffset, err := boundedUint32(lastByte, "PGCI last byte")
	if err != nil {
		return nil, err
	}
	if lastOffset < 7 || base > len(r.data) || lastOffset >= len(r.data)-base {
		return nil, fmt.Errorf("%w: language-unit table bounds", ErrInvalidIFO)
	}
	end := base + lastOffset + 1
	if base+8+int(count)*8 > end {
		return nil, fmt.Errorf("%w: language-unit table bounds", ErrInvalidIFO)
	}
	units := make([]LanguageUnit, 0, count)
	for index := 0; index < int(count); index++ {
		offset := base + 8 + index*8
		lang, readErr := r.bytes(offset, 2, "language code")
		if readErr != nil {
			return nil, readErr
		}
		start, readErr := r.u32(offset+4, "language PGCIT offset")
		if readErr != nil {
			return nil, readErr
		}
		startOffset, readErr := boundedUint32(start, "language PGCIT offset")
		if readErr != nil {
			return nil, readErr
		}
		pgcitBase := base + startOffset
		if startOffset < 8 || pgcitBase >= end {
			return nil, fmt.Errorf("%w: language PGCIT offset", ErrInvalidIFO)
		}
		chains, readErr := r.programChainTable(pgcitBase, end)
		if readErr != nil {
			return nil, fmt.Errorf("language unit %d: %w", index+1, readErr)
		}
		units = append(units, LanguageUnit{
			Code:          string(lang),
			Extension:     r.data[offset+2],
			Exists:        r.data[offset+3],
			ProgramChains: chains,
		})
	}
	return units, nil
}

func (r boundedReader) programChainTable(base, parentEnd int) ([]ProgramChain, error) {
	count, err := r.u16(base, "PGC count")
	if err != nil {
		return nil, err
	}
	if count > maxProgramChains {
		return nil, fmt.Errorf("%w: PGC count %d", ErrInvalidIFO, count)
	}
	lastByte, err := r.u32(base+4, "PGCIT last byte")
	if err != nil {
		return nil, err
	}
	lastOffset, err := boundedUint32(lastByte, "PGCIT last byte")
	if err != nil {
		return nil, err
	}
	if lastOffset < 7 || base > parentEnd || lastOffset >= parentEnd-base {
		return nil, fmt.Errorf("%w: PGC table bounds", ErrInvalidIFO)
	}
	end := base + lastOffset + 1
	if base+8+int(count)*8 > end {
		return nil, fmt.Errorf("%w: PGC table bounds", ErrInvalidIFO)
	}
	chains := make([]ProgramChain, 0, count)
	for index := 0; index < int(count); index++ {
		offset := base + 8 + index*8
		entry, readErr := r.bytes(offset, 1, "PGC entry")
		if readErr != nil {
			return nil, readErr
		}
		start, readErr := r.u32(offset+4, "PGC offset")
		if readErr != nil {
			return nil, readErr
		}
		startOffset, readErr := boundedUint32(start, "PGC offset")
		if readErr != nil {
			return nil, readErr
		}
		pgcBase := base + startOffset
		if startOffset < 8 || pgcBase > end || pgcSize > end-pgcBase {
			return nil, fmt.Errorf("%w: PGC %d offset", ErrInvalidIFO, index+1)
		}
		pgc, readErr := r.programChain(pgcBase, index+1, entry[0])
		if readErr != nil {
			return nil, fmt.Errorf("PGC %d: %w", index+1, readErr)
		}
		chains = append(chains, pgc)
	}
	return chains, nil
}

func (r boundedReader) programChain(base, number int, entryID uint8) (ProgramChain, error) {
	fixed, err := r.bytes(base, pgcSize, "PGC header")
	if err != nil {
		return ProgramChain{}, err
	}
	programs, cells := fixed[2], fixed[3]
	if programs > cells {
		return ProgramChain{}, fmt.Errorf("%w: programs=%d cells=%d", ErrInvalidIFO, programs, cells)
	}
	pgc := ProgramChain{
		Number:       number,
		EntryID:      entryID,
		MenuID:       entryID & 0x0f,
		Programs:     programs,
		Cells:        cells,
		PlaybackTime: dvdTime(fixed[4:8]),
		Next:         binary.BigEndian.Uint16(fixed[156:158]),
		Previous:     binary.BigEndian.Uint16(fixed[158:160]),
		GoUp:         binary.BigEndian.Uint16(fixed[160:162]),
		PlaybackMode: fixed[162],
		StillTime:    fixed[163],
	}
	for index := range pgc.AudioControl {
		pgc.AudioControl[index] = binary.BigEndian.Uint16(fixed[12+index*2 : 14+index*2])
	}
	for index := range pgc.SubpictureControl {
		pgc.SubpictureControl[index] = binary.BigEndian.Uint32(fixed[28+index*4 : 32+index*4])
	}
	for index := range pgc.Palette {
		pgc.Palette[index] = binary.BigEndian.Uint32(fixed[164+index*4 : 168+index*4])
	}
	commandOffset := int(binary.BigEndian.Uint16(fixed[228:230]))
	programOffset := int(binary.BigEndian.Uint16(fixed[230:232]))
	cellPlaybackOffset := int(binary.BigEndian.Uint16(fixed[232:234]))
	cellPositionOffset := int(binary.BigEndian.Uint16(fixed[234:236]))
	if commandOffset != 0 {
		pgc.PreCommands, pgc.PostCommands, pgc.CellCommands, err = r.commandTable(base + commandOffset)
		if err != nil {
			return ProgramChain{}, err
		}
	}
	if programs != 0 {
		if programOffset < pgcSize {
			return ProgramChain{}, fmt.Errorf("%w: program map offset", ErrInvalidIFO)
		}
		programMap, readErr := r.bytes(base+programOffset, int(programs), "program map")
		if readErr != nil {
			return ProgramChain{}, readErr
		}
		pgc.ProgramMap = append([]uint8(nil), programMap...)
		for index, cell := range pgc.ProgramMap {
			if cell == 0 || cell > cells || index > 0 && cell < pgc.ProgramMap[index-1] {
				return ProgramChain{}, fmt.Errorf("%w: program %d cell mapping", ErrInvalidIFO, index+1)
			}
		}
	}
	if cells != 0 {
		if cellPlaybackOffset < pgcSize || cellPositionOffset < pgcSize {
			return ProgramChain{}, fmt.Errorf("%w: cell table offset", ErrInvalidIFO)
		}
		pgc.CellPlayback, err = r.cellPlayback(base+cellPlaybackOffset, int(cells))
		if err != nil {
			return ProgramChain{}, err
		}
		pgc.CellPositions, err = r.cellPositions(base+cellPositionOffset, int(cells))
		if err != nil {
			return ProgramChain{}, err
		}
	}
	return pgc, nil
}

func (r boundedReader) commandTable(base int) ([]vm.Command, []vm.Command, []vm.Command, error) {
	header, err := r.bytes(base, 8, "command table")
	if err != nil {
		return nil, nil, nil, err
	}
	counts := [3]int{
		int(binary.BigEndian.Uint16(header[0:2])),
		int(binary.BigEndian.Uint16(header[2:4])),
		int(binary.BigEndian.Uint16(header[4:6])),
	}
	total := counts[0] + counts[1] + counts[2]
	lastByte := int(binary.BigEndian.Uint16(header[6:8]))
	if lastByte < 7 || total > (lastByte+1-8)/8 {
		return nil, nil, nil, fmt.Errorf("%w: command table bounds", ErrInvalidIFO)
	}
	data, err := r.bytes(base+8, total*8, "VM commands")
	if err != nil {
		return nil, nil, nil, err
	}
	commands := make([]vm.Command, total)
	for index := range commands {
		copy(commands[index][:], data[index*8:(index+1)*8])
	}
	preEnd := counts[0]
	postEnd := preEnd + counts[1]
	return commands[:preEnd], commands[preEnd:postEnd], commands[postEnd:], nil
}

func (r boundedReader) cellPlayback(base, count int) ([]CellPlayback, error) {
	data, err := r.bytes(base, count*24, "cell playback table")
	if err != nil {
		return nil, err
	}
	cells := make([]CellPlayback, count)
	for index := range cells {
		item := data[index*24 : (index+1)*24]
		cells[index] = CellPlayback{
			Flags:               binary.BigEndian.Uint16(item[0:2]),
			StillTime:           item[2],
			CommandNumber:       item[3],
			PlaybackTime:        dvdTime(item[4:8]),
			FirstSector:         binary.BigEndian.Uint32(item[8:12]),
			FirstILVUEndSector:  binary.BigEndian.Uint32(item[12:16]),
			LastVOBUStartSector: binary.BigEndian.Uint32(item[16:20]),
			LastSector:          binary.BigEndian.Uint32(item[20:24]),
		}
		if cells[index].FirstSector > cells[index].LastSector || cells[index].LastVOBUStartSector > cells[index].LastSector {
			return nil, fmt.Errorf("%w: cell %d sector bounds", ErrInvalidIFO, index+1)
		}
	}
	return cells, nil
}

func (r boundedReader) cellPositions(base, count int) ([]CellPosition, error) {
	data, err := r.bytes(base, count*4, "cell position table")
	if err != nil {
		return nil, err
	}
	positions := make([]CellPosition, count)
	for index := range positions {
		item := data[index*4 : (index+1)*4]
		positions[index] = CellPosition{VOBID: binary.BigEndian.Uint16(item[0:2]), CellID: item[3]}
	}
	return positions, nil
}

func dvdTime(data []byte) DVDTime {
	return DVDTime{
		Hour:   data[0],
		Minute: data[1],
		Second: data[2],
		Frame:  data[3],
	}
}

func boundedUint32(value uint32, label string) (int, error) {
	if value > maxIFOBytes {
		return 0, fmt.Errorf("%w: %s exceeds bounded file size", ErrInvalidIFO, label)
	}
	return int(value), nil
}
