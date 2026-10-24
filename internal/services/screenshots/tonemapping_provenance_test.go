// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package screenshots

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/imageevidence"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestTonemappingRestoresOnlyMatchingScreenshotBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := imageevidence.Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	records := []api.Screenshot{{
		ImagePath:     path,
		ContentSHA256: digest,
		Tonemapping:   api.ScreenshotTonemapped,
	}}
	images := []api.ScreenshotImage{{Path: path}}
	restoreScreenshotTonemapping(images, records)
	if images[0].Tonemapping != api.ScreenshotTonemapped {
		t.Fatal("lost saved processing")
	}
	if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	restoreScreenshotTonemapping(images, records)
	if images[0].Tonemapping != "" {
		t.Fatal("replacement inherited processing")
	}
}

func TestTonemappingLinkedURLKeepsItsOwnEvidence(t *testing.T) {
	links := []api.ScreenshotLinkedImage{{URL: "https://images.example.invalid/known.png"}, {URL: "https://images.example.invalid/unknown.png"}}
	restoreLinkedTonemapping(links, []api.UploadedImageLink{{
		RawURL:        links[0].URL,
		Tonemapping:   api.ScreenshotNotTonemapped,
		ContentSHA256: "digest",
	}})
	if links[0].Tonemapping != api.ScreenshotNotTonemapped || links[1].Tonemapping != "" {
		t.Fatalf("links=%+v", links)
	}
}

