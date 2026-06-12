// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type imageHostResolutionRepo struct {
	*stubRepo
	replaceCalls int
}

func (r *imageHostResolutionRepo) ReplaceScreenshotSlots(ctx context.Context, sourcePath string, slots []api.ScreenshotSlot) error {
	r.replaceCalls++
	return r.stubRepo.ReplaceScreenshotSlots(ctx, sourcePath, slots)
}

func TestEnsureDescriptionImageHostSkipUploadDoesNotMaterializeURLOnlySlots(t *testing.T) {
	originalFactory := newDescriptionSlotImageHTTPClient
	clientCalls := 0
	newDescriptionSlotImageHTTPClient = func() *http.Client {
		clientCalls++
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("skip upload should not fetch description image slots")
			return nil, errors.New("unexpected request")
		})}
	}
	t.Cleanup(func() {
		newDescriptionSlotImageHTTPClient = originalFactory
	})

	skipUpload := true
	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	repo := &imageHostResolutionRepo{stubRepo: &stubRepo{}}
	meta := api.PreparedMetadata{
		SourcePath:          sourcePath,
		DescriptionOverride: "[center][img]http://8.8.8.8/image.gif[/img][/center]",
		ImageHostOverrides: api.ImageHostOverrides{
			SkipUpload: &skipUpload,
		},
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}}
	trackerCfg := config.TrackerConfig{ImageHost: "imgbox"}

	resolution, err := ensureDescriptionImageHostWithData(context.Background(), "MTV", meta, cfg, trackerCfg, repo, &stubImageService{}, api.NopLogger{}, nil)
	if err != nil {
		t.Fatalf("ensure image host: %v", err)
	}
	if resolution.feedback.Status != "warning" {
		t.Fatalf("expected warning feedback, got %#v", resolution.feedback)
	}
	if !strings.Contains(resolution.feedback.Message, "automatic image-host uploads are disabled") {
		t.Fatalf("expected skip-upload warning, got %q", resolution.feedback.Message)
	}
	if len(resolution.screenshots) != 0 {
		t.Fatalf("expected no resolved screenshots, got %#v", resolution.screenshots)
	}
	if clientCalls != 0 {
		t.Fatalf("expected no description image HTTP client creation, got %d", clientCalls)
	}
	if repo.replaceCalls != 0 {
		t.Fatalf("expected no screenshot slot persistence, got %d calls", repo.replaceCalls)
	}
	if len(repo.screenshotSlots) != 0 {
		t.Fatalf("expected synthesized URL-only slots to stay unpersisted, got %#v", repo.screenshotSlots)
	}
}

func TestDownloadDescriptionSlotImageRejectsPrivateTargets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("private target should be rejected before transport")
		return nil, errors.New("unexpected transport call")
	})}
	for _, rawURL := range []string{
		"http://127.0.0.1/image.png",
		"http://[::1]/image.png",
		"http://localhost/image.png",
		"http://10.0.0.1/image.png",
		"http://240.0.0.1/image.png",
	} {
		t.Run(rawURL, func(t *testing.T) {
			outPath := filepath.Join(t.TempDir(), "image.png")
			if err := downloadDescriptionSlotImage(ctx, client, rawURL, outPath); err == nil {
				t.Fatalf("expected %s to be rejected", rawURL)
			}
			if _, err := os.Stat(outPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected no output file, got stat err %v", err)
			}
		})
	}
}

func TestDownloadDescriptionSlotImageRejectsRedirectToDNSResolvedReservedIPv4(t *testing.T) {
	// Keep this test non-parallel: it overrides global resolver state.
	calls := 0
	originalLookup := descriptionSlotImageLookupIPAddrs
	descriptionSlotImageLookupIPAddrs = func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		return []net.IPAddr{{IP: net.ParseIP("240.0.0.1")}}, nil
	}
	t.Cleanup(func() {
		descriptionSlotImageLookupIPAddrs = originalLookup
	})

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "8.8.8.8":
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"http://reserved-slot.test/image.png"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		case "reserved-slot.test":
			t.Fatal("DNS-resolved reserved redirect target should be rejected before transport")
		case "127.0.0.1":
			t.Fatal("private redirect target should be rejected before transport")
		default:
			t.Fatalf("unexpected host %q", req.URL.Host)
		}
		return nil, errors.New("unexpected transport call")
	})}

	err := downloadDescriptionSlotImage(context.Background(), client, "http://8.8.8.8/image.png", filepath.Join(t.TempDir(), "image.png"))
	if err == nil {
		t.Fatal("expected redirect DNS block rejection")
	}
	if calls != 1 {
		t.Fatalf("expected one blocked DNS resolution attempt, got %d", calls)
	}
}

