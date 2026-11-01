// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/connectiontest"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestZiplineChecksOnlyVerifiedV3ReadEndpoint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		path, body, want string
		requests         int
	}{
		{"", ``, "missing_configuration", 0},
		{"/api/upload", `{"count":0}`, "success", 1},
		{"/api/upload", `{"count":-1}`, "unexpected_response", 1},
		{"/api/upload", `{"username":"synthetic-private-user"}`, "unexpected_response", 1},
		{"/custom/upload", ``, "unsupported", 0},
		{"/prefix/api/upload", ``, "unsupported", 0},
	} {
		t.Run(tc.path+tc.want, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/user/paged" || r.URL.RawQuery != "count=true" || r.Header.Get("Authorization") != "synthetic-zipline-key" {
					t.Error("probe used an unverified operation or wrong credentials")
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			cfg := config.Config{ImageHosting: config.ImageHostingConfig{ZiplineURL: server.URL + tc.path, ZiplineAPIKey: "synthetic-zipline-key"}}
			if tc.path == "" {
				cfg.ImageHosting.ZiplineURL = ""
			}
			got := connectiontest.Run(t.Context(), func(ctx context.Context, client *http.Client) api.ConnectionTestResult {
				return TestConnection(ctx, cfg.ImageHosting, "zipline", client)
			})
			if got.Code != tc.want || requests.Load() != int32(tc.requests) {
				t.Fatalf("result=%#v requests=%d", got, requests.Load())
			}
			if strings.Contains(got.Message, "synthetic-") {
				t.Fatal("remote content or key leaked")
			}
		})
	}
}

func TestPixhostProbeValidatesReadOnlyStatusContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ response, want string }{
		{`{"results":[{"url":"https://pixhost.to/show/1/1_upbrr-connection-test.jpg","status":"not_found"}]}`, "success"},
		{`{"results":[{"url":"https://pixhost.to/show/1/1_upbrr-connection-test.jpg","status":"invalid"}]}`, "invalid_probe"},
		{`{"results":[{"url":"https://pixhost.to/show/1/1_upbrr-connection-test.jpg","status":"uploaded"}]}`, "unexpected_response"},
		{`{"results":[]}`, "unexpected_response"},
	} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil || r.Method != http.MethodPost || r.URL.String() != "https://api.pixhost.to/images/status" || string(body) != `{"urls":["https://pixhost.to/show/1/1_upbrr-connection-test.jpg"]}` || r.Header.Get("Authorization") != "" {
				t.Error("probe sent an unauthorized operation, content or credentials")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.response))}, nil
		})}
		got := TestConnection(t.Context(), config.ImageHostingConfig{}, "pixhost", client)
		if got.Code != tc.want {
			t.Fatalf("status result=%#v", got)
		}
	}
}

func TestUploadOnlyHostLimitsSendNoRequests(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe remote request"); return nil, nil })}
	for _, host := range []string{"imgbb", "lensdump", "ptscreens", "onlyimage", "passtheimage", "utppm", "sharex"} {
		got := TestConnection(t.Context(), config.ImageHostingConfig{}, host, client)
		if got.Code != "unsupported" || !strings.Contains(got.Message, "upload") {
			t.Fatalf("%s limitation=%#v", host, got)
		}
		if host == "passtheimage" || host == "utppm" {
			if !strings.Contains(got.Message, "Chevereto /api/1/upload") || strings.Contains(got.Message, "v1.1") {
				t.Fatalf("%s limitation names the wrong upload contract", host)
			}
		}
	}
}

type countedSessionBody struct {
	source    *strings.Reader
	readBytes int
	closed    bool
}

func (body *countedSessionBody) Read(buffer []byte) (int, error) {
	n, err := body.source.Read(buffer)
	body.readBytes += n
	if err != nil {
		return n, fmt.Errorf("counted session body: %w", err)
	}
	return n, nil
}

func (body *countedSessionBody) Close() error {
	body.closed = true
	return nil
}

func TestImgboxProbeBoundsAllResponseReads(t *testing.T) {
	t.Parallel()
	body := &countedSessionBody{
		source: strings.NewReader(`<input name="authenticity_token" value="synthetic-token">` + strings.Repeat(" ", 2*int(maxResponseBodyPreviewBytes))),
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/html"}, "Set-Cookie": {"_imgbox_session=synthetic-cookie"}},
			Body:       body,
		}, nil
	})}
	got := TestConnection(t.Context(), config.ImageHostingConfig{}, "imgbox", client)
	if got.Code != "success" || !body.closed || int64(body.readBytes) > maxResponseBodyPreviewBytes {
		t.Fatalf("probe=%s closed=%v bytes=%d", got.Code, body.closed, body.readBytes)
	}
}

