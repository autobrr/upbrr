// Package spu reassembles, decodes, and composites DVD subpicture packets.
//
// The control and RLE behavior is derived from FFmpeg dvdsubdec.c commit
// 40ce1513c6a883bc2b96d33d968013b89dfe76af (LGPL-2.1-or-later). The DVD
// palette/highlight layout also follows VideoLAN libdvdread 7.0.1 nav_types.h
// (GPL-2.0-or-later). See plans/dvd-menu-engine-proof-adr.md.
package spu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
)

const (
	// MaxPacketBytes is the largest accepted declared subpicture packet size.
	MaxPacketBytes = 65_535
	// MaxControlBlocks bounds control-sequence traversal.
	MaxControlBlocks = 128
	// MaxOverlayWidth is the largest accepted decoded overlay width.
	MaxOverlayWidth = 1024
	// MaxOverlayHeight is the largest accepted decoded overlay height.
	MaxOverlayHeight = 1024
	// MaxOverlayPixels bounds decoded overlay allocation.
	MaxOverlayPixels = 1 << 20
)

// ErrInvalidSPU identifies malformed, incomplete, or oversized subpicture data.
var ErrInvalidSPU = errors.New("invalid DVD subpicture")

// Packet is one complete DVD subpicture unit.
type Packet struct {
	// StreamID is the DVD subpicture substream identifier.
	StreamID uint8
	// Data contains one complete owned subpicture packet.
	Data []byte
}

// Reassembler joins bounded private-stream fragments by subpicture stream id.
type Reassembler struct {
	streams [32][]byte
}

// Push appends one fragment and returns any completed packets. It accepts only
// DVD subpicture stream IDs and retains incomplete data for later calls.
func (r *Reassembler) Push(streamID uint8, fragment []byte) ([]Packet, error) {
	if streamID < 0x20 || streamID > 0x3f {
		return nil, fmt.Errorf("%w: stream id 0x%02x", ErrInvalidSPU, streamID)
	}
	index := streamID - 0x20
	if len(r.streams[index])+len(fragment) > MaxPacketBytes*2 {
		return nil, fmt.Errorf("%w: reassembly limit", ErrInvalidSPU)
	}
	r.streams[index] = append(r.streams[index], fragment...)
	var packets []Packet
	for len(r.streams[index]) >= 2 {
		size := int(binary.BigEndian.Uint16(r.streams[index][:2]))
		if size < 4 || size > MaxPacketBytes {
			return nil, fmt.Errorf("%w: packet size %d", ErrInvalidSPU, size)
		}
		if len(r.streams[index]) < size {
			break
		}
		data := append([]byte(nil), r.streams[index][:size]...)
		packets = append(packets, Packet{StreamID: streamID, Data: data})
		r.streams[index] = append(r.streams[index][:0], r.streams[index][size:]...)
	}
	return packets, nil
}

// Overlay is decoded palette-indexed subpicture state at one control date.
type Overlay struct {
	// Bounds is the half-open destination rectangle in DVD menu pixel coordinates.
	Bounds image.Rectangle
	// Pixels contains four-entry palette indexes in row-major order.
	Pixels []uint8
	// Stride is the number of palette indexes per row.
	Stride int
	// Palette contains the decoded NRGBA colors for Pixels.
	Palette [4]color.NRGBA
	// ColorMap contains authored indexes into the PGC color lookup table.
	ColorMap [4]uint8
	// Alpha contains authored four-bit opacity values.
	Alpha [4]uint8
	// StartDate is the selected display-control start date.
	StartDate uint16
	// EndDate is the selected display-control end date.
	EndDate uint16
	// Menu reports that the packet contains authored menu subpicture state.
	Menu bool
}

// Highlight overrides palette entries inside one selected-button rectangle.
type Highlight struct {
	// Bounds is the half-open selected-button rectangle.
	Bounds image.Rectangle
	// Palette is the authored button color/alpha word.
	Palette uint32
}

type controlState struct {
	menu       bool
	started    bool
	startDate  uint16
	endDate    uint16
	colorMap   [4]uint8
	alpha      [4]uint8
	x1, y1     int
	x2, y2     int
	offsetEven int
	offsetOdd  int
}

