// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/paths"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildUnit3DDescriptionTemplate(t *testing.T) {
	meta := api.PreparedMetadata{DescriptionTemplate: "  Example Template  "}
	cfg := config.Config{}
	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Example Template") {
		t.Fatalf("expected template to be used, got %q", result)
	}
	if !strings.Contains(result, "Uploaded by upbrr") {
		t.Fatalf("expected signature in description, got %q", result)
	}
}

func TestBuildUnit3DDescriptionKeptAppendsScreenshots(t *testing.T) {
	meta := api.PreparedMetadata{}
	cfg := config.Config{Description: config.DescriptionSettingsConfig{ThumbnailSize: 350}}
	kept := "Kept Description"
	screens := []api.ScreenshotImage{{ImgURL: "https://img.example/s1.png"}}
	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, kept, nil, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Kept Description") {
		t.Fatalf("expected kept description, got %q", result)
	}
	if !strings.Contains(result, "[img=350]https://img.example/s1.png[/img]") {
		t.Fatalf("expected screenshot to be appended, got %q", result)
	}
	if !strings.Contains(result, "Uploaded by upbrr") {
		t.Fatalf("expected signature in description, got %q", result)
	}
}

func TestBuildUnit3DDescriptionAppliesDescriptionConfig(t *testing.T) {
	meta := api.PreparedMetadata{}
	cfg := config.Config{
		Description: config.DescriptionSettingsConfig{
			ThumbnailSize:    300,
			ScreensPerRow:    "2",
			ScreenshotHeader: "Screenshots",
			CustomSignature:  "[size=2]custom sig[/size]",
		},
	}
	screens := []api.ScreenshotImage{
		{RawURL: "https://raw.example/1.png", WebURL: "https://web.example/1"},
		{RawURL: "https://raw.example/2.png", WebURL: "https://web.example/2"},
		{RawURL: "https://raw.example/3.png", WebURL: "https://web.example/3"},
	}
	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Screenshots") {
		t.Fatalf("expected screenshot header, got %q", result)
	}
	if !strings.Contains(result, "[size=2]custom sig[/size]") {
		t.Fatalf("expected custom signature, got %q", result)
	}
	if strings.Contains(result, "Uploaded by upbrr") {
		t.Fatalf("expected custom signature to replace UA signature, got %q", result)
	}
	expectedLine := "[center]\n[url=https://web.example/1][img=300]https://raw.example/1.png[/img][/url] [url=https://web.example/2][img=300]https://raw.example/2.png[/img][/url]\n[url=https://web.example/3][img=300]https://raw.example/3.png[/img][/url]\n[/center]"
	if !strings.Contains(result, expectedLine) {
		t.Fatalf("expected screens-per-row formatting, got %q", result)
	}
}

func TestBuildUnit3DDescriptionAddsLogoEpisodeOverviewAndMenuImages(t *testing.T) {
	meta := api.PreparedMetadata{
		EpisodeOverview: "Episode [b]overview[/b] text",
		ExternalMetadata: api.ExternalMetadata{
			TMDB: &api.TMDBMetadata{Logo: "https://image.tmdb.org/t/p/original/logo.png"},
		},
	}
	cfg := config.Config{
		Description: config.DescriptionSettingsConfig{
			AddLogo:         true,
			LogoSize:        400,
			EpisodeOverview: true,
			DiscMenuHeader:  "Disc menu",
			ThumbnailSize:   250,
		},
	}
	menuImages := []api.ScreenshotImage{{ImgURL: "https://img.example/menu1.png"}}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", menuImages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, expected := range []string{
		"[img=400]https://image.tmdb.org/t/p/original/logo.png[/img]",
		"[center]Episode %5Bb%5Doverview%5B/b%5D text[/center]",
		"Disc menu",
		"[img=250]https://img.example/menu1.png[/img]",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("expected %q in description, got %q", expected, result)
		}
	}
}

