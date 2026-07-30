package graph

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
	"github.com/autobrr/upbrr/internal/dvdvideo/nav"
	"github.com/autobrr/upbrr/internal/dvdvideo/vm"
)

func TestDiscoverReachableAndStructuralMenus(t *testing.T) {
	logger := &graphTestLogger{}
	root := ifo.ProgramChain{
		Number:   1,
		EntryID:  0x83,
		MenuID:   3,
		Programs: 1,
		Cells:    1,
	}
	chapter := ifo.ProgramChain{
		Number:   2,
		EntryID:  0x07,
		MenuID:   7,
		Programs: 1,
		Cells:    1,
	}
	audio := ifo.ProgramChain{
		Number:   3,
		EntryID:  0x05,
		MenuID:   5,
		Programs: 1,
		Cells:    1,
	}
	disc := ifo.Disc{Manager: ifo.File{
		Kind:      ifo.KindManager,
		Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: []ifo.ProgramChain{root, chapter, audio}}},
	}}
	resolver := fakeResolver{live: map[int]LiveState{
		1: {
			Visible:     true,
			Fingerprint: []byte("root"),
			Buttons:     []nav.Button{{Command: linkPGCN(2)}},
		},
		2: {Visible: true, Fingerprint: []byte("chapter")},
		3: {
			Visible:     true,
			Fingerprint: []byte("audio"),
			Buttons:     []nav.Button{{Number: 1}},
		},
	}}
	result, err := Discover(context.Background(), disc, resolver, Options{MaxItems: 6, Logger: logger})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Screens) != 3 || result.Screens[0].Discovery != DiscoveryReachable || result.Screens[1].Discovery != DiscoveryReachable || result.Screens[2].Discovery != DiscoveryStructural {
		t.Fatalf("screens = %+v", result.Screens)
	}
	if !result.Partial || result.Complete {
		t.Fatalf("coverage = complete:%t partial:%t", result.Complete, result.Partial)
	}
	if result.VisitedButtons != 1 {
		t.Fatalf("visited buttons = %d, want 1", result.VisitedButtons)
	}
	for _, expected := range []string{
		"DEBUG DVD menus: discovery started language= inventoried=3 seeds=1",
		"DEBUG DVD menus: branch queued action=button",
		"DEBUG DVD menus: screen selected discovery=structural",
		`WARN DVD menus: traversal warning code=structural_only detail="visible menu was not reached through navigation" domain=vmg`,
		"DEBUG DVD menus: structural reconciliation complete classified=3 transitions=0 noninteractive=0 utility=0 duplicates=0 visible_unreached=1 failures=0",
		"DEBUG DVD menus: discovery complete inventoried=3 classified=3 reached=2",
	} {
		if !logger.Contains(expected) {
			t.Fatalf("missing DVD graph log event %q", expected)
		}
	}
}

type graphTestLogger struct {
	entries []string
}

func (l *graphTestLogger) add(level, format string, args ...any) {
	l.entries = append(l.entries, level+" "+fmt.Sprintf(format, args...))
}

func (l *graphTestLogger) Tracef(format string, args ...any) {
	l.add("TRACE", format, args...)
}

func (l *graphTestLogger) Debugf(format string, args ...any) {
	l.add("DEBUG", format, args...)
}

func (l *graphTestLogger) Infof(format string, args ...any) {
	l.add("INFO", format, args...)
}

func (l *graphTestLogger) Warnf(format string, args ...any) {
	l.add("WARN", format, args...)
}

func (l *graphTestLogger) Contains(expected string) bool {
	for _, entry := range l.entries {
		if strings.Contains(entry, expected) {
			return true
		}
	}
	return false
}

func (l *graphTestLogger) Count(expected string) int {
	count := 0
	for _, entry := range l.entries {
		if strings.Contains(entry, expected) {
			count++
		}
	}
	return count
}

func TestDiscoverTruncatesStoredScreens(t *testing.T) {
	logger := &graphTestLogger{}
	chains := []ifo.ProgramChain{
		{
			Number:   1,
			EntryID:  0x83,
			MenuID:   3,
			Programs: 1,
			Cells:    1,
		},
		{
			Number:   2,
			EntryID:  0x85,
			MenuID:   5,
			Programs: 1,
			Cells:    1,
		},
	}
	disc := ifo.Disc{Manager: ifo.File{Kind: ifo.KindManager, Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: chains}}}}
	resolver := fakeResolver{live: map[int]LiveState{
		1: {Visible: true, Fingerprint: []byte("one")},
		2: {
			Visible:     true,
			Fingerprint: []byte("two"),
			Buttons:     []nav.Button{{Number: 1}},
		},
	}}
	result, err := Discover(context.Background(), disc, resolver, Options{MaxItems: 1, Logger: logger})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Screens) != 1 || !result.Truncated || result.Complete {
		t.Fatalf("result = %+v", result)
	}
	if logger.Count("DVD menus: storage limit applied") != 1 {
		t.Fatal("expected one aggregated storage-limit log")
	}
	if logger.Count("decision=truncate") != 0 {
		t.Fatal("unexpected per-candidate truncation log")
	}
}

