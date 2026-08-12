// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type cliCompositeFlagClass struct {
	kind   string
	reason string
}

func TestEveryCanonicalCLIFlagIsClassifiedForCompositeUpload(t *testing.T) {
	t.Parallel()

	registered := sourceFlagNames(t, "cli_options.go", "parseCLIOptions")
	aliases := cliFlagAliases()
	canonical := make(map[string]struct{}, len(registered))
	for name := range registered {
		if target, ok := aliases[name]; ok {
			if _, exists := registered[target]; !exists {
				t.Errorf("alias %q targets unregistered canonical flag %q", name, target)
			}
			name = target
		}
		canonical[name] = struct{}{}
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

	assertSourceFlagsEqual(t, "serve", sourceFlagNames(t, "cli_options.go", "parseServeOptions"), []string{
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
	for _, function := range []string{
		"runCreateAPITokenCommand",
		"runListAPITokensCommand",
		"runRevokeAPITokenCommand",
	} {
		for name := range sourceFlagNames(t, "api_token_cli.go", function) {
			apiTokenFlags[name] = struct{}{}
		}
	}
	assertSourceFlagsEqual(t, "API token", apiTokenFlags, []string{"config", "name", "owner", "scopes"})
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

func sourceFlagNames(t *testing.T, fileName string, functionName string) map[string]struct{} {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve parity test source path")
	}
	path := filepath.Join(filepath.Dir(currentFile), fileName)
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	result := make(map[string]struct{})
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasSuffix(selector.Sel.Name, "Var") {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "fs" {
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			name, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				t.Errorf("decode flag name %s: %v", literal.Value, unquoteErr)
				return true
			}
			result[name] = struct{}{}
			return true
		})
		return result
	}
	t.Fatalf("function %s not found in %s", functionName, fileName)
	return nil
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
