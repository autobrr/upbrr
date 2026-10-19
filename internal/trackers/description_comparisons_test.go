// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveDescriptionComparisonsUsesEffectiveSourcesAndFinality(t *testing.T) {
	t.Parallel()
	block := "[comparison=Source A,Source B]https://images.example.test/a.png https://images.example.test/b.png[/comparison]"
	other := strings.ReplaceAll(block, "Source A", "Other source")
	source := filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv")
	repo := &stubRepo{descriptionOverride: block, overrideGroupKey: "unit3d"}
	registry := descriptionAssetsTestRegistry(t)
	service := NewServiceWithRegistryAndImages(config.Config{}, api.NopLogger{}, repo, registry, &stubImageService{repo: repo})
	subject := api.DescriptionSubject{
		SourcePath:   source,
		MediaBinding: trackerTestMediaBinding(source),
		DescriptionGroups: []api.DescriptionBuilderGroup{
			{
				GroupKey:    "final",
				Trackers:    []string{"ANT"},
				Description: other,
				Final:       true,
			},
			{
				GroupKey:    "editable",
				Trackers:    []string{"BTN"},
				Description: "[code]" + other + "[/code]\n" + block,
			},
			{
				GroupKey:    "isolated",
				Trackers:    []string{"NBL"},
				Description: other,
			},
		},
	}
	items, err := service.ResolveDescriptionComparisons(t.Context(), subject, []string{"AITHER", "HHD", "ANT", "BTN", "NBL"})
	if err != nil || len(items) != 2 || items[0].BBCode != block || !reflect.DeepEqual(items[0].Trackers, []string{"AITHER", "HHD", "BTN"}) ||
		items[1].BBCode != other || !reflect.DeepEqual(items[1].Trackers, []string{"NBL"}) {
		t.Fatalf("effective comparison sources=%+v err=%v", items, err)
	}
	subject.DescriptionGroups = []api.DescriptionBuilderGroup{{
		GroupKey:    "editable",
		Trackers:    []string{"BTN"},
		Description: strings.Replace(block, "https://images.example.test/b.png", "", 1),
	}}
	if _, err := service.ResolveDescriptionComparisons(t.Context(), subject, []string{"BTN"}); err == nil {
		t.Fatal("incomplete frame accepted")
	}
}

func TestDescriptionComparisonCloneDetachesSourceAndCompletion(t *testing.T) {
	t.Parallel()
	original := &api.ExactMediaAssets{DescriptionComparisons: []api.DescriptionComparison{{
		BBCode:     "original",
		Sources:    []string{"Source A"},
		ImageURLs:  []string{"https://images.example.test/a.png"},
		Trackers:   []string{"ANT"},
		Collection: &api.ImageCollection{Members: []api.ImageCollectionMember{{Identity: "original", Label: "Source A"}}},
	}}}
	clone := original.Clone()
	clone.DescriptionComparisons[0].Sources[0] = "changed"
	clone.DescriptionComparisons[0].ImageURLs[0] = "changed"
	clone.DescriptionComparisons[0].Trackers[0] = "changed"
	clone.DescriptionComparisons[0].Collection.Members[0].Identity = "changed"
	if original.DescriptionComparisons[0].Sources[0] != "Source A" || original.DescriptionComparisons[0].ImageURLs[0] != "https://images.example.test/a.png" ||
		original.DescriptionComparisons[0].Trackers[0] != "ANT" || original.DescriptionComparisons[0].Collection.Members[0].Identity != "original" {
		t.Fatalf("comparison clone retained mutable aliases: %+v", original)
	}
}

