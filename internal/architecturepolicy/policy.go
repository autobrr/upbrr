// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package architecturepolicy enforces lasting ownership boundaries for the
// canonical runtime and prepared-release architecture.
package architecturepolicy

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Violation is one source location that crosses a protected architecture boundary.
type Violation struct {
	File    string
	Line    int
	Column  int
	Message string
}

var releasePageDirectories = map[string]struct{}{
	"bluray_candidates":   {},
	"description_builder": {},
	"dupe_check":          {},
	"input":               {},
	"menu_images":         {},
	"playlist_selection":  {},
	"screenshots":         {},
	"tracker_data":        {},
	"tracker_upload":      {},
	"upload_images":       {},
}

var messageRecoveryPattern = regexp.MustCompile(`(?i)(error|message)[A-Za-z0-9_?.]*(?:\.toLowerCase\(\))?\.(includes|startsWith|match)\(`)

var frontendBDMVPathPattern = regexp.MustCompile(`(?i)(\$\{[^}]+\}[^\n]*[\\/]BDMV|[+][^\n]*["'\x60][\\/]?BDMV|BDMV[^\n]*\.slice\()`)

var preparationIntentTrackersPattern = regexp.MustCompile(`(?s)export\s+type\s+PreparationIntent\s*=.*?\btrackers\b.*?};`)

var workflowInterfaceMarkers = []string{"Capability", "Module", "Operation", "Runner", "Service", "Workflow"}

var preparedStateFields = map[string]struct{}{
	"BlockedTrackers":             {},
	"ClientTorrentPath":           {},
	"ClientOverrides":             {},
	"CrossSeedTorrents":           {},
	"DescriptionOverride":         {},
	"DescriptionGroups":           {},
	"ImageHostOverrides":          {},
	"IgnoreDupesFor":              {},
	"Mode":                        {},
	"Options":                     {},
	"QuestionnaireAnswers":        {},
	"RuleAuthorizations":          {},
	"ScreenshotOverrides":         {},
	"TorrentOverrides":            {},
	"TorrentPath":                 {},
	"TrackerConfigOverrides":      {},
	"TrackerQuestionnaireAnswers": {},
	"TrackerRuleFailures":         {},
	"Trackers":                    {},
	"TrackersRemove":              {},
	"TrackerSiteOverrides":        {},
}

var hostActivationFunctions = map[string]struct{}{
	"applyConfig":            {},
	"buildAndInstallRuntime": {},
	"saveAndApplyConfig":     {},
}

// CheckRepository scans architecture-sensitive source without following generated,
// dependency, build-output, or VCS directories. It returns violations sorted by
// file, line, then column.
func CheckRepository(root string) ([]Violation, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	var violations []Violation
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("architecture policy: resolve relative path %q: %w", path, err)
		}
		relative = filepath.ToSlash(relative)
		var found []Violation
		switch filepath.Ext(path) {
		case ".go":
			if strings.HasSuffix(path, ".generated.go") {
				return nil
			}
			found, err = checkGoFile(path, relative)
		case ".ts", ".tsx":
			found, err = checkFrontendFile(path, relative)
		default:
			return nil
		}
		if err != nil {
			return err
		}
		violations = append(violations, found...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan repository: %w", err)
	}
	slices.SortFunc(violations, func(left, right Violation) int {
		if result := strings.Compare(left.File, right.File); result != 0 {
			return result
		}
		if left.Line != right.Line {
			return left.Line - right.Line
		}
		return left.Column - right.Column
	})
	return violations, nil
}

func ignoredDirectory(name string) bool {
	switch name {
	case ".git", ".gocache", "dist", "node_modules", "tmp", "vendor":
		return true
	default:
		return false
	}
}

