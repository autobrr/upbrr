// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package commonhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type uploadRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip uploadRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return roundTrip(req)
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
