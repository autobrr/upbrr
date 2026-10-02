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

func TestAudioAnalysisFollowsEvidenceAndMenus(t *testing.T) {
	t.Parallel()
	got, err := buildDescription(t.Context(), oeTestSubject(), config.Config{}, config.TrackerConfig{}, api.NopLogger{},
		"Notes\n\n[spoiler=source_audio]\n[img]https://images.example.invalid/audio.png[/img]\n[/spoiler]",
		[]api.ScreenshotImage{{
			RawURL: "https://images.example.invalid/menu.png",
			ImgURL: "https://images.example.invalid/menu.png",
			WebURL: "https://images.example.invalid/menu",
		}},
		oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(got, "SVT-AV1 preset=4") >= strings.Index(got, "menu.png") ||
		strings.Index(got, "menu.png") >= strings.Index(got, "audio.png") ||
		strings.Index(got, "audio.png") >= strings.Index(got, "one-thumb.png") ||
		!strings.Contains(got, "[img=350]https://images.example.invalid/audio.png[/img]") {
		t.Fatalf("audio analysis placement = %q", got)
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
		"[url=https://images.example/one][img=350]https://images.example/one-thumb.png[/img][/url]",
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

func TestDescriptionKeepsExistingLinkedScreenshotWithoutDuplicatingIt(t *testing.T) {
	screenshots := oeTestScreenshots()
	screenshots[1].ImgURL = "https://images.example/two-thumb.png"
	linkedRaw := "[url=" + screenshots[0].WebURL + "][img=300]" + screenshots[0].RawURL + "[/img][/url]"
	kept := "[center]Existing screenshot: " + linkedRaw + "[/center]"
	got, err := buildDescription(t.Context(), oeTestSubject(), config.Config{}, config.TrackerConfig{}, api.NopLogger{}, kept, nil, screenshots)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, linkedRaw) != 1 || strings.Contains(got, screenshots[0].ImgURL) {
		t.Fatalf("existing linked screenshot was duplicated: %s", got)
	}
	if !strings.Contains(got, "[img=350]"+screenshots[1].ImgURL+"[/img]") {
		t.Fatalf("new screenshot did not use its hosted thumbnail: %s", got)
	}
}

func TestDescriptionPreservesImportedComparisonMarkup(t *testing.T) {
	comparison := "[comparison=Source,Encode]\r\nhttps://images.example/source.png https://images.example/encode.png\r\n[/comparison]"
	meta := oeTestSubject()
	meta.TrackerData = []api.TrackerMetadata{{Tracker: "OE", Description: comparison}}
	got, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{},
		"[pre]Notes[/pre]\n\n"+comparison, nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, comparison) || !strings.Contains(got, "[code]Notes[/code]") {
		t.Fatalf("OE changed imported comparison or skipped surrounding cleanup: %q", got)
	}
	meta.TrackerData = nil
	got, err = buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{},
		"[pre]Notes[/pre]\n\n"+comparison, nil, oeTestScreenshots())
	if err != nil || !strings.Contains(got, comparison) {
		t.Fatalf("OE changed DB-only comparison: description=%q err=%v", got, err)
	}
}

func TestDescriptionPreservesComparisonEvidenceMarkup(t *testing.T) {
	comparison := "[spoiler=Comparisons]\r\n[b]Source Notes[/b][code]source comparison notes[/code]\r\n[/spoiler]"
	templateComparison := "[comparison=Source,Encode]\r\n[b]Source Notes[/b][code]template comparison notes[/code]\r\n[/comparison]"
	meta := oeTestSubject()
	meta.DescriptionTemplate = strings.Join([]string{"[pre]Template[/pre]", templateComparison}, "\n\n")
	got, err := buildDescription(t.Context(), meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{},
		comparison+"\n\n[b]Source Notes[/b][code]stale notes[/code]", nil, oeTestScreenshots())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, comparison) || !strings.Contains(got, templateComparison) ||
		!strings.Contains(got, "[code]Template[/code]") || strings.Contains(got, "stale notes") ||
		!strings.Contains(got, "[b]Source Notes[/b]\n[code]Example BluRay source; original HDR10 only[/code]") {
		t.Fatalf("OE changed comparison evidence or kept stale notes: %q", got)
	}
}

func TestAppendMissingScreenshotIgnoresComparisonLink(t *testing.T) {
	screenshot := api.ScreenshotImage{
		WebURL: "https://img.example/full",
		RawURL: "https://img.example/full.png",
	}
	comparison := "[spoiler=Comparisons][url=" + screenshot.WebURL + "][img]" + screenshot.RawURL + "[/img][/url][/spoiler]"
	description := oeAppendMissingScreenshotLinks(comparison, []api.ScreenshotImage{screenshot}, 350)
	if !strings.Contains(description, comparison) || strings.Count(description, screenshot.RawURL) != 2 {
		t.Fatalf("comparison link satisfied separately selected screenshot: %q", description)
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