// checkGoFile enforces ownership boundaries that require Go syntax and type-name inspection.
func checkGoFile(path string, relative string) ([]Violation, error) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relative, err)
	}
	var violations []Violation
	add := func(position token.Pos, message string) {
		location := files.Position(position)
		violations = append(violations, Violation{
			File:    relative,
			Line:    location.Line,
			Column:  location.Column,
			Message: message,
		})
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		if strings.HasSuffix(relative, "_test.go") {
			return true
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "SearchPathedTorrents" &&
				!strings.HasPrefix(relative, "internal/clientdiscovery/") {
				add(selector.Sel.Pos(), "production torrent-client search belongs to internal/clientdiscovery")
			}
		}
		if selector, ok := node.(*ast.SelectorExpr); ok && strings.HasPrefix(relative, "internal/preparedrelease/") &&
			selectedTypeName(selector.X) == "api" && selector.Sel.Name == "InteractionModeUnattended" {
			add(selector.Pos(), "canonical preparation must preserve caller interaction mode")
		}
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		typeName := selectedTypeName(literal.Type)
		switch {
		case strings.HasPrefix(relative, "internal/preparedrelease/") && typeName == "Request" && selectedPackageName(literal.Type) == "api":
			add(literal.Pos(), "canonical preparation cannot reconstruct broad api.Request values")
		case (typeName == "PreparedReleaseDisplay" || typeName == "ProviderDisplay") &&
			!strings.HasPrefix(relative, "internal/preparedrelease/"):
			add(literal.Pos(), "prepared-release display construction belongs to internal/preparedrelease")
		case typeName == "TrackerEligibility" &&
			!strings.HasPrefix(relative, "internal/core/") &&
			!strings.HasPrefix(relative, "pkg/api/"):
			add(literal.Pos(), "tracker eligibility construction belongs to internal/core")
		}
		return true
	})

	for _, declaration := range parsed.Decls {
		switch typed := declaration.(type) {
		case *ast.FuncDecl:
			if outsideRuntimeOwner(relative) {
				if _, forbidden := hostActivationFunctions[typed.Name.Name]; forbidden {
					add(typed.Name.Pos(), "runtime activation sequencing belongs to webserver.RuntimeActivator")
				}
			}
		case *ast.GenDecl:
			for _, specification := range typed.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if interfaceType, ok := typeSpec.Type.(*ast.InterfaceType); ok && workflowInterface(typeSpec.Name.Name) {
					checkWorkflowInterface(interfaceType, add)
				}
				if relative == "pkg/api/prepared_release.go" {
					checkCanonicalMaps(typeSpec.Type, add)
					if typeSpec.Name.Name == "NamingFacts" {
						checkCanonicalNamingFacts(typeSpec.Type, add)
					}
				}
				if strings.HasPrefix(relative, "pkg/api/") {
					checkSingleSourceContract(typeSpec.Name.Name, typeSpec.Type, add)
				}
				if strings.HasPrefix(relative, "internal/preparedrelease/") && (typeSpec.Name.Name == "Seed" || typeSpec.Name.Name == "envelope") {
					checkPreparedState(typeSpec.Type, add)
				}
				if relative == "internal/preparedrelease/state/state.go" && typeSpec.Name.Name == "State" {
					checkPreparedState(typeSpec.Type, add)
				}
			}
		}
	}
	return violations, nil
}

// selectedPackageName returns the qualifier from a direct package selector expression.
func selectedPackageName(value ast.Expr) string {
	selector, ok := value.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}

func selectedTypeName(value ast.Expr) string {
	switch typed := value.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

// checkSingleSourceContract rejects fields that would let canonical operation
// contracts represent multiple sources or carry presentation correlation.
func checkSingleSourceContract(typeName string, value ast.Expr, add func(token.Pos, string)) {
	var forbidden map[string]struct{}
	switch typeName {
	case "Request":
		forbidden = map[string]struct{}{
			"Paths":                    {},
			"Mode":                     {},
			"ExternalIDSelections":     {},
			"PlaylistSelections":       {},
			"PlaylistSelectionsUseAll": {},
		}
	case "PrepareInput":
		forbidden = map[string]struct{}{"AdditionalPaths": {}, "CorrelationID": {}}
	default:
		return
	}
	structure, ok := value.(*ast.StructType)
	if !ok {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if _, found := forbidden[name.Name]; found {
				if name.Name == "CorrelationID" {
					add(name.Pos(), "canonical preparation input cannot contain operation presentation correlation")
					continue
				}
				add(name.Pos(), "canonical release operation contracts are single-source: "+typeName+"."+name.Name)
			}
		}
	}
}