func TestBuildUnit3DDescriptionAddsBlurayLinkAndImages(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		ExternalMetadata: api.ExternalMetadata{
			Bluray: &api.BlurayMetadata{
				SelectedReleaseID: "123",
				Candidates: []api.BlurayReleaseCandidate{
					{
						ReleaseID: "123",
						URL:       "https://www.blu-ray.com/movies/Example-Blu-ray/123/",
						CoverImages: []api.BlurayImage{
							{Kind: "front", URL: "https://img.example/front[1].jpg"},
							{Kind: "back", URL: "https://img.example/back.jpg"},
							{Kind: "bad", URL: "javascript:alert(1)"},
						},
					},
				},
			},
		},
	}
	cfg := config.Config{
		Description: config.DescriptionSettingsConfig{
			AddBlurayLink:   true,
			UseBlurayImages: true,
			BlurayImageSize: 260,
		},
	}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, expected := range []string{
		"[center]https://www.blu-ray.com/movies/Example-Blu-ray/123/[/center]",
		"[img=260]https://img.example/front%5B1%5D.jpg[/img]",
		"[img=260]https://img.example/back.jpg[/img]",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("expected %q in description, got %q", expected, result)
		}
	}
	if strings.Contains(result, "javascript:alert") {
		t.Fatalf("expected unsafe blu-ray image URL to be skipped, got %q", result)
	}
}

func TestBuildUnit3DDescriptionSanitizesBlurayLink(t *testing.T) {
	cfg := config.Config{
		Description: config.DescriptionSettingsConfig{
			AddBlurayLink: true,
		},
	}
	meta := api.PreparedMetadata{
		DiscType: "BDMV",
		ExternalMetadata: api.ExternalMetadata{
			Bluray: &api.BlurayMetadata{
				SelectedReleaseID: "123",
				Candidates: []api.BlurayReleaseCandidate{
					{
						ReleaseID: "123",
						URL:       "https://www.blu-ray.com/movies/Example-[Bad]/123/",
					},
				},
			},
		},
	}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[center]https://www.blu-ray.com/movies/Example-%5BBad%5D/123/[/center]") {
		t.Fatalf("expected escaped blu-ray URL in description, got %q", result)
	}
	if strings.Contains(result, "Example-[Bad]") {
		t.Fatalf("expected raw BBCode brackets to be escaped, got %q", result)
	}

	meta.ExternalMetadata.Bluray.Candidates[0].URL = "not a url"
	result, err = buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "[center]not a url[/center]") {
		t.Fatalf("expected invalid blu-ray URL to be skipped, got %q", result)
	}
}

func TestBuildUnit3DDescriptionKeptIncludesScreenshots(t *testing.T) {
	meta := api.PreparedMetadata{}
	cfg := config.Config{Description: config.DescriptionSettingsConfig{ThumbnailSize: 350}}
	kept := "Kept [img]https://img.example/keep.png[/img]"
	screens := []api.ScreenshotImage{{ImgURL: "https://img.example/s1.png"}}
	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, kept, nil, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "s1.png") {
		t.Fatalf("expected screenshot to be included, got %q", result)
	}
}

func TestBuildUnit3DDescriptionSkipsBDInfo(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "db.sqlite")
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: dbPath},
		Description:  config.DescriptionSettingsConfig{CustomDescriptionHeader: "Header"},
	}
	meta := api.PreparedMetadata{
		SourcePath: filepath.Join(root, "Movie.mkv"),
		DiscType:   "BDMV",
		SelectedBDMVPlaylists: []api.PlaylistInfo{
			{File: "00001.MPLS"},
		},
	}

	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		t.Fatalf("tmp root: %v", err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		t.Fatalf("release temp dir: %v", err)
	}
	bdinfoPath := paths.BDMVSummaryPath(tmpDir, "00001.MPLS")
	if err := os.WriteFile(bdinfoPath, []byte("BDINFO_CONTENT"), 0o600); err != nil {
		t.Fatalf("write bdinfo: %v", err)
	}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "BDINFO_CONTENT") {
		t.Fatalf("expected bdinfo to be skipped, got %q", result)
	}
	if !strings.Contains(result, "Header") {
		t.Fatalf("expected header in description, got %q", result)
	}
}

func TestBuildUnit3DDescriptionSkipsMediaInfo(t *testing.T) {
	root := t.TempDir()
	miPath := filepath.Join(root, "MediaInfo.txt")
	if err := os.WriteFile(miPath, []byte("MEDIAINFO_CONTENT"), 0o600); err != nil {
		t.Fatalf("write mediainfo: %v", err)
	}

	cfg := config.Config{}
	meta := api.PreparedMetadata{
		SourcePath:        filepath.Join(root, "Movie.mkv"),
		MediaInfoTextPath: miPath,
	}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "MEDIAINFO_CONTENT") {
		t.Fatalf("expected mediainfo to be skipped, got %q", result)
	}
}

