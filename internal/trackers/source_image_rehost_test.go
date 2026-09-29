// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type menuImageTestDefinition struct{ stubDefinition }

func (menuImageTestDefinition) UsesMenuImages() bool { return true }

type menuScreenshotTestDefinition struct{ menuImageTestDefinition }

func (menuScreenshotTestDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeScreenshots
}

type nativeSourceImageTestDefinition struct {
	stubDefinition
	reusableURL string
}

func (d nativeSourceImageTestDefinition) SourceOnlyImageReusable(rawURL string, _ []api.TrackerMetadata) bool {
	return rawURL == d.reusableURL
}

func TestRehostSourceOnlyComparisonImagesPreservesMarkupAndReusesUploads(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "Example.Release.2026-GRP.mkv")
	urls := []string{
		"https://wsrv.nl/?url=https%3A%2F%2Fexample.org%2Fone.png",
		"https://passthepopcorn.me/static/two.jpg",
	}
	description := "Before\n[comparison=Source|Encode]\n[url=" + urls[0] + "][img]" + urls[0] + "[/img][/url]\n[img]" + urls[1] + "[/img]\n[/comparison]\nAfter"
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "upbrr.db")},
		ImageHosting: config.ImageHostingConfig{Host1: "imgbox"},
	}
	tmpRoot, err := dbsvc.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatal(err)
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, source, api.ReleaseInfo{})
	if err != nil {
		t.Fatal(err)
	}
	for index, rawURL := range urls {
		pathValue := localTrackerArtifactPaths(filepath.Join(tmpDir, "ptp"), rawURL, index)[0]
		if err := os.MkdirAll(filepath.Dir(pathValue), 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(pathValue)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	repo := &stubRepo{}
	images := &stubImageService{repo: repo}
	registry := descriptionAssetsTestRegistry(t)
	if err := registry.RegisterDescriptor(Descriptor{Name: "TL", Definition: stubDefinition{name: "TL"}}); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, repo, registry, images)
	skipUpload := true
	meta := api.UploadSubject{
		MediaBinding:       trackerTestMediaBinding(source),
		SourcePath:         source,
		Release:            api.ReleaseInfo{},
		TrackerData:        []api.TrackerMetadata{{Tracker: "PTP", ImageURLs: urls}},
		ExactMedia:         &api.ExactMediaAssets{},
		ImageHostOverrides: api.ImageHostOverrides{SkipUpload: &skipUpload},
	}
	preloaded := &preloadedDescriptionAssetData{trackerRecords: meta.TrackerData}
	want := strings.ReplaceAll(strings.ReplaceAll(description, urls[0], "https://imgbox/0.png"), urls[1], "https://imgbox/1.png")
	for attempt := range 2 {
		assets := &DescriptionAssets{Description: description}
		if err := service.rehostSourceOnlyDescriptionImages(t.Context(), "BTN", meta, config.TrackerConfig{}, assets, preloaded); err != nil {
			t.Fatal(err)
		}
		if assets.Description != want {
			t.Fatalf("attempt %d comparison markup changed:\n%s", attempt, assets.Description)
		}
	}
	optionalAssets := &DescriptionAssets{Description: description}
	if err := service.rehostSourceOnlyDescriptionImages(t.Context(), "TL", meta, config.TrackerConfig{}, optionalAssets, preloaded); err != nil {
		t.Fatal(err)
	}
	optionalWant := strings.ReplaceAll(strings.ReplaceAll(description, urls[0], "https://example.org/one.png"), urls[1], "https://imgbox/1.png")
	if optionalAssets.Description != optionalWant {
		t.Fatalf("optional tracker comparison markup changed:\n%s", optionalAssets.Description)
	}
	if len(images.calls) != 1 || images.calls[0] != "imgbox" {
		t.Fatalf("comparison images uploaded more than once: %#v", images.calls)
	}
}

