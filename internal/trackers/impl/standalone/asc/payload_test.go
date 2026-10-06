// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func payloadTestMovie() api.UploadSubject {
	return api.UploadSubject{
		Type:           "WEBDL",
		Container:      "mkv",
		VideoCodec:     "AVC",
		Audio:          "DD+ 5.1",
		AudioLanguages: []string{"English", "Portuguese"},
		Release: api.ReleaseInfo{
			Title:      "Example Movie",
			Year:       2026,
			Resolution: "1080p",
		},
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 1234567},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			OriginalLanguage: "en",
			Genres:           "Drama, Comedy",
			YouTube:          "abc123",
		}},
	}
}

func TestBuildPayloadMovie(t *testing.T) {
	t.Parallel()

	payload := buildPayload(payloadTestMovie(), config.TrackerConfig{Anon: true}, "desc", "Example Movie", "General report")
	wantFields := map[string]string{
		"category_id": categoryMovie,
		"name":        "Example Movie",
		"year":        "2026",
		"imdb_id":     "tt1234567",
		"width":       "1920",
		"height":      "1080",
		"anonymous":   "1",
		"subtitle":    subtitleNone,
		"trailer_url": "https://www.youtube.com/watch?v=abc123",
		"description": "desc",
		"mediainfo":   "General report",
	}
	for key, want := range wantFields {
		if got := payload.fields[key]; got != want {
			t.Errorf("field %s = %q, want %q", key, got, want)
		}
	}
	wantAttributes := []string{"1", "59", audioDual, "92", "195", "161", "231", "227"}
	if !slices.Equal(payload.attributeIDs, wantAttributes) {
		t.Fatalf("attribute_ids = %v, want %v", payload.attributeIDs, wantAttributes)
	}
	multipart := payload.multipartFields([]string{"screenshots/a.webp", "screenshots/b.webp"})
	if !slices.Equal(multipart["attribute_ids[]"], wantAttributes) || multipart["category_id"][0] != categoryMovie {
		t.Fatalf("multipart fields = %v", multipart)
	}
	if !slices.Equal(multipart["screenshot_paths[]"], []string{"screenshots/a.webp", "screenshots/b.webp"}) {
		t.Fatalf("screenshot paths = %v", multipart["screenshot_paths[]"])
	}
	preview := payload.previewFields()
	for _, key := range []string{"description", "mediainfo"} {
		if _, ok := preview[key]; ok {
			t.Fatalf("preview fields should not duplicate %s", key)
		}
	}
}

func TestBuildPayloadAnimeUsesVideoProfile(t *testing.T) {
	t.Parallel()

	meta := payloadTestMovie()
	meta.Anime = true
	meta.Identity.Category = api.CanonicalCategoryTV
	payload := buildPayload(meta, config.TrackerConfig{}, "desc", "Example Anime - S01", "General report")
	if payload.fields["category_id"] != categoryAnimeSeries {
		t.Fatalf("category = %q", payload.fields["category_id"])
	}
	for _, key := range []string{"imdb_id", "width", "height", "trailer_url", "mediainfo", "subtitle"} {
		if strings.TrimSpace(payload.fields[key]) == "" {
			t.Errorf("anime payload missing %s", key)
		}
	}
	if !slices.Equal(payload.genreIDs, []string{"231", "227"}) {
		t.Fatalf("anime genres = %v", payload.genreIDs)
	}
}

func TestValidatePayloadFieldsRequiresScreenshotsAndCover(t *testing.T) {
	t.Parallel()

	meta := payloadTestMovie()
	meta.ProviderMetadata.TMDB.Overview = "Overview"
	payload := buildPayload(meta, config.TrackerConfig{}, "desc", "Example Movie", "")
	if got := validatePayloadFields(meta, payload, 2, "https://image.example/poster.jpg"); got != "missing MediaInfo report" {
		t.Fatalf("missing mediainfo reason = %q", got)
	}
	payload.fields["mediainfo"] = "General report"
	if got := validatePayloadFields(meta, payload, 1, "https://image.example/poster.jpg"); got == "" {
		t.Fatal("expected screenshot minimum to block")
	}
	if got := validatePayloadFields(meta, payload, 2, ""); got != "missing poster URL" {
		t.Fatalf("missing cover reason = %q", got)
	}
	if got := validatePayloadFields(meta, payload, 2, "https://image.example/poster.jpg"); got != "" {
		t.Fatalf("expected valid payload, got %q", got)
	}
	payload.fields["description"] = "[img]https://image.example/shot.png[/img]"
	if got := validatePayloadFields(meta, payload, 2, "https://image.example/poster.jpg"); got != "description embeds images hosted outside ASC" {
		t.Fatalf("foreign image reason = %q", got)
	}
}

func TestSelectScreenshotsFiltersAndCaps(t *testing.T) {
	t.Parallel()

	assets := trackers.DescriptionAssets{}
	for idx := range 8 {
		assets.Screenshots = append(assets.Screenshots, api.ScreenshotImage{Path: filepath.Join("shots", "s"+string(rune('a'+idx))+".png")})
	}
	assets.Screenshots[0].Path = ""
	assets.Screenshots[1].Path = filepath.Join("shots", "s.bmp")
	got := selectScreenshots(assets)
	if len(got) != maxScreenshots || got[0] != filepath.Join("shots", "sc.png") {
		t.Fatalf("selected screenshots = %v", got)
	}
}

func TestLoadScreenshotFilesShrinksOversizedPNG(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	small := filepath.Join(dir, "small.png")
	large := filepath.Join(dir, "large.png")
	writeTestPNG(t, small, 16, 16)
	writeTestPNG(t, large, 1700, 1300)
	if info, err := os.Stat(large); err != nil || info.Size() <= maxImageBytes {
		t.Fatalf("fixture must exceed the image limit: %v", err)
	}

	files, err := loadScreenshotFiles([]string{small, large})
	if err != nil {
		t.Fatalf("load screenshots: %v", err)
	}
	if len(files) != 2 || files[0].FileName != "small.png" || files[1].FileName != "large.jpg" {
		t.Fatalf("unexpected files %+v", files)
	}
	if len(files[1].Content) > maxImageBytes || !bytes.HasPrefix(files[1].Content, []byte{0xFF, 0xD8}) {
		t.Fatalf("oversized screenshot was not re-encoded as JPEG (%d bytes)", len(files[1].Content))
	}
}

func writeTestPNG(t *testing.T, path string, width int, height int) {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(rng.UintN(256)),
				G: uint8(rng.UintN(256)),
				B: uint8(rng.UintN(256)),
				A: 255,
			})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
}
