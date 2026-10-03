// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package data

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLookupUnit3DPreservesImageOutcomes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		images    string
		wantError bool
		cancel    bool
		wantCount int
	}{
		{
			name:      "mixed",
			images:    "[img]https://93.184.216.34/valid.png[/img]\n[img]https://93.184.216.34/failed.png[/img]\n[img]https://93.184.216.34/last.png[/img]",
			wantError: true,
			wantCount: 3,
		},
		{
			name:      "all failed",
			images:    "[img]https://93.184.216.34/failed.png[/img]",
			wantError: true,
		},
		{
			name:      "decode failed",
			images:    "[img]https://93.184.216.34/invalid.png[/img]",
			wantError: true,
		},
		{
			name:      "nonpublic",
			images:    "[img]http://127.0.0.1/private.png[/img]",
			wantError: true,
		},
		{name: "no images"},
		{
			name:      "valid",
			images:    "[img]https://93.184.216.34/valid.png[/img]",
			wantCount: 1,
		},
		{
			name:      "canceled",
			images:    "[img]https://93.184.216.34/valid.png[/img]",
			wantError: true,
			cancel:    true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/torrents/42":
					_ = json.NewEncoder(w).Encode(map[string]any{"attributes": map[string]any{
						"imdb_id":     1234567,
						"description": "Release notes\n" + tt.images,
					}})
				case "/failed.png":
					w.WriteHeader(http.StatusInternalServerError)
				case "/invalid.png":
					w.Header().Set("Content-Type", "image/png")
					_, _ = w.Write([]byte("private invalid image data"))
				case "/valid.png", "/last.png":
					if tt.cancel {
						cancel()
						return
					}
					w.Header().Set("Content-Type", "image/png")
					if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
						t.Errorf("encode image: %v", err)
					}
				default:
					t.Errorf("unexpected image request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			base, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := NewClientWithRegistry(config.Config{}, api.NopLogger{},
				&http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}},
				testUnit3DRegistry(t, "TEST", "https://tracker.example"))
			result, err := client.Lookup(ctx, "TEST", "42", api.UploadSubject{}, "", false, true)
			if (err != nil) != tt.wantError || result.IMDBID != 1234567 || !strings.Contains(result.Description, "Release notes") || len(result.Images) != tt.wantCount {
				t.Fatalf("image outcome: result=%+v err=%v", result, err)
			}
			if tt.name == "mixed" && (result.Images[0].RawURL != "https://93.184.216.34/valid.png" || result.Images[1].RawURL != "" || result.Images[2].RawURL != "https://93.184.216.34/last.png") {
				t.Fatalf("image positions changed: %+v", result.Images)
			}
			if tt.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation cause lost: %v", err)
			}
			if err != nil && (strings.Contains(err.Error(), "93.184") || strings.Contains(err.Error(), ".png") || strings.Contains(err.Error(), "private")) {
				t.Fatalf("image failure leaked source content: %v", err)
			}
		})
	}
}

func TestPrepareDescriptionImagesPreservesResolutionFailure(t *testing.T) {
	originalResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("synthetic image DNS unavailable")
		},
	}
	t.Cleanup(func() { net.DefaultResolver = originalResolver })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/valid.png" {
			t.Errorf("unexpected image request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			t.Errorf("encode image: %v", err)
		}
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: rewriteHostTransport{base: base, rt: server.Client().Transport}}
	images, err := PrepareDescriptionImages(t.Context(), client, "TEST", api.NopLogger{}, []bbcode.Image{
		{RawURL: "https://93.184.216.34/valid.png"},
		{RawURL: "https://i.ibb.co/example/thumb.png", WebURL: "https://ibb.co/example"},
		{RawURL: "https://93.184.216.34/valid.png"},
	})
	if err == nil || len(images) != 3 || images[0].RawURL == "" || images[1] != (bbcode.Image{}) || images[2].RawURL == "" {
		t.Fatalf("resolution failure lost or image positions changed: images=%+v err=%v", images, err)
	}
	if strings.Contains(err.Error(), "ibb.co") || strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("resolution error leaked source detail: %v", err)
	}
}

func TestPrepareDescriptionImagesEmptyInputs(t *testing.T) {
	t.Parallel()
	for _, input := range [][]bbcode.Image{nil, {{}}} {
		images, err := PrepareDescriptionImages(t.Context(), nil, "TEST", nil, input)
		if len(images) != 0 || err != nil {
			t.Fatalf("empty image input became a failure: images=%+v err=%v", images, err)
		}
	}
}
