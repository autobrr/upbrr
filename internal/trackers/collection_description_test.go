// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDescriptionComparisonReplacementIsExactScopedAndFinalSafe(t *testing.T) {
	t.Parallel()
	block := "[comparison=Source A,Source B]https://images.example.test/a.png https://images.example.test/b.png[/comparison]"
	literals := []string{
		"[code]" + block + "[/code]", "<pre>" + block + "</pre>", "<code><code>" + block + "</code></code>",
		"<!--" + block + "-->", "<span data-example='" + block + "'>notes</span>", "<pre>" + block,
	}
	source := "Before\n" + block + "\nAfter\n" + strings.Join(literals, "\n")
	record := api.ImageCollection{
		Fingerprint:        "comparison-viewer",
		ContentFingerprint: "content",
		Host:               "slowpics",
		URL:                "https://slow.pics/c/SyntheticComparison",
		Purpose:            api.ScreenshotPurposeComparison,
		Category:           "Comps",
		Label:              "Source A vs Source B",
		AccountScope:       "anonymous",
		Completed:          true,
		Members:            []api.ImageCollectionMember{{Identity: "a", Label: "Source A"}, {Identity: "b", Label: "Source B"}},
	}
	registry := descriptionAssetsTestRegistry(t)
	for _, name := range []string{"owned", "other tracker", "skip without cache", "final", "analysis caption"} {
		t.Run(name, func(t *testing.T) {
			collection := record
			if name == "analysis caption" {
				collection.Label = api.ImageCollectionCategoryAnalysis
			}
			tracker := "ANT"
			if name == "other tracker" {
				tracker = "BTN"
			}
			exact := &api.ExactMediaAssets{
				CollectionHost: "slowpics",
				Collections:    []api.ImageCollection{collection},
				DescriptionComparisons: []api.DescriptionComparison{{
					BBCode:     block,
					Trackers:   []string{"ANT"},
					Collection: &collection,
				}},
			}
			if name == "skip without cache" {
				exact.DescriptionComparisons[0].Collection = nil
				exact.Collections = nil
			}
			meta := api.UploadSubject{
				SourcePath: filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv"),
				ExactMedia: exact,
				DescriptionGroups: []api.DescriptionBuilderGroup{{
					GroupKey:    "edited",
					Trackers:    []string{"ANT", "BTN"},
					Description: source,
					Final:       name == "final",
				}},
			}
			assets, err := ResolveDescriptionAssets(t.Context(), tracker, meta, nil, api.NopLogger{}, registry)
			if err != nil {
				t.Fatal(err)
			}
			if name == "owned" || name == "analysis caption" {
				if strings.Count(assets.Description, record.URL) != 1 || strings.Count(assets.Description, block) != len(literals) {
					t.Fatalf("owned replacement duplicated, lost literals, or left inline images: %s", assets.Description)
				}
				for _, literal := range literals {
					if !strings.Contains(assets.Description, literal) {
						t.Fatalf("literal comparison changed: %s", assets.Description)
					}
				}
			} else if assets.Description != source {
				t.Fatalf("unowned or final source changed: %s", assets.Description)
			}
		})
	}
}

