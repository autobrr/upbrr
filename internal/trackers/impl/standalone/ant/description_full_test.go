// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTFullDescriptionKeepsContentAndSeparatesScreenshots(t *testing.T) {
	t.Parallel()
	const screenshot = "https://images.example.invalid/selected.png"
	const header = "[b]Screenshots[/b]"
	const notes = "[quote=Encoder]Keep these notes[/quote]\n[*]First note\n[*]Second note\nArtwork: [img]https://images.example.invalid/artwork.png[/img]\n[img]https://images.example.invalid/Selected.png[/img]\n[align=center][img]https://images.example.invalid/centered-artwork.png[/img][/align]"
	input := trackers.PreparationInput{
		Tracker: "ANT",
		Meta: api.UploadSubject{
			DescriptionTemplate: "[b]Template token[/b]",
			HDR:                 "HDR10",
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{Logo: "https://images.example.invalid/logo.png"},
			},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{
			Description: config.DescriptionSettingsConfig{
				CustomDescriptionHeader: "[b]Header token[/b]",
				ScreenshotHeader:        header,
				TonemappedHeader:        "[b]Tonemapped token[/b]",
				CustomSignature:         "[i]Signature token[/i]",
				AddLogo:                 true,
			},
			ScreenshotHandling: config.ScreenshotHandlingConfig{ToneMap: true},
		}),
		Assets: &trackers.DescriptionAssets{
			Description: notes + "\n\n" + header + "\n[center][img]" + screenshot + "[/img][/center]" +
				"\n\nCaption retained [url=https://images.example.invalid/page][img]" + screenshot + "[/img][/url]" +
				"\n\nThe configured " + header + " label is mentioned here.",
			Screenshots: []api.ScreenshotImage{{RawURL: screenshot}},
		},
	}
	result, err := prepareDescription(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{notes, "Template token", "Header token", "Signature token", "logo.png", "Caption retained", "The configured " + header + " label is mentioned here."} {
		if !strings.Contains(result.Description, want) {
			t.Errorf("description lost %q: %q", want, result.Description)
		}
	}
	if strings.Contains(result.Description, screenshot) || strings.Count(result.Description, header) != 1 {
		t.Errorf("screenshot section leaked into description: %q", result.Description)
	}
	if Profile().UploadContentMode != trackers.UploadContentModeDescription {
		t.Error("ANT must consume full shared descriptions")
	}

	dir := t.TempDir()
	input.Meta.TorrentPath = filepath.Join(dir, "Example.torrent")
	input.Meta.MediaInfoTextPath = filepath.Join(dir, "MEDIAINFO.txt")
	input.Meta.Identity = api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123}
	input.Meta.Source = "WEB-DL"
	input.TrackerConfig = config.TrackerConfig{APIKey: "test-key"}
	input.Projection = &api.TrackerReleaseProjection{
		TrackerID:         "ANT",
		Readiness:         api.ReadinessStatusReady,
		UploadReady:       true,
		UploadReleaseName: "Example.Movie.2026.1080p.WEB-DL-GRP",
	}
	for path, content := range map[string]string{input.Meta.TorrentPath: "torrent fixture", input.Meta.MediaInfoTextPath: "General\nVideo"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	input.Assets.Description = result.Description
	input.Assets.Final = true
	state, err := prepareUploadState(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if state.fields["release_desc"] != result.Description || state.fields["screenshots"] != screenshot {
		t.Fatalf("description/screenshot payload mismatch: description=%q screenshots=%q", state.fields["release_desc"], state.fields["screenshots"])
	}
}

func TestANTFullDescriptionIncludesConfiguredPartsWithoutNotes(t *testing.T) {
	t.Parallel()
	result, err := prepareDescription(t.Context(), trackers.PreparationInput{
		Meta: api.UploadSubject{
			DescriptionTemplate: "Template token",
			DiscType:            "DVD",
			DVDVOBMediaInfoText: "VOB MediaInfo token",
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{Description: config.DescriptionSettingsConfig{
			CustomDescriptionHeader: "Header token",
			CustomSignature:         "Signature token",
			ScreenshotHeader:        "Screenshots token",
		}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Template token", "Header token", "Signature token", "VOB MediaInfo token"} {
		if !strings.Contains(result.Description, want) {
			t.Errorf("missing %q: %q", want, result.Description)
		}
	}
	if strings.Contains(result.Description, "Screenshots token") {
		t.Fatalf("orphan screenshot header: %q", result.Description)
	}
}

func TestANTTonemappingDescriptionDoesNotInferAppliedConversion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, hdr                  string
		toneMap, screenshots, want bool
	}{
		{"unknown HDR screenshots", "HDR10", true, true, false},
		{"unknown DV screenshots", "DV", true, true, false},
		{"SDR", "", true, true, false},
		{"disabled", "HDR10", false, true, false},
		{"no screenshots", "HDR10", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assets := trackers.DescriptionAssets{Description: "Notes token"}
			if tc.screenshots {
				assets.Screenshots = []api.ScreenshotImage{{RawURL: "https://images.example.invalid/selected.png"}}
			}
			result, err := prepareDescription(t.Context(), trackers.PreparationInput{
				Meta: api.UploadSubject{HDR: tc.hdr},
				Runtime: trackers.PreparationRuntimeFromConfig(config.Config{
					Description:        config.DescriptionSettingsConfig{TonemappedHeader: "Tonemapped token"},
					ScreenshotHandling: config.ScreenshotHandlingConfig{ToneMap: tc.toneMap},
				}),
				Assets: &assets,
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(result.Description, "Tonemapped token") != tc.want {
				t.Fatalf("tonemapping label=%q, want present=%t", result.Description, tc.want)
			}
		})
	}
}

func TestANTDescriptionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := prepareDescription(ctx, trackers.PreparationInput{}); err == nil {
		t.Fatal("canceled description preparation succeeded")
	}
}

func TestANTFinalDescriptionRemainsAuthoritative(t *testing.T) {
	t.Parallel()
	const final = "[h1]Reviewed title[/h1]\n[img]https://images.example.invalid/selected.png[/img]\n" +
		"[right][url=https://github.com/autobrr/upbrr]Reviewed signature[/url][/right]"
	result, err := prepareDescription(t.Context(), trackers.PreparationInput{
		Meta: api.UploadSubject{DescriptionTemplate: "Must not be added"},
		Assets: &trackers.DescriptionAssets{
			Description: final,
			Final:       true,
			Screenshots: []api.ScreenshotImage{{RawURL: "https://images.example.invalid/selected.png"}},
		},
	})
	if err != nil || result.Description != final {
		t.Fatalf("reviewed description changed: description=%q err=%v", result.Description, err)
	}
}

func TestANTDescriptionPreservesUserTonemappingNotes(t *testing.T) {
	t.Parallel()
	const notes = "[b]These screenshots were tonemapped by the encoder[/b]"
	result, err := prepareDescription(t.Context(), trackers.PreparationInput{
		Assets: &trackers.DescriptionAssets{Description: notes},
	})
	if err != nil || !strings.Contains(result.Description, notes) {
		t.Fatalf("user tonemapping notes changed: description=%q err=%v", result.Description, err)
	}
}

func TestANTScreenshotHeaderRequiresOwnedSection(t *testing.T) {
	t.Parallel()
	const header = "[b]Screenshots[/b]"
	const image = "[center][img]https://images.example.invalid/selected.png[/img][/center]"
	for _, tc := range []struct {
		name, source, want string
	}{
		{"user heading", header + "\nThese are encoder notes.", header + "\nThese are encoder notes."},
		{"standalone user heading", header, header},
		{"adjacent owned gallery", "Notes\n\n" + header + "\n\n" + image, "Notes"},
		{"same-block owned gallery", "Notes\n\n" + header + "\n" + image, "Notes"},
		{"mixed user content", header + "\nThese are encoder notes.\n" + image, header + "\nThese are encoder notes."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := prepareDescriptionText(tc.source, header, []api.ScreenshotImage{{RawURL: "https://images.example.invalid/selected.png"}})
			if got != tc.want {
				t.Fatalf("description=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestANTDescriptionPreservesComparisonUsingSelectedImage(t *testing.T) {
	t.Parallel()
	const selected = "https://images.example.invalid/selected.png"
	const comparison = "[comparison=Source,Encode]\n[img]" + selected + "[/img]\n" +
		"[img]https://images.example.invalid/encode.png[/img]\n[/comparison]"
	const source = "Release notes\n\n" + comparison + "\n\n[b]Screenshots[/b]\n[center][img]" + selected + "[/img][/center]"
	got := prepareDescriptionText(source, "[b]Screenshots[/b]", []api.ScreenshotImage{{RawURL: selected}})
	if want := "Release notes\n\n" + comparison; got != want {
		t.Fatalf("comparison content changed: %q, want %q", got, want)
	}
}

func TestANTDescriptionPreservesLiteralScreenshotExamples(t *testing.T) {
	t.Parallel()
	const header = "[b]Screenshots[/b]"
	const selected = "https://images.example.invalid/selected.png"
	const gallery = header + "\n[center][img]" + selected + "[/img][/center]"
	const signature = "[right][url=https://github.com/autobrr/upbrr][size=4]Uploaded by upbrr[/size][/url][/right]"
	for _, tag := range []string{"code", "pre"} {
		t.Run(tag, func(t *testing.T) {
			literal := "[" + tag + "]first\n\n\n  indented line\n" + gallery + "\n" + signature + "[/" + tag + "]"
			result, err := prepareDescription(t.Context(), trackers.PreparationInput{
				Runtime: trackers.PreparationRuntimeFromConfig(config.Config{Description: config.DescriptionSettingsConfig{ScreenshotHeader: header}}),
				Assets: &trackers.DescriptionAssets{
					Description: literal + "\n\n" + gallery,
					Screenshots: []api.ScreenshotImage{{RawURL: selected}},
				},
			})
			if err != nil || result.Description != literal {
				t.Fatalf("literal content changed: description=%q err=%v, want %q", result.Description, err, literal)
			}
		})
	}
}

func TestANTDescriptionPreservesParagraphIndentation(t *testing.T) {
	t.Parallel()
	const notes = "Encoder notes\n\n  indented text\n  more text\n\nTail"
	if got := prepareDescriptionText(notes, "", nil); got != notes {
		t.Fatalf("paragraph indentation changed: %q, want %q", got, notes)
	}
}
