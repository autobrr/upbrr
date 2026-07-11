package spu

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func TestDecodeTwoFieldRLE(t *testing.T) {
	packet := syntheticPacket()
	var clut [16]uint32
	clut[1] = 0x00808080
	clut[2] = 0x00c08080
	overlay, err := Decode(packet, clut, ^uint16(0))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := []uint8{1, 1, 2, 2}
	for index := range want {
		if overlay.Pixels[index] != want[index] {
			t.Fatalf("pixels = %v, want %v", overlay.Pixels, want)
		}
	}
	if !overlay.Menu || overlay.Bounds.Dx() != 2 || overlay.Bounds.Dy() != 2 {
		t.Fatalf("overlay = %+v", overlay)
	}
}

func TestReassemblerJoinsFragments(t *testing.T) {
	packet := syntheticPacket()
	var reassembler Reassembler
	complete, err := reassembler.Push(0x20, packet[:5])
	if err != nil || len(complete) != 0 {
		t.Fatalf("first Push = %v, %v", complete, err)
	}
	complete, err = reassembler.Push(0x20, packet[5:])
	if err != nil {
		t.Fatalf("second Push: %v", err)
	}
	if len(complete) != 1 || len(complete[0].Data) != len(packet) {
		t.Fatalf("complete = %+v", complete)
	}
}

func TestCompositeAppliesHighlightPalette(t *testing.T) {
	packet := syntheticPacket()
	var clut [16]uint32
	clut[1] = 0x00808080
	clut[2] = 0x00c08080
	clut[3] = 0x00ff8080
	overlay, err := Decode(packet, clut, ^uint16(0))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	background := color.NRGBA{A: 0xff}
	image := newSolid(2, 2, background)
	highlight := Highlight{Bounds: overlay.Bounds, Palette: 0x3333ffff}
	result := Composite(image, overlay, clut, &highlight)
	if result.NRGBAAt(0, 0) == overlay.Palette[1] {
		t.Fatal("highlight palette was not applied")
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(syntheticPacket())
	f.Add([]byte{0, 4, 0, 4})
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = Decode(data, [16]uint32{}, ^uint16(0))
	})
}

func syntheticPacket() []byte {
	packet := make([]byte, 31)
	binary.BigEndian.PutUint16(packet[0:2], uint16(len(packet)))
	binary.BigEndian.PutUint16(packet[2:4], 6)
	packet[4] = 0x90
	packet[5] = 0xa0
	binary.BigEndian.PutUint16(packet[8:10], 6)
	position := 10
	packet[position] = 0x00
	position++
	packet[position] = 0x01
	position++
	packet[position] = 0x03
	packet[position+1] = 0x32
	packet[position+2] = 0x10
	position += 3
	packet[position] = 0x04
	packet[position+1] = 0xff
	packet[position+2] = 0xff
	position += 3
	packet[position] = 0x05
	packet[position+1] = 0
	packet[position+2] = 0
	packet[position+3] = 1
	packet[position+4] = 0
	packet[position+5] = 0
	packet[position+6] = 1
	position += 7
	packet[position] = 0x06
	binary.BigEndian.PutUint16(packet[position+1:position+3], 4)
	binary.BigEndian.PutUint16(packet[position+3:position+5], 5)
	position += 5
	packet[position] = 0xff
	return packet
}

type solidImage struct {
	pixel color.NRGBA
	rect  imageRectangle
}

type imageRectangle struct{ maxX, maxY int }

func newSolid(width, height int, pixel color.NRGBA) solidImage {
	return solidImage{pixel: pixel, rect: imageRectangle{maxX: width, maxY: height}}
}

func (s solidImage) ColorModel() color.Model { return color.NRGBAModel }

func (s solidImage) Bounds() image.Rectangle { return image.Rect(0, 0, s.rect.maxX, s.rect.maxY) }

func (s solidImage) At(_, _ int) color.Color { return s.pixel }
