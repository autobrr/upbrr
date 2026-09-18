// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
)

func TestBTNGroupOnlyUploadRetainsSuccessOnAPIError(t *testing.T) {
	t.Parallel()
	var apiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload.php" {
			http.Redirect(w, r, "/torrents.php?id=123", http.StatusFound)
			return
		}
		if r.URL.Path == "/rpc" {
			apiCalls.Add(1)
			_, _ = io.WriteString(w, `{"error":{"code":-32004,"message":"private upstream detail","data":"secret-token"},"result":null}`)
			return
		}
		_, _ = io.WriteString(w, "torrent group")
	}))
	defer server.Close()
	req := newBTNUploadTestRequest(t)
	req, failure := trackers.PrepareInputWithReleaseNamePolicy(req, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	logger := &captureBTNLogger{}
	req.Logger = logger
	summary, err := submitPreparedUpload(t.Context(), req, uploadContext{
		baseURL:   server.URL,
		uploadURL: server.URL + "/upload.php",
		apiURL:    server.URL + "/rpc",
		apiToken:  "test-api-token",
		client:    server.Client(),
	}, filepath.Join(t.TempDir(), "registered.torrent"), nil, "application/octet-stream")
	if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
		t.Fatalf("accepted upload must remain successful: summary=%+v err=%v", summary, err)
	}
	torrent := summary.UploadedTorrents[0]
	if torrent.TorrentURL != server.URL+"/torrents.php?id=123" || torrent.TorrentID != "" || torrent.TorrentPath != "" {
		t.Fatalf("unexpected artifact identity after failed lookup: %+v", torrent)
	}
	logs := strings.Join(logger.warnings, "\n")
	if !strings.Contains(logs, "getTorrents rejected request code=-32004") {
		t.Fatal("missing API failure diagnostic")
	}
	if strings.Contains(logs, "private upstream detail") || strings.Contains(logs, "secret-token") || strings.Contains(logs, "test-api-token") {
		t.Fatal("API diagnostic exposed private response data")
	}
	if apiCalls.Load() != 1 {
		t.Fatalf("API errors must not retry: calls=%d", apiCalls.Load())
	}
}

func TestBTNAPIVisibilityRetriesAreBounded(t *testing.T) {
	t.Parallel()
	for _, intermediate := range []bool{false, true} {
		t.Run(fmt.Sprintf("intermediate=%t", intermediate), func(t *testing.T) {
			t.Parallel()
			var apiCalls, uploadCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/upload.php":
					uploadCalls.Add(1)
					if intermediate {
						_, _ = io.WriteString(w, `<p>You need to download the torrent file.</p><form action="/torrents.php?id=123"></form>`)
					} else {
						http.Redirect(w, r, "/torrents.php?id=123", http.StatusFound)
					}
				case "/rpc":
					apiCalls.Add(1)
					_, _ = io.WriteString(w, `{"result":{"results":"0"}}`)
				default:
					http.Error(w, "detail unavailable", http.StatusInternalServerError)
				}
			}))
			defer server.Close()
			req, failure := trackers.PrepareInputWithReleaseNamePolicy(newBTNUploadTestRequest(t), Profile().ReleaseNamePolicy)
			if failure != nil {
				t.Fatal(failure)
			}
			summary, err := submitPreparedUpload(t.Context(), req, uploadContext{
				baseURL:   server.URL,
				uploadURL: server.URL + "/upload.php",
				apiURL:    server.URL + "/rpc",
				apiToken:  "test-api-token",
				client:    server.Client(),
			}, filepath.Join(t.TempDir(), "registered.torrent"), nil, "application/octet-stream")
			if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 || summary.UploadedTorrents[0].TorrentPath != "" {
				t.Fatalf("accepted upload must survive exhausted lookup: summary=%+v err=%v", summary, err)
			}
			if uploadCalls.Load() != 1 || apiCalls.Load() != 4 {
				t.Fatalf("unexpected retry counts: uploads=%d searches=%d", uploadCalls.Load(), apiCalls.Load())
			}
		})
	}
}

type btnCancelRetryLogger struct {
	captureBTNLogger
	cancel context.CancelFunc
}

func (l *btnCancelRetryLogger) Infof(string, ...any) {
	l.cancel()
}

