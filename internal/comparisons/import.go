// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package comparisons retains complete, reviewable rows across imported and captured images.
package comparisons

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register supported comparison-image decoders
	_ "image/png"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp" // register WebP comparison-image decoding

	"github.com/autobrr/upbrr/pkg/api"
)

// ImportedImage supplies owned image bytes and their retained artifact identity.
// Name is the base filename; explicit RowID, SourceID and Label replace legacy
// shot-group-label naming.
type ImportedImage struct {
	ID, Name, RowID, SourceID, Label string
	Bytes                            []byte
}

// Import maps prepared images without changing their bytes or inferring video
// facts. Invalid rows remain visible with correction problems; mainSourceID names
// a parsed column, never an index into the supplied paths or images.
func Import(images []ImportedImage, mainSourceID string) (api.ComparisonPlan, []api.ComparisonProblem) {
	plan := api.ComparisonPlan{
		Imported:     true,
		MainSourceID: NormalizeImportID(mainSourceID),
		Sources:      []api.ComparisonSource{},
		Rows:         []api.ComparisonRow{},
	}
	sources := map[string]int{}
	rows := map[string]int{}
	conflictingLabels := map[string]bool{}
	var problems []api.ComparisonProblem
	for index, input := range images {
		rowID, sourceID, label := comparisonImportMapping(input)
		if rowID == "" {
			rowID = "unmapped-" + input.ID
			if input.ID == "" {
				rowID += strconv.Itoa(index + 1)
			}
		}
		if sourceID != "" {
			if sourceIndex, exists := sources[sourceID]; !exists {
				sources[sourceID] = len(plan.Sources)
				plan.Sources = append(plan.Sources, api.ComparisonSource{
					ID:       sourceID,
					Label:    label,
					Imported: true,
				})
			} else if plan.Sources[sourceIndex].Label != label && !conflictingLabels[sourceID] {
				conflictingLabels[sourceID] = true
				plan.Sources[sourceIndex].Label = ""
				problems = append(problems, api.ComparisonProblem{
					RowID:    rowID,
					SourceID: sourceID,
					Message:  "Images disagree on this source label; choose one label for the column.",
				})
			}
		}
		rowIndex, exists := rows[rowID]
		if !exists {
			rowIndex = len(plan.Rows)
			rows[rowID] = rowIndex
			plan.Rows = append(plan.Rows, api.ComparisonRow{ID: rowID})
		}
		digest := sha256.Sum256(input.Bytes)
		cell := api.ComparisonCell{
			SourceID:      sourceID,
			ArtifactID:    api.PublicResourceID(input.ID),
			ContentSHA256: hex.EncodeToString(digest[:]),
		}
		if _, _, err := InspectImage(input.Bytes); err != nil {
			cell.ContentSHA256 = ""
			problems = append(problems, api.ComparisonProblem{
				RowID:    rowID,
				SourceID: sourceID,
				Message:  "Replace this image with a readable PNG, JPEG, or WebP image of at most 64 megapixels.",
			})
		}
		plan.Rows[rowIndex].Cells = append(plan.Rows[rowIndex].Cells, cell)
	}
	slices.SortStableFunc(plan.Sources, func(a, b api.ComparisonSource) int { return comparisonImportOrder(a.ID, b.ID) })
	slices.SortStableFunc(plan.Rows, func(a, b api.ComparisonRow) int { return comparisonImportOrder(a.ID, b.ID) })
	for i := range plan.Rows {
		slices.SortStableFunc(plan.Rows[i].Cells, func(a, b api.ComparisonCell) int { return comparisonImportOrder(a.SourceID, b.SourceID) })
	}
	plan.Preferences.Count = len(plan.Rows)
	problems = append(problems, Validate(plan)...)
	plan.Problems = problems
	return plan, problems
}

// InspectImage validates readable PNG, JPEG or WebP bytes before retention.
// It bounds decoded allocation to 64 megapixels and never rewrites the image.
func InspectImage(payload []byte) (image.Config, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		return image.Config{}, "", fmt.Errorf("inspect comparison image: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 64_000_000/config.Height {
		return image.Config{}, "", errors.New("comparison image must contain at most 64 megapixels")
	}
	if format != "png" && format != "jpeg" && format != "webp" {
		return image.Config{}, "", errors.New("comparison image must be PNG, JPEG or WebP")
	}
	_, _, err = image.Decode(bytes.NewReader(payload))
	if err != nil {
		return image.Config{}, "", fmt.Errorf("decode comparison image: %w", err)
	}
	return config, format, nil
}

func comparisonImportMapping(input ImportedImage) (rowID, sourceID, label string) {
	if input.RowID != "" || input.SourceID != "" || input.Label != "" {
		return NormalizeImportID(input.RowID), NormalizeImportID(input.SourceID), strings.TrimSpace(input.Label)
	}
	extension := filepath.Ext(input.Name)
	if !strings.EqualFold(extension, ".png") && !strings.EqualFold(extension, ".jpg") &&
		!strings.EqualFold(extension, ".jpeg") && !strings.EqualFold(extension, ".webp") {
		return "", "", ""
	}
	name := strings.TrimSuffix(input.Name, extension)
	row, rest, found := strings.Cut(name, "-")
	if !found {
		return "", "", ""
	}
	source, label, found := strings.Cut(rest, "-")
	if !found || !comparisonImportNumeric(row) || !comparisonImportNumeric(source) {
		return "", "", ""
	}
	return NormalizeImportID(row), NormalizeImportID(source), strings.TrimSpace(label)
}

// NormalizeImportID trims an explicit row or source ID and removes numeric zero
// padding. Non-numeric identities retain their spelling.
func NormalizeImportID(value string) string {
	value = strings.TrimSpace(value)
	if comparisonImportNumeric(value) {
		value = strings.TrimLeft(value, "0")
		if value == "" {
			return "0"
		}
	}
	return value
}

func comparisonImportNumeric(value string) bool {
	return value != "" && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func comparisonImportOrder(a, b string) int {
	aNumeric, bNumeric := comparisonImportNumeric(a), comparisonImportNumeric(b)
	if aNumeric != bNumeric {
		if aNumeric {
			return -1
		}
		return 1
	}
	if aNumeric && len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}