func TestResolveDescriptionComparisonsIgnoreHTMLLiterals(t *testing.T) {
	t.Parallel()
	block := "[comparison=Source A,Source B]https://images.example.test/a.png[/comparison]"
	outside := "[comparison=Outside A,Outside B]https://images.example.test/a.png https://images.example.test/b.png[/comparison]"
	registry := descriptionAssetsTestRegistry(t)
	service := NewServiceWithRegistryAndImages(config.Config{}, api.NopLogger{}, &stubRepo{}, registry, nil)
	for _, literal := range []string{
		"[code]" + block + "[/code]", "[pre]" + block + "[/pre]", "<pre>" + block + "</pre>", "<code>" + block + "</code>",
		"<pre><pre>" + block + "</pre></pre>", "<code><code>" + block + "</code></code>", "<pre>" + block, "<code>" + block,
		"<!--" + block + "-->", "<!--" + block, "<span data-example='" + block + "'>notes</span>",
	} {
		for _, prefix := range []string{"", outside + "\n"} {
			want := 0
			if prefix != "" {
				want = 1
			}
			subject := api.DescriptionSubject{
				SourcePath:        filepath.Join(t.TempDir(), "Synthetic.Release.2026-GRP.mkv"),
				DescriptionGroups: []api.DescriptionBuilderGroup{{Trackers: []string{"BTN"}, Description: prefix + literal}},
			}
			items, err := service.ResolveDescriptionComparisons(t.Context(), subject, []string{"BTN"})
			if err != nil || len(items) != want || len(items) > 0 && items[0].BBCode != outside {
				t.Fatalf("literal became upload authority: literal=%q items=%+v err=%v", literal, items, err)
			}
		}
	}
}

func TestDescriptionComparisonMaterializationPreservesRowsAndUsesCache(t *testing.T) {
	originalClient := newDescriptionSlotImageHTTPClient
	t.Cleanup(func() { newDescriptionSlotImageHTTPClient = originalClient })
	var requests []string
	newDescriptionSlotImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, req.URL.String())
			img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
			img.SetNRGBA(0, 0, color.NRGBA{R: uint8(len(requests)), A: 255})
			var data bytes.Buffer
			if err := png.Encode(&data, img); err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/png"}},
				Body:       io.NopCloser(bytes.NewReader(data.Bytes())),
			}, nil
		})}
	}
	root := t.TempDir()
	service := &Service{cfg: config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "upbrr.db")}}, logger: api.NopLogger{}}
	subject := api.DescriptionSubject{SourcePath: filepath.Join(root, "Synthetic.Release.2026-GRP.mkv"), ExactMedia: &api.ExactMediaAssets{ImageHostUploadSkipped: true}}
	item := api.DescriptionComparison{
		BBCode:    "synthetic block identity",
		Sources:   []string{"Source A", "Source B"},
		ImageURLs: []string{"https://8.8.8.8/a1.png", "https://8.8.8.8/b1.png", "https://8.8.8.8/a2.png", "https://8.8.8.8/b2.png"},
	}
	if input, err := service.MaterializeDescriptionComparison(t.Context(), subject, item); err != nil || len(input.Images) != 0 || len(requests) != 0 {
		t.Fatalf("skip downloaded sources: %+v %v", input, err)
	}
	subject.ExactMedia.ImageHostUploadSkipped = false
	first, err := service.MaterializeDescriptionComparison(t.Context(), subject, item)
	if err != nil || first.Columns != 2 || len(first.Images) != 4 || !reflect.DeepEqual(requests, item.ImageURLs) ||
		!reflect.DeepEqual(first.Labels, []string{"Source A", "Source B", "Source A", "Source B"}) {
		t.Fatalf("matrix=%+v requests=%v err=%v", first, requests, err)
	}
	for _, img := range first.Images {
		if img.ComparisonCount != 4 || img.ComparisonGroup != first.Generation || img.Purpose != api.ScreenshotPurposeFinal {
			t.Fatalf("lost comparison authority: %+v", img)
		}
	}
	subject.ExactMedia.ImageHostUploadSkipped = true
	second, err := service.MaterializeDescriptionComparison(t.Context(), subject, item)
	if err != nil || !reflect.DeepEqual(first, second) || len(requests) != 4 {
		t.Fatalf("cache changed or redownloaded: %+v %v", second, err)
	}
	otherSubject := subject
	otherSubject.SourcePath = filepath.Join(root, "other", filepath.Base(subject.SourcePath))
	otherSubject.ExactMedia = subject.ExactMedia.Clone()
	otherSubject.ExactMedia.ImageHostUploadSkipped = false
	other, err := service.MaterializeDescriptionComparison(t.Context(), otherSubject, item)
	if err != nil || len(other.Images) != 4 || len(requests) != 8 || other.Images[0].Path == first.Images[0].Path {
		t.Fatalf("equal basenames shared input cache: first=%+v other=%+v requests=%d err=%v", first, other, len(requests), err)
	}
	item.ImageURLs[0] = "https://127.0.0.1/private.png"
	subject.ExactMedia.ImageHostUploadSkipped = false
	if _, err := service.MaterializeDescriptionComparison(t.Context(), subject, item); err == nil || len(requests) != 8 || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("private URL reached transport or leaked: %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.MaterializeDescriptionComparison(canceled, subject, item); err == nil || len(requests) != 8 {
		t.Fatalf("canceled materialization made requests: %v", err)
	}
}

