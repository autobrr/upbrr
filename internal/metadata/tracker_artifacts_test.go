// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	trackerdata "github.com/autobrr/upbrr/internal/trackers/data"
	"github.com/autobrr/upbrr/pkg/api"
)

type artifactRoundTripFunc func(*http.Request) (*http.Response, error)

func (f artifactRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPersistUnit3DArtifactsLogsDownloadReasonWithoutImageURL(t *testing.T) {
	previousHTTPClient := newUnit3DArtifactImageHTTPClient
	newUnit3DArtifactImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: artifactRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("download failed for %s", req.URL.String())
		})}
	}
	t.Cleanup(func() { newUnit3DArtifactImageHTTPClient = previousHTTPClient })
	logger := &recordingLogger{}
	tempDir := t.TempDir()
	svc := &Service{
		cfg:    config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")}},
		logger: logger,
	}
	imageURL := "https://93.184.216.34/private-image.png?marker=example"
	successful := svc.persistTrackerArtifacts(t.Context(), preparationstate.State{SourcePath: filepath.Join(tempDir, "source")},
		"AITHER", trackerdata.Result{Validated: []bbcode.Image{{RawURL: imageURL}}}, true)
	if len(successful) != 0 || len(logger.warnings) != 1 ||
		!strings.Contains(logger.warnings[0], "index=1 reason=request_failed") ||
		strings.Contains(logger.warnings[0], "private-image") || strings.Contains(logger.warnings[0], "marker=example") {
		t.Fatalf("expected URL-free download failure reason, got warnings=%v successful=%v", logger.warnings, successful)
	}
}

func TestTrackerArtifactDownloadUsesWsrvSource(t *testing.T) {
	previousValidate := validateTrackerArtifactImageURL
	validateTrackerArtifactImageURL = func(_ context.Context, rawURL string) error {
		if strings.Contains(rawURL, "wsrv") {
			t.Errorf("validated proxy instead of source: %q", rawURL)
		}
		return nil
	}
	t.Cleanup(func() { validateTrackerArtifactImageURL = previousValidate })
	for index, test := range []struct {
		proxy string
		want  string
	}{
		{
			proxy: "https://wsrv.nl/?n=-1&ll&url=https%3A%2F%2Fthumbs2.imgbox.com%2F13%2Fae%2Fshot_t.png",
			want:  "https://images2.imgbox.com/13/ae/shot_o.png",
		},
		{
			proxy: "https://wsrv.aither.cc/?n=-1&ll&url=https%3A%2F%2Fimg.blutopia.cc%2F2026%2F06%2F14%2Fshot.png",
			want:  "https://img.blutopia.cc/2026/06/14/shot.png",
		},
	} {
		client := &http.Client{Transport: artifactRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != test.want {
				t.Errorf("download URL = %q, want %q", req.URL, test.want)
			}
			payload := trackerDataPNG1x1()
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(payload)),
				ContentLength: int64(len(payload)),
				Request:       req,
			}, nil
		})}
		outPath := filepath.Join(t.TempDir(), fmt.Sprintf("source-%d.png", index))
		if reason := downloadImage(t.Context(), client, test.proxy, outPath, 0, false); reason != "" {
			t.Fatalf("source download failed: %s", reason)
		}
		if info, err := os.Stat(outPath); err != nil || info.Size() == 0 {
			t.Fatalf("source image was not saved: info=%v err=%v", info, err)
		}
	}
}

