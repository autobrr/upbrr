// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(reader); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	return buf.String()
}

func TestParseCLIOptionsCompatibilityFlags(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"-ua", "-uac", "-sdc", "-sda", "-ddc", "--tmdb", "123", "--imdb", "tt456", "movie.mkv"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !opts.Unattended || !opts.UnattendedConfirm || !opts.SkipDupeCheck || !opts.SkipDupeAsActual || !opts.DoubleDupeCheck {
		t.Fatalf("expected compatibility flags parsed: %#v", opts)
	}
	if len(paths) != 1 || paths[0] != "movie.mkv" {
		t.Fatalf("unexpected paths: %#v", paths)
	}
	if !visited["unattended"] || !visited["unattended_confirm"] || !visited["skip-dupe-check"] || !visited["skip-dupe-asking"] || !visited["double-dupe-check"] {
		t.Fatalf("unexpected visited flags: %#v", visited)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Options.InteractionMode != api.InteractionModeUnattendedConfirm {
		t.Fatalf("expected unattended confirm interaction mode, got %q", req.Options.InteractionMode)
	}
	if !req.SkipDupeCheck || !req.SkipDupeAsActual || !req.DoubleDupeCheck {
		t.Fatalf("expected dupe flags to propagate into request, got %#v", req)
	}
	if req.ExternalIDOverrides.TMDBID == nil || *req.ExternalIDOverrides.TMDBID != 123 {
		t.Fatalf("expected tmdb override 123, got %#v", req.ExternalIDOverrides.TMDBID)
	}
	if req.ExternalIDOverrides.IMDBID == nil || *req.ExternalIDOverrides.IMDBID != 456 {
		t.Fatalf("expected imdb override 456, got %#v", req.ExternalIDOverrides.IMDBID)
	}
}

func TestParseServeOptionsDevNoAuth(t *testing.T) {
	opts, visited, err := parseServeOptions([]string{"--dev-no-auth"})
	if err != nil {
		t.Fatalf("parse serve options: %v", err)
	}
	if !opts.DevNoAuth {
		t.Fatalf("expected dev-no-auth to parse, got %#v", opts)
	}
	if !visited["dev-no-auth"] {
		t.Fatalf("expected dev-no-auth visited flag, got %#v", visited)
	}
}

func TestParseServeOptionsAddressHostPort(t *testing.T) {
	opts, visited, err := parseServeOptions([]string{"--addr", "0.0.0.0:9090", "--host", "localhost", "--port", "7481", "--base-url", "https://example.test/upbrr/", "--persist-listen", "--persist-web-config"})
	if err != nil {
		t.Fatalf("parse serve options: %v", err)
	}
	if opts.Addr != "0.0.0.0:9090" || opts.Host != "localhost" || opts.Port != 7481 || opts.BaseURL != "https://example.test/upbrr/" || !opts.PersistListen || !opts.PersistWebConfig {
		t.Fatalf("unexpected serve options: %#v", opts)
	}
	for _, name := range []string{"addr", "host", "port", "base-url", "persist-listen", "persist-web-config"} {
		if !visited[name] {
			t.Fatalf("expected %s visited flag, got %#v", name, visited)
		}
	}
}

func TestParseServeOptionsPortUsesDecimalSyntax(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{name: "separate value", args: []string{"--port", "080"}, want: 80},
		{name: "equals value", args: []string{"--port=010"}, want: 10},
		{name: "leading zero nine", args: []string{"--port", "009"}, want: 9},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, visited, err := parseServeOptions(tc.args)
			if err != nil {
				t.Fatalf("parse serve options: %v", err)
			}
			if opts.Port != tc.want {
				t.Fatalf("port = %d, want %d", opts.Port, tc.want)
			}
			if !visited["port"] {
				t.Fatalf("expected port visited flag, got %#v", visited)
			}
		})
	}
}

func TestParseServeOptionsRejectsNonDecimalPort(t *testing.T) {
	_, _, err := parseServeOptions([]string{"--port", "0x50"})
	if err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("expected invalid port error, got %v", err)
	}
}

func TestApplyServeOptionOverridesAddress(t *testing.T) {
	cfg, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), serveOptions{Addr: "0.0.0.0:9090"}, map[string]bool{"addr": true})
	if err != nil {
		t.Fatalf("apply serve overrides: %v", err)
	}
	if cfg.Host != "0.0.0.0" || cfg.Port != 9090 {
		t.Fatalf("unexpected web config: %#v", cfg)
	}
}

func TestApplyServeOptionOverridesAddressWithBaseURL(t *testing.T) {
	cfg, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), serveOptions{
		Addr:    "0.0.0.0:9090",
		BaseURL: " https://example.test/upbrr/ ",
	}, map[string]bool{"addr": true, "base-url": true})
	if err != nil {
		t.Fatalf("apply serve overrides: %v", err)
	}
	if cfg.Host != "0.0.0.0" || cfg.Port != 9090 || cfg.BaseURL != "https://example.test/upbrr/" {
		t.Fatalf("unexpected web config: %#v", cfg)
	}
}

func TestApplyServeOptionOverridesAddressMatrix(t *testing.T) {
	cases := []struct {
		name string
		addr string
		host string
		port int
	}{
		{name: "host port", addr: "localhost:9090", host: "localhost", port: 9090},
		{name: "colon port shorthand", addr: ":9091", host: "0.0.0.0", port: 9091},
		{name: "bracketed ipv6", addr: "[::1]:9092", host: "::1", port: 9092},
		{name: "scoped ipv6", addr: "[fe80::1%zone]:9093", host: "fe80::1%zone", port: 9093},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), serveOptions{Addr: tc.addr}, map[string]bool{"addr": true})
			if err != nil {
				t.Fatalf("apply serve overrides: %v", err)
			}
			if cfg.Host != tc.host || cfg.Port != tc.port {
				t.Fatalf("unexpected web config: %#v", cfg)
			}
		})
	}
}

