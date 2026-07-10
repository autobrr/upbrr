// Package nav parses DVD-Video NAV PCI/DSI packets and routes DVD subpicture
// private-stream payloads.
//
// PCI/DSI layouts are derived from VideoLAN libdvdread 7.0.1 nav_types.h and
// nav_read.c (commit c7f373951bae9642e1ce1fbb2cd02f92c09756e0),
// GPL-2.0-or-later.
package nav

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/autobrr/upbrr/internal/dvdvideo/vm"
)

const (
	// SectorSize is the fixed byte size of a DVD logical sector.
	SectorSize = 2048
	// PCIPayloadSize is the required private-stream-2 PCI payload size.
	PCIPayloadSize        = 979
	minimumDSIPayloadSize = 32
	// MaxButtons is the DVD-Video maximum button count per PCI.
	MaxButtons = 36
	// MaxButtonGroups is the DVD-Video maximum display-group count per PCI.
	MaxButtonGroups     = 3
	defaultPacketLimit  = 4096
	defaultPayloadLimit = 16 << 20
	defaultSectorLimit  = 4096
)

// ErrInvalidNAV identifies malformed or out-of-bounds NAV/PES data.
var ErrInvalidNAV = errors.New("invalid DVD NAV data")

// PCI contains presentation timing and authored highlight/button state.
type PCI struct {
	// LogicalBlock is the PCI's authored navigation logical-block number.
	LogicalBlock uint32
	// VOBUCategory contains the raw authored VOBU category flags.
	VOBUCategory uint16
	// VOBUStartPTS is the presentation timestamp at VOBU start.
	VOBUStartPTS uint32
	// VOBUEndPTS is the presentation timestamp at VOBU end.
	VOBUEndPTS uint32
	// VOBUSequenceEndPTS is the presentation timestamp at sequence end.
	VOBUSequenceEndPTS uint32
	// HighlightStatus is the raw authored highlight status.
	HighlightStatus uint16
	// HighlightStartPTS is the highlight activation timestamp.
	HighlightStartPTS uint32
	// HighlightEndPTS is the highlight end timestamp.
	HighlightEndPTS uint32
	// ButtonSelectEndPTS is the final button-selection timestamp.
	ButtonSelectEndPTS uint32
	// ButtonGroupCount is the authored display-group count.
	ButtonGroupCount uint8
	// ButtonOffset is the authored button table offset selector.
	ButtonOffset uint8
	// ButtonCount is the authored button entry count.
	ButtonCount uint8
	// SelectableCount is the number of authored selectable buttons.
	SelectableCount uint8
	// ForcedSelectButton is the authored default selected button number.
	ForcedSelectButton uint8
	// ForcedActionButton is the authored automatically activated button number.
	ForcedActionButton uint8
	// ButtonColors contains normal and selected palette words for each display group.
	ButtonColors [3][2]uint32
	// ButtonGroups contains parsed buttons indexed by zero-based display group.
	ButtonGroups [MaxButtonGroups][]Button
}

// Button is one live PCI button entry for a display group.
type Button struct {
	// Number is the one-based authored button number.
	Number uint8
	// ColorGroup selects one of PCI.ButtonColors' display groups.
	ColorGroup uint8
	// XStart is the inclusive left edge in DVD menu pixels.
	XStart uint16
	// YStart is the inclusive top edge in DVD menu pixels.
	YStart uint16
	// XEnd is the inclusive right edge in DVD menu pixels.
	XEnd uint16
	// YEnd is the inclusive bottom edge in DVD menu pixels.
	YEnd uint16
	// AutoAction reports whether selecting the button activates it immediately.
	AutoAction bool
	// Up is the neighboring button number for upward navigation.
	Up uint8
	// Down is the neighboring button number for downward navigation.
	Down uint8
	// Left is the neighboring button number for left navigation.
	Left uint8
	// Right is the neighboring button number for right navigation.
	Right uint8
	// Command is the VM instruction executed when the button activates.
	Command vm.Command
}

