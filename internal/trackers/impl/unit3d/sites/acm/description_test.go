// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package acm

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDescriptionUsesOnlyACMMarkupTransforms(t *testing.T) {
	const body = "[center][spoiler=Scene NFO:][code]scene nfo[/code][/spoiler][/center]\n" +
		"[right][url=https://github.com/autobrr/upbrr][size=4]Uploaded by upbrr[/size][/url][/right]\n" +
		"[align=left][hide=FraMeSToR NFO:][pre]release notes[/pre][/hide][/align]\n" +
		"[comparison=Source,Encode]https://images.example/source.png https://images.example/encode.png[/comparison]"
	got, err := buildACMDescription(t.Context(), api.UploadSubject{}, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, body, nil,
		[]api.ScreenshotImage{{RawURL: "https://images.example/screen.png", ImgURL: "https://images.example/thumb.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "scene nfo") {
		t.Fatalf("ACM scene NFO cleanup was skipped: %q", got)
	}
	if strings.Count(got, "Uploaded by upbrr") != 1 {
		t.Fatalf("ACM must replace the imported signature: %q", got)
	}
	for _, want := range []string{
		"[align=left][spoiler=FraMeSToR NFO:][code]release notes[/code][/spoiler][/align]",
		"[spoiler=Source vs Encode]",
		"https://images.example/source.png",
		"https://images.example/encode.png",
		"https://images.example/thumb.png",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing tracker-owned markup %q: %q", want, got)
		}
	}
}

func TestDescriptionTransformsTemplateAndReplacesScreenshots(t *testing.T) {
	meta := api.UploadSubject{DescriptionTemplate: "[center][spoiler=Scene NFO:][code]stale scene nfo[/code][/spoiler][/center]\n" +
		"[align=left][hide=FraMeSToR NFO:][pre]template notes[/pre][/hide][/align]\n" +
		"[comparison=Source,Encode]https://images.example/source.png https://images.example/encode.png[/comparison]"}
	oldScreenshots := []api.ScreenshotImage{
		{RawURL: "https://images.example/old-one.png"},
		{RawURL: "https://images.example/old-two.png"},
		{RawURL: "https://images.example/old-three.png"},
	}
	first, err := buildACMDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "User notes", nil, oldScreenshots)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, "stale scene nfo") || !strings.Contains(first, "[spoiler=FraMeSToR NFO:][code]template notes[/code][/spoiler]") {
		t.Errorf("template bypassed ACM transforms: %s", first)
	}
	meta.DescriptionTemplate = ""
	current := []api.ScreenshotImage{{RawURL: "https://images.example/current.png"}}
	rebuilt, err := buildACMDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, first, nil, current)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rebuilt, "https://images.example/old-") {
		t.Errorf("regeneration retained obsolete screenshots: %s", rebuilt)
	}
	for _, want := range []string{"User notes", "template notes", "[spoiler=Source vs Encode]", "https://images.example/source.png", "https://images.example/encode.png", "https://images.example/current.png"} {
		if !strings.Contains(rebuilt, want) {
			t.Errorf("regeneration lost %q: %s", want, rebuilt)
		}
	}
}