func TestApplyServeOptionOverridesHostPort(t *testing.T) {
	cfg, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), serveOptions{Host: "[::1]", Port: 9091}, map[string]bool{"host": true, "port": true})
	if err != nil {
		t.Fatalf("apply serve overrides: %v", err)
	}
	if cfg.Host != "::1" || cfg.Port != 9091 {
		t.Fatalf("unexpected web config: %#v", cfg)
	}
}

func TestApplyServeOptionOverridesBaseURL(t *testing.T) {
	cfg, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), serveOptions{BaseURL: " https://example.test/upbrr/ "}, map[string]bool{"base-url": true})
	if err != nil {
		t.Fatalf("apply serve overrides: %v", err)
	}
	if cfg.BaseURL != "https://example.test/upbrr/" {
		t.Fatalf("base url = %q, want trimmed configured URL", cfg.BaseURL)
	}
}

func TestApplyServeEnvOverrides(t *testing.T) {
	cfg, err := applyServeEnvOverrides(
		webserver.DefaultCLIConfig(),
		serveEnvOptions{
			Host:           "0.0.0.0",
			Port:           "9090",
			BaseURL:        " /upbrr/?token=secret#frag ",
			OpenBrowser:    "false",
			TrustedProxies: "127.0.0.1, 10.0.0.0/8",
		},
		map[string]bool{
			"host":            true,
			"port":            true,
			"base-url":        true,
			"open-browser":    true,
			"trusted-proxies": true,
		},
	)
	if err != nil {
		t.Fatalf("apply serve env overrides: %v", err)
	}
	if cfg.Host != "0.0.0.0" || cfg.Port != 9090 || cfg.BaseURL != "/upbrr" || cfg.OpenBrowser {
		t.Fatalf("unexpected web config: %#v", cfg)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "127.0.0.1" || cfg.TrustedProxies[1] != "10.0.0.0/8" {
		t.Fatalf("trusted proxies = %#v", cfg.TrustedProxies)
	}
}

func TestApplyServeEnvOverridesRejectsEmptyBaseURL(t *testing.T) {
	stored := webserver.DefaultCLIConfig()
	stored.BaseURL = "/stored/"

	_, err := applyServeEnvOverrides(
		stored,
		serveEnvOptions{BaseURL: " \t "},
		map[string]bool{"base-url": true},
	)
	if err == nil || !strings.Contains(err.Error(), "UPBRR_WEB_BASE_URL cannot be empty") {
		t.Fatalf("expected empty UPBRR_WEB_BASE_URL error, got %v", err)
	}
}

func TestApplyServeOptionOverridesCLIOverridesEnv(t *testing.T) {
	envCfg, err := applyServeEnvOverrides(
		webserver.DefaultCLIConfig(),
		serveEnvOptions{Host: "0.0.0.0", Port: "9090", BaseURL: "/env/"},
		map[string]bool{"host": true, "port": true, "base-url": true},
	)
	if err != nil {
		t.Fatalf("apply serve env overrides: %v", err)
	}
	cfg, err := applyServeOptionOverrides(
		envCfg,
		serveOptions{Host: "127.0.0.1", Port: 9191, BaseURL: "/cli/"},
		map[string]bool{"host": true, "port": true, "base-url": true},
	)
	if err != nil {
		t.Fatalf("apply serve option overrides: %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 9191 || cfg.BaseURL != "/cli" {
		t.Fatalf("CLI did not override env config: %#v", cfg)
	}
}

func TestApplyServeOverridesReplaceInvalidPersistedBaseURL(t *testing.T) {
	persisted := webserver.DefaultCLIConfig()
	persisted.BaseURL = "javascript:alert(1)"

	envCfg, err := applyServeEnvOverrides(
		persisted,
		serveEnvOptions{BaseURL: "/env/"},
		map[string]bool{"base-url": true},
	)
	if err != nil {
		t.Fatalf("apply serve env overrides: %v", err)
	}
	if envCfg.BaseURL != "/env" {
		t.Fatalf("env base URL override = %q, want /env", envCfg.BaseURL)
	}

	cliCfg, err := applyServeOptionOverrides(
		persisted,
		serveOptions{BaseURL: "/cli/"},
		map[string]bool{"base-url": true},
	)
	if err != nil {
		t.Fatalf("apply serve option overrides: %v", err)
	}
	if cliCfg.BaseURL != "/cli" {
		t.Fatalf("CLI base URL override = %q, want /cli", cliCfg.BaseURL)
	}
}

func TestApplyServeOptionOverridesHostPortScopedIPv6(t *testing.T) {
	cfg, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), serveOptions{Host: "[fe80::1%zone]", Port: 9091}, map[string]bool{"host": true, "port": true})
	if err != nil {
		t.Fatalf("apply serve overrides: %v", err)
	}
	if cfg.Host != "fe80::1%zone" || cfg.Port != 9091 {
		t.Fatalf("unexpected web config: %#v", cfg)
	}
	if got := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)); got != "[fe80::1%zone]:9091" {
		t.Fatalf("unexpected bind address: %q", got)
	}
}

func TestApplyServeOptionOverridesHostPortIPv4MappedIPv6(t *testing.T) {
	cfg, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), serveOptions{Host: "[::ffff:127.0.0.1]", Port: 9091}, map[string]bool{"host": true, "port": true})
	if err != nil {
		t.Fatalf("apply serve overrides: %v", err)
	}
	if cfg.Host != "::ffff:127.0.0.1" || cfg.Port != 9091 {
		t.Fatalf("unexpected web config: %#v", cfg)
	}
	if got := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)); got != "[::ffff:127.0.0.1]:9091" {
		t.Fatalf("unexpected bind address: %q", got)
	}
}

func TestApplyServeOptionOverridesAddressScopedIPv6ProducesValidBindAddress(t *testing.T) {
	cfg, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), serveOptions{Addr: "[fe80::1%zone]:9093"}, map[string]bool{"addr": true})
	if err != nil {
		t.Fatalf("apply serve overrides: %v", err)
	}
	if cfg.Host != "fe80::1%zone" || cfg.Port != 9093 {
		t.Fatalf("unexpected web config: %#v", cfg)
	}
	if got := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)); got != "[fe80::1%zone]:9093" {
		t.Fatalf("unexpected bind address: %q", got)
	}
}

func TestApplyServeOptionOverridesRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name    string
		opts    serveOptions
		visited map[string]bool
		want    string
	}{
		{name: "addr with host", opts: serveOptions{Addr: "localhost:7480", Host: "localhost"}, visited: map[string]bool{"addr": true, "host": true}, want: "--addr cannot be used"},
		{name: "empty host", opts: serveOptions{Host: " "}, visited: map[string]bool{"host": true}, want: "--host cannot be empty"},
		{name: "host includes port", opts: serveOptions{Host: "localhost:7480"}, visited: map[string]bool{"host": true}, want: "--host cannot include a port"},
		{name: "scoped ipv6 hostport", opts: serveOptions{Host: "fe80::1%zone:9090"}, visited: map[string]bool{"host": true}, want: "--host cannot include a port"},
		{name: "invalid port", opts: serveOptions{Port: 70000}, visited: map[string]bool{"port": true}, want: "invalid port"},
		{name: "invalid addr", opts: serveOptions{Addr: "localhost"}, visited: map[string]bool{"addr": true}, want: "--addr must be host:port"},
		{name: "unbracketed ipv6 addr", opts: serveOptions{Addr: "::1:9090"}, visited: map[string]bool{"addr": true}, want: "--addr must be host:port"},
		{name: "empty base url", opts: serveOptions{BaseURL: " "}, visited: map[string]bool{"base-url": true}, want: "--base-url cannot be empty"},
		{name: "invalid base url", opts: serveOptions{BaseURL: "javascript:alert(1)"}, visited: map[string]bool{"base-url": true}, want: "http or https"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyServeOptionOverrides(webserver.DefaultCLIConfig(), tc.opts, tc.visited)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseServeHostRejectsMalformedBrackets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		host string
		want string
	}{
		{name: "leading bracket only", host: "[::1", want: "invalid bracket syntax"},
		{name: "trailing bracket only", host: "::1]", want: "invalid bracket syntax"},
		{name: "nested brackets", host: "[[::1]]", want: "invalid bracket syntax"},
		{name: "empty brackets", host: "[]", want: "invalid bracket syntax"},
		{name: "bracketed hostname", host: "[localhost]", want: "IPv6 literals"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseServeHost(tc.host)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseServeHost(%q) error = %v, want substring %q", tc.host, err, tc.want)
			}
		})
	}
}

func TestParseServeHostRejectsInvalidColonHosts(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"foo:bar:baz", "::1:http", "fe80::1%zone:9090", "::ffff:127.0.0.1:9090", "::ffff:127.0.0.999", "localhost:"} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			_, err := parseServeHost(host)
			if err == nil || !strings.Contains(err.Error(), "cannot include a port") {
				t.Fatalf("parseServeHost(%q) error = %v, want port rejection", host, err)
			}
		})
	}
}

func TestParseServeHostPreservesValidIPv6(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want string
	}{
		{host: "::1", want: "::1"},
		{host: "::1:9090", want: "::1:9090"},
		{host: "2001:db8::1234", want: "2001:db8::1234"},
		{host: "[2001:db8::1234]", want: "2001:db8::1234"},
		{host: "[::1]", want: "::1"},
		{host: "::ffff:127.0.0.1", want: "::ffff:127.0.0.1"},
		{host: "[::ffff:127.0.0.1]", want: "::ffff:127.0.0.1"},
		{host: "fe80::1%zone", want: "fe80::1%zone"},
		{host: "[fe80::1%zone]", want: "fe80::1%zone"},
	}

	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			got, err := parseServeHost(tc.host)
			if err != nil {
				t.Fatalf("parseServeHost(%q): %v", tc.host, err)
			}
			if got != tc.want {
				t.Fatalf("parseServeHost(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

func TestParseCLIOptionsRejectsGUIFlag(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"-gui"}); err == nil {
		t.Fatal("expected gui flag to be rejected")
	}
}

func TestParseCLIOptionsExportConfigPlaintext(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"--export-config", "out.yaml", "--export-config-plaintext"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !opts.ExportConfigPlaintext {
		t.Fatalf("expected export-config-plaintext to parse, got %#v", opts)
	}
	if !visited["export-config"] || !visited["export-config-plaintext"] {
		t.Fatalf("unexpected visited flags: %#v", visited)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no positional paths, got %#v", paths)
	}
}

func TestParseCLIOptionsRejectsExportConfigPlaintextWithoutExportConfig(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--export-config-plaintext"}); err == nil {
		t.Fatal("expected --export-config-plaintext without --export-config to fail")
	}
}

func TestParseCLIOptionsRejectsExportConfigPlaintextWithEmptyExportConfig(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--export-config", "", "--export-config-plaintext"}); err == nil {
		t.Fatal("expected --export-config-plaintext with empty --export-config value to fail")
	}
}

func TestParseCLIOptionsPythonAliases(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"-s", "6",
		"-c", "tv",
		"-t", "webdl",
		"-res", "1080p",
		"-g", "NTb",
		"-serv", "NF",
		"-ns",
		"-reg", "A",
		"-year", "2024",
		"-met", "Pilot",
		"--repack", "REPACK",
		"show.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(paths) != 1 || paths[0] != "show.mkv" {
		t.Fatalf("unexpected paths: %#v", paths)
	}
	if opts.Screens != 6 || opts.Category != "tv" || opts.Type != "webdl" || opts.Resolution != "1080p" {
		t.Fatalf("unexpected parsed aliases: %#v", opts)
	}
	if opts.Tag != "NTb" || opts.Service != "NF" || !opts.NoSeed || opts.Region != "A" {
		t.Fatalf("unexpected parsed alias values: %#v", opts)
	}
	if opts.ManualYear != 2024 || opts.EpisodeTitle != "Pilot" || opts.Edition != "REPACK" {
		t.Fatalf("expected python alias values to populate canonical fields: %#v", opts)
	}
	for _, name := range []string{"screens", "category", "type", "resolution", "tag", "service", "no-seed", "region", "manual-year", "episode-title", "edition"} {
		if !visited[name] {
			t.Fatalf("expected alias %q to resolve to canonical visited key, got %#v", name, visited)
		}
	}
}