func TestPlanPreservesTrackerURLTonemapping(t *testing.T) {
	for _, multiDisc := range []bool{false, true} {
		t.Run(map[bool]string{false: "single media", true: "multi-disc"}[multiDisc], func(t *testing.T) {
			subject, _, _ := multiDiscScreenshotSubject(t)
			if !multiDisc {
				subject.SourcePath = subject.Discs[0].VideoPath
				subject.MediaInfoJSONPath = subject.Discs[0].MediaInfoJSONPath
				subject.Discs = nil
			}
			subject.MediaBinding = screenshotTestBinding(subject.SourcePath)
			repo := openScreenshotTestRepository(t)
			tmpRoot := t.TempDir()
			tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, subject.SourcePath, subject.Release)
			if err != nil {
				t.Fatal(err)
			}
			trackerDir := filepath.Join(tmpDir, "aither")
			if err := os.MkdirAll(trackerDir, 0o700); err != nil {
				t.Fatal(err)
			}
			urls := []string{
				"https://images.example.invalid/mapped.png",
				"https://images.example.invalid/unmapped.png",
				"https://images.example.invalid/unknown.png",
			}
			statuses := []api.ScreenshotTonemapping{api.ScreenshotTonemapped, api.ScreenshotNotTonemapped, ""}
			digests := make([]string, len(urls))
			artifacts := make([]string, len(urls))
			for index, url := range urls {
				artifacts[index] = filepath.Join(trackerDir, buildTrackerImageFilename(url, index))
				writeTrackerTestPNG(t, artifacts[index])
				digests[index], err = imageevidence.Digest(artifacts[index])
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.SaveScreenshot(t.Context(), subject.MediaBinding, api.Screenshot{
					ImagePath:     artifacts[index],
					Purpose:       api.ScreenshotPurposeFinal,
					Tonemapping:   api.ScreenshotTonemapped,
					ContentSHA256: digests[index],
				}); err != nil {
					t.Fatal(err)
				}
				if statuses[index] != "" {
					if err := repo.SaveUploadedImages(t.Context(), subject.MediaBinding, "pixhost", []api.UploadedImageLink{{
						ImagePath:     artifacts[index],
						Purpose:       api.ScreenshotPurposeFinal,
						Host:          "pixhost",
						RawURL:        url,
						ImgURL:        url,
						Tonemapping:   statuses[index],
						ContentSHA256: digests[index],
					}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			record := api.TrackerMetadata{
				SourcePath: subject.SourcePath,
				Tracker:    "AITHER",
				ImageURLs:  urls,
			}
			if err := repo.SaveTrackerMetadata(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			service := NewServiceWithRepo(config.Config{}, api.NopLogger{}, tmpRoot, nil, repo)
			checkPlan := func() {
				t.Helper()
				plan, err := service.Plan(t.Context(), subject, 3)
				if err != nil {
					t.Fatal(err)
				}
				if len(plan.TrackerImageLinks) != len(urls) {
					t.Fatalf("tracker links=%+v", plan.TrackerImageLinks)
				}
				for index, link := range plan.TrackerImageLinks {
					if link.URL != record.ImageURLs[index] {
						t.Fatalf("tracker URL=%q, want %q", link.URL, record.ImageURLs[index])
					}
					wantStatus := statuses[index]
					wantDigest := digests[index]
					if wantStatus == "" || link.URL != urls[index] {
						wantStatus = ""
						wantDigest = ""
					}
					if link.Tonemapping != wantStatus || link.ContentSHA256 != wantDigest {
						t.Fatalf("tracker URL evidence=%+v, want processing=%q digest=%q", link, wantStatus, wantDigest)
					}
				}
			}
			checkPlan()
			for _, artifact := range artifacts {
				if err := os.WriteFile(artifact, testPNGBytes(t, color.RGBA{R: 255, A: 255}), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			checkPlan()
			record.ImageURLs = append([]string(nil), urls...)
			record.ImageURLs[0] = "https://images.example.invalid/replacement.png"
			writeTrackerTestPNG(t, filepath.Join(trackerDir, buildTrackerImageFilename(record.ImageURLs[0], 0)))
			if err := repo.SaveTrackerMetadata(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			checkPlan()
		})
	}
}

// The runner emits synthetic PNGs; this verifies lifecycle evidence, not HDR pixel quality.
func TestTonemappingCaptureSurvivesRepositoryRestartAndPreferenceChange(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "tonemapped", false: "not tonemapped"}[enabled], func(t *testing.T) {
			subject, _, _ := multiDiscScreenshotSubject(t)
			subject.Discs = subject.Discs[:1]
			subject.MediaBinding = screenshotTestBinding(subject.SourcePath)
			subject.HDR = "HDR10"
			ffmpegRoot := t.TempDir()
			if err := writeTestBundledFFmpeg(ffmpegRoot); err != nil {
				t.Fatal(err)
			}
			t.Chdir(ffmpegRoot)
			database := filepath.Join(t.TempDir(), "test.sqlite")
			repo, err := db.Open(database)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			tmp := t.TempDir()
			cfg := config.Config{}
			cfg.ScreenshotHandling.ToneMap = enabled
			runner := &writingScreenshotRunner{payload: testPNGBytes(t, color.RGBA{
				R: 16,
				G: 16,
				B: 16,
				A: 255,
			})}
			service := NewServiceWithRepo(cfg, api.NopLogger{}, tmp, runner, repo)
			plan, err := service.Plan(t.Context(), subject, 1)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Capture(t.Context(), subject, plan.SuggestedSelections, api.ScreenshotPurposeFinal)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Images) != 1 {
				t.Fatalf("capture=%+v", result)
			}
			image := result.Images[0]
			want := api.ScreenshotNotTonemapped
			if enabled {
				want = api.ScreenshotTonemapped
			}
			if image.Tonemapping != want || len(image.ContentSHA256) != 64 {
				t.Fatalf("capture evidence=%+v", image)
			}
			if err := service.SaveFinalSelections(t.Context(), subject, result.Images); err != nil {
				t.Fatal(err)
			}
			url := "https://images.example.invalid/captured.png"
			if err := repo.SaveUploadedImages(t.Context(), subject.MediaBinding, "pixhost", []api.UploadedImageLink{{
				ImagePath:     image.Path,
				Purpose:       image.Purpose,
				Host:          "pixhost",
				RawURL:        url,
				ImgURL:        url,
				Tonemapping:   image.Tonemapping,
				ContentSHA256: image.ContentSHA256,
			}}); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			repo, err = db.Open(database)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			cfg.ScreenshotHandling.ToneMap = !enabled
			restarted := NewServiceWithRepo(cfg, api.NopLogger{}, tmp, nil, repo)
			restored, err := restarted.Plan(t.Context(), subject, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(restored.FinalSelections) != 1 || restored.FinalSelections[0].Tonemapping != want {
				t.Fatalf("restored=%+v", restored.FinalSelections)
			}
			links, err := repo.ListUploadedImagesByPath(t.Context(), subject.MediaBinding)
			if err != nil {
				t.Fatal(err)
			}
			if len(links) != 1 || links[0].Tonemapping != want || links[0].RawURL != url {
				t.Fatalf("links=%+v", links)
			}
			restoredImage := restored.FinalSelections[0]
			restoredImage.RawURL = links[0].RawURL
			notice := unit3d.TonemappingNotice("TONEMAPPED", []api.ScreenshotImage{restoredImage})
			if (notice != "") != enabled {
				t.Fatalf("notice=%q", notice)
			}
		})
	}
}