func TestComparisonProxyUsesSupportedSourceHostWithoutUpload(t *testing.T) {
	t.Parallel()
	imgboxProxy := "https://wsrv.nl/?n=-1&ll&url=https%3A%2F%2Fthumbs2.imgbox.com%2F13%2Fae%2Fshot_t.png"
	blutopiaProxy := "https://wsrv.aither.cc/?n=-1&ll&url=https%3A%2F%2Fimg.blutopia.cc%2F2026%2F06%2F14%2Fshot.png"
	for _, test := range []struct {
		tracker string
		proxy   string
		want    string
	}{
		{
tracker: "BTN",
 proxy: imgboxProxy,
 want: "https://images2.imgbox.com/13/ae/shot_o.png",
},
		{
tracker: "AITHER",
 proxy: blutopiaProxy,
 want: "https://img.blutopia.cc/2026/06/14/shot.png",
},
	} {
		block := "[comparison=Source|Encode]\r\n[img]" + test.proxy + "[/img]\r\n[/comparison]"
		assets := &DescriptionAssets{Description: "Notes\n" + block + "\nMore notes"}
		service := &Service{registry: descriptionAssetsTestRegistry(t)}
		if err := service.rehostSourceOnlyDescriptionImages(t.Context(), test.tracker, api.UploadSubject{}, config.TrackerConfig{}, assets, nil); err != nil {
			t.Fatalf("%s direct source: %v", test.tracker, err)
		}
		want := "Notes\n" + strings.ReplaceAll(block, test.proxy, test.want) + "\nMore notes"
		if assets.Description != want {
			t.Fatalf("%s comparison = %q, want %q", test.tracker, assets.Description, want)
		}
	}
}

func TestComparisonProxyDownloadsSourceWhenRehostingIsRequired(t *testing.T) {
	const proxy = "https://wsrv.aither.cc/?url=https%3A%2F%2Fimg.blutopia.cc%2Fshot.png"
	const direct = "https://img.blutopia.cc/shot.png"
	previousClient := newDescriptionSlotImageHTTPClient
	previousLookup := descriptionSlotImageLookupIPAddrs
	requests := 0
	newDescriptionSlotImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			if req.URL.String() != direct {
				t.Errorf("requested %q, want source %q", req.URL, direct)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/png"}},
				Body:       io.NopCloser(strings.NewReader("\x89PNG\r\n\x1a\n\x00\x00\x00\x00")),
				Request:    req,
			}, nil
		})}
	}
	descriptionSlotImageLookupIPAddrs = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	t.Cleanup(func() {
		newDescriptionSlotImageHTTPClient = previousClient
		descriptionSlotImageLookupIPAddrs = previousLookup
	})

	root := t.TempDir()
	source := filepath.Join(root, "Example.Release.2026-GRP.mkv")
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "upbrr.db")},
		ImageHosting: config.ImageHostingConfig{Host1: "imgbox"},
	}
	repo := &stubRepo{}
	images := &stubImageService{repo: repo}
	service := NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, repo, descriptionAssetsTestRegistry(t), images)
	description := "[comparison=A|B]\n[img]" + proxy + "[/img]\n[/comparison]"
	assets := &DescriptionAssets{Description: description}
	meta := api.UploadSubject{SourcePath: source, MediaBinding: trackerTestMediaBinding(source)}
	if err := service.rehostSourceOnlyDescriptionImages(t.Context(), "BTN", meta, config.TrackerConfig{}, assets, nil); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(images.calls) != 1 || images.calls[0] != "imgbox" ||
		assets.Description != strings.ReplaceAll(description, proxy, "https://imgbox/0.png") {
		t.Fatalf("source rehosting requests=%d calls=%#v description=%q", requests, images.calls, assets.Description)
	}
}