func TestBTNAPIVisibilityWaitIsCancellable(t *testing.T) {
	t.Parallel()
	var apiCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalls.Add(1)
		_, _ = io.WriteString(w, `{"result":{"results":"0"}}`)
	}))
	defer server.Close()
	req, failure := trackers.PrepareInputWithReleaseNamePolicy(newBTNUploadTestRequest(t), Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req.Logger = &btnCancelRetryLogger{cancel: cancel}
	_, _, err := resolveAndDownloadViaAPI(ctx, server.URL, "test-api-token", req, "123", filepath.Join(t.TempDir(), "registered.torrent"))
	if !errors.Is(err, context.Canceled) || apiCalls.Load() != 1 {
		t.Fatalf("cancellation failed: calls=%d err=%v", apiCalls.Load(), err)
	}
}

func TestBTNGroupOnlyUploadWaitsForAPIVisibility(t *testing.T) {
	t.Parallel()
	var apiSearchCalls, uploadCalls atomic.Int32
	req, failure := trackers.PrepareInputWithReleaseNamePolicy(newBTNUploadTestRequest(t), Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	releaseName, err := req.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	handlerErrs := newHTTPHandlerErrorRecorder(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload.php":
			uploadCalls.Add(1)
			http.Redirect(w, r, "/torrents.php?id=123", http.StatusFound)
		case "/rpc":
			var rpc struct {
				Method string `json:"method"`
				Params struct {
					Key     string            `json:"key"`
					ID      string            `json:"id"`
					Search  map[string]string `json:"search"`
					Results int               `json:"results"`
				} `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
				handlerErrs.Errorf("decode request: %v", err)
				return
			}
			if rpc.Params.Key != "test-api-token" {
				handlerErrs.Errorf("missing named API key")
			}
			switch rpc.Method {
			case "getTorrents":
				if len(rpc.Params.Search) != 1 || rpc.Params.Search["group_id"] != "123" || rpc.Params.Results != 1000 {
					handlerErrs.Errorf("incorrect upload lookup filter: %v", rpc.Params.Search)
				}
				if apiSearchCalls.Add(1) < 3 {
					_, _ = io.WriteString(w, `{"result":{"results":"0"}}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": map[string]any{
						"results": "3",
						"torrents": map[string]any{
							"458": map[string]string{
								"GroupID":     "999",
								"ReleaseName": releaseName,
							},
							"457": map[string]string{
								"GroupID":     "123",
								"ReleaseName": strings.Replace(releaseName, "1080p", "720p", 1),
							},
							"456": map[string]string{
								"TorrentID":   "456",
								"GroupID":     "123",
								"ReleaseName": " " + strings.ToUpper(releaseName) + " ",
							},
						},
					},
				})
			case "getTorrentById":
				if rpc.Params.ID != "456" {
					handlerErrs.Errorf("incorrect torrent lookup ID: %q", rpc.Params.ID)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"result": map[string]string{"DownloadURL": "http://" + r.Host + "/download"},
				})
			default:
				handlerErrs.Errorf("unexpected method: %s", rpc.Method)
			}
		case "/download":
			_, _ = w.Write(btnRegisteredTorrentFixture())
		default:
			_, _ = io.WriteString(w, "torrent group")
		}
	}))
	defer server.Close()
	outputPath := filepath.Join(t.TempDir(), "registered.torrent")
	summary, err := submitPreparedUpload(t.Context(), req, uploadContext{
		baseURL:   server.URL,
		uploadURL: server.URL + "/upload.php",
		apiURL:    server.URL + "/rpc",
		apiToken:  "test-api-token",
		client:    server.Client(),
	}, outputPath, nil, "application/octet-stream")
	handlerErrs.Check()
	if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
		t.Fatalf("upload result: %+v err=%v", summary, err)
	}
	torrent := summary.UploadedTorrents[0]
	if torrent.TorrentID != "456" || torrent.TorrentPath != outputPath || torrent.TorrentURL != server.URL+"/torrents.php?id=123&torrentid=456" {
		t.Fatalf("incorrect registered artifact: %+v", torrent)
	}
	if _, err := os.Stat(torrent.TorrentPath); err != nil {
		t.Fatalf("registered torrent unavailable for injection: %v", err)
	}
	if uploadCalls.Load() != 1 || apiSearchCalls.Load() != 3 {
		t.Fatalf("unexpected retry counts: uploads=%d searches=%d", uploadCalls.Load(), apiSearchCalls.Load())
	}
}

