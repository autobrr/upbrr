package render

import (
	"context"
	"os"
	"testing"
)

func TestFFmpegDVDVideoCapability(t *testing.T) {
	executable := os.Getenv("UPBRR_TEST_FFMPEG")
	if executable == "" {
		t.Skip("set UPBRR_TEST_FFMPEG to run the local FFmpeg capability proof")
	}
	capability, err := Probe(context.Background(), ExecRunner{}, executable)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !capability.Available {
		t.Fatal("FFmpeg DVD menu capability unavailable")
	}
}
