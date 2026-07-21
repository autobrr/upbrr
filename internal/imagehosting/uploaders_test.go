// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"errors"
	"fmt"
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
	n, err := t.reader.Read(p)
	if err == nil {
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	return n, fmt.Errorf("read tracking response body: %w", err)
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

func TestHDBUploadBatchChunksLargeUploads(t *testing.T) {
	tmpDir := t.TempDir()
	paths := make([]string, 0, hdbMaxBatchUploadImages+1)
	for idx := range hdbMaxBatchUploadImages + 1 {
		path := filepath.Join(tmpDir, fmt.Sprintf("shot-%02d.png", idx+1))
		if err := os.WriteFile(path, []byte("testdata"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		paths = append(paths, path)
	}

	requestFileCounts := make([]int, 0)
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse media type: %v", err)
			}
			if mediaType != "multipart/form-data" {
				t.Fatalf("unexpected media type: %s", mediaType)
			}
			reader := multipartReader(t, req, params["boundary"])
			fileCount := 0
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("read multipart part: %v", err)
				}
				_, _ = io.Copy(io.Discard, part)
				if part.FileName() != "" {
					fileCount++
				}
			}
			requestFileCounts = append(requestFileCounts, fileCount)
			var body strings.Builder
			for idx := 0; idx < fileCount; idx++ {
				_, _ = fmt.Fprintf(&body, "[url=https://img.hdbits.org/%d][img]https://t.hdbits.org/%d.jpg[/img][/url]", len(requestFileCounts), idx)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body.String())),
			}, nil
		}),
	}

	uploader := &hdbUploader{
		username: "user",
		passkey:  "pass",
		client:   client,
	}
	results, err := uploader.UploadBatchWithName(context.Background(), paths, "release")
	if err != nil {
		t.Fatalf("UploadBatchWithName returned error: %v", err)
	}
	if len(requestFileCounts) != 2 {
		t.Fatalf("expected 2 chunk requests, got %d", len(requestFileCounts))
	}
	if requestFileCounts[0] != hdbMaxBatchUploadImages || requestFileCounts[1] != 1 {
		t.Fatalf("unexpected chunk sizes: %v", requestFileCounts)
	}
	if len(results) != len(paths) {
		t.Fatalf("expected %d results, got %d", len(paths), len(results))
	}
}

func TestImgboxUploadRejectedUsesFallbackAfterSanitizingError(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "shot.png")
	if err := os.WriteFile(imagePath, []byte("testdata"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://imgbox.com/":
				header := make(http.Header)
				header.Add("Set-Cookie", "session=abc; Path=/")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader(`<input name="authenticity_token" value="csrf-token">`)),
				}, nil
			case "https://imgbox.com/ajax/token/generate":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"token_id":"1","token_secret":"secret","gallery_id":"2","gallery_secret":"gallery"}`)),
				}, nil
			case "https://imgbox.com/upload/process":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"ok":false,"error":"   "}`)),
				}, nil
			default:
				t.Fatalf("unexpected request URL: %s", req.URL.String())
				return nil, nil
			}
		}),
	}

	_, err := (&imgboxUploader{client: client}).Upload(context.Background(), imagePath)
	if err == nil {
		t.Fatal("expected rejected upload to fail")
	}
	if !strings.Contains(err.Error(), "imgbox upload rejected: unknown error") {
		t.Fatal("expected unknown error fallback")
	}
	if strings.Contains(err.Error(), "imgbox upload rejected:  ") {
		t.Fatal("rejection message must not be whitespace-only")
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

func TestLostimgUploaderPostsRepeatedFileFields(t *testing.T) {
	tmpDir := t.TempDir()
	firstPath := filepath.Join(tmpDir, "shot-01.png")
	secondPath := filepath.Join(tmpDir, "shot-02.png")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("testdata"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
	}

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://lostimg.cc/api/v1/images" {
				t.Fatalf("unexpected request URL: %s", req.URL.String())
			}
			if got := req.Header.Get("Authorization"); got != "Bearer secret" {
				t.Fatal("expected bearer auth")
			}
			mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse media type: %v", err)
			}
			if mediaType != "multipart/form-data" {
				t.Fatalf("unexpected media type: %s", mediaType)
			}
			reader := multipartReader(t, req, params["boundary"])
			fileFields := []string{}
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("read multipart part: %v", err)
				}
				_, _ = io.Copy(io.Discard, part)
				if part.FileName() != "" {
					fileFields = append(fileFields, part.FormName())
				}
			}
			if len(fileFields) != 2 || fileFields[0] != "file[]" || fileFields[1] != "file[]" {
				t.Fatalf("expected repeated file[] fields, got %v", fileFields)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"urls":["https://lostimg.cc/a.png","https://lostimg.cc/b.png"]}`)),
			}, nil
		}),
	}

	results, err := (&lostimgUploader{apiKey: "secret", client: client}).UploadBatch(context.Background(), []string{firstPath, secondPath})
	if err != nil {
		t.Fatalf("UploadBatch returned error: %v", err)
	}
	if len(results) != 2 || results[0].RawURL != "https://lostimg.cc/a.png" || results[1].RawURL != "https://lostimg.cc/b.png" {
		t.Fatalf("unexpected lostimg results: %#v", results)
	}
}

func TestLostimgUploaderAcceptsSingleURLResponse(t *testing.T) {
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
				Body:       io.NopCloser(strings.NewReader(`{"url":"https://lostimg.cc/shot.png"}`)),
			}, nil
		}),
	}

	result, err := (&lostimgUploader{apiKey: "secret", client: client}).Upload(context.Background(), imagePath)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if result.RawURL != "https://lostimg.cc/shot.png" {
		t.Fatalf("unexpected raw URL: %q", result.RawURL)
	}
}

func TestPixhostUploaderPostsCurrentDomain(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "shot.png")
	if err := os.WriteFile(imagePath, []byte("testdata"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != pixhostUploadURL {
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
			if fields["content_type"] != "0" || fields["max_th_size"] != "350" {
				t.Fatalf("unexpected pixhost fields: %v", fields)
			}
			if len(fileFields) != 1 || fileFields[0] != "img" {
				t.Fatalf("expected img file field, got %v", fileFields)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"th_url":"https://t1.pixhost.cc/thumbs/11645/shot.png","show_url":"https://pixhost.cc/show/11645/shot.png"}`)),
			}, nil
		}),
	}

	result, err := (&pixhostUploader{client: client}).Upload(context.Background(), imagePath)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if result.ImgURL != "https://t1.pixhost.cc/thumbs/11645/shot.png" {
		t.Fatalf("unexpected img URL: %q", result.ImgURL)
	}
	if result.RawURL != "https://img1.pixhost.cc/images/11645/shot.png" {
		t.Fatalf("unexpected raw URL: %q", result.RawURL)
	}
	if result.WebURL != "https://pixhost.cc/show/11645/shot.png" {
		t.Fatalf("unexpected web URL: %q", result.WebURL)
	}
}

func TestReelflixUploaderPostsSourceWithAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "shot.png")
	if err := os.WriteFile(imagePath, []byte("testdata"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "https://img.reelflix.cc/api/1/upload" {
				t.Fatalf("unexpected request URL: %s", req.URL.String())
			}
			if got := req.Header.Get("X-Api-Key"); got != "secret" {
				t.Fatal("expected X-API-Key")
			}
			mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil {
				t.Fatalf("parse media type: %v", err)
			}
			if mediaType != "multipart/form-data" {
				t.Fatalf("unexpected media type: %s", mediaType)
			}
			reader := multipartReader(t, req, params["boundary"])
			fileFields := []string{}
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("read multipart part: %v", err)
				}
				_, _ = io.Copy(io.Discard, part)
				if part.FileName() != "" {
					fileFields = append(fileFields, part.FormName())
				}
			}
			if len(fileFields) != 1 || fileFields[0] != "source" {
				t.Fatalf("expected source file field, got %v", fileFields)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"status_code": 200,
					"image": {
						"url": "https://img.reelflix.cc/images/shot.png",
						"url_viewer": "https://img.reelflix.cc/image/shot",
						"medium": {"url": "https://img.reelflix.cc/images/medium/shot.png"}
					}
				}`)),
			}, nil
		}),
	}

	result, err := (&reelflixUploader{apiKey: "secret", client: client}).Upload(context.Background(), imagePath)
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if result.ImgURL != "https://img.reelflix.cc/images/medium/shot.png" {
		t.Fatalf("unexpected img URL: %q", result.ImgURL)
	}
	if result.RawURL != "https://img.reelflix.cc/images/shot.png" {
		t.Fatalf("unexpected raw URL: %q", result.RawURL)
	}
	if result.WebURL != "https://img.reelflix.cc/image/shot" {
		t.Fatalf("unexpected web URL: %q", result.WebURL)
	}
}

func TestReadAndCloseResponseBodyClosesBody(t *testing.T) {
	body := &trackingReadCloser{reader: strings.NewReader("partial response")}
	resp := &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     make(http.Header),
		Body:       body,
	}

	payload, err := readLimitedAndCloseResponseBody(resp)
	if err != nil {
		t.Fatalf("readLimitedAndCloseResponseBody returned error: %v", err)
	}
	if string(payload) != "partial response" {
		t.Fatalf("unexpected payload: %q", safeResponsePreview(payload))
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
