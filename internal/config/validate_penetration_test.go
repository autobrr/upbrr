// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"strings"
	"testing"

	imagehostpolicy "github.com/autobrr/upbrr/internal/imagehosting/policy"
)

// Validate is the CLI's fail-fast check. These tests target every failure
// branch so silent acceptance of a bad field configuration shows up at code
// review instead of in production.

func withBase(mut func(*Config)) Config {
	cfg := Config{
		MainSettings:       MainSettingsConfig{TMDBAPI: "k"},
		ScreenshotHandling: ScreenshotHandlingConfig{Screens: 1},
	}
	if mut != nil {
		mut(&cfg)
	}
	return cfg
}

func TestValidateAllowsMissingTMDBAPI(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) { c.MainSettings.TMDBAPI = "" })
	if err := cfg.Validate(); err != nil {
		t.Fatalf("missing optional tmdb_api: %v", err)
	}
}

func TestValidateScreensZeroOrNegative(t *testing.T) {
	t.Parallel()

	for _, screens := range []int{0, -1, -100} {
		cfg := withBase(func(c *Config) { c.ScreenshotHandling.Screens = screens })
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "screens") {
			t.Errorf("screens=%d: want error mentioning screens, got %v", screens, err)
		}
	}
}

func TestValidateMaxConcurrentTrackersNegative(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) { c.PostUpload.MaxConcurrentTrackers = -1 })
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "max_concurrent_tracker_uploads") {
		t.Fatalf("want concurrency error, got %v", err)
	}
}

// Zero must be allowed (zero means "auto" in the upload pipeline).
func TestValidateMaxConcurrentTrackersZero(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) { c.PostUpload.MaxConcurrentTrackers = 0 })
	if err := cfg.Validate(); err != nil {
		t.Fatalf("zero concurrency must be allowed, got: %v", err)
	}
}

func TestValidateLoggingFileEnabledRequiresSizing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		size     int
		maxFiles int
		wantMsg  string
	}{
		{"missing size", 0, 5, "max_total_size_mb"},
		{"negative size", -1, 5, "max_total_size_mb"},
		{"missing files", 100, 0, "max_files"},
		{"negative files", 100, -1, "max_files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := withBase(func(c *Config) {
				c.Logging.FileEnabled = true
				c.Logging.MaxTotalSizeMB = tc.size
				c.Logging.MaxFiles = tc.maxFiles
			})
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("want error mentioning %s, got %v", tc.wantMsg, err)
			}
		})
	}
}

// When FileEnabled is false, zero sizing is fine.
func TestValidateLoggingFileDisabledIgnoresSizing(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) {
		c.Logging.FileEnabled = false
		c.Logging.MaxTotalSizeMB = 0
		c.Logging.MaxFiles = 0
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("file logging disabled should not require sizing: %v", err)
	}
}

// Torrent clients default to qBittorrent; the UI no longer exposes a type field.
func TestValidateTorrentClientTypeDefaultsToQbit(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) {
		c.TorrentClients = map[string]TorrentClientConfig{
			"q": {
				QbitURL:  "http://x",
				QbitUser: "u",
				QbitPass: "p",
			},
		}
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("qbit should be default torrent client type: %v", err)
	}
}

// The legacy `torrent_client` field must be accepted as a substitute for
// `type`.
func TestValidateTorrentClientLegacyTypeField(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) {
		c.TorrentClients = map[string]TorrentClientConfig{
			"legacy": {TorrentClient: "watch", WatchFolder: "/tmp"},
		}
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("legacy torrent_client field should be accepted: %v", err)
	}
}

func TestValidateWatchClientMissingFolder(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) {
		c.TorrentClients = map[string]TorrentClientConfig{
			"w": {Type: "watch", WatchFolder: "   "},
		}
	})
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "watch_folder") {
		t.Fatalf("want watch_folder error, got %v", err)
	}
}

// qbit host may come from URL or QbitURL; both should satisfy validation.
func TestValidateQbitHostAlternatives(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		client TorrentClientConfig
	}{
		{"url", TorrentClientConfig{
			Type:     "qbit",
			URL:      "http://x",
			Username: "u",
			Password: "p",
		}},
		{"qbit_url", TorrentClientConfig{
			Type:     "qbit",
			QbitURL:  "http://x",
			QbitUser: "u",
			QbitPass: "p",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := withBase(func(c *Config) {
				c.TorrentClients = map[string]TorrentClientConfig{"q": tc.client}
			})
			if err := cfg.Validate(); err != nil {
				t.Fatalf("%s: expected valid, got %v", tc.name, err)
			}
		})
	}
}

func TestQbitSpecificFieldsWinOverLegacyAliases(t *testing.T) {
	t.Parallel()

	client := TorrentClientConfig{
		Username:          "legacy-user",
		Password:          "legacy-pass",
		Category:          "legacy-cat",
		Tags:              []string{"legacy-tag"},
		QbitUser:          "qbit-user",
		QbitPass:          "qbit-pass",
		QbitCategoryValue: "qbit-cat",
		QbitTag:           "qbit-tag",
	}

	if got := client.QbitUsername(); got != "qbit-user" {
		t.Fatalf("expected qbit user to win, got %q", got)
	}
	if got := client.QbitPassword(); got != "qbit-pass" {
		t.Fatalf("expected qbit pass to win, got %q", got)
	}
	if got := client.QbitCategory(); got != "qbit-cat" {
		t.Fatalf("expected qbit category to win, got %q", got)
	}
	if got := client.QbitTags(); got != "qbit-tag" {
		t.Fatalf("expected qbit tag to win, got %q", got)
	}
}