// checkFrontendFile enforces release-session ownership and rejects invalid
// progress, Job, mutation, and BDMV path-derivation patterns.
func checkFrontendFile(path string, relative string) ([]Violation, error) {
	if !strings.HasPrefix(relative, "webui/src/") || strings.HasSuffix(relative, ".d.ts") {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relative, err)
	}
	text := string(content)
	var violations []Violation
	add := func(offset int, message string) {
		line, column := textPosition(text, offset)
		violations = append(violations, Violation{
			File:    relative,
			Line:    line,
			Column:  column,
			Message: message,
		})
	}
	if isReleasePage(relative) {
		for offset, line := range sourceLines(text) {
			if strings.Contains(line, " from ") && strings.Contains(line, "/api/") {
				add(offset, "release pages must use a release-session facet, not production API clients")
			}
		}
	}
	if relative == "webui/src/releaseSession/types.ts" {
		for _, marker := range []string{"Dispatch<", "SetStateAction", "MutableRefObject", "RefObject", "dispatch:"} {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "release-session facets cannot expose React mutation primitives: "+marker)
			}
		}
		if match := preparationIntentTrackersPattern.FindStringIndex(text); match != nil {
			add(match[0], "frontend PreparationIntent cannot contain workflow tracker selection")
		}
	}
	if !strings.HasPrefix(relative, "webui/src/releaseSession/") {
		if offset := strings.Index(text, "preparation:progress"); offset >= 0 {
			add(offset, "preparation progress subscription belongs to webui/src/releaseSession")
		}
	}
	if !strings.HasPrefix(relative, "webui/src/jobRegistry/") && relative != "webui/src/api/app.ts" {
		for _, marker := range []string{"jobsClient", "jobs:update"} {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "duplicate/upload Job coordination belongs to webui/src/jobRegistry")
			}
		}
	}
	if !strings.HasPrefix(relative, "webui/src/releaseSession/") && !strings.HasPrefix(relative, "webui/src/jobRegistry/") {
		if offset := strings.Index(text, "useReleaseJobs"); offset >= 0 {
			add(offset, "active release Job access belongs behind useReleaseSession")
		}
	}
	if strings.HasPrefix(relative, "webui/src/releaseSession/") ||
		strings.HasPrefix(relative, "webui/src/jobRegistry/") ||
		strings.HasPrefix(relative, "webui/src/api/") {
		for offset, line := range sourceLines(text) {
			if match := messageRecoveryPattern.FindStringIndex(line); match != nil {
				add(offset+match[0], "operation recovery cannot be inferred from error-message substrings")
			}
		}
	}
	for offset, line := range sourceLines(text) {
		if match := frontendBDMVPathPattern.FindStringIndex(line); match != nil {
			add(offset+match[0], "frontend cannot derive BDMV resource paths from preparation sources")
		}
	}
	return violations, nil
}

func isReleasePage(relative string) bool {
	const prefix = "webui/src/pages/"
	if !strings.HasPrefix(relative, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(relative, prefix)
	directory, _, _ := strings.Cut(remainder, "/")
	_, found := releasePageDirectories[directory]
	return found
}

func sourceLines(value string) map[int]string {
	lines := make(map[int]string)
	offset := 0
	for line := range strings.SplitSeq(value, "\n") {
		lines[offset] = line
		offset += len(line) + 1
	}
	return lines
}

func textPosition(value string, offset int) (int, int) {
	line, column := 1, 1
	for index := 0; index < offset && index < len(value); index++ {
		if value[index] == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return line, column
}

func checkCanonicalNamingFacts(value ast.Expr, add func(token.Pos, string)) {
	structure, ok := value.(*ast.StructType)
	if !ok {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if name.Name == "Category" {
				add(name.Pos(), "canonical top-level category belongs only to ExternalIdentity")
			}
		}
	}
}

func workflowInterface(name string) bool {
	for _, marker := range workflowInterfaceMarkers {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func checkWorkflowInterface(value *ast.InterfaceType, add func(token.Pos, string)) {
	for _, field := range value.Methods.List {
		function, ok := field.Type.(*ast.FuncType)
		if !ok || function.Params == nil {
			continue
		}
		for _, parameter := range function.Params.List {
			if typeNamesPreparedRelease(parameter.Type) {
				add(parameter.Pos(), "workflow interfaces must accept owner-local inputs, not PreparedRelease")
			}
		}
	}
}

func typeNamesPreparedRelease(value ast.Expr) bool {
	switch typed := value.(type) {
	case *ast.Ident:
		return typed.Name == "PreparedRelease"
	case *ast.SelectorExpr:
		return typed.Sel.Name == "PreparedRelease"
	case *ast.ArrayType:
		return typeNamesPreparedRelease(typed.Elt)
	case *ast.StarExpr:
		return typeNamesPreparedRelease(typed.X)
	default:
		return false
	}
}

func checkCanonicalMaps(value ast.Expr, add func(token.Pos, string)) {
	ast.Inspect(value, func(node ast.Node) bool {
		mapping, ok := node.(*ast.MapType)
		if !ok {
			return true
		}
		key, keyOK := mapping.Key.(*ast.Ident)
		if keyOK && key.Name == "string" && isAny(mapping.Value) {
			add(mapping.Pos(), "canonical prepared facts cannot use map[string]any")
		}
		return true
	})
}

func isAny(value ast.Expr) bool {
	if identifier, ok := value.(*ast.Ident); ok {
		return identifier.Name == "any"
	}
	interfaceType, ok := value.(*ast.InterfaceType)
	return ok && (interfaceType.Methods == nil || len(interfaceType.Methods.List) == 0)
}

func checkPreparedState(value ast.Expr, add func(token.Pos, string)) {
	structure, ok := value.(*ast.StructType)
	if !ok {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if _, forbidden := preparedStateFields[name.Name]; forbidden {
				add(name.Pos(), "prepared release collection and transfer state cannot retain workflow state: "+name.Name)
			}
		}
	}
}

func outsideRuntimeOwner(relative string) bool {
	return !strings.HasPrefix(relative, "internal/webserver/")
}
