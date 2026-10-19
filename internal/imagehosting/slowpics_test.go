// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

const (
	slowpicsTestCollectionID  = "7b3b2639-f8b5-4b7c-a6eb-66ce166575f6"
	slowpicsTestImageID       = "f3cc105c-b176-4377-8d02-d33940dc8845"
	slowpicsTestSecondImageID = "5df37b0c-c567-4a8c-965b-bd63b55156ae"
)

func TestSlowpicsCollectionProtocol(t *testing.T) {
	t.Parallel()
	firstPath, firstPNG := slowpicsTestPNG(t)
	secondPath, secondPNG := slowpicsTestPNG(t)
	request := slowpicsCollectionRequest{
		Name:   "Synthetic Collection",
		Images: []slowpicsCollectionImage{{Name: "00:10", Path: firstPath}, {Name: "00:20", Path: secondPath}},
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := url.Parse(slowpicsOrigin)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(origin, []*http.Cookie{{Name: "private-existing-login", Value: "must-not-send"}})
	var calls []string
	var browserID string
	client := &http.Client{Jar: jar, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "slow.pics" {
			t.Fatalf("unexpected destination: %s", req.URL)
		}
		if strings.Contains(req.Header.Get("Cookie"), "private-existing-login") {
			t.Fatal("inherited authenticated cookie")
		}
		if req.Header.Get("User-Agent") != "upbrr" {
			t.Fatal("missing application User-Agent")
		}
		if req.Header.Get("Accept") != "*/*" || req.Header.Get("Accept-Language") != "en-US,en;q=0.9" {
			t.Fatal("missing slow.pics request negotiation headers")
		}
		calls = append(calls, req.Method+" "+req.URL.Path)
		if req.URL.Path == "/comparison" {
			if len(calls) != 1 || req.Method != http.MethodGet {
				t.Fatal("session was not bootstrapped first")
			}
			if err := os.WriteFile(firstPath, []byte("changed after validation"), 0o600); err != nil {
				t.Fatal(err)
			}
			response := slowpicsTestResponse(http.StatusOK, "text/html", "<html>Anonymous upload</html>")
			response.Header.Add("Set-Cookie", "anonymous-session=synthetic; Path=/; Secure")
			response.Header.Add("Set-Cookie", "XSRF-TOKEN=synthetic%2Btoken; Path=/upload; Secure")
			return response, nil
		}
		if req.Header.Get("X-Xsrf-Token") != "synthetic+token" || !strings.Contains(req.Header.Get("Cookie"), "anonymous-session=synthetic") {
			t.Fatal("anonymous cookie or XSRF token missing")
		}
		if req.Header.Get("Origin") != slowpicsOrigin || req.Header.Get("Referer") != slowpicsOrigin+"/comparison" {
			t.Fatal("unexpected origin headers")
		}
		fields, file := slowpicsTestMultipart(t, req)
		if req.URL.Path == "/upload/comparison" {
			browserID = fields["browserId"]
			if !validSlowpicsUUID(browserID) || file != nil {
				t.Fatal("create request must use a browser UUID without a file")
			}
			want := map[string]string{
				"collectionName":                     "Synthetic Collection",
				"hentai":                             "false",
				"optimizeImages":                     "false",
				"stripHdr":                           "false",
				"desiredFileType":                    "image/png",
				"public":                             "false",
				"visibility":                         "LINK_ONLY",
				"removeAfter":                        "",
				"browserId":                          browserID,
				"comparisons[0].name":                "Synthetic Collection",
				"comparisons[0].hentai":              "false",
				"comparisons[0].sortOrder":           "0",
				"comparisons[0].images[0].name":      "00:10",
				"comparisons[0].images[0].sortOrder": "0",
				"comparisons[0].images[1].name":      "00:20",
				"comparisons[0].images[1].sortOrder": "1",
			}
			if !reflect.DeepEqual(fields, want) {
				t.Fatalf("create fields = %v, want %v", fields, want)
			}
			return slowpicsTestCreated(slowpicsTestImageID, slowpicsTestSecondImageID), nil
		}
		index := len(calls) - 3
		ids := []string{slowpicsTestImageID, slowpicsTestSecondImageID}
		if index < 0 || index >= len(ids) || req.URL.Path != "/upload/image/"+ids[index] {
			t.Fatalf("unexpected member request: %s", req.URL.Path)
		}
		want := map[string]string{
			"collectionUuid": slowpicsTestCollectionID,
			"imageUuid":      ids[index],
			"browserId":      browserID,
			"originalName":   "",
		}
		if !reflect.DeepEqual(fields, want) || !bytes.Equal(file, [][]byte{firstPNG, secondPNG}[index]) {
			t.Fatal("member fields or validated PNG snapshot changed")
		}
		return slowpicsTestResponse(http.StatusOK, "application/json", "{}"), nil
	})}
	result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), request)
	if err != nil || result != "https://slow.pics/c/SyntheticKey" || len(calls) != 4 {
		t.Fatalf("result=%q error=%v calls=%v", result, err, calls)
	}
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("source file removed: %v", err)
	}
	if len(client.Jar.Cookies(origin)) != 1 || client.Timeout != 0 || client.CheckRedirect != nil {
		t.Fatal("caller client was mutated")
	}
}

