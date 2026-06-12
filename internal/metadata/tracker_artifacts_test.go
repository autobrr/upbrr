// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/bbcode"
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/pkg/api"
)

type artifactRoundTripFunc func(*http.Request) (*http.Response, error)

func (f artifactRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPersistUnit3DArtifactsMaxConcurrentImageDownloads(t *testing.T) {
	const imageCount = 12

	var inFlight int32
	var maxInFlight int32

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

			current := atomic.AddInt32(&inFlight, 1)
			for {
				peak := atomic.LoadInt32(&maxInFlight)
				if current <= peak {
					break
				}
				if atomic.CompareAndSwapInt32(&maxInFlight, peak, current) {
					break
				}
			}
			defer atomic.AddInt32(&inFlight, -1)

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

	meta := api.PreparedMetadata{SourcePath: filepath.Join(tempDir, "source")}
	result := trackerdata.Result{Validated: validated}

	successful := svc.persistUnit3DArtifacts(context.Background(), meta, "BHD", result, true)
	if len(successful) != imageCount {
		t.Fatalf("expected %d downloaded images, got %d", imageCount, len(successful))
	}
	if got := atomic.LoadInt32(&maxInFlight); got > unit3dImageWorkers {
		t.Fatalf("expected max in-flight <= %d, got %d", unit3dImageWorkers, got)
	}
	if got := atomic.LoadInt32(&maxInFlight); got < 2 {
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

	meta := api.PreparedMetadata{SourcePath: filepath.Join(tempDir, "source")}
	result := trackerdata.Result{Validated: []bbcode.Image{{RawURL: "http://127.0.0.1/private.png"}}}

	successful := svc.persistUnit3DArtifacts(context.Background(), meta, "BHD", result, true)
	if len(successful) != 0 {
		t.Fatalf("expected private image not to persist, got %+v", successful)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("expected private image not to be fetched, got %d request(s)", got)
	}
}
