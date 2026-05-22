// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		"--imghost", "PTPIMG",
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
	if req.ImageHostOverrides.PreferredHost == nil || *req.ImageHostOverrides.PreferredHost != "ptpimg" {
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
						{Host: "ptpimg", Message: "temporary failure"},
					},
				},
			},
			contains: []string{
				"Dry run: ready (looks good)",
				"Tracker release name: Movie.2024.1080p",
				"Payload fields: category, name",
				"Images: reuploaded to imgbox",
				"Image host warning: ptpimg failed: temporary failure",
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

func TestPrintDryRunDetails(t *testing.T) {
	tests := []struct {
		name     string
		entry    api.TrackerDryRunEntry
		contains []string
	}{
		{
			name: "prints endpoint files payload and description",
			entry: api.TrackerDryRunEntry{
				Endpoint: "https://tracker.test/upload",
				Files: []api.TrackerDryRunFile{
					{Field: "torrent", Path: "C:\\tmp\\file.torrent", Present: true},
					{Field: "nfo", Path: "", Present: false},
				},
				Payload: map[string]string{
					"category": "MOVIE",
					"name":     "Movie.2024",
				},
				Description: "hello world",
			},
			contains: []string{
				"Endpoint: https://tracker.test/upload",
				"Files:",
				"- torrent [present]: C:\\tmp\\file.torrent",
				"- nfo [missing]: (none)",
				"Payload:",
				"- category: MOVIE",
				"- name: Movie.2024",
				"Description:\nhello world\n",
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
			if len(tt.contains) == 0 && output != "" {
				t.Fatalf("expected no output, got %q", output)
			}
		})
	}
}

func TestPrintDebugUploadReview(t *testing.T) {
	review := api.UploadReview{
		SourcePath: "C:\\releases\\movie",
		Trackers: []api.TrackerReview{
			{
				Tracker: "BLU",
				DryRun: api.TrackerDryRunEntry{
					Tracker:     "BLU",
					Status:      "ready",
					Endpoint:    "https://blu.test/upload",
					Payload:     map[string]string{"name": "Movie.2024"},
					Description: "test description",
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
		"[Debug Dry Run] C:\\releases\\movie",
		"[BLU Debug Payload]",
		"Dry run: ready",
		"Endpoint: https://blu.test/upload",
		"- name: Movie.2024",
		"[HDB Debug Payload]",
		"Banned group: group banned",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
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