func TestBTNUploadReadsRegisteredTorrentLink(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		page         string
		redirect     bool
		wantAPI      bool
		failAPI      bool
		unknownGroup bool
		pageStatus   int
	}{
		{
			name:     "redirected warning with canonical link after group navigation",
			page:     `<p>You need to download the torrent file.</p><a href="/torrents.php?id=123">Group</a><a href="/torrents.php?torrentid=456&amp;id=123">Uploaded torrent</a>`,
			redirect: true,
		},
		{
			name: "warning download link before continue",
			page: `<p>You need to download the torrent file.</p><a href="/torrents.php?action=download&amp;id=456">Download</a><form action="/torrents.php?id=123"></form>`,
		},
		{
			name: "direct upload response canonical link",
			page: `<a href="/torrents.php?id=123&amp;torrentid=456">Uploaded torrent</a>`,
		},
		{
			name:     "redirected warning download link",
			page:     `<p>You need to download the torrent file.</p><a href="/torrents.php?action=download&amp;id=456">Download</a><form action="/torrents.php?id=123"></form>`,
			redirect: true,
		},
		{
			name:     "sole older group torrent uses API",
			page:     `<a href="/torrents.php?id=123&amp;torrentid=789">Older torrent</a>`,
			redirect: true,
			wantAPI:  true,
		},
		{
			name:    "intermediate API download failure uses authenticated download",
			page:    `<p>You need to download the torrent file.</p><form action="/torrents.php?id=123"></form>`,
			wantAPI: true,
			failAPI: true,
		},
		{
			name:     "warning conflicting canonical links use API",
			page:     `<p>You need to download the torrent file.</p><a href="/torrents.php?id=123&amp;torrentid=789">Older</a><a href="/torrents.php?id=123&amp;torrentid=456">New</a>`,
			redirect: true,
			wantAPI:  true,
		},
		{
			name:     "warning conflicting download and canonical links use API",
			page:     `<p>You need to download the torrent file.</p><a href="/torrents.php?action=download&amp;id=456">Download</a><a href="/torrents.php?id=123&amp;torrentid=789">Older</a>`,
			redirect: true,
			wantAPI:  true,
		},
		{
			name:         "direct response conflicting groups use name search",
			page:         `<a href="/torrents.php?id=999&amp;torrentid=789">Older</a><a href="/torrents.php?id=123&amp;torrentid=456">New</a>`,
			wantAPI:      true,
			unknownGroup: true,
		},
		{
			name:     "warning unrelated canonical group uses confirmed group API",
			page:     `<p>You need to download the torrent file.</p><a href="/torrents.php?id=999&amp;torrentid=789">Other group</a>`,
			redirect: true,
			wantAPI:  true,
		},
		{
			name:       "failed group page ignores old warning link",
			page:       `<p>You need to download the torrent file.</p><a href="/torrents.php?id=123&amp;torrentid=789">Older torrent</a>`,
			redirect:   true,
			wantAPI:    true,
			pageStatus: http.StatusInternalServerError,
		},
		{
			name:     "ambiguous group links use API",
			page:     `<a href="/torrents.php?id=123&amp;torrentid=789">Other torrent</a><a href="/torrents.php?id=123&amp;torrentid=456">Uploaded torrent</a>`,
			redirect: true,
			wantAPI:  true,
		},
		{
			name:     "unrelated group and external links use API",
			page:     `<a href="https://example.invalid/torrents.php?id=123&amp;torrentid=789">External</a><a href="/torrents.php?id=999&amp;torrentid=789">Other group</a>`,
			redirect: true,
			wantAPI:  true,
		},
		{
			name:     "ordinary group download link is not upload identity",
			page:     `<a href="/torrents.php?action=download&amp;id=789">Existing torrent</a>`,
			redirect: true,
			wantAPI:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, failure := trackers.PrepareInputWithReleaseNamePolicy(newBTNUploadTestRequest(t), Profile().ReleaseNamePolicy)
			if failure != nil {
				t.Fatal(failure)
			}
			releaseName, err := req.ReviewedUploadName()
			if err != nil {
				t.Fatal(err)
			}
			var uploads, searches, downloads atomic.Int32
			handlerErrs := newHTTPHandlerErrorRecorder(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/upload.php":
					uploads.Add(1)
					http.SetCookie(w, &http.Cookie{
						Name:  "session",
						Value: "test-session",
						Path:  "/",
					})
					if tc.redirect {
						http.Redirect(w, r, "/torrents.php?id=123", http.StatusFound)
					} else {
						_, _ = io.WriteString(w, tc.page)
					}
				case "/torrents.php":
					if r.URL.Query().Get("action") == "download" {
						if r.URL.Query().Get("id") != "456" {
							handlerErrs.Errorf("downloaded another torrent: %s", r.URL)
						}
						if tc.failAPI {
							if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "test-session" {
								handlerErrs.Errorf("missing authenticated download session")
								http.Error(w, "unauthorized", http.StatusUnauthorized)
								return
							}
						}
						downloads.Add(1)
						_, _ = w.Write(btnRegisteredTorrentFixture())
					} else {
						if !tc.redirect && !tc.wantAPI {
							handlerErrs.Errorf("warning download link should avoid fetching the group page")
						}
						if tc.failAPI {
							http.Error(w, "detail unavailable", http.StatusInternalServerError)
							return
						}
						if tc.pageStatus != 0 {
							w.WriteHeader(tc.pageStatus)
						}
						_, _ = io.WriteString(w, tc.page)
					}
				case "/rpc":
					var rpc struct {
						Method string `json:"method"`
						Params struct {
							ID     string            `json:"id"`
							Search map[string]string `json:"search"`
						} `json:"params"`
					}
					if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
						handlerErrs.Errorf("decode request: %v", err)
						return
					}
					switch rpc.Method {
					case "getTorrents":
						searches.Add(1)
						key, value := "group_id", "123"
						if tc.unknownGroup {
							key, value = "release", releaseName
						}
						if len(rpc.Params.Search) != 1 || rpc.Params.Search[key] != value {
							handlerErrs.Errorf("incorrect upload search: %v", rpc.Params.Search)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"torrents": map[string]any{
							"456": map[string]string{"GroupID": "123", "ReleaseName": releaseName},
						}}})
					case "getTorrentById":
						if rpc.Params.ID != "456" {
							handlerErrs.Errorf("resolved another torrent: %s", rpc.Params.ID)
						}
						if tc.failAPI {
							_, _ = io.WriteString(w, `{"error":{"code":-32002},"result":null}`)
							return
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]string{
							"DownloadURL": "http://" + r.Host + "/torrents.php?action=download&id=456",
						}})
					default:
						handlerErrs.Errorf("unexpected API method: %s", rpc.Method)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := server.Client()
			client.Jar, err = cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			outputPath := filepath.Join(t.TempDir(), "registered.torrent")
			summary, err := submitPreparedUpload(t.Context(), req, uploadContext{
				baseURL:   server.URL,
				uploadURL: server.URL + "/upload.php",
				apiURL:    server.URL + "/rpc",
				apiToken:  "test-api-token",
				client:    client,
			}, outputPath, nil, "application/octet-stream")
			handlerErrs.Check()
			if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
				t.Fatalf("upload result: %+v err=%v", summary, err)
			}
			torrent := summary.UploadedTorrents[0]
			if torrent.TorrentID != "456" || torrent.TorrentPath != outputPath || torrent.TorrentURL != server.URL+"/torrents.php?id=123&torrentid=456" {
				t.Fatalf("incorrect registered artifact: %+v", torrent)
			}
			payload, err := os.ReadFile(outputPath)
			if err != nil || !bytes.Equal(payload, btnRegisteredTorrentFixture()) {
				t.Fatalf("registered artifact not retained: %v", err)
			}
			wantSearches := int32(0)
			if tc.wantAPI {
				wantSearches = 1
			}
			if uploads.Load() != 1 || downloads.Load() != 1 || searches.Load() != wantSearches {
				t.Fatalf("unexpected calls: uploads=%d downloads=%d searches=%d", uploads.Load(), downloads.Load(), searches.Load())
			}
		})
	}
}

