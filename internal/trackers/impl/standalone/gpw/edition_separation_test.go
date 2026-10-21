// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package gpw

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestHybridRetainsRemasterTitle(t *testing.T) {
	subject := api.UploadSubject{Cut: "Director's Cut", ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid}}
	if got := remasterTitle(subject); got != "Director's Cut Hybrid" {
		t.Fatalf("remaster title=%q", got)
	}
	if subject.EditionLabel() != "Director's Cut" {
		t.Fatal("Hybrid entered canonical edition label")
	}
}
