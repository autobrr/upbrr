// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package uploadinput

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveMaterializesDescriptionFileAndValidatesManualImages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	descriptionPath := filepath.Join(root, "description.txt")
	imagePath := filepath.Join(root, "comparison.png")
	if err := os.WriteFile(descriptionPath, []byte("Synthetic description."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\nsynthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	primary := 1
	request := api.CreateReleaseWorkflowUploadRequest{
		Descriptions: api.ReleaseWorkflowUploadDescriptions{
			Overrides: []api.ReleaseWorkflowUploadDescriptionOverride{{
				GroupKey: "default",
				File:     &descriptionPath,
			}},
		},
		Media: api.ReleaseWorkflowUploadMedia{
			Screenshots: api.ReleaseWorkflowUploadScreenshots{
				ComparisonPaths:        []string{imagePath},
				ComparisonPrimaryIndex: &primary,
			},
		},
	}
	_, resolved, err := Resolve(context.Background(), request)
	if err != nil {
		t.Fatalf("resolve upload inputs: %v", err)
	}
	override := resolved.Descriptions.Overrides[0]
	if override.Inline == nil || *override.Inline != "Synthetic description." ||
		override.File != nil || override.URL != nil {
		t.Fatalf("resolved description = %#v", override)
	}
	media, err := readMediaPaths(resolved.Media.Screenshots.ComparisonPaths, &primary)
	if err != nil {
		t.Fatalf("read manual media: %v", err)
	}
	if len(media) != 1 || media[0].Name != "comparison.png" ||
		media[0].ContentType != "image/png" || len(media[0].Bytes) == 0 {
		t.Fatalf("resolved manual media = %#v", media)
	}
}

func TestDescriptionURLRejectsPrivateAddress(t *testing.T) {
	t.Parallel()

	if _, err := validateDescriptionURL(context.Background(), "http://127.0.0.1/private"); err == nil {
		t.Fatal("private description URL was accepted")
	}
}

func TestReadMediaPathsRejectsInvalidPrimaryIndex(t *testing.T) {
	t.Parallel()

	index := 2
	if _, err := readMediaPaths([]string{`C:\synthetic\one.png`}, &index); err == nil {
		t.Fatal("out-of-range primary index was accepted")
	}
}

func TestComparisonCollectionsPreserveInputGroupsAndPrimaryOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	folder := filepath.Join(root, "Grade_Check")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(root, "Single.png")
	for _, name := range []string{one, filepath.Join(folder, "B.png"), filepath.Join(folder, "A.png")} {
		if err := os.WriteFile(name, []byte("\x89PNG\r\n\x1a\nsynthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	primary := 2
	media, err := readMediaPaths([]string{one, folder}, &primary)
	if err != nil {
		t.Fatal(err)
	}
	if len(media) != 3 || media[0].Name != "A.png" || media[1].Name != "B.png" || media[2].Name != "Single.png" {
		t.Fatalf("ordered members: %#v", media)
	}
	if media[0].ComparisonGroup != media[1].ComparisonGroup || media[1].ComparisonGroup == media[2].ComparisonGroup || media[0].ComparisonCount != 2 || media[2].ComparisonCount != 1 || media[0].ComparisonLabel != "Grade Check" {
		t.Fatalf("group authority: %#v", media)
	}
}

func TestComparisonCollectionsKeepSameBasenamesInDistinctGroups(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	firstFile := filepath.Join(root, "first-file", "Same.png")
	secondFile := filepath.Join(root, "second-file", "Same.png")
	firstFolder := filepath.Join(root, "first-folder", "Shared")
	secondFolder := filepath.Join(root, "second-folder", "Shared")
	paths := []string{
		firstFile, secondFile,
		filepath.Join(firstFolder, "B.png"), filepath.Join(firstFolder, "A.png"),
		filepath.Join(secondFolder, "C.png"), filepath.Join(secondFolder, "B.png"), filepath.Join(secondFolder, "A.png"),
	}
	contentByPath := make(map[string]string, len(paths))
	for index, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		content := "\x89PNG\r\n\x1a\nsynthetic-" + strconv.Itoa(index)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		contentByPath[path] = content
	}
	primary := 4
	media, err := readMediaPaths([]string{firstFile, firstFolder, secondFile, secondFolder}, &primary)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		filepath.Join(secondFolder, "A.png"), filepath.Join(secondFolder, "B.png"), filepath.Join(secondFolder, "C.png"),
		firstFile, filepath.Join(firstFolder, "A.png"), filepath.Join(firstFolder, "B.png"), secondFile,
	}
	if len(media) != len(wantPaths) {
		t.Fatalf("member count=%d want=%d", len(media), len(wantPaths))
	}
	groupStarts := []int{0, 3, 4, 6}
	groupSizes := []int{3, 1, 2, 1}
	seenGroups := make(map[string]bool, len(groupStarts))
	for index, start := range groupStarts {
		group := media[start].ComparisonGroup
		if group == "" || seenGroups[group] {
			t.Fatalf("same-basename inputs merged into group %q", group)
		}
		seenGroups[group] = true
		for offset := range groupSizes[index] {
			member := media[start+offset]
			if member.ComparisonGroup != group || member.ComparisonCount != groupSizes[index] {
				t.Fatalf("group size or ordered membership changed at %d: %+v", start+offset, member)
			}
		}
	}
	for index, path := range wantPaths {
		if media[index].Name != filepath.Base(path) || string(media[index].Bytes) != contentByPath[path] {
			t.Fatalf("same-basename member order changed at %d: %+v", index, media[index])
		}
	}
	if media[0].ComparisonLabel != "Shared" || media[4].ComparisonLabel != "Shared" ||
		media[3].ComparisonLabel != "Same.png" || media[6].ComparisonLabel != "Same.png" {
		t.Fatalf("distinct groups lost their shared display labels: %+v", media)
	}
}
