package ifo

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseManagerMenuInventory(t *testing.T) {
	data := syntheticManagerIFO()
	file, err := Parse(data, KindManager, 0)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(file.Languages) != 1 || file.Languages[0].Code != "en" {
		t.Fatalf("languages = %+v, want one English unit", file.Languages)
	}
	chains := file.Languages[0].ProgramChains
	if len(chains) != 1 {
		t.Fatalf("program chains = %d, want 1", len(chains))
	}
	pgc := chains[0]
	if pgc.MenuID != 3 || pgc.Programs != 1 || pgc.Cells != 1 {
		t.Fatalf("PGC identity = %+v", pgc)
	}
	if len(pgc.PreCommands) != 1 || pgc.PreCommands[0][7] != 1 {
		t.Fatalf("pre-commands = %x", pgc.PreCommands)
	}
	if pgc.CellPlayback[0].LastSector != 11 || pgc.CellPositions[0].VOBID != 2 {
		t.Fatalf("cell data = %+v %+v", pgc.CellPlayback, pgc.CellPositions)
	}
}

func TestParseRejectsTruncatedLanguageTable(t *testing.T) {
	data := syntheticManagerIFO()
	data = data[:sectorSize+10]
	_, err := Parse(data, KindManager, 0)
	if !errors.Is(err, ErrInvalidIFO) {
		t.Fatalf("Parse error = %v, want ErrInvalidIFO", err)
	}
}

func TestDVDTimeDuration(t *testing.T) {
	duration, err := (DVDTime{Hour: 0x01, Minute: 0x02, Second: 0x03, Frame: 0x52}).Duration()
	if err != nil {
		t.Fatalf("Duration: %v", err)
	}
	want := time.Hour + 2*time.Minute + 3*time.Second + 12*time.Second/25
	if duration != want {
		t.Fatalf("Duration = %s, want %s", duration, want)
	}
}

func TestDVDTimeDurationRejectsInvalidBCD(t *testing.T) {
	_, err := (DVDTime{Minute: 0x6a}).Duration()
	if !errors.Is(err, ErrInvalidIFO) {
		t.Fatalf("Duration error = %v, want ErrInvalidIFO", err)
	}
}

func TestParseRejectsProgramCellOutsidePGC(t *testing.T) {
	data := syntheticManagerIFO()
	data[sectorSize+16+16+252] = 2
	_, err := Parse(data, KindManager, 0)
	if !errors.Is(err, ErrInvalidIFO) {
		t.Fatalf("Parse error = %v, want ErrInvalidIFO", err)
	}
}

func TestInspectDirectoryRecoversMissingManagerIFOFromBUP(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "video_ts.bup"), syntheticManagerIFO(), 0o600); err != nil {
		t.Fatalf("write manager BUP: %v", err)
	}

	disc, err := InspectDirectory(root)
	if err != nil {
		t.Fatalf("InspectDirectory: %v", err)
	}
	if !disc.Manager.Recovered || disc.Manager.SourceName != "video_ts.bup" {
		t.Fatalf("manager recovery = recovered:%t source:%q", disc.Manager.Recovered, disc.Manager.SourceName)
	}
}

func TestInspectDirectoryDiscoversBUPOnlyTitleSet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VIDEO_TS.IFO"), syntheticManagerIFO(), 0o600); err != nil {
		t.Fatalf("write manager IFO: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "vts_02_0.bup"), syntheticTitleSetIFO(), 0o600); err != nil {
		t.Fatalf("write title-set BUP: %v", err)
	}

	disc, err := InspectDirectory(root)
	if err != nil {
		t.Fatalf("InspectDirectory: %v", err)
	}
	if len(disc.TitleSets) != 1 || disc.TitleSets[0].VTS != 2 || !disc.TitleSets[0].Recovered || disc.TitleSets[0].SourceName != "vts_02_0.bup" {
		t.Fatalf("title sets = %+v", disc.TitleSets)
	}
}

func TestInspectDirectoryRejectsManagerIFOSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "manager-target")
	if err := os.WriteFile(target, syntheticManagerIFO(), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "VIDEO_TS.IFO")); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}

	_, err := InspectDirectory(root)
	if !errors.Is(err, ErrInvalidIFO) {
		t.Fatalf("InspectDirectory error = %v, want ErrInvalidIFO", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(syntheticManagerIFO())
	f.Add([]byte("DVDVIDEO-VMG"))
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = Parse(data, KindManager, 0)
	})
}

func syntheticManagerIFO() []byte {
	data := make([]byte, sectorSize*2)
	copy(data, "DVDVIDEO-VMG")
	binary.BigEndian.PutUint32(data[200:204], 1)

	pgci := sectorSize
	binary.BigEndian.PutUint16(data[pgci:pgci+2], 1)
	binary.BigEndian.PutUint32(data[pgci+4:pgci+8], 312)
	copy(data[pgci+8:pgci+10], "en")
	data[pgci+11] = 0x80
	binary.BigEndian.PutUint32(data[pgci+12:pgci+16], 16)

	pgcit := pgci + 16
	binary.BigEndian.PutUint16(data[pgcit:pgcit+2], 1)
	binary.BigEndian.PutUint32(data[pgcit+4:pgcit+8], 296)
	data[pgcit+8] = 0x83
	binary.BigEndian.PutUint32(data[pgcit+12:pgcit+16], 16)

	pgc := pgcit + 16
	data[pgc+2] = 1
	data[pgc+3] = 1
	binary.BigEndian.PutUint16(data[pgc+228:pgc+230], 236)
	binary.BigEndian.PutUint16(data[pgc+230:pgc+232], 252)
	binary.BigEndian.PutUint16(data[pgc+232:pgc+234], 253)
	binary.BigEndian.PutUint16(data[pgc+234:pgc+236], 277)

	commands := pgc + 236
	binary.BigEndian.PutUint16(data[commands:commands+2], 1)
	binary.BigEndian.PutUint16(data[commands+6:commands+8], 15)
	data[commands+15] = 1
	data[pgc+252] = 1

	cell := pgc + 253
	binary.BigEndian.PutUint32(data[cell+8:cell+12], 10)
	binary.BigEndian.PutUint32(data[cell+12:cell+16], 10)
	binary.BigEndian.PutUint32(data[cell+16:cell+20], 11)
	binary.BigEndian.PutUint32(data[cell+20:cell+24], 11)
	position := pgc + 277
	binary.BigEndian.PutUint16(data[position:position+2], 2)
	data[position+3] = 1
	return data
}

func syntheticTitleSetIFO() []byte {
	data := syntheticManagerIFO()
	copy(data, "DVDVIDEO-VTS")
	binary.BigEndian.PutUint32(data[200:204], 0)
	binary.BigEndian.PutUint32(data[208:212], 1)
	return data
}
