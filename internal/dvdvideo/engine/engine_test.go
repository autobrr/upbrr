package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/dvdvideo/graph"
	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
	"github.com/autobrr/upbrr/internal/dvdvideo/nav"
	"github.com/autobrr/upbrr/internal/dvdvideo/render"
)

func TestCaptureDirectoryUsesInventoryCoordinatesAndComposesMenu(t *testing.T) {
	root := writeSyntheticDVD(t, true)
	runner := newFakeRunner(t)
	logger := &engineTestLogger{}

	result, err := CaptureDirectory(context.Background(), root, runner, "ffmpeg", Options{Traversal: graph.Options{MaxItems: 1}, Logger: logger})
	if err != nil {
		t.Fatalf("CaptureDirectory: %v", err)
	}
	if len(result.Captures) != 1 || !result.Complete || result.Partial || result.Truncated {
		t.Fatalf("result = %+v", result)
	}
	capture := result.Captures[0]
	if capture.Coordinate.Kind != ifo.KindManager || capture.Coordinate.VTS != 0 || capture.Coordinate.LanguageUnit != 1 || capture.Coordinate.PGC != 1 || capture.Coordinate.Program != 1 || capture.Coordinate.Cell != 1 {
		t.Fatalf("coordinate = %+v", capture.Coordinate)
	}
	if !capture.HasOverlay || !capture.HasHighlight || imageIsBlack(capture.Image) {
		t.Fatalf("capture state overlay=%t highlight=%t black=%t", capture.HasOverlay, capture.HasHighlight, imageIsBlack(capture.Image))
	}

	wantPrefix := []string{"-hide_banner", "-loglevel", "error", "-f", "dvdvideo", "-menu", "1", "-menu_vts", "0", "-menu_lu", "1", "-pgc", "1", "-pg", "1", "-i", root}
	frameArgs := runner.calls[2].args
	if !reflect.DeepEqual(frameArgs[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("frame args prefix = %q, want %q", frameArgs[:len(wantPrefix)], wantPrefix)
	}
	for _, expected := range []string{
		"DEBUG DVD menus: inventory ready",
		"DEBUG DVD menus: discovery started",
		"TRACE DVD menus: state visiting domain=vmg",
		"DEBUG DVD menus: state resolved domain=vmg",
		"DEBUG DVD menus: render started index=1",
		"DEBUG DVD menus: frame decoded index=1",
		"DEBUG DVD menus: capture stored index=1",
		"DEBUG DVD menus: rendering complete",
	} {
		if !logger.Contains(expected) {
			t.Fatalf("missing DVD engine log event %q", expected)
		}
	}
	if logger.Contains(root) {
		t.Fatal("DVD engine logs exposed the source path")
	}
}

type engineTestLogger struct {
	entries []string
}

func (l *engineTestLogger) add(level, format string, args ...any) {
	l.entries = append(l.entries, level+" "+fmt.Sprintf(format, args...))
}

func (l *engineTestLogger) Tracef(format string, args ...any) {
	l.add("TRACE", format, args...)
}

func (l *engineTestLogger) Debugf(format string, args ...any) {
	l.add("DEBUG", format, args...)
}

func (l *engineTestLogger) Infof(format string, args ...any) {
	l.add("INFO", format, args...)
}

func (l *engineTestLogger) Warnf(format string, args ...any) {
	l.add("WARN", format, args...)
}

func (l *engineTestLogger) Contains(expected string) bool {
	for _, entry := range l.entries {
		if strings.Contains(entry, expected) {
			return true
		}
	}
	return false
}

func TestCaptureDirectoryRejectsItemLimitOverSafetyMaximum(t *testing.T) {
	t.Parallel()

	_, err := CaptureDirectory(context.Background(), t.TempDir(), newFakeRunner(t), "ffmpeg", Options{
		Traversal: graph.Options{MaxItems: graph.MaxMenuItems + 1},
	})
	if !errors.Is(err, ErrCapture) {
		t.Fatalf("CaptureDirectory error = %v, want ErrCapture", err)
	}
}

func TestVisibleMenuStateRequiresAuthoredMenuEvidence(t *testing.T) {
	t.Parallel()

	base := ifo.ProgramChain{
		Programs:     2,
		Cells:        1,
		CellPlayback: []ifo.CellPlayback{{}},
	}
	withPGCStill := base
	withPGCStill.StillTime = 0xff
	withCellStill := base
	withCellStill.CellPlayback = []ifo.CellPlayback{{StillTime: 5}}
	entry := base
	entry.EntryID = 0x80
	tests := []struct {
		name       string
		pgc        ifo.ProgramChain
		coordinate graph.Coordinate
		buttons    []nav.Button
		overlay    bool
		highlight  bool
		want       bool
	}{
		{
			name:       "button",
			pgc:        base,
			coordinate: graph.Coordinate{Program: 1, Cell: 1},
			buttons:    []nav.Button{{Number: 1}},
			want:       true,
		},
		{
			name:       "overlay",
			pgc:        base,
			coordinate: graph.Coordinate{Program: 1, Cell: 1},
			overlay:    true,
			want:       true,
		},
		{
			name:       "highlight",
			pgc:        base,
			coordinate: graph.Coordinate{Program: 1, Cell: 1},
			highlight:  true,
			want:       true,
		},
		{
			name:       "pgc still",
			pgc:        withPGCStill,
			coordinate: graph.Coordinate{Program: 1, Cell: 1},
			want:       true,
		},
		{
			name:       "cell still",
			pgc:        withCellStill,
			coordinate: graph.Coordinate{Program: 1, Cell: 1},
			want:       true,
		},
		{
			name:       "entry program",
			pgc:        entry,
			coordinate: graph.Coordinate{Program: 1, Cell: 1},
			want:       true,
		},
		{
			name:       "entry transition program",
			pgc:        entry,
			coordinate: graph.Coordinate{Program: 2, Cell: 1},
		},
		{
			name:       "plain transition",
			pgc:        base,
			coordinate: graph.Coordinate{Program: 1, Cell: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := visibleMenuState(tt.pgc, tt.coordinate, tt.buttons, tt.overlay, tt.highlight); got != tt.want {
				t.Fatalf("visibleMenuState() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestCaptureDirectoryCapturesManagerAndTitleSetCoordinates(t *testing.T) {
	root := writeSyntheticDVD(t, true)
	if err := os.WriteFile(filepath.Join(root, "VTS_01_0.IFO"), syntheticTitleSetIFO(), 0o600); err != nil {
		t.Fatalf("write synthetic title-set IFO: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "VTS_01_0.VOB"), syntheticMenuVOB(true), 0o600); err != nil {
		t.Fatalf("write synthetic title-set menu VOB: %v", err)
	}
	runner := newFakeRunner(t)
	runner.outputs = append(runner.outputs, runner.outputs[2])

	result, err := CaptureDirectory(context.Background(), root, runner, "ffmpeg", Options{Traversal: graph.Options{MaxItems: 2}})
	if err != nil {
		t.Fatalf("CaptureDirectory: %v", err)
	}
	if len(result.Captures) != 2 || !result.Complete || result.Partial || result.Truncated {
		t.Fatalf("result = %+v", result)
	}
	manager, titleSet := result.Captures[0].Coordinate, result.Captures[1].Coordinate
	if manager.Kind != ifo.KindManager || manager.VTS != 0 || titleSet.Kind != ifo.KindTitleSet || titleSet.VTS != 1 {
		t.Fatalf("capture coordinates = manager:%+v title_set:%+v", manager, titleSet)
	}
	if got := runner.calls[2].args[7:9]; !reflect.DeepEqual(got, []string{"-menu_vts", "0"}) {
		t.Fatalf("manager FFmpeg coordinate = %q", got)
	}
	if got := runner.calls[3].args[7:9]; !reflect.DeepEqual(got, []string{"-menu_vts", "1"}) {
		t.Fatalf("title-set FFmpeg coordinate = %q", got)
	}
}

func TestCaptureDirectoryReportsMissingSPUAsPartial(t *testing.T) {
	root := writeSyntheticDVD(t, false)
	runner := newFakeRunner(t)
	logger := &engineTestLogger{}

	result, err := CaptureDirectory(context.Background(), root, runner, "ffmpeg", Options{Traversal: graph.Options{MaxItems: 1}, Logger: logger})
	if err != nil {
		t.Fatalf("CaptureDirectory: %v", err)
	}
	if len(result.Captures) != 1 || !result.Partial || result.Complete || result.Captures[0].HasOverlay {
		t.Fatalf("result = %+v", result)
	}
	if !hasWarning(result.Warnings, "spu_unavailable") {
		t.Fatalf("warnings = %+v", result.Warnings)
	}
	if !logger.Contains(`WARN DVD menus: render warning code=spu_unavailable detail="menu subpicture state was not available" domain=vmg`) {
		t.Fatal("missing DVD render warning log")
	}
}

func TestCaptureDirectoryReturnsFatalWhenEveryFrameFails(t *testing.T) {
	root := writeSyntheticDVD(t, true)
	runner := newFakeRunner(t)
	runner.errors = []error{nil, nil, errors.New("synthetic process failure")}

	result, err := CaptureDirectory(context.Background(), root, runner, "ffmpeg", Options{Traversal: graph.Options{MaxItems: 1}})
	if !errors.Is(err, ErrCapture) {
		t.Fatalf("CaptureDirectory error = %v, want ErrCapture", err)
	}
	if len(result.Captures) != 0 || !result.Partial || !hasWarning(result.Warnings, "frame_decode") {
		t.Fatalf("result = %+v", result)
	}
}

func TestCaptureDiscPreservesSuccessfulFramesAfterLaterDecodeFailure(t *testing.T) {
	root := writeSyntheticDVD(t, true)
	resolver, err := newDirectoryResolver(root, nav.CellScanOptions{})
	if err != nil {
		t.Fatalf("newDirectoryResolver: %v", err)
	}
	base := syntheticProgramChain(1)
	second := syntheticProgramChain(2)
	second.EntryID = 0x85
	second.MenuID = 5
	disc := ifo.Disc{Manager: ifo.File{
		Kind:      ifo.KindManager,
		Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: []ifo.ProgramChain{base, second}}},
	}}
	frameRunner := newFakeRunner(t)
	runner := &fakeRunner{
		outputs: []render.Output{frameRunner.outputs[2], {}},
		errors:  []error{nil, errors.New("synthetic later frame failure")},
	}
	result, err := captureDisc(context.Background(), root, disc, resolver, runner, "ffmpeg", render.Capability{Available: true}, Options{
		Traversal:      graph.Options{MaxItems: 2},
		ProcessTimeout: DefaultProcessTimeout,
	})
	if err != nil {
		t.Fatalf("captureDisc: %v", err)
	}
	if len(result.Captures) != 1 || !result.Partial || result.Complete || !hasWarning(result.Warnings, "frame_decode") {
		t.Fatalf("result = %+v", result)
	}
}

func hasWarning(warnings []graph.Warning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func writeSyntheticDVD(t *testing.T, withSPU bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "VIDEO_TS.IFO"), syntheticManagerIFO(), 0o600); err != nil {
		t.Fatalf("write synthetic IFO: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "VIDEO_TS.VOB"), syntheticMenuVOB(withSPU), 0o600); err != nil {
		t.Fatalf("write synthetic menu VOB: %v", err)
	}
	return root
}

func syntheticManagerIFO() []byte {
	data := make([]byte, nav.SectorSize*2)
	copy(data, "DVDVIDEO-VMG")
	binary.BigEndian.PutUint32(data[200:204], 1)

	pgci := nav.SectorSize
	binary.BigEndian.PutUint16(data[pgci:pgci+2], 1)
	binary.BigEndian.PutUint32(data[pgci+4:pgci+8], 296)
	copy(data[pgci+8:pgci+10], "en")
	data[pgci+11] = 0x80
	binary.BigEndian.PutUint32(data[pgci+12:pgci+16], 16)

	pgcit := pgci + 16
	binary.BigEndian.PutUint16(data[pgcit:pgcit+2], 1)
	binary.BigEndian.PutUint32(data[pgcit+4:pgcit+8], 280)
	data[pgcit+8] = 0x83
	binary.BigEndian.PutUint32(data[pgcit+12:pgcit+16], 16)

	pgc := pgcit + 16
	data[pgc+2] = 1
	data[pgc+3] = 1
	binary.BigEndian.PutUint32(data[pgc+28:pgc+32], 0x8000_0000)
	binary.BigEndian.PutUint32(data[pgc+164+4:pgc+164+8], 0x0080_8080)
	binary.BigEndian.PutUint32(data[pgc+164+8:pgc+164+12], 0x00c0_8080)
	binary.BigEndian.PutUint32(data[pgc+164+12:pgc+164+16], 0x00ff_8080)
	binary.BigEndian.PutUint16(data[pgc+230:pgc+232], 236)
	binary.BigEndian.PutUint16(data[pgc+232:pgc+234], 237)
	binary.BigEndian.PutUint16(data[pgc+234:pgc+236], 261)
	data[pgc+236] = 1

	cell := pgc + 237
	binary.BigEndian.PutUint32(data[cell+8:cell+12], 0)
	binary.BigEndian.PutUint32(data[cell+12:cell+16], 0)
	binary.BigEndian.PutUint32(data[cell+16:cell+20], 0)
	binary.BigEndian.PutUint32(data[cell+20:cell+24], 0)
	position := pgc + 261
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

func syntheticProgramChain(number int) ifo.ProgramChain {
	chain := ifo.ProgramChain{
		Number:        number,
		EntryID:       0x83,
		MenuID:        3,
		Programs:      1,
		Cells:         1,
		ProgramMap:    []uint8{1},
		CellPlayback:  []ifo.CellPlayback{{FirstSector: 0, LastSector: 0}},
		CellPositions: []ifo.CellPosition{{VOBID: 2, CellID: 1}},
	}
	chain.SubpictureControl[0] = 0x8000_0000
	chain.Palette[1] = 0x0080_8080
	chain.Palette[2] = 0x00c0_8080
	chain.Palette[3] = 0x00ff_8080
	return chain
}

func syntheticMenuVOB(withSPU bool) []byte {
	sector := make([]byte, nav.SectorSize)
	copy(sector, []byte{0, 0, 1, 0xba})
	sector[4] = 0x40
	offset := 14

	pci := make([]byte, nav.PCIPayloadSize)
	binary.BigEndian.PutUint32(pci[12:16], 90_000)
	binary.BigEndian.PutUint32(pci[16:20], 180_000)
	binary.BigEndian.PutUint16(pci[96:98], 1)
	binary.BigEndian.PutUint32(pci[98:102], 90_000)
	pci[110] = 0x10
	pci[113] = 1
	pci[114] = 1
	pci[116] = 1
	pci[117] = 1
	binary.BigEndian.PutUint32(pci[118:122], 0x3333_ffff)
	put24(pci[142:145], 1<<22|0<<12|1)
	put24(pci[145:148], 1<<22|0<<12|1)
	pci[148] = 1
	pci[149] = 1
	pci[150] = 1
	pci[151] = 1
	writePrivateStream2(sector, offset, 0, pci)
	offset += 7 + len(pci)

	dsi := make([]byte, 32)
	binary.BigEndian.PutUint16(dsi[24:26], 2)
	dsi[27] = 1
	writePrivateStream2(sector, offset, 1, dsi)
	offset += 7 + len(dsi)
	if !withSPU {
		return sector
	}

	packet := syntheticSPUPacket()
	payload := append([]byte{0x80, 0, 0, 0x20}, packet...)
	copy(sector[offset:], []byte{0, 0, 1, 0xbd})
	binary.BigEndian.PutUint16(sector[offset+4:offset+6], uint16(len(payload)))
	copy(sector[offset+6:], payload)
	return sector
}

func syntheticSPUPacket() []byte {
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

func writePrivateStream2(target []byte, offset int, streamID byte, payload []byte) {
	copy(target[offset:], []byte{0, 0, 1, 0xbf})
	binary.BigEndian.PutUint16(target[offset+4:offset+6], uint16(len(payload)+1))
	target[offset+6] = streamID
	copy(target[offset+7:], payload)
}

func put24(target []byte, value uint32) {
	target[0] = byte(value >> 16)
	target[1] = byte(value >> 8)
	target[2] = byte(value)
}

type runnerCall struct {
	executable string
	args       []string
	limit      int
}

type fakeRunner struct {
	outputs []render.Output
	errors  []error
	calls   []runnerCall
}

func newFakeRunner(t *testing.T) *fakeRunner {
	t.Helper()
	var frame bytes.Buffer
	background := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			background.SetNRGBA(x, y, color.NRGBA{
				R: 16,
				G: 32,
				B: 48,
				A: 0xff,
			})
		}
	}
	if err := png.Encode(&frame, background); err != nil {
		t.Fatalf("encode background: %v", err)
	}
	return &fakeRunner{outputs: []render.Output{
		{Stdout: []byte("Demuxer dvdvideo\n-menu -menu_lu -menu_vts -pgc -pg\n")},
		{Stdout: []byte("ffmpeg version synthetic\n")},
		{Stdout: frame.Bytes()},
	}}
}

func (r *fakeRunner) Run(_ context.Context, executable string, args []string, limit int) (render.Output, error) {
	index := len(r.calls)
	r.calls = append(r.calls, runnerCall{
		executable: executable,
		args:       append([]string(nil), args...),
		limit:      limit,
	})
	var output render.Output
	var err error
	if index < len(r.outputs) {
		output = r.outputs[index]
	}
	if index < len(r.errors) {
		err = r.errors[index]
	}
	return output, err
}