func TestCollectionDescriptionsOnlyRenderSetLinks(t *testing.T) {
	record := api.ImageCollection{
		Fingerprint:        "set",
		ContentFingerprint: "content",
		Host:               "slowpics",
		URL:                "https://slow.pics/c/synthetic",
		Purpose:            api.ScreenshotPurposeComparison,
		Category:           "Comps",
		Label:              "Set A",
		AccountScope:       "anonymous",
		Completed:          true,
		Members:            []api.ImageCollectionMember{{Identity: "image", Label: "Image"}},
	}
	exact := &api.ExactMediaAssets{CollectionHost: "slowpics", Collections: []api.ImageCollection{record}}
	block, err := exactComparisonCollectionBlock(exact)
	if err != nil || strings.Count(block, "[url=") != 1 || strings.Contains(block, "[img]") {
		t.Fatalf("comparison block = %q %v", block, err)
	}
	record.Purpose, record.Category = api.ScreenshotPurposeAudioAnalysis, "Audio Plots"
	exact.Collections = []api.ImageCollection{record}
	exact.AudioTracks = []api.AudioDescriptionTrack{{
		Ordinal: 1,
		Images:  []api.ScreenshotImage{{Path: "unhosted", Purpose: api.ScreenshotPurposeAudioAnalysis}},
		Stats:   "Synthetic amplitude statistics",
	}}
	block, err = exactAudioDescriptionBlock("OPEN", exact)
	if err != nil || strings.Contains(block, "[url=") || strings.Contains(block, "[img]") || !strings.Contains(block, "Synthetic amplitude statistics") {
		t.Fatalf("audio block = %q %v", block, err)
	}
	links, err := exactAnalysisCollectionLinks(exact)
	if err != nil || links != "[url="+record.URL+"]HDR and audio data[/url]" {
		t.Fatalf("direct analysis links=%q err=%v", links, err)
	}
	record.Purpose, record.Category = api.ScreenshotPurposeHDRAnalysis, "Grade Checks"
	exact.Collections = []api.ImageCollection{record}
	exact.HDRPlots = []api.HDRDescriptionPlot{{Label: "Grade evidence"}}
	block, err = exactHDRDescriptionBlock("OPEN", exact)
	if err != nil || block != "" {
		t.Fatalf("HDR block = %q %v", block, err)
	}
	record.Label = "[img]unsafe"
	exact.Collections = []api.ImageCollection{record}
	if _, err = exactAnalysisCollectionLinks(exact); err == nil {
		t.Fatal("unsafe label rendered")
	}
}

func TestAnalysisCollectionLinkIsDirectAndReplacedOutsideOwnedExamples(t *testing.T) {
	t.Parallel()
	const oldLink = "[url=https://slow.pics/c/old]HDR and audio data[/url]"
	const currentLink = "[url=https://slow.pics/c/current]HDR and audio data[/url]"
	const comparisonBlock = "[spoiler=Comps]\n[url=https://slow.pics/c/comparison]Comparison[/url]\n[/spoiler]"
	const example = "[code]" + oldLink + "[/code]\n<pre>" + oldLink + "</pre>\n<code>" + oldLink + "</code>\n<!--" + oldLink +
		"-->\n<span data-example='" + oldLink + "'>notes</span>"
	exact := &api.ExactMediaAssets{
		CollectionHost: "slowpics",
		AudioAnalysis:  &api.AudioAnalysisRef{ID: "audio", Revision: 1},
		Collections: []api.ImageCollection{{
			Fingerprint:        "analysis",
			ContentFingerprint: "content",
			Host:               "slowpics",
			URL:                "https://slow.pics/c/current",
			Purpose:            api.ScreenshotPurposeHDRAnalysis,
			Category:           api.ImageCollectionCategoryAnalysis,
			Label:              api.ImageCollectionCategoryAnalysis,
			AccountScope:       "anonymous",
			Completed:          true,
			Members:            []api.ImageCollectionMember{{Identity: "image", Label: "Plot"}},
		}, {
			Fingerprint:        "comparison",
			ContentFingerprint: "comparison-content",
			Host:               "slowpics",
			URL:                "https://slow.pics/c/comparison",
			Purpose:            api.ScreenshotPurposeComparison,
			Category:           "Comps",
			Label:              "Comparison",
			AccountScope:       "anonymous",
			Completed:          true,
			Members:            []api.ImageCollectionMember{{Identity: "comparison-image", Label: "Source"}},
		}},
		ComparisonSets: []api.ImageCollectionInput{{
			Generation: "comparison",
			Purpose:    api.ScreenshotPurposeComparison,
			Category:   "Comps",
			Label:      "Comparison",
		}},
		AudioTracks: []api.AudioDescriptionTrack{{Ordinal: 1, Stats: "Synthetic amplitude statistics"}},
	}
	subject := api.UploadSubject{
		SourcePath:          filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv"),
		ExactMedia:          exact,
		DescriptionOverride: "Notes\n\n" + example + "\n\n" + oldLink,
	}
	registry := descriptionAssetsTestRegistry(t)
	for range 2 {
		assets, err := ResolveDescriptionAssets(t.Context(), "AITHER", subject, &stubRepo{}, api.NopLogger{}, registry)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(assets.Description, currentLink) != 1 || strings.Count(assets.Description, oldLink) != 5 ||
			!strings.Contains(assets.Description, example) || strings.Count(assets.Description, comparisonBlock) != 1 {
			t.Fatalf("links/examples changed: %q", assets.Description)
		}
		body, analysis := description.SplitTrailingSourceAudioSpoiler(assets.Description)
		if !strings.Contains(body, comparisonBlock) || strings.Contains(analysis, comparisonBlock) ||
			!strings.HasSuffix(analysis, "[/spoiler]\n\n"+currentLink) || !strings.Contains(analysis, "Synthetic amplitude statistics") ||
			strings.Contains(analysis, "[spoiler=HDR plots]") {
			t.Fatalf("direct analysis placement: body=%q analysis=%q", body, analysis)
		}
		subject.DescriptionOverride = assets.Description
	}
	subject.DescriptionGroupsFinal = true
	subject.DescriptionGroups = []api.DescriptionBuilderGroup{{Trackers: []string{"AITHER"}, RawDescription: subject.DescriptionOverride}}
	want := subject.DescriptionOverride
	subject.DescriptionOverride = ""
	assets, err := ResolveDescriptionAssets(t.Context(), "AITHER", subject, &stubRepo{}, api.NopLogger{}, registry)
	if err != nil || assets.Description != want {
		t.Fatalf("final text changed: %q err=%v", assets.Description, err)
	}
}

