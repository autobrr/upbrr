// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ifo

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// FeatureTitle describes an authored global DVD title, independently of menus.
// Unsupported titles remain visible for explicit selection feedback.
type FeatureTitle struct {
	Number, VTS, VTSTitle, Chapters, Angles int
	Duration                                time.Duration
	Duration90                              int64
	Chain                                   ProgramChain
	TimeUnit                                uint8
	TimeMap                                 []uint32
	VOBU                                    []uint32
	Unsupported                             string
}

// FeatureDisc owns the selected IFO/BUP bytes used to interpret feature titles.
// Metadata contains base names only and permits complete structural hashing.
type FeatureDisc struct {
	Titles   []FeatureTitle
	Metadata map[string][]byte
}

// InspectFeatures reads bounded authored title, PTT, PGC and seek-map tables.
// It neither opens title payloads nor executes the DVD VM.
func InspectFeatures(root string) (FeatureDisc, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return FeatureDisc{}, fmt.Errorf("DVD feature metadata: %w", err)
	}
	names := map[string]string{}
	for _, entry := range entries {
		name := strings.ToUpper(entry.Name())
		if entry.IsDir() {
			continue
		}
		if _, duplicate := names[name]; duplicate {
			return FeatureDisc{}, fmt.Errorf("%w: duplicate structural name", ErrInvalidIFO)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return FeatureDisc{}, fmt.Errorf("%w: linked DVD resource", ErrInvalidIFO)
		}
		names[name] = entry.Name()
	}
	disc := FeatureDisc{Metadata: map[string][]byte{}}
	read := func(name string, kind Kind, vts int, validate func([]byte) error) ([]byte, error) {
		for _, candidate := range []string{name + ".IFO", name + ".BUP"} {
			actual, exists := names[candidate]
			if !exists {
				continue
			}
			data, readErr := readBounded(filepath.Join(root, actual))
			if readErr != nil {
				continue
			}
			if _, parseErr := Parse(data, kind, vts); parseErr != nil {
				continue
			}
			if validate(data) != nil {
				continue
			}
			if len(data)+featureMetadataSize(disc.Metadata) > maxIFOBytes {
				return nil, ErrTooLarge
			}
			disc.Metadata[candidate] = data
			return data, nil
		}
		return nil, fmt.Errorf("%w: missing or invalid feature IFO/BUP", ErrInvalidIFO)
	}
	manager, err := read("VIDEO_TS", KindManager, 0, func(data []byte) error { _, err := featureTitles(data); return err })
	if err != nil {
		return FeatureDisc{}, err
	}
	titles, err := featureTitles(manager)
	if err != nil {
		return FeatureDisc{}, err
	}
	sets := map[int][]byte{}
	indexes := map[int][]uint32{}
	indexEntries := 0
	for _, title := range titles {
		data := sets[title.VTS]
		if data == nil {
			data, err = read(fmt.Sprintf("VTS_%02d_0", title.VTS), KindTitleSet, title.VTS, func(data []byte) error {
				for _, candidate := range titles {
					if candidate.VTS == title.VTS {
						if err := candidate.resolve(data); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				return FeatureDisc{}, err
			}
			sets[title.VTS] = data
		}
		if err := title.resolve(data); err != nil {
			return FeatureDisc{}, err
		}
		if prior, exists := indexes[title.VTS]; exists {
			title.VOBU = prior
		} else {
			indexes[title.VTS] = title.VOBU
			indexEntries += len(title.VOBU)
		}
		indexEntries += len(title.TimeMap)
		if indexEntries > 1_000_000 {
			return FeatureDisc{}, ErrTooLarge
		}
		disc.Titles = append(disc.Titles, title)
	}
	return disc, nil
}

func featureTitles(manager []byte) ([]FeatureTitle, error) {
	table, err := featureTable(manager, 196)
	if err != nil {
		return nil, err
	}
	count := int(binary.BigEndian.Uint16(table))
	if count < 1 || count > 99 || count > (len(table)-8)/12 {
		return nil, ErrInvalidIFO
	}
	titles := make([]FeatureTitle, 0, count)
	for index := range count {
		entry := table[8+index*12 : 8+(index+1)*12]
		title := FeatureTitle{
			Number:   index + 1,
			Angles:   int(entry[1]),
			Chapters: int(binary.BigEndian.Uint16(entry[2:4])),
			VTS:      int(entry[6]),
			VTSTitle: int(entry[7]),
		}
		if title.VTS < 1 || title.VTS > 99 || title.VTSTitle < 1 || title.Angles < 1 || title.Angles > 9 || title.Chapters < 1 {
			return nil, ErrInvalidIFO
		}
		titles = append(titles, title)
	}
	return titles, nil
}

func featureMetadataSize(metadata map[string][]byte) int {
	var total int
	for _, data := range metadata {
		total += len(data)
	}
	return total
}

// featureTable bounds every nested parser to the declared table, not the file.
func featureTable(data []byte, pointer int) ([]byte, error) {
	if pointer < 0 || pointer > len(data)-4 {
		return nil, ErrInvalidIFO
	}
	sector := uint64(binary.BigEndian.Uint32(data[pointer : pointer+4]))
	base := sector * sectorSize
	if sector == 0 || base+8 > uint64(len(data)) {
		return nil, ErrInvalidIFO
	}
	length := uint64(binary.BigEndian.Uint32(data[base+4:base+8])) + 1
	if length < 8 || length > uint64(len(data))-base {
		return nil, ErrInvalidIFO
	}
	return data[base : base+length], nil
}

func (title *FeatureTitle) resolve(data []byte) error {
	if title.Chapters < 1 || title.Chapters > 65535 {
		return ErrInvalidIFO
	}
	ptt, err := featureTable(data, 200)
	if err != nil {
		return err
	}
	count := int(binary.BigEndian.Uint16(ptt))
	if count < title.VTSTitle || count > 99 || 8+count*4 > len(ptt) {
		return ErrInvalidIFO
	}
	start := uint64(binary.BigEndian.Uint32(ptt[8+(title.VTSTitle-1)*4:]))
	end := uint64(len(ptt))
	if title.VTSTitle < count {
		end = uint64(binary.BigEndian.Uint32(ptt[8+title.VTSTitle*4:]))
	}
	if start < uint64(8+count*4) || end < start || end > uint64(len(ptt)) || end-start != uint64(title.Chapters)*4 {
		return ErrInvalidIFO
	}
	pgc := int(binary.BigEndian.Uint16(ptt[start:]))
	for chapter := range title.Chapters {
		entry := ptt[start+uint64(chapter)*4:]
		if int(binary.BigEndian.Uint16(entry)) != pgc {
			title.Unsupported = "multi_pgc_navigation"
			return nil
		}
		if binary.BigEndian.Uint16(entry[2:]) != uint16(chapter+1) {
			title.Unsupported = "nonsequential_programs"
			return nil
		}
	}
	table, err := featureTable(data, 204)
	if err != nil {
		return err
	}
	chains, err := (boundedReader{data: table}).programChainTable(0, len(table))
	if err != nil || pgc < 1 || pgc > len(chains) {
		return fmt.Errorf("%w: feature PGC", ErrInvalidIFO)
	}
	title.Chain = chains[pgc-1]
	// Explicit title/part entry starts its programme directly, as dvdnav_part_play
	// does, bypassing menu pre-command navigation and stopping at this PGC's end.
	// Commands that redirect playback during a cell are rejected below.
	if title.Angles != 1 || title.Chain.Next != 0 || title.Chain.PlaybackMode != 0 || title.Chain.StillTime != 0 ||
		int(title.Chain.Programs) != title.Chapters || title.Chain.ProgramMap[0] != 1 {
		title.Unsupported = "nonsequential_programme"
		return nil
	}
	for _, cell := range title.Chain.CellPlayback {
		if cell.Flags&0xf400 != 0 || cell.StillTime != 0 || cell.FirstSector > cell.LastVOBUStartSector ||
			cell.LastVOBUStartSector > cell.LastSector {
			title.Unsupported = "interleaved_or_invalid_cell"
			return nil
		}
		if cell.CommandNumber != 0 &&
			(int(cell.CommandNumber) > len(title.Chain.CellCommands) || title.Chain.CellCommands[cell.CommandNumber-1] != ([8]byte{})) {
			title.Unsupported = "cell_navigation"
			return nil
		}
		duration, durationErr := cell.PlaybackTime.Duration()
		if durationErr != nil || duration <= 0 {
			return ErrInvalidIFO
		}
		ticks, tickErr := cell.PlaybackTime.Ticks90()
		if tickErr != nil {
			return tickErr
		}
		title.Duration90 += ticks
		title.Duration += duration
	}
	pgcTicks, err := title.Chain.PlaybackTime.Ticks90()
	if err != nil || title.Duration90 != pgcTicks {
		title.Unsupported = "contradictory_cell_duration"
		return nil
	}
	// Seek maps are optional in DVD-Video; absent maps are a visible comparison
	// capability limitation rather than permission to scan a whole title.
	tmaps, err := featureTable(data, 212)
	if err != nil {
		title.Unsupported = "missing_time_map"
		return nil
	}
	if int(binary.BigEndian.Uint16(tmaps)) < pgc || 8+pgc*4 > len(tmaps) {
		return ErrInvalidIFO
	}
	mapStart := uint64(binary.BigEndian.Uint32(tmaps[8+(pgc-1)*4:]))
	if mapStart+4 > uint64(len(tmaps)) {
		return ErrInvalidIFO
	}
	title.TimeUnit = tmaps[mapStart]
	entries := uint64(binary.BigEndian.Uint16(tmaps[mapStart+2:]))
	if title.TimeUnit == 0 || entries > (uint64(len(tmaps))-mapStart-4)/4 {
		return ErrInvalidIFO
	}
	for index := range entries {
		title.TimeMap = append(title.TimeMap, binary.BigEndian.Uint32(tmaps[mapStart+4+index*4:]))
	}
	// VOBU_ADMAP has a four-byte last-byte header, unlike the other tables.
	base := uint64(binary.BigEndian.Uint32(data[228:232])) * sectorSize
	if base == 0 || base+4 > uint64(len(data)) {
		title.Unsupported = "missing_vobu_map"
		return nil
	}
	length := uint64(binary.BigEndian.Uint32(data[base:])) + 1
	if length < 4 || length > uint64(len(data))-base || (length-4)%4 != 0 || (length-4)/4 > 1_000_000 {
		return ErrInvalidIFO
	}
	for offset := base + 4; offset < base+length; offset += 4 {
		sector := binary.BigEndian.Uint32(data[offset:])
		if len(title.VOBU) > 0 && sector <= title.VOBU[len(title.VOBU)-1] {
			return ErrInvalidIFO
		}
		title.VOBU = append(title.VOBU, sector)
	}
	addresses, err := featureTable(data, 224)
	if err != nil || (len(addresses)-8)%12 != 0 {
		return ErrInvalidIFO
	}
	for index, cell := range title.Chain.CellPlayback {
		position := title.Chain.CellPositions[index]
		found := false
		for offset := 8; offset < len(addresses); offset += 12 {
			entry := addresses[offset : offset+12]
			if binary.BigEndian.Uint16(entry) == position.VOBID && entry[2] == position.CellID {
				start, end := binary.BigEndian.Uint32(entry[4:]), binary.BigEndian.Uint32(entry[8:])
				if found || start > end || cell.FirstSector < start || cell.LastSector > end {
					return ErrInvalidIFO
				}
				found = true
			}
		}
		if !found {
			return ErrInvalidIFO
		}
	}
	for _, proposal := range title.TimeMap {
		if _, found := slices.BinarySearch(title.VOBU, proposal&0x7fffffff); !found {
			return ErrInvalidIFO
		}
	}
	return nil
}

// Ticks90 preserves the exact authored PAL/NTSC frame fraction in MPEG ticks.
func (value DVDTime) Ticks90() (int64, error) {
	if _, err := value.Duration(); err != nil {
		return 0, err
	}
	hour, _ := decodeBCD(value.Hour)
	minute, _ := decodeBCD(value.Minute)
	second, _ := decodeBCD(value.Second)
	frames, _ := decodeBCD(value.Frame & 0x3f)
	ticks := int64(hour*3600+minute*60+second) * 90000
	if value.Frame&0xc0 == 0x40 {
		ticks += int64(frames) * 3600
	}
	if value.Frame&0xc0 == 0xc0 {
		ticks += int64(frames) * 3003
	}
	return ticks, nil
}