// DSI contains the NAV fields used for VOBU/cell timing and discontinuities.
type DSI struct {
	// SystemClock is the raw DSI system clock reference.
	SystemClock uint32
	// LogicalBlock is the DSI navigation logical-block number.
	LogicalBlock uint32
	// VOBUEndAddress is the relative end address of the current VOBU.
	VOBUEndAddress uint32
	// FirstReference is the first authored video reference address.
	FirstReference uint32
	// SecondReference is the second authored video reference address.
	SecondReference uint32
	// ThirdReference is the third authored video reference address.
	ThirdReference uint32
	// VOBID is the current VOB identifier.
	VOBID uint16
	// CellID is the current cell identifier.
	CellID uint8
	// CellElapsedTime is the raw four-byte DVD elapsed-time value.
	CellElapsedTime [4]uint8
}

// Packet is one NAV sector's parsed PCI and DSI pair.
type Packet struct {
	// PCI is the sector's presentation-control packet.
	PCI PCI
	// DSI is the sector's data-search packet.
	DSI DSI
}

// SPUFragment is one bounded private-stream-1 DVD subpicture fragment.
type SPUFragment struct {
	// StreamID is the DVD subpicture substream identifier from 0x20 through 0x3f.
	StreamID uint8
	// Data is an owned fragment of the subpicture payload.
	Data []byte
	// PTS is the decoded MPEG presentation timestamp when HasPTS is true.
	PTS uint64
	// HasPTS reports whether the PES header supplied a presentation timestamp.
	HasPTS bool
}

// CellScanOptions bound independent sector and subpicture work while reading
// one IFO-selected menu cell.
type CellScanOptions struct {
	// MaxSectors bounds sectors read from the cell; zero uses the package default.
	MaxSectors int
	// MaxSPUPackets bounds routed subpicture fragments; zero uses the package default.
	MaxSPUPackets int
	// MaxSPUBytes bounds aggregate subpicture payload bytes; zero uses the package default.
	MaxSPUBytes int
}

// CellScan is the parsed NAV and subpicture state found in one bounded cell
// prefix. Truncated reports that MaxSectors stopped the scan before LastSector.
type CellScan struct {
	// Packets contains every valid NAV sector found in the scanned prefix.
	Packets []Packet
	// SPUFragments contains bounded, owned subpicture fragments from that prefix.
	SPUFragments []SPUFragment
	// ScannedSectors is the number of whole sectors read.
	ScannedSectors int
	// Truncated reports that MaxSectors stopped the scan before lastSector.
	Truncated bool
}

// ScanCell reads whole DVD sectors from one IFO-validated cell range. Sector
// addresses are relative to the already-selected VMG or VTS menu VOB.
func ScanCell(reader io.ReaderAt, size int64, firstSector, lastSector uint32, opts CellScanOptions) (CellScan, error) {
	if reader == nil || size < 0 || firstSector > lastSector {
		return CellScan{}, fmt.Errorf("%w: invalid cell source or sector bounds", ErrInvalidNAV)
	}
	maxSectors := opts.MaxSectors
	if maxSectors == 0 {
		maxSectors = defaultSectorLimit
	}
	packetLimit := opts.MaxSPUPackets
	if packetLimit == 0 {
		packetLimit = defaultPacketLimit
	}
	payloadLimit := opts.MaxSPUBytes
	if payloadLimit == 0 {
		payloadLimit = defaultPayloadLimit
	}
	if maxSectors < 0 || packetLimit < 0 || payloadLimit < 0 {
		return CellScan{}, fmt.Errorf("%w: negative cell scan limit", ErrInvalidNAV)
	}

	sectorCount := uint64(lastSector) - uint64(firstSector) + 1
	result := CellScan{}
	if sectorCount > uint64(maxSectors) {
		sectorCount = uint64(maxSectors)
		result.Truncated = true
	}
	endByte := (uint64(firstSector) + sectorCount) * SectorSize
	if endByte > uint64(size) {
		return CellScan{}, fmt.Errorf("%w: cell sector outside menu VOB", ErrInvalidNAV)
	}

	sector := make([]byte, SectorSize)
	spuBytes := 0
	for index := uint64(0); index < sectorCount; index++ {
		offset := (uint64(firstSector) + index) * SectorSize
		if offset > math.MaxInt64 {
			return CellScan{}, fmt.Errorf("%w: cell byte offset overflow", ErrInvalidNAV)
		}
		// DVD sector offsets fit int64 after the file-size bound above.
		read, err := reader.ReadAt(sector, int64(offset))
		if err != nil && !errors.Is(err, io.EOF) {
			return CellScan{}, fmt.Errorf("read DVD menu sector: %w", err)
		}
		if read != SectorSize {
			return CellScan{}, fmt.Errorf("%w: truncated menu sector", ErrInvalidNAV)
		}
		result.ScannedSectors++

		if looksLikeNAVSector(sector) {
			packet, parseErr := ParseNAVSector(sector)
			if parseErr != nil {
				return CellScan{}, parseErr
			}
			result.Packets = append(result.Packets, packet)
		}
		fragments, routeErr := RouteSPUFragments(sector, packetLimit, payloadLimit)
		if routeErr != nil {
			return CellScan{}, routeErr
		}
		if len(result.SPUFragments)+len(fragments) > packetLimit {
			return CellScan{}, fmt.Errorf("%w: SPU packet limit", ErrInvalidNAV)
		}
		for _, fragment := range fragments {
			spuBytes += len(fragment.Data)
			if spuBytes > payloadLimit {
				return CellScan{}, fmt.Errorf("%w: SPU payload limit", ErrInvalidNAV)
			}
			result.SPUFragments = append(result.SPUFragments, fragment)
		}
	}
	return result, nil
}

