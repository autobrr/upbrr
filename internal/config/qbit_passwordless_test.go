// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import "testing"

func TestValidateQbitPasswordless(t *testing.T) {
	t.Parallel()

	for name, client := range map[string]TorrentClientConfig{
		"url only":       {URL: "http://localhost:8080"},
		"empty password": {URL: "http://localhost:8080", Username: "user"},
		"qbit aliases":   {QbitURL: "http://localhost:8080", QbitUser: "user"},
		"blank credentials": {
			QbitURL:  "http://localhost:8080",
			QbitUser: " ",
			QbitPass: " ",
		},
		"qbittorrent type": {Type: "qbittorrent", URL: "http://localhost:8080"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := withBase(func(c *Config) {
				c.TorrentClients = map[string]TorrentClientConfig{"local": client}
			})
			if err := cfg.Validate(); err != nil {
				t.Fatalf("passwordless qBittorrent config should validate: %v", err)
			}
		})
	}
}