// Decode decodes state active at or immediately before date. Use ^uint16(0)
// to select the final authored control state.
func Decode(packet []byte, clut [16]uint32, date uint16) (Overlay, error) {
	if len(packet) < 10 {
		return Overlay{}, fmt.Errorf("%w: packet length %d", ErrInvalidSPU, len(packet))
	}
	size := int(binary.BigEndian.Uint16(packet[:2]))
	if size < 4 || size > len(packet) || size > MaxPacketBytes {
		return Overlay{}, fmt.Errorf("%w: declared size %d", ErrInvalidSPU, size)
	}
	packet = packet[:size]
	commandOffset := int(binary.BigEndian.Uint16(packet[2:4]))
	if commandOffset < 4 || commandOffset+4 > len(packet) {
		return Overlay{}, fmt.Errorf("%w: command offset %d", ErrInvalidSPU, commandOffset)
	}
	state := controlState{offsetEven: -1, offsetOdd: -1}
	position := commandOffset
	seen := make(map[int]struct{})
	for range MaxControlBlocks {
		if _, ok := seen[position]; ok {
			break
		}
		seen[position] = struct{}{}
		if position+4 > len(packet) {
			return Overlay{}, fmt.Errorf("%w: control block", ErrInvalidSPU)
		}
		blockDate := binary.BigEndian.Uint16(packet[position : position+2])
		next := int(binary.BigEndian.Uint16(packet[position+2 : position+4]))
		if blockDate > date {
			break
		}
		var err error
		state, err = parseCommands(packet, position+4, blockDate, state)
		if err != nil {
			return Overlay{}, err
		}
		if next == position {
			break
		}
		if next < commandOffset || next <= position || next+4 > len(packet) {
			return Overlay{}, fmt.Errorf("%w: next control offset %d", ErrInvalidSPU, next)
		}
		position = next
	}
	if state.offsetEven < 0 || state.offsetOdd < 0 || state.x2 < state.x1 || state.y2 < state.y1 {
		return Overlay{}, fmt.Errorf("%w: incomplete display state", ErrInvalidSPU)
	}
	width, height := state.x2-state.x1+1, state.y2-state.y1+1
	if width <= 0 || height <= 0 || width > MaxOverlayWidth || height > MaxOverlayHeight || width*height > MaxOverlayPixels {
		return Overlay{}, fmt.Errorf("%w: overlay dimensions %dx%d", ErrInvalidSPU, width, height)
	}
	pixels := make([]uint8, width*height)
	if err := decodeField(packet, state.offsetEven, width, height, 0, pixels); err != nil {
		return Overlay{}, err
	}
	if height > 1 {
		if err := decodeField(packet, state.offsetOdd, width, height, 1, pixels); err != nil {
			return Overlay{}, err
		}
	}
	overlay := Overlay{
		Bounds:    image.Rect(state.x1, state.y1, state.x2+1, state.y2+1),
		Pixels:    pixels,
		Stride:    width,
		ColorMap:  state.colorMap,
		Alpha:     state.alpha,
		StartDate: state.startDate,
		EndDate:   state.endDate,
		Menu:      state.menu,
	}
	for index := range overlay.Palette {
		overlay.Palette[index] = clutColor(clut[state.colorMap[index]], state.alpha[index]*17)
	}
	return overlay, nil
}

func parseCommands(packet []byte, position int, date uint16, state controlState) (controlState, error) {
	for position < len(packet) {
		command := packet[position]
		position++
		switch command {
		case 0x00:
			state.menu = true
		case 0x01:
			state.started = true
			state.startDate = date
		case 0x02:
			state.endDate = date
		case 0x03:
			if position+2 > len(packet) {
				return state, fmt.Errorf("%w: color map", ErrInvalidSPU)
			}
			state.colorMap[3] = packet[position] >> 4
			state.colorMap[2] = packet[position] & 0x0f
			state.colorMap[1] = packet[position+1] >> 4
			state.colorMap[0] = packet[position+1] & 0x0f
			position += 2
		case 0x04:
			if position+2 > len(packet) {
				return state, fmt.Errorf("%w: alpha map", ErrInvalidSPU)
			}
			state.alpha[3] = packet[position] >> 4
			state.alpha[2] = packet[position] & 0x0f
			state.alpha[1] = packet[position+1] >> 4
			state.alpha[0] = packet[position+1] & 0x0f
			position += 2
		case 0x05:
			if position+6 > len(packet) {
				return state, fmt.Errorf("%w: display area", ErrInvalidSPU)
			}
			state.x1 = int(packet[position])<<4 | int(packet[position+1]>>4)
			state.x2 = int(packet[position+1]&0x0f)<<8 | int(packet[position+2])
			state.y1 = int(packet[position+3])<<4 | int(packet[position+4]>>4)
			state.y2 = int(packet[position+4]&0x0f)<<8 | int(packet[position+5])
			position += 6
		case 0x06:
			if position+4 > len(packet) {
				return state, fmt.Errorf("%w: RLE offsets", ErrInvalidSPU)
			}
			state.offsetEven = int(binary.BigEndian.Uint16(packet[position : position+2]))
			state.offsetOdd = int(binary.BigEndian.Uint16(packet[position+2 : position+4]))
			position += 4
		case 0xff:
			return state, nil
		default:
			return state, fmt.Errorf("%w: command 0x%02x", ErrInvalidSPU, command)
		}
	}
	return state, fmt.Errorf("%w: unterminated command sequence", ErrInvalidSPU)
}