func TestSlowpicsComparisonMatrixProtocol(t *testing.T) {
	t.Parallel()
	for _, columns := range []int{0, 1, 2} {
		t.Run(strconv.Itoa(columns), func(t *testing.T) {
			var input slowpicsCollectionRequest
			input.Name, input.Columns = "Synthetic comparison", columns
			snapshots := make([][]byte, 0, 4)
			ids := make([]string, 0, 4)
			for index := range 4 {
				path, data := slowpicsTestPNG(t)
				input.Images = append(input.Images, slowpicsCollectionImage{Name: fmt.Sprintf("Source %d", index%max(1, columns)+1), Path: path})
				snapshots = append(snapshots, data)
				ids = append(ids, fmt.Sprintf("7b3b2639-f8b5-4b7c-a6eb-%012d", index+1))
			}
			wireColumns := columns
			if wireColumns <= 1 {
				wireColumns = len(ids)
			}
			members := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/comparison" {
					return slowpicsTestBootstrapResponse(http.StatusOK), nil
				}
				fields, file := slowpicsTestMultipart(t, req)
				if req.URL.Path == "/upload/comparison" {
					rows := make([][]string, len(ids)/wireColumns)
					for row := range rows {
						prefix := fmt.Sprintf("comparisons[%d]", row)
						if fields[prefix+".name"] == "" {
							t.Fatal("missing comparison row name")
						}
						for column := range wireColumns {
							if fields[fmt.Sprintf("%s.images[%d].name", prefix, column)] != input.Images[row*wireColumns+column].Name {
								t.Fatalf("source/row association changed: %v", fields)
							}
						}
						rows[row] = ids[row*wireColumns : (row+1)*wireColumns]
					}
					body, err := json.Marshal(slowpicsCreatedCollection{
						CollectionUUID: slowpicsTestCollectionID,
						Key:            "SyntheticKey",
						Images:         rows,
					})
					if err != nil {
						t.Fatal(err)
					}
					return slowpicsTestResponse(http.StatusOK, "application/json", string(body)), nil
				}
				if members >= len(ids) || req.URL.Path != "/upload/image/"+ids[members] || !bytes.Equal(file, snapshots[members]) {
					t.Fatal("image bytes uploaded to the wrong matrix cell")
				}
				members++
				return slowpicsTestResponse(http.StatusOK, "", "OK"), nil
			})}
			if result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), input); err != nil || result == "" || members != 4 {
				t.Fatalf("result=%s members=%d err=%v", result, members, err)
			}
		})
	}
}

func TestSlowpicsSingleImageUsesCollectionProtocol(t *testing.T) {
	t.Parallel()
	path, snapshot := slowpicsTestPNG(t)
	creates, members := 0, 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/comparison" {
			return slowpicsTestBootstrapResponse(http.StatusOK), nil
		}
		fields, file := slowpicsTestMultipart(t, req)
		switch req.URL.Path {
		case "/upload/collection":
			creates++
			if file != nil || fields["images[0].name"] != "One" || fields["images[0].hentai"] != "false" ||
				fields["images[0].sortOrder"] != "0" || fields["visibility"] != "LINK_ONLY" || fields["public"] != "false" ||
				fields["optimizeImages"] != "false" || fields["desiredFileType"] != "image/png" || len(fields) != 12 {
				t.Fatalf("invalid single-image collection form: %v", fields)
			}
			return slowpicsTestCreated(slowpicsTestImageID), nil
		case "/upload/image/" + slowpicsTestImageID:
			members++
			if !bytes.Equal(file, snapshot) {
				t.Fatal("single-image snapshot changed")
			}
			return slowpicsTestResponse(http.StatusOK, "", "OK"), nil
		default:
			t.Fatalf("single image used unsupported comparison endpoint: %s", req.URL.Path)
			return nil, nil
		}
	})}
	result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
	if err != nil || result == "" || creates != 1 || members != 1 {
		t.Fatalf("result=%q err=%v creates=%d members=%d", result, err, creates, members)
	}
}

