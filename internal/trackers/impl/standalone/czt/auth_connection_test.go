// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package czt

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

type connectionTransport func(*http.Request) (*http.Response, error)

func (fn connectionTransport) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestConnectionValidatesProtectedSearchWithoutFetchingContent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ body, want string }{
		{`<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><newznab:response offset="0" total="0"/></channel></rss>`, "success"},
		{`<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><torznab:response offset="0" total="0"/></channel></rss>`, "success"},
		{`<error code="100" description="synthetic-private-error"/>`, "credentials_rejected"},
		{`<error code="102" description="synthetic-private-error"/>`, "access_denied"},
		{`<rss><channel><response offset="0" total="0"/></channel></rss>`, "unexpected_response"},
		{`<html>generic availability page</html>`, "unexpected_response"},
	} {
		requests := 0
		client := &http.Client{Transport: connectionTransport(func(req *http.Request) (*http.Response, error) {
			requests++
			query := req.URL.Query()
			if req.Method != http.MethodGet || req.URL.Host != "czteam.me" || req.URL.Path != "/torznab" || query.Get("t") != "search" || query.Get("apikey") != "0123456789abcdef" || query.Get("q") != "upbrr.connection.test.synthetic" || query.Get("limit") != "1" || req.Body != nil {
				t.Error("probe used an unauthorized operation or wrong draft credentials")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}
		got := New().ConnectionProbe().Test(t.Context(), trackers.ConnectionProbeInput{Config: config.TrackerConfig{Passkey: "0123456789abcdef"}}, client)
		if got.Code != tc.want || requests != 1 {
			t.Fatalf("probe=%#v requests=%d", got, requests)
		}
		if strings.Contains(got.Message, "synthetic-") {
			t.Fatal("remote response leaked")
		}
	}
}
