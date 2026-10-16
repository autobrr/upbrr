// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package importer

import "testing"

func TestImportPasswordlessQbit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		file    string
		content string
		user    string
	}{
		{"yaml omitted", "config.yaml", "torrent_clients:\n  local:\n    url: http://localhost:8080\n", ""},
		{"yaml null", "config.yaml", "torrent_clients:\n  local:\n    qbit_url: http://localhost:8080\n    qbit_user: user\n    qbit_pass: null\n", "user"},
		{"yaml empty", "config.yaml", "torrent_clients:\n  local:\n    url: http://localhost:8080\n    username: user\n    password: ''\n", "user"},
		{"json omitted", "config.json", `{"TorrentClients":{"local":{"URL":"http://localhost:8080"}}}`, ""},
		{"json null", "config.json", `{"TorrentClients":{"local":{"QbitURL":"http://localhost:8080","QbitUser":"user","QbitPass":null}}}`, "user"},
		{"json empty", "config.json", `{"TorrentClients":{"local":{"URL":"http://localhost:8080","Username":"user","Password":""}}}`, "user"},
		{"python omitted", "config.py", `config = {'TORRENT_CLIENTS': {'local': {'torrent_client': 'qbit', 'qbit_url': 'http://localhost:8080'}}}`, ""},
		{"python none", "config.py", `config = {'TORRENT_CLIENTS': {'local': {'torrent_client': 'qbit', 'qbit_url': 'http://localhost:8080', 'qbit_user': 'user', 'qbit_pass': None}}}`, "user"},
		{"python empty", "config.py", `config = {'TORRENT_CLIENTS': {'local': {'torrent_client': 'qbit', 'qbit_url': 'http://localhost:8080', 'qbit_user': 'user', 'qbit_pass': ''}}}`, "user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, warnings, err := ImportFromContent(tt.file, []byte(tt.content))
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if len(warnings) != 0 {
				t.Fatalf("unexpected import warnings: %v", warnings)
			}
			client, ok := cfg.TorrentClients["local"]
			if !ok || client.QbitHost() != "http://localhost:8080" || client.QbitUsername() != tt.user || client.QbitPassword() != "" {
				t.Fatal("passwordless client was not preserved")
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("validate imported config: %v", err)
			}
		})
	}
}