func TestSlowpicsComparisonRejectsIncompleteRowsAndResponseCells(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	input := slowpicsTestInput(path)
	input.Columns = 2
	if _, err := validateSlowpicsCollectionRequest(t.Context(), input); err == nil {
		t.Fatal("accepted an incomplete source row")
	}
	for _, rows := range [][][]string{
		{{slowpicsTestImageID, slowpicsTestSecondImageID}},
		{{slowpicsTestImageID}, {}},
		{{slowpicsTestImageID}, {slowpicsTestImageID}},
		{{slowpicsTestImageID}, {"invalid"}},
	} {
		body, err := json.Marshal(slowpicsCreatedCollection{
			CollectionUUID: slowpicsTestCollectionID,
			Key:            "SyntheticKey",
			Images:         rows,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parseSlowpicsCollection(slowpicsResponse{
			status:      http.StatusOK,
			contentType: "application/json",
			body:        body,
		}, 2, 1); err == nil {
			t.Fatalf("accepted invalid response cells: %v", rows)
		}
	}
}

func TestSlowpicsValidatesEveryPNGBeforeRequests(t *testing.T) {
	t.Parallel()
	validPath, data := slowpicsTestPNG(t)
	badPath := filepath.Join(t.TempDir(), "private-source.png")
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "not PNG", data: []byte("not a PNG")},
		{name: "empty"},
		{name: "truncated", data: data[:len(data)-8]},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(badPath, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			called := false
			client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				called = true
				return nil, errors.New("unexpected network request")
			})}
			result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsCollectionRequest{
				Name: "Synthetic", Images: []slowpicsCollectionImage{{Name: "One", Path: validPath}, {Name: "Two", Path: badPath}},
			})
			if err == nil || result != "" || called || errors.Is(err, errSlowpicsOutcomeUnknown) || strings.Contains(err.Error(), badPath) {
				t.Fatalf("result=%q error=%v network=%v", result, err, called)
			}
		})
	}
}

func TestSlowpicsLogsSafeFailureDiagnostics(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, endpoint, contentType, body, stage, reason string
		status                                           int
		transportFailure                                 bool
	}{
		{
			name:     "session rejected",
			endpoint: "/comparison",
			status:   http.StatusForbidden,
			stage:    "session",
			reason:   "anonymous session unavailable",
		},
		{
			name:     "create rejected",
			endpoint: "/upload/collection",
			status:   http.StatusBadRequest,
			stage:    "create",
			reason:   "collection creation unavailable",
		},
		{
			name:        "create HTML",
			endpoint:    "/upload/collection",
			status:      http.StatusOK,
			contentType: "text/html",
			body:        "<html>private-response-secret</html>",
			stage:       "create",
			reason:      "not valid JSON",
		},
		{
			name:             "create transport",
			endpoint:         "/upload/collection",
			transportFailure: true,
			stage:            "create",
			reason:           "request failed",
		},
		{
			name:     "member rejected",
			endpoint: "/upload/image/" + slowpicsTestImageID,
			status:   http.StatusForbidden,
			stage:    "member",
			reason:   "image upload was not acknowledged",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path, _ := slowpicsTestPNG(t)
			logger := &recordingImageHostLogger{}
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == test.endpoint {
					if test.transportFailure {
						return nil, errors.New("https://private.example.invalid/?token=private-transport-secret")
					}
					return slowpicsTestResponse(test.status, test.contentType, test.body), nil
				}
				if req.URL.Path == "/comparison" {
					return slowpicsTestBootstrapResponse(http.StatusOK), nil
				}
				if req.URL.Path == "/upload/collection" {
					return slowpicsTestCreated(slowpicsTestImageID), nil
				}
				t.Fatalf("unexpected request: %s", req.URL.Path)
				return nil, nil
			})}
			_, err := (&slowpicsUploader{client: client, logger: logger}).UploadCollection(t.Context(), slowpicsTestInput(path))
			if err == nil || !logger.contains("collection upload failed stage="+test.stage) || !logger.contains(test.reason) {
				t.Fatalf("missing failure diagnostics: error=%v logs=%v", err, logger.messages)
			}
			if !logger.contains(fmt.Sprintf("http_status=%d", test.status)) || !logger.contains("duration_ms=") {
				t.Fatalf("missing request diagnostics: %v", logger.messages)
			}
			for _, secret := range []string{path, "Synthetic Collection", "SyntheticKey", "synthetic-xsrf", "private-", slowpicsTestCollectionID, slowpicsTestImageID} {
				if logger.contains(secret) {
					t.Fatalf("private data in slow.pics diagnostics: %v", logger.messages)
				}
			}
		})
	}
}

