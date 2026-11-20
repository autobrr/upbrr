// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/autobrr/upbrr/internal/pathing"
)

// CLPIEntry is a proposed random-access point in the original 192-byte packet
// domain. PTS45 is quantized CLPI evidence, not a decoded frame identity.
type CLPIEntry struct {
	PTS45       int64
	Packet      uint32
	AngleChange bool
	EndOffset   uint8
}

// ErrComparisonIndex identifies absent, malformed or unbounded seek evidence.
var ErrComparisonIndex = errors.New("comparison source has no valid bounded random-access index")

// ReadCLPICPI reads only the selected CLPI's primary-PID CPI map. Layout and
// reconstruction follow libbluray's clpi_parse.c; no payload index is built.
func ReadCLPICPI(root, path string, pid uint16) ([]CLPIEntry, error) {
	file, err := pathing.OpenFileWithinRoot(root, path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("blu-ray random access index: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("blu-ray random access index: %w", err)
	}
	return parseCLPICPI(data, pid)
}

func parseCLPICPI(data []byte, pid uint16) ([]CLPIEntry, error) {
	if len(data) < 32 || len(data) > 64<<20 || string(data[:4]) != "HDMV" {
		return nil, ErrComparisonIndex
	}
	base := uint64(binary.BigEndian.Uint32(data[16:20]))
	if base+8 > uint64(len(data)) {
		return nil, ErrComparisonIndex
	}
	length := uint64(binary.BigEndian.Uint32(data[base:]))
	if length < 4 || length > uint64(len(data))-base-4 {
		return nil, ErrComparisonIndex
	}
	section := data[base+4 : base+4+length]
	if section[1]&15 != 1 {
		return nil, ErrComparisonIndex
	}
	count := int(section[3])
	if count > (len(section)-4)/12 {
		return nil, ErrComparisonIndex
	}
	var entries []CLPIEntry
	for index := range count {
		header := section[4+index*12 : 4+(index+1)*12]
		if binary.BigEndian.Uint16(header) != pid {
			continue
		}
		if entries != nil {
			return nil, ErrComparisonIndex
		}
		// 16 PID, 10 reserved, 4 type, 16 coarse count, 18 fine count.
		bits := binary.BigEndian.Uint64(header[:8])
		kind := (bits >> 34) & 15
		coarseCount := (bits >> 18) & 65535
		fineCount := bits & 0x3ffff
		start := uint64(binary.BigEndian.Uint32(header[8:])) + 2
		if kind != 1 || coarseCount == 0 || fineCount == 0 || coarseCount+fineCount > 1_000_000 || start+4+coarseCount*8 > uint64(len(section)) {
			return nil, ErrComparisonIndex
		}
		fineStart := start + uint64(binary.BigEndian.Uint32(section[start:]))
		if fineStart < start+4+coarseCount*8 || fineStart+fineCount*4 > uint64(len(section)) {
			return nil, ErrComparisonIndex
		}
		for coarse := range coarseCount {
			word := binary.BigEndian.Uint64(section[start+4+coarse*8:])
			ref := word >> 46
			end := fineCount
			if coarse+1 < coarseCount {
				end = binary.BigEndian.Uint64(section[start+4+(coarse+1)*8:]) >> 46
			}
			if ref >= end || end > fineCount || coarse == 0 && ref != 0 {
				return nil, ErrComparisonIndex
			}
			ptsBase := ((word >> 32) & 0x3ffe) << 18
			packetBase := (word & 0xffffffff) &^ uint64(0x1ffff)
			for fine := ref; fine < end; fine++ {
				value := binary.BigEndian.Uint32(section[fineStart+fine*4:])
				packet := packetBase + uint64(value&0x1ffff)
				if packet > 0xffffffff {
					return nil, ErrComparisonIndex
				}
				entry := CLPIEntry{
					PTS45:       int64(ptsBase + uint64((value>>17)&0x7ff)*256),
					Packet:      uint32(packet),
					AngleChange: value>>31 != 0,
					EndOffset:   uint8((value >> 28) & 7),
				}
				if len(entries) > 0 && entry.Packet <= entries[len(entries)-1].Packet {
					return nil, ErrComparisonIndex
				}
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) == 0 {
		return nil, ErrComparisonIndex
	}
	return entries, nil
}
