// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package legacy

import (
	"testing"
)

func TestExtractConfigDict(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "simple assignment",
			input: `config = {"DEFAULT": {}}`,
		},
		{
			name:  "with leading comments",
			input: "# comment\nconfig = {\"DEFAULT\": {}}",
		},
		{
			name:    "no config assignment",
			input:   `data = {"DEFAULT": {}}`,
			wantErr: true,
		},
		{
			name:  "config not part of larger identifier",
			input: "my_config = 1\nconfig = {\"DEFAULT\": {}}",
		},
		{
			name:  "comment between config and equals",
			input: "config # the legacy map\n = {\"DEFAULT\": {}}",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := extractConfigDict(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error but got none")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseDict(t *testing.T) {
	input := `{"key1": "value1", "key2": 42}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", val)
	}
	if m["key1"] != "value1" {
		t.Errorf("key1: got %v, want value1", m["key1"])
	}
	if m["key2"] != 42 {
		t.Errorf("key2: got %v, want 42", m["key2"])
	}
}

func TestParseBoolsAndNone(t *testing.T) {
	input := `{"a": True, "b": False, "c": None}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	if m["a"] != true {
		t.Errorf("a: got %v, want true", m["a"])
	}
	if m["b"] != false {
		t.Errorf("b: got %v, want false", m["b"])
	}
	if m["c"] != nil {
		t.Errorf("c: got %v, want nil", m["c"])
	}
}

func TestParseNestedDict(t *testing.T) {
	input := `{"outer": {"inner": "deep"}}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	inner := m["outer"].(map[string]any)
	if inner["inner"] != "deep" {
		t.Errorf("inner: got %v, want deep", inner["inner"])
	}
}

func TestParseList(t *testing.T) {
	input := `["a", "b", 3]`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	list := val.([]any)
	if len(list) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(list))
	}
	if list[0] != "a" {
		t.Errorf("element 0: got %v, want a", list[0])
	}
	if list[2] != 3 {
		t.Errorf("element 2: got %v, want 3", list[2])
	}
}

func TestParseTrailingCommas(t *testing.T) {
	input := `{"a": 1, "b": 2,}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	if m["a"] != 1 || m["b"] != 2 {
		t.Errorf("unexpected values: %v", m)
	}
}

func TestParseComments(t *testing.T) {
	input := `{
		# This is a comment
		"key": "value" # inline comment
	}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	if m["key"] != "value" {
		t.Errorf("key: got %v, want value", m["key"])
	}
}

func TestParseSingleQuotedStrings(t *testing.T) {
	input := `{'key': 'value'}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	if m["key"] != "value" {
		t.Errorf("key: got %v, want value", m["key"])
	}
}

func TestParseEscapes(t *testing.T) {
	input := `{"key": "line1\nline2"}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	if m["key"] != "line1\nline2" {
		t.Errorf("key: got %q, want %q", m["key"], "line1\nline2")
	}
}

func TestParseFloats(t *testing.T) {
	input := `{"score": 3.14, "neg": -1}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	if m["score"] != 3.14 {
		t.Errorf("score: got %v, want 3.14", m["score"])
	}
	if m["neg"] != -1 {
		t.Errorf("neg: got %v, want -1", m["neg"])
	}
}

func TestParseTuple(t *testing.T) {
	input := `{"t": (1, 2, 3)}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	list := m["t"].([]any)
	if len(list) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(list))
	}
}

func TestParseLegacyConfig(t *testing.T) {
	input := []byte(`
# Legacy config
config = {
    'DEFAULT': {
        'tmdb_api': 'my-api-key',
        'screens': 6,
        'img_host_1': 'ptpimg',
        'tone_map': True,
    },
    'TRACKERS': {
        'default_trackers': 'BHD, PTP',
        'BHD': {
            'api_key': 'bhd-secret-key',
            'bhd_rss_key': 'bhd-rss',
        },
    },
    'TORRENT_CLIENTS': {
        'qbittorrent': {
            'torrent_client': 'qbit',
            'qbit_url': 'http://localhost:8080',
            'qbit_user': 'admin',
            'qbit_pass': 'secret',
        },
    },
}
`)
	legacy, err := ParseLegacyConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if legacy.Default["tmdb_api"] != "my-api-key" {
		t.Errorf("tmdb_api: got %v", legacy.Default["tmdb_api"])
	}
	if legacy.Default["screens"] != 6 {
		t.Errorf("screens: got %v", legacy.Default["screens"])
	}
	if legacy.Default["tone_map"] != true {
		t.Errorf("tone_map: got %v", legacy.Default["tone_map"])
	}

	trackers := legacy.Trackers
	if trackers["default_trackers"] != "BHD, PTP" {
		t.Errorf("default_trackers: got %v", trackers["default_trackers"])
	}
	bhd := trackers["BHD"].(map[string]any)
	if bhd["api_key"] != "bhd-secret-key" {
		t.Errorf("BHD.api_key: got %v", bhd["api_key"])
	}

	clients := legacy.TorrentClients
	qbit := clients["qbittorrent"].(map[string]any)
	if qbit["qbit_url"] != "http://localhost:8080" {
		t.Errorf("qbit_url: got %v", qbit["qbit_url"])
	}
}

func TestParseEmptyDict(t *testing.T) {
	input := `{}`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := val.(map[string]any)
	if len(m) != 0 {
		t.Errorf("expected empty dict, got %v", m)
	}
}

func TestParseEmptyList(t *testing.T) {
	input := `[]`
	p := newParser(input)
	val, err := p.parseValue()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	list := val.([]any)
	if len(list) != 0 {
		t.Errorf("expected empty list, got %v", list)
	}
}