func TestDescriptionComparisonProxyUsesScopedPTPSession(t *testing.T) {
	originalClient := newDescriptionSlotImageHTTPClient
	originalLookup := descriptionSlotImageLookupIPAddrs
	t.Cleanup(func() {
		newDescriptionSlotImageHTTPClient = originalClient
		descriptionSlotImageLookupIPAddrs = originalLookup
	})
	var requests []string
	newDescriptionSlotImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, req.URL.String())
			session, err := req.Cookie("session")
			if err != nil || session.Value != "test-session" || req.URL.Scheme != "https" || req.URL.Hostname() != "passthepopcorn.me" {
				t.Fatal("normalized PTP request lacks its scoped session")
			}
			if req.URL.Path == "/upload.php" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Set-Cookie": []string{"img=test-image; Path=/; Secure"}},
					Body:       io.NopCloser(strings.NewReader("ok")),
				}, nil
			}
			cookie, err := req.Cookie("img")
			if err != nil || cookie.Value != "test-image" || req.URL.Path != "/i/synthetic.png" {
				t.Fatal("normalized comparison download lacks the refreshed image cookie")
			}
			var data bytes.Buffer
			if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/png"}},
				Body:       io.NopCloser(bytes.NewReader(data.Bytes())),
			}, nil
		})}
	}
	descriptionSlotImageLookupIPAddrs = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	root := t.TempDir()
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "upbrr.db")}}
	cookiePath, err := dbsvc.CookiePath(cfg.MainSettings.DBPath, "PTP.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cookiePath, []byte(`{"session":"test-session"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{cfg: cfg, logger: api.NopLogger{}}
	subject := api.DescriptionSubject{SourcePath: filepath.Join(root, "Synthetic.Release.2026-GRP.mkv"), ExactMedia: &api.ExactMediaAssets{ImageHostUploadSkipped: true}}
	item := api.DescriptionComparison{
		BBCode:    "synthetic proxy comparison",
		Sources:   []string{"Source"},
		ImageURLs: []string{"https://wsrv.nl/?url=" + url.QueryEscape("http://passthepopcorn.me/i/synthetic.png") + "&w=300"},
	}
	if input, err := service.MaterializeDescriptionComparison(t.Context(), subject, item); err != nil || len(input.Images) != 0 || len(requests) != 0 {
		t.Fatalf("skip performed proxy authentication: input=%+v requests=%d err=%v", input, len(requests), err)
	}
	subject.ExactMedia.ImageHostUploadSkipped = false
	if input, err := service.MaterializeDescriptionComparison(t.Context(), subject, item); err != nil || len(input.Images) != 1 || len(requests) != 2 {
		t.Fatalf("proxy comparison failed: input=%+v requests=%d err=%v", input, len(requests), err)
	}
}

func TestComparisonSourcePNGPreservesNativePNGAndConvertsJPEG(t *testing.T) {
	t.Parallel()
	for _, nativePNG := range []bool{false, true} {
		t.Run("nativePNG="+strconv.FormatBool(nativePNG), func(t *testing.T) {
			img := image.NewNRGBA64(image.Rect(0, 0, 2, 2))
			img.SetNRGBA64(0, 0, color.NRGBA64{R: 12345, A: 65535})
			var encoded bytes.Buffer
			var err error
			if nativePNG {
				err = png.Encode(&encoded, img)
			} else {
				err = jpeg.Encode(&encoded, img, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			source, dest := filepath.Join(root, "source.image"), filepath.Join(root, "comparison.png")
			if err := os.WriteFile(source, encoded.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := comparisonSourcePNG(source, dest); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := png.Decode(bytes.NewReader(data)); err != nil || nativePNG && !bytes.Equal(encoded.Bytes(), data) {
				t.Fatalf("invalid PNG or changed native PNG bytes: %v", err)
			}
		})
	}
}