func TestBTNAPIResolutionRejectsSingleNonmatchingTorrent(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "Example.Show.S01E01.720p.WEB-DL.H.265-GRP"} {
		selection := selectBTNAPITorrent(map[string]map[string]any{
			"456": {"GroupID": "123", "ReleaseName": name},
		}, "Example.Show.S01E01.1080p.WEB-DL.H.265-GRP", "123")
		if selection.ID != "" {
			t.Fatalf("selected a nonmatching torrent before the upload was visible: %+v", selection)
		}
	}
}

func TestBTNOversizedUploadPageFallsBackToAPI(t *testing.T) {
	t.Parallel()
	for _, intermediate := range []bool{false, true} {
		for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("intermediate=%t/status=%d", intermediate, status), func(t *testing.T) {
				t.Parallel()
				req, failure := trackers.PrepareInputWithReleaseNamePolicy(newBTNUploadTestRequest(t), Profile().ReleaseNamePolicy)
				if failure != nil {
					t.Fatal(failure)
				}
				releaseName, err := req.ReviewedUploadName()
				if err != nil {
					t.Fatal(err)
				}
				handlerErrs := newHTTPHandlerErrorRecorder(t)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/download" {
						_, _ = w.Write(btnRegisteredTorrentFixture())
						return
					}
					var rpc struct {
						Method string `json:"method"`
						Params struct {
							Search map[string]string `json:"search"`
							ID     string            `json:"id"`
						} `json:"params"`
					}
					if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
						handlerErrs.Errorf("decode request: %v", err)
						return
					}
					switch rpc.Method {
					case "getTorrents":
						if rpc.Params.Search["group_id"] != "123" {
							handlerErrs.Errorf("confirmed group lost after oversized page: %v", rpc.Params.Search)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"torrents": map[string]any{
							"456": map[string]string{"GroupID": "123", "ReleaseName": releaseName},
						}}})
					case "getTorrentById":
						if rpc.Params.ID != "456" {
							handlerErrs.Errorf("selected torrent from truncated page: %q", rpc.Params.ID)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]string{"DownloadURL": "http://" + r.Host + "/download"}})
					default:
						handlerErrs.Errorf("unexpected method: %s", rpc.Method)
					}
				}))
				defer server.Close()
				page := `<p>You need to download the torrent file.</p><a href="/torrents.php?id=123&amp;torrentid=789">Older</a>` +
					strings.Repeat(" ", btnUploadPageMaxBytes+4096)
				pageReader := strings.NewReader(page)
				uploads := 0
				client := &http.Client{Transport: btnRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
					response := &http.Response{
						StatusCode: status,
						Header:     make(http.Header),
						Body:       io.NopCloser(pageReader),
						Request:    r,
					}

					if r.URL.Path == "/upload.php" {
						uploads++
						response.StatusCode = http.StatusOK
						if intermediate {
							response.Body = io.NopCloser(strings.NewReader(`<p>You need to download the torrent file.</p><form action="/torrents.php?id=123"></form>`))
						} else {
							response.StatusCode = http.StatusFound
							response.Header.Set("Location", "/torrents.php?id=123")
							response.Body = io.NopCloser(strings.NewReader(""))
						}
					} else if r.URL.Path != "/torrents.php" || r.URL.Query().Get("action") != "" {
						handlerErrs.Errorf("unexpected site request: %s", r.URL)
					}
					return response, nil
				})}
				outputPath := filepath.Join(t.TempDir(), "registered.torrent")
				summary, err := submitPreparedUpload(t.Context(), req, uploadContext{
					baseURL:   server.URL,
					uploadURL: server.URL + "/upload.php",
					apiURL:    server.URL + "/rpc",
					apiToken:  "test-api-token",
					client:    client,
				}, outputPath, nil, "application/octet-stream")
				handlerErrs.Check()
				if err != nil || summary.Uploaded != 1 || len(summary.UploadedTorrents) != 1 {
					t.Fatalf("confirmed upload lost: summary=%+v err=%v", summary, err)
				}
				if uploads != 1 || len(page)-pageReader.Len() > btnUploadPageMaxBytes+1 {
					t.Fatalf("unbounded read or resubmission: uploads=%d bytes=%d", uploads, len(page)-pageReader.Len())
				}
				if torrent := summary.UploadedTorrents[0]; torrent.TorrentID != "456" || torrent.TorrentPath != outputPath {
					t.Fatalf("incorrect retained artifact: %+v", torrent)
				}
				payload, err := os.ReadFile(outputPath)
				if err != nil || !bytes.Equal(payload, btnRegisteredTorrentFixture()) {
					t.Fatalf("registered artifact not retained: %v", err)
				}
			})
		}
	}
}

func TestBTNFailedUploadResponseCannotSupplyTorrentIdentity(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: btnRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Request:    r,
			Body:       io.NopCloser(strings.NewReader(`<p>You need to download the torrent file.</p><a href="/torrents.php?id=123&amp;torrentid=789">Older torrent</a>`)),
		}, nil
	})}
	summary, err := submitPreparedUpload(t.Context(), newBTNUploadTestRequest(t), uploadContext{
		baseURL:   "https://example.invalid",
		uploadURL: "https://example.invalid/upload.php",
		client:    client,
	}, filepath.Join(t.TempDir(), "registered.torrent"), nil, "application/octet-stream")
	if err == nil || summary.Uploaded != 0 || calls != 1 {
		t.Fatalf("failed upload supplied artifact authority: summary=%+v err=%v requests=%d", summary, err, calls)
	}
}
