// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bt

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestProcessBTGroupPageKeepsFolderBasedPackName(t *testing.T) {
	t.Parallel()

	root, err := html.Parse(strings.NewReader(`
<table><tr id="torrent42"><td><a onclick="gtoggle()">Season Pack</a></td></tr></table>
<div id="files_42">
  <div class="filelist_path">/Example.Show.S01.1080p-GRP/</div>
  <table class="filelist_table"><tr><td>Example.Show.S01E01.1080p-GRP.mkv</td></tr></table>
</div>`))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	var found []string
	processBTGroupPage(root, &found)
	if len(found) != 1 || found[0] != "Example.Show.S01.1080p-GRP" {
		t.Fatalf("found items = %#v", found)
	}
}
