package logpolicy

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var disallowedStdlibCalls = map[string]map[string]struct{}{
	"fmt": {
		"Print":   {},
		"Printf":  {},
		"Println": {},
	},
	"log": {
		"Fatal":   {},
		"Fatalf":  {},
		"Fatalln": {},
		"Panic":   {},
		"Panicf":  {},
		"Panicln": {},
		"Print":   {},
		"Printf":  {},
		"Println": {},
	},
}

var loggerMethods = map[string]struct{}{
	"Tracef": {},
	"Debugf": {},
	"Infof":  {},
	"Warnf":  {},
	"Errorf": {},
}

var bareFormats = map[string]struct{}{
	"%v":  {},
	"%+v": {},
	"%s":  {},
	"%q":  {},
}

const maxInfoFormatLength = 180

var infoErrorOnlyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\berror\b`),
	regexp.MustCompile(`\bfailed\b`),
	regexp.MustCompile(`\bfailure\b`),
	regexp.MustCompile(`\bfatal\b`),
	regexp.MustCompile(`\bpanic\b`),
	regexp.MustCompile(`\bexception\b`),
	regexp.MustCompile(`\btimed out\b`),
	regexp.MustCompile(`\btimeout\b`),
	regexp.MustCompile(`\bunable to\b`),
	regexp.MustCompile(`\bcannot\b`),
	regexp.MustCompile(`\bcan't\b`),
	regexp.MustCompile(`\bdenied\b`),
	regexp.MustCompile(`\brejected\b`),
}

var infoErrorExemptions = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:no|without)\s+errors?\b(?:$|[\s,.;:!?])`),
	regexp.MustCompile(`\berror\s+(?:rate|rates|budget|budgets|count|counts|code|codes)\b`),
	regexp.MustCompile(`\bskipped\b.*\bdue to\b`),
}

var debugExecutionFlowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bpart=\w+\b`),
	regexp.MustCompile(`\b(?:loaded|resolved|applied)\b.*\b(?:len|bytes|count|size)=\d+`),
	regexp.MustCompile(`\bstart\s+(?:tracker|source)=`),
	regexp.MustCompile(`\b(?:total|filtered|resolved|slots)=\d+`),
	regexp.MustCompile(`\b(?:tracker|source|desc_len|screenshots|count)=.+\s+(?:tracker|source|desc_len|screenshots|count)=`),
	regexp.MustCompile(`\bpathed search clients=`),
	regexp.MustCompile(`\bpathed search running for client\b`),
	regexp.MustCompile(`\bsearching qbittorrent client\b`),
	regexp.MustCompile(`\bsearching via qbittorrent\s+(?:proxy|webapi)\b`),
	regexp.MustCompile(`\bfetched\s+(?:\d+|%d)\s+torrents\b`),
	regexp.MustCompile(`\bname-matched\s+(?:\d+|%d)\s+of\s+(?:\d+|%d)\s+torrents\b`),
	regexp.MustCompile(`\bmatched\s+(?:\d+|%d)\s+torrents\b`),
	regexp.MustCompile(`\bselected hash\b.*\bpreferred=`),
	regexp.MustCompile(`\bvalidated exported torrent for\b.*\bpiece=`),
	regexp.MustCompile(`\bpathed search client\b.*\bresults matches=`),
	regexp.MustCompile(`\bstopping pathed search after\b`),
}

var infoExecutionFlowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:metadata|info|series metadata)\s+loaded\b.*\b(?:id|series_id|title|name)=`),
	regexp.MustCompile(`\bsearch selected\b.*\b(?:id|imdb|tvdb|candidates)=`),
	regexp.MustCompile(`\bcache hit\b.*\b(?:id|series_id|episodes)=`),
	regexp.MustCompile(`\bepisode lookup\b.*\b(?:id|season|episode|series)=`),
}

var infoShouldBeDebugPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\btrackers:\s+preparation built description for\b`),
	regexp.MustCompile(`\bimage hosting:\s+starting batch upload to\b`),
	regexp.MustCompile(`\bmetadata:\s+btn claim window expired\b`),
	regexp.MustCompile(`\bmediainfo:\s+analyzing\b`),
	regexp.MustCompile(`\bclients:\s+no default search client set; searching all qbittorrent clients\b`),
}

var debugErrorOrientedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bsearch failed\b.*\bstatus=(?:\d+|%d)\b`),
}

var infoRoutineCheckPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bchecked for\b.*\braw=(?:\d+|%[dt])\s+filtered=(?:\d+|%[dt])\s+dupes=`),
}

var infoVerboseSignals = []string{
	"response body",
	"request body",
	"payload",
	"headers",
	"stack trace",
	"traceback",
}

type Violation struct {
	File    string
	Line    int
	Column  int
	Message string
}

func CheckRepository(root string) ([]Violation, error) {
	internalRoot := filepath.Join(root, "internal")
	if _, err := os.Stat(internalRoot); err != nil {
		return nil, fmt.Errorf("logpolicy: stat internal root: %w", err)
	}

	violations := make([]Violation, 0)
	fset := token.NewFileSet()
	err := filepath.WalkDir(internalRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileViolations, err := checkFile(fset, root, path)
		if err != nil {
			return err
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("logpolicy: walk repository: %w", err)
	}

	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		if violations[i].Line != violations[j].Line {
			return violations[i].Line < violations[j].Line
		}
		return violations[i].Column < violations[j].Column
	})

	return violations, nil
}

func checkFile(fset *token.FileSet, root string, path string) ([]Violation, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	aliases := importAliases(file)
	sanitizedVars := collectSanitizedVars(file)
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	violations := make([]Violation, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if packageName, ok := selector.X.(*ast.Ident); ok {
			importPath := aliases[packageName.Name]
			if methods, found := disallowedStdlibCalls[importPath]; found {
				if _, banned := methods[selector.Sel.Name]; banned {
					violations = append(violations, violationAt(fset, relPath, selector.Sel.Pos(), fmt.Sprintf("use the project logger instead of %s.%s in internal packages", packageName.Name, selector.Sel.Name)))
				}
			}
			if importPath != "" {
				return true
			}
		}

		if _, ok := loggerMethods[selector.Sel.Name]; !ok {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}

		firstArg, ok := call.Args[0].(*ast.BasicLit)
		if !ok || firstArg.Kind != token.STRING {
			return true
		}

		format, err := strconv.Unquote(firstArg.Value)
		if err != nil {
			return true
		}
		trimmed := strings.TrimSpace(format)
		lowerFormat := strings.ToLower(trimmed)
		if _, bare := bareFormats[trimmed]; bare {
			violations = append(violations, violationAt(fset, relPath, firstArg.Pos(), selector.Sel.Name+" must include contextual text instead of logging a bare format string"))
		}
		if selector.Sel.Name == "Infof" {
			for _, message := range infoLevelHygieneViolations(lowerFormat, trimmed) {
				violations = append(violations, violationAt(fset, relPath, firstArg.Pos(), message))
			}
		}
		if selector.Sel.Name == "Debugf" {
			for _, message := range debugLevelHygieneViolations(lowerFormat) {
				violations = append(violations, violationAt(fset, relPath, firstArg.Pos(), message))
			}
		}
		if strings.Contains(lowerFormat, "response body") {
			for _, arg := range call.Args[1:] {
				if isUnsafeBodyLikeExpr(arg, sanitizedVars) {
					violations = append(violations, violationAt(fset, relPath, arg.Pos(), "response body log arguments must be redacted before logging"))
				}
			}
		}
		for _, arg := range call.Args[1:] {
			if isUnsafeUsernameLikeExpr(arg, sanitizedVars) {
				violations = append(violations, violationAt(fset, relPath, arg.Pos(), "username log arguments must be redacted before logging"))
			}
		}
		if isAuthSensitiveFormat(lowerFormat) {
			for _, arg := range call.Args[1:] {
				if isRawErrorLikeExpr(arg) {
					violations = append(violations, violationAt(fset, relPath, arg.Pos(), "auth-sensitive log arguments must not include raw errors; log a stable incident code and operator-safe context instead"))
				}
			}
		}

		return true
	})

	return violations, nil
}

