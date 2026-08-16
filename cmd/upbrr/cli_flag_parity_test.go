// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

type cliCompositeFlagClass struct {
	kind   string
	reason string
}

func TestEveryCanonicalCLIFlagIsClassifiedForCompositeUpload(t *testing.T) {
	t.Parallel()

	registered := commandFlagNames(newUploadRootCommand(cliIO{}, nil).Flags())
	aliases := cliFlagAliases()
	if len(registered) != 150 || len(aliases) != 53 {
		t.Fatalf("upload flag inventory: registered=%d aliases=%d, want 150 and 53", len(registered), len(aliases))
	}
	for alias, target := range aliases {
		if _, exists := registered[alias]; !exists {
			t.Errorf("alias %q is not registered", alias)
		}
		if _, exists := registered[target]; !exists {
			t.Errorf("alias %q targets unregistered canonical flag %q", alias, target)
		}
	}
	canonical := make(map[string]struct{}, len(registered))
	for name := range registered {
		if target, ok := aliases[name]; ok {
			name = target
		}
		canonical[name] = struct{}{}
	}
	if len(canonical) != 97 {
		t.Fatalf("canonical upload flags=%d, want 97", len(canonical))
	}

	classified := cliCompositeFlagManifest()
	for name := range canonical {
		classification, ok := classified[name]
		if !ok {
			t.Errorf("canonical CLI flag %q has no composite parity classification", name)
			continue
		}
		if classification.kind == "cli_only" && strings.TrimSpace(classification.reason) == "" {
			t.Errorf("CLI-only flag %q has no exclusion reason", name)
		}
	}
	for name := range classified {
		if _, ok := canonical[name]; !ok {
			t.Errorf("composite parity manifest contains unregistered canonical flag %q", name)
		}
	}
}

func TestServeAndAPITokenFlagsStayOutsideUploadManifest(t *testing.T) {
	t.Parallel()

	assertSourceFlagsEqual(t, "serve", commandFlagNames(newServeCommand(cliIO{}).Flags()), []string{
		"addr",
		"base-url",
		"config",
		"dev-no-auth",
		"host",
		"persist-listen",
		"persist-web-config",
		"port",
	})
	apiTokenFlags := make(map[string]struct{})
	for _, command := range []*pflag.FlagSet{
		newAPITokenListCommand(cliIO{}).Flags(),
		newAPITokenRevokeCommand(cliIO{}).Flags(),
	} {
		for name := range commandFlagNames(command) {
			apiTokenFlags[name] = struct{}{}
		}
	}
	assertSourceFlagsEqual(t, "API token", apiTokenFlags, []string{"config"})
}

func cliCompositeFlagManifest() map[string]cliCompositeFlagClass {
	mapped := []string{
		"aither",
		"anon",
		"asian",
		"bhd",
		"blu",
		"btn",
		"category",
		"channel",
		"client",
		"commentary",
		"comparison",
		"comparison_index",
		"daily",
		"debug",
		"descfile",
		"desclink",
		"disctype",
		"distributor",
		"double-dupe-check",
		"draft",
		"dual-audio",
		"edition",
		"episode",
		"episode-title",
		"force-recheck",
		"foreign",
		"get-dvd-menus",
		"hdb",
		"imdb",
		"imghost",
		"infohash",
		"keep-folder",
		"log-level",
		"lst",
		"mal",
		"manual_frames",
		"manual-year",
		"max-piece-size",
		"menu-images",
		"modq",
		"no-aka",
		"no-dual",
		"no-dub",
		"no-distributor",
		"no-edition",
		"no-episode-title",
		"no-season",
		"no-seed",
		"no-tag",
		"no-year",
		"nohash",
		"not-anime",
		"oe",
		"onlyID",
		"opera",
		"original-language",
		"personalrelease",
		"ptp",
		"qbit-cat",
		"qbit-tag",
		"region",
		"rehash",
		"resolution",
		"screens",
		"season",
		"service",
		"site-check",
		"site-upload",
		"skip_auto_torrent",
		"skip-dupe-asking",
		"skip-dupe-check",
		"skip-imagehost-upload",
		"source",
		"stream",
		"tag",
		"tmdb",
		"trackers",
		"trackers-remove",
		"tvdb",
		"tvmaze",
		"type",
		"ulcx",
		"unattended",
		"unattended_confirm",
		"upload-only",
		"webdv",
	}
	result := make(map[string]cliCompositeFlagClass, len(mapped)+10)
	for _, name := range mapped {
		result[name] = cliCompositeFlagClass{kind: "mapped"}
	}
	for name, reason := range map[string]string{
		"cleanup":                 "cross-workflow storage administration",
		"config":                  "process configuration selection",
		"console-log-level":       "process console verbosity",
		"create-auth":             "authentication administration",
		"delete-tmp":              "pre-upload storage administration",
		"export-config":           "configuration administration",
		"export-config-plaintext": "configuration administration",
		"import-config":           "configuration administration",
		"limit-queue":             "multi-source scheduling",
		"queue":                   "multi-source discovery and scheduling",
		"version":                 "process information",
	} {
		result[name] = cliCompositeFlagClass{kind: "cli_only", reason: reason}
	}
	return result
}

func commandFlagNames(fs *pflag.FlagSet) map[string]struct{} {
	result := make(map[string]struct{})
	fs.VisitAll(func(flag *pflag.Flag) {
		if flag.Name != "help" {
			result[flag.Name] = struct{}{}
		}
	})
	return result
}

func assertSourceFlagsEqual(
	t *testing.T,
	label string,
	actual map[string]struct{},
	expected []string,
) {
	t.Helper()
	actualNames := make([]string, 0, len(actual))
	for name := range actual {
		actualNames = append(actualNames, name)
	}
	slices.Sort(actualNames)
	slices.Sort(expected)
	if !slices.Equal(actualNames, expected) {
		t.Fatalf("%s flags = %v, want %v", label, actualNames, expected)
	}
}
