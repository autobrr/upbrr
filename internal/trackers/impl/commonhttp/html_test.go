// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package commonhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func TestGetHTMLReturnsVisibleNonSuccessDetail(t *testing.T) {
	t.Parallel()

	body := `<html><head>` + strings.Repeat(`<link href="/favicon">`, 4000) +
		`<script>{"message":"private-script"}</script></head><body>` +
		`<div role="alert">Session expired: token&#61;synthetic-secret</div></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	status, root, err := GetHTML(t.Context(), server.Client(), server.URL, nil, nil)
	if status != http.StatusForbidden || root != nil {
		t.Fatalf("status=%d root=%v", status, root)
	}
	want := "commonhttp: HTML GET request failed status=403: Session expired: token=[REDACTED]"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestGetHTMLPreservesSuccessfulParsing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><div id="result">ok</div></body></html>`))
	}))
	defer server.Close()

	status, root, err := GetHTML(t.Context(), server.Client(), server.URL, nil, nil)
	if err != nil {
		t.Fatalf("GetHTML: %v", err)
	}
	if status != http.StatusOK || root == nil {
		t.Fatalf("status=%d root=%v", status, root)
	}
	result := FirstNode(root, func(node *xhtml.Node) bool { return Attr(node, "id") == "result" })
	if result == nil || strings.TrimSpace(NodeText(result)) != "ok" {
		t.Fatalf("result node = %v", result)
	}
}
