// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type connectionTransport func(*http.Request) (*http.Response, error)

func (fn connectionTransport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestConnectionUsesPublicLookupContractAndRejectsAvailabilityPages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ body, want string }{
		{evidenceSearchHTML, "success"},
		{`<html>Blu-ray.com available</html>`, "unexpected_response"},
		{`No index`, "unexpected_response"},
		{`<title>Service Unavailable</title>` + evidenceSearchHTML, "unavailable"},
		{`<h1>Access denied</h1>` + evidenceSearchHTML, "access_denied"},
		{`<div class="alert alert-danger">synthetic-private-error</div>` + evidenceSearchHTML, "remote_error"},
		{strings.ReplaceAll(evidenceSearchHTML, "Example Movie", "Error Movie"), "success"},
	} {
		client := &http.Client{Transport: connectionTransport(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet || req.URL.Host != "www.blu-ray.com" || req.URL.Path != "/search/" || req.URL.Query().Get("quicksearch_keyword") != "tt0133093" || req.Header.Get("Authorization") != "" || req.Body != nil {
				t.Error("probe did not use the public lookup contract")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}
		got := TestConnection(t.Context(), client)
		if got.Code != tc.want {
			t.Fatalf("lookup response result=%#v", got)
		}
	}
}