func TestSlowpicsRejectsMalformedCollectionResponses(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	valid := fmt.Sprintf(`{"collectionUuid":%q,"key":"SyntheticKey","images":[[%q]]}`, slowpicsTestCollectionID, slowpicsTestImageID)
	for _, test := range []struct{ name, body, contentType string }{
		{
			name:        "HTML login",
			body:        "<html>Sign in</html>",
			contentType: "text/html",
		},
		{
			name:        "JSON with HTML type",
			body:        valid,
			contentType: "text/html",
		},
		{
			name:        "HTML with JSON type",
			body:        "<html>private token</html>",
			contentType: "application/json",
		},
		{
			name:        "malformed JSON",
			body:        "{",
			contentType: "application/json",
		},
		{
			name:        "trailing JSON",
			body:        valid + `{}`,
			contentType: "application/json",
		},
		{
			name:        "invalid UUID",
			body:        strings.Replace(valid, slowpicsTestCollectionID, "invalid-private-id", 1),
			contentType: "application/json",
		},
		{
			name:        "invalid key",
			body:        strings.Replace(valid, "SyntheticKey", "../private-key?token=secret", 1),
			contentType: "application/json",
		},
		{
			name:        "invalid image",
			body:        strings.Replace(valid, slowpicsTestImageID, "private-image", 1),
			contentType: "application/json",
		},
		{
			name:        "missing images",
			body:        strings.Replace(valid, `[["`+slowpicsTestImageID+`"]]`, `[]`, 1),
			contentType: "application/json",
		},
		{
			name:        "too many columns",
			body:        strings.Replace(valid, `[["`+slowpicsTestImageID+`"]]`, `[["`+slowpicsTestImageID+`","`+slowpicsTestSecondImageID+`"]]`, 1),
			contentType: "application/json",
		},
		{
			name:        "too many rows",
			body:        strings.Replace(valid, `[["`+slowpicsTestImageID+`"]]`, `[["`+slowpicsTestImageID+`"],[]]`, 1),
			contentType: "application/json",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path == "/comparison" {
					return slowpicsTestBootstrapResponse(http.StatusOK), nil
				}
				if req.URL.Path != "/upload/collection" {
					t.Fatal("uploaded member after invalid create response")
				}
				return slowpicsTestResponse(http.StatusOK, test.contentType, test.body), nil
			})}
			result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
			if result != "" || !errors.Is(err, errSlowpicsOutcomeUnknown) || calls != 2 {
				t.Fatalf("result=%q error=%v calls=%d", result, err, calls)
			}
			for _, secret := range []string{path, slowpicsTestCollectionID, slowpicsTestImageID, "private", "secret", test.body} {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatalf("unsafe error: %v", err)
				}
			}
		})
	}
}

func TestSlowpicsRejectsDuplicateImageIDs(t *testing.T) {
	t.Parallel()
	response := slowpicsResponse{
		status:      http.StatusOK,
		contentType: "application/json",
		body: []byte(fmt.Sprintf(
			`{"collectionUuid":%q,"key":"SyntheticKey","images":[[%q,%q]]}`, slowpicsTestCollectionID, slowpicsTestImageID, strings.ToUpper(slowpicsTestImageID),
		)),
	}
	if _, err := parseSlowpicsCollection(response, 2, 2); err == nil {
		t.Fatal("accepted duplicate image IDs")
	}
}

func TestSlowpicsNeverRetriesAmbiguousCreation(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			creates := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/comparison" {
					return slowpicsTestBootstrapResponse(http.StatusOK), nil
				}
				creates++
				return slowpicsTestResponse(status, "application/json", `{"error":"private-token"}`), nil
			})}
			_, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
			if !errors.Is(err, errSlowpicsOutcomeUnknown) || creates != 1 || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("error=%v creates=%d", err, creates)
			}
		})
	}
}

