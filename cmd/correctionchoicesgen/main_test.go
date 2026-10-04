// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

func TestGeneratedCorrectionChoicesCurrent(t *testing.T) {
	t.Parallel()
	want, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(filepath.Join("..", "..", outputPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, want) {
		t.Fatal("Input correction choices are stale; run make correction-choices")
	}
}

func TestGeneratedDiscCatalogChoices(t *testing.T) {
	t.Parallel()
	data, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	var choices map[string][]metadata.CorrectionChoice
	if err := json.Unmarshal(data, &choices); err != nil {
		t.Fatal(err)
	}
	publishers := choices["Distributor"]
	if len(publishers) != len(unit3d.DistributorNames()) {
		t.Fatal("incomplete distributor suggestions")
	}
	for _, choice := range publishers {
		if choice.Label != choice.Value || unit3d.DistributorID(choice.Value) == "" {
			t.Fatalf("invalid publisher choice %q", choice.Value)
		}
	}
	regions := make(map[string]string)
	for _, choice := range choices["Region"] {
		regions[choice.Value] = choice.Label
	}
	for _, code := range unit3d.RegionCodes() {
		if regions[code] == "" {
			t.Fatalf("missing shared region %q", code)
		}
	}
	if regions["GBR"] != "United Kingdom (GBR)" {
		t.Fatal("lost existing full region label")
	}
}