// ParsePCI decodes a PCI payload beginning immediately after substream id 0.
func ParsePCI(data []byte) (PCI, error) {
	if len(data) < PCIPayloadSize {
		return PCI{}, fmt.Errorf("%w: PCI payload length %d", ErrInvalidNAV, len(data))
	}
	reader := bitReader{data: data[:PCIPayloadSize]}
	var pci PCI
	var err error
	if pci.LogicalBlock, err = reader.u32(); err != nil {
		return PCI{}, err
	}
	if pci.VOBUCategory, err = reader.u16(); err != nil {
		return PCI{}, err
	}
	if err = reader.skip(16 + 32); err != nil { // reserved + prohibited user operations
		return PCI{}, err
	}
	if pci.VOBUStartPTS, err = reader.u32(); err != nil {
		return PCI{}, err
	}
	if pci.VOBUEndPTS, err = reader.u32(); err != nil {
		return PCI{}, err
	}
	if pci.VOBUSequenceEndPTS, err = reader.u32(); err != nil {
		return PCI{}, err
	}
	if err = reader.skip(32 + 32*8 + 9*32); err != nil { // elapsed, ISRC, angle destinations
		return PCI{}, err
	}
	if pci.HighlightStatus, err = reader.u16(); err != nil {
		return PCI{}, err
	}
	if pci.HighlightStartPTS, err = reader.u32(); err != nil {
		return PCI{}, err
	}
	if pci.HighlightEndPTS, err = reader.u32(); err != nil {
		return PCI{}, err
	}
	if pci.ButtonSelectEndPTS, err = reader.u32(); err != nil {
		return PCI{}, err
	}
	if err = reader.skip(2); err != nil {
		return PCI{}, err
	}
	groups, err := reader.u8(2)
	if err != nil {
		return PCI{}, err
	}
	pci.ButtonGroupCount = groups
	if err = reader.skip(12); err != nil { // reserved and group display types
		return PCI{}, err
	}
	values := []*uint8{
		&pci.ButtonOffset,
		&pci.ButtonCount,
		&pci.SelectableCount,
	}
	for _, target := range values {
		value, readErr := reader.u8(8)
		if readErr != nil {
			return PCI{}, readErr
		}
		*target = value
	}
	if err = reader.skip(8); err != nil {
		return PCI{}, err
	}
	for _, target := range []*uint8{&pci.ForcedSelectButton, &pci.ForcedActionButton} {
		value, readErr := reader.u8(8)
		if readErr != nil {
			return PCI{}, readErr
		}
		*target = value
	}
	for group := range pci.ButtonColors {
		for mode := range pci.ButtonColors[group] {
			value, readErr := reader.u32()
			if readErr != nil {
				return PCI{}, readErr
			}
			pci.ButtonColors[group][mode] = value
		}
	}
	if err := validateHeader(pci); err != nil {
		return PCI{}, err
	}

	perGroup := MaxButtons
	if pci.ButtonGroupCount != 0 {
		perGroup = MaxButtons / int(pci.ButtonGroupCount)
	}
	for index := range MaxButtons {
		button, readErr := readButton(&reader)
		if readErr != nil {
			return PCI{}, readErr
		}
		if pci.ButtonGroupCount == 0 {
			continue
		}
		group := index / perGroup
		position := index % perGroup
		if group >= int(pci.ButtonGroupCount) || position >= int(pci.ButtonCount) {
			continue
		}
		//nolint:gosec // position is bounded by the 36-entry DVD button table.
		button.Number = uint8(position + 1)
		if err := validateButton(button, pci.ButtonCount); err != nil {
			return PCI{}, fmt.Errorf("button group %d item %d: %w", group+1, position+1, err)
		}
		pci.ButtonGroups[group] = append(pci.ButtonGroups[group], button)
	}
	return pci, nil
}