func TestSlowpicsRateLimitedCreation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, retryAfter                                string
		wait                                            time.Duration
		creates                                         int
		nextStatus                                      int
		failure, unknown, cancel, persistent, transport bool
	}{
		{
			name:    "missing header",
			wait:    time.Minute,
			creates: 2,
		},
		{
			name:       "invalid header",
			retryAfter: "invalid",
			wait:       time.Minute,
			creates:    2,
		},
		{
			name:       "seconds",
			retryAfter: "65",
			wait:       65 * time.Second,
			creates:    2,
		},
		{
			name:       "date",
			retryAfter: "date",
			wait:       70 * time.Second,
			creates:    2,
		},
		{
			name:       "persistent",
			wait:       3 * time.Minute,
			creates:    3,
			failure:    true,
			persistent: true,
		},
		{
			name:       "excessive delay",
			retryAfter: "301",
			creates:    1,
			failure:    true,
		},
		{
			name:       "overflow",
			retryAfter: "99999999999999999999999",
			creates:    1,
			failure:    true,
		},
		{
			name:       "transport after rejection",
			retryAfter: "0",
			creates:    2,
			failure:    true,
			unknown:    true,
			transport:  true,
		},
		{
			name:       "server failure after rejection",
			retryAfter: "0",
			creates:    2,
			failure:    true,
			unknown:    true,
			nextStatus: http.StatusServiceUnavailable,
		},
		{
			name:    "canceled wait",
			wait:    10 * time.Second,
			creates: 1,
			failure: true,
			cancel:  true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				path, _ := slowpicsTestPNG(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				logger := &recordingImageHostLogger{}
				var bodies [][]byte
				members := 0
				started := time.Now()
				client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					switch req.URL.Path {
					case "/comparison":
						return slowpicsTestBootstrapResponse(http.StatusOK), nil
					case "/upload/collection":
						body, err := io.ReadAll(req.Body)
						if err != nil {
							t.Fatal(err)
						}
						bodies = append(bodies, body)
						if len(bodies) > 1 && test.transport {
							return nil, errors.New("private transport response")
						}
						if len(bodies) > 1 && test.nextStatus != 0 {
							return slowpicsTestResponse(test.nextStatus, "application/json", "{}"), nil
						}
						if len(bodies) > 1 && !test.persistent {
							return slowpicsTestCreated(slowpicsTestImageID), nil
						}
						response := slowpicsTestResponse(http.StatusTooManyRequests, "application/json", `{"message":"private rate limit response"}`)
						value := test.retryAfter
						if value == "date" {
							value = started.Add(70 * time.Second).UTC().Format(http.TimeFormat)
						}
						response.Header.Set("Retry-After", value)
						if test.cancel {
							time.AfterFunc(10*time.Second, cancel)
						}
						return response, nil
					default:
						members++
						return slowpicsTestResponse(http.StatusNoContent, "", ""), nil
					}
				})}
				result, err := (&slowpicsUploader{client: client, logger: logger}).UploadCollection(ctx, slowpicsTestInput(path))
				if (err != nil) != test.failure || errors.Is(err, errSlowpicsOutcomeUnknown) != test.unknown ||
					len(bodies) != test.creates || time.Since(started) != test.wait || (result == "") != test.failure {
					t.Fatalf("result=%q error=%v creates=%d elapsed=%s", result, err, len(bodies), time.Since(started))
				}
				if test.cancel && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
				if test.failure && members != 0 || !test.failure && members != 1 {
					t.Fatalf("members=%d", members)
				}
				for _, body := range bodies {
					if !bytes.Equal(body, bodies[0]) {
						t.Fatal("creation retry changed payload")
					}
				}
				if test.wait > 0 && !logger.contains("stage=create state=rate_limited") {
					t.Fatalf("missing wait diagnostics: %v", logger.messages)
				}
				if logger.contains("private") || logger.contains("Synthetic Collection") || logger.contains("synthetic-xsrf") {
					t.Fatalf("unsafe logs: %v", logger.messages)
				}
			})
		})
	}
}

func TestSlowpicsRejectsAllRedirects(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	for _, endpoint := range []string{"/comparison", "/upload/collection", "/upload/image/" + slowpicsTestImageID} {
		for _, destination := range []string{slowpicsOrigin + "/login", "https://example.invalid/private"} {
			t.Run(endpoint+destination, func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.URL.Path == endpoint {
						response := slowpicsTestResponse(http.StatusTemporaryRedirect, "text/html", "redirect")
						response.Header.Set("Location", destination)
						return response, nil
					}
					switch req.URL.Path {
					case "/comparison":
						return slowpicsTestBootstrapResponse(http.StatusOK), nil
					case "/upload/collection":
						return slowpicsTestCreated(slowpicsTestImageID), nil
					default:
						t.Fatalf("followed redirect to %s", req.URL)
						return nil, nil
					}
				})}
				result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
				if result != "" || err == nil || errors.Is(err, errSlowpicsOutcomeUnknown) != (endpoint != "/comparison") || calls > 3 {
					t.Fatalf("result=%q error=%v calls=%d", result, err, calls)
				}
			})
		}
	}
}

