package render

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProbeRequiresExactMenuOptions(t *testing.T) {
	runner := &fakeRunner{outputs: []Output{
		{Stdout: []byte("Demuxer dvdvideo\n-menu -menu_lu -menu_vts -pgc -pg\n")},
		{Stdout: []byte("ffmpeg version example\n")},
	}}
	capability, err := Probe(context.Background(), runner, "ffmpeg")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !capability.Available || capability.Version != "ffmpeg version example" {
		t.Fatalf("capability = %+v", capability)
	}

	missing := &fakeRunner{outputs: []Output{{Stdout: []byte("Demuxer dvdvideo\n-menu\n")}}}
	_, err = Probe(context.Background(), missing, "ffmpeg")
	if !errors.Is(err, ErrCapability) {
		t.Fatalf("Probe error = %v, want ErrCapability", err)
	}
	for _, option := range []string{"-menu_lu", "-menu_vts", "-pgc", "-pg"} {
		if !strings.Contains(err.Error(), option) {
			t.Fatalf("Probe error = %v, want missing option %s", err, option)
		}
	}
}

func TestDecodeFrameRejectsOversizedDimensions(t *testing.T) {
	var payload bytes.Buffer
	if err := png.Encode(&payload, image.NewNRGBA(image.Rect(0, 0, MaxFrameWidth+1, 1))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	runner := &fakeRunner{outputs: []Output{{Stdout: payload.Bytes()}}}
	_, err := DecodeFrame(context.Background(), runner, "ffmpeg", FrameRequest{
		SourcePath:   `C:\path\to\VIDEO_TS`,
		LanguageUnit: 1,
		PGC:          1,
		Program:      1,
	})
	if !errors.Is(err, ErrFrame) {
		t.Fatalf("DecodeFrame error = %v, want ErrFrame", err)
	}
}

func TestBuildArgsPlacesInputOptionsBeforeSource(t *testing.T) {
	request := FrameRequest{
		SourcePath:   `C:\path\to\VIDEO_TS`,
		VTS:          2,
		LanguageUnit: 1,
		PGC:          3,
		Program:      4,
		Target:       1500 * time.Millisecond,
		Deinterlace:  true,
	}
	args, err := BuildArgs(request)
	if err != nil {
		t.Fatalf("BuildArgs: %v", err)
	}
	wantPrefix := []string{"-hide_banner", "-loglevel", "error", "-f", "dvdvideo", "-menu", "1", "-menu_vts", "2", "-menu_lu", "1", "-pgc", "3", "-pg", "4", "-ss", "1.500000", "-i", request.SourcePath}
	if !reflect.DeepEqual(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("args prefix = %q, want %q", args[:len(wantPrefix)], wantPrefix)
	}
}

type fakeRunner struct {
	outputs []Output
	errors  []error
	calls   int
}

func (r *fakeRunner) Run(_ context.Context, _ string, _ []string, _ int) (Output, error) {
	index := r.calls
	r.calls++
	var output Output
	var err error
	if index < len(r.outputs) {
		output = r.outputs[index]
	}
	if index < len(r.errors) {
		err = r.errors[index]
	}
	return output, err
}
