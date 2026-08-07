// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hds

import (
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func TestHDSHasNextPageIgnoresEarlierGenericLinks(t *testing.T) {
	t.Parallel()

	root, err := xhtml.Parse(strings.NewReader(`<a href="index.php?pages=0">1</a>`))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if hdsHasNextPage(root, 1) {
		t.Fatal("earlier page link reported as next page")
	}
}
