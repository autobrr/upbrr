// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package description

import (
	"slices"
	"strings"

	xhtml "golang.org/x/net/html"
)

// RenderSample renders tracker markup but removes navigable links and all
// image sources except the caller's in-memory sample images. Draft BBCode and
// tracker-owned defaults must never initiate browser requests.
func RenderSample(raw string, images map[string]string) string {
	nodes, err := xhtml.ParseFragment(strings.NewReader(Render(raw)), fragmentContext())
	if err != nil {
		return ""
	}
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		node.Attr = slices.DeleteFunc(node.Attr, func(attr xhtml.Attribute) bool {
			return attr.Key == "href"
		})
		for i := range node.Attr {
			if node.Attr[i].Key == "src" {
				node.Attr[i].Val = images[node.Attr[i].Val]
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	var output strings.Builder
	for _, node := range nodes {
		visit(node)
		sanitizeNode(&output, node)
	}
	return output.String()
}