func validateHeader(pci PCI) error {
	if pci.ButtonGroupCount > MaxButtonGroups {
		return fmt.Errorf("%w: button group count %d", ErrInvalidNAV, pci.ButtonGroupCount)
	}
	if pci.ButtonCount > MaxButtons || pci.SelectableCount > pci.ButtonCount {
		return fmt.Errorf("%w: button counts %d/%d", ErrInvalidNAV, pci.ButtonCount, pci.SelectableCount)
	}
	if pci.ButtonGroupCount == 0 {
		if pci.ButtonCount != 0 {
			return fmt.Errorf("%w: buttons without display group", ErrInvalidNAV)
		}
		return nil
	}
	if int(pci.ButtonCount) > MaxButtons/int(pci.ButtonGroupCount) {
		return fmt.Errorf("%w: %d buttons in %d groups", ErrInvalidNAV, pci.ButtonCount, pci.ButtonGroupCount)
	}
	for _, number := range []uint8{pci.ForcedSelectButton & 0x3f, pci.ForcedActionButton & 0x3f} {
		if number > pci.ButtonCount {
			return fmt.Errorf("%w: forced button %d", ErrInvalidNAV, number)
		}
	}
	return nil
}

func readButton(reader *bitReader) (Button, error) {
	var button Button
	color, err := reader.u8(2)
	if err != nil {
		return Button{}, err
	}
	button.ColorGroup = color
	xStart, err := reader.u16Bits(10)
	if err != nil {
		return Button{}, err
	}
	button.XStart = xStart
	if err := reader.skip(2); err != nil {
		return Button{}, err
	}
	xEnd, err := reader.u16Bits(10)
	if err != nil {
		return Button{}, err
	}
	button.XEnd = xEnd
	autoAction, err := reader.u8(2)
	if err != nil {
		return Button{}, err
	}
	button.AutoAction = autoAction != 0
	yStart, err := reader.u16Bits(10)
	if err != nil {
		return Button{}, err
	}
	button.YStart = yStart
	if err := reader.skip(2); err != nil {
		return Button{}, err
	}
	yEnd, err := reader.u16Bits(10)
	if err != nil {
		return Button{}, err
	}
	button.YEnd = yEnd
	for _, target := range []*uint8{&button.Up, &button.Down, &button.Left, &button.Right} {
		if err := reader.skip(2); err != nil {
			return Button{}, err
		}
		value, readErr := reader.u8(6)
		if readErr != nil {
			return Button{}, readErr
		}
		*target = value
	}
	for index := range button.Command {
		value, readErr := reader.u8(8)
		if readErr != nil {
			return Button{}, readErr
		}
		button.Command[index] = value
	}
	return button, nil
}

func validateButton(button Button, count uint8) error {
	if button.ColorGroup > 3 {
		return fmt.Errorf("%w: color group %d", ErrInvalidNAV, button.ColorGroup)
	}
	if button.XStart > button.XEnd || button.YStart > button.YEnd {
		return fmt.Errorf("%w: inverted rectangle", ErrInvalidNAV)
	}
	for _, neighbor := range []uint8{button.Up, button.Down, button.Left, button.Right} {
		if neighbor > count {
			return fmt.Errorf("%w: neighbor %d exceeds count %d", ErrInvalidNAV, neighbor, count)
		}
	}
	return nil
}