func TestBuildCLIRequestDebugImpliesDryRunAndOnlyID(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"--debug", "--onlyID", "movie.mkv"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if !req.Options.Debug {
		t.Fatalf("expected debug enabled, got %#v", req.Options)
	}
	if !req.Options.DryRun {
		t.Fatalf("expected debug to imply dry run, got %#v", req.Options)
	}
	if !req.Options.OnlyID {
		t.Fatalf("expected onlyID to propagate, got %#v", req.Options)
	}
	if req.Options.RunLogLevel != "" {
		t.Fatalf("expected run log level unset when flag omitted, got %q", req.Options.RunLogLevel)
	}
}

func TestParseCLIOptionsFlagsAfterPath(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"movie.mkv", "--debug", "-dtmp"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(paths) != 1 || paths[0] != "movie.mkv" {
		t.Fatalf("unexpected paths: %#v", paths)
	}
	if !opts.Debug || !opts.DeleteTmp {
		t.Fatalf("expected trailing flags to parse, got %#v", opts)
	}
	if !visited["debug"] || !visited["delete-tmp"] {
		t.Fatalf("expected trailing flags visited, got %#v", visited)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if !req.Options.Debug || !req.Options.DryRun {
		t.Fatalf("expected trailing debug to force dry run, got %#v", req.Options)
	}
}

func TestParseCLIOptionsRejectsUnknownFlagAfterPath(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"movie.mkv", "--typo-flag"}); err == nil {
		t.Fatal("expected unknown trailing flag to fail")
	}
}

func TestParseCLIOptionsLogLevel(t *testing.T) {
	opts, visited, _, err := parseCLIOptions([]string{"--log-level", "trace", "movie.mkv"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !visited["log-level"] {
		t.Fatalf("expected log-level visited flag, got %#v", visited)
	}
	if opts.LogLevel != "trace" {
		t.Fatalf("expected normalized log level trace, got %q", opts.LogLevel)
	}
}

func TestParseCLIOptionsRejectsInvalidLogLevel(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--log-level", "verbose", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid log level to fail")
	}
}

func TestBuildCLIRequestPropagatesRunLogLevel(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"--debug", "--log-level", "trace", "movie.mkv"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Options.RunLogLevel != "trace" {
		t.Fatalf("expected run log level trace, got %q", req.Options.RunLogLevel)
	}
	if !req.Options.Debug {
		t.Fatalf("expected debug enabled, got %#v", req.Options)
	}
}

func TestBuildCLIRequestSkipAutoTorrent(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"--skip_auto_torrent", "movie.mkv"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if !req.Options.SkipAutoTorrent {
		t.Fatalf("expected skip_auto_torrent to propagate, got %#v", req.Options)
	}
}

func TestBuildCLIRequestKeepFolder(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"-kf", "movie-folder"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !visited["keep-folder"] {
		t.Fatalf("expected keep-folder visited, got %#v", visited)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if !req.Options.KeepFolder {
		t.Fatalf("expected keep-folder to propagate, got %#v", req.Options)
	}
}

func TestBuildCLIRequestExecutionOptions(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--queue", "daily",
		"--limit-queue", "3",
		"--site-check",
		"--site-upload", "blu",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Execution.QueueName != "daily" {
		t.Fatalf("expected queue name daily, got %q", req.Execution.QueueName)
	}
	if req.Execution.QueueLimit != 3 {
		t.Fatalf("expected queue limit 3, got %d", req.Execution.QueueLimit)
	}
	if !req.Execution.SiteCheck {
		t.Fatalf("expected site-check enabled, got %#v", req.Execution)
	}
	if req.Execution.SiteUploadTracker != "BLU" {
		t.Fatalf("expected site upload tracker BLU, got %q", req.Execution.SiteUploadTracker)
	}
	if !req.Options.DryRun {
		t.Fatalf("expected site-check to imply dry run, got %#v", req.Options)
	}
	if len(req.Trackers) != 1 || req.Trackers[0] != "BLU" {
		t.Fatalf("expected site-upload tracker to override request trackers, got %#v", req.Trackers)
	}
}

func TestParseCLIOptionsRejectsNegativeQueueLimit(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--limit-queue", "-1", "movie.mkv"}); err == nil {
		t.Fatal("expected negative limit-queue to fail")
	}
}

func TestParseCLIOptionsWhitespaceQueueRejected(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--queue", "   ", "movie.mkv"}); err == nil {
		t.Fatal("expected whitespace-only --queue to fail")
	}
}

func TestParseCLIOptionsQueueTrimsAndStores(t *testing.T) {
	opts, _, _, err := parseCLIOptions([]string{"--queue", "  daily  ", "root"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.QueueName != "daily" {
		t.Fatalf("expected trimmed queue name %q, got %q", "daily", opts.QueueName)
	}
}

func TestBuildCLIRequestTMDBCompatibilityParsing(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"--tmdb", "movie/123", "movie.mkv"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.ExternalIDOverrides.TMDBID == nil || *req.ExternalIDOverrides.TMDBID != 123 {
		t.Fatalf("expected tmdb override 123, got %#v", req.ExternalIDOverrides.TMDBID)
	}
	if req.ReleaseNameOverrides.Category == nil || *req.ReleaseNameOverrides.Category != "MOVIE" {
		t.Fatalf("expected tmdb category inference, got %#v", req.ReleaseNameOverrides.Category)
	}
}

func TestParseCLIOptionsRejectsInvalidTMDBCompatibilityValue(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--tmdb", "movie/not-a-number", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid tmdb compatibility input to fail")
	}
}