func TestResolveCollectionDescriptionPreservesUnownedComparisons(t *testing.T) {
	t.Parallel()
	const customBlock = "[spoiler=Comps]\nCustom comparison notes\n[url=https://example.invalid/custom-comparison]Custom set[/url]\n[/spoiler]"
	const currentBlock = "[spoiler=Comps]\n[url=https://slow.pics/c/synthetic]Set A[/url]\n[/spoiler]"
	const ordinaryBody = "Synthetic release notes"
	const flatBlock = "[spoiler=Comps]\n[img]https://images.example.invalid/comparison.png[/img]\n[/spoiler]"
	record := api.ImageCollection{
		Fingerprint:        "set",
		ContentFingerprint: "content",
		Host:               "slowpics",
		URL:                "https://slow.pics/c/synthetic",
		Purpose:            api.ScreenshotPurposeComparison,
		Category:           "Comps",
		Label:              "Set A",
		AccountScope:       "anonymous",
		Completed:          true,
		Members:            []api.ImageCollectionMember{{Identity: "image", Label: "Image"}},
	}
	for _, test := range []struct {
		name                   string
		host                   string
		noExact, noSets, final bool
		description            string
		wantCurrent            int
	}{
		{
			name:        "default without exact assets",
			noExact:     true,
			description: ordinaryBody + "\n\n" + customBlock,
		},
		{name: "unset collection host", description: ordinaryBody + "\n\n" + customBlock},
		{
			name:        "legacy flat host",
			noSets:      true,
			description: ordinaryBody + "\n\n" + customBlock + "\n\n" + flatBlock,
		},
		{
			name:        "no current comparison sets",
			host:        "slowpics",
			noSets:      true,
			description: ordinaryBody + "\n\n" + customBlock,
		},
		{
			name:        "mixed custom and current collection",
			host:        "slowpics",
			description: ordinaryBody + "\n\n" + customBlock + "\n\n" + currentBlock,
			wantCurrent: 1,
		},
		{
			name:        "repeated current block",
			host:        "slowpics",
			description: currentBlock + "\n\n" + ordinaryBody + "\n\n" + customBlock + "\n\n" + currentBlock,
			wantCurrent: 1,
		},
		{
			name:        "saved final override",
			host:        "slowpics",
			final:       true,
			description: currentBlock + "\n\n" + ordinaryBody + "\n\n" + customBlock + "\n\n" + currentBlock,
			wantCurrent: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			exact := &api.ExactMediaAssets{
				CollectionHost: test.host,
				Collections:    []api.ImageCollection{record},
				ComparisonSets: []api.ImageCollectionInput{{
					Generation: "generation",
					Purpose:    api.ScreenshotPurposeComparison,
					Category:   "Comps",
					Label:      "Set A",
				}},
			}
			if test.host == "" {
				exact.Collections = nil
			}
			if test.noSets {
				exact.ComparisonSets = nil
			}
			if test.noExact {
				exact = nil
			}
			subject := api.UploadSubject{
				SourcePath:          filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv"),
				DescriptionOverride: test.description,
				ExactMedia:          exact,
			}
			if test.final {
				subject.DescriptionOverride = ""
				subject.DescriptionGroupsFinal = true
				subject.DescriptionGroups = []api.DescriptionBuilderGroup{{
					GroupKey:    "unit3d",
					Trackers:    []string{"AITHER"},
					Description: test.description,
				}}
			}
			assets, err := ResolveDescriptionAssets(t.Context(), "AITHER", subject, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
			if err != nil {
				t.Fatalf("resolve collection description: %v", err)
			}
			if strings.Count(assets.Description, customBlock) != 1 || !strings.Contains(assets.Description, ordinaryBody) {
				t.Fatalf("unrelated custom comparison or surrounding notes changed: %q", assets.Description)
			}
			if got := strings.Count(assets.Description, currentBlock); got != test.wantCurrent {
				t.Fatalf("current collection block count=%d want=%d: %q", got, test.wantCurrent, assets.Description)
			}
			if test.wantCurrent == 0 || test.final {
				if assets.Description != test.description {
					t.Fatalf("untouched description changed: got %q want %q", assets.Description, test.description)
				}
			}
			if assets.Final != test.final || !assets.Override {
				t.Fatalf("final=%v override=%v; want final=%v override=true", assets.Final, assets.Override, test.final)
			}
			if len(assets.Screenshots) != 0 || len(assets.MenuImages) != 0 || len(assets.Slots) != 0 {
				t.Fatalf("comparison link entered ordinary screenshot assets: %#v", assets)
			}
		})
	}
}