func TestSlowpicsRetriesOnlyMembersWithStablePayload(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	logger := &recordingImageHostLogger{}
	var bodies [][]byte
	creates := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/comparison":
			return slowpicsTestBootstrapResponse(http.StatusOK), nil
		case "/upload/collection":
			creates++
			return slowpicsTestCreated(slowpicsTestImageID), nil
		case "/upload/image/" + slowpicsTestImageID:
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			bodies = append(bodies, body)
			status := []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK}[len(bodies)-1]
			response := slowpicsTestResponse(status, "application/json", "{}")
			response.Header.Set("Retry-After", "0")
			return response, nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, nil
		}
	})}
	result, err := (&slowpicsUploader{client: client, logger: logger}).UploadCollection(t.Context(), slowpicsTestInput(path))
	if err != nil || result == "" || creates != 1 || len(bodies) != 3 || !bytes.Equal(bodies[0], bodies[1]) || !bytes.Equal(bodies[1], bodies[2]) {
		t.Fatalf("result=%q error=%v creates=%d attempts=%d", result, err, creates, len(bodies))
	}
	if !logger.contains("state=retrying attempt=2 http_status=429 delay_ms=0") ||
		!logger.contains("state=retrying attempt=3 http_status=503 delay_ms=0") ||
		logger.countLevelContaining("WARN", "slow.pics") != 0 {
		t.Fatalf("missing successful retry diagnostics: %v", logger.messages)
	}
}

func TestSlowpicsMemberFailuresNeverYieldCollectionURL(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	for _, test := range []struct {
		name              string
		status            int
		contentType, body string
		transportError    bool
		wantAttempts      int
	}{
		{
			name:         "rejected",
			status:       http.StatusForbidden,
			wantAttempts: 1,
		},
		{
			name:         "challenge",
			status:       http.StatusOK,
			contentType:  "text/html",
			body:         "<html>private-token</html>",
			wantAttempts: 1,
		},
		{
			name:         "hidden HTML",
			status:       http.StatusOK,
			contentType:  "application/json",
			body:         "<html>private-token</html>",
			wantAttempts: 1,
		},
		{
			name:         "exhausted",
			status:       http.StatusServiceUnavailable,
			wantAttempts: 3,
		},
		{
			name:           "transport",
			transportError: true,
			wantAttempts:   1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/comparison":
					return slowpicsTestBootstrapResponse(http.StatusOK), nil
				case "/upload/collection":
					return slowpicsTestCreated(slowpicsTestImageID), nil
				default:
					attempts++
					if test.transportError {
						return nil, fmt.Errorf("private-token %s", path)
					}
					response := slowpicsTestResponse(test.status, test.contentType, test.body)
					response.Header.Set("Retry-After", "0")
					return response, nil
				}
			})}
			result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
			if result != "" || !errors.Is(err, errSlowpicsOutcomeUnknown) || attempts != test.wantAttempts || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), path) {
				t.Fatalf("result=%q error=%v attempts=%d", result, err, attempts)
			}
		})
	}
}

func TestSlowpicsCancellationDuringRetry(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attempts := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/comparison":
			return slowpicsTestBootstrapResponse(http.StatusOK), nil
		case "/upload/collection":
			return slowpicsTestCreated(slowpicsTestImageID), nil
		default:
			attempts++
			cancel()
			response := slowpicsTestResponse(http.StatusTooManyRequests, "application/json", "{}")
			response.Header.Set("Retry-After", "30")
			return response, nil
		}
	})}
	start := time.Now()
	result, err := (&slowpicsUploader{client: client}).UploadCollection(ctx, slowpicsTestInput(path))
	if result != "" || !errors.Is(err, errSlowpicsOutcomeUnknown) || !errors.Is(err, context.Canceled) || attempts != 1 || time.Since(start) > time.Second {
		t.Fatalf("result=%q error=%v attempts=%d elapsed=%s", result, err, attempts, time.Since(start))
	}
}

