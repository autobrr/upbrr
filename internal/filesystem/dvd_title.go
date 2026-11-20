// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
	"github.com/autobrr/upbrr/internal/dvdvideo/nav"
	"github.com/autobrr/upbrr/internal/pathing"
)

// ComparisonSpan is an original, half-open encoded byte range. It is private
// replay evidence, never an FFmpeg argument supplied by a client.
type ComparisonSpan struct {
	Path       string
	Start, End int64
}

// DVDTitleReader exposes only numbered title VOBs as one logical sector domain.
// Menu VOBs, links, missing parts and non-sector-aligned resources are rejected.
type DVDTitleReader struct {
	files  []*os.File
	starts []int64
	paths  []string
	Size   int64
}

// OpenDVDTitle opens a read-only logical view; the caller must Close it.
func OpenDVDTitle(root string, vts int) (*DVDTitleReader, error) {
	if vts < 1 || vts > 99 {
		return nil, ErrComparisonIndex
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("DVD title input: %w", err)
	}
	reader := &DVDTitleReader{}
	ok := false
	defer func() {
		if !ok {
			_ = reader.Close()
		}
	}()
	names := map[string]string{}
	for _, entry := range entries {
		upper := strings.ToUpper(entry.Name())
		if _, exists := names[upper]; exists {
			return nil, ErrComparisonIndex
		}
		names[upper] = entry.Name()
	}
	missing := false
	for part := 1; part <= 9; part++ {
		name, exists := names[fmt.Sprintf("VTS_%02d_%d.VOB", vts, part)]
		if !exists {
			missing = true
			continue
		}
		if missing {
			return nil, ErrComparisonIndex
		}
		path := filepath.Join(root, name)
		// Entries are single directory names; the contained open below rejects links.
		info, err := os.Lstat(path) // #nosec G703 -- path is confined to the selected DVD root.
		if err != nil || !info.Mode().IsRegular() || !pathing.IsWithinRoot(root, path) {
			return nil, ErrComparisonIndex
		}
		file, err := pathing.OpenFileWithinRoot(root, path, os.O_RDONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("DVD title input: %w", err)
		}
		reader.files = append(reader.files, file)
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() <= 0 || opened.Size()%nav.SectorSize != 0 {
			return nil, ErrComparisonIndex
		}
		reader.paths = append(reader.paths, path)
		reader.starts = append(reader.starts, reader.Size)
		if opened.Size() > 1<<40-reader.Size {
			return nil, ErrComparisonIndex
		}
		reader.Size += opened.Size()
	}
	if len(reader.files) == 0 {
		return nil, ErrComparisonIndex
	}
	ok = true
	return reader, nil
}

// Close closes every physical part, including after partial-open failure.
func (r *DVDTitleReader) Close() error {
	var result error
	for _, file := range r.files {
		result = errors.Join(result, file.Close())
	}
	return result
}

// ReadAt preserves contiguous sectors across a physical VOB split.
func (r *DVDTitleReader) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, ErrComparisonIndex
	}
	total := 0
	for len(p) > 0 && offset < r.Size {
		index := sort.Search(len(r.starts), func(i int) bool { return r.starts[i] > offset }) - 1
		if index < 0 {
			return total, ErrComparisonIndex
		}
		end := r.Size
		if index+1 < len(r.starts) {
			end = r.starts[index+1]
		}
		count := int(min(int64(len(p)), end-offset))
		n, err := r.files[index].ReadAt(p[:count], offset-r.starts[index])
		total += n
		offset += int64(n)
		p = p[n:]
		if err != nil {
			return total, fmt.Errorf("DVD title input: %w", err)
		}
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}

func (r *DVDTitleReader) spans(start, end uint64) ([]ComparisonSpan, error) {
	if r.Size < 0 || r.Size > 1<<40 || start > 1<<29 || start >= end || end > 1<<29 || end > uint64(r.Size/nav.SectorSize) ||
		(end-start)*nav.SectorSize > 2<<30 {
		return nil, ErrComparisonIndex
	}
	var spans []ComparisonSpan
	first, last := int64(start)*nav.SectorSize, int64(end)*nav.SectorSize
	for i, path := range r.paths {
		stop := r.Size
		if i+1 < len(r.starts) {
			stop = r.starts[i+1]
		}
		a, b := max(first, r.starts[i]), min(last, stop)
		if a < b {
			spans = append(spans, ComparisonSpan{
				Path:  path,
				Start: a - r.starts[i],
				End:   b - r.starts[i],
			})
		}
	}
	return spans, nil
}