func TestPersistTrackerArtifactsReplacesLegacyProxyCache(t *testing.T) {
	const proxyURL = "https://wsrv.aither.cc/?w=350&url=https%3A%2F%2F93.184.216.34%2Ffull.png"
	const directURL = "https://93.184.216.34/full.png"
	previousClient := newUnit3DArtifactImageHTTPClient
	previousValidate := validateTrackerArtifactImageURL
	var requests atomic.Int32
	newUnit3DArtifactImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: artifactRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests.Add(1)
			if req.URL.String() != directURL {
				t.Errorf("download URL = %q, want %q", req.URL, directURL)
			}
			payload := trackerDataPNG1x1()
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(payload)),
				ContentLength: int64(len(payload)),
				Request:       req,
			}, nil
		})}
	}
	validateTrackerArtifactImageURL = func(_ context.Context, rawURL string) error {
		if rawURL != directURL {
			t.Errorf("validated URL = %q, want %q", rawURL, directURL)
		}
		return nil
	}
	t.Cleanup(func() {
		newUnit3DArtifactImageHTTPClient = previousClient
		validateTrackerArtifactImageURL = previousValidate
	})

	root := t.TempDir()
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "db.sqlite")}}
	meta := preparationstate.State{SourcePath: filepath.Join(root, "source")}
	tmpRoot, err := dbsvc.Subdir(cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		t.Fatal(err)
	}
	tmpDir, _, err := paths.ReleaseTempDir(tmpRoot, meta, meta.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(tmpDir, "aither", buildImageFilename(proxyURL, 0))
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("old resized proxy bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &Service{cfg: cfg, logger: api.NopLogger{}}
	successful := svc.persistTrackerArtifacts(t.Context(), meta, "AITHER", trackerdata.Result{
		Validated: []bbcode.Image{{RawURL: proxyURL}},
	}, true)
	if len(successful) != 1 || successful[0] != proxyURL || requests.Load() != 1 {
		t.Fatalf("proxy refresh: successful=%v requests=%d", successful, requests.Load())
	}
	assertTrackerArtifactExists(t, cfg, meta.SourcePath, "AITHER", directURL, 0)
}

func TestPTPTrackerImageLookupPersistsAuthenticatedArtifact(t *testing.T) {
	const originalURL = "http://passthepopcorn.me/i/shot.png"
	const requestURL = "https://passthepopcorn.me/i/shot.png"
	previousClient := newUnit3DArtifactImageHTTPClient
	previousValidate := validateTrackerArtifactImageURL
	newUnit3DArtifactImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: artifactRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/upload.php" {
				if cookie, err := req.Cookie("session"); err != nil || cookie.Value != "test-session" || req.Header.Get("Apiuser") != "" || req.Header.Get("Apikey") != "" {
					t.Errorf("PTP web session request missing saved cookie or sent API auth")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Set-Cookie": []string{"img=test-image; Path=/; Secure"}},
					Body:       io.NopCloser(strings.NewReader("ok")),
					Request:    req,
				}, nil
			}
			imageCookie, imageErr := req.Cookie("img")
			sessionCookie, sessionErr := req.Cookie("session")
			if req.URL.String() != requestURL || imageErr != nil || imageCookie.Value != "test-image" ||
				sessionErr != nil || sessionCookie.Value != "test-session" || req.Header.Get("Apiuser") != "" || req.Header.Get("Apikey") != "" {
				t.Errorf("PTP artifact request = %s headers=%v", req.URL, req.Header)
			}
			payload := trackerDataPNG1x1()
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(payload)),
				ContentLength: int64(len(payload)),
				Request:       req,
			}, nil
		})}
	}
	validateTrackerArtifactImageURL = func(_ context.Context, rawURL string) error {
		if rawURL != requestURL {
			t.Errorf("validated PTP URL = %q", rawURL)
		}
		return nil
	}
	t.Cleanup(func() {
		newUnit3DArtifactImageHTTPClient = previousClient
		validateTrackerArtifactImageURL = previousValidate
	})

	root := t.TempDir()
	sourcePath := filepath.Join(root, "Example.Release.2026-GRP.mkv")
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
	repo := &fakeRepo{}
	lookup := &stubTrackerLookup{results: map[string]trackerdata.Result{
		"PTP": {
			TrackerID:   "303",
			Description: "PTP description",
			Images:      []bbcode.Image{{RawURL: originalURL}},
		},
	}}
	service := NewService(repo, WithConfig(cfg), WithTrackerDataLookup(lookup), WithTrackerRegistry(trackerDataTestRegistry(t)))
	state, err := service.collectTrackerEvidence(t.Context(), preparationstate.State{
		SourcePath: sourcePath,
		TrackerIDs: map[string]string{"ptp": "303"},
		Policy:     preparationstate.CollectionPolicy{KeepImages: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, found := trackerRecordFor(state.TrackerData, "PTP")
	if !found || len(record.ImageURLs) != 1 || record.ImageURLs[0] != originalURL || len(repo.trackerMetadata) != 1 ||
		len(repo.trackerMetadata[0].ImageURLs) != 1 || repo.trackerMetadata[0].ImageURLs[0] != originalURL {
		t.Fatalf("PTP image metadata: record=%#v saved=%#v", record, repo.trackerMetadata)
	}
	assertTrackerArtifactExists(t, cfg, sourcePath, "PTP", originalURL, 0)
}

func TestPersistUnit3DArtifactsDoesNotReuseDifferentURLWithSameBasename(t *testing.T) {
	var requests atomic.Int32
	previousHTTPClient := newUnit3DArtifactImageHTTPClient
	newUnit3DArtifactImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: artifactRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests.Add(1)
			payload := trackerDataPNG1x1()
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(payload)),
				ContentLength: int64(len(payload)),
				Request:       req,
			}, nil
		})}
	}
	t.Cleanup(func() { newUnit3DArtifactImageHTTPClient = previousHTTPClient })
	tempDir := t.TempDir()
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")}}
	svc := &Service{cfg: cfg, logger: api.NopLogger{}}
	meta := preparationstate.State{SourcePath: filepath.Join(tempDir, "source")}
	oldURL := "https://93.184.216.34/a/shot.png"
	newURL := "https://93.184.216.34/b/shot.png"
	if buildImageFilename(oldURL, 0) == buildImageFilename(newURL, 0) {
		t.Fatal("different image URLs share an artifact filename")
	}
	for _, imageURL := range []string{oldURL, newURL} {
		successful := svc.persistTrackerArtifacts(t.Context(), meta, "AITHER", trackerdata.Result{
			Validated: []bbcode.Image{{RawURL: imageURL}},
		}, true)
		if len(successful) != 1 || successful[0] != imageURL {
			t.Fatalf("persist %q: successful=%v", imageURL, successful)
		}
		assertTrackerArtifactExists(t, cfg, meta.SourcePath, "AITHER", imageURL, 0)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("different URLs fetched %d times, want 2", got)
	}
}