func TestValidateQbitMissingCredentials(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		client TorrentClientConfig
		msg    string
	}{
		{"missing host", TorrentClientConfig{
			Type:     "qbit",
			Username: "u",
			Password: "p",
		}, "url"},
		{"missing user", TorrentClientConfig{
			Type:     "qbit",
			URL:      "http://x",
			Password: "p",
		}, "username"},
		{"missing pass", TorrentClientConfig{
			Type:     "qbit",
			URL:      "http://x",
			Username: "u",
		}, "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := withBase(func(c *Config) {
				c.TorrentClients = map[string]TorrentClientConfig{"q": tc.client}
			})
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("want error mentioning %s, got %v", tc.msg, err)
			}
		})
	}
}

func TestValidateQbitLinking(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		client  TorrentClientConfig
		wantErr string
	}{
		{
			name: "hardlink requires linked folder",
			client: TorrentClientConfig{
				Type:     "qbit",
				URL:      "http://x",
				Username: "u",
				Password: "p",
				Linking:  "hardlink",
			},
			wantErr: "linked_folder",
		},
		{
			name: "symlink accepts linked folder",
			client: TorrentClientConfig{
				Type:         "qbit",
				URL:          "http://x",
				Username:     "u",
				Password:     "p",
				Linking:      "symlink",
				LinkedFolder: StringList{"D:\\Links"},
			},
		},
		{
			name: "reflink accepts linked folder",
			client: TorrentClientConfig{
				Type:         "qbit",
				URL:          "http://x",
				Username:     "u",
				Password:     "p",
				Linking:      "reflink",
				LinkedFolder: StringList{"D:\\Links"},
			},
		},
		{
			name: "reflink requires linked folder",
			client: TorrentClientConfig{
				Type:     "qbit",
				URL:      "http://x",
				Username: "u",
				Password: "p",
				Linking:  "reflink",
			},
			wantErr: "linked_folder",
		},
		{
			name: "invalid mode rejected",
			client: TorrentClientConfig{
				Type:     "qbit",
				URL:      "http://x",
				Username: "u",
				Password: "p",
				Linking:  "copy",
			},
			wantErr: "linking",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := withBase(func(c *Config) {
				c.TorrentClients = map[string]TorrentClientConfig{"q": tc.client}
			})
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error mentioning %s, got %v", tc.wantErr, err)
			}
		})
	}
}

// A qui proxy URL supplies host AND credentials, so no user/pass is required.
func TestValidateQbitWithQuiProxy(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) {
		c.TorrentClients = map[string]TorrentClientConfig{
			"q": {Type: "qbit", QuiProxyURL: "http://proxy:7476/abc"},
		}
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("qui proxy must not require user/pass: %v", err)
	}
}

// A raw "qui" type requires a qui_proxy_url.
func TestValidateQuiRequiresProxy(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) {
		c.TorrentClients = map[string]TorrentClientConfig{"q": {Type: "qui"}}
	})
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "qui_proxy_url") {
		t.Fatalf("want qui_proxy_url error, got %v", err)
	}
}

// Every known tracker with an image-rehost policy must accept img_rehost=true.
// If the schema shrinks, at least one tracker from each policy should still
// pass so we don't accidentally flip a whole group off.
func TestValidateAllImageRehostPoliciesAccepted(t *testing.T) {
	t.Parallel()

	for trackerName := range imagehostpolicy.KnownTrackerPolicies() {
		cfg := withBase(func(c *Config) {
			trackerCfg := TrackerConfig{ImgRehost: true}
			if strings.EqualFold(trackerName, "THR") {
				trackerCfg.ImgAPI = "secret"
			}
			c.Trackers.Trackers = map[string]TrackerConfig{trackerName: trackerCfg}
		})
		if err := cfg.Validate(); err != nil {
			t.Errorf("tracker %s img_rehost should validate: %v", trackerName, err)
		}
	}
}

// A tracker name that doesn't match any policy (even with mixed case) must be
// rejected when img_rehost is requested.
func TestValidateUnknownRehostPolicyRejected(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) {
		c.Trackers.Trackers = map[string]TrackerConfig{"TL": {ImgRehost: true}}
	})
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "TL") {
		t.Fatalf("want TL rejection, got %v", err)
	}
}

// Case insensitivity: "hdb" must resolve the same policy as "HDB".
func TestValidateImageRehostTrackerNameCaseInsensitive(t *testing.T) {
	t.Parallel()

	cfg := withBase(func(c *Config) {
		c.Trackers.Trackers = map[string]TrackerConfig{"hdb": {ImgRehost: true}}
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("lowercase tracker name should still resolve policy: %v", err)
	}
}