func TestBuildUnit3DDescriptionIncludesDVDVOBMediaInfo(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:            "DVD",
		DVDVOBMediaInfoText: "VOB_MI_CONTENT",
	}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[spoiler=VOB MediaInfo][code]VOB_MI_CONTENT[/code][/spoiler]") {
		t.Fatalf("expected dvd vob mediainfo block, got %q", result)
	}
	if !strings.Contains(result, "Uploaded by upbrr") {
		t.Fatalf("expected signature in description, got %q", result)
	}
}

func TestBuildUnit3DDescriptionSkipsDVDVOBMediaInfoForNonDVD(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:            "BDMV",
		DVDVOBMediaInfoText: "VOB_MI_CONTENT",
	}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "[spoiler=VOB MediaInfo][code]") {
		t.Fatalf("did not expect dvd vob mediainfo block, got %q", result)
	}
}

func TestBuildUnit3DDescriptionSkipsDVDVOBMediaInfoWhenEmpty(t *testing.T) {
	meta := api.PreparedMetadata{
		DiscType:            "DVD",
		DVDVOBMediaInfoText: "   \n\t",
	}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "[spoiler=VOB MediaInfo][code]") {
		t.Fatalf("did not expect dvd vob mediainfo block, got %q", result)
	}
}

func TestBuildUnit3DDescriptionIncludesTonemapHeaderForHDRScreens(t *testing.T) {
	meta := api.PreparedMetadata{HDR: "HDR10"}
	cfg := config.Config{
		ScreenshotHandling: config.ScreenshotHandlingConfig{ToneMap: true},
		Description: config.DescriptionSettingsConfig{
			TonemappedHeader: "[center]tone[/center]",
		},
	}
	screens := []api.ScreenshotImage{{ImgURL: "https://img.example/s1.png"}}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[center]tone[/center]") {
		t.Fatalf("expected tonemap header for HDR screenshots, got %q", result)
	}
}

func TestBuildUnit3DDescriptionSkipsTonemapHeaderForNonHDR(t *testing.T) {
	meta := api.PreparedMetadata{}
	cfg := config.Config{
		ScreenshotHandling: config.ScreenshotHandlingConfig{ToneMap: true},
		Description: config.DescriptionSettingsConfig{
			TonemappedHeader: "[center]tone[/center]",
		},
	}
	screens := []api.ScreenshotImage{{ImgURL: "https://img.example/s1.png"}}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", nil, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "[center]tone[/center]") {
		t.Fatalf("did not expect tonemap header for non-HDR content, got %q", result)
	}
}

func TestBuildUnit3DDescriptionACMTransformsBaseDescription(t *testing.T) {
	meta := api.PreparedMetadata{
		Type:            "WEBDL",
		ServiceLongName: "Netflix",
	}
	result, err := buildUnit3DDescription(context.Background(), "ACM", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "[pre]x[/pre]\n[hide=test]y[/hide]\n[img]https://img.example/z.png[/img]", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[code]x[/code]") {
		t.Fatalf("expected pre converted to code, got %q", result)
	}
	if !strings.Contains(result, "[spoiler=test]y[/spoiler]") {
		t.Fatalf("expected hide converted to spoiler, got %q", result)
	}
	if !strings.Contains(result, "not transcoded, just remuxed from the direct Netflix stream") {
		t.Fatalf("expected ACM web source header, got %q", result)
	}
	if !strings.Contains(result, "[img=300]https://img.example/z.png[/img]") {
		t.Fatalf("expected img resize normalization, got %q", result)
	}
}

func TestBuildUnit3DDescriptionFinalizesUnit3DBBCode(t *testing.T) {
	meta := api.PreparedMetadata{}
	kept := "[hide=Extras]notes[/hide]\n[user]name[/user]\n[comparison=Source, Encode]https://img.example/a.png https://img.example/b.png[/comparison]"

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, kept, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[spoiler=Extras]notes[/spoiler]") {
		t.Fatalf("expected hide converted to spoiler, got %q", result)
	}
	if strings.Contains(result, "[user]") || strings.Contains(result, "[/user]") {
		t.Fatalf("expected user tags removed, got %q", result)
	}
	if !strings.Contains(result, "[comparison=Source, Encode]") {
		t.Fatalf("expected comparison tag preserved, got %q", result)
	}
	if !strings.Contains(result, "https://img.example/a.png https://img.example/b.png[/comparison]") {
		t.Fatalf("expected comparison images preserved, got %q", result)
	}
}