func TestBuildCLIRequestTrackerIDOverrides(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--aither", "https://aither.cc/torrents/12345",
		"--ptp", "https://passthepopcorn.me/torrents.php?id=10&torrentid=67890",
		"--hdb", "https://hdbits.org/details.php?id=2468&other=1",
		"--bhd", "https://beyond-hd.me/torrents/The.Movie.2024.98765",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.TrackerIDOverrides["aither"]; got != "12345" {
		t.Fatalf("expected aither tracker id 12345, got %q", got)
	}
	if got := req.TrackerIDOverrides["ptp"]; got != "67890" {
		t.Fatalf("expected ptp tracker id 67890, got %q", got)
	}
	if got := req.TrackerIDOverrides["hdb"]; got != "2468" {
		t.Fatalf("expected hdb tracker id 2468, got %q", got)
	}
	if got := req.TrackerIDOverrides["bhd"]; got != "98765" {
		t.Fatalf("expected bhd tracker id 98765, got %q", got)
	}
}

func TestParseCLIOptionsRejectsInvalidTrackerURL(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--ptp", "https://passthepopcorn.me/torrents.php?id=10", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid tracker url to fail")
	}
}

func TestBuildCLIRequestDescriptionOverrides(t *testing.T) {
	dir := t.TempDir()
	descPath := filepath.Join(dir, "desc.txt")
	if err := os.WriteFile(descPath, []byte("custom description"), 0o600); err != nil {
		t.Fatalf("write desc file: %v", err)
	}

	opts, visited, paths, err := parseCLIOptions([]string{
		"--descfile", descPath,
		"--desclink", "https://example.com/description.txt",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.DescriptionOverrideRaw != "custom description" {
		t.Fatalf("expected descfile contents in request, got %q", req.DescriptionOverrideRaw)
	}
	if req.DescriptionOverrideURL != "https://example.com/description.txt" {
		t.Fatalf("expected desclink in request, got %q", req.DescriptionOverrideURL)
	}
}

func TestBuildCLIRequestMetadataOverrides(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--distributor", "Criterion",
		"--original-language", "ja",
		"--commentary",
		"--personalrelease",
		"--stream",
		"--webdv",
		"--not-anime",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.MetadataOverrides.Distributor == nil || *req.MetadataOverrides.Distributor != "Criterion" {
		t.Fatalf("expected distributor override, got %#v", req.MetadataOverrides.Distributor)
	}
	if req.MetadataOverrides.OriginalLanguage == nil || *req.MetadataOverrides.OriginalLanguage != "ja" {
		t.Fatalf("expected original language override, got %#v", req.MetadataOverrides.OriginalLanguage)
	}
	if req.MetadataOverrides.Commentary == nil || !*req.MetadataOverrides.Commentary {
		t.Fatalf("expected commentary override, got %#v", req.MetadataOverrides.Commentary)
	}
	if req.MetadataOverrides.PersonalRelease == nil || !*req.MetadataOverrides.PersonalRelease {
		t.Fatalf("expected personal release override, got %#v", req.MetadataOverrides.PersonalRelease)
	}
	if req.MetadataOverrides.StreamOptimized == nil || !*req.MetadataOverrides.StreamOptimized {
		t.Fatalf("expected stream override, got %#v", req.MetadataOverrides.StreamOptimized)
	}
	if req.MetadataOverrides.WebDV == nil || !*req.MetadataOverrides.WebDV {
		t.Fatalf("expected webdv override, got %#v", req.MetadataOverrides.WebDV)
	}
	if req.MetadataOverrides.Anime == nil || *req.MetadataOverrides.Anime {
		t.Fatalf("expected not-anime to force anime=false, got %#v", req.MetadataOverrides.Anime)
	}
}

func TestBuildCLIRequestTrackerConfigOverrides(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--anon",
		"--draft",
		"--modq",
		"--channel", "spd",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.TrackerConfigOverrides.Anon == nil || !*req.TrackerConfigOverrides.Anon {
		t.Fatalf("expected anon override, got %#v", req.TrackerConfigOverrides.Anon)
	}
	if req.TrackerConfigOverrides.Draft == nil || !*req.TrackerConfigOverrides.Draft {
		t.Fatalf("expected draft override, got %#v", req.TrackerConfigOverrides.Draft)
	}
	if req.TrackerConfigOverrides.ModQ == nil || !*req.TrackerConfigOverrides.ModQ {
		t.Fatalf("expected modq override, got %#v", req.TrackerConfigOverrides.ModQ)
	}
	if req.TrackerConfigOverrides.Channel == nil || *req.TrackerConfigOverrides.Channel != "spd" {
		t.Fatalf("expected channel override, got %#v", req.TrackerConfigOverrides.Channel)
	}
}

func TestBuildCLIRequestClientOverrides(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--client", "qbit",
		"--qbit-tag", "mytag",
		"--qbit-cat", "mycat",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.ClientOverrides.Client == nil || *req.ClientOverrides.Client != "qbit" {
		t.Fatalf("expected client override, got %#v", req.ClientOverrides.Client)
	}
	if req.ClientOverrides.QbitTag == nil || *req.ClientOverrides.QbitTag != "mytag" {
		t.Fatalf("expected qbit tag override, got %#v", req.ClientOverrides.QbitTag)
	}
	if req.ClientOverrides.QbitCategory == nil || *req.ClientOverrides.QbitCategory != "mycat" {
		t.Fatalf("expected qbit category override, got %#v", req.ClientOverrides.QbitCategory)
	}
}

func TestBuildCLIRequestImageHostOverrides(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--imghost", "pixhost",
		"--skip-imagehost-upload",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.ImageHostOverrides.PreferredHost == nil || *req.ImageHostOverrides.PreferredHost != "pixhost" {
		t.Fatalf("expected preferred image host override, got %#v", req.ImageHostOverrides.PreferredHost)
	}
	if req.ImageHostOverrides.SkipUpload == nil || !*req.ImageHostOverrides.SkipUpload {
		t.Fatalf("expected skip image-host upload override, got %#v", req.ImageHostOverrides.SkipUpload)
	}
}

func TestPrintDryRunSummary(t *testing.T) {
	tests := []struct {
		name     string
		entry    api.TrackerDryRunEntry
		contains []string
	}{
		{
			name: "prints summary details",
			entry: api.TrackerDryRunEntry{
				Tracker:     "BLU",
				Status:      "ready",
				Message:     "looks good",
				ReleaseName: "Movie.2024.1080p",
				Payload: map[string]string{
					"category": "MOVIE",
					"name":     "Movie.2024.1080p",
				},
				ImageHost: api.ImageHostFeedback{
					Reuploaded: true,
					Message:    "reuploaded to imgbox",
					Warnings: []api.ImageHostWarning{
						{Host: "pixhost", Message: "temporary failure"},
					},
				},
			},
			contains: []string{
				"Dry run: ready (looks good)",
				"Tracker release name: Movie.2024.1080p",
				"Images: reuploaded to imgbox",
				"Image host warning: pixhost failed: temporary failure",
			},
		},
		{
			name: "prints release name changes",
			entry: api.TrackerDryRunEntry{
				Tracker:                 "AITHER",
				Status:                  "ready",
				ReleaseName:             "Movie.2024.1080p.WEB-DL.x264-GRP",
				OriginalReleaseName:     "Movie.2024.1080p.WEB-DL.H264-GRP",
				UploadReleaseName:       "Movie.2024.1080p.WEB-DL.x264-GRP",
				ReleaseNameChanged:      true,
				ReleaseNameChangeReason: "tracker naming policy",
			},
			contains: []string{
				"Dry run: ready",
				"Tracker release name changed: Movie.2024.1080p.WEB-DL.H264-GRP -> Movie.2024.1080p.WEB-DL.x264-GRP (reason: tracker naming policy)",
			},
		},
		{
			name:  "empty tracker prints nothing",
			entry: api.TrackerDryRunEntry{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := captureStdout(t, func() {
				printDryRunSummary(tt.entry)
			})
			for _, expected := range tt.contains {
				if !strings.Contains(output, expected) {
					t.Fatalf("expected output to contain %q, got %q", expected, output)
				}
			}
			if len(tt.contains) == 0 && output != "" {
				t.Fatalf("expected no output, got %q", output)
			}
		})
	}
}

func TestPrintMetadataPreviewShowsRichReleaseDetails(t *testing.T) {
	output := captureStdout(t, func() {
		printMetadataPreview(api.MetadataPreview{
			SourcePath:  "C:\\path\\to\\Example.Release.2026.S01.1080p.WEB-DL-GRP",
			ReleaseName: "Example Release 2026 S01 1080p WEB-DL H.264-GRP",
			TrackerName: "LST",
			ExternalIDs: api.ExternalIDs{
				TMDBID:   123456,
				IMDBID:   7654321,
				TVDBID:   234567,
				TVmazeID: 34567,
				Category: "TV",
			},
			Warnings: []string{"Season pack contains mixed group tags (ALT, GRP); trackers with mixed-origin support will use Mixed."},
			ExternalPreview: []api.ExternalPreview{{
				Provider:     "tmdb",
				ID:           123456,
				Title:        "Example Release",
				Year:         2026,
				Overview:     "Example overview for a fictional series used in CLI preview output.",
				Genres:       "Drama, Mystery",
				Category:     "TV",
				FirstAirDate: "2026-02-22",
				Runtime:      55,
				Rating:       7.9,
				RatingCount:  1200,
			}},
		}, true)
	})

	for _, expected := range []string{
		"Release details",
		"Debug mode: no actual tracker uploads will be processed.",
		"Source: [local path]",
		"Upload name: Example Release 2026 S01 1080p WEB-DL H.264-GRP",
		"Database info",
		"Title: Example Release (2026)",
		"Overview: Example overview for a fictional series used in CLI preview output.",
		"Genres: Drama, Mystery",
		"Category: TV",
		"Date: 2026-02-22",
		"Runtime: 55 min",
		"Rating: 7.9 (1200 votes)",
		"Tracker data from: LST",
		"External IDs",
		"TMDB: https://www.themoviedb.org/tv/123456",
		"IMDb: https://www.imdb.com/title/tt7654321",
		"TVDB: https://www.thetvdb.com/?id=234567&tab=series",
		"TVmaze: https://www.tvmaze.com/shows/34567",
		"Warnings:",
		"- Season pack contains mixed group tags (ALT, GRP); trackers with mixed-origin support will use Mixed.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}
}

func TestPrintDryRunDetails(t *testing.T) {
	tests := []struct {
		name        string
		entry       api.TrackerDryRunEntry
		contains    []string
		notContains []string
	}{
		{
			name: "prints files payload and condenses body fields",
			entry: api.TrackerDryRunEntry{
				Endpoint: "https://tracker.test/upload",
				Files: []api.TrackerDryRunFile{
					{Field: "torrent", Path: "C:\\Users\\Tester\\.upbrr\\tmp\\file.torrent", Present: true},
					{Field: "nfo", Path: "", Present: false},
				},
				Payload: map[string]string{
					"category":    "MOVIE",
					"description": "line 1\nline 2",
					"keywords":    "movie, webdl",
					"mediainfo":   "General\nComplete name: Movie.2024.mkv",
					"name":        "Movie.2024",
					"passkey":     "secret-passkey",
				},
				Description: "line 1\nline 2",
			},
			contains: []string{
				"Files:",
				"- torrent [present]: .upbrr/tmp/file.torrent",
				"- nfo [missing]: (none)",
				"Payload:",
				"- category: MOVIE",
				"- description: [13 bytes, 2 lines omitted]",
				"- keywords: movie, webdl",
				"- mediainfo: [37 bytes, 2 lines omitted]",
				"- name: Movie.2024",
				"- passkey: [REDACTED]",
			},
			notContains: []string{
				"C:\\Users\\Tester",
				"Endpoint:",
				"https://tracker.test/upload",
				"line 1\nline 2",
				"secret-passkey",
				"General\nComplete name",
			},
		},
		{
			name:  "empty details print nothing",
			entry: api.TrackerDryRunEntry{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := captureStdout(t, func() {
				printDryRunDetails(tt.entry)
			})
			for _, expected := range tt.contains {
				if !strings.Contains(output, expected) {
					t.Fatalf("expected output to contain %q, got %q", expected, output)
				}
			}
			for _, unexpected := range tt.notContains {
				if strings.Contains(output, unexpected) {
					t.Fatalf("expected output not to contain %q, got %q", unexpected, output)
				}
			}
			if len(tt.contains) == 0 && output != "" {
				t.Fatalf("expected no output, got %q", output)
			}
		})
	}
}

func TestPrintDryRunDetailsRedactsSensitiveEndpointAndPayload(t *testing.T) {
	output := captureStdout(t, func() {
		printDryRunDetails(api.TrackerDryRunEntry{
			Endpoint: "https://tracker.test/api/upload?api_key=secret-key&passkey=secret-pass",
			Payload: map[string]string{
				"api_key":  "secret-key",
				"auth":     "secret-auth",
				"keywords": "movie, webdl",
				"name":     "Movie.2024",
				"passkey":  "secret-pass",
				"announce": "https://tracker.test/announce?passkey=secret-pass",
			},
		})
	})

	for _, secret := range []string{"secret-key", "secret-pass", "secret-auth"} {
		if strings.Contains(output, secret) {
			t.Fatalf("expected %q to be redacted, got %q", secret, output)
		}
	}
	for _, expected := range []string{
		"- api_key: [REDACTED]",
		"- auth: [REDACTED]",
		"- keywords: movie, webdl",
		"- name: Movie.2024",
		"- passkey: [REDACTED]",
		"- announce: https://tracker.test/announce?passkey=[REDACTED]",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected redacted output to contain %q, got %q", expected, output)
		}
	}
	if strings.Contains(output, "Endpoint:") {
		t.Fatalf("expected endpoint to be omitted from dry-run details")
	}
}

func TestFormatPathLabelKeepsDBRelativePath(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "windows db tmp path",
			input: `C:\Users\Tester\.upbrr\tmp\file.torrent`,
			want:  ".upbrr/tmp/file.torrent",
		},
		{
			name:  "unix db cache path",
			input: "/home/tester/.upbrr/cache/banned/file.json",
			want:  ".upbrr/cache/banned/file.json",
		},
		{
			name:  "outside db path",
			input: `D:\media\Example.Release.2026-GRP`,
			want:  "[local path]",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPathLabel(tt.input); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestPrintDryRunDetailsSummarizesDescriptionWithoutPayloadDescription(t *testing.T) {
	output := captureStdout(t, func() {
		printDryRunDetails(api.TrackerDryRunEntry{
			Payload:     map[string]string{"name": "Movie.2024"},
			Description: "first line\nsecond line",
		})
	})

	if !strings.Contains(output, "Description: [22 bytes, 2 lines omitted]") {
		t.Fatalf("expected summarized description, got %q", output)
	}
	if strings.Contains(output, "first line") {
		t.Fatalf("expected raw description to be omitted, got %q", output)
	}
}

func TestPrintDebugUploadReview(t *testing.T) {
	review := api.UploadReview{
		SourcePath: "C:\\releases\\movie",
		Trackers: []api.TrackerReview{
			{
				Tracker: "BLU",
				DryRun: api.TrackerDryRunEntry{
					Tracker:             "BLU",
					Status:              "ready",
					Endpoint:            "https://blu.test/upload",
					ReleaseName:         "Movie.2024",
					OriginalReleaseName: "Movie 2024",
					UploadReleaseName:   "Movie.2024",
					ReleaseNameChanged:  true,
					Payload:             map[string]string{"name": "Movie.2024"},
					Description:         "test description",
				},
			},
			{
				Tracker:      "HDB",
				Banned:       true,
				BannedReason: "group banned",
			},
		},
	}

	output := captureStdout(t, func() {
		printDebugUploadReview(review)
	})

	for _, expected := range []string{
		"[Debug Dry Run] [local path]",
		"[BLU Debug Payload]",
		"Dry run: ready",
		"Tracker release name changed: Movie 2024 -> Movie.2024",
		"- name: Movie.2024",
		"[HDB Debug Payload]",
		"Banned group: group banned",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}
	for _, unexpected := range []string{
		"Endpoint:",
		"Payload fields:",
	} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("expected output not to contain %q, got %q", unexpected, output)
		}
	}
}

func TestPrintDebugUploadReviewGroupsIdenticalPayloads(t *testing.T) {
	review := api.UploadReview{
		SourcePath: "C:\\releases\\movie",
		Trackers: []api.TrackerReview{
			{
				Tracker: "BLU",
				DryRun: api.TrackerDryRunEntry{
					Tracker:     "BLU",
					Status:      "ready",
					ReleaseName: "Movie.2024",
					Payload:     map[string]string{"category": "MOVIE", "name": "Movie.2024"},
				},
			},
			{
				Tracker: "SP",
				DryRun: api.TrackerDryRunEntry{
					Tracker:     "SP",
					Status:      "ready",
					ReleaseName: "Movie.2024",
					Payload:     map[string]string{"category": "MOVIE", "name": "Movie.2024"},
				},
			},
			{
				Tracker: "HDB",
				DryRun: api.TrackerDryRunEntry{
					Tracker:             "HDB",
					Status:              "ready",
					ReleaseName:         "Movie-2024",
					OriginalReleaseName: "Movie 2024",
					UploadReleaseName:   "Movie-2024",
					ReleaseNameChanged:  true,
					Payload:             map[string]string{"category": "MOVIE", "name": "Movie-2024"},
				},
			},
		},
	}

	output := captureStdout(t, func() {
		printDebugUploadReview(review)
	})

	for _, expected := range []string{
		"[BLU, SP Debug Payload]",
		"[HDB Debug Payload]",
		"Tracker release name changed: Movie 2024 -> Movie-2024",
		"- name: Movie.2024",
		"- name: Movie-2024",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}
	for _, unexpected := range []string{
		"[BLU Debug Payload]",
		"[SP Debug Payload]",
	} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("expected output not to contain %q, got %q", unexpected, output)
		}
	}
	if count := strings.Count(output, "- name: Movie.2024"); count != 1 {
		t.Fatalf("expected grouped payload to print once, got %d occurrences in %q", count, output)
	}
}