func TestSlowpicsRetryAfterBounds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		value   string
		attempt int
		delay   time.Duration
		retry   bool
	}{
		{
			value: "0",
			delay: 0,
			retry: true,
		},
		{
			value: "2",
			delay: 2 * time.Second,
			retry: true,
		},
		{
			value: "30",
			delay: 30 * time.Second,
			retry: true,
		},
		{value: "31", retry: false},
		{value: "9223372036854775807", retry: false},
		{value: "999999999999999999999999999999", retry: false},
		{
			value: now.Add(5 * time.Second).Format(http.TimeFormat),
			delay: 5 * time.Second,
			retry: true,
		},
		{
			value: now.Add(time.Minute).Format(http.TimeFormat),
			delay: time.Minute,
			retry: false,
		},
		{
			value: now.Add(-time.Minute).Format(http.TimeFormat),
			delay: 0,
			retry: true,
		},
		{
			value:   "invalid",
			attempt: 1,
			delay:   2 * time.Second,
			retry:   true,
		},
		{
			value: "-1",
			delay: time.Second,
			retry: true,
		},
	} {
		delay, retry := slowpicsRetryDelay(test.value, now, time.Duration(test.attempt+1)*time.Second, slowpicsMaxRetryDelay)
		if delay != test.delay || retry != test.retry {
			t.Errorf("Retry-After %q = (%s,%v), want (%s,%v)", test.value, delay, retry, test.delay, test.retry)
		}
	}
}

func TestSlowpicsBoundsResponsesAndSanitizesTransportFailure(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	for _, stage := range []string{"/comparison", "/upload/collection", "/upload/image/" + slowpicsTestImageID} {
		for _, oversized := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/oversized=%v", stage, oversized), func(t *testing.T) {
				client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == stage {
						if oversized {
							return slowpicsTestResponse(http.StatusOK, "application/json", strings.Repeat("x", int(slowpicsMaxResponseBytes+1))), nil
						}
						return nil, fmt.Errorf("private-token %s %s", slowpicsTestImageID, path)
					}
					if req.URL.Path == "/comparison" {
						return slowpicsTestBootstrapResponse(http.StatusOK), nil
					}
					return slowpicsTestCreated(slowpicsTestImageID), nil
				})}
				result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
				if result != "" || err == nil || errors.Is(err, errSlowpicsOutcomeUnknown) != (stage != "/comparison") {
					t.Fatalf("result=%q error=%v", result, err)
				}
				for _, secret := range []string{"private-token", path, slowpicsTestImageID} {
					if strings.Contains(err.Error(), secret) {
						t.Fatalf("unsafe error: %v", err)
					}
				}
			})
		}
	}
}

func slowpicsTestPNG(t *testing.T) (string, []byte) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private-screenshot.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, encoded.Bytes()
}

func slowpicsTestInput(path string) slowpicsCollectionRequest {
	return slowpicsCollectionRequest{Name: "Synthetic Collection", Images: []slowpicsCollectionImage{{Name: "One", Path: path}}}
}

func slowpicsTestCreated(ids ...string) *http.Response {
	quoted := make([]string, len(ids))
	for index, id := range ids {
		quoted[index] = fmt.Sprintf("%q", id)
	}
	return slowpicsTestResponse(http.StatusOK, "application/json", fmt.Sprintf(
		`{"collectionUuid":%q,"key":"SyntheticKey","images":[[%s]]}`, slowpicsTestCollectionID, strings.Join(quoted, ","),
	))
}

func slowpicsTestResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func slowpicsTestMultipart(t *testing.T, req *http.Request) (map[string]string, []byte) {
	t.Helper()
	mediaType, parameters, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("invalid multipart type: %v", err)
	}
	reader := multipart.NewReader(req.Body, parameters["boundary"])
	fields := make(map[string]string)
	var file []byte
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if part.FileName() != "" {
			if part.FormName() != "file" || part.FileName() != "image.png" || part.Header.Get("Content-Type") != "image/png" || file != nil {
				t.Fatal("invalid PNG part")
			}
			file = data
		} else {
			if _, exists := fields[part.FormName()]; exists {
				t.Fatal("duplicate field")
			}
			fields[part.FormName()] = string(data)
		}
	}
	return fields, file
}

func TestSlowpicsLocalSafetyLimits(t *testing.T) {
	t.Parallel()
	path, data := slowpicsTestPNG(t)
	for _, label := range []string{"", "/private/media/image.png", `C:\private\image.png`, "<script>", "line\nbreak", strings.Repeat("x", 121)} {
		input := slowpicsTestInput(path)
		input.Images[0].Name = label
		if _, err := validateSlowpicsCollectionRequest(t.Context(), input); err == nil {
			t.Errorf("accepted unsanitized label %q", label)
		}
	}
	input := slowpicsTestInput(path)
	input.Images = make([]slowpicsCollectionImage, 101)
	if _, err := validateSlowpicsCollectionRequest(t.Context(), input); err == nil {
		t.Fatal("accepted too many images")
	}
	if _, err := readSlowpicsPNG(path, int64(len(data)-1)); err == nil {
		t.Fatal("accepted image beyond remaining byte budget")
	}
	if _, err := readSlowpicsPNG(t.TempDir(), slowpicsMaxImageBytes); err == nil {
		t.Fatal("accepted directory")
	}
	oversized := append([]byte(nil), data...)
	binary.BigEndian.PutUint32(oversized[16:20], 32768)
	binary.BigEndian.PutUint32(oversized[20:24], 4096)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSlowpicsPNG(path, slowpicsMaxImageBytes); err == nil || !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("oversized PNG dimensions: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := validateSlowpicsCollectionRequest(ctx, slowpicsTestInput(path)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled validation: %v", err)
	}
}