func TestRehostPTPComparisonImageWithoutCachedArtifact(t *testing.T) {
	const rawURL = "https://passthepopcorn.me/i/shot.gif"
	originalClient := newDescriptionSlotImageHTTPClient
	originalLookup := descriptionSlotImageLookupIPAddrs
	requests := 0
	newDescriptionSlotImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/upload.php" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Set-Cookie": []string{"img=test-image; Path=/; Secure"}},
					Body:       io.NopCloser(strings.NewReader("ok")),
					Request:    req,
				}, nil
			}
			requests++
			if req.URL.String() != rawURL {
				t.Fatalf("unexpected image URL %q", req.URL)
			}
			imageCookie, imageErr := req.Cookie("img")
			sessionCookie, sessionErr := req.Cookie("session")
			if imageErr != nil || imageCookie.Value != "test-image" || sessionErr != nil || sessionCookie.Value != "test-session" ||
				req.Header.Get("Apiuser") != "" || req.Header.Get("Apikey") != "" {
				t.Fatalf("PTP image request missing site auth: headers=%v", req.Header)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"image/gif"}},
				Body:       io.NopCloser(strings.NewReader("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")),
				Request:    req,
			}, nil
		})}
	}
	descriptionSlotImageLookupIPAddrs = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	t.Cleanup(func() {
		newDescriptionSlotImageHTTPClient = originalClient
		descriptionSlotImageLookupIPAddrs = originalLookup
	})

	root := t.TempDir()
	source := filepath.Join(root, "Example.Release.2026-GRP.mkv")
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "upbrr.db")},
		ImageHosting: config.ImageHostingConfig{Host1: "imgbox"},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "test-user", PTPAPIKey: "test-key"},
		}},
	}
	cookiePath, err := dbsvc.CookiePath(cfg.MainSettings.DBPath, "PTP.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cookiePath, []byte(`{"session":"test-session"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := &stubRepo{}
	images := &stubImageService{repo: repo}
	registry := descriptionAssetsTestRegistry(t)
	service := NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, repo, registry, images)
	description := "[comparison=A|B]\n[img]" + rawURL + "[/img]\n[/comparison]"
	assets := &DescriptionAssets{Description: description}
	meta := api.UploadSubject{
		SourcePath:   source,
		MediaBinding: trackerTestMediaBinding(source),
		TrackerData:  []api.TrackerMetadata{{Tracker: "PTP", Description: description}},
	}
	if err := service.rehostSourceOnlyDescriptionImages(t.Context(), "BTN", meta, config.TrackerConfig{}, assets, nil); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || assets.Description != strings.ReplaceAll(description, rawURL, "https://imgbox/0.png") {
		t.Fatalf("uncached PTP comparison image: requests=%d description=%q", requests, assets.Description)
	}
	proxyURL := "https://wsrv.nl/?url=" + url.QueryEscape(rawURL)
	proxyDescription := "[comparison=A|B]\n[img]" + proxyURL + "[/img]\n[/comparison]"
	proxyAssets := &DescriptionAssets{Description: proxyDescription}
	if err := service.rehostSourceOnlyDescriptionImages(t.Context(), "BTN", meta, config.TrackerConfig{}, proxyAssets, nil); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || proxyAssets.Description != strings.ReplaceAll(proxyDescription, proxyURL, "https://imgbox/0.png") {
		t.Fatalf("PTP comparison proxy source: requests=%d description=%q", requests, proxyAssets.Description)
	}
}

func TestPTPDescriptionImageAuthIsNotSentToRedirectHost(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "upbrr.db")},
		Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
			"PTP": {PTPAPIUser: "test-user", PTPAPIKey: "test-key"},
		}},
	}
	cookiePath, err := dbsvc.CookiePath(cfg.MainSettings.DBPath, "PTP.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cookiePath, []byte(`{"session":"test-session"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client := PTPDescriptionImageHTTPClient(t.Context(), &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() == "passthepopcorn.me" {
			if req.URL.Path == "/upload.php" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Set-Cookie": []string{"img=test-image; Path=/; Secure"}},
					Body:       io.NopCloser(strings.NewReader("ok")),
					Request:    req,
				}, nil
			}
			imageCookie, imageErr := req.Cookie("img")
			sessionCookie, sessionErr := req.Cookie("session")
			if imageErr != nil || imageCookie.Value != "test-image" || sessionErr != nil || sessionCookie.Value != "test-session" ||
				req.Header.Get("Apiuser") != "" || req.Header.Get("Apikey") != "" {
				t.Fatal("PTP image request missing site auth")
			}
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://images.example/shot.png"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
		if req.Header.Get("Cookie") != "" {
			t.Fatalf("PTP auth escaped on redirect: headers=%v", req.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    req,
		}, nil
	})}, cfg, api.NopLogger{})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://passthepopcorn.me/i/shot.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestRehostSourceOnlyDescriptionImagesKeepsOriginTrackerLinks(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		tracker string
		link    string
	}{
		{tracker: "PTP", link: "https://passthepopcorn.me/static/shot.jpg"},
		{tracker: "AITHER", link: "https://wsrv.aither.cc/?url=https%3A%2F%2Fimg.blutopia.cc%2Fshot.png"},
	} {
		description := "[comparison=A|B]\n[img]" + testCase.link + "[/img]\n[/comparison]"
		assets := &DescriptionAssets{Description: description}
		registry := NewRegistry()
		if err := registry.RegisterDescriptor(Descriptor{
			Name:       testCase.tracker,
			Definition: nativeSourceImageTestDefinition{stubDefinition: stubDefinition{name: testCase.tracker}, reusableURL: testCase.link},
		}); err != nil {
			t.Fatal(err)
		}
		if err := (&Service{registry: registry}).rehostSourceOnlyDescriptionImages(t.Context(), testCase.tracker, api.UploadSubject{}, config.TrackerConfig{}, assets, nil); err != nil {
			t.Fatalf("%s origin description: %v", testCase.tracker, err)
		}
		want := description
		if testCase.tracker == "AITHER" {
			want = strings.ReplaceAll(description, testCase.link, "https://img.blutopia.cc/shot.png")
		}
		if assets.Description != want {
			t.Fatalf("%s origin description changed: %q", testCase.tracker, assets.Description)
		}
	}
}

