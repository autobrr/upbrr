// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func comparisonDescriptionFixture(t *testing.T) *api.ExactMediaAssets {
	t.Helper()
	root := t.TempDir()
	exact := &api.ExactMediaAssets{
		Comparisons: &api.ComparisonPlan{
			Imported:     true,
			MainSourceID: "2",
			Sources: []api.ComparisonSource{
				{
					ID:       "1",
					Label:    "Source HD",
					Imported: true,
				},
				{
					ID:       "2",
					Label:    "Encode",
					Imported: true,
				},
				{
					ID:       "3",
					Label:    "Filtered",
					Imported: true,
				},
			},
		},
		ComparisonImages:  make(map[api.PublicResourceID]api.ScreenshotImage),
		ComparisonUploads: make(map[api.PublicResourceID][]api.UploadedImageLink),
	}
	for rowIndex := 1; rowIndex <= 2; rowIndex++ {
		row := api.ComparisonRow{ID: strconv.Itoa(rowIndex)}
		for _, source := range []string{"3", "1", "2"} {
			id := api.PublicResourceID(fmt.Sprintf("row-%d-source-%s", rowIndex, source))
			path := filepath.Join(root, string(id)+".png")
			digest := strings.Repeat("a", 64)
			row.Cells = append(row.Cells, api.ComparisonCell{
				SourceID:      source,
				ArtifactID:    id,
				ContentSHA256: digest,
			})
			exact.ComparisonImages[id] = api.ScreenshotImage{
				Path:          path,
				Purpose:       api.ScreenshotPurposeComparison,
				ContentSHA256: digest,
			}
			exact.ComparisonUploads[id] = []api.UploadedImageLink{{
				ImagePath:     path,
				Purpose:       api.ScreenshotPurposeComparison,
				ContentSHA256: digest,
				Host:          "imgbb",
				UsageScope:    "global",
				RawURL:        "https://images.example.invalid/" + string(id) + ".png",
				ImgURL:        "https://images.example.invalid/thumb-" + string(id) + ".png",
				WebURL:        "https://images.example.invalid/page-" + string(id),
			}}
		}
		exact.Comparisons.Rows = append(exact.Comparisons.Rows, row)
	}
	fingerprint, err := comparisons.ReviewFingerprint(*exact.Comparisons)
	if err != nil {
		t.Fatal(err)
	}
	exact.Comparisons.Fingerprint = fingerprint
	exact.Comparisons.ApprovedFingerprint = fingerprint
	return exact
}

func TestComparisonDescriptionKeepsCompleteRowsInSourceOrder(t *testing.T) {
	t.Parallel()
	exact := comparisonDescriptionFixture(t)
	block, err := exactComparisonDescriptionBlock("ALPHA", exact)
	if err != nil || !strings.Contains(block, "[comparison=Source HD, Encode, Filtered]") {
		t.Fatalf("comparison labels = %q, err=%v", block, err)
	}
	_, body, _ := strings.Cut(block, "[comparison=Source HD, Encode, Filtered]")
	body, _, _ = strings.Cut(body, "[/comparison]")
	want := []string{
		"https://images.example.invalid/row-1-source-1.png",
		"https://images.example.invalid/row-1-source-2.png",
		"https://images.example.invalid/row-1-source-3.png",
		"https://images.example.invalid/row-2-source-1.png",
		"https://images.example.invalid/row-2-source-2.png",
		"https://images.example.invalid/row-2-source-3.png",
	}
	if got := strings.Fields(body); !slices.Equal(got, want) {
		t.Fatalf("comparison URL tokens = %q, want %q", got, want)
	}
	delete(exact.ComparisonUploads, "row-2-source-2")
	if _, err := exactComparisonDescriptionBlock("ALPHA", exact); err == nil {
		t.Fatal("incomplete host row was rendered")
	}
	exact.ImageHostUploadSkipped = true
	block, err = exactComparisonDescriptionBlock("ALPHA", exact)
	if err != nil || strings.Contains(block, "row-2-") || strings.Count(block, "https://images.example.invalid/") != 3 {
		t.Fatalf("hosting opt-out did not omit the whole unavailable row: %s, err=%v", block, err)
	}
	exact.Comparisons.Sources[0].Label = "Changed"
	if _, err := exactComparisonDescriptionBlock("ALPHA", exact); err == nil {
		t.Fatal("changed labels inherited previous image approval")
	}
}

