// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bjs

import (
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestExtractBJSResultsNewTVLayout(t *testing.T) {
	t.Parallel()

	root := mustParseHTML(t, `
<div class="main_column">
  <table>
    <tr class="resolution_header"><td>1080p</td></tr>
    <tr class="season_header"><td>Temporada 1</td></tr>
    <tr><td rowspan="2"><a href="torrents.php?id=77">Example S01E02</a></td></tr>
    <tr id="torrent123"><td><a href="torrents.php?torrentid=123">Example.S01E02.1080p-GRP</a></td><td class="number_column nobr">1.5 GiB</td></tr>
    <tr><td rowspan="2"><a href="torrents.php?id=78">Example S01E03</a></td></tr>
    <tr id="torrent124"><td><a href="torrents.php?torrentid=124">Example.S01E03.1080p-GRP</a></td><td class="number_column nobr">1.7 GiB</td></tr>
  </table>
</div>`)

	entries := extractBJSResults("https://bj-share.info", root, api.DuplicateSubject{
		Identity:   api.ExternalIdentity{Category: "TV"},
		SeasonInt:  1,
		EpisodeInt: 2,
		Release:    api.ReleaseInfo{Resolution: "1080p"},
	})

	if len(entries) != 1 {
		t.Fatalf("expected one matching episode entry, got %#v", entries)
	}
	if entries[0].ID != "123" || entries[0].Name != "Example.S01E02.1080p-GRP" {
		t.Fatalf("unexpected entry: %#v", entries[0])
	}
	if !entries[0].SizeKnown || entries[0].SizeBytes == 0 {
		t.Fatalf("expected parsed size, got %#v", entries[0])
	}
}

func TestExtractBJSResultsLegacyLoadIfNeededLayout(t *testing.T) {
	t.Parallel()

	root := mustParseHTML(t, `
<div class="main_column">
  <table>
    <tr id="torrent555">
      <td><a onclick="loadIfNeeded('555', '42')">Movie.2026.2160p-GRP</a></td>
    </tr>
  </table>
</div>`)

	entries := extractBJSResults("https://bj-share.info", root, api.DuplicateSubject{})
	if len(entries) != 1 {
		t.Fatalf("expected one legacy entry, got %#v", entries)
	}
	if entries[0].ID != "555" || entries[0].Link != "https://bj-share.info/torrents.php?torrentid=555" {
		t.Fatalf("unexpected legacy entry: %#v", entries[0])
	}
}

func mustParseHTML(t *testing.T, value string) *xhtml.Node {
	t.Helper()

	root, err := xhtml.Parse(strings.NewReader(value))
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	return root
}
