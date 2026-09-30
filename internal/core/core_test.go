// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRenderDescriptionUsesPreviewImageSource(t *testing.T) {
	t.Parallel()
	full := "https://images.example.invalid/full.png"
	preview := "https://images.example.invalid/preview.png"
	rendered, err := (&Core{}).RenderDescription(context.Background(), "[url="+full+"][img]"+full+"[/img][/url]", map[string]string{full: preview})
	if err != nil || !strings.Contains(rendered, `src="`+preview+`"`) || !strings.Contains(rendered, `href="`+full+`"`) {
		t.Fatalf("rendered description = %q, err = %v", rendered, err)
	}
}

func TestBuildTrackerPreviewSanitizesStoredDescription(t *testing.T) {
	t.Parallel()
	records := []api.TrackerMetadata{{
		Tracker: "Example",
		Description: `<center><img src="https://images.example.invalid/poster.png" onerror="bad()"></center>` +
			`<a href="javascript:bad()">unsafe</a><p>Use [draft] &amp; review</p>`,
	}}
	previews := buildTrackerPreview(records, nil)
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

func TestBuildTrackerPreviewUsesStoredImagePreviews(t *testing.T) {
	t.Parallel()
	full := "https://images.example.invalid/full.png"
	previewURL := "https://images.example.invalid/preview.png"
	previews := buildTrackerPreview([]api.TrackerMetadata{{
		Tracker:       "Example",
		Description:   "[url=" + full + "][img]" + full + "[/img][/url]",
		ImageURLs:     []string{full},
		ImagePreviews: map[string]string{full: previewURL},
	}}, nil)
	if len(previews) != 1 || previews[0].ImagePreviews[full] != previewURL ||
		!strings.Contains(previews[0].DescriptionHTML, `src="`+previewURL+`"`) ||
		!strings.Contains(previews[0].DescriptionHTML, `href="`+full+`"`) {
		t.Fatalf("stored tracker preview = %#v", previews)
	}
}

func TestBuildTrackerPreviewUsesExactTorrentPages(t *testing.T) {
	t.Parallel()

	registry := trackerimpl.MustNewRegistry()
	for _, test := range []struct {
		name   string
		record api.TrackerMetadata
		want   string
	}{
		{
			name:   "Unit3D legacy ID",
			record: api.TrackerMetadata{Tracker: "BLU", TrackerID: "42"},
			want:   "https://blutopia.cc/torrents/42",
		},
		{name: "BHD legacy ID without verified page", record: api.TrackerMetadata{Tracker: "BHD", TrackerID: "42"}},
		{name: "HDB legacy ID without verified page", record: api.TrackerMetadata{Tracker: "HDB", TrackerID: "42"}},
		{
			name: "BHD exact page",
			record: api.TrackerMetadata{
				Tracker:    "BHD",
				TrackerID:  "42",
				TorrentURL: "https://beyond-hd.me/details/42",
			},
			want: "https://beyond-hd.me/details/42",
		},
		{
			name: "HDB exact page",
			record: api.TrackerMetadata{
				Tracker:    "HDB",
				TrackerID:  "42",
				TorrentURL: "https://hdbits.org/details.php?id=42",
			},
			want: "https://hdbits.org/details.php?id=42",
		},
		{
			name: "BTN exact page",
			record: api.TrackerMetadata{
				Tracker:    "BTN",
				TrackerID:  "42",
				TorrentURL: "https://broadcasthe.net/torrents.php?id=99&torrentid=42",
			},
			want: "https://broadcasthe.net/torrents.php?id=99&torrentid=42",
		},
		{
			name: "ANT exact page with working ID",
			record: api.TrackerMetadata{
				Tracker:    "ANT",
				TrackerID:  "1",
				TorrentURL: "https://anthelion.me/torrents.php?id=42",
			},
			want: "https://anthelion.me/torrents.php?id=42",
		},
		{name: "BTN ID alone", record: api.TrackerMetadata{Tracker: "BTN", TrackerID: "42"}},
		{name: "invalid stored URL", record: api.TrackerMetadata{Tracker: "ANT", TorrentURL: "javascript:alert(1)"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := buildTrackerPreview([]api.TrackerMetadata{test.record}, registry)
			if len(got) != 1 || got[0].TorrentURL != test.want {
				t.Fatalf("torrent URL = %#v, want %q", got, test.want)
			}
		})
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
			previews := buildTrackerPreview([]api.TrackerMetadata{{Tracker: "Example", Description: test.raw}}, nil)
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