func TestComparisonDescriptionUsesCompatibleHostedVariants(t *testing.T) {
	t.Parallel()
	exact := comparisonDescriptionFixture(t)
	for id, uploads := range exact.ComparisonUploads {
		link := uploads[0]
		link.Host = "pixhost"
		link.RawURL = "https://pixhost.example.invalid/" + string(id) + ".png"
		exact.ComparisonUploads[id] = append(uploads, link)
	}
	for _, test := range []struct {
		name, configured, wantDomain string
		policy                       *ImageHostPolicy
		failed                       []string
		trackerScoped                bool
	}{
		{"required host", "", "pixhost", &ImageHostPolicy{AllowedHosts: []string{"pixhost"}}, nil, false},
		{"failed optional host", "", "pixhost", nil, []string{" IMGBB "}, false},
		{"configured host has no complete scope", "pixhost", "images", &ImageHostPolicy{AllowedHosts: []string{"pixhost", "imgbb"}}, nil, false},
		{"complete tracker scope preferred to global", "", "pixhost", nil, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := exact.Clone()
			for id, uploads := range input.ComparisonUploads {
				if test.trackerScoped || test.configured != "" && strings.HasPrefix(string(id), "row-1-") {
					uploads[1].UsageScope = "tracker:ALPHA"
				}
			}
			registry := NewRegistry()
			if err := registry.RegisterDescriptor(Descriptor{
				Name:       "ALPHA",
				Definition: stubDefinition{name: "ALPHA"},
				ImageHost:  test.policy,
			}); err != nil {
				t.Fatal(err)
			}
			filtered, err := exactMediaForTrackerHost("ALPHA", api.UploadSubject{
				ExactMedia:         input,
				ImageHostOverrides: api.ImageHostOverrides{FailedHosts: test.failed},
			}, config.Config{}, config.TrackerConfig{ImageHost: test.configured}, registry)
			if err != nil {
				t.Fatal(err)
			}
			block, err := exactComparisonDescriptionBlock("ALPHA", filtered)
			if err != nil || strings.Count(block, "https://"+test.wantDomain+".example.invalid/") != 6 {
				t.Fatalf("comparison host policy changed row coverage: %q, err=%v", block, err)
			}
			if len(exact.ComparisonUploads["row-1-source-1"]) != 2 {
				t.Fatal("tracker selection mutated shared upload variants")
			}
		})
	}
}

func TestComparisonDescriptionReplacesOnlyItsOwnedSection(t *testing.T) {
	t.Parallel()
	const manual = "[comparison=Manual, Reference][img]https://images.example.invalid/manual.png[/img][/comparison]"
	const old = "[spoiler=source_comparisons][comparison=Old, Reference][img]https://images.example.invalid/old.png[/img][/comparison][/spoiler]"
	const literal = "[code]" + old + "[/code]"
	exact := comparisonDescriptionFixture(t)
	assets, err := ResolveDescriptionAssets(t.Context(), "ANT", api.UploadSubject{
		SourcePath:          filepath.Join(t.TempDir(), "Example.Movie.2026.mkv"),
		DescriptionOverride: manual + "\n" + literal + "\n" + old,
		ExactMedia:          exact,
	}, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
	if err != nil || !strings.Contains(assets.Description, manual) || !strings.Contains(assets.Description, literal) ||
		strings.Count(assets.Description, "https://images.example.invalid/old.png") != 1 ||
		strings.Count(assets.Description, "row-1-source-1.png") != 1 {
		t.Fatalf("comparison replacement touched unrelated content: %q, err=%v", assets.Description, err)
	}
}
