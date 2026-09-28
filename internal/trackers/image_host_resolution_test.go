// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDescriptionSlotImageNameUsesURLIdentity(t *testing.T) {
	firstURL := "https://host-a.example/shot.png"
	secondURL := "https://host-b.example/shot.png"
	first := buildDescriptionSlotImageName(firstURL, 0)
	second := buildDescriptionSlotImageName(secondURL, 0)
	if first == second {
		t.Fatalf("different image URLs share cache file %q", first)
	}
	if screenshotSourceMatchKey(first) != screenshotURLMatchKey(firstURL) ||
		screenshotSourceMatchKey(second) != screenshotURLMatchKey(secondURL) {
		t.Fatalf("hashed cache files do not match their source URLs: first=%q second=%q", first, second)
	}
	if buildDescriptionSlotImageName(firstURL, 0) != first {
		t.Fatalf("same image URL did not reuse cache identity %q", first)
	}
}

func TestExactMediaForTrackerHostKeepsCompatibleSavedScreenshots(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	for _, target := range []struct {
		name string
		host string
	}{
		{name: "ALPHA", host: "pixhost"},
		{name: "BETA", host: "imgbb"},
	} {
		if err := registry.RegisterDescriptor(Descriptor{
			Name:       target.name,
			Definition: stubDefinition{name: target.name},
			ImageHost:  &ImageHostPolicy{AllowedHosts: []string{target.host}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	exact := &api.ExactMediaAssets{}
	for index, host := range []string{"pixhost", "pixhost", "imgbb", "imgbb"} {
		pathValue := filepath.Join(root, fmt.Sprintf("image-%d.png", index))
		url := fmt.Sprintf("https://%s.example.invalid/image-%d.png", host, index)
		exact.Screenshots = append(exact.Screenshots, api.ScreenshotImage{Path: pathValue, Purpose: api.ScreenshotPurposeFinal})
		exact.ScreenshotUploads = append(exact.ScreenshotUploads, api.UploadedImageLink{
			ImagePath:  pathValue,
			Host:       host,
			UsageScope: "global",
			RawURL:     url,
		})
	}
	for _, target := range []struct {
		name string
		host string
	}{
		{name: "ALPHA", host: "pixhost"},
		{name: "BETA", host: "imgbb"},
	} {
		filtered, err := exactMediaForTrackerHost(target.name, api.UploadSubject{ExactMedia: exact}, config.Config{}, config.TrackerConfig{}, registry)
		if err != nil {
			t.Fatal(err)
		}
		if len(filtered.Screenshots) != 2 || len(filtered.ScreenshotUploads) != 2 {
			t.Fatalf("%s exact media = %#v", target.name, filtered)
		}
		for _, link := range filtered.ScreenshotUploads {
			if link.Host != target.host {
				t.Fatalf("%s received incompatible upload %#v", target.name, link)
			}
		}
		preloaded, err := preloadDescriptionAssetData(t.Context(), api.UploadSubject{ExactMedia: filtered}, nil, registry)
		if err != nil || len(preloaded.screenshotSlots) != 2 {
			t.Fatalf("%s screenshot slots = %#v err=%v", target.name, preloaded, err)
		}
	}
}

func TestExactMediaForTrackerHostKeepsAllowedExistingHost(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		tracker string
		host    string
		allowed []string
	}{
		{
			tracker: "BHD",
			host:    "imgbb",
			allowed: []string{"imgbox", "imgbb", "pixhost", "bhd", "passtheimage"},
		},
		{
			tracker: "BHD",
			host:    "bhd",
			allowed: []string{"imgbox", "imgbb", "pixhost", "bhd", "passtheimage"},
		},
		{
			tracker: "DC",
			host:    "postimg",
			allowed: []string{"imgbox", "imgbb", "bhd", "imgur", "postimg", "sharex"},
		},
	} {
		t.Run(test.tracker+"-"+test.host, func(t *testing.T) {
			t.Parallel()
			registry := NewRegistry()
			if err := registry.RegisterDescriptor(Descriptor{
				Name:       test.tracker,
				Definition: stubDefinition{name: test.tracker},
				ImageHost:  &ImageHostPolicy{AllowedHosts: test.allowed},
			}); err != nil {
				t.Fatal(err)
			}
			allowed, err := ReusableImageHostAllowedWithRegistry(registry, config.Config{}, test.tracker, test.host, api.ImageHostOverrides{})
			if err != nil || !allowed {
				t.Fatalf("existing %s host accepted = %t err=%v", test.host, allowed, err)
			}
			pathValue := filepath.Join(t.TempDir(), "saved.png")
			exact := &api.ExactMediaAssets{
				Screenshots: []api.ScreenshotImage{{Path: pathValue, Purpose: api.ScreenshotPurposeFinal}},
				ScreenshotUploads: []api.UploadedImageLink{{
					ImagePath:  pathValue,
					Host:       test.host,
					UsageScope: "global",
					RawURL:     "https://images.example.invalid/saved.png",
				}},
			}
			filtered, err := exactMediaForTrackerHost(test.tracker, api.UploadSubject{ExactMedia: exact}, config.Config{}, config.TrackerConfig{}, registry)
			if err != nil || len(filtered.Screenshots) != 1 || len(filtered.ScreenshotUploads) != 1 {
				t.Fatalf("existing %s exact media = %#v err=%v", test.host, filtered, err)
			}
		})
	}
}

func TestExactMediaForOptionalTrackerExcludesAnotherTrackersOwnedHost(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	for _, descriptor := range []Descriptor{
		{Name: "ALPHA", Definition: stubDefinition{name: "ALPHA"}},
		{
			Name:       "LST",
			Definition: stubDefinition{name: "LST"},
			ImageHost:  &ImageHostPolicy{OwnedHosts: []string{"lostimg"}},
		},
	} {
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	globalPath := filepath.Join(root, "global.png")
	ownedPath := filepath.Join(root, "owned.png")
	exact := &api.ExactMediaAssets{
		Screenshots: []api.ScreenshotImage{
			{Path: globalPath, Purpose: api.ScreenshotPurposeFinal},
			{Path: ownedPath, Purpose: api.ScreenshotPurposeFinal},
		},
		ScreenshotUploads: []api.UploadedImageLink{
			{
				ImagePath:  globalPath,
				Host:       "imgbb",
				UsageScope: "global",
				RawURL:     "https://i.ibb.co/global.png",
			},
			{
				ImagePath:  ownedPath,
				Host:       "lostimg",
				UsageScope: "tracker:LST",
				RawURL:     "https://lostimg.example.invalid/owned.png",
			},
		},
	}
	filtered, err := exactMediaForTrackerHost("ALPHA", api.UploadSubject{ExactMedia: exact}, config.Config{}, config.TrackerConfig{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Screenshots) != 1 || filtered.Screenshots[0].Path != globalPath ||
		len(filtered.ScreenshotUploads) != 1 || filtered.ScreenshotUploads[0].Host != "imgbb" {
		t.Fatalf("optional tracker received another tracker's owned image: %#v", filtered)
	}
}

func TestExactMediaForRequiredTrackerDropsUnusableHostedLinks(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	for _, descriptor := range []Descriptor{
		{
			Name:       "ALPHA",
			Definition: stubDefinition{name: "ALPHA"},
			ImageHost:  &ImageHostPolicy{AllowedHosts: []string{"imgbb"}},
		},
		{
			Name:       "LST",
			Definition: stubDefinition{name: "LST"},
			ImageHost:  &ImageHostPolicy{OwnedHosts: []string{"lostimg"}},
		},
	} {
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	pathValue := filepath.Join(t.TempDir(), "owned.png")
	exact := &api.ExactMediaAssets{
		Screenshots: []api.ScreenshotImage{{Path: pathValue, Purpose: api.ScreenshotPurposeFinal}},
		ScreenshotUploads: []api.UploadedImageLink{{
			ImagePath:  pathValue,
			Host:       "lostimg",
			UsageScope: "tracker:LST",
			RawURL:     "https://lostimg.example.invalid/owned.png",
		}},
	}
	filtered, err := exactMediaForTrackerHost("ALPHA", api.UploadSubject{ExactMedia: exact}, config.Config{}, config.TrackerConfig{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Screenshots) != 1 || len(filtered.ScreenshotUploads) != 0 {
		t.Fatalf("required tracker retained unusable hosted link: %#v", filtered)
	}
}

func TestDescriptionSlotImageFailureReasonOmitsURLDetails(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		err    error
		reason string
	}{
		{
			name:   "status",
			err:    errors.New("status 403"),
			reason: "http_status_403",
		},
		{
			name:   "content type",
			err:    errors.New("invalid content-type \"text/html\""),
			reason: "non_image_content_type",
		},
		{
			name:   "private",
			err:    errors.New("blocked private image host \"localhost\""),
			reason: "invalid_or_nonpublic_url",
		},
		{
			name:   "request with secret URL",
			err:    errors.New("execute request: Get \"https://img.example/secret?token=sample\": refused"),
			reason: "request_failed",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := descriptionSlotImageFailureReason(testCase.err); got != testCase.reason {
				t.Fatalf("reason = %q, want %q", got, testCase.reason)
			}
		})
	}
}

func TestUploadSubjectForDescriptionPreservesPreparedMediaIdentity(t *testing.T) {
	t.Parallel()

	binding := trackerTestMediaBinding(filepath.Join(t.TempDir(), "source"))
	subject := api.DescriptionSubject{
		MediaBinding: binding,
		SourcePath:   binding.SourcePath,
		Disc:         api.DiscFacts{PrimaryDiscID: "disc-a"},
		Discs:        []api.DiscEvidenceResource{{ID: "disc-a", Name: "Disc 1"}},
	}

	projected := uploadSubjectForDescription(subject)
	if !projected.MediaBinding.Equal(binding) || projected.Disc.PrimaryDiscID != "disc-a" ||
		len(projected.Discs) != 1 || projected.Discs[0].ID != "disc-a" {
		t.Fatalf("upload subject = %#v", projected)
	}
}

func TestEnsureDescriptionImageHostRejectsInvalidPreparedMediaBinding(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "source.mkv")
	_, err := ensureDescriptionImageHostWithDataAndRegistry(
		context.Background(),
		"BTN",
		api.UploadSubject{
			MediaBinding: api.PreparedMediaBinding{SourcePath: sourcePath},
			SourcePath:   sourcePath,
		},
		config.Config{},
		config.TrackerConfig{ImageHost: "imgbox"},
		&imageHostResolutionRepo{stubRepo: &stubRepo{}},
		&stubImageService{},
		api.NopLogger{},
		descriptionAssetsTestRegistry(t),
		nil,
	)
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type imageHostResolutionRepo struct {
	*stubRepo
	replaceCalls int
}

func (r *imageHostResolutionRepo) ReplaceScreenshotSlots(
	ctx context.Context,
	binding api.PreparedMediaBinding,
	slots []api.ScreenshotSlot,
) error {
	r.replaceCalls++
	return r.stubRepo.ReplaceScreenshotSlots(ctx, binding, slots)
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
	meta := api.UploadSubject{
		MediaBinding:        trackerTestMediaBinding(sourcePath),
		SourcePath:          sourcePath,
		DescriptionOverride: "[center][img]http://8.8.8.8/image.gif[/img][/center]",
		ImageHostOverrides: api.ImageHostOverrides{
			SkipUpload: &skipUpload,
		},
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}}
	trackerCfg := config.TrackerConfig{ImageHost: "imgbox"}

	resolution, err := ensureDescriptionImageHostWithDataAndRegistry(
		context.Background(), "BTN", meta, cfg, trackerCfg, repo, &stubImageService{}, api.NopLogger{}, descriptionAssetsTestRegistry(t), nil,
	)
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

func TestOptionalImageHostPoliciesPreserveExistingPreferredHosts(t *testing.T) {
	t.Parallel()

	policy := imageHostPolicy{
		uploadHosts: []string{"imgbb", "pixhost"},
		preferred:   []string{"imgbb", "pixhost"},
	}

	selectionPolicy := optionalImageHostSelectionPolicy(policy)
	if got := strings.Join(selectionPolicy.preferred, ","); got != "imgbb,pixhost" {
		t.Fatalf("expected existing preferred hosts to be preserved, got %q", got)
	}

	uploadPolicy := optionalImageHostUploadPolicy(policy)
	if !uploadPolicy.required {
		t.Fatalf("expected upload policy to require preferred hosts, got %#v", uploadPolicy)
	}
	if got := strings.Join(uploadPolicy.preferred, ","); got != "imgbb,pixhost" {
		t.Fatalf("expected existing preferred hosts to drive upload policy, got %q", got)
	}
}

func TestOptionalImageHostPoliciesPrependExplicitPreferredHost(t *testing.T) {
	t.Parallel()

	policy := imageHostPolicy{
		uploadHosts: []string{"imgbb", "pixhost"},
		preferred:   []string{"imgbb", "pixhost"},
	}

	selectionPolicy := optionalImageHostSelectionPolicy(policy, "pixhost")
	if got := strings.Join(selectionPolicy.preferred, ","); got != "pixhost,imgbb" {
		t.Fatalf("expected explicit preferred host to prepend existing hosts, got %q", got)
	}

	uploadPolicy := optionalImageHostUploadPolicy(policy, "pixhost")
	if got := strings.Join(uploadPolicy.preferred, ","); got != "pixhost,imgbb" {
		t.Fatalf("expected upload policy to merge explicit and existing preferred hosts, got %q", got)
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
	meta := api.UploadSubject{SourcePath: "source.mkv"}
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
	meta := api.UploadSubject{SourcePath: "source.mkv"}
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