func TestBuildCLIRequestTrackerSiteOverrides(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--foreign",
		"--opera",
		"--asian",
		"--disctype", "bd50",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.TrackerSiteOverrides.TIK.Foreign == nil || !*req.TrackerSiteOverrides.TIK.Foreign {
		t.Fatalf("expected foreign override, got %#v", req.TrackerSiteOverrides.TIK.Foreign)
	}
	if req.TrackerSiteOverrides.TIK.Opera == nil || !*req.TrackerSiteOverrides.TIK.Opera {
		t.Fatalf("expected opera override, got %#v", req.TrackerSiteOverrides.TIK.Opera)
	}
	if req.TrackerSiteOverrides.TIK.Asian == nil || !*req.TrackerSiteOverrides.TIK.Asian {
		t.Fatalf("expected asian override, got %#v", req.TrackerSiteOverrides.TIK.Asian)
	}
	if req.TrackerSiteOverrides.TIK.DiscType == nil || *req.TrackerSiteOverrides.TIK.DiscType != "BD50" {
		t.Fatalf("expected disctype override, got %#v", req.TrackerSiteOverrides.TIK.DiscType)
	}
}

func TestParseCLIOptionsRejectsInvalidTIKDiscType(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--disctype", "dvd7", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid disctype to fail")
	}
}

