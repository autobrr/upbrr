// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Command correctionchoicesgen generates Input suggestions from metadata naming
// dictionaries. Run it from the repository root, or pass -check to detect drift.
package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

var outputPath = filepath.Join("webui", "src", "pages", "input", "correctionChoices.json")

func main() {
	check := flag.Bool("check", false, "fail when generated Input correction choices differ")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(check bool) error {
	data, err := generate()
	if err != nil {
		return err
	}
	if check {
		current, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			return fmt.Errorf("read correction choices: %w", readErr)
		}
		if !bytes.Equal(current, data) {
			return errors.New("input correction choices are stale; run make correction-choices")
		}
		return nil
	}
	if err := os.WriteFile(outputPath, data, 0o600); err != nil {
		return fmt.Errorf("write correction choices: %w", err)
	}
	return nil
}

func generate() ([]byte, error) {
	choices, err := metadata.CorrectionChoices()
	if err != nil {
		return nil, fmt.Errorf("build correction choices: %w", err)
	}
	choices["Region"] = mergeCatalogChoices(choices["Region"], unit3d.RegionCodes())
	choices["Distributor"] = mergeCatalogChoices(nil, unit3d.DistributorNames())
	data, err := json.MarshalIndent(choices, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode correction choices: %w", err)
	}
	return append(data, '\n'), nil
}

func mergeCatalogChoices(choices []metadata.CorrectionChoice, values []string) []metadata.CorrectionChoice {
	known := make(map[string]bool, len(choices))
	for _, choice := range choices {
		known[choice.Value] = true
	}
	for _, value := range values {
		if !known[value] {
			choices = append(choices, metadata.CorrectionChoice{Value: value, Label: value})
		}
	}
	slices.SortFunc(choices, func(a, b metadata.CorrectionChoice) int { return cmp.Compare(a.Value, b.Value) })
	return choices
}