func TestPersistUnit3DArtifactsMaxConcurrentImageDownloads(t *testing.T) {
	const imageCount = 12

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32

	png1x1 := []byte{
		137, 80, 78, 71, 13, 10, 26, 10,
		0, 0, 0, 13, 73, 72, 68, 82,
		0, 0, 0, 1, 0, 0, 0, 1,
		8, 6, 0, 0, 0, 31, 21, 196,
		137, 0, 0, 0, 13, 73, 68, 65,
		84, 120, 156, 99, 248, 15, 4, 0,
		9, 251, 3, 253, 160, 158, 134, 129,
		0, 0, 0, 0, 73, 69, 78, 68,
		174, 66, 96, 130,
	}

	previousHTTPClient := newUnit3DArtifactImageHTTPClient
	newUnit3DArtifactImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: artifactRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/img" {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(bytes.NewReader(nil)),
					Request:    req,
				}, nil
			}

			current := inFlight.Add(1)
			for {
				peak := maxInFlight.Load()
				if current <= peak {
					break
				}
				if maxInFlight.CompareAndSwap(peak, current) {
					break
				}
			}
			defer inFlight.Add(-1)

			time.Sleep(75 * time.Millisecond)
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Type": []string{"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(png1x1)),
				ContentLength: int64(len(png1x1)),
				Request:       req,
			}, nil
		})}
	}
	t.Cleanup(func() {
		newUnit3DArtifactImageHTTPClient = previousHTTPClient
	})

	validated := make([]bbcode.Image, 0, imageCount)
	for range imageCount {
		validated = append(validated, bbcode.Image{RawURL: "http://93.184.216.34/img"})
	}

	tempDir := t.TempDir()
	svc := &Service{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
		},
		logger: api.NopLogger{},
	}

	meta := preparationstate.State{SourcePath: filepath.Join(tempDir, "source")}
	result := trackerdata.Result{Validated: validated}

	successful := svc.persistTrackerArtifacts(context.Background(), meta, "BHD", result, true)
	if len(successful) != imageCount {
		t.Fatalf("expected %d downloaded images, got %d", imageCount, len(successful))
	}
	if got := maxInFlight.Load(); got > unit3dImageWorkers {
		t.Fatalf("expected max in-flight <= %d, got %d", unit3dImageWorkers, got)
	}
	if got := maxInFlight.Load(); got < 2 {
		t.Fatalf("expected concurrent downloads, max in-flight was %d", got)
	}
}

func TestPersistUnit3DArtifactsRejectsPrivateImageURLBeforeDownload(t *testing.T) {
	var requests atomic.Int32
	previousHTTPClient := newUnit3DArtifactImageHTTPClient
	newUnit3DArtifactImageHTTPClient = func() *http.Client {
		return &http.Client{Transport: artifactRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests.Add(1)
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Request:    req,
			}, nil
		})}
	}
	t.Cleanup(func() {
		newUnit3DArtifactImageHTTPClient = previousHTTPClient
	})

	tempDir := t.TempDir()
	svc := &Service{
		cfg: config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tempDir, "db.sqlite")},
		},
		logger: api.NopLogger{},
	}

	meta := preparationstate.State{SourcePath: filepath.Join(tempDir, "source")}
	result := trackerdata.Result{Validated: []bbcode.Image{{RawURL: "http://127.0.0.1/private.png"}}}

	successful := svc.persistTrackerArtifacts(context.Background(), meta, "BHD", result, true)
	if len(successful) != 0 {
		t.Fatalf("expected private image not to persist, got %+v", successful)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected private image not to be fetched, got %d request(s)", got)
	}
}