func TestDiscoverClassifiesInvisibleStructuralProgramsWithoutCapturingThem(t *testing.T) {
	t.Parallel()

	root := ifo.ProgramChain{
		Number:   1,
		EntryID:  0x83,
		MenuID:   3,
		Programs: 1,
		Cells:    1,
	}
	transition := ifo.ProgramChain{
		Number:   2,
		Programs: 1,
		Cells:    1,
	}
	disc := ifo.Disc{Manager: ifo.File{
		Kind:      ifo.KindManager,
		Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: []ifo.ProgramChain{root, transition}}},
	}}
	resolver := fakeResolver{live: map[int]LiveState{
		1: {Visible: true, Fingerprint: []byte("root")},
		2: {Visible: false, Fingerprint: []byte("transition")},
	}}
	result, err := Discover(context.Background(), disc, resolver, Options{MaxItems: 1})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Screens) != 1 || result.Screens[0].Coordinate.PGC != 1 {
		t.Fatalf("screens = %+v", result.Screens)
	}
	if result.Partial || result.Truncated || !result.Complete {
		t.Fatalf("coverage = complete:%t partial:%t truncated:%t", result.Complete, result.Partial, result.Truncated)
	}
}

func TestDiscoverDoesNotCaptureUnreachedZeroButtonScreens(t *testing.T) {
	t.Parallel()

	root := ifo.ProgramChain{
		Number:   1,
		EntryID:  0x83,
		MenuID:   3,
		Programs: 1,
		Cells:    1,
	}
	nonInteractive := ifo.ProgramChain{
		Number:    2,
		Programs:  1,
		Cells:     1,
		StillTime: 0xff,
	}
	disc := ifo.Disc{Manager: ifo.File{
		Kind:      ifo.KindManager,
		Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: []ifo.ProgramChain{root, nonInteractive}}},
	}}
	resolver := fakeResolver{live: map[int]LiveState{
		1: {Visible: true, Fingerprint: []byte("root")},
		2: {
			Visible:       true,
			Fingerprint:   []byte("non-menu"),
			AuthoredStill: true,
			AuthoredEntry: true,
		},
	}}
	result, err := Discover(context.Background(), disc, resolver, Options{MaxItems: 2})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Screens) != 1 || result.Screens[0].Coordinate.PGC != 1 {
		t.Fatalf("screens = %+v", result.Screens)
	}
	if result.Partial || result.Truncated || !result.Complete {
		t.Fatalf("coverage = complete:%t partial:%t truncated:%t", result.Complete, result.Partial, result.Truncated)
	}
}

func TestDiscoverDeduplicatesEquivalentControlsAcrossBackgrounds(t *testing.T) {
	t.Parallel()

	logger := &graphTestLogger{}
	root := ifo.ProgramChain{
		Number:   1,
		EntryID:  0x83,
		MenuID:   3,
		Programs: 1,
		Cells:    1,
	}
	alternate := ifo.ProgramChain{
		Number:   2,
		MenuID:   3,
		Programs: 1,
		Cells:    1,
	}
	disc := ifo.Disc{Manager: ifo.File{
		Kind:      ifo.KindManager,
		Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: []ifo.ProgramChain{root, alternate}}},
	}}
	button := nav.Button{
		Number:     1,
		ColorGroup: 1,
		XStart:     10,
		YStart:     20,
		XEnd:       100,
		YEnd:       80,
	}
	alternateButton := button
	alternateButton.XStart += 7
	alternateButton.YEnd += 8
	resolver := fakeResolver{live: map[int]LiveState{
		1: {
			Visible:       true,
			Fingerprint:   []byte("first-background"),
			Buttons:       []nav.Button{button},
			AuthoredEntry: true,
		},
		2: {
			Visible:     true,
			Fingerprint: []byte("alternate-background"),
			Buttons:     []nav.Button{alternateButton},
		},
	}}
	result, err := Discover(context.Background(), disc, resolver, Options{MaxItems: 2, Logger: logger})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Screens) != 1 || result.Screens[0].Coordinate.PGC != 1 {
		t.Fatalf("screens = %+v", result.Screens)
	}
	if result.Partial || result.Truncated || !result.Complete {
		t.Fatalf("coverage = complete:%t partial:%t truncated:%t", result.Complete, result.Partial, result.Truncated)
	}
	if !logger.Contains("screen skipped decision=dedupe reason=control_identity") {
		t.Fatal("missing control-identity dedupe log")
	}
}

