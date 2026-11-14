package filesystem

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
)

func TestComparisonCPIReconstructsCoarseFinePacketDomains(t *testing.T) {
	data := make([]byte, 84)
	copy(data, "HDMV0200")
	binary.BigEndian.PutUint32(data[16:], 32)
	binary.BigEndian.PutUint32(data[32:], 48)
	section := data[36:]
	section[1], section[3] = 1, 1
	binary.BigEndian.PutUint64(section[4:], uint64(0x1011)<<48|1<<34|2<<18|3)
	binary.BigEndian.PutUint32(section[12:], 14)
	binary.BigEndian.PutUint32(section[16:], 20)
	binary.BigEndian.PutUint64(section[20:], uint64(4)<<32|10)
	binary.BigEndian.PutUint64(section[28:], uint64(2)<<46|uint64(6)<<32|0x20007)
	binary.BigEndian.PutUint32(section[36:], 1<<17|16)
	binary.BigEndian.PutUint32(section[40:], 4<<17|200)
	binary.BigEndian.PutUint32(section[44:], 1<<17|17)
	entries, err := parseCLPICPI(data, 0x1011)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	for i, want := range []CLPIEntry{{PTS45: 1048832, Packet: 16}, {PTS45: 1049600, Packet: 200}, {PTS45: 1573120, Packet: 131089}} {
		if entries[i] != want {
			t.Fatalf("entry=%+v want=%+v", entries[i], want)
		}
	}
	for _, broken := range [][]byte{data[:83], bytes.Replace(data, []byte("HDMV"), []byte("MPLS"), 1)} {
		if _, err := parseCLPICPI(broken, 0x1011); !errors.Is(err, ErrComparisonIndex) {
			t.Fatalf("malformed CPI accepted: %v", err)
		}
	}
	if _, err := parseCLPICPI(data, 0x1012); !errors.Is(err, ErrComparisonIndex) {
		t.Fatal("another PID supplied primary random-access authority")
	}
	corrupt := bytes.Clone(data)
	binary.BigEndian.PutUint32(corrupt[52:], 0)
	if _, err := parseCLPICPI(corrupt, 0x1011); !errors.Is(err, ErrComparisonIndex) {
		t.Fatal("overlapping coarse/fine tables accepted")
	}
}

func TestDVDLogicalReaderPreservesPhysicalVOBBoundaries(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string][]byte{
		"VTS_01_0.VOB": []byte("menu"),
		"VTS_01_1.VOB": bytes.Repeat([]byte{'A'}, 4096),
		"VTS_01_2.VOB": bytes.Repeat([]byte{'B'}, 4096),
	} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := OpenDVDTitle(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var crossing [4]byte
	if n, err := reader.ReadAt(crossing[:], 4094); err != nil || n != 4 || string(crossing[:]) != "AABB" {
		t.Fatalf("split read=%q count=%d err=%v", crossing, n, err)
	}
	spans, err := reader.spans(1, 3)
	if err != nil || len(spans) != 2 || spans[0].Start != 2048 || spans[0].End != 4096 || spans[1].Start != 0 || spans[1].End != 2048 {
		t.Fatalf("logical spans=%+v err=%v", spans, err)
	}
	if err := os.WriteFile(filepath.Join(root, "VTS_01_4.VOB"), bytes.Repeat([]byte{'D'}, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	if extra, err := OpenDVDTitle(root, 1); err == nil {
		_ = extra.Close()
		t.Fatal("gapped title VOB sequence accepted")
	}
}

func TestLocalDVDComparisonWindows(t *testing.T) {
	root := os.Getenv("UPBRR_COMPARISON_DVD")
	if root == "" {
		t.Skip("opt-in original-clock DVD acceptance")
	}
	disc, err := ifo.InspectFeatures(root)
	if err != nil {
		t.Fatal(err)
	}
	title := disc.Titles[0]
	reader, err := OpenDVDTitle(root, title.VTS)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, second := range []int64{20, 600, 1028, 2500, 5000} {
		windows, err := ResolveDVDWindows(reader, title, second*90000, (second+64)*90000)
		if err != nil {
			t.Fatalf("window %d: %v", second, err)
		}
		for _, window := range windows {
			t.Logf("second=%d cell=%d title_offset=%d origin=%d start=%d stop=%d nav_probes=%d", second, window.Cell, window.TitleOffset90, window.SourceOrigin90, window.Start90, window.End90, window.NAVProbes)
			for _, span := range window.Spans {
				t.Logf("part=%s bytes=[%d,%d)", filepath.Base(span.Path), span.Start, span.End)
			}
		}
	}
}
