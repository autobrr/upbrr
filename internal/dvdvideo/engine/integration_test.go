package engine

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/dvdvideo/graph"
	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
	"github.com/autobrr/upbrr/internal/dvdvideo/render"
)

func TestExternalDVDInventoryDerivedCapture(t *testing.T) {
	root := os.Getenv("UPBRR_TEST_DVD_VIDEO_TS")
	if root == "" {
		t.Skip("set UPBRR_TEST_DVD_VIDEO_TS to run the external read-only DVD proof")
	}
	executable := os.Getenv("UPBRR_TEST_FFMPEG")
	if executable == "" {
		executable = "ffmpeg"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := CaptureDirectory(ctx, root, render.ExecRunner{}, executable, Options{
		Traversal:      graph.Options{Language: "en", MaxItems: DefaultMaxMenuItems},
		ProcessTimeout: time.Minute,
		Deinterlace:    true,
	})
	if err != nil {
		t.Fatalf("CaptureDirectory: %v", err)
	}
	t.Logf("external DVD capture summary inventoried=%d selected=%d captured=%d states=%d buttons=%d partial=%t truncated=%t warnings=%d", result.Inventoried, result.Selected, len(result.Captures), result.VisitedStates, result.VisitedButtons, result.Partial, result.Truncated, len(result.Warnings))
	for _, warning := range result.Warnings {
		t.Logf("external DVD capture warning code=%s", warning.Code)
	}
	var manager, titleSet bool
	var overlay, highlight bool
	for index, capture := range result.Captures {
		t.Logf("external DVD capture index=%d discovery=%s domain=%d vts=%d language_unit=%d menu_id=%d pgc=%d pg=%d cell=%d overlay=%t highlight=%t", index+1, capture.Discovery, capture.Coordinate.Kind, capture.Coordinate.VTS, capture.Coordinate.LanguageUnit, capture.Coordinate.MenuID, capture.Coordinate.PGC, capture.Coordinate.Program, capture.Coordinate.Cell, capture.HasOverlay, capture.HasHighlight)
		if imageIsBlack(capture.Image) {
			t.Fatal("inventory-derived capture produced a black composed frame")
		}
		overlay = overlay || capture.HasOverlay
		highlight = highlight || capture.HasHighlight
		switch capture.Coordinate.Kind {
		case ifo.KindManager:
			manager = true
		case ifo.KindTitleSet:
			titleSet = true
		}
	}
	t.Logf("external DVD capture domains manager=%t title_set=%t", manager, titleSet)
	if !overlay || !highlight {
		t.Fatalf("composed state overlay=%t highlight=%t captures=%d", overlay, highlight, len(result.Captures))
	}
	if !result.Capability.Available {
		t.Fatal("capture completed without the required FFmpeg DVD menu capability")
	}
}
