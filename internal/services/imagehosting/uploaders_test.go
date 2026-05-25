// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type trackingReadCloser struct {
	reader io.Reader
	closed bool
}

func (t *trackingReadCloser) Read(p []byte) (int, error) {
	return t.reader.Read(p)
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}

func TestHDBUploadBatchUsesSingleGalleryRequest(t *testing.T) {
	tmpDir := t.TempDir()
	firstPath := filepath.Join(tmpDir, "shot-01.png")
	secondPath := filepath.Join(tmpDir, "shot-02.png")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("testdata"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
	}

	requestCount := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestCount++
			if req.URL.String() != "https://img.hdbits.org/upload_api.php" {
				t.Fatalf("unexpected request URL: %s", req.URL.String())
			}
			mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse media type: %v", err)
			}
			if mediaType != "multipart/form-data" {
				t.Fatalf("unexpected media type: %s", mediaType)
			}
			reader := multipartReader(t, req, params["boundary"])
			fields := map[string]string{}
			fileFields := []string{}
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("read multipart part: %v", err)
				}
				body, err := io.ReadAll(part)
				if err != nil {
					t.Fatalf("read part body: %v", err)
				}
				if part.FileName() == "" {
					fields[part.FormName()] = string(body)
					continue
				}
				fileFields = append(fileFields, part.FormName())
			}
			if fields["galleryoption"] != "1" {
				t.Fatalf("expected galleryoption 1, got %q", fields["galleryoption"])
			}
			if fields["galleryname"] != "shot-01" {
				t.Fatalf("expected gallery name shot-01, got %q", fields["galleryname"])
			}
			if len(fileFields) != 2 {
				t.Fatalf("expected 2 uploaded files, got %d", len(fileFields))
			}
			if fileFields[0] != "images_files[0]" || fileFields[1] != "images_files[1]" {
				t.Fatalf("unexpected file field names: %v", fileFields)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"[url=https://img.hdbits.org/a1][img]https://t.hdbits.org/a1.jpg[/img][/url]" +
						"[url=https://img.hdbits.org/b2][img]https://t.hdbits.org/b2.jpg[/img][/url]",
				)),
			}, nil
		}),
	}

	uploader := &hdbUploader{
		username: "user",
		passkey:  "pass",
		client:   client,
	}

	results, err := uploader.UploadBatch(context.Background(), []string{firstPath, secondPath})
	if err != nil {
		t.Fatalf("UploadBatch returned error: %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("expected 1 request, got %d", requestCount)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].RawURL != "https://img.hdbits.org/a1.jpg" {
		t.Fatalf("unexpected first raw URL: %q", results[0].RawURL)
	}
	if results[1].RawURL != "https://img.hdbits.org/b2.jpg" {
		t.Fatalf("unexpected second raw URL: %q", results[1].RawURL)
	}
}

func TestParseHDBUploadResultsMultipleMatches(t *testing.T) {
	results, err := parseHDBUploadResults([]byte(
		"[url=https://img.hdbits.org/a1][img]https://t.hdbits.org/a1.jpg[/img][/url]\n" +
			"[url=https://img.hdbits.org/b2][img]https://t.hdbits.org/b2.jpg[/img][/url]",
	))
	if err != nil {
		t.Fatalf("parseHDBUploadResults returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ImgURL != "https://t.hdbits.org/a1.jpg" {
		t.Fatalf("unexpected first thumb URL: %q", results[0].ImgURL)
	}
	if results[1].WebURL != "https://img.hdbits.org/b2" {
		t.Fatalf("unexpected second web URL: %q", results[1].WebURL)
	}
}

func TestTHRUploaderPostsSourceAndKey(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "shot.png")
	if err := os.WriteFile(imagePath, []byte("testdata"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://img2.torrenthr.org/api/1/upload" {
				t.Fatalf("unexpected request URL: %s", req.URL.String())
			}
			mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse media type: %v", err)
			}
			if mediaType != "multipart/form-data" {
				t.Fatalf("unexpected media type: %s", mediaType)
			}
			reader := multipartReader(t, req, params["boundary"])
			fields := map[string]string{}
			fileFields := []string{}
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("read multipart part: %v", err)
				}
				body, err := io.ReadAll(part)
				if err != nil {
					t.Fatalf("read part body: %v", err)
				}
				if part.FileName() == "" {
					fields[part.FormName()] = string(body)
					continue
				}
				fileFields = append(fileFields, part.FormName())
			}
			if fields["key"] != "secret" {
				t.Fatalf("expected key field, got %q", fields["key"])
			}
			if len(fileFields) != 1 || fileFields[0] != "source" {
				t.Fatalf("expected source file field, got %v", fileFields)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"image":{"url":"https://img2.torrenthr.org/images/shot.png"}}`)),
			}, nil
		}),
	}

	result, err := (&thrUploader{apiKey: "secret", client: client}).Upload(context.Background(), imagePath)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if result.RawURL != "https://img2.torrenthr.org/images/shot.png" {
		t.Fatalf("unexpected raw URL: %q", result.RawURL)
	}
}

func TestTHRUploaderRequiresImageURL(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "shot.png")
	if err := os.WriteFile(imagePath, []byte("testdata"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"image":{},"error":{"message":"bad image"}}`)),
			}, nil
		}),
	}

	_, err := (&thrUploader{apiKey: "secret", client: client}).Upload(context.Background(), imagePath)
	if err == nil {
		t.Fatal("expected missing URL error")
	}
	if !strings.Contains(err.Error(), "bad image") {
		t.Fatalf("expected response error message, got %v", err)
	}
}

func TestReadAndCloseResponseBodyClosesBody(t *testing.T) {
	body := &trackingReadCloser{reader: strings.NewReader("partial response")}
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     make(http.Header),
		Body:       body,
	}

	payload, err := readAndCloseResponseBody(resp)
	if err != nil {
		t.Fatalf("readAndCloseResponseBody returned error: %v", err)
	}
	if string(payload) != "partial response" {
		t.Fatalf("unexpected payload: %q", string(payload))
	}
	if !body.closed {
		t.Fatal("expected response body to be closed")
	}
}

func multipartReader(t *testing.T, req *http.Request, boundary string) *multipart.Reader {
	t.Helper()
	if boundary == "" {
		t.Fatal("missing multipart boundary")
	}
	return multipart.NewReader(req.Body, boundary)
}