func TestPTPDoesNotRequireUnusedDVDMenuUpload(t *testing.T) {
	t.Parallel()
	assets := &DescriptionAssets{MenuImages: []api.ScreenshotImage{{Path: "unused-menu.png", Purpose: api.ScreenshotPurposeMenu}}}
	if err := (&Service{}).rehostSourceOnlyDescriptionImages(t.Context(), "PTP", api.UploadSubject{}, config.TrackerConfig{}, assets, nil); err != nil {
		t.Fatalf("PTP unused DVD menu: %v", err)
	}
}

func TestScreenshotModePreparationRehostsDVDMenu(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.mkv")
	menuPath := filepath.Join(root, "menu.png")
	if err := os.WriteFile(menuPath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:              "ANT",
		Definition:        menuScreenshotTestDefinition{menuImageTestDefinition{stubDefinition{name: "ANT"}}},
		UploadContentMode: UploadContentModeScreenshots,
		ImageHost:         &ImageHostPolicy{AllowedHosts: []string{"imgbox"}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "upbrr.db")}}
	repo := &stubRepo{}
	images := &stubImageService{repo: repo}
	service := NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, repo, registry, images)
	skipUpload := true
	meta := api.UploadSubject{
		SourcePath:   sourcePath,
		MediaBinding: trackerTestMediaBinding(sourcePath),
		ExactMedia: &api.ExactMediaAssets{DVDMenus: []api.DVDMenuCaptureImage{{
			Path:    menuPath,
			Purpose: api.ScreenshotPurposeMenu,
			RawURL:  "https://passthepopcorn.me/static/menu.png",
		}}},
		ImageHostOverrides: api.ImageHostOverrides{SkipUpload: &skipUpload},
	}
	prepared := service.prepareUploadContent(t.Context(), "ANT", meta, config.TrackerConfig{}, nil, nil)
	if prepared.State != preparedUploadContentReady || prepared.Assets == nil || len(prepared.Assets.MenuImages) != 1 ||
		prepared.Assets.MenuImages[0].RawURL != "https://imgbox/0.png" || len(images.calls) != 1 {
		t.Fatalf("screenshot-mode menu preparation = %#v, calls=%#v", prepared, images.calls)
	}
}

func TestRehostUnhostedDVDMenuImage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.mkv")
	menuPath := filepath.Join(root, "menu.png")
	file, err := os.Create(menuPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "upbrr.db")}}
	repo := &stubRepo{}
	images := &stubImageService{repo: repo}
	registry := descriptionAssetsTestRegistry(t)
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "BHD",
		Definition: menuImageTestDefinition{stubDefinition{name: "BHD"}},
		ImageHost:  &ImageHostPolicy{AllowedHosts: []string{"imgbox"}},
	}); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithRegistryAndImages(cfg, api.NopLogger{}, repo, registry, images)
	skipUpload := true
	meta := api.UploadSubject{
		SourcePath:         sourcePath,
		MediaBinding:       trackerTestMediaBinding(sourcePath),
		ExactMedia:         &api.ExactMediaAssets{},
		ImageHostOverrides: api.ImageHostOverrides{SkipUpload: &skipUpload},
	}
	assets := &DescriptionAssets{MenuImages: []api.ScreenshotImage{{Path: menuPath, Purpose: api.ScreenshotPurposeMenu}}}
	if err := service.rehostSourceOnlyDescriptionImages(t.Context(), "BHD", meta, config.TrackerConfig{}, assets, nil); err != nil {
		t.Fatal(err)
	}
	if len(images.calls) != 1 || images.calls[0] != "imgbox" || assets.MenuImages[0].RawURL != "https://imgbox/0.png" {
		t.Fatalf("rehosted DVD menu = %#v, calls=%#v", assets.MenuImages, images.calls)
	}
	reused := &DescriptionAssets{MenuImages: []api.ScreenshotImage{{Path: menuPath, Purpose: api.ScreenshotPurposeMenu}}}
	if err := service.rehostSourceOnlyDescriptionImages(t.Context(), "BHD", meta, config.TrackerConfig{}, reused, nil); err != nil {
		t.Fatal(err)
	}
	if len(images.calls) != 1 || reused.MenuImages[0].RawURL != assets.MenuImages[0].RawURL {
		t.Fatalf("DVD menu upload was not reused: %#v, calls=%#v", reused.MenuImages, images.calls)
	}
}