func collectSanitizedVars(file *ast.File) map[string]struct{} {
	result := make(map[string]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.AssignStmt:
			redacted := redactedExprIndexes(typed.Rhs)
			for index, lhs := range typed.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name == "_" {
					continue
				}
				rhsIndex := index
				if len(typed.Rhs) == 1 {
					rhsIndex = 0
				}
				if rhsIndex >= len(typed.Rhs) {
					continue
				}
				if _, ok := redacted[rhsIndex]; ok {
					result[ident.Name] = struct{}{}
				}
			}
		case *ast.ValueSpec:
			redacted := redactedExprIndexes(typed.Values)
			for index, name := range typed.Names {
				if name == nil || name.Name == "_" || index >= len(typed.Values) {
					continue
				}
				if _, ok := redacted[index]; ok {
					result[name.Name] = struct{}{}
				}
			}
		}
		return true
	})
	return result
}

func redactedExprIndexes(exprs []ast.Expr) map[int]struct{} {
	var result map[int]struct{}
	for index, expr := range exprs {
		if expr == nil {
			continue
		}
		ast.Inspect(expr, func(node ast.Node) bool {
			if isRedactionCall(node) {
				if result == nil {
					result = make(map[int]struct{})
				}
				result[index] = struct{}{}
				return false
			}
			return true
		})
	}
	return result
}

func containsRedactionCall(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if isRedactionCall(node) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isRedactionCall(node ast.Node) bool {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "redaction" {
		return false
	}
	return selector.Sel.Name == "RedactValue" || selector.Sel.Name == "RedactPrivateInfo"
}

func isUnsafeBodyLikeExpr(expr ast.Expr, sanitizedVars map[string]struct{}) bool {
	if containsRedactionCall(expr) {
		return false
	}
	if isRawBodyStringConversion(expr) {
		return true
	}
	unsafe := false
	ast.Inspect(expr, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		if _, safe := sanitizedVars[ident.Name]; safe {
			return false
		}
		if isSuspiciousBodyName(ident.Name) {
			unsafe = true
			return false
		}
		return true
	})
	return unsafe
}

func isUnsafeUsernameLikeExpr(expr ast.Expr, sanitizedVars map[string]struct{}) bool {
	if containsRedactionCall(expr) {
		return false
	}
	if isExplicitlySanitizedExpr(expr) {
		return false
	}
	unsafe := false
	ast.Inspect(expr, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			if _, safe := sanitizedVars[typed.Name]; safe {
				return false
			}
			if isSensitiveUsernameName(typed.Name) {
				unsafe = true
				return false
			}
		case *ast.SelectorExpr:
			if isSensitiveUsernameName(typed.Sel.Name) {
				unsafe = true
				return false
			}
		}
		return true
	})
	return unsafe
}

func isRawErrorLikeExpr(expr ast.Expr) bool {
	if isExplicitlySanitizedExpr(expr) {
		return false
	}
	raw := false
	ast.Inspect(expr, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			if isErrorLikeName(typed.Name) {
				raw = true
				return false
			}
		case *ast.SelectorExpr:
			if isErrorLikeName(typed.Sel.Name) {
				raw = true
				return false
			}
		case *ast.CallExpr:
			if selector, ok := typed.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Error" {
				raw = true
				return false
			}
		}
		return true
	})
	return raw
}

func isExplicitlySanitizedExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(fun.Name)), "redact")
	case *ast.SelectorExpr:
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(fun.Sel.Name)), "redact")
	default:
		return false
	}
}

func importAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		pathValue, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(pathValue)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = pathValue
	}
	return aliases
}

func violationAt(fset *token.FileSet, file string, pos token.Pos, message string) Violation {
	position := fset.Position(pos)
	return Violation{
		File:    file,
		Line:    position.Line,
		Column:  position.Column,
		Message: message,
	}
}

func isRawBodyStringConversion(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		pkg, ok := selector.X.(*ast.Ident)
		if ok && pkg.Name == "strings" && selector.Sel.Name == "TrimSpace" && len(call.Args) == 1 {
			return isRawBodyStringConversion(call.Args[0])
		}
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "string" || len(call.Args) != 1 {
		return false
	}
	bodyIdent, ok := call.Args[0].(*ast.Ident)
	if !ok {
		return false
	}
	return isSuspiciousBodyName(bodyIdent.Name)
}

