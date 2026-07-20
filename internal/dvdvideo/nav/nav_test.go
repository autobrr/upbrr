package nav

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestParsePCIButton(t *testing.T) {
	data := make([]byte, PCIPayloadSize)
	binary.BigEndian.PutUint32(data[12:16], 90_000)
	binary.BigEndian.PutUint32(data[16:20], 180_000)
	data[110] = 0x10 // one button group
	data[113] = 1
	data[114] = 1
	data[116] = 1
	data[117] = 1
	binary.BigEndian.PutUint32(data[118:122], 0x12345678)
	put24(data[142:145], 1<<22|10<<12|100)
	put24(data[145:148], 1<<22|20<<12|80)
	data[148] = 1
	data[149] = 1
	data[150] = 1
	data[151] = 1
	data[159] = 7

	pci, err := ParsePCI(data)
	if err != nil {
		t.Fatalf("ParsePCI: %v", err)
	}
	if pci.VOBUStartPTS != 90_000 || pci.VOBUEndPTS != 180_000 {
		t.Fatalf("PTS = %d..%d", pci.VOBUStartPTS, pci.VOBUEndPTS)
	}
	if len(pci.ButtonGroups[0]) != 1 {
		t.Fatalf("buttons = %+v", pci.ButtonGroups)
	}
	button := pci.ButtonGroups[0][0]
	if button.XStart != 10 || button.XEnd != 100 || button.YStart != 20 || button.YEnd != 80 || !button.AutoAction {
		t.Fatalf("button = %+v", button)
	}
	if button.Command[7] != 7 {
		t.Fatalf("command = %x", button.Command)
	}
}

func TestParsePCIRejectsTooManyButtons(t *testing.T) {
	data := make([]byte, PCIPayloadSize)
	data[110] = 0x10
	data[113] = 37
	_, err := ParsePCI(data)
	if !errors.Is(err, ErrInvalidNAV) {
		t.Fatalf("ParsePCI error = %v, want ErrInvalidNAV", err)
	}
}

func TestRouteSPUFragments(t *testing.T) {
	payload := []byte{0x80, 0, 0, 0x20, 1, 2, 3}
	packet := append([]byte{0, 0, 1, 0xbd, 0, byte(len(payload))}, payload...)
	fragments, err := RouteSPUFragments(packet, 1, 3)
	if err != nil {
		t.Fatalf("RouteSPUFragments: %v", err)
	}
	if len(fragments) != 1 || fragments[0].StreamID != 0x20 || len(fragments[0].Data) != 3 {
		t.Fatalf("fragments = %+v", fragments)
	}
}

func TestRouteSPUFragmentsReadsPTS(t *testing.T) {
	pts := []byte{0x21, 0, 1, 0, 1}
	payload := append([]byte{0x80, 0x80, 5}, pts...)
	payload = append(payload, 0x20, 1, 2, 3)
	packet := append([]byte{0, 0, 1, 0xbd, 0, byte(len(payload))}, payload...)
	fragments, err := RouteSPUFragments(packet, 1, 3)
	if err != nil {
		t.Fatalf("RouteSPUFragments: %v", err)
	}
	if len(fragments) != 1 || !fragments[0].HasPTS || fragments[0].PTS != 0 {
		t.Fatalf("fragments = %+v", fragments)
	}
}

func TestRouteSPUFragmentsRejectsTruncatedPESHeader(t *testing.T) {
	data := append(make([]byte, 6), 0, 0, 1, 0xbd)
	_, err := RouteSPUFragments(data, 1, 1)
	if !errors.Is(err, ErrInvalidNAV) || !strings.Contains(err.Error(), "truncated PES payload") {
		t.Fatalf("RouteSPUFragments error = %v, want truncated ErrInvalidNAV", err)
	}
}

func TestScanCellParsesNAVAndSPU(t *testing.T) {
	sector := syntheticNAVSector()
	reader := bytes.NewReader(sector)
	result, err := ScanCell(reader, int64(len(sector)), 0, 0, CellScanOptions{
		MaxSectors:    1,
		MaxSPUPackets: 1,
		MaxSPUBytes:   3,
	})
	if err != nil {
		t.Fatalf("ScanCell: %v", err)
	}
	if result.ScannedSectors != 1 || len(result.Packets) != 1 || len(result.SPUFragments) != 1 || result.Truncated {
		t.Fatalf("scan = %+v", result)
	}
}

func TestScanCellReportsTruncation(t *testing.T) {
	data := make([]byte, SectorSize*2)
	result, err := ScanCell(bytes.NewReader(data), int64(len(data)), 0, 1, CellScanOptions{MaxSectors: 1})
	if err != nil {
		t.Fatalf("ScanCell: %v", err)
	}
	if !result.Truncated || result.ScannedSectors != 1 {
		t.Fatalf("scan = %+v", result)
	}
}

func FuzzParsePCI(f *testing.F) {
	f.Add(make([]byte, PCIPayloadSize))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = ParsePCI(data)
	})
}

func FuzzRouteSPUFragments(f *testing.F) {
	f.Add([]byte{0, 0, 1, 0xbd, 0, 0})
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = RouteSPUFragments(data, 8, 4096)
	})
}

func put24(target []byte, value uint32) {
	target[0] = byte(value >> 16)
	target[1] = byte(value >> 8)
	target[2] = byte(value)
}

func syntheticNAVSector() []byte {
	sector := make([]byte, SectorSize)
	copy(sector, []byte{0, 0, 1, 0xba})
	sector[4] = 0x40
	offset := 14

	pci := make([]byte, PCIPayloadSize)
	writePrivateStream2(sector, offset, 0, pci)
	offset += 7 + len(pci)
	dsi := make([]byte, minimumDSIPayloadSize)
	writePrivateStream2(sector, offset, 1, dsi)
	offset += 7 + len(dsi)

	payload := []byte{0x80, 0, 0, 0x20, 1, 2, 3}
	copy(sector[offset:], []byte{0, 0, 1, 0xbd})
	binary.BigEndian.PutUint16(sector[offset+4:offset+6], uint16(len(payload)))
	copy(sector[offset+6:], payload)
	return sector
}

func writePrivateStream2(target []byte, offset int, streamID byte, payload []byte) {
	copy(target[offset:], []byte{0, 0, 1, 0xbf})
	binary.BigEndian.PutUint16(target[offset+4:offset+6], uint16(len(payload)+1))
	target[offset+6] = streamID
	copy(target[offset+7:], payload)
}