// DVDWindow maps one cell occurrence onto original clock and bounded sectors.
// SourceOrigin90 + cell-relative time identifies its unadjusted presentation clock.
type DVDWindow struct {
	Preroll90                     int64
	Cell                          int
	TitleOffset90, SourceOrigin90 int64
	Start90, End90                int64
	Spans                         []ComparisonSpan
	NAVProbes                     int
}

// ResolveDVDWindows partitions a presentation window at authored cell boundaries.
// Index proposals are validated with NAV clocks; no whole-cell scan is used.
func ResolveDVDWindows(reader *DVDTitleReader, title ifo.FeatureTitle, start90, end90 int64) ([]DVDWindow, error) {
	if title.Unsupported != "" || start90 < 0 || end90 <= start90 || end90-start90 > 64*90000 || end90 > title.Duration90 {
		return nil, ErrComparisonIndex
	}
	var windows []DVDWindow
	var offset int64
	probes := 0
	for index, cell := range title.Chain.CellPlayback {
		duration, err := cell.PlaybackTime.Ticks90()
		if err != nil {
			return nil, fmt.Errorf("DVD title input: %w", err)
		}
		next := offset + duration
		if start90 < next && end90 > offset {
			priorProbes := probes
			position := title.Chain.CellPositions[index]
			probe := func(sector uint32) (nav.Packet, int64, error) {
				probes++
				if probes > 4096 || sector < cell.FirstSector || sector > cell.LastVOBUStartSector {
					return nav.Packet{}, 0, ErrComparisonIndex
				}
				buffer := make([]byte, nav.SectorSize)
				if _, err := reader.ReadAt(buffer, int64(sector)*nav.SectorSize); err != nil {
					return nav.Packet{}, 0, err
				}
				packet, err := nav.ParseNAVSector(buffer)
				if err != nil || packet.DSI.VOBID != position.VOBID || packet.DSI.CellID != position.CellID || packet.DSI.LogicalBlock != sector ||
					packet.PCI.LogicalBlock != sector {
					return nav.Packet{}, 0, ErrComparisonIndex
				}
				raw := packet.DSI.CellElapsedTime
				elapsed, err := (ifo.DVDTime{
					Hour:   raw[0],
					Minute: raw[1],
					Second: raw[2],
					Frame:  raw[3],
				}).Ticks90()
				if err != nil {
					return packet, 0, fmt.Errorf("DVD title clock: %w", err)
				}
				return packet, elapsed, nil
			}
			first, elapsed, err := probe(cell.FirstSector)
			if err != nil {
				return nil, err
			}
			var originalClock int64
			clockFound := false
			for sector := uint64(cell.FirstSector) + 1; sector <= min(uint64(cell.LastSector), uint64(cell.FirstSector)+uint64(first.DSI.VOBUEndAddress), uint64(cell.FirstSector)+64); sector++ {
				if sector > 1<<32 {
					return nil, ErrComparisonIndex
				}
				probes++
				if probes > 4096 {
					return nil, ErrComparisonIndex
				}
				buffer := make([]byte, nav.SectorSize)
				if _, err := reader.ReadAt(buffer, int64(sector)*nav.SectorSize); err != nil {
					return nil, err
				}
				packets, err := nav.ParseVideoPES(buffer)
				if err != nil {
					return nil, fmt.Errorf("DVD title input: %w", err)
				}
				for _, packet := range packets {
					if packet.HasPTS && packet.StreamID == 0xe0 {
						if packet.PTS >= 1<<33 {
							return nil, ErrComparisonIndex
						}
						originalClock = int64(packet.PTS)
						clockFound = true
						break
					}
				}
				if clockFound {
					break
				}
			}
			if !clockFound {
				return nil, ErrComparisonIndex
			}
			startClock := comparisonClockNear(int64(first.PCI.VOBUStartPTS), originalClock)
			if absDVD(startClock-originalClock) > 90000 {
				return nil, ErrComparisonIndex
			}
			origin := startClock - elapsed
			last, lastTime, err := probe(cell.LastVOBUStartSector)
			if err != nil {
				return nil, err
			}
			// DVD PCI clocks are 32-bit. A cell can cross one wrap; retain its
			// epoch rather than allowing a decoder-adjusted timestamp to define it.
			unwrap := func(value uint32) int64 { return comparisonClockNear(int64(value), origin+lastTime) }
			if absDVD(unwrap(last.PCI.VOBUStartPTS)-origin-lastTime) > 3600 || absDVD(unwrap(last.PCI.VOBUEndPTS)-origin-duration) > 3600 {
				return nil, fmt.Errorf(
					"%w: cell=%d NAV start residual=%d end residual=%d",
					ErrComparisonIndex,
					index,
					unwrap(last.PCI.VOBUStartPTS)-origin-lastTime,
					unwrap(last.PCI.VOBUEndPTS)-origin-duration,
				)
			}
			lo, hi := slices.BinarySearch(title.VOBU, cell.FirstSector)
			if !hi {
				return nil, ErrComparisonIndex
			}
			stop, found := slices.BinarySearch(title.VOBU, cell.LastVOBUStartSector)
			if !found {
				return nil, ErrComparisonIndex
			}
			wanted := max(start90-offset, 0)
			left, right := lo, stop+1
			// The authored time map proposes a search pivot. Original NAV clocks
			// validate it; neither the map nor its nominal second is a frame clock.
			if title.TimeUnit > 0 && len(title.TimeMap) > 0 {
				slot := min(start90/(int64(title.TimeUnit)*90000)-1, int64(len(title.TimeMap)-1))
				if slot >= 0 {
					proposal := title.TimeMap[slot] & 0x7fffffff
					if proposal >= cell.FirstSector && proposal <= cell.LastVOBUStartSector {
						pivot, found := slices.BinarySearch(title.VOBU, proposal)
						packet, stamp, err := probe(proposal)
						if !found || err != nil || absDVD(comparisonClockNear(int64(packet.PCI.VOBUStartPTS), origin+stamp)-origin-stamp) > 3600 {
							return nil, ErrComparisonIndex
						}
						if stamp <= wanted {
							left = pivot
						} else {
							right = pivot + 1
						}
					}
				}
			}
			for left < right {
				mid := left + (right-left)/2
				_, stamp, err := probe(title.VOBU[mid])
				if err != nil {
					return nil, err
				}
				if stamp <= wanted {
					left = mid + 1
				} else {
					right = mid
				}
			}
			begin := max(lo, left-1)
			// Include a preceding VOBU for open-GOP dependencies, within ten seconds.
			if begin > lo {
				begin--
			}
			_, beginTime, err := probe(title.VOBU[begin])
			if err != nil || wanted-beginTime > 10*90000 {
				return nil, ErrComparisonIndex
			}
			wantedEnd := min(end90-offset, duration)
			left, right = begin, stop+1
			for left < right {
				mid := left + (right-left)/2
				_, stamp, err := probe(title.VOBU[mid])
				if err != nil {
					return nil, err
				}
				if stamp <= wantedEnd+90000 {
					left = mid + 1
				} else {
					right = mid
				}
			}
			sectorEnd := uint64(cell.LastSector) + 1
			if left <= stop {
				sectorEnd = uint64(title.VOBU[left])
			}
			spans, err := reader.spans(uint64(title.VOBU[begin]), sectorEnd)
			if err != nil {
				return nil, err
			}
			windows = append(windows, DVDWindow{
				Preroll90:      wanted - beginTime,
				Cell:           index,
				TitleOffset90:  offset,
				SourceOrigin90: origin,
				Start90:        origin + wanted,
				End90:          origin + wantedEnd,
				Spans:          spans,
				NAVProbes:      probes - priorProbes,
			})
		}
		offset = next
	}
	return windows, nil
}

func comparisonClockNear(value, expected int64) int64 {
	const modulus = int64(1 << 32)
	result := (expected/modulus)*modulus + value%modulus
	if result-expected > modulus/2 {
		result -= modulus
	}
	if expected-result > modulus/2 {
		result += modulus
	}
	return result
}

func absDVD(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