func isSuspiciousBodyName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "body" || lower == "payload" || strings.HasSuffix(lower, "body") || strings.HasSuffix(lower, "payload") || strings.Contains(lower, "bodystr") || strings.Contains(lower, "bodypreview")
}

func isSensitiveUsernameName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "username" || strings.HasSuffix(lower, "username") || strings.Contains(lower, ".username")
}

func isErrorLikeName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "err" || lower == "error" || strings.HasSuffix(lower, "err") || strings.HasSuffix(lower, "error")
}

func isAuthSensitiveFormat(lowerFormat string) bool {
	authSignals := []string{
		"auth upgrade",
		"login upgrade",
		"credential",
		"refresh credentials",
		"protected data rewrap",
		"pending auth upgrade",
	}
	for _, signal := range authSignals {
		if strings.Contains(lowerFormat, signal) {
			return true
		}
	}
	return false
}

func infoLevelHygieneViolations(lowerFormat string, trimmedFormat string) []string {
	violations := make([]string, 0, 5)

	if isErrorOnlyInfoMessage(lowerFormat) {
		violations = append(violations, "Infof appears error-oriented; use Warnf/Errorf or rephrase for progress/outcome context")
	}

	if isOverlyVerboseInfoMessage(lowerFormat, trimmedFormat) {
		violations = append(violations, "Infof appears overly verbose; move detailed diagnostics to Debugf/Tracef")
	}

	if isExecutionFlowInfoMessage(lowerFormat) {
		violations = append(violations, "Infof appears to be execution flow reporting; use Tracef for granular step-by-step logging")
	}

	if isRoutineCheckInfoMessage(lowerFormat) {
		violations = append(violations, "Infof appears to be a routine check result; use Debugf for troubleshooting details")
	}

	if isInfoTroubleshootingMessage(lowerFormat) {
		violations = append(violations, "Infof appears to be troubleshooting detail; use Debugf for non-user-facing progress")
	}

	return violations
}

func isErrorOnlyInfoMessage(lowerFormat string) bool {
	for _, exemption := range infoErrorExemptions {
		if exemption.MatchString(lowerFormat) {
			return false
		}
	}
	for _, pattern := range infoErrorOnlyPatterns {
		if pattern.MatchString(lowerFormat) {
			return true
		}
	}
	return false
}

func isOverlyVerboseInfoMessage(lowerFormat string, trimmedFormat string) bool {
	if len(trimmedFormat) > maxInfoFormatLength {
		return true
	}
	for _, signal := range infoVerboseSignals {
		if strings.Contains(lowerFormat, signal) {
			return true
		}
	}
	return false
}

func debugLevelHygieneViolations(lowerFormat string) []string {
	violations := make([]string, 0, 2)

	if isExecutionFlowDebugMessage(lowerFormat) {
		violations = append(violations, "Debugf appears to be execution flow reporting; use Tracef for granular step-by-step logging")
	}

	if isErrorOrientedDebugMessage(lowerFormat) {
		violations = append(violations, "Debugf appears error-oriented; use Warnf/Errorf for failure conditions")
	}

	return violations
}

func isExecutionFlowDebugMessage(lowerFormat string) bool {
	for _, pattern := range debugExecutionFlowPatterns {
		if pattern.MatchString(lowerFormat) {
			return true
		}
	}
	return false
}

func isExecutionFlowInfoMessage(lowerFormat string) bool {
	for _, pattern := range infoExecutionFlowPatterns {
		if pattern.MatchString(lowerFormat) {
			return true
		}
	}
	return false
}

func isRoutineCheckInfoMessage(lowerFormat string) bool {
	for _, pattern := range infoRoutineCheckPatterns {
		if pattern.MatchString(lowerFormat) {
			return true
		}
	}
	return false
}

func isInfoTroubleshootingMessage(lowerFormat string) bool {
	for _, pattern := range infoShouldBeDebugPatterns {
		if pattern.MatchString(lowerFormat) {
			return true
		}
	}
	return false
}

func isErrorOrientedDebugMessage(lowerFormat string) bool {
	for _, pattern := range debugErrorOrientedPatterns {
		if pattern.MatchString(lowerFormat) {
			return true
		}
	}
	return false
}
