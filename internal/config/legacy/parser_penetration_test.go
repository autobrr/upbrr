// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package legacy

import (
	"strings"
	"testing"
)

// The Python dict parser is hand-rolled. These tests attack every recursive
// path: unterminated collections, empty input, bad literals, escape edge
// cases. Each failure mode must emit a clear error rather than panic or loop.

func TestParseLegacyConfigEmptyInput(t *testing.T) {
	t.Parallel()

	if _, err := ParseLegacyConfig(nil); err == nil {
		t.Fatalf("expected error on empty input")
	}
	if _, err := ParseLegacyConfig([]byte("")); err == nil {
		t.Fatalf("expected error on empty string")
	}
}

func TestParseLegacyConfigMissingAssignment(t *testing.T) {
	t.Parallel()

	inputs := []string{
		`data = {"DEFAULT": {}}`,
		`# only comments`,
		``,
		`configuration = {}`,
	}
	for _, in := range inputs {
		if _, err := ParseLegacyConfig([]byte(in)); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}
}

// Unterminated dicts/lists/tuples/strings must return an error, not infinite
// loop or panic.
func TestParserUnterminatedCollections(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"unterminated dict":   `{"a": 1`,
		"unterminated list":   `["a", "b"`,
		"unterminated tuple":  `("a", "b"`,
		"unterminated string": `{"a": "never closed`,
		"unterminated triple": `{"a": """never closed`,
		"missing value":       `{"a":`,
		"missing colon":       `{"a" 1}`,
		"missing comma":       `{"a": 1 "b": 2}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			p := newParser(src)
			if _, err := p.parseValue(); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

// parseString must handle escape sequences the converter uses and leave
// unknown escapes literal (escape char + following char) so data round-trips.
func TestParserStringEscapes(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		`"a\nb"`:        "a\nb",
		`"a\rb"`:        "a\rb",
		`"a\tb"`:        "a\tb",
		`"a\\b"`:        "a\\b",
		`"a\"b"`:        "a\"b",
		`'a\'b'`:        "a'b",
		`"unknown\qxx"`: "unknown\\qxx",
	}
	for src, want := range cases {
		p := newParser(src)
		got, err := p.parseString()
		if err != nil {
			t.Errorf("%s: error %v", src, err)
			continue
		}
		if got != want {
			t.Errorf("%s: got %q want %q", src, got, want)
		}
	}
}

// Triple-quoted strings must preserve internal newlines verbatim.
func TestParserTripleQuotedString(t *testing.T) {
	t.Parallel()

	src := `"""line1
line2
line3"""`
	p := newParser(src)
	got, err := p.parseString()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != "line1\nline2\nline3" {
		t.Fatalf("got %q", got)
	}
}

// Unicode in strings must pass through untouched (UTF-8 bytes).
func TestParserUnicodeStrings(t *testing.T) {
	t.Parallel()

	src := `{"title": "日本語 — ÿµ€"}`
	p := newParser(src)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := requireMap(t, val)
	if m["title"] != "日本語 — ÿµ€" {
		t.Fatalf("got %q", m["title"])
	}
}

// Integer and float scalar parsing.
func TestParserScalarNumericBoundaries(t *testing.T) {
	t.Parallel()

	cases := map[string]any{
		`0`:          0,
		`-1`:         -1,
		`2147483647`: 2147483647, // max int32
		`0.0`:        0.0,
		`-3.14`:      -3.14,
		`1e10`:       1e10,
		`.5`:         0.5,
	}
	for src, want := range cases {
		p := newParser(src)
		got, err := p.parseValue()
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		// Integer comparison: the parser returns int for whole-number literals.
		if wantInt, ok := want.(int); ok {
			if gotInt, ok := got.(int); !ok || gotInt != wantInt {
				t.Errorf("%s: got %v (%T) want %v", src, got, got, want)
			}
			continue
		}
		if got != want {
			t.Errorf("%s: got %v (%T) want %v", src, got, got, want)
		}
	}
}

// Tokens that look like identifiers become bare strings — legacy configs use
// this for things like `torrent_client: qbit`.
func TestParserBareIdentifier(t *testing.T) {
	t.Parallel()

	p := newParser(`qbittorrent`)
	got, err := p.parseValue()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != "qbittorrent" {
		t.Fatalf("got %v", got)
	}
}

// Deeply nested dicts must parse without blowing the recursion budget for
// realistic depths (10 levels is plenty for legacy configs).
func TestParserDeepNesting(t *testing.T) {
	t.Parallel()

	built := strings.Repeat(`{"k":`, 10) + `"leaf"` + strings.Repeat(`}`, 10)

	p := newParser(built)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cur := val
	for i := range 10 {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("level %d: not a map", i)
		}
		cur = m["k"]
	}
	if cur != "leaf" {
		t.Fatalf("leaf: got %v", cur)
	}
}

// A CRLF-line-ended config.py file (common on Windows) must parse identically
// to LF.
func TestParseLegacyConfigCRLF(t *testing.T) {
	t.Parallel()

	src := "config = {\r\n    'DEFAULT': {\r\n        'tmdb_api': 'k',\r\n    },\r\n    'TRACKERS': {},\r\n    'TORRENT_CLIENTS': {},\r\n}\r\n"
	legacy, err := ParseLegacyConfig([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if legacy.Default["tmdb_api"] != "k" {
		t.Fatalf("tmdb_api: got %v", legacy.Default["tmdb_api"])
	}
}

// Comments may appear between key and value, at end of line, and as whole
// lines — every placement must be tolerated.
func TestParseLegacyConfigCommentPlacements(t *testing.T) {
	t.Parallel()

	src := `
# leading comment
config = {
    # inside-dict comment
    'DEFAULT': { # inline
        'tmdb_api': 'k', # trailing
        # full-line comment
        'screens': 6,
    },
    'TRACKERS': {},
    'TORRENT_CLIENTS': {},
}
# trailing comment
`
	legacy, err := ParseLegacyConfig([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if legacy.Default["tmdb_api"] != "k" {
		t.Fatalf("tmdb_api: got %v", legacy.Default["tmdb_api"])
	}
	if legacy.Default["screens"] != 6 {
		t.Fatalf("screens: got %v", legacy.Default["screens"])
	}
}

// A config whose top-level value is not a dict must return a clear error.
func TestParseLegacyConfigNonDictTop(t *testing.T) {
	t.Parallel()

	src := `config = [1, 2, 3]`
	if _, err := ParseLegacyConfig([]byte(src)); err == nil {
		t.Fatalf("expected error for list top-level")
	}
}

// The assignment must not latch on to identifiers that merely contain
// "config" as a substring — `config_path`, `my_config`, `configure`.
func TestExtractConfigDictSubstringSafety(t *testing.T) {
	t.Parallel()

	inputs := []string{
		`config_path = "/etc/upbrr.py"`,
		`my_config = {}`,
		`def configure():`,
	}
	for _, in := range inputs {
		if _, err := extractConfigDict(in); err == nil {
			t.Errorf("expected error for %q", in)
		}
	}

	// `config` followed by a comment before `=` must still resolve (this is
	// existing behavior — see TestExtractConfigDict/"comment between...").
	src := "config # comment\n = {}"
	if _, err := extractConfigDict(src); err != nil {
		t.Fatalf("comment between config and '=' should still parse: %v", err)
	}
}

// The parser must handle a dict whose keys are integer literals without
// panicking (it stringifies them via fmt.Sprintf).
func TestParserIntegerKeys(t *testing.T) {
	t.Parallel()

	src := `{1: "a", 2: "b"}`
	p := newParser(src)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := requireMap(t, val)
	if m["1"] != "a" || m["2"] != "b" {
		t.Fatalf("got %v", m)
	}
}

// Tokens that cannot be parsed as int or float fall back to literal strings.
func TestParserMalformedScalarBecomesString(t *testing.T) {
	t.Parallel()

	p := newParser(`0xABC`) // hex not supported as int
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if val != "0xABC" {
		t.Fatalf("got %v", val)
	}
}