func TestDiscoverPreservesSameLayoutWithDifferentCommands(t *testing.T) {
	t.Parallel()

	root := ifo.ProgramChain{
		Number:   1,
		EntryID:  0x83,
		MenuID:   3,
		Programs: 1,
		Cells:    1,
	}
	different := ifo.ProgramChain{
		Number:   2,
		MenuID:   3,
		Programs: 1,
		Cells:    1,
	}
	disc := ifo.Disc{Manager: ifo.File{
		Kind:      ifo.KindManager,
		Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: []ifo.ProgramChain{root, different}}},
	}}
	baseButton := nav.Button{
		Number: 1,
		XStart: 10,
		YStart: 20,
		XEnd:   100,
		YEnd:   80,
	}
	differentButton := baseButton
	differentButton.Command = linkPGCN(1)
	resolver := fakeResolver{live: map[int]LiveState{
		1: {
			Visible:       true,
			Fingerprint:   []byte("first"),
			Buttons:       []nav.Button{baseButton},
			AuthoredEntry: true,
		},
		2: {
			Visible:     true,
			Fingerprint: []byte("second"),
			Buttons:     []nav.Button{differentButton},
		},
	}}
	result, err := Discover(context.Background(), disc, resolver, Options{MaxItems: 2})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Screens) != 2 {
		t.Fatalf("screens = %+v", result.Screens)
	}
}

func TestDiscoverTraversesButDoesNotCaptureManagerUtilityScreen(t *testing.T) {
	t.Parallel()

	logger := &graphTestLogger{}
	utility := ifo.ProgramChain{
		Number:   1,
		Programs: 1,
		Cells:    1,
	}
	root := ifo.ProgramChain{
		Number:   2,
		MenuID:   3,
		Programs: 1,
		Cells:    1,
	}
	disc := ifo.Disc{Manager: ifo.File{
		Kind:      ifo.KindManager,
		Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: []ifo.ProgramChain{utility, root}}},
	}}
	resolver := fakeResolver{live: map[int]LiveState{
		1: {
			Visible:     true,
			Fingerprint: []byte("utility"),
			Buttons:     []nav.Button{{Number: 1, Command: linkPGCN(2)}},
		},
		2: {
			Visible:     true,
			Fingerprint: []byte("root"),
			Buttons:     []nav.Button{{Number: 1}},
		},
	}}
	result, err := Discover(context.Background(), disc, resolver, Options{MaxItems: 2, Logger: logger})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(result.Screens) != 1 || result.Screens[0].Coordinate.PGC != 2 {
		t.Fatalf("screens = %+v", result.Screens)
	}
	if result.VisitedButtons != 2 {
		t.Fatalf("visited buttons = %d, want 2", result.VisitedButtons)
	}
	if !logger.Contains("utility=1") {
		t.Fatal("missing manager utility classification summary")
	}
}

func TestDiscoverRejectsItemLimitOverSafetyMaximum(t *testing.T) {
	t.Parallel()

	_, err := Discover(context.Background(), ifo.Disc{}, fakeResolver{}, Options{MaxItems: MaxMenuItems + 1})
	if err == nil {
		t.Fatal("expected item limit error")
	}
}

func TestDiscoverMapsProgramToAuthoredCell(t *testing.T) {
	chain := ifo.ProgramChain{
		Number:     1,
		EntryID:    0x83,
		MenuID:     3,
		Programs:   2,
		Cells:      3,
		ProgramMap: []uint8{1, 3},
	}
	chain.PreCommands = []vm.Command{linkPGN(2)}
	disc := ifo.Disc{Manager: ifo.File{Kind: ifo.KindManager, Languages: []ifo.LanguageUnit{{Code: "en", ProgramChains: []ifo.ProgramChain{chain}}}}}
	resolver := &coordinateResolver{}
	result, err := Discover(context.Background(), disc, resolver, Options{MaxItems: 2})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !resolver.saw(2, 3) {
		t.Fatalf("coordinates = %+v, want program 2 cell 3", resolver.coordinates)
	}
	if result.Inventoried != 2 {
		t.Fatalf("inventoried = %d, want 2 program coordinates", result.Inventoried)
	}
}

type fakeResolver struct{ live map[int]LiveState }

func (r fakeResolver) Resolve(_ context.Context, coordinate Coordinate, _ ifo.ProgramChain, _ vm.Registers) (LiveState, error) {
	return r.live[coordinate.PGC], nil
}

type coordinateResolver struct{ coordinates []Coordinate }

func (r *coordinateResolver) Resolve(_ context.Context, coordinate Coordinate, _ ifo.ProgramChain, _ vm.Registers) (LiveState, error) {
	r.coordinates = append(r.coordinates, coordinate)
	return LiveState{Visible: true, Fingerprint: []byte("mapped")}, nil
}

func (r *coordinateResolver) saw(program, cell int) bool {
	for _, coordinate := range r.coordinates {
		if coordinate.Program == program && coordinate.Cell == cell {
			return true
		}
	}
	return false
}

func linkPGCN(number uint16) vm.Command {
	var instruction uint64
	instruction |= 1 << 61
	instruction |= 4 << 48
	instruction |= uint64(number) << 0
	var command vm.Command
	binary.BigEndian.PutUint64(command[:], instruction)
	return command
}

func linkPGN(number uint16) vm.Command {
	var instruction uint64
	instruction |= 1 << 61
	instruction |= 6 << 48
	instruction |= uint64(number) << 0
	var command vm.Command
	binary.BigEndian.PutUint64(command[:], instruction)
	return command
}
