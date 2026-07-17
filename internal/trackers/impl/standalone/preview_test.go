// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package standalone

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildPreviewNormalizesStatusAndDefensivelyCopies(t *testing.T) {
	t.Parallel()

	spec := PreviewSpec{
		Tracker:          " dc ",
		BlockedReason:    "missing metadata",
		ReleaseName:      " Example.Release.2026.1080p-GRP ",
		DescriptionGroup: " DC ",
		Payload:          map[string]string{"name": "prepared"},
		Files:            []api.TrackerDryRunFile{{
Field: "file",
 Path: "example.torrent",
 Present: true,
}},
	}
	entry := BuildPreview(spec)
	spec.Payload["name"] = "mutated"
	spec.Files[0].Path = "mutated"
	if entry.Tracker != "DC" || entry.Status != "blocked" || entry.Message != "missing metadata" {
		t.Fatalf("preview identity/status = %#v", entry)
	}
	if entry.ReleaseName != "Example.Release.2026.1080p-GRP" || entry.DescriptionGroup != "dc" {
		t.Fatalf("preview projection = %#v", entry)
	}
	if entry.Payload["name"] != "prepared" || entry.Files[0].Path != "example.torrent" {
		t.Fatalf("preview retained caller mutation: %#v", entry)
	}
}