func TestBuildUnit3DDescriptionAddsSHRIIslandReleaseNotes(t *testing.T) {
	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{Group: "island"},
	}

	result, err := buildUnit3DDescription(context.Background(), "SHRI", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "Base description", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Release Shareisland 🏴‍☠️") {
		t.Fatalf("expected Shareisland notes, got %q", result)
	}
	if !strings.Contains(result, "Base description") {
		t.Fatalf("expected base description to be preserved, got %q", result)
	}
}

func TestBuildUnit3DDescriptionSkipsSHRIIslandReleaseNotesForOtherGroups(t *testing.T) {
	meta := api.PreparedMetadata{
		Release: api.ReleaseInfo{Group: "other"},
	}

	result, err := buildUnit3DDescription(context.Background(), "SHRI", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, "Base description", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "Release Shareisland") {
		t.Fatalf("did not expect Shareisland notes, got %q", result)
	}
}

func TestBuildUnit3DDescriptionSkipsDuplicateTemplateAndKeptContent(t *testing.T) {
	block := `[center]
[url=https://pixhost.to/8ca234.png][img=350]https://pixhost.to/8ca234.png[/img][/url]
[url=https://pixhost.to/4oh0bz.png][img=350]https://pixhost.to/4oh0bz.png[/img][/url]
[/center]

[right][url=https://github.com/autobrr/upbrr][size=10]upbrr[/size][/url][/right]`
	meta := api.PreparedMetadata{DescriptionTemplate: block}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, block, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count := strings.Count(result, "[center]"); count != 1 {
		t.Fatalf("expected one screenshot block, got %d in %q", count, result)
	}
	if count := strings.Count(result, "Uploaded by upbrr"); count != 1 {
		t.Fatalf("expected one signature, got %d in %q", count, result)
	}
}

func TestBuildUnit3DDescriptionStripsKnownBotSignatures(t *testing.T) {
	kept := strings.Join([]string{
		"Body",
		"[center][b]Uploaded Using [url=https://github.com/HDInnovations/UNIT3D]UNIT3D[/url] Auto Uploader[/b][/center]",
		"[center][url=https://github.com/z-ink/uploadrr][img=300]https://i.ibb.co/2NVWb0c/uploadrr.webp[/img][/url][/center]",
		"[center][url=https://github.com/edge20200/Only-Uploader]Powered by Only-Uploader[/url][/center]",
		"[center][url=/torrents?perPage=50&name=Example][/url][/center]",
		"[center][b][size=20]brush[/size][/b] This is an internal release which was first released exclusively on Aither. Cheers to all the Aither users[/center]",
		"[center]   [/center]",
		"[right]Created by Upload Assistant[/right]",
	}, "\n")

	result, err := buildUnit3DDescription(context.Background(), "AITHER", api.PreparedMetadata{}, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, kept, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, unexpected := range []string{"Uploaded Using", "uploadrr", "Only-Uploader", "/torrents?perPage", "Aither users", "[center]   [/center]", "Upload Assistant"} {
		if strings.Contains(result, unexpected) {
			t.Fatalf("expected %q to be stripped, got %q", unexpected, result)
		}
	}
	if !strings.Contains(result, "Body") {
		t.Fatalf("expected body to remain, got %q", result)
	}
}

func TestBuildUnit3DDescriptionKeepsCenteredScreenshotsBeforeAitherFooter(t *testing.T) {
	kept := strings.Join([]string{
		"Body",
		"[center][url=https://web.example/1][img=350]https://raw.example/1.png[/img][/url][/center]",
		"[center][b][size=20]brush[/size][/b] This is an internal release which was first released exclusively on Aither. Cheers to all the Aither users[/center]",
	}, "\n\n")

	result, err := buildUnit3DDescription(context.Background(), "AITHER", api.PreparedMetadata{}, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, kept, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "Aither users") {
		t.Fatalf("expected aither footer stripped, got %q", result)
	}
	if !strings.Contains(result, "https://raw.example/1.png") {
		t.Fatalf("expected centered screenshot preserved, got %q", result)
	}
	if count := strings.Count(result, "[center]"); count != 1 {
		t.Fatalf("expected one centered screenshot block, got %d in %q", count, result)
	}
}

func TestBuildUnit3DDescriptionReplacesExistingScreenshotBlock(t *testing.T) {
	base := `[center]
[url=https://pixhost.to/8ca234.png][img=350]https://pixhost.to/8ca234.png[/img][/url]
[url=https://pixhost.to/7129bd.png][img=350]https://pixhost.to/7129bd.png[/img][/url]

[url=https://pixhost.to/4oh0bz.png][img=350]https://pixhost.to/4oh0bz.png[/img][/url]
[url=https://pixhost.to/7sv795.png][img=350]https://pixhost.to/7sv795.png[/img][/url]
[/center]

[center][spoiler=Scene NFO:][code]scene nfo[/code][/spoiler][/center]`
	screens := []api.ScreenshotImage{
		{RawURL: "https://new.example/1.png", WebURL: "https://web.example/1"},
		{RawURL: "https://new.example/2.png", WebURL: "https://web.example/2"},
		{RawURL: "https://new.example/3.png", WebURL: "https://web.example/3"},
		{RawURL: "https://new.example/4.png", WebURL: "https://web.example/4"},
	}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", api.PreparedMetadata{}, config.Config{
		Description: config.DescriptionSettingsConfig{ThumbnailSize: 350, ScreensPerRow: "2"},
	}, config.TrackerConfig{}, api.NopLogger{}, base, nil, screens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "https://pixhost.to/8ca234.png") {
		t.Fatalf("expected old screenshot block removed, got %q", result)
	}
	if strings.Contains(result, "scene nfo") {
		t.Fatalf("expected NFO block removed from description, got %q", result)
	}
	if count := strings.Count(result, "[center]"); count != 1 {
		t.Fatalf("expected only one centered screenshot block, got %d in %q", count, result)
	}
	if count := strings.Count(result, "https://new.example/"); count != 4 {
		t.Fatalf("expected one rebuilt screenshot block, got %q", result)
	}
}