func TestParseCLIOptionsRejectsInvalidImageHost(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--imghost", "not-a-host", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid imghost to fail")
	}
	if _, _, _, err := parseCLIOptions([]string{"--imghost", "hdb", "movie.mkv"}); err == nil {
		t.Fatal("expected tracker-owned hdb imghost to fail")
	}
}

func TestBuildCLIRequestInfoHashOverrides(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--torrenthash", "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.TorrentOverrides.InfoHash == nil || *req.TorrentOverrides.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("expected normalized infohash override, got %#v", req.TorrentOverrides.InfoHash)
	}
}

func TestParseCLIOptionsRejectsInvalidInfoHash(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--infohash", "not-a-hash", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid infohash to fail")
	}
}

func TestBuildCLIRequestMaxPieceSizeOverride(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"--max-piece-size", "16", "movie.mkv"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.TorrentOverrides.MaxPieceSizeMiB == nil || *req.TorrentOverrides.MaxPieceSizeMiB != 16 {
		t.Fatalf("expected max piece size override, got %#v", req.TorrentOverrides.MaxPieceSizeMiB)
	}
}

func TestParseCLIOptionsRejectsInvalidMaxPieceSize(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--max-piece-size", "3", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid max-piece-size to fail")
	}
}

func TestBuildCLIRequestMALOverride(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{"--mal", "321", "anime.mkv"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.ExternalIDOverrides.MALID == nil || *req.ExternalIDOverrides.MALID != 321 {
		t.Fatalf("expected mal override 321, got %#v", req.ExternalIDOverrides.MALID)
	}
}

func TestBuildCLIRequestScreenshotOverrides(t *testing.T) {
	comparisonDir := t.TempDir()

	opts, visited, paths, err := parseCLIOptions([]string{
		"--manual_frames", "100,250,500",
		"--comparison", comparisonDir,
		"--comparison_index", "2",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.ScreenshotOverrides.ManualFrames; len(got) != 3 || got[0] != 100 || got[1] != 250 || got[2] != 500 {
		t.Fatalf("expected manual frame overrides, got %#v", got)
	}
	if got := req.ScreenshotOverrides.ComparisonPaths; len(got) != 1 || got[0] != comparisonDir {
		t.Fatalf("expected comparison path override, got %#v", got)
	}
	if req.ScreenshotOverrides.ComparisonPrimaryIndex == nil || *req.ScreenshotOverrides.ComparisonPrimaryIndex != 2 {
		t.Fatalf("expected comparison primary index override, got %#v", req.ScreenshotOverrides.ComparisonPrimaryIndex)
	}
}

func TestParseCLIOptionsRejectsInvalidManualFrames(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--manual_frames", "10,nope,30", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid manual_frames to fail")
	}
}

func TestParseCLIOptionsRejectsInvalidComparisonIndex(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--comparison_index", "0", "movie.mkv"}); err == nil {
		t.Fatal("expected invalid comparison_index to fail")
	}
}

func TestBuildCLIRequestTorrentHashModeOverrides(t *testing.T) {
	opts, visited, paths, err := parseCLIOptions([]string{
		"--force-recheck",
		"--nohash",
		"movie.mkv",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	req, err := buildCLIRequest(opts, visited, paths, 4)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.ClientOverrides.ForceRecheck == nil || !*req.ClientOverrides.ForceRecheck {
		t.Fatalf("expected force-recheck override, got %#v", req.ClientOverrides.ForceRecheck)
	}
	if req.TorrentOverrides.NoHash == nil || !*req.TorrentOverrides.NoHash {
		t.Fatalf("expected nohash override, got %#v", req.TorrentOverrides.NoHash)
	}
	if req.TorrentOverrides.Rehash != nil {
		t.Fatalf("expected rehash to be unset, got %#v", req.TorrentOverrides.Rehash)
	}
}

func TestParseCLIOptionsRejectsConflictingHashModes(t *testing.T) {
	if _, _, _, err := parseCLIOptions([]string{"--nohash", "--rehash", "movie.mkv"}); err == nil {
		t.Fatal("expected conflicting nohash and rehash flags to fail")
	}
}