func decodeField(packet []byte, offset, width, height, parity int, pixels []uint8) error {
	if offset < 4 || offset >= len(packet) {
		return fmt.Errorf("%w: RLE offset %d", ErrInvalidSPU, offset)
	}
	reader := nibbleReader{data: packet, nibble: offset * 2}
	for y := parity; y < height; y += 2 {
		for x := 0; x < width; {
			length, value, err := reader.run()
			if err != nil {
				return err
			}
			if length == 0 {
				length = width - x
			}
			if length > width-x {
				return fmt.Errorf("%w: RLE crosses scanline", ErrInvalidSPU)
			}
			for column := 0; column < length; column++ {
				pixels[y*width+x+column] = value
			}
			x += length
		}
		reader.alignByte()
	}
	return nil
}

type nibbleReader struct {
	data   []byte
	nibble int
}

func (r *nibbleReader) read() (uint16, error) {
	if r.nibble >= len(r.data)*2 {
		return 0, fmt.Errorf("%w: truncated RLE", ErrInvalidSPU)
	}
	value := r.data[r.nibble/2]
	if r.nibble%2 == 0 {
		value >>= 4
	} else {
		value &= 0x0f
	}
	r.nibble++
	return uint16(value), nil
}

func (r *nibbleReader) run() (int, uint8, error) {
	var value uint16
	for threshold := uint16(1); value < threshold && threshold <= 0x40; threshold <<= 2 {
		nibble, err := r.read()
		if err != nil {
			return 0, 0, err
		}
		value = value<<4 | nibble
	}
	colorIndex := uint8(value & 3)
	if value < 4 {
		return 0, colorIndex, nil
	}
	return int(value >> 2), colorIndex, nil
}

func (r *nibbleReader) alignByte() {
	if r.nibble%2 != 0 {
		r.nibble++
	}
}

func clutColor(value uint32, alpha uint8) color.NRGBA {
	y := boundedByte((value >> 16) & 0xff)
	cr := boundedByte((value >> 8) & 0xff)
	cb := boundedByte(value & 0xff)
	r, g, b := color.YCbCrToRGB(y, cb, cr)
	return color.NRGBA{R: r, G: g, B: b, A: alpha}
}

// Composite overlays subpicture pixels and an optional selected-button
// highlight onto a decoded background frame. It returns a new NRGBA image and
// does not mutate background.
func Composite(background image.Image, overlay Overlay, clut [16]uint32, highlight *Highlight) *image.NRGBA {
	bounds := background.Bounds()
	result := image.NewNRGBA(bounds)
	draw.Draw(result, bounds, background, bounds.Min, draw.Src)
	clip := overlay.Bounds.Intersect(bounds)
	for y := clip.Min.Y; y < clip.Max.Y; y++ {
		for x := clip.Min.X; x < clip.Max.X; x++ {
			index := overlay.Pixels[(y-overlay.Bounds.Min.Y)*overlay.Stride+x-overlay.Bounds.Min.X]
			pixel := overlay.Palette[index]
			if highlight != nil && image.Pt(x, y).In(highlight.Bounds) {
				colorIndex, alpha := highlightEntry(highlight.Palette, index)
				pixel = clutColor(clut[colorIndex], alpha*17)
			}
			blendNRGBA(result, x, y, pixel)
		}
	}
	return result
}

func highlightEntry(palette uint32, index uint8) (uint8, uint8) {
	shift := index * 4
	alpha := boundedByte((palette >> shift) & 0x0f)
	colorIndex := boundedByte((palette >> (16 + shift)) & 0x0f)
	return colorIndex, alpha
}

func blendNRGBA(target *image.NRGBA, x, y int, source color.NRGBA) {
	if source.A == 0 {
		return
	}
	if source.A == 0xff {
		target.SetNRGBA(x, y, source)
		return
	}
	destination := target.NRGBAAt(x, y)
	sa := uint32(source.A)
	da := uint32(destination.A) * (255 - sa) / 255
	outA := sa + da
	if outA == 0 {
		return
	}
	blend := func(src, dst uint8) uint8 {
		return boundedByte((uint32(src)*sa + uint32(dst)*da) / outA)
	}
	target.SetNRGBA(x, y, color.NRGBA{
		R: blend(source.R, destination.R),
		G: blend(source.G, destination.G),
		B: blend(source.B, destination.B),
		A: boundedByte(outA),
	})
}

func boundedByte(value uint32) uint8 {
	if value > 0xff {
		panic("DVD subpicture byte conversion exceeded 8 bits")
	}
	return uint8(value)
}
