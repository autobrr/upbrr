// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package commonhttp

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
)

var sizePattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*(KB|MB|GB|TB|KiB|MiB|GiB|TiB|B)\b`)

// GetText performs a tracker GET request and reads the full response body for
// every HTTP status. Non-success status codes are returned as data, not errors.
func GetText(ctx context.Context, client *http.Client, endpoint string, params url.Values, cookies []*http.Cookie) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, "", fmt.Errorf("commonhttp: create text GET request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	req.Header.Set("User-Agent", "upbrr")
	ApplyCookies(req, cookies)
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("commonhttp: text GET request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("commonhttp: read text response: %w", err)
	}
	return resp.StatusCode, string(body), nil
}

// GetHTML performs a tracker GET request and parses successful responses.
// Non-2xx responses return their status with a nil document and nil error.
func GetHTML(ctx context.Context, client *http.Client, endpoint string, params url.Values, cookies []*http.Cookie) (int, *xhtml.Node, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("commonhttp: create HTML GET request: %w", err)
	}
	if len(params) > 0 {
		req.URL.RawQuery = params.Encode()
	}
	req.Header.Set("User-Agent", "upbrr")
	ApplyCookies(req, cookies)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("commonhttp: HTML GET request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, nil, nil
	}
	root, err := xhtml.Parse(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("commonhttp: parse HTML response: %w", err)
	}
	return resp.StatusCode, root, nil
}

// FindNodes returns matching nodes in document order.
func FindNodes(root *xhtml.Node, match func(*xhtml.Node) bool) []*xhtml.Node {
	nodes := make([]*xhtml.Node, 0)
	WalkNodes(root, func(node *xhtml.Node) {
		if match(node) {
			nodes = append(nodes, node)
		}
	})
	return nodes
}

// FirstNode returns the first matching node in document order.
func FirstNode(root *xhtml.Node, match func(*xhtml.Node) bool) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node == nil || found != nil {
			return
		}
		if match(node) {
			found = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return found
}

// WalkNodes visits an HTML tree in document order.
func WalkNodes(root *xhtml.Node, visit func(*xhtml.Node)) {
	if root == nil {
		return
	}
	visit(root)
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		WalkNodes(child, visit)
	}
}

// Attr returns a trimmed HTML attribute value.
func Attr(node *xhtml.Node, key string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

// HasClass reports whether an HTML node has a class token.
func HasClass(node *xhtml.Node, className string) bool {
	for value := range strings.FieldsSeq(Attr(node, "class")) {
		if strings.EqualFold(value, strings.TrimSpace(className)) {
			return true
		}
	}
	return false
}

// NodeText returns concatenated descendant text.
func NodeText(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		builder.WriteString(NodeText(child))
	}
	return builder.String()
}

// AbsoluteURL resolves a possibly relative tracker link.
func AbsoluteURL(baseURL, value string) string {
	base, baseErr := url.Parse(strings.TrimSpace(baseURL))
	ref, refErr := url.Parse(strings.TrimSpace(value))
	if baseErr == nil && refErr == nil {
		return base.ResolveReference(ref).String()
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(value, "/")
}

// ParseSizeBytes parses decimal and binary byte units.
func ParseSizeBytes(value string) (int64, bool) {
	match := sizePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 3 {
		return 0, false
	}
	amount, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	multipliers := map[string]float64{
		"b":   1,
		"kb":  1e3,
		"mb":  1e6,
		"gb":  1e9,
		"tb":  1e12,
		"kib": 1024,
		"mib": 1024 * 1024,
		"gib": 1024 * 1024 * 1024,
		"tib": 1024 * 1024 * 1024 * 1024,
	}
	return int64(math.Round(amount * multipliers[strings.ToLower(match[2])])), true
}