func TestBuildUnit3DDescriptionStripsExistingSceneNFOBlock(t *testing.T) {
	kept := `[center][spoiler=Scene NFO:][code]stale scene nfo[/code][/spoiler][/center]

Custom body`
	meta := api.PreparedMetadata{Scene: true}

	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, config.Config{}, config.TrackerConfig{}, api.NopLogger{}, kept, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "stale scene nfo") {
		t.Fatalf("expected scene nfo block removed, got %q", result)
	}
	if strings.Contains(result, "[spoiler=Scene NFO:][code]") {
		t.Fatalf("expected no scene nfo block in description, got %q", result)
	}
	if !strings.Contains(result, "Custom body") {
		t.Fatalf("expected surrounding description preserved, got %q", result)
	}
}

func TestDefinitionBuildDescriptionFormatsOverrideContent(t *testing.T) {
	definition := New("AITHER")
	result, err := definition.BuildDescription(context.Background(), trackers.DescriptionRequest{
		Tracker: "AITHER",
		Meta:    api.PreparedMetadata{},
		AppConfig: config.Config{
			Description: config.DescriptionSettingsConfig{ThumbnailSize: 350},
		},
		Logger: api.NopLogger{},
		Assets: &trackers.DescriptionAssets{
			Description: "[align=center][img width=350]https://img.example/a.png[/img][/align]",
			Override:    true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Description, "[align=") {
		t.Fatalf("expected override to be finalized for unit3d, got %q", result.Description)
	}
	if !strings.Contains(result.Description, "[center]") {
		t.Fatalf("expected unit3d-safe centering, got %q", result.Description)
	}
}

func TestBuildUnit3DDescriptionFiltersMenuImagesFromScreenshots(t *testing.T) {
	meta := api.PreparedMetadata{}
	cfg := config.Config{}
	menuImages := []api.ScreenshotImage{{ImgURL: "https://img.example/menu1.png"}}
	screenshots := []api.ScreenshotImage{
		{ImgURL: "https://img.example/screen1.png"},
		{ImgURL: "https://img.example/menu1.png"}, // Duplicate of menu image
	}
	result, err := buildUnit3DDescription(context.Background(), "AITHER", meta, cfg, config.TrackerConfig{}, api.NopLogger{}, "", menuImages, screenshots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count occurrences of menu1.png
	count := strings.Count(result, "menu1.png")
	if count != 1 {
		t.Fatalf("expected menu image to appear only once, got %d times in %q", count, result)
	}
}