func TestSourceOnlyDescriptionImageURLsDoesNotTreatPlainLinksAsImages(t *testing.T) {
	t.Parallel()
	description := "[url=https://passthepopcorn.me/torrents.php]PTP[/url]\n" +
		"[url=https://wsrv.nl/?url=docs]proxy details[/url]\n" +
		"[url=https://passthepopcorn.me/torrents.php][img]https://pixhost.to/images/shot.png[/img][/url]\n" +
		"[img]https://wsrv.nl/?url=shot[/img]"
	urls := sourceOnlyDescriptionImageURLs(description)
	if len(urls) != 1 || urls[0] != "https://wsrv.nl/?url=shot" {
		t.Fatalf("source-only image URLs = %#v", urls)
	}
}

func TestSourceOnlyDescriptionImageURLsFindsBareComparisonImages(t *testing.T) {
	t.Parallel()
	first := "https://passthepopcorn.me/static/image.jpg"
	second := "https://wsrv.nl/?url=https%3A%2F%2Fexample.org%2Fimage.png"
	description := "Before\n[comparison=A|B]\n" + first + "\n" + second + "\n[/comparison]\nAfter"
	urls := sourceOnlyDescriptionImageURLs(description)
	if len(urls) != 2 || urls[0] != first || urls[1] != second {
		t.Fatalf("bare comparison URLs = %#v", urls)
	}
	want := "Before\n[comparison=A|B]\nhttps://imgbox/one.jpg\nhttps://imgbox/two.png\n[/comparison]\nAfter"
	got := rewriteSourceOnlyDescriptionImageURLs(description, map[string]string{
		first:  "https://imgbox/one.jpg",
		second: "https://imgbox/two.png",
	})
	if got != want {
		t.Fatalf("bare comparison rewrite = %q", got)
	}
}

func TestRewriteSourceOnlyDescriptionImageURLsHandlesProxyVariants(t *testing.T) {
	t.Parallel()
	short := "https://wsrv.nl/?url=https%3A%2F%2Fexample.org%2Fimage.png"
	long := short + "&w=350"
	description := "[comparison=A|B]\n[img]" + short + "[/img]\n[img]" + long + "[/img]\n[/comparison]"
	want := "[comparison=A|B]\n[img]https://imgbox/one.png[/img]\n[img]https://imgbox/two.png[/img]\n[/comparison]"
	got := rewriteSourceOnlyDescriptionImageURLs(description, map[string]string{
		short: "https://imgbox/one.png",
		long:  "https://imgbox/two.png",
	})
	if got != want {
		t.Fatalf("proxy variant rewrite = %q", got)
	}
}

func TestUploadableCachedSourceImagePathKeepsAVIF(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cached := filepath.Join(root, "cached-image")
	payload := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f', 0, 0, 0, 0, 'a', 'v', 'i', 'f'}
	if err := os.WriteFile(cached, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "upload.avif")
	if got := uploadableCachedSourceImagePath(cached, filepath.Join(root, "upload.png")); got != want {
		t.Fatalf("cached AVIF upload path = %q, want %q", got, want)
	}
}

func TestPreferredFullSizeSourceURL(t *testing.T) {
	t.Parallel()
	full := "https://img.onlyimage.org/shot.png"
	proxy := "https://wsrv.nl/?url=" + url.QueryEscape("https://img.onlyimage.org/shot.md.png") + "&w=350"
	if got := preferredFullSizeSourceURL(proxy); got != full {
		t.Fatalf("full-size source = %q, want %q", got, full)
	}
	if got := preferredFullSizeSourceURL("https://wsrv.nl/?url=" + url.QueryEscape(full)); got != full {
		t.Fatalf("already full-size source = %q", got)
	}
	if got := preferredFullSizeSourceURL("https://passthepopcorn.me/static/shot.jpg"); got != "" {
		t.Fatalf("direct PTP source = %q", got)
	}
	blu := "https://wsrv.aither.cc/?url=" + url.QueryEscape("https://img.blutopia.cc/shot.md.png") + "&w=350"
	got := preferredFullSizeSourceURL(blu)
	if got != "https://img.blutopia.cc/shot.png" {
		t.Fatalf("full-size Aither source = %q", got)
	}
}