func TestSlowpicsAcceptsSuccessfulMemberAcknowledgements(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusNoContent} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/comparison":
					return slowpicsTestBootstrapResponse(http.StatusOK), nil
				case "/upload/collection":
					response := slowpicsTestCreated(slowpicsTestImageID)
					response.StatusCode = http.StatusCreated
					return response, nil
				default:
					return slowpicsTestResponse(status, "", ""), nil
				}
			})}
			result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
			if err != nil || result == "" {
				t.Fatalf("result=%q error=%v", result, err)
			}
		})
	}
}

func TestSlowpicsPartialMemberFailureDoesNotPublishURL(t *testing.T) {
	t.Parallel()
	first, _ := slowpicsTestPNG(t)
	second, _ := slowpicsTestPNG(t)
	members := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/comparison":
			return slowpicsTestBootstrapResponse(http.StatusOK), nil
		case "/upload/comparison":
			return slowpicsTestCreated(slowpicsTestImageID, slowpicsTestSecondImageID), nil
		default:
			members++
			if members == 1 {
				return slowpicsTestResponse(http.StatusNoContent, "", ""), nil
			}
			return slowpicsTestResponse(http.StatusForbidden, "application/json", "{}"), nil
		}
	})}
	result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsCollectionRequest{
		Name:   "Synthetic Collection",
		Images: []slowpicsCollectionImage{{Name: "One", Path: first}, {Name: "Two", Path: second}},
	})
	if result != "" || !errors.Is(err, errSlowpicsOutcomeUnknown) || members != 2 {
		t.Fatalf("result=%q error=%v members=%d", result, err, members)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("local source removed: %v", err)
		}
	}
}

func slowpicsTestBootstrapResponse(status int) *http.Response {
	response := slowpicsTestResponse(status, "text/html", "")
	response.Header.Add("Set-Cookie", "XSRF-TOKEN=synthetic-token; Path=/upload; Secure")
	return response
}

func TestSlowpicsRejectsInvalidBootstrapTokenBeforeCreate(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	for _, cookie := range []string{
		"", "XSRF-TOKEN=; Path=/upload; Secure",
		"XSRF-TOKEN=synthetic; Path=/comparison; Secure",
		"XSRF-TOKEN=synthetic; Domain=example.invalid; Path=/upload; Secure",
		"XSRF-TOKEN=%; Path=/upload; Secure",
		"XSRF-TOKEN=%0D%0A; Path=/upload; Secure",
		"XSRF-TOKEN=%00; Path=/upload; Secure",
		"XSRF-TOKEN=%7F; Path=/upload; Secure",
	} {
		t.Run(cookie, func(t *testing.T) {
			posts := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					posts++
					return slowpicsTestCreated(slowpicsTestImageID), nil
				}
				response := slowpicsTestResponse(http.StatusOK, "text/html", "")
				if cookie != "" {
					response.Header.Add("Set-Cookie", cookie)
				}
				return response, nil
			})}
			result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
			if result != "" || err == nil || errors.Is(err, errSlowpicsOutcomeUnknown) || posts != 0 {
				t.Fatalf("result=%q error=%v posts=%d", result, err, posts)
			}
		})
	}
}

func TestSlowpicsLostTokenAfterCreateKeepsUnknownOutcome(t *testing.T) {
	t.Parallel()
	path, _ := slowpicsTestPNG(t)
	members := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/comparison":
			return slowpicsTestBootstrapResponse(http.StatusOK), nil
		case "/upload/collection":
			response := slowpicsTestCreated(slowpicsTestImageID)
			response.Header.Add("Set-Cookie", "XSRF-TOKEN=; Path=/upload; Secure; Max-Age=0")
			return response, nil
		default:
			members++
			return slowpicsTestResponse(http.StatusNoContent, "", ""), nil
		}
	})}
	result, err := (&slowpicsUploader{client: client}).UploadCollection(t.Context(), slowpicsTestInput(path))
	if result != "" || !errors.Is(err, errSlowpicsOutcomeUnknown) || members != 0 {
		t.Fatalf("result=%q error=%v members=%d", result, err, members)
	}
}