// ParseDSI decodes the DSI general-information prefix.
func ParseDSI(data []byte) (DSI, error) {
	if len(data) < minimumDSIPayloadSize {
		return DSI{}, fmt.Errorf("%w: DSI payload length %d", ErrInvalidNAV, len(data))
	}
	return DSI{
		SystemClock:     binary.BigEndian.Uint32(data[0:4]),
		LogicalBlock:    binary.BigEndian.Uint32(data[4:8]),
		VOBUEndAddress:  binary.BigEndian.Uint32(data[8:12]),
		FirstReference:  binary.BigEndian.Uint32(data[12:16]),
		SecondReference: binary.BigEndian.Uint32(data[16:20]),
		ThirdReference:  binary.BigEndian.Uint32(data[20:24]),
		VOBID:           binary.BigEndian.Uint16(data[24:26]),
		CellID:          data[27],
		CellElapsedTime: [4]uint8(data[28:32]),
	}, nil
}

// ParseNAVSector decodes the paired private-stream-2 PCI and DSI PES packets
// in one 2048-byte NAV sector.
func ParseNAVSector(sector []byte) (Packet, error) {
	if len(sector) != SectorSize || !bytes.Equal(sector[:4], []byte{0, 0, 1, 0xba}) {
		return Packet{}, fmt.Errorf("%w: NAV sector pack header", ErrInvalidNAV)
	}
	offset, err := packEnd(sector, 0)
	if err != nil {
		return Packet{}, err
	}
	if offset+6 <= len(sector) && bytes.Equal(sector[offset:offset+4], []byte{0, 0, 1, 0xbb}) {
		length := int(binary.BigEndian.Uint16(sector[offset+4 : offset+6]))
		offset += 6 + length
	}
	pciPayload, next, err := privateStream2(sector, offset, 0)
	if err != nil {
		return Packet{}, err
	}
	dsiPayload, _, err := privateStream2(sector, next, 1)
	if err != nil {
		return Packet{}, err
	}
	pci, err := ParsePCI(pciPayload)
	if err != nil {
		return Packet{}, err
	}
	dsi, err := ParseDSI(dsiPayload)
	if err != nil {
		return Packet{}, err
	}
	return Packet{PCI: pci, DSI: dsi}, nil
}

func looksLikeNAVSector(sector []byte) bool {
	if len(sector) != SectorSize || !bytes.Equal(sector[:4], []byte{0, 0, 1, 0xba}) {
		return false
	}
	offset, err := packEnd(sector, 0)
	if err != nil {
		return false
	}
	if offset+6 <= len(sector) && bytes.Equal(sector[offset:offset+4], []byte{0, 0, 1, 0xbb}) {
		length := int(binary.BigEndian.Uint16(sector[offset+4 : offset+6]))
		offset += 6 + length
	}
	return offset+7 <= len(sector) && bytes.Equal(sector[offset:offset+4], []byte{0, 0, 1, 0xbf}) && sector[offset+6] == 0
}

func privateStream2(data []byte, offset int, substream uint8) ([]byte, int, error) {
	if offset < 0 || offset+7 > len(data) || !bytes.Equal(data[offset:offset+4], []byte{0, 0, 1, 0xbf}) {
		return nil, offset, fmt.Errorf("%w: private-stream-2 header", ErrInvalidNAV)
	}
	length := int(binary.BigEndian.Uint16(data[offset+4 : offset+6]))
	end := offset + 6 + length
	if length < 1 || end > len(data) || data[offset+6] != substream {
		return nil, offset, fmt.Errorf("%w: private-stream-2 substream %d", ErrInvalidNAV, substream)
	}
	return data[offset+7 : end], end, nil
}

func packEnd(data []byte, offset int) (int, error) {
	if offset+14 > len(data) {
		return 0, fmt.Errorf("%w: truncated pack header", ErrInvalidNAV)
	}
	if data[offset+4]&0x40 == 0 { // MPEG-1 pack header
		if offset+12 > len(data) {
			return 0, fmt.Errorf("%w: truncated MPEG-1 pack", ErrInvalidNAV)
		}
		return offset + 12, nil
	}
	end := offset + 14 + int(data[offset+13]&0x07)
	if end > len(data) {
		return 0, fmt.Errorf("%w: pack stuffing", ErrInvalidNAV)
	}
	return end, nil
}

