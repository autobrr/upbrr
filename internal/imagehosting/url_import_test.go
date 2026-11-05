// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/imageevidence"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type importImageTransport func(*http.Request) (*http.Response, error)

func (f importImageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestURLImportBindsBothRemoteImagesBeforePersistence(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"verified", "changed hosted bytes", "private source", "wrong destination", "live zero budget", "live exhausted budget"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			imagePath := filepath.Join(root, "source.png")
			if err := os.WriteFile(imagePath, imageBytes.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			digest, err := imageevidence.Digest(imagePath)
			if err != nil {
				t.Fatal(err)
			}
			old := http.DefaultTransport
			http.DefaultTransport = importImageTransport(func(r *http.Request) (*http.Response, error) {
				body := imageBytes.Bytes()
				if scenario == "changed hosted bytes" && r.URL.Hostname() == "8.8.4.4" {
					body = append(bytes.Clone(body), 'x')
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"image/png"}},
					Body:       io.NopCloser(bytes.NewReader(body)),
					Request:    r,
				}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			base := imageHostingTestRegistry(t)
			descriptor, _ := base.LookupDescriptor("GPW")
			calls := 0
			descriptor.ImageHost.URLImport.Host = "8.8.4.4"
			descriptor.ImageHost.URLImport.Import = func(context.Context, *http.Client, string, string) (string, error) {
				calls++
				if scenario == "wrong destination" {
					return "https://9.9.9.9/wrong.png", nil
				}
				return "https://8.8.4.4/hosted.png", nil
			}
			registry := trackers.NewRegistry()
			if err := registry.RegisterDescriptor(descriptor); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{"GPW": {APIKey: "synthetic-key"}}}}
			cfg.MainSettings.DBPath = filepath.Join(root, "db.sqlite")
			repo := &recordingRepo{}
			service := NewServiceWithRegistry(cfg, api.NopLogger{}, repo, registry)
			if scenario == "live zero budget" || scenario == "live exhausted budget" {
				budget := 0
				if scenario == "live exhausted budget" {
					budget = 1
				}
				service, err = NewLiveTestServiceWithRegistry(cfg, api.NopLogger{}, repo, registry, "test-run", filepath.Join(root, "image-effects.private.jsonl"), budget)
				if err != nil {
					t.Fatal(err)
				}
				if budget == 1 {
					guard, ok := service.uploaders["pixhost"].(*liveTestUploader)
					if !ok {
						t.Fatal("ordinary uploader lost live guard")
					}
					if _, err := guard.upload(t.Context(), []string{imagePath}, func() ([]uploadResult, error) {
						return []uploadResult{{RawURL: "https://pixhost.invalid/one.png"}}, nil
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			binding := api.PreparedMediaBinding{
				SourcePath:               filepath.Join(root, "Example.Release.mkv"),
				PreparedMediaFingerprint: "fingerprint",
				PreparedGeneration:       1,
			}
			source := "https://8.8.8.8/source.png"
			if scenario == "private source" {
				source = "http://127.0.0.1/private.png"
			}
			links, err := service.ImportURLs(t.Context(), api.ImageHostingSubject{SourcePath: binding.SourcePath, MediaBinding: binding}, "8.8.4.4", "tracker:GPW", []api.ScreenshotImage{{
				Path:          imagePath,
				RawURL:        source,
				Purpose:       api.ScreenshotPurposeFinal,
				ContentSHA256: digest,
				Tonemapping:   api.ScreenshotNotTonemapped,
			}})
			if scenario == "live zero budget" || scenario == "live exhausted budget" {
				if !errors.Is(err, errLiveImageBudget) || calls != 0 || len(repo.savedImages) != 0 {
					t.Fatalf("native import bypassed shared live budget: calls=%d err=%v", calls, err)
				}
				return
			}
			if scenario != "verified" {
				if err == nil || len(repo.savedImages) != 0 || scenario == "private source" && calls != 0 {
					t.Fatal("unverified native result was persisted or private source was submitted")
				}
				return
			}
			if err != nil || calls != 1 || len(links) != 1 || len(repo.savedImages) != 1 || links[0].ContentSHA256 != digest || links[0].Tonemapping != api.ScreenshotNotTonemapped || links[0].AccountScope == "" {
				t.Fatalf("verified native result not persisted: %v", err)
			}
			repo.uploads = links
			candidates, err := service.ListCandidates(t.Context(), api.ImageHostingSubject{SourcePath: binding.SourcePath, MediaBinding: binding})
			if err != nil || len(candidates) != 1 {
				t.Fatal("current native account could not reuse persisted upload")
			}
			cfg.Trackers.Trackers["GPW"] = config.TrackerConfig{APIKey: "replacement-key"}
			replacement := NewServiceWithRegistry(cfg, api.NopLogger{}, repo, registry)
			candidates, err = replacement.ListCandidates(t.Context(), api.ImageHostingSubject{SourcePath: binding.SourcePath, MediaBinding: binding})
			if err != nil || len(candidates) != 0 {
				t.Fatal("candidate listing reused a stale native account")
			}
		})
	}
}