func TestDownloadDescriptionSlotImageRejectsRedirectToPrivate(t *testing.T) {
	t.Parallel()

	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Host == "127.0.0.1" {
			t.Fatal("private redirect target should be rejected before transport")
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://127.0.0.1/private.png"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}

	err := downloadDescriptionSlotImage(context.Background(), client, "http://8.8.8.8/image.png", filepath.Join(t.TempDir(), "image.png"))
	if err == nil {
		t.Fatalf("expected private redirect rejection")
	}
	if calls != 1 {
		t.Fatalf("expected one public request before redirect rejection, got %d", calls)
	}
}

func TestDownloadDescriptionSlotImageRejectsNonImagePayload(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Body:       io.NopCloser(strings.NewReader("not an image")),
			Request:    req,
		}, nil
	})}

	err := downloadDescriptionSlotImage(context.Background(), client, "http://8.8.8.8/image.png", filepath.Join(t.TempDir(), "image.png"))
	if err == nil {
		t.Fatalf("expected non-image payload rejection")
	}
}

func TestDownloadDescriptionSlotImageAcceptsPublicImage(t *testing.T) {
	t.Parallel()

	payload := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": []string{"image/gif"}},
			Body:          io.NopCloser(strings.NewReader(string(payload))),
			ContentLength: int64(len(payload)),
			Request:       req,
		}, nil
	})}
	outPath := filepath.Join(t.TempDir(), "image.gif")

	if err := downloadDescriptionSlotImage(context.Background(), client, "http://8.8.8.8/image.gif", outPath); err != nil {
		t.Fatalf("download public image: %v", err)
	}
	written, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(written) != string(payload) {
		t.Fatalf("unexpected payload written")
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if mode := info.Mode().Perm(); runtime.GOOS != "windows" && mode != 0o600 {
		t.Fatalf("expected 0600 output mode, got %o", mode)
	}
}

func TestMaterializeDescriptionSlotImagesKeepsRetryStateOnDownloadFailure(t *testing.T) {
	t.Parallel()

	originalURL := "http://127.0.0.1/image.png"
	slots := []api.ScreenshotSlot{{
		SourcePath:          "source.mkv",
		SlotOrder:           0,
		SourceKind:          screenshotSlotSourceDescription,
		OriginalKey:         originalURL,
		OriginalURL:         originalURL,
		SectionKind:         screenshotSectionWrapped,
		RenderInScreenshots: true,
	}}
	meta := api.PreparedMetadata{SourcePath: "source.mkv"}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}}

	results, changed := materializeDescriptionSlotImages(context.Background(), meta, cfg, "AITHER", slots, api.NopLogger{})

	if changed {
		t.Fatalf("expected failed materialization to leave persisted slot state unchanged")
	}
	if len(results) != 0 {
		t.Fatalf("expected no materialized results, got %#v", results)
	}
	if !slots[0].RenderInScreenshots {
		t.Fatalf("expected renderable flag to remain set for retry")
	}
	if slots[0].OriginalURL != originalURL {
		t.Fatalf("expected original URL to be preserved, got %q", slots[0].OriginalURL)
	}
	if strings.TrimSpace(slots[0].ImagePath) != "" {
		t.Fatalf("expected no image path after failed materialization, got %q", slots[0].ImagePath)
	}
}

func TestMaterializeDescriptionSlotImagesPreservesExistingLocalImagePath(t *testing.T) {
	t.Parallel()

	imagePath := filepath.Join(t.TempDir(), "existing.png")
	if err := os.WriteFile(imagePath, []byte("local"), 0o600); err != nil {
		t.Fatalf("write local image: %v", err)
	}
	slots := []api.ScreenshotSlot{{
		SourcePath:          "source.mkv",
		SlotOrder:           2,
		SourceKind:          screenshotSlotSourceSelection,
		OriginalKey:         imagePath,
		ImagePath:           imagePath,
		SectionKind:         screenshotSectionWrapped,
		RenderInScreenshots: true,
	}}
	meta := api.PreparedMetadata{SourcePath: "source.mkv"}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}}

	results, changed := materializeDescriptionSlotImages(context.Background(), meta, cfg, "AITHER", slots, api.NopLogger{})

	if changed {
		t.Fatalf("expected existing local image path to remain unchanged")
	}
	if len(results) != 1 || results[0].Path != imagePath {
		t.Fatalf("expected existing local image path result, got %#v", results)
	}
	if slots[0].ImagePath != imagePath {
		t.Fatalf("expected slot image path to be preserved, got %q", slots[0].ImagePath)
	}
}
