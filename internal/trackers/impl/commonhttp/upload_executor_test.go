// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package commonhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type uploadRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip uploadRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return roundTrip(req)
}

func TestExecuteUploadRetainsProtocolEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		status   int
		body     io.ReadCloser
		complete bool
	}{
		{"structured rejection", 400, io.NopCloser(strings.NewReader(`{"success":false,"message":"api_key=synthetic-secret"}`)), true},
		{"accepted redirect with incomplete body", 302, &partialErrorReadCloser{payload: []byte("partial"), err: io.ErrUnexpectedEOF}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: uploadRoundTripper(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: test.status,
					Header:     http.Header{"Location": []string{"/details.php?id=123"}},
					Body:       test.body,
					Request:    req,
				}, nil
			})}
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://tracker.invalid/upload", nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := ExecuteUpload(client, req, UploadExecutionOptions{Tracker: "EXAMPLE", SuccessStatus: func(status int) bool { return status == 302 }})
			if result.StatusCode != test.status || result.Header.Get("Location") != "/details.php?id=123" || result.BodyComplete != test.complete {
				t.Fatalf("status=%d location=%q complete=%v error=%v", result.StatusCode, result.Header.Get("Location"), result.BodyComplete, err)
			}
			if test.complete {
				if err != nil || !strings.Contains(string(result.ProtocolBody), `"success":false`) || strings.Contains(string(result.Body), "synthetic-secret") || strings.Contains(string(result.Preview), "synthetic-secret") {
					t.Fatalf("protocol/diagnostic separation failed: error=%v", err)
				}
			} else if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("read error=%v", err)
			}
		})
	}
}

func TestExecuteUploadBoundsAndRedactsFailure(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: uploadRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`api_key=secret-value ` + strings.Repeat("x", 128))),
			Request:    req,
		}, nil
	})}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://tracker.invalid/upload", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	result, err := ExecuteUpload(client, req, UploadExecutionOptions{Tracker: "DC", PreviewLimit: 32})
	if err != nil {
		t.Fatalf("execute upload: %v", err)
	}
	if result.Success || len(result.Body) > 32 || strings.Contains(string(result.Preview), "secret-value") {
		t.Fatalf("unsafe failure projection: %#v", result)
	}
}

func TestExecuteUploadRejectsOversizedSuccessUnlessFullBodyIsExplicit(t *testing.T) {
	t.Parallel()

	newClient := func() *http.Client {
		return &http.Client{Transport: uploadRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("artifact")),
				Request:    req,
			}, nil
		})}
	}
	newRequest := func() *http.Request {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://tracker.invalid/upload", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		return req
	}
	if _, err := ExecuteUpload(newClient(), newRequest(), UploadExecutionOptions{Tracker: "CZT", SuccessBodyLimit: 4}); err == nil {
		t.Fatal("expected bounded success body failure")
	}
	result, err := ExecuteUpload(newClient(), newRequest(), UploadExecutionOptions{Tracker: "CZT", SuccessBody: FullSuccessBody})
	if err != nil {
		t.Fatalf("execute full-body upload: %v", err)
	}
	if string(result.Body) != "artifact" {
		t.Fatalf("success body = %q", result.Body)
	}
}

func TestExecuteUploadFindsLateHTMLFailureForPublicError(t *testing.T) {
	t.Parallel()

	responseBody := `<html><head>` + strings.Repeat(`<link href="/favicon">`, 4000) +
		`</head><body><div class="alert-danger">Upload permission denied.</div></body></html>`
	client := &http.Client{Transport: uploadRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Request:    req,
		}, nil
	})}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://tracker.invalid/upload", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	result, err := ExecuteUpload(client, req, UploadExecutionOptions{Tracker: "DC"})
	if err != nil {
		t.Fatalf("execute upload: %v", err)
	}
	if result.Success || string(result.Body) != "Upload permission denied." || string(result.Preview) != "Upload permission denied." {
		t.Fatalf("unexpected failure projection: %#v", result)
	}

	publicErr := UploadHTTPError("DC", result.StatusCode, result.Preview)
	if publicErr.Error() != "trackers: DC upload failed status=403: Upload permission denied." {
		t.Fatalf("public error = %v", publicErr)
	}
}

func TestExecuteUploadPreservesFailureReadErrorIdentityAndDetail(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("api_key=synthetic-secret")
	client := &http.Client{Transport: uploadRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       &partialErrorReadCloser{payload: []byte(`<div class="error">Invalid source</div>`), err: sentinel},
			Request:    req,
		}, nil
	})}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://tracker.invalid/upload", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	_, err = ExecuteUpload(client, req, UploadExecutionOptions{Tracker: "DC"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected read error identity, got %v", err)
	}
	if !strings.Contains(err.Error(), "response body read failed; Invalid source") {
		t.Fatalf("expected partial response detail, got %v", err)
	}
	if strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("read error leaked secret: %v", err)
	}
}
