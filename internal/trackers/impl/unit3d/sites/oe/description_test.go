// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func oeTestScreenshots() []api.ScreenshotImage {
	return []api.ScreenshotImage{
		{
RawURL: "https://images.example/one.png",
 ImgURL: "https://images.example/one-thumb.png",
 WebURL: "https://images.example/one",
},
		{RawURL: "https://images.example/two.png"},
		{ImgURL: "https://images.example/three.png"},
	}
}

func oeTestSubject() api.UploadSubject {
	return api.UploadSubject{
		Type:       "ENCODE",
		VideoCodec: "AV1",
		Tag:        "-SM737",
		TrackerQuestionnaireAnswers: map[string]map[string]string{"OE": {
			oeEncodingSettingsKey: "SVT-AV1 preset=4 crf=20",
			oeSourceNotesKey:      "Example BluRay source; original HDR10 only",
		}},
	}
}

func TestDescriptionOwnsOEMarkupEvidenceAndScreenshots(t *testing.T) {
	meta := oeTestSubject()
	meta.ProviderMetadata = api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Logo: "https://images.example/title-logo.png"}}
	meta.DescriptionTemplate = "[hide=Template][pre]template notes[/pre][/hide]"
	cfg := config.Config{}
	cfg.Description.AddLogo = true
	cfg.Description.ThumbnailSize = 900
	const body = "[align=left][hide=FraMeSToR NFO:][pre]release notes[/pre][/hide][/align]\n" +
		"[comparison=Source,Encode]https://images.example/source.png https://images.example/encode.png[/comparison]\n" +
		"[img]https://images.example/one.png[/img]\n" +
		"[right][url=https://github.com/autobrr/upbrr]Uploaded by upbrr[/url][/right]"
	got, err := buildDescription(t.Context(), meta, cfg, config.TrackerConfig{}, api.NopLogger{}, body, nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[align=left][spoiler=FraMeSToR NFO:][code]release notes[/code][/spoiler][/align]",
		"[spoiler=Template][code]template notes[/code][/spoiler]",
		"SVT-AV1 preset=4 crf=20", "Example BluRay source; original HDR10 only",
		"https://images.example/source.png", "https://images.example/encode.png",
		"[url=https://images.example/one][img=350]https://images.example/one.png[/img][/url]",
		"[url=https://images.example/two.png][img=350]https://images.example/two.png[/img][/url]",
		"[url=https://images.example/three.png][img=350]https://images.example/three.png[/img][/url]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from description: %s", want, got)
		}
	}
	if strings.Contains(got, "title-logo") || strings.Contains(got, "[img=900]") || strings.Count(got, "Uploaded by upbrr") != 1 {
		t.Fatalf("invalid OE logo, image sizing or signature: %s", got)
	}
	if !cfg.Description.AddLogo || cfg.Description.ThumbnailSize != 900 {
		t.Fatal("OE changed shared description config")
	}
	meta.DescriptionOverride = got
	meta.DescriptionGroupsFinal = true
	failures, err := checkDescriptionRequirements(t.Context(), api.NewTrackerValidationSubject(meta, "OE"), api.NopLogger{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("composed description must pass final validation: %v %v", failures, err)
	}
}

func TestDescriptionRequiresThreeDistinctRenderableScreenshots(t *testing.T) {
	for name, screenshots := range map[string][]api.ScreenshotImage{
		"missing":       nil,
		"two":           oeTestScreenshots()[:2],
		"duplicates":    {oeTestScreenshots()[0], oeTestScreenshots()[0], oeTestScreenshots()[1]},
		"web page only": {{WebURL: "https://images.example/page"}, oeTestScreenshots()[0], oeTestScreenshots()[1]},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildDescription(t.Context(), api.UploadSubject{}, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "notes", nil, screenshots)
			if err == nil {
				t.Fatal("incomplete screenshot evidence was accepted")
			}
		})
	}
}

func TestDescriptionRequiresConditionalEvidence(t *testing.T) {
	for _, key := range []string{oeEncodingSettingsKey, oeSourceNotesKey} {
		t.Run(key, func(t *testing.T) {
			meta := oeTestSubject()
			delete(meta.TrackerQuestionnaireAnswers["OE"], key)
			_, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "notes", nil, oeTestScreenshots())
			if err == nil {
				t.Fatal("missing description evidence was accepted")
			}
		})
	}
}

func TestDescriptionMultilineEvidenceSurvivesFinalValidation(t *testing.T) {
	meta := oeTestSubject()
	meta.TrackerQuestionnaireAnswers["OE"][oeEncodingSettingsKey] = "SVT-AV1 preset=4\r\n\r\n\r\ncrf=20"
	meta.TrackerQuestionnaireAnswers["OE"][oeSourceNotesKey] = "Example BluRay source\n\n\nOriginal HDR10 only"
	description, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "notes", nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	meta.DescriptionGroupsFinal = true
	meta.DescriptionOverride = description
	failures, err := checkDescriptionRequirements(t.Context(), api.NewTrackerValidationSubject(meta, "OE"), api.NopLogger{})
	if err != nil || len(failures) != 0 {
		t.Fatalf("generated multiline evidence failed final validation: %+v %v", failures, err)
	}
}

func TestDescriptionRegenerationReplacesEvidenceAndScreenshots(t *testing.T) {
	meta := oeTestSubject()
	const notes = "User introduction\n[comparison=Source,Encode]https://images.example/source.png https://images.example/encode.png[/comparison]"
	first, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, notes, nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, first, nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{oeEncodingSettingsLabel, oeSourceNotesLabel} {
		if count := strings.Count(rebuilt, "[b]"+label+"[/b]"); count != 1 {
			t.Errorf("regeneration produced %d %s blocks", count, label)
		}
	}
	meta.TrackerQuestionnaireAnswers["OE"][oeEncodingSettingsKey] = "SVT-AV1 preset=5 crf=21"
	meta.TrackerQuestionnaireAnswers["OE"][oeSourceNotesKey] = "Corrected BluRay source notes"
	current := oeTestScreenshots()
	for index := range current {
		current[index].RawURL = strings.ReplaceAll(current[index].RawURL, ".example/", ".example/current-")
		current[index].ImgURL = strings.ReplaceAll(current[index].ImgURL, ".example/", ".example/current-")
		current[index].WebURL = strings.ReplaceAll(current[index].WebURL, ".example/", ".example/current-")
	}
	rebuilt, err = buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, rebuilt, nil, current)
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"preset=4", "original HDR10 only", "https://images.example/one.png", "https://images.example/two.png", "https://images.example/three.png"} {
		if strings.Contains(rebuilt, stale) {
			t.Errorf("regeneration retained stale content %q: %s", stale, rebuilt)
		}
	}
	for _, want := range []string{"User introduction", "preset=5 crf=21", "Corrected BluRay source notes", "https://images.example/source.png", "https://images.example/encode.png", "https://images.example/current-two.png"} {
		if !strings.Contains(rebuilt, want) {
			t.Errorf("regeneration lost %q: %s", want, rebuilt)
		}
	}
}