func TestImgboxProbeStopsBeforeTokenOrGalleryCreation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		body string
		want string
	}{
		{`<input name="authenticity_token" value="synthetic-session-token">`, "success"},
		{`<html>generic availability page</html>`, "unavailable"},
		{`<title>Checking your browser</title>`, "unavailable"},
	} {
		requests := 0
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			if req.Method != http.MethodGet || req.URL.String() != "https://imgbox.com/" || req.Body != nil || req.Header.Get("Authorization") != "" {
				t.Error("probe attempted more than the public session read")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/html"}, "Set-Cookie": {"_imgbox_session=synthetic-cookie; Secure; HttpOnly"}},
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}, nil
		})}
		got := TestConnection(t.Context(), config.ImageHostingConfig{}, "imgbox", client)
		if got.Code != tc.want || requests != 1 {
			t.Fatalf("anonymous probe=%#v requests=%d", got, requests)
		}
		if strings.Contains(got.Message, "synthetic-") {
			t.Fatal("remote secret leaked")
		}
	}
}

// blockedSessionBody supplies a valid session prefix, then remains blocked until
// request cancellation, like an HTTP response whose remaining stream stalls.
type blockedSessionBody struct {
	ctx     context.Context
	prefix  *strings.Reader
	blocked chan struct{}
	once    sync.Once
}

func (body *blockedSessionBody) Read(buffer []byte) (int, error) {
	if body.prefix.Len() > 0 {
		n, err := body.prefix.Read(buffer)
		if err != nil {
			return n, fmt.Errorf("read session prefix: %w", err)
		}
		return n, nil
	}
	body.once.Do(func() { close(body.blocked) })
	<-body.ctx.Done()
	return 0, fmt.Errorf("session response canceled: %w", body.ctx.Err())
}
func (*blockedSessionBody) Close() error { return nil }

func TestImgboxProbeCancellationWinsAfterValidSessionPrefix(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	const token = `<input name="authenticity_token" value="synthetic-token">`
	body := &blockedSessionBody{
		ctx:     ctx,
		prefix:  strings.NewReader(token + strings.Repeat(" ", 1024-len(token))),
		blocked: make(chan struct{}),
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/html"}, "Set-Cookie": {"_imgbox_session=synthetic-cookie"}},
			Body:       body,
		}, nil
	})}
	completed := make(chan api.ConnectionTestResult, 1)
	go func() { completed <- TestConnection(ctx, config.ImageHostingConfig{}, "imgbox", client) }()
	select {
	case <-body.blocked:
		cancel()
		if got := <-completed; got.Code != "canceled" {
			t.Fatalf("late session result=%#v", got)
		}
	case got := <-completed:
		// A reader that stops without draining can finish before cancellation.
		if got.Code != "success" {
			t.Fatalf("completed session result=%#v", got)
		}
	}
}

func TestSamaritanoProbeUsesAuthenticatedReadWithoutUploading(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"status":true,"data":{"items":[],"page":1,"size":1,"total":0}}`,
		`{"status":false,"message":"synthetic-private-error","data":{"items":[],"page":1,"size":1,"total":0}}`,
		`{"status":true,"data":{"items":[],"page":1,"size":20,"total":0}}`,
		`<html>Login required</html>`,
	} {
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.Method != http.MethodGet || req.URL.Host != "img.samaritano.cc" || req.URL.Path != "/api/v1/images" || req.Body != nil || req.URL.Query().Get("page_size") != "1" || req.URL.Query().Get("keyword") != "upbrr.connection.test.synthetic" || req.Header.Get("Authorization") != "Bearer synthetic-token" {
				t.Error("wrong credential or operation")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		got := TestConnection(t.Context(), config.ImageHostingConfig{SamaritanoAPI: "synthetic-token"}, "samaritano", client)
		if (got.Code == "success") != strings.HasPrefix(body, `{"status":true,"data":{"items":[],"page":1,"size":1,`) || calls != 1 || strings.Contains(got.Message, "synthetic-") {
			t.Fatalf("unexpected result: %#v", got)
		}
	}
}
