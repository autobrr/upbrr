// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildTrackerPreviewSanitizesStoredDescription(t *testing.T) {
	t.Parallel()
	records := []api.TrackerMetadata{{
		Tracker: "Example",
		Description: `<center><img src="https://images.example.invalid/poster.png" onerror="bad()"></center>` +
			`<a href="javascript:bad()">unsafe</a><p>Use [draft] &amp; review</p>`,
	}}
	previews := buildTrackerPreview(records, config.Config{})
	if len(previews) != 1 || previews[0].Description != records[0].Description {
		t.Fatalf("expected raw stored description to remain available, got %#v", previews)
	}
	html := previews[0].DescriptionHTML
	if !strings.Contains(html, `<center><img src="https://images.example.invalid/poster.png" /></center>`) ||
		!strings.Contains(html, `<a>unsafe</a>`) ||
		!strings.Contains(html, `<p>Use [draft] &amp; review</p>`) ||
		strings.Contains(html, "onerror") || strings.Contains(html, "javascript:") {
		t.Fatalf("expected safe stored description preview, got %q", html)
	}
}

func TestBuildTrackerPreviewKeepsRawAndSanitizesDescriptionVariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		contains []string
		excludes []string
	}{
		{
			name:     "mixed HTML and linked BBCode image",
			raw:      `<p>Notes &amp; credits</p>[center][url=https://images.example.invalid/full][img=500]https://images.example.invalid/shot.png[/img][/url][/center]`,
			contains: []string{`<p>Notes &amp; credits</p>`, `text-align: center`, `width="500"`, `href="https://images.example.invalid/full"`},
			excludes: []string{`[center]`, `&amp;amp;`},
		},
		{
			name:     "malformed HTML and unsafe attributes",
			raw:      `<div><img src="javascript:bad()" onerror="bad()"><a href="javascript:bad()">unsafe`,
			contains: []string{`unsafe`},
			excludes: []string{`javascript:`, `onerror`, `<script`},
		},
		{
			name:     "encoded HTML",
			raw:      `&lt;center&gt;Encoded &amp; safe&lt;/center&gt;`,
			contains: []string{`Encoded &amp; safe`},
			excludes: []string{`&amp;amp;`, `<script`},
		},
		{
			name:     "HTML pre with BBCode outside",
			raw:      `<pre>[b]literal[/b]</pre>[b]outside[/b]`,
			contains: []string{`<pre>[b]literal[/b]</pre>`, `<b>outside</b>`},
			excludes: []string{`<pre><b>`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previews := buildTrackerPreview([]api.TrackerMetadata{{Tracker: "Example", Description: test.raw}}, config.Config{})
			if len(previews) != 1 || previews[0].Description != test.raw {
				t.Fatalf("raw description changed: %#v", previews)
			}
			for _, expected := range test.contains {
				if !strings.Contains(previews[0].DescriptionHTML, expected) {
					t.Errorf("preview missing %q: %q", expected, previews[0].DescriptionHTML)
				}
			}
			for _, forbidden := range test.excludes {
				if strings.Contains(previews[0].DescriptionHTML, forbidden) {
					t.Errorf("preview retained %q: %q", forbidden, previews[0].DescriptionHTML)
				}
			}
		})
	}
}

func TestApplyMetadataDefaults(t *testing.T) {
	t.Parallel()

	configured := applyMetadataDefaults(api.PrepareInput{}, config.MetadataConfig{
		SkipAutoTorrent: true,
		KeepImages:      true,
		OnlyID:          true,
	})
	if !configured.Search.Skip || !configured.Policy.KeepImages || !configured.Policy.OnlyID {
		t.Fatalf("configured metadata defaults were not applied: %#v", configured)
	}

	requested := applyMetadataDefaults(api.PrepareInput{
		Search: api.ClientSearchPolicy{Skip: true},
		Policy: api.PreparationPolicy{KeepImages: true, OnlyID: true},
	}, config.MetadataConfig{})
	if !requested.Search.Skip || !requested.Policy.KeepImages || !requested.Policy.OnlyID {
		t.Fatalf("request metadata options were not preserved: %#v", requested)
	}
}

func TestApplyContinuationPreparationDefaultsUsesActiveConfigEachTime(t *testing.T) {
	t.Parallel()

	previous := api.ContinueReleaseWorkflowRequest{Intent: api.WorkflowIntent{
		Preparation: &api.PrepareInput{Policy: api.PreparationPolicy{OnlyID: true}},
	}}
	previous = applyContinuationPreparationDefaults(previous, config.MetadataConfig{})
	if previous.Intent.Preparation == nil || !previous.Intent.Preparation.Policy.OnlyID {
		t.Fatalf("previous explicit only-id input = %#v", previous.Intent.Preparation)
	}

	explicitFalse := api.ContinueReleaseWorkflowRequest{Intent: api.WorkflowIntent{
		Preparation: &api.PrepareInput{},
	}}
	explicitFalse = applyContinuationPreparationDefaults(explicitFalse, config.MetadataConfig{})
	if explicitFalse.Intent.Preparation == nil || explicitFalse.Intent.Preparation.Policy.OnlyID {
		t.Fatalf("explicit only-id false was retained from prior request: %#v", explicitFalse.Intent.Preparation)
	}

	configured := config.MetadataConfig{OnlyID: true}
	first := applyContinuationPreparationDefaults(explicitFalse, configured)
	second := applyContinuationPreparationDefaults(explicitFalse, configured)
	if first.Intent.Preparation == nil || second.Intent.Preparation == nil ||
		!first.Intent.Preparation.Policy.OnlyID || !second.Intent.Preparation.Policy.OnlyID {
		t.Fatalf("configured only-id default was not applied consistently: %#v, %#v", first.Intent.Preparation, second.Intent.Preparation)
	}
}

func TestWorkflowPrivateVaultRootIsDatabaseScoped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	firstDB := filepath.Join(root, "first.db")
	secondDB := filepath.Join(root, "second.db")
	first := workflowPrivateVaultRoot(firstDB)
	if first != workflowPrivateVaultRoot(firstDB) {
		t.Fatal("same database path produced unstable private vault root")
	}
	if first == workflowPrivateVaultRoot(secondDB) {
		t.Fatal("distinct databases in one directory shared a private vault root")
	}
}
