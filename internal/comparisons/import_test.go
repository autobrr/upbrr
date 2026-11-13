// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package comparisons

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func comparisonImportImages(t *testing.T) []ImportedImage {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.NRGBA{R: 120, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	images := make([]ImportedImage, 0, 6)
	for _, row := range []string{"10", "02"} {
		for source, label := range []string{"source.v1", "encode", "filtered"} {
			id := fmt.Sprintf("%s-%02d", row, source+1)
			images = append(images, ImportedImage{
				ID:    id,
				Name:  id + "-" + label + ".png",
				Bytes: slices.Clone(encoded.Bytes()),
			})
		}
	}
	return images
}

func TestImportPairsCompleteRowsByNumericIdentity(t *testing.T) {
	images := comparisonImportImages(t)
	original := slices.Clone(images[0].Bytes)
	plan, problems := Import(images, "02")
	if len(problems) != 0 || plan.MainSourceID != "2" || !plan.Imported || len(plan.Sources) != 3 || len(plan.Rows) != 2 {
		t.Fatalf("import = %+v, problems = %+v", plan, problems)
	}
	if plan.Rows[0].ID != "2" || plan.Rows[1].ID != "10" || plan.Rows[0].Cells[1].ArtifactID != "02-02" ||
		plan.Rows[1].Cells[1].ArtifactID != "10-02" {
		t.Fatalf("images paired by list position instead of shot/group identity: %+v", plan.Rows)
	}
	for i, source := range plan.Sources {
		if source.ID != strconv.Itoa(i+1) || !source.Imported || source.FrameRate != 0 || source.DurationSeconds != 0 {
			t.Fatalf("unexpected source identity or inferred video facts: %+v", source)
		}
	}
	for _, row := range plan.Rows {
		for _, cell := range row.Cells {
			if cell.TimestampSeconds != nil || cell.FrameNumber != nil || cell.PictureType != "" || cell.Confidence != nil {
				t.Fatalf("import invented video evidence: %+v", cell)
			}
			if cell.ContentSHA256 != fmt.Sprintf("%x", sha256.Sum256(original)) {
				t.Fatal("import did not retain the actual image digest")
			}
		}
	}
	if !bytes.Equal(images[0].Bytes, original) {
		t.Fatal("import changed supplied image bytes")
	}
}

func TestImportProblemsRemainExplicit(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func([]ImportedImage) []ImportedImage
	}{
		{"duplicate cell", func(images []ImportedImage) []ImportedImage { return append(images, images[0]) }},
		{"missing cell", func(images []ImportedImage) []ImportedImage { return images[1:] }},
		{"unmapped image", func(images []ImportedImage) []ImportedImage { images[0].Name = "custom.png"; return images }},
		{"conflicting labels", func(images []ImportedImage) []ImportedImage { images[0].Name = "10-01-other.png"; return images }},
		{"unreadable image", func(images []ImportedImage) []ImportedImage { images[0].Bytes = []byte("not an image"); return images }},
		{"oversized decoded image", func(images []ImportedImage) []ImportedImage {
			payload := images[0].Bytes
			binary.BigEndian.PutUint32(payload[16:20], 100_000)
			binary.BigEndian.PutUint32(payload[20:24], 100_000)
			binary.BigEndian.PutUint32(payload[29:33], crc32.ChecksumIEEE(payload[12:29]))
			return images
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, problems := Import(test.change(comparisonImportImages(t)), "2")
			if len(problems) == 0 || len(plan.Problems) == 0 {
				t.Fatalf("invalid import became ready: %+v", plan)
			}
			if test.name == "missing cell" {
				if len(plan.Rows) != 2 || len(plan.Rows[1].Cells) != 2 {
					t.Fatalf("incomplete row was silently truncated: %+v", plan.Rows)
				}
				plan.Rows[1].Excluded = true
				if remaining := Validate(plan); len(remaining) != 0 {
					t.Fatalf("explicit row exclusion could not resolve missing cells: %+v", remaining)
				}
			}
		})
	}
	plan, problems := Import(comparisonImportImages(t), "")
	if plan.MainSourceID != "" || !slices.ContainsFunc(problems, func(problem api.ComparisonProblem) bool { return problem.RowID == "" && problem.SourceID == "" }) {
		t.Fatal("missing main-column choice was silently guessed")
	}
}

func TestImportAcceptsExplicitMappingWithoutVideoFacts(t *testing.T) {
	images := comparisonImportImages(t)[:2]
	for i := range images {
		images[i].Name = "custom.png"
		images[i].RowID = "scene-a"
		images[i].SourceID = []string{"original", "comparison"}[i]
		images[i].Label = []string{"Source", "Encode"}[i]
	}
	plan, problems := Import(images, "comparison")
	if len(problems) != 0 || len(plan.Rows) != 1 || plan.Rows[0].ID != "scene-a" || plan.MainSourceID != "comparison" {
		t.Fatalf("explicit mapping failed: %+v, %+v", plan, problems)
	}
	plan.Rows[0].Cells[0].TimestampSeconds = new(1.0)
	if len(Validate(plan)) == 0 {
		t.Fatal("imported pixels acquired invented timing evidence")
	}
}
