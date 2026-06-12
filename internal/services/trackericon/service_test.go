// SPDX-License-Identifier: GPL-2.0-or-later

package trackericon

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

var testPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withIconHTTPTestHooks(t *testing.T, transport http.RoundTripper, lookup func(context.Context, string) ([]net.IPAddr, error)) {
	t.Helper()
	oldClient := iconHTTPClient
	oldLookup := iconLookupIPAddr
	iconHTTPClient = &http.Client{Timeout: oldClient.Timeout, Transport: transport}
	iconLookupIPAddr = lookup
	t.Cleanup(func() {
		iconHTTPClient = oldClient
		iconLookupIPAddr = oldLookup
	})
}

func publicLookup(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
}

func TestGetTrackerIconTriesCustomURLAsIsFirst(t *testing.T) {
	var requested []string
	withIconHTTPTestHooks(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(testPNG)),
		}, nil
	}), publicLookup)

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	dataURL, err := GetTrackerIcon(context.Background(), dbPath, "fallback.example", "https://icons.example/custom.png")
	if err != nil {
		t.Fatalf("get tracker icon: %v", err)
	}
	if len(requested) != 1 || requested[0] != "https://icons.example/custom.png" {
		t.Fatalf("expected exact custom URL first, got %#v", requested)
	}
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Fatalf("expected PNG data URL, got %q", dataURL)
	}
}

func TestGetTrackerIconFallsBackToCustomURLRootFavicon(t *testing.T) {
	var requested []string
	withIconHTTPTestHooks(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.String())
		status := http.StatusNotFound
		body := []byte("missing")
		if len(requested) == 2 {
			status = http.StatusOK
			body = testPNG
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	}), publicLookup)

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	if _, err := GetTrackerIcon(context.Background(), dbPath, "fallback.example", "https://icons.example/path/icon.png"); err != nil {
		t.Fatalf("get tracker icon: %v", err)
	}
	if len(requested) < 2 {
		t.Fatalf("expected fallback request, got %#v", requested)
	}
	if requested[0] != "https://icons.example/path/icon.png" || requested[1] != "https://icons.example/favicon.ico" {
		t.Fatalf("unexpected request order: %#v", requested)
	}
}

func TestGetTrackerIconCachesDifferentCustomURLsSeparately(t *testing.T) {
	var requested []string
	withIconHTTPTestHooks(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(testPNG)),
		}, nil
	}), publicLookup)

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	if _, err := GetTrackerIcon(context.Background(), dbPath, "icons.example", "https://icons.example/one.png"); err != nil {
		t.Fatalf("get first tracker icon: %v", err)
	}
	if _, err := GetTrackerIcon(context.Background(), dbPath, "icons.example", "https://icons.example/two.png"); err != nil {
		t.Fatalf("get second tracker icon: %v", err)
	}
	if len(requested) != 2 || requested[0] != "https://icons.example/one.png" || requested[1] != "https://icons.example/two.png" {
		t.Fatalf("expected separate custom URL fetches, got %#v", requested)
	}
}

func TestGetTrackerIconBlocksPrivateResolvedAddresses(t *testing.T) {
	var requested int
	withIconHTTPTestHooks(t, roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		requested++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(testPNG)),
		}, nil
	}), func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})

	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	if _, err := GetTrackerIcon(context.Background(), dbPath, "private.example", "https://private.example/icon.png"); err == nil {
		t.Fatal("expected private address to fail")
	}
	if requested != 0 {
		t.Fatalf("expected private address to be blocked before request, got %d requests", requested)
	}
}
