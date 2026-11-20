package ifo

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestFeatureTitlesRemainIndependentWithinOneVTS(t *testing.T) {
	root := t.TempDir()
	manager, vts := featureFixture()
	for name, data := range map[string][]byte{"VIDEO_TS.IFO": manager, "VTS_01_0.IFO": vts} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	disc, err := InspectFeatures(root)
	if err != nil || len(disc.Titles) != 2 {
		t.Fatalf("titles=%+v err=%v", disc.Titles, err)
	}
	for i, title := range disc.Titles {
		if title.Number != i+1 || title.VTSTitle != i+1 || title.Chain.Number != i+1 || title.Duration90 != 900000 || title.Unsupported != "" {
			t.Fatalf("title=%+v", title)
		}
	}
	// The same authored cell can belong to distinct global title selections.
	if disc.Titles[0].Chain.CellPlayback[0].FirstSector != disc.Titles[1].Chain.CellPlayback[0].FirstSector {
		t.Fatal("fixture lost shared-cell coverage")
	}
	if err := os.WriteFile(filepath.Join(root, "VTS_01_0.BUP"), vts, 0o600); err != nil {
		t.Fatal(err)
	}
	broken := append([]byte(nil), vts...)
	binary.BigEndian.PutUint32(broken[200:204], 99)
	if err := os.WriteFile(filepath.Join(root, "VTS_01_0.IFO"), broken, 0o600); err != nil {
		t.Fatal(err)
	}
	disc, err = InspectFeatures(root)
	if err != nil || disc.Metadata["VTS_01_0.BUP"] == nil {
		t.Fatalf("feature-table BUP recovery failed: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "VTS_01_0.BUP")); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectFeatures(root); !errors.Is(err, ErrInvalidIFO) {
		t.Fatalf("malformed feature table accepted: %v", err)
	}
}

func TestFeatureRejectsCellsBeforeFirstProgramme(t *testing.T) {
	manager, data := featureFixture()
	table := data[2*sectorSize : 3*sectorSize]
	pgc := table[600:893]
	copy(pgc, table[24:289])
	binary.BigEndian.PutUint32(table[12:], 600)
	binary.BigEndian.PutUint32(table[4:], 892)
	pgc[3], pgc[236] = 2, 2
	binary.BigEndian.PutUint16(pgc[234:], 285)
	copy(pgc[261:285], pgc[237:261])
	// Both cells have valid addresses and total the PGC duration, but programme
	// one starts at the second cell. Decoding the whole PGC would add a picture
	// sequence that does not belong to the selected title entry point.
	for index := range 2 {
		cell := pgc[237+index*24:]
		cell[6] = 5
		binary.BigEndian.PutUint32(cell[8:], uint32(10+index*50))
		binary.BigEndian.PutUint32(cell[16:], uint32(50+index*50))
		binary.BigEndian.PutUint32(cell[20:], uint32(59+index*50))
		binary.BigEndian.PutUint16(pgc[285+index*4:], 1)
		pgc[288+index*4] = 1
	}
	titles, err := featureTitles(manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := titles[0].resolve(data); err != nil {
		t.Fatal(err)
	}
	if titles[0].Unsupported == "" {
		t.Fatal("admitted cells before the selected programme")
	}
}

func TestFeatureUnsupportedTitleDoesNotReplaceSharedVOBUIndex(t *testing.T) {
	root := t.TempDir()
	manager, vts := featureFixture()
	manager[sectorSize+9] = 2 // The first title is multi-angle; the second is supported.
	for name, data := range map[string][]byte{"VIDEO_TS.IFO": manager, "VTS_01_0.IFO": vts} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	disc, err := InspectFeatures(root)
	if err != nil || len(disc.Titles) != 2 {
		t.Fatalf("titles=%+v err=%v", disc.Titles, err)
	}
	if disc.Titles[0].Unsupported != "nonsequential_programme" {
		t.Fatalf("first title unexpectedly supported: %+v", disc.Titles[0])
	}
	title := disc.Titles[1]
	if title.Unsupported != "" || len(title.VOBU) != 10 || len(title.TimeMap) != 3 {
		t.Fatalf("supported title lost seek evidence: %+v", title)
	}
	for _, sector := range title.TimeMap {
		if !slices.Contains(title.VOBU, sector&0x7fffffff) {
			t.Fatalf("time-map sector %d is missing from the VOBU index", sector)
		}
	}
}

func TestFeatureNTSCCellsAgreeWithPGCAcrossNominalSecond(t *testing.T) {
	manager, data := featureFixture()
	table := data[2*sectorSize : 3*sectorSize]
	pgc := table[600:893]
	copy(pgc, table[24:289])
	binary.BigEndian.PutUint32(table[12:], 600)
	binary.BigEndian.PutUint32(table[4:], 892)
	pgc[3], pgc[6], pgc[7] = 2, 1, 0xc0 // One nominal NTSC second in the PGC.
	binary.BigEndian.PutUint16(pgc[234:], 285)
	copy(pgc[261:285], pgc[237:261])
	for index := range 2 {
		cell := pgc[237+index*24:]
		cell[6], cell[7] = 0, 0xd5 // Fifteen NTSC frames per cell.
		binary.BigEndian.PutUint32(cell[8:], uint32(10+index*50))
		binary.BigEndian.PutUint32(cell[16:], uint32(50+index*50))
		binary.BigEndian.PutUint32(cell[20:], uint32(59+index*50))
		binary.BigEndian.PutUint16(pgc[285+index*4:], 1)
		pgc[288+index*4] = 1
	}
	titles, err := featureTitles(manager)
	if err != nil {
		t.Fatal(err)
	}
	title := titles[0]
	if err := title.resolve(data); err != nil {
		t.Fatal(err)
	}
	if title.Unsupported != "" || title.Duration90 != 90090 || title.Duration != 1001*time.Millisecond || len(title.VOBU) == 0 {
		t.Fatalf("valid NTSC title rejected or mistimed: %+v", title)
	}
}

func TestFeatureCellCommandsAcceptOnlyNoOp(t *testing.T) {
	for _, scenario := range []string{"no-op", "navigation", "invalid command", "interleaved", "still"} {
		t.Run(scenario, func(t *testing.T) {
			manager, data := featureFixture()
			table := data[2*sectorSize : 3*sectorSize]
			copy(table[305:570], table[289:554])
			clear(table[289:305])
			binary.BigEndian.PutUint32(table[20:], 305)
			binary.BigEndian.PutUint32(table[4:], 569)
			pgc := table[24:305]
			binary.BigEndian.PutUint16(pgc[228:], 265)
			binary.BigEndian.PutUint16(pgc[269:], 1)
			binary.BigEndian.PutUint16(pgc[271:], 15)
			pgc[240] = 1
			switch scenario {
			case "navigation":
				pgc[273] = 0x30
			case "invalid command":
				pgc[240] = 2
			case "interleaved":
				pgc[237] = 4
			case "still":
				pgc[239] = 1
			}
			titles, err := featureTitles(manager)
			if err != nil {
				t.Fatal(err)
			}
			title := titles[0]
			if err := title.resolve(data); err != nil {
				t.Fatal(err)
			}
			if (title.Unsupported == "") != (scenario == "no-op") {
				t.Fatalf("cell %s admission: %s", scenario, title.Unsupported)
			}
		})
	}
}

func featureFixture() ([]byte, []byte) {
	manager := make([]byte, 3*sectorSize)
	copy(manager, "DVDVIDEO-VMG")
	binary.BigEndian.PutUint32(manager[196:200], 1)
	titles := manager[sectorSize:]
	binary.BigEndian.PutUint16(titles, 2)
	binary.BigEndian.PutUint32(titles[4:8], 31)
	for i := range 2 {
		entry := titles[8+i*12:]
		entry[1] = 1
		binary.BigEndian.PutUint16(entry[2:4], 1)
		entry[6] = 1
		entry[7] = byte(i + 1)
	}
	vts := make([]byte, 6*sectorSize)
	copy(vts, "DVDVIDEO-VTS")
	for pointer, sector := range map[int]uint32{
		200: 1,
		204: 2,
		212: 3,
		224: 4,
		228: 5,
	} {
		binary.BigEndian.PutUint32(vts[pointer:pointer+4], sector)
	}
	ptt := vts[sectorSize : 2*sectorSize]
	binary.BigEndian.PutUint16(ptt, 2)
	binary.BigEndian.PutUint32(ptt[4:8], 23)
	for i := range 2 {
		binary.BigEndian.PutUint32(ptt[8+i*4:], uint32(16+i*4))
		binary.BigEndian.PutUint16(ptt[16+i*4:], uint16(i+1))
		binary.BigEndian.PutUint16(ptt[18+i*4:], 1)
	}
	pgcs := vts[2*sectorSize : 3*sectorSize]
	binary.BigEndian.PutUint16(pgcs, 2)
	binary.BigEndian.PutUint32(pgcs[4:8], 553)
	for i := range 2 {
		start := 24 + i*265
		binary.BigEndian.PutUint32(pgcs[12+i*8:], uint32(start))
		pgc := pgcs[start : start+265]
		pgc[2], pgc[3], pgc[6], pgc[7] = 1, 1, 0x10, 0x40
		binary.BigEndian.PutUint16(pgc[230:232], 236)
		binary.BigEndian.PutUint16(pgc[232:234], 237)
		binary.BigEndian.PutUint16(pgc[234:236], 261)
		pgc[236] = 1
		cell := pgc[237:261]
		cell[6], cell[7] = 0x10, 0x40
		binary.BigEndian.PutUint32(cell[8:], 10)
		binary.BigEndian.PutUint32(cell[16:], 100)
		binary.BigEndian.PutUint32(cell[20:], 109)
		binary.BigEndian.PutUint16(pgc[261:], 1)
		pgc[264] = 1
	}
	tmaps := vts[3*sectorSize : 4*sectorSize]
	binary.BigEndian.PutUint16(tmaps, 2)
	binary.BigEndian.PutUint32(tmaps[4:8], 47)
	for i := range 2 {
		start := 16 + i*16
		binary.BigEndian.PutUint32(tmaps[8+i*4:], uint32(start))
		tmaps[start] = 3
		binary.BigEndian.PutUint16(tmaps[start+2:], 3)
		for j := range 3 {
			binary.BigEndian.PutUint32(tmaps[start+4+j*4:], uint32(20+j*30))
		}
	}
	addresses := vts[4*sectorSize : 5*sectorSize]
	binary.BigEndian.PutUint16(addresses, 1)
	binary.BigEndian.PutUint32(addresses[4:8], 19)
	binary.BigEndian.PutUint16(addresses[8:], 1)
	addresses[10] = 1
	binary.BigEndian.PutUint32(addresses[12:], 10)
	binary.BigEndian.PutUint32(addresses[16:], 109)
	vobu := vts[5*sectorSize:]
	binary.BigEndian.PutUint32(vobu, 43)
	for i := range 10 {
		binary.BigEndian.PutUint32(vobu[4+i*4:], uint32(10+i*10))
	}
	return manager, vts
}

func TestFeatureLocalDisc(t *testing.T) {
	root := os.Getenv("UPBRR_COMPARISON_DVD")
	if root == "" {
		t.Skip("opt-in local DVD structural acceptance")
	}
	disc, err := InspectFeatures(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range disc.Titles {
		t.Logf("title=%d vts=%d programme=%d chapters=%d cells=%d duration=%s time_unit=%d time_entries=%d vobu_entries=%d limitation=%s", title.Number, title.VTS, title.VTSTitle, title.Chapters, len(title.Chain.CellPlayback), title.Duration, title.TimeUnit, len(title.TimeMap), len(title.VOBU), title.Unsupported)
		if title.Unsupported != "" {
			t.Fatal(title.Unsupported)
		}
	}
}
