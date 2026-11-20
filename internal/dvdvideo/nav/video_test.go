package nav

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestVideoPESPreservesOriginal33BitClock(t *testing.T) {
	pts := uint64(1<<32) + 90003
	data := []byte{0, 0, 1, 0xe0, 0, 11, 0x80, 0x80, 5, 0, 0, 0, 0, 0, 0xaa, 0xbb, 0xcc}
	stamp := data[9:14]
	stamp[0] = 0x21 | byte((pts>>29)&14)
	stamp[1] = byte(pts >> 22)
	stamp[2] = byte((pts>>14)&254) | 1
	stamp[3] = byte(pts >> 7)
	stamp[4] = byte((pts<<1)&254) | 1
	packets, err := ParseVideoPES(data)
	if err != nil || len(packets) != 1 || !packets[0].HasPTS || packets[0].PTS != pts || packets[0].StreamID != 0xe0 || len(packets[0].Data) != 3 {
		t.Fatalf("packets=%+v err=%v", packets, err)
	}
	data[11] &= 0xfe
	if _, err := ParseVideoPES(data); !errors.Is(err, ErrInvalidNAV) {
		t.Fatal("invalid clock markers accepted")
	}
	binary.BigEndian.PutUint16(data[4:6], 65535)
	if _, err := ParseVideoPES(data); !errors.Is(err, ErrInvalidNAV) {
		t.Fatal("truncated first PES accepted")
	}
}
