// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUploadBatchRequestLimits(t *testing.T) {
	const capBytes int64 = 100 << 20
	for _, host := range []string{"lostimg", "hdb"} {
		t.Run(host, func(t *testing.T) {
			countLimit := 50
			if host == "hdb" {
				countLimit = 9
			}
			for _, scenario := range []string{"size", "count and size", "exact boundary", "over boundary", "later failure"} {
				t.Run(scenario, func(t *testing.T) {
					sizes := []int64{60 << 20, 60 << 20, 1}
					expectedCounts := []int{1, 2}
					wantError := false
					switch scenario {
					case "count and size":
						sizes = make([]int64, countLimit+3)
						for i := range sizes {
							sizes[i] = 1
						}
						sizes[0], sizes[countLimit] = 60<<20, 60<<20
						sizes[countLimit+1] = 60 << 20
						expectedCounts = []int{countLimit, 1, 2}
					case "exact boundary", "over boundary":
						// Measure the wire envelope independently, with the standard encoder.
						var envelope strings.Builder
						writer := multipart.NewWriter(&envelope)
						if host == "hdb" {
							for _, f := range [][2]string{{"username", "user"}, {"passkey", "pass"}, {"galleryoption", "1"}, {"galleryname", "release"}, {"thumbsize", "w300"}} {
								if err := writer.WriteField(f[0], f[1]); err != nil {
									t.Fatal(err)
								}
							}
						}
						field := "file[]"
						if host == "hdb" {
							field = "images_files[0]"
						}
						if _, err := writer.CreateFormFile(field, "shot-00.png"); err != nil {
							t.Fatal(err)
						}
						if err := writer.Close(); err != nil {
							t.Fatal(err)
						}
						sizes = []int64{capBytes - int64(envelope.Len())}
						expectedCounts = []int{1}
						if scenario == "over boundary" {
							sizes[0]++
							expectedCounts = nil
							wantError = true
						}
					case "later failure":
						sizes = []int64{60 << 20, 60 << 20, capBytes}
						expectedCounts = []int{1, 1}
						wantError = true
					}
					paths := make([]string, len(sizes))
					for i, size := range sizes {
						paths[i] = filepath.Join(t.TempDir(), fmt.Sprintf("shot-%02d.png", i))
						file, err := os.OpenFile(paths[i], os.O_CREATE|os.O_RDWR, 0o600)
						if err != nil {
							t.Fatal(err)
						}
						if err := file.Truncate(size); err != nil {
							t.Fatal(err)
						}
						if err := file.Close(); err != nil {
							t.Fatal(err)
						}
					}
					var counts []int
					var uploaded []string
					client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						if req.ContentLength > capBytes {
							t.Fatalf("encoded request is %d bytes, cap %d", req.ContentLength, capBytes)
						}
						_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
						if err != nil {
							t.Fatal(err)
						}
						reader := multipart.NewReader(req.Body, params["boundary"])
						var urls []string
						var response strings.Builder
						for {
							part, err := reader.NextPart()
							if err == io.EOF {
								break
							}
							if err != nil {
								t.Fatal(err)
							}
							if part.FileName() == "" {
								value, err := io.ReadAll(part)
								if err != nil {
									t.Fatal(err)
								}
								if part.FormName() == "galleryname" && string(value) != "release" {
									t.Fatalf("gallery changed: %q", value)
								}
								continue
							}
							if _, err := io.Copy(io.Discard, part); err != nil {
								t.Fatal(err)
							}
							uploaded = append(uploaded, part.FileName())
							url := "https://images.example/" + part.FileName()
							urls = append(urls, url)
							fmt.Fprintf(&response, "[url=%s][img]%s[/img][/url]", url, url)
						}
						counts = append(counts, len(urls))
						if host == "lostimg" {
							data, err := json.Marshal(map[string][]string{"urls": urls})
							if err != nil {
								t.Fatal(err)
							}
							response.Reset()
							response.Write(data)
						}
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader(response.String())),
						}, nil
					})}
					var results []uploadResult
					var err error
					if host == "lostimg" {
						results, err = (&lostimgUploader{apiKey: "test", client: client}).UploadBatch(context.Background(), paths)
					} else {
						results, err = (&hdbUploader{
							username: "user",
							passkey:  "pass",
							client:   client,
						}).UploadBatchWithName(context.Background(), paths, "release")
					}
					if wantError {
						if err == nil || !strings.Contains(err.Error(), "100 MiB") {
							t.Fatalf("expected actionable size error, got %v", err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(counts, expectedCounts) {
						t.Fatalf("request counts %v, want %v", counts, expectedCounts)
					}
					for i, name := range uploaded {
						if name != filepath.Base(paths[i]) {
							t.Fatalf("upload order/duplication at %d: %s", i, name)
						}
					}
					expectedResults := len(uploaded)
					if host == "hdb" && wantError {
						expectedResults = 0
					}
					if len(results) != expectedResults {
						t.Fatalf("results %d, want %d", len(results), expectedResults)
					}
					for i, result := range results {
						if result.RawURL != "https://images.example/"+uploaded[i] {
							t.Fatalf("result order at %d: %s", i, result.RawURL)
						}
					}
				})
			}
		})
	}
}