func TestAnalysisLinkCleanupPreservesSameNamedComparison(t *testing.T) {
	t.Parallel()
	exact := &api.ExactMediaAssets{CollectionHost: "slowpics"}
	for _, purpose := range []api.ScreenshotPurpose{api.ScreenshotPurposeComparison, api.ScreenshotPurposeHDRAnalysis} {
		category := api.ImageCollectionCategoryAnalysis
		if purpose == api.ScreenshotPurposeComparison {
			category = "Comps"
		}
		exact.Collections = append(exact.Collections, api.ImageCollection{
			Fingerprint:        string(purpose),
			ContentFingerprint: string(purpose),
			Host:               "slowpics",
			URL:                "https://slow.pics/c/" + string(purpose),
			Purpose:            purpose,
			Category:           category,
			Label:              api.ImageCollectionCategoryAnalysis,
			AccountScope:       "anonymous",
			Completed:          true,
			Members:            []api.ImageCollectionMember{{Identity: "image", Label: "Plot"}},
		})
	}
	exact.ComparisonSets = []api.ImageCollectionInput{{
		Generation: "comparison",
		Purpose:    api.ScreenshotPurposeComparison,
		Category:   "Comps",
		Label:      api.ImageCollectionCategoryAnalysis,
	}}
	subject := api.UploadSubject{
		SourcePath:          filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv"),
		DescriptionOverride: "Notes",
		ExactMedia:          exact,
	}
	for range 2 {
		assets, err := ResolveDescriptionAssets(t.Context(), "AITHER", subject, &stubRepo{}, api.NopLogger{}, descriptionAssetsTestRegistry(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, collection := range exact.Collections {
			if strings.Count(assets.Description, collection.URL) != 1 {
				t.Fatalf("lost or duplicated collection: %q", assets.Description)
			}
		}
		subject.DescriptionOverride = assets.Description
	}
}