// RouteSPUFragments returns DVD subpicture payload fragments from MPEG-2
// private-stream-1 PES packets. Limits are independent of input length.
func RouteSPUFragments(data []byte, packetLimit, payloadLimit int) ([]SPUFragment, error) {
	if packetLimit == 0 {
		packetLimit = defaultPacketLimit
	}
	if payloadLimit == 0 {
		payloadLimit = defaultPayloadLimit
	}
	if packetLimit < 0 || payloadLimit < 0 {
		return nil, fmt.Errorf("%w: negative routing limit", ErrInvalidNAV)
	}
	fragments := make([]SPUFragment, 0)
	total := 0
	for offset := 0; offset+6 <= len(data); {
		index := bytes.Index(data[offset:], []byte{0, 0, 1, 0xbd})
		if index < 0 {
			break
		}
		offset += index
		length := int(binary.BigEndian.Uint16(data[offset+4 : offset+6]))
		end := offset + 6 + length
		if end > len(data) {
			return nil, fmt.Errorf("%w: truncated PES payload", ErrInvalidNAV)
		}
		if length < 4 || offset+9 > end || data[offset+6]&0xc0 != 0x80 {
			return nil, fmt.Errorf("%w: private-stream-1 header", ErrInvalidNAV)
		}
		headerLength := int(data[offset+8])
		payload := offset + 9 + headerLength
		if payload >= end {
			return nil, fmt.Errorf("%w: private-stream-1 payload", ErrInvalidNAV)
		}
		substream := data[payload]
		if substream >= 0x20 && substream <= 0x3f {
			if len(fragments) >= packetLimit {
				return nil, fmt.Errorf("%w: SPU packet limit", ErrInvalidNAV)
			}
			fragment := data[payload+1 : end]
			total += len(fragment)
			if total > payloadLimit {
				return nil, fmt.Errorf("%w: SPU payload limit", ErrInvalidNAV)
			}
			item := SPUFragment{StreamID: substream, Data: append([]byte(nil), fragment...)}
			if data[offset+7]&0x80 != 0 {
				if headerLength < 5 {
					return nil, fmt.Errorf("%w: truncated private-stream-1 PTS", ErrInvalidNAV)
				}
				item.PTS = decodePTS(data[offset+9 : offset+14])
				item.HasPTS = true
			}
			fragments = append(fragments, item)
		}
		offset = end
	}
	return fragments, nil
}

func decodePTS(data []byte) uint64 {
	return uint64(data[0]&0x0e)<<29 |
		uint64(binary.BigEndian.Uint16(data[1:3])>>1)<<15 |
		uint64(binary.BigEndian.Uint16(data[3:5])>>1)
}

type bitReader struct {
	data []byte
	bit  int
}

func (r *bitReader) read(count int) (uint64, error) {
	if count < 0 || count > 64 || r.bit+count > len(r.data)*8 {
		return 0, fmt.Errorf("%w: bitstream bounds offset=%d count=%d size=%d", ErrInvalidNAV, r.bit, count, len(r.data)*8)
	}
	var value uint64
	for range count {
		value = value<<1 | uint64(r.data[r.bit/8]>>(7-(r.bit%8))&1)
		r.bit++
	}
	return value, nil
}

func (r *bitReader) skip(count int) error {
	if count < 0 || r.bit+count > len(r.data)*8 {
		return fmt.Errorf("%w: bitstream bounds offset=%d count=%d size=%d", ErrInvalidNAV, r.bit, count, len(r.data)*8)
	}
	r.bit += count
	return nil
}

func (r *bitReader) u16() (uint16, error) {
	return r.u16Bits(16)
}

func (r *bitReader) u32() (uint32, error) {
	value, err := r.read(32)
	if err != nil {
		return 0, err
	}
	if value > 0xffff_ffff {
		return 0, fmt.Errorf("%w: value exceeds 32 bits", ErrInvalidNAV)
	}
	return uint32(value), nil
}

func (r *bitReader) u8(count int) (uint8, error) {
	value, err := r.read(count)
	if err != nil {
		return 0, err
	}
	if value > 0xff {
		return 0, fmt.Errorf("%w: value exceeds 8 bits", ErrInvalidNAV)
	}
	return uint8(value), nil
}

func (r *bitReader) u16Bits(count int) (uint16, error) {
	value, err := r.read(count)
	if err != nil {
		return 0, err
	}
	if value > 0xffff {
		return 0, fmt.Errorf("%w: value exceeds 16 bits", ErrInvalidNAV)
	}
	return uint16(value), nil
}
