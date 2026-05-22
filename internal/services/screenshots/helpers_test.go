// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"encoding/json"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildScreenshotSelections(t *testing.T) {
	meta := api.PreparedMetadata{}
	selections := buildScreenshotSelections(4, 600, 24, meta)
	if len(selections) != 4 {
		t.Fatalf("expected 4 selections, got %d", len(selections))
	}
	prev := -1.0
	for _, sel := range selections {
		if sel.TimestampSeconds <= 0 {
			t.Fatalf("expected positive timestamp, got %f", sel.TimestampSeconds)
		}
		if sel.TimestampSeconds <= prev {
			t.Fatalf("timestamps not increasing: %f <= %f", sel.TimestampSeconds, prev)
		}
		prev = sel.TimestampSeconds
	}
}

func TestSanitizeFilename(t *testing.T) {
	got := sanitizeFilename("My:File/Name")
	if got == "" || got == "My:File/Name" {
		t.Fatalf("expected sanitized filename, got %q", got)
	}
}

func TestBuildManualFrameSelections(t *testing.T) {
	selections := buildManualFrameSelections([]int{240, 480, 720}, 24)
	if len(selections) != 3 {
		t.Fatalf("expected 3 selections, got %d", len(selections))
	}
	for idx, selection := range selections {
		if selection.Index != idx {
			t.Fatalf("expected index %d, got %d", idx, selection.Index)
		}
		if selection.Frame != (idx+1)*240 {
			t.Fatalf("unexpected frame at %d: %#v", idx, selection)
		}
		if selection.Source != "manual" {
			t.Fatalf("expected manual source, got %#v", selection)
		}
	}
	if selections[1].TimestampSeconds != 20 {
		t.Fatalf("expected second timestamp 20, got %f", selections[1].TimestampSeconds)
	}
}

func TestMediaInfoVideoGeometryBuildsPARScaleFactors(t *testing.T) {
	var doc mediaInfoDoc
	payload := []byte(`{"media":{"track":[{"@type":"Video","Width":"720 pixels","Height":"576 pixels","PixelAspectRatio":"0.750","DisplayAspectRatio":"4:3"}]}}`)
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("unmarshal mediainfo: %v", err)
	}

	width, height, widthScale, heightScale := mediaInfoVideoGeometry(doc)
	if width != 720 || height != 576 {
		t.Fatalf("expected source dimensions 720x576, got %dx%d", width, height)
	}
	if widthScale != 1 {
		t.Fatalf("expected width scale 1, got %f", widthScale)
	}
	if heightScale < 0.9374 || heightScale > 0.9376 {
		t.Fatalf("expected height scale near 0.9375, got %f", heightScale)
	}
}

func TestMediaInfoVideoGeometryScalesWidthForWidePixels(t *testing.T) {
	var doc mediaInfoDoc
	payload := []byte(`{"media":{"track":[{"@type":"Video","Width":"720","Height":"480","PixelAspectRatio":"1.185","DisplayAspectRatio":"16:9"}]}}`)
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("unmarshal mediainfo: %v", err)
	}

	width, height, widthScale, heightScale := mediaInfoVideoGeometry(doc)
	if width != 720 || height != 480 {
		t.Fatalf("expected source dimensions 720x480, got %dx%d", width, height)
	}
	if widthScale != 1.185 || heightScale != 1 {
		t.Fatalf("expected width scale 1.185 and height scale 1, got %f x %f", widthScale, heightScale)
	}
}
