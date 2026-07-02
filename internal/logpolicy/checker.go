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

const (
	maxInfoFormatLength            = 180
	workflowLogScoreThreshold      = 4
	enableWorkflowBranchErrorCheck = true
	enableWorkflowDecisionCheck    = true
	enableWorkflowStableFieldCheck = true
)

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
	regexp.MustCompile(`\bblocked\b`),
	regexp.MustCompile(`\baborted\b`),
	regexp.MustCompile(`\bunavailable\b`),
	regexp.MustCompile(`\bnot ready\b`),
	regexp.MustCompile(`\brequires?\b`),
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

var warnRoutineProgressPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bcompleted\b`),
	regexp.MustCompile(`\bstarted\b`),
	regexp.MustCompile(`\bselected\b`),
	regexp.MustCompile(`\bloaded\b`),
	regexp.MustCompile(`\bresolved\b`),
	regexp.MustCompile(`\bvalidated\b`),
	regexp.MustCompile(`\bsaved\b`),
	regexp.MustCompile(`\busing\b`),
	regexp.MustCompile(`\bfound\b`),
}

var warnAttentionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\berror\b`),
	regexp.MustCompile(`\bfailed\b`),
	regexp.MustCompile(`\bfailures?\b`),
	regexp.MustCompile(`\bblocked\b`),
	regexp.MustCompile(`\baborted\b`),
	regexp.MustCompile(`\brejected\b`),
	regexp.MustCompile(`\bdenied\b`),
	regexp.MustCompile(`\bunavailable\b`),
	regexp.MustCompile(`\bnot ready\b`),
	regexp.MustCompile(`\brequires?\b`),
	regexp.MustCompile(`\bskipp?ed\b`),
	regexp.MustCompile(`\bmatch found\b`),
	regexp.MustCompile(`\bno ready\b`),
}

var traceUserOutcomePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bupload completed\b`),
	regexp.MustCompile(`\baccepted\b`),
	regexp.MustCompile(`\baborted\b`),
	regexp.MustCompile(`\bfailed\b`),
	regexp.MustCompile(`\bblocked\b`),
	regexp.MustCompile(`\bready\b`),
	regexp.MustCompile(`\bsaved config\b`),
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

var rawErrorLogFieldRe = regexp.MustCompile(`(?:^|[\s,;])(?:err|error)=%`)

type Violation struct {
	File    string
	Line    int
	Column  int
	Message string
}

// CheckRepository scans repo-owned Go and frontend test sources for logging and
// shareable-output patterns that can expose secrets or unsafe diagnostics.
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
		if filepath.Ext(path) != ".go" {
			return nil
		}

		var fileViolations []Violation
		var err error
		if strings.HasSuffix(path, "_test.go") {
			fileViolations, err = checkTestFile(fset, root, path)
		} else {
			fileViolations, err = checkFile(fset, root, path)
		}
		if err != nil {
			return err
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("logpolicy: walk repository: %w", err)
	}

	loggerViolations, err := checkProjectLoggerPathSanitization(fset, root)
	if err != nil {
		return nil, err
	}
	violations = append(violations, loggerViolations...)

	cmdRoot := filepath.Join(root, "cmd", "upbrr")
	if _, err := os.Stat(cmdRoot); err == nil {
		err = filepath.WalkDir(cmdRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}

			var fileViolations []Violation
			var err error
			if strings.HasSuffix(path, "_test.go") {
				fileViolations, err = checkTestFile(fset, root, path)
			} else {
				fileViolations, err = checkCLISensitiveOutputFile(fset, root, path)
			}
			if err != nil {
				return err
			}
			violations = append(violations, fileViolations...)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("logpolicy: walk cmd/upbrr: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("logpolicy: stat cmd/upbrr root: %w", err)
	}

	guiRoot := filepath.Join(root, "gui")
	if _, err := os.Stat(guiRoot); err == nil {
		err = filepath.WalkDir(guiRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "frontend", "build":
					return filepath.SkipDir
				default:
					return nil
				}
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fileViolations, err := checkTerminalSensitiveOutputFile(fset, root, path)
			if err != nil {
				return err
			}
			violations = append(violations, fileViolations...)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("logpolicy: walk gui: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("logpolicy: stat gui root: %w", err)
	}

	frontendViolations, err := checkFrontendTestSensitiveMatchers(root)
	if err != nil {
		return nil, err
	}
	violations = append(violations, frontendViolations...)

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

// checkProjectLoggerPathSanitization guards the central logger path sanitizer
// that protects all project logger output, including internal Debugf/Infof/etc.
func checkProjectLoggerPathSanitization(fset *token.FileSet, root string) ([]Violation, error) {
	path := filepath.Join(root, "internal", "logging", "logger.go")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("logpolicy: stat logger: %w", err)
	}

	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "logf" || fn.Body == nil {
			continue
		}
		if logfFormatsThroughSanitizeMessage(fn.Body) {
			return nil, nil
		}
		return []Violation{violationAt(fset, relPath, fn.Name.Pos(), "project logger logf must sanitize formatted messages with SanitizeMessage before output")}, nil
	}

	return []Violation{violationAt(fset, relPath, file.Package, "project logger logf not found; logger output path sanitization cannot be verified")}, nil
}

func logfFormatsThroughSanitizeMessage(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		switch typed := node.(type) {
		case *ast.AssignStmt:
			for index, lhs := range typed.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name != "formatted" {
					continue
				}
				rhsIndex := index
				if len(typed.Rhs) == 1 {
					rhsIndex = 0
				}
				if rhsIndex < len(typed.Rhs) && isSanitizeMessageSprintfCall(typed.Rhs[rhsIndex]) {
					found = true
					return false
				}
			}
		case *ast.ValueSpec:
			for index, name := range typed.Names {
				if name == nil || name.Name != "formatted" || index >= len(typed.Values) {
					continue
				}
				if isSanitizeMessageSprintfCall(typed.Values[index]) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

func isSanitizeMessageSprintfCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "SanitizeMessage" {
		return false
	}
	return containsFmtSprintfCall(call.Args[0])
}

func containsFmtSprintfCall(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Sprintf" {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if ok && pkg.Name == "fmt" {
			found = true
			return false
		}
		return true
	})
	return found
}

var (
	// Frontend secret-output patterns scan complete TS/TSX test files so
	// typed declarations, multiline matchers, and JSX attributes are covered.
	frontendEncryptedEnvelopeDeclRe = regexp.MustCompile("\\b(?:const|let|var)\\s+([A-Za-z_$][\\w$]*)\\s*(?::[^=;]+)?=\\s*[\"'`]upbrr-enc:")
	frontendDirectEnvelopeMatcherRe = regexp.MustCompile("\\.(?:toBe|toEqual|toStrictEqual|toContain|toMatch)\\s*\\(\\s*(?:[\"'`]upbrr-enc:|([A-Za-z_$][\\w$]*))")
	frontendRawPayloadDOMRe         = regexp.MustCompile("(?s)(?:[\"']data-testid[\"']\\s*:\\s*[\"']payload[\"'][^;]*buildSavePayload\\s*\\(\\)|buildSavePayload\\s*\\(\\)[^;]*[\"']data-testid[\"']\\s*:\\s*[\"']payload[\"']|data-testid\\s*=\\s*(?:[\"']payload[\"']|\\{\\s*[\"']payload[\"']\\s*\\})[^;]*buildSavePayload\\s*\\(\\)|buildSavePayload\\s*\\(\\)[^;]*data-testid\\s*=\\s*(?:[\"']payload[\"']|\\{\\s*[\"']payload[\"']\\s*\\}))")
)

// checkFrontendTestSensitiveMatchers scans frontend tests for assertions and DOM
// fixtures that would print encrypted envelopes or save payloads in failure output.
func checkFrontendTestSensitiveMatchers(root string) ([]Violation, error) {
	frontendRoot := filepath.Join(root, "gui", "frontend", "src")
	if _, err := os.Stat(frontendRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("logpolicy: stat frontend root: %w", err)
	}

	violations := make([]Violation, 0)
	err := filepath.WalkDir(frontendRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".test.ts") && !strings.HasSuffix(path, ".test.tsx") {
			return nil
		}

		fileViolations, err := checkFrontendTestSensitiveMatcherFile(root, path)
		if err != nil {
			return err
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("logpolicy: walk frontend tests: %w", err)
	}
	return violations, nil
}

// checkFrontendTestSensitiveMatcherFile applies the frontend secret-output regex
// checks to one full test file and reports source-positioned violations.
func checkFrontendTestSensitiveMatcherFile(root string, path string) ([]Violation, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	text := string(content)
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	encryptedNames := make(map[string]struct{})
	for _, match := range frontendEncryptedEnvelopeDeclRe.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			encryptedNames[match[1]] = struct{}{}
		}
	}

	violations := make([]Violation, 0)
	for _, match := range frontendDirectEnvelopeMatcherRe.FindAllStringSubmatchIndex(text, -1) {
		if match[2] >= 0 {
			name := text[match[2]:match[3]]
			if _, ok := encryptedNames[name]; !ok {
				continue
			}
		}
		line, column := lineColumnForOffset(content, match[0])
		violations = append(violations, Violation{
			File:    relPath,
			Line:    line,
			Column:  column,
			Message: "frontend test assertions must not print encrypted envelope values; assert a boolean predicate or use static sanitized failure text",
		})
	}
	for _, match := range frontendRawPayloadDOMRe.FindAllStringIndex(text, -1) {
		line, column := lineColumnForOffset(content, match[0])
		violations = append(violations, Violation{
			File:    relPath,
			Line:    line,
			Column:  column,
			Message: "frontend tests must not render raw save payloads into the DOM; Testing Library failures can dump secret payload content",
		})
	}
	return violations, nil
}

// lineColumnForOffset converts a byte offset in scanner input into one-based
// line and column values for diagnostics.
func lineColumnForOffset(content []byte, offset int) (int, int) {
	line, column := 1, 1
	for index, value := range content {
		if index >= offset {
			break
		}
		if value == '\n' {
			line++
			column = 1
			continue
		}
		column++
	}
	return line, column
}

// checkCLISensitiveOutputFile flags raw dry-run endpoint and payload printing in
// cmd/upbrr, where stdout becomes user-shareable debug log material.
func checkCLISensitiveOutputFile(fset *token.FileSet, root string, path string) ([]Violation, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	aliases := importAliases(file)
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	dryRunFileVars := collectDryRunFileVars(file)
	localPathVars := collectLocalPathVars(file, aliases)
	violations := make([]Violation, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !isFmtPrintSelector(selector, aliases) {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		format := cliOutputFormat(call)
		if isTerminalStderrOutputCall(call, aliases) {
			for _, arg := range fmtOutputArgs(call) {
				if isUnsafeTerminalDiagnosticArg(format, arg, aliases) {
					violations = append(violations, violationAt(fset, relPath, arg.Pos(), "terminal error/warning output must be sanitized before printing"))
				}
			}
		}
		for _, arg := range fmtOutputArgs(call) {
			if isDryRunPayloadValueExpr(arg) && !isSafeDryRunOutputExpr(arg) {
				violations = append(violations, violationAt(fset, relPath, arg.Pos(), "dry-run payload output must be redacted before printing"))
			}
			if containsDryRunFilePathExpr(arg, dryRunFileVars) && !isSafeDryRunOutputExpr(arg) {
				violations = append(violations, violationAt(fset, relPath, arg.Pos(), "dry-run file path output must be reduced to a DB-relative or basename label before printing"))
			}
			if containsLocalPathOutputExpr(arg, localPathVars, aliases) && !isSafeLocalPathOutputExpr(arg) {
				violations = append(violations, violationAt(fset, relPath, arg.Pos(), "local filesystem path output must be reduced to a stable path label before printing"))
			}
		}
		if strings.Contains(strings.ToLower(format), "endpoint:") {
			for _, arg := range fmtOutputArgs(call) {
				if !isSafeDryRunOutputExpr(arg) && containsEndpointExpr(arg) {
					violations = append(violations, violationAt(fset, relPath, arg.Pos(), "dry-run endpoint output must be redacted before printing"))
				}
			}
		}
		return true
	})
	return violations, nil
}

// checkTerminalSensitiveOutputFile scans command-style entrypoints for terminal
// diagnostics that can expose raw errors, warnings, or unsafe path text.
func checkTerminalSensitiveOutputFile(fset *token.FileSet, root string, path string) ([]Violation, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	aliases := importAliases(file)
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	violations := make([]Violation, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isTerminalStderrOutputCall(call, aliases) {
			return true
		}
		format := cliOutputFormat(call)
		for _, arg := range fmtOutputArgs(call) {
			if isUnsafeTerminalDiagnosticArg(format, arg, aliases) {
				violations = append(violations, violationAt(fset, relPath, arg.Pos(), "terminal error/warning output must be sanitized before printing"))
			}
		}
		return true
	})
	return violations, nil
}

// collectDryRunFileVars tracks range variables bound to dry-run file entries so
// file.Path output can be required to pass through a path label formatter.
func collectDryRunFileVars(file *ast.File) map[string]struct{} {
	vars := make(map[string]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		stmt, ok := node.(*ast.RangeStmt)
		if !ok {
			return true
		}
		selector, ok := stmt.X.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Files" {
			return true
		}
		value, ok := stmt.Value.(*ast.Ident)
		if !ok || value.Name == "_" {
			return true
		}
		vars[value.Name] = struct{}{}
		return true
	})
	return vars
}

// collectLocalPathVars tracks identifiers that likely contain host filesystem
// paths, including values derived from filepath and app DB path helpers.
func collectLocalPathVars(file *ast.File, aliases map[string]string) map[string]struct{} {
	vars := make(map[string]struct{})
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncDecl:
			if typed.Type == nil || typed.Type.Params == nil {
				return true
			}
			for _, field := range typed.Type.Params.List {
				for _, name := range field.Names {
					if isLocalPathIdentName(name.Name) {
						vars[name.Name] = struct{}{}
					}
				}
			}
		case *ast.AssignStmt:
			for index, lhs := range typed.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || ident.Name == "_" {
					continue
				}
				if isLocalPathIdentName(ident.Name) {
					vars[ident.Name] = struct{}{}
					continue
				}
				if index < len(typed.Rhs) && isLocalPathExpr(typed.Rhs[index], vars, aliases) {
					vars[ident.Name] = struct{}{}
				}
			}
		case *ast.RangeStmt:
			if isLocalPathExpr(typed.X, vars, aliases) {
				for _, expr := range []ast.Expr{typed.Key, typed.Value} {
					ident, ok := expr.(*ast.Ident)
					if ok && ident.Name != "_" {
						vars[ident.Name] = struct{}{}
					}
				}
			}
		}
		return true
	})
	return vars
}

// checkFile enforces production Go logging policy for internal packages,
// including logger hygiene, sensitive dataflow, and bounded response-body use.
func checkFile(fset *token.FileSet, root string, path string) ([]Violation, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
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
	allows, allowViolations := collectLogpolicyAllows(fset, relPath, file)
	violations = append(violations, allowViolations...)
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
					appendLogpolicyViolation(fset, relPath, allows, &violations, selector.Sel.Pos(), fmt.Sprintf("use the project logger instead of %s.%s in internal packages", packageName.Name, selector.Sel.Name))
				}
			}
			if isTerminalStderrOutputCall(call, aliases) {
				format := cliOutputFormat(call)
				for _, arg := range fmtOutputArgs(call) {
					if isUnsafeTerminalDiagnosticArg(format, arg, aliases) {
						appendLogpolicyViolation(fset, relPath, allows, &violations, arg.Pos(), "terminal error/warning output must be sanitized before printing")
					}
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
			appendLogpolicyViolation(fset, relPath, allows, &violations, firstArg.Pos(), selector.Sel.Name+" must include contextual text instead of logging a bare format string")
		}
		if selector.Sel.Name == "Infof" {
			for _, message := range infoLevelHygieneViolations(lowerFormat, trimmed) {
				appendLogpolicyViolation(fset, relPath, allows, &violations, firstArg.Pos(), message)
			}
		}
		if selector.Sel.Name == "Debugf" {
			for _, message := range debugLevelHygieneViolations(lowerFormat) {
				appendLogpolicyViolation(fset, relPath, allows, &violations, firstArg.Pos(), message)
			}
		}
		if selector.Sel.Name == "Warnf" {
			for _, message := range warnLevelHygieneViolations(lowerFormat) {
				appendLogpolicyViolation(fset, relPath, allows, &violations, firstArg.Pos(), message)
			}
		}
		if selector.Sel.Name == "Tracef" {
			for _, message := range traceLevelHygieneViolations(lowerFormat) {
				appendLogpolicyViolation(fset, relPath, allows, &violations, firstArg.Pos(), message)
			}
		}
		if strings.Contains(lowerFormat, "response body") {
			for _, arg := range call.Args[1:] {
				if isUnsafeBodyLikeExpr(arg, sanitizedVars) {
					appendLogpolicyViolation(fset, relPath, allows, &violations, arg.Pos(), "response body log arguments must be redacted before logging")
				}
			}
		}
		if hasRawErrorLogField(lowerFormat) {
			for _, arg := range call.Args[1:] {
				if isSafeSensitiveOutputExpr(arg) {
					continue
				}
				if isRawErrorLikeExpr(arg) {
					appendLogpolicyViolation(fset, relPath, allows, &violations, arg.Pos(), "raw error log fields must be redacted before logging")
				}
			}
		}
		for _, arg := range call.Args[1:] {
			if isUnsafeUsernameLikeExpr(arg, sanitizedVars) {
				appendLogpolicyViolation(fset, relPath, allows, &violations, arg.Pos(), "username log arguments must be redacted before logging")
			}
		}
		if isAuthSensitiveFormat(lowerFormat) {
			for _, arg := range call.Args[1:] {
				if isRawErrorLikeExpr(arg) {
					appendLogpolicyViolation(fset, relPath, allows, &violations, arg.Pos(), "auth-sensitive log arguments must not include raw errors; log a stable incident code and operator-safe context instead")
				}
			}
		}

		return true
	})
	violations = append(violations, checkWorkflowLoggingCoverage(fset, relPath, file, allows)...)
	violations = append(violations, checkUnboundedResponseBodyUses(fset, relPath, file, aliases)...)

	sensitiveViolations := checkSensitiveOutputParsed(fset, relPath, file, aliases, allows, false)
	violations = append(violations, sensitiveViolations...)
	violations = append(violations, unusedLogpolicyAllowViolations(fset, relPath, allows)...)

	return violations, nil
}

// checkTestFile enforces shareable test-output policy, including secret
// dataflow and httptest handler fatal-call checks.
func checkTestFile(fset *token.FileSet, root string, path string) ([]Violation, error) {
	violations, err := checkSensitiveOutputFile(fset, root, path, true)
	if err != nil {
		return nil, err
	}
	handlerViolations, err := checkTestHandlerFatalCalls(fset, root, path)
	if err != nil {
		return nil, err
	}
	violations = append(violations, handlerViolations...)
	return violations, nil
}

type sensitiveKind string

const (
	sensitiveHTTPHeader      sensitiveKind = "http-header"
	sensitiveCookieContainer sensitiveKind = "cookie-container"
	sensitiveFormValue       sensitiveKind = "form-value"
	sensitiveQueryValue      sensitiveKind = "query-value"
	sensitiveConfigField     sensitiveKind = "config-field"
	sensitiveEndpoint        sensitiveKind = "endpoint"
	sensitivePayload         sensitiveKind = "payload"
	sensitiveBody            sensitiveKind = "body"
	sensitiveRawError        sensitiveKind = "raw-error"
	sensitiveGeneric         sensitiveKind = "generic"
)

type sensitiveValue struct {
	kind  sensitiveKind
	label string
}

type sensitiveBinding struct {
	value     sensitiveValue
	sensitive bool
}

type sensitiveModel struct {
	aliases              map[string]string
	scopes               []map[string]sensitiveBinding
	relPath              string
	testFile             bool
	testSensitiveFixture bool
}

// pushScope starts a lexical binding scope for sensitive-value dataflow.
func (m *sensitiveModel) pushScope() {
	m.scopes = append(m.scopes, make(map[string]sensitiveBinding))
}

// popScope drops the current lexical binding scope if one exists.
func (m *sensitiveModel) popScope() {
	if len(m.scopes) == 0 {
		return
	}
	m.scopes = m.scopes[:len(m.scopes)-1]
}

// currentScope returns the active lexical scope, creating one for top-level use.
func (m *sensitiveModel) currentScope() map[string]sensitiveBinding {
	if len(m.scopes) == 0 {
		m.pushScope()
	}
	return m.scopes[len(m.scopes)-1]
}

// declare records a new binding in the current lexical scope.
func (m *sensitiveModel) declare(name string, value sensitiveValue, sensitive bool) {
	m.currentScope()[name] = sensitiveBinding{value: value, sensitive: sensitive}
}

// assign updates the nearest existing binding or declares one when assignment
// targets a name not yet seen by the model.
func (m *sensitiveModel) assign(name string, value sensitiveValue, sensitive bool) {
	for i := len(m.scopes) - 1; i >= 0; i-- {
		if _, ok := m.scopes[i][name]; ok {
			m.scopes[i][name] = sensitiveBinding{value: value, sensitive: sensitive}
			return
		}
	}
	m.declare(name, value, sensitive)
}

// lookup resolves a binding from innermost to outermost scope and returns
// whether the current value is sensitive.
func (m *sensitiveModel) lookup(name string) (sensitiveValue, bool) {
	for i := len(m.scopes) - 1; i >= 0; i-- {
		binding, ok := m.scopes[i][name]
		if !ok {
			continue
		}
		return binding.value, binding.sensitive
	}
	return sensitiveValue{}, false
}

type logpolicyAllow struct {
	line   int
	pos    token.Pos
	reason string
	used   bool
}

// checkSensitiveOutputFile tracks sensitive values through one Go file and
// flags values reaching logs, returned errors, test diagnostics, or artifacts.
func checkSensitiveOutputFile(fset *token.FileSet, root string, path string, testFile bool) ([]Violation, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	allows, allowViolations := collectLogpolicyAllows(fset, relPath, file)
	violations := append([]Violation(nil), allowViolations...)
	aliases := importAliases(file)
	violations = append(violations, checkSensitiveOutputParsed(fset, relPath, file, aliases, allows, testFile)...)
	violations = append(violations, unusedLogpolicyAllowViolations(fset, relPath, allows)...)

	return violations, nil
}

// checkSensitiveOutputParsed runs the sensitive-value visitor after import
// aliases and inline allow directives have been collected for the file.
func checkSensitiveOutputParsed(fset *token.FileSet, relPath string, file *ast.File, aliases map[string]string, allows map[int]*logpolicyAllow, testFile bool) []Violation {
	violations := make([]Violation, 0)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		model := sensitiveModel{
			aliases:              aliases,
			relPath:              relPath,
			testFile:             testFile,
			testSensitiveFixture: testFile && (containsSensitiveFixtureLiteral(fn.Body) || isRedactionTestPath(relPath)),
		}
		functionContext := strings.ToLower(relPath + " " + fn.Name.Name)
		ast.Walk(&sensitiveOutputVisitor{
			fset:            fset,
			allows:          allows,
			violations:      &violations,
			model:           &model,
			testFile:        testFile,
			functionContext: functionContext,
		}, fn.Body)
	}

	return violations
}

// unusedLogpolicyAllowViolations reports suppressions that did not match an
// active finding, keeping allow comments tied to current checker output.
func unusedLogpolicyAllowViolations(fset *token.FileSet, relPath string, allows map[int]*logpolicyAllow) []Violation {
	violations := make([]Violation, 0)
	for _, allow := range allows {
		if allow.reason == "" || allow.used {
			continue
		}
		violations = append(violations, violationAt(fset, relPath, allow.pos, "unused logpolicy allow comment"))
	}

	return violations
}

type sensitiveOutputVisitor struct {
	fset            *token.FileSet
	allows          map[int]*logpolicyAllow
	violations      *[]Violation
	model           *sensitiveModel
	testFile        bool
	functionContext string
	scopeStack      []bool
}

// Visit maintains lexical scope while scanning calls and assignments for
// sensitive-value propagation.
func (v *sensitiveOutputVisitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		if len(v.scopeStack) == 0 {
			return nil
		}
		pushedScope := v.scopeStack[len(v.scopeStack)-1]
		v.scopeStack = v.scopeStack[:len(v.scopeStack)-1]
		if pushedScope {
			v.model.popScope()
		}
		return nil
	}

	pushedScope := isSensitiveScopeNode(node)
	if pushedScope {
		v.model.pushScope()
	}
	v.scopeStack = append(v.scopeStack, pushedScope)

	switch typed := node.(type) {
	case *ast.AssignStmt:
		markSensitiveAssignment(v.model, typed)
	case *ast.RangeStmt:
		markSensitiveRange(v.model, typed)
	case *ast.CallExpr:
		v.checkCall(typed)
	}
	return v
}

// checkCall reports a violation when a modeled sensitive value reaches a sink
// without a recognized sanitizing wrapper.
func (v *sensitiveOutputVisitor) checkCall(call *ast.CallExpr) {
	for _, sinkArg := range sensitiveSinkArgs(call, v.model.aliases, v.testFile, v.functionContext) {
		if isSafeSensitiveOutputExpr(sinkArg.expr) {
			continue
		}
		value, ok := sensitivityOfExpr(v.model, sinkArg.expr)
		if !ok && sinkArg.forceRawError {
			value = sensitiveValue{kind: sensitiveRawError, label: "raw error"}
			ok = true
		}
		if !ok && v.testFile && v.model.testSensitiveFixture && isTestLogBufferDumpExpr(sinkArg.expr, v.model.relPath) {
			value = sensitiveValue{kind: sensitiveGeneric, label: "test log buffer"}
			ok = true
		}
		if !ok {
			continue
		}
		if value.kind == sensitiveBody && isRawErrorLikeExpr(sinkArg.expr) {
			continue
		}
		if shouldSuppressLogpolicyViolation(v.fset, v.allows, sinkArg.expr.Pos(), value) {
			continue
		}
		*v.violations = append(*v.violations, violationAt(v.fset, v.model.relPath, sinkArg.expr.Pos(), sensitiveOutputMessage(value)))
	}
}

func isSensitiveScopeNode(node ast.Node) bool {
	switch node.(type) {
	case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
		return true
	default:
		return false
	}
}

// collectLogpolicyAllows indexes line-local allow comments and reports allows
// that omit a required reason.
func collectLogpolicyAllows(fset *token.FileSet, relPath string, file *ast.File) (map[int]*logpolicyAllow, []Violation) {
	allows := make(map[int]*logpolicyAllow)
	violations := make([]Violation, 0)
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if !strings.HasPrefix(text, "logpolicy:allow") {
				continue
			}
			reason := strings.TrimSpace(strings.TrimPrefix(text, "logpolicy:allow"))
			line := fset.Position(comment.Pos()).Line
			allows[line] = &logpolicyAllow{
				line:   line,
				pos:    comment.Pos(),
				reason: reason,
			}
			if reason == "" {
				violations = append(violations, violationAt(fset, relPath, comment.Pos(), "logpolicy allow comment must include a reason"))
			}
		}
	}
	return allows, violations
}

// shouldSuppressLogpolicyViolation consumes a matching allow comment unless the
// value is a never-allow header such as Cookie or Authorization.
func shouldSuppressLogpolicyViolation(fset *token.FileSet, allows map[int]*logpolicyAllow, pos token.Pos, value sensitiveValue) bool {
	if value.kind == sensitiveHTTPHeader && isNeverAllowHeader(value.label) {
		return false
	}
	return shouldSuppressLogpolicyPosition(fset, allows, pos)
}

func shouldSuppressLogpolicyPosition(fset *token.FileSet, allows map[int]*logpolicyAllow, pos token.Pos) bool {
	line := fset.Position(pos).Line
	for _, candidateLine := range []int{line, line - 1} {
		allow := allows[candidateLine]
		if allow == nil || allow.reason == "" {
			continue
		}
		allow.used = true
		return true
	}
	return false
}

func appendLogpolicyViolation(fset *token.FileSet, relPath string, allows map[int]*logpolicyAllow, violations *[]Violation, pos token.Pos, message string) {
	if shouldSuppressLogpolicyPosition(fset, allows, pos) {
		return
	}
	*violations = append(*violations, violationAt(fset, relPath, pos, message))
}

func isNeverAllowHeader(label string) bool {
	switch strings.ToLower(label) {
	case "cookie", "set-cookie", "authorization", "proxy-authorization":
		return true
	default:
		return false
	}
}

type sinkArg struct {
	expr          ast.Expr
	forceRawError bool
	format        string
}

// sensitiveSinkArgs identifies call arguments whose values are user-shareable
// output for logging, returned errors, test failure messages, or artifacts.
func sensitiveSinkArgs(call *ast.CallExpr, aliases map[string]string, testFile bool, functionContext string) []sinkArg {
	if len(call.Args) == 0 {
		return nil
	}
	if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
		return sinkArgs(call.Args, false)
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	if isHTTPErrorSelector(selector, aliases) {
		if len(call.Args) < 2 {
			return nil
		}
		forceRawError := isSensitiveHTTPErrorContext(functionContext) && isRawErrorLikeExpr(call.Args[1])
		return []sinkArg{{expr: call.Args[1], forceRawError: forceRawError}}
	}

	if isFmtSelector(selector, aliases, "Errorf") && hasStringArg(call, 0) {
		return sinkArgsWithFormat(call.Args[1:], false, stringArgValue(call, 0))
	}
	if isFmtPrintSelector(selector, aliases) && testFile {
		switch selector.Sel.Name {
		case "Printf":
			if hasStringArg(call, 0) {
				return sinkArgsWithFormat(call.Args[1:], false, stringArgValue(call, 0))
			}
		case "Print", "Println":
			return sinkArgs(call.Args, false)
		}
	}
	if isFmtSelector(selector, aliases, "Fprintf") && testFile {
		if len(call.Args) > 2 && hasStringArg(call, 1) {
			return sinkArgsWithFormat(call.Args[2:], false, stringArgValue(call, 1))
		}
	}

	if receiver, ok := selector.X.(*ast.Ident); ok && aliases[receiver.Name] != "" {
		return nil
	}
	if _, logger := loggerMethods[selector.Sel.Name]; logger && hasStringArg(call, 0) {
		if testFile || selector.Sel.Name == "Tracef" || selector.Sel.Name == "Debugf" ||
			selector.Sel.Name == "Infof" || selector.Sel.Name == "Warnf" || selector.Sel.Name == "Errorf" {
			return sinkArgsWithFormat(call.Args[1:], false, stringArgValue(call, 0))
		}
	}
	if testFile && isTestAssertionOutputMethod(selector.Sel.Name) {
		if isTestAssertionFormatOutputMethod(selector.Sel.Name) && hasStringArg(call, 0) {
			format := stringArgValue(call, 0)
			forceRawError := isSensitiveTestRawErrorContext(functionContext, format)
			return sinkArgsWithFormat(call.Args[1:], forceRawError, format)
		}
		return sinkArgs(call.Args, false)
	}

	return nil
}

func sinkArgs(exprs []ast.Expr, forceRawError bool) []sinkArg {
	return sinkArgsWithFormat(exprs, forceRawError, "")
}

func sinkArgsWithFormat(exprs []ast.Expr, forceRawError bool, format string) []sinkArg {
	result := make([]sinkArg, 0, len(exprs))
	for _, expr := range exprs {
		result = append(result, sinkArg{expr: expr, forceRawError: forceRawError && isRawErrorLikeExpr(expr), format: format})
	}
	return result
}

func hasStringArg(call *ast.CallExpr, index int) bool {
	if index >= len(call.Args) {
		return false
	}
	lit, ok := call.Args[index].(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func stringArgValue(call *ast.CallExpr, index int) string {
	if index >= len(call.Args) {
		return ""
	}
	lit, ok := call.Args[index].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return value
}

func isTestAssertionOutputMethod(name string) bool {
	switch name {
	case "Fatal", "Fatalf", "Error", "Errorf", "Log", "Logf":
		return true
	default:
		return false
	}
}

func isTestAssertionFormatOutputMethod(name string) bool {
	switch name {
	case "Fatalf", "Errorf", "Logf":
		return true
	default:
		return false
	}
}

func isFmtSelector(selector *ast.SelectorExpr, aliases map[string]string, name string) bool {
	pkg, ok := selector.X.(*ast.Ident)
	return ok && aliases[pkg.Name] == "fmt" && selector.Sel.Name == name
}

func isHTTPErrorSelector(selector *ast.SelectorExpr, aliases map[string]string) bool {
	pkg, ok := selector.X.(*ast.Ident)
	return ok && aliases[pkg.Name] == "net/http" && selector.Sel.Name == "Error"
}

// checkTestHandlerFatalCalls reports t.Fatal-style calls inside HTTP test
// handlers, where the handler runs outside the test goroutine.
func checkTestHandlerFatalCalls(fset *token.FileSet, root string, path string) ([]Violation, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	aliases := importAliases(file)
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	violations := make([]Violation, 0)
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.FuncLit)
		if !ok || !isHTTPHandlerFuncLit(lit, aliases) {
			return true
		}
		ast.Inspect(lit.Body, func(handlerNode ast.Node) bool {
			if nested, ok := handlerNode.(*ast.FuncLit); ok && nested != lit {
				return false
			}
			call, ok := handlerNode.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isTestingFatalCall(call) {
				violations = append(violations, violationAt(fset, relPath, call.Pos(), "httptest handlers must not call t.Fatal/t.Fatalf from the request goroutine; record the error and assert from the test goroutine"))
			}
			return true
		})
		return false
	})
	return violations, nil
}

func isHTTPHandlerFuncLit(lit *ast.FuncLit, aliases map[string]string) bool {
	if lit.Type == nil || lit.Type.Params == nil {
		return false
	}
	paramTypes := expandedParamTypes(lit.Type.Params.List)
	if len(paramTypes) != 2 {
		return false
	}
	return isHTTPResponseWriterType(paramTypes[0], aliases) && isHTTPRequestPointerType(paramTypes[1], aliases)
}

func expandedParamTypes(fields []*ast.Field) []ast.Expr {
	result := make([]ast.Expr, 0, len(fields))
	for _, field := range fields {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			result = append(result, field.Type)
		}
	}
	return result
}

func isHTTPResponseWriterType(expr ast.Expr, aliases map[string]string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ResponseWriter" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && aliases[pkg.Name] == "net/http"
}

func isHTTPRequestPointerType(expr ast.Expr, aliases map[string]string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := star.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Request" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && aliases[pkg.Name] == "net/http"
}

// isTestingFatalCall recognizes fatal test methods on conventional testing
// receivers used by handler fixtures.
func isTestingFatalCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch selector.Sel.Name {
	case "Fatal", "Fatalf", "FailNow":
	default:
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch strings.ToLower(receiver.Name) {
	case "t", "tb":
		return true
	default:
		return false
	}
}

// checkUnboundedResponseBodyUses flags response bodies that are read without a
// limit before flowing into redaction, errors, or shareable artifacts.
func checkUnboundedResponseBodyUses(fset *token.FileSet, relPath string, file *ast.File, aliases map[string]string) []Violation {
	violations := make([]Violation, 0)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		unboundedBodyVars := make(map[string]struct{})
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.AssignStmt:
				markUnboundedBodyAssignments(typed, aliases, unboundedBodyVars)
			case *ast.CallExpr:
				if isUnboundedResponseBodyUseCall(typed, unboundedBodyVars) {
					violations = append(violations, violationAt(fset, relPath, typed.Pos(), "response body reads used for logs/errors/artifacts must be bounded before redaction"))
				}
			}
			return true
		})
	}
	return violations
}

// markUnboundedBodyAssignments updates body-read taint for assignment targets.
// Single-call tuple assignments taint only the first return value.
func markUnboundedBodyAssignments(stmt *ast.AssignStmt, aliases map[string]string, unboundedBodyVars map[string]struct{}) {
	if len(stmt.Rhs) == 1 {
		for index, target := range stmt.Lhs {
			ident, ok := target.(*ast.Ident)
			if !ok || ident.Name == "_" {
				continue
			}
			if index == 0 && isUnboundedResponseBodyRead(stmt.Rhs[0], aliases) {
				unboundedBodyVars[ident.Name] = struct{}{}
				continue
			}
			delete(unboundedBodyVars, ident.Name)
		}
		return
	}

	for index, target := range stmt.Lhs {
		ident, ok := target.(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		if index >= len(stmt.Rhs) || !isUnboundedResponseBodyRead(stmt.Rhs[index], aliases) {
			delete(unboundedBodyVars, ident.Name)
			continue
		}
		unboundedBodyVars[ident.Name] = struct{}{}
	}
}

// isUnboundedResponseBodyRead reports io.ReadAll(resp.Body)-style reads that do
// not wrap the body with io.LimitReader.
func isUnboundedResponseBodyRead(expr ast.Expr, aliases map[string]string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	if isUnboundedResponseBodyHelperCallName(callName(call)) {
		return true
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || aliases[pkg.Name] != "io" || selector.Sel.Name != "ReadAll" || len(call.Args) != 1 {
		return false
	}
	if isLimitReaderCall(call.Args[0], aliases) {
		return false
	}
	return isResponseBodyExpr(call.Args[0])
}

// isUnboundedResponseBodyUseCall reports uses where an unbounded body value is
// about to become redacted output, an error message, or an artifact.
func isUnboundedResponseBodyUseCall(call *ast.CallExpr, unboundedBodyVars map[string]struct{}) bool {
	if len(unboundedBodyVars) == 0 {
		return false
	}
	if isRedactionCall(call) {
		for _, arg := range call.Args {
			if containsUnboundedBodyVar(arg, unboundedBodyVars) {
				return true
			}
		}
	}
	name := callName(call)
	if isResponseBodyErrorOrArtifactHelper(name) {
		for _, arg := range call.Args {
			if containsUnboundedBodyVar(arg, unboundedBodyVars) {
				return true
			}
		}
	}
	return false
}

// isResponseBodyErrorOrArtifactHelper recognizes helpers whose output may be
// returned to users or written into diagnostic artifacts.
func isResponseBodyErrorOrArtifactHelper(name string) bool {
	switch name {
	case "UploadHTTPError", "safeResponsePreview", "safeResponseMessage":
		return true
	default:
		return false
	}
}

// isUnboundedResponseBodyHelperCallName recognizes legacy helpers known to read
// a response body without enforcing the repo preview limit.
func isUnboundedResponseBodyHelperCallName(name string) bool {
	switch name {
	case "readAndCloseResponseBody":
		return true
	default:
		return false
	}
}

// containsUnboundedBodyVar reports whether an expression references a currently
// tainted unbounded response-body binding.
func containsUnboundedBodyVar(expr ast.Expr, unboundedBodyVars map[string]struct{}) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		if _, ok := unboundedBodyVars[ident.Name]; ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// isLimitReaderCall recognizes io.LimitReader wrappers that bound body reads.
func isLimitReaderCall(expr ast.Expr, aliases map[string]string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && aliases[pkg.Name] == "io" && selector.Sel.Name == "LimitReader"
}

func isSensitiveHTTPErrorContext(context string) bool {
	signals := []string{"auth", "cookie", "login", "credential", "csrf", "token"}
	for _, signal := range signals {
		if strings.Contains(context, signal) {
			return true
		}
	}
	return false
}

func isSensitiveTestRawErrorContext(context string, format string) bool {
	normalized := canonicalSensitiveKeyName(context)
	if !strings.Contains(normalized, "uploadrejected") &&
		!strings.Contains(normalized, "uploadrejection") &&
		!strings.Contains(normalized, "rejectionmessage") &&
		!strings.Contains(normalized, "sanitizingerror") {
		return false
	}
	lowerFormat := strings.ToLower(format)
	return strings.Contains(lowerFormat, "fallback") ||
		strings.Contains(lowerFormat, "rejection message") ||
		strings.Contains(lowerFormat, "upload rejected")
}

// markSensitiveAssignment propagates sensitivity through assignments and keeps
// multi-return calls indexed to the assigned result positions.
func markSensitiveAssignment(model *sensitiveModel, stmt *ast.AssignStmt) {
	declare := stmt.Tok == token.DEFINE
	for index, target := range stmt.Lhs {
		ident, ok := target.(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		rhsIndex := index
		if len(stmt.Rhs) == 1 {
			rhsIndex = 0
		}
		if rhsIndex >= len(stmt.Rhs) {
			model.bind(ident.Name, sensitiveValue{}, false, declare)
			continue
		}
		value, sensitive := sensitivityOfExprResult(model, stmt.Rhs[rhsIndex], index)
		model.bind(ident.Name, value, sensitive, declare)
	}
}

// bind applies declaration or assignment semantics for one modeled identifier.
func (m *sensitiveModel) bind(name string, value sensitiveValue, sensitive bool, declare bool) {
	if declare {
		m.declare(name, value, sensitive)
		return
	}
	m.assign(name, value, sensitive)
}

// markSensitiveRange treats range key and value variables as sensitive when the
// ranged expression is sensitive.
func markSensitiveRange(model *sensitiveModel, stmt *ast.RangeStmt) {
	value, sensitive := sensitivityOfExpr(model, stmt.X)
	for _, target := range []ast.Expr{stmt.Key, stmt.Value} {
		ident, ok := target.(*ast.Ident)
		if ok && ident.Name != "_" {
			model.bind(ident.Name, value, sensitive, stmt.Tok == token.DEFINE)
		}
	}
}

// sensitivityOfExprResult returns the sensitivity of one assignment result,
// preserving per-result behavior for known multi-return calls.
func sensitivityOfExprResult(model *sensitiveModel, expr ast.Expr, resultIndex int) (sensitiveValue, bool) {
	if call, ok := expr.(*ast.CallExpr); ok {
		if value, sensitive := sensitivityOfKnownCallResult(model, call, resultIndex); sensitive {
			return value, true
		}
		if resultIndex != 0 {
			return sensitiveValue{}, false
		}
	}
	return sensitivityOfExpr(model, expr)
}

// sensitivityOfExpr classifies expressions that directly contain, derive from,
// or propagate sensitive values.
func sensitivityOfExpr(model *sensitiveModel, expr ast.Expr) (sensitiveValue, bool) {
	if expr == nil || isSafeSensitiveOutputExpr(expr) {
		return sensitiveValue{}, false
	}
	switch typed := expr.(type) {
	case *ast.Ident:
		return model.lookup(typed.Name)
	case *ast.ParenExpr:
		return sensitivityOfExpr(model, typed.X)
	case *ast.UnaryExpr:
		if typed.Op == token.NOT {
			return sensitiveValue{}, false
		}
		return sensitivityOfExpr(model, typed.X)
	case *ast.BinaryExpr:
		if isBooleanBinaryOp(typed.Op) {
			return sensitiveValue{}, false
		}
		if value, ok := sensitivityOfSecretBearingURLExpr(model, typed); ok {
			return value, true
		}
		return firstSensitiveExpr(model, typed.X, typed.Y)
	case *ast.SelectorExpr:
		if value, ok := sensitivityOfSelectorExpr(model, typed); ok {
			return value, true
		}
		return sensitivityOfExpr(model, typed.X)
	case *ast.IndexExpr:
		if value, ok := sensitivityOfPayloadIndex(model, typed); ok {
			return value, true
		}
		return sensitivityOfExpr(model, typed.X)
	case *ast.SliceExpr:
		return sensitivityOfExpr(model, typed.X)
	case *ast.StarExpr:
		return sensitivityOfExpr(model, typed.X)
	case *ast.CallExpr:
		if value, ok := sensitivityOfKnownCallResult(model, typed, 0); ok {
			return value, true
		}
		if value, ok := sensitivityOfDirectCall(model, typed); ok {
			return value, true
		}
		if isSensitivePropagatingCall(model, typed) {
			for _, arg := range typed.Args {
				if value, ok := sensitivityOfExpr(model, arg); ok {
					return value, true
				}
			}
		}
	case *ast.CompositeLit:
		if isCookieCompositeType(typed.Type) {
			return sensitiveValue{kind: sensitiveCookieContainer, label: "cookies"}, true
		}
		for _, elt := range typed.Elts {
			if value, ok := sensitivityOfExpr(model, elt); ok {
				return value, true
			}
		}
	}
	return sensitiveValue{}, false
}

// isSensitivePropagatingCall recognizes wrappers that preserve the sensitive
// content of their arguments instead of sanitizing it.
func isSensitivePropagatingCall(model *sensitiveModel, call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "string"
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok {
			switch model.aliases[pkg.Name] {
			case "fmt":
				return fun.Sel.Name == "Sprintf"
			case "strings":
				return fun.Sel.Name == "TrimSpace"
			}
		}
	}
	return false
}

// firstSensitiveExpr returns the first sensitive expression in source order.
func firstSensitiveExpr(model *sensitiveModel, exprs ...ast.Expr) (sensitiveValue, bool) {
	for _, expr := range exprs {
		if value, ok := sensitivityOfExpr(model, expr); ok {
			return value, true
		}
	}
	return sensitiveValue{}, false
}

func sensitivityOfPayloadIndex(model *sensitiveModel, index *ast.IndexExpr) (sensitiveValue, bool) {
	if value, ok := sensitivityOfExpr(model, index.X); ok {
		return value, true
	}
	return sensitiveValue{}, false
}

// sensitivityOfDirectCall classifies sensitive values produced directly by
// header, form, query, cookie, URL, and body-read calls.
func sensitivityOfDirectCall(model *sensitiveModel, call *ast.CallExpr) (sensitiveValue, bool) {
	if value, ok := sensitivityOfKnownSensitiveCall(model, call); ok {
		return value, true
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return sensitiveValue{}, false
	}
	switch selector.Sel.Name {
	case "Get":
		if len(call.Args) != 1 {
			return sensitiveValue{}, false
		}
		key, ok := stringLiteral(call.Args[0])
		if !ok {
			return sensitiveValue{}, false
		}
		if isHeaderGetCall(selector) && isSensitiveHeaderName(key) {
			return sensitiveValue{kind: sensitiveHTTPHeader, label: canonicalHeaderName(key)}, true
		}
		if isQueryGetCall(selector) && isSensitiveQueryKey(key) {
			return sensitiveValue{kind: sensitiveQueryValue, label: key}, true
		}
	case "FormValue":
		if len(call.Args) == 1 {
			key, ok := stringLiteral(call.Args[0])
			if ok && isSensitiveFormKey(key) {
				return sensitiveValue{kind: sensitiveFormValue, label: key}, true
			}
		}
	case "Cookies":
		if len(call.Args) == 0 && isJarCookiesCall(selector) {
			return sensitiveValue{kind: sensitiveCookieContainer, label: "cookies"}, true
		}
	case "ReadAll":
		pkg, ok := selector.X.(*ast.Ident)
		if ok && model.aliases[pkg.Name] == "io" && len(call.Args) == 1 && isResponseBodyExpr(call.Args[0]) {
			return sensitiveValue{kind: sensitiveBody, label: "response body"}, true
		}
	}
	return sensitiveValue{}, false
}

// sensitivityOfKnownSensitiveCall classifies repo helper calls whose return
// values carry cookies, response bodies, URLs, or credential-like strings.
func sensitivityOfKnownSensitiveCall(model *sensitiveModel, call *ast.CallExpr) (sensitiveValue, bool) {
	name := callName(call)
	if value, ok := sensitivityOfSensitiveValueHelperCallName(name); ok {
		return value, true
	}
	switch name {
	case "LoadTrackerCookieMap", "LoadTrackerHTTPCookies", "CookieMapToHTTPCookies", "CookiesToMap", "httpCookiesToMap", "cookiesFromJar", "btnCookiesFromJar":
		return sensitiveValue{kind: sensitiveCookieContainer, label: "cookies"}, true
	case "postForm", "postMultipart", "postMultipartWithFields", "postMultipartRepeatedFileField", "readAndCloseResponseBody",
		"readTVDBResponseBody", "readIMDbResponseBody":
		return sensitiveValue{kind: sensitiveBody, label: "response body"}, true
	case "String":
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && isSensitiveURLReceiver(model, selector.X) {
			return sensitiveValue{kind: sensitiveEndpoint, label: "url"}, true
		}
	}
	return sensitiveValue{}, false
}

// sensitivityOfSensitiveValueHelperCallName classifies helper names that imply
// the first returned value is a credential-like secret.
func sensitivityOfSensitiveValueHelperCallName(name string) (sensitiveValue, bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	if !isSensitiveValueHelperName(lower) {
		return sensitiveValue{}, false
	}
	normalized := canonicalSensitiveKeyName(name)
	switch {
	case strings.Contains(normalized, "apikey"):
		return sensitiveValue{kind: sensitiveConfigField, label: "APIKey"}, true
	case strings.Contains(normalized, "token"):
		return sensitiveValue{kind: sensitiveConfigField, label: "token"}, true
	case strings.Contains(normalized, "passkey"):
		return sensitiveValue{kind: sensitiveConfigField, label: "passkey"}, true
	case strings.Contains(normalized, "authkey"):
		return sensitiveValue{kind: sensitiveConfigField, label: "authkey"}, true
	case strings.Contains(normalized, "rsskey"):
		return sensitiveValue{kind: sensitiveConfigField, label: "rsskey"}, true
	case strings.Contains(normalized, "torrentpass"):
		return sensitiveValue{kind: sensitiveConfigField, label: "torrentpass"}, true
	default:
		return sensitiveValue{}, false
	}
}

func isSensitiveValueHelperName(name string) bool {
	for _, prefix := range []string{"load", "get", "read", "fetch", "lookup", "extract", "parse", "generate", "refresh", "create", "new", "stored", "current"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func callName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

func sensitivityOfSelectorExpr(model *sensitiveModel, selector *ast.SelectorExpr) (sensitiveValue, bool) {
	name := strings.TrimSpace(selector.Sel.Name)
	if model.testFile && isConfigOwnerTestPath(model.relPath) {
		return sensitiveValue{}, false
	}
	if isSensitiveConfigFieldName(name) {
		return sensitiveValue{kind: sensitiveConfigField, label: name}, true
	}
	if isSensitiveEndpointFieldName(name) {
		return sensitiveValue{kind: sensitiveEndpoint, label: name}, true
	}
	if name == "URL" && strings.Contains(strings.ToLower(selectorPath(selector.X)), "tracker") {
		return sensitiveValue{kind: sensitiveEndpoint, label: name}, true
	}
	return sensitiveValue{}, false
}

func sensitivityOfSecretBearingURLExpr(model *sensitiveModel, expr *ast.BinaryExpr) (sensitiveValue, bool) {
	if !binaryExprContainsSecretURLKey(expr) {
		return sensitiveValue{}, false
	}
	if value, ok := firstSensitiveExpr(model, expr.X, expr.Y); ok {
		return value, true
	}
	if containsSensitiveSelector(model, expr) {
		return sensitiveValue{kind: sensitiveEndpoint, label: "secret URL"}, true
	}
	return sensitiveValue{}, false
}

// sensitivityOfKnownCallResult models helpers where only the first return value
// carries sensitive data and trailing returns such as errors are safe by default.
func sensitivityOfKnownCallResult(model *sensitiveModel, call *ast.CallExpr, resultIndex int) (sensitiveValue, bool) {
	if resultIndex != 0 {
		return sensitiveValue{}, false
	}
	return sensitivityOfDirectCall(model, call)
}

func isHeaderGetCall(selector *ast.SelectorExpr) bool {
	receiver, ok := selector.X.(*ast.SelectorExpr)
	return ok && receiver.Sel.Name == "Header"
}

func isQueryGetCall(selector *ast.SelectorExpr) bool {
	call, ok := selector.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	querySelector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && querySelector.Sel.Name == "Query"
}

func isJarCookiesCall(selector *ast.SelectorExpr) bool {
	receiver, ok := selector.X.(*ast.SelectorExpr)
	return ok && receiver.Sel.Name == "Jar"
}

func isResponseBodyExpr(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Body"
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func isBooleanBinaryOp(op token.Token) bool {
	return op == token.EQL ||
		op == token.NEQ ||
		op == token.LSS ||
		op == token.LEQ ||
		op == token.GTR ||
		op == token.GEQ ||
		op == token.LAND ||
		op == token.LOR
}

func isSensitiveHeaderName(name string) bool {
	switch canonicalHeaderName(name) {
	case "cookie", "set-cookie", "authorization", "proxy-authorization", "x-api-key", "x-api-token", "x-auth-token":
		return true
	default:
		return false
	}
}

func canonicalHeaderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func isSensitiveFormKey(key string) bool {
	switch canonicalSensitiveKeyName(key) {
	case "password", "passkey", "token", "auth", "apikey", "apitoken", "authkey", "csrf", "anticsrftoken", "secret":
		return true
	default:
		return false
	}
}

func isSensitiveQueryKey(key string) bool {
	switch canonicalSensitiveKeyName(key) {
	case "token", "apikey", "apitoken", "passkey", "authkey", "secret", "rsskey", "torrentpass", "password", "auth", "csrf", "anticsrftoken":
		return true
	default:
		return false
	}
}

func isConfigOwnerTestPath(relPath string) bool {
	return strings.HasPrefix(relPath, "internal/config/") ||
		strings.HasPrefix(relPath, "internal/configstore/")
}

func isSensitiveConfigFieldName(name string) bool {
	switch canonicalSensitiveKeyName(name) {
	case "apikey", "apitoken", "password", "passkey", "token", "authkey", "anticsrftoken", "otpuri", "tmdbapi", "sonarrapikey", "radarrapikey", "qbitpass", "rsskey", "torrentpass", "secret":
		return true
	default:
		return false
	}
}

func canonicalSensitiveKeyName(name string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(name)))
}

func isSensitiveEndpointFieldName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "announce", "announcelist", "announceurl", "endpoint":
		return true
	default:
		return false
	}
}

func isCookieCompositeType(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.SelectorExpr:
		return typed.Sel.Name == "Cookie"
	case *ast.StarExpr:
		return isCookieCompositeType(typed.X)
	case *ast.ArrayType:
		return isCookieCompositeType(typed.Elt)
	default:
		return false
	}
}

func isSensitiveURLReceiver(model *sensitiveModel, expr ast.Expr) bool {
	if _, ok := sensitivityOfExpr(model, expr); ok {
		return true
	}
	return strings.Contains(strings.ToLower(selectorPath(expr)), "tracker")
}

func selectorPath(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		prefix := selectorPath(typed.X)
		if prefix == "" {
			return typed.Sel.Name
		}
		return prefix + "." + typed.Sel.Name
	case *ast.IndexExpr:
		return selectorPath(typed.X)
	default:
		return ""
	}
}

func binaryExprContainsSecretURLKey(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		lower := canonicalSensitiveURLLiteral(value)
		if strings.Contains(lower, "api_key=") ||
			strings.Contains(lower, "apikey=") ||
			strings.Contains(lower, "api_token=") ||
			strings.Contains(lower, "apitoken=") ||
			strings.Contains(lower, "passkey=") ||
			strings.Contains(lower, "authkey=") ||
			strings.Contains(lower, "rsskey=") ||
			strings.Contains(lower, "torrentpass=") ||
			strings.Contains(lower, "secret=") ||
			strings.Contains(lower, "token=") {
			found = true
			return false
		}
		return true
	})
	return found
}

func canonicalSensitiveURLLiteral(value string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(value))
}

func containsSensitiveSelector(model *sensitiveModel, expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if _, sensitive := sensitivityOfSelectorExpr(model, selector); sensitive {
			found = true
			return false
		}
		return true
	})
	return found
}

func containsSensitiveFixtureLiteral(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		lower := canonicalSensitiveURLLiteral(value)
		if strings.Contains(lower, "hunter2") ||
			strings.Contains(lower, "secret") ||
			strings.Contains(lower, "apikey") ||
			strings.Contains(lower, "apitoken") ||
			strings.Contains(lower, "passkey") ||
			strings.Contains(lower, "authkey") ||
			strings.Contains(lower, "rsskey") ||
			strings.Contains(lower, "torrentpass") {
			found = true
			return false
		}
		return true
	})
	return found
}

func isRedactionTestPath(relPath string) bool {
	return strings.HasPrefix(relPath, "internal/redaction/") && strings.HasSuffix(relPath, "_test.go")
}

func isTestLogBufferDumpExpr(expr ast.Expr, relPath string) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		switch lower := strings.ToLower(strings.TrimSpace(ident.Name)); {
		case lower == "log" || lower == "logs" || lower == "text" ||
			lower == "alllogs" || lower == "infolog" || lower == "tracelog" ||
			lower == "warnlog" || lower == "errorlog" || lower == "debuglog" ||
			strings.HasSuffix(lower, "logs"):
			found = true
			return false
		case lower == "input" || lower == "output" || lower == "redacted" || lower == "secret":
			if strings.HasPrefix(relPath, "internal/redaction/") {
				found = true
				return false
			}
			return true
		default:
			return true
		}
	})
	return found
}

func isSafeSensitiveOutputExpr(expr ast.Expr) bool {
	if containsSafeOutputCall(expr) {
		return true
	}
	switch typed := expr.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return typed.Name == "true" || typed.Name == "false" || typed.Name == "nil"
	case *ast.UnaryExpr:
		return typed.Op == token.NOT
	case *ast.BinaryExpr:
		return isBooleanBinaryOp(typed.Op)
	case *ast.CallExpr:
		if ident, ok := typed.Fun.(*ast.Ident); ok && ident.Name == "len" {
			return true
		}
		if selector, ok := typed.Fun.(*ast.SelectorExpr); ok {
			switch selector.Sel.Name {
			case "Contains", "HasPrefix", "HasSuffix", "EqualFold":
				return true
			}
		}
	}
	return false
}

func containsSafeOutputCall(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isSafeOutputCall(call) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isSafeOutputCall(call *ast.CallExpr) bool {
	if isRedactionCall(call) {
		return true
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		lower := strings.ToLower(strings.TrimSpace(fun.Name))
		return strings.HasPrefix(lower, "redact") ||
			fun.Name == "safeDryRunEndpoint" ||
			fun.Name == "formatDryRunPayloadValue" ||
			fun.Name == "safeResponsePreview"
	case *ast.SelectorExpr:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(fun.Sel.Name)), "redact") {
			return true
		}
		return fun.Sel.Name == "RedactErrorDetail" || fun.Sel.Name == "ExtractHTTPErrorDetail"
	default:
		return false
	}
}

func sensitiveOutputMessage(value sensitiveValue) string {
	switch value.kind {
	case sensitiveHTTPHeader:
		return "sensitive HTTP header output must be redacted or replaced with stable state"
	case sensitiveCookieContainer:
		return "cookie output must be redacted or reduced to count/state"
	case sensitiveFormValue:
		return "sensitive form value output must be redacted or replaced with stable state"
	case sensitiveQueryValue:
		return "sensitive query value output must be redacted or replaced with stable state"
	case sensitiveConfigField:
		return "secret config field output must be redacted or replaced with stable state"
	case sensitiveEndpoint:
		return "secret-bearing URL/endpoint output must be redacted before printing"
	case sensitivePayload:
		return "secret-bearing payload output must be redacted or reduced to safe fields"
	case sensitiveBody:
		return "request/response body output must be redacted before printing"
	case sensitiveRawError:
		return "remote/auth/cookie/token errors must not expose raw errors in logs or responses"
	case sensitiveGeneric:
		return "sensitive output must be redacted or replaced with stable state"
	default:
		return "sensitive output must be redacted or replaced with stable state"
	}
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

func isFmtPrintSelector(selector *ast.SelectorExpr, aliases map[string]string) bool {
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || aliases[pkg.Name] != "fmt" {
		return false
	}
	switch selector.Sel.Name {
	case "Print", "Printf", "Println", "Fprint", "Fprintf", "Fprintln":
		return true
	default:
		return false
	}
}

func isTerminalStderrOutputCall(call *ast.CallExpr, aliases map[string]string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isFmtPrintSelector(selector, aliases) || len(call.Args) == 0 {
		return false
	}
	switch selector.Sel.Name {
	case "Fprint", "Fprintf", "Fprintln":
	default:
		return false
	}
	target, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok || target.Sel.Name != "Stderr" {
		return false
	}
	pkg, ok := target.X.(*ast.Ident)
	return ok && aliases[pkg.Name] == "os"
}

// isUnsafeTerminalDiagnosticArg reports stderr diagnostic args that include raw
// errors or warning text without a recognized sanitizer.
func isUnsafeTerminalDiagnosticArg(format string, expr ast.Expr, aliases map[string]string) bool {
	if isSafeTerminalDiagnosticExpr(expr, aliases) {
		return false
	}
	if containsRawTerminalDiagnosticExpr(expr, aliases) {
		return true
	}
	if strings.Contains(strings.ToLower(format), "warning") && containsWarningLikeExpr(expr) {
		return true
	}
	return false
}

// containsRawTerminalDiagnosticExpr detects error-like values while respecting
// sanitizer calls nested inside larger diagnostic expressions.
func containsRawTerminalDiagnosticExpr(expr ast.Expr, aliases map[string]string) bool {
	if isSafeTerminalDiagnosticExpr(expr, aliases) {
		return false
	}
	raw := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if raw || node == nil {
			return false
		}
		if exprNode, ok := node.(ast.Expr); ok && isSafeTerminalDiagnosticExpr(exprNode, aliases) {
			return false
		}
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

func containsWarningLikeExpr(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		ident, ok := node.(*ast.Ident)
		if ok && isWarningLikeName(ident.Name) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isWarningLikeName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "w" || lower == "warning" || lower == "warn" || strings.HasSuffix(lower, "warning") || strings.HasSuffix(lower, "warn")
}

func isSafeTerminalDiagnosticExpr(expr ast.Expr, aliases map[string]string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		switch fun.Name {
		case "formatTerminalDiagnostic", "formatTerminalError", "formatTerminalWarning", "sanitizeTerminalDiagnostic":
			return true
		default:
			return false
		}
	case *ast.SelectorExpr:
		pkg, ok := fun.X.(*ast.Ident)
		if !ok {
			return false
		}
		return aliases[pkg.Name] == "github.com/autobrr/upbrr/internal/logging" && fun.Sel.Name == "SanitizeMessage"
	default:
		return false
	}
}

// fmtOutputArgs returns only user-visible fmt arguments, excluding writers and
// format strings according to the specific print method.
func fmtOutputArgs(call *ast.CallExpr) []ast.Expr {
	if call == nil || len(call.Args) == 0 {
		return nil
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	switch selector.Sel.Name {
	case "Print", "Println":
		return call.Args
	case "Printf":
		return call.Args[1:]
	case "Fprint", "Fprintln":
		return call.Args[1:]
	case "Fprintf":
		if len(call.Args) <= 2 {
			return nil
		}
		return call.Args[2:]
	default:
		return nil
	}
}

func cliOutputFormat(call *ast.CallExpr) string {
	if len(call.Args) == 0 {
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	var formatIndex int
	switch selector.Sel.Name {
	case "Printf":
		formatIndex = 0
	case "Fprintf":
		formatIndex = 1
	default:
		return ""
	}
	if formatIndex >= len(call.Args) {
		return ""
	}
	firstArg, ok := call.Args[formatIndex].(*ast.BasicLit)
	if !ok || firstArg.Kind != token.STRING {
		return ""
	}
	format, err := strconv.Unquote(firstArg.Value)
	if err != nil {
		return ""
	}
	return format
}

func containsEndpointExpr(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if isDryRunEndpointExprNode(node) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isDryRunEndpointExprNode(node ast.Node) bool {
	selector, ok := node.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Endpoint"
}

// containsLocalPathOutputExpr reports output expressions that include likely
// local filesystem paths not wrapped by an approved path-label formatter.
func containsLocalPathOutputExpr(expr ast.Expr, localPathVars map[string]struct{}, aliases map[string]string) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		if call, ok := node.(*ast.CallExpr); ok && isSafeLocalPathOutputCall(call, aliases) {
			return false
		}
		if exprNode, ok := node.(ast.Expr); ok && isLocalPathExpr(exprNode, localPathVars, aliases) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isSafeLocalPathOutputExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && isSafeLocalPathOutputCall(call, nil)
}

func isSafeLocalPathOutputCall(call *ast.CallExpr, aliases map[string]string) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		switch fun.Name {
		case "formatDryRunFilePath", "formatLogPath", "formatPathLabel", "formatSourceLabel", "sourcePathLabel", "pathLabel":
			return true
		default:
			return false
		}
	case *ast.SelectorExpr:
		pkg, ok := fun.X.(*ast.Ident)
		if !ok {
			return false
		}
		importPath := ""
		if aliases != nil {
			importPath = aliases[pkg.Name]
		}
		return (importPath == "path/filepath" || pkg.Name == "filepath") && fun.Sel.Name == "Base" ||
			(importPath == "github.com/autobrr/upbrr/internal/pathutil" || pkg.Name == "pathutil") && fun.Sel.Name == "Base"
	default:
		return false
	}
}

// isLocalPathExpr recognizes host filesystem path expressions while avoiding
// URL, URI, route, and endpoint names.
func isLocalPathExpr(expr ast.Expr, localPathVars map[string]struct{}, aliases map[string]string) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		if _, ok := localPathVars[typed.Name]; ok {
			return true
		}
		return isLocalPathIdentName(typed.Name)
	case *ast.SelectorExpr:
		if isLocalPathFieldName(typed.Sel.Name) {
			return true
		}
		return isLocalPathExpr(typed.X, localPathVars, aliases)
	case *ast.CallExpr:
		return isLocalPathProducingCall(typed, aliases)
	case *ast.ParenExpr:
		return isLocalPathExpr(typed.X, localPathVars, aliases)
	case *ast.UnaryExpr:
		return isLocalPathExpr(typed.X, localPathVars, aliases)
	case *ast.IndexExpr:
		return isLocalPathExpr(typed.X, localPathVars, aliases)
	}
	return false
}

func isLocalPathProducingCall(call *ast.CallExpr, aliases map[string]string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok {
		if pkg, pkgOK := selector.X.(*ast.Ident); pkgOK {
			switch aliases[pkg.Name] {
			case "path/filepath":
				switch selector.Sel.Name {
				case "Abs", "Clean", "Dir", "Join":
					return true
				}
			case "github.com/autobrr/upbrr/internal/services/db":
				return selector.Sel.Name == "Subdir"
			case "github.com/autobrr/upbrr/internal/paths":
				return strings.Contains(strings.ToLower(selector.Sel.Name), "path") || selector.Sel.Name == "ReleaseTempDir"
			}
		}
	}
	name := strings.ToLower(callName(call))
	return strings.Contains(name, "torrentpath") ||
		strings.Contains(name, "artifactpath") ||
		strings.Contains(name, "failurepath") ||
		strings.Contains(name, "temppath") ||
		strings.Contains(name, "releasedir")
}

func isLocalPathIdentName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || lower == "path" {
		return false
	}
	if strings.Contains(lower, "url") || strings.Contains(lower, "uri") || strings.Contains(lower, "route") || strings.Contains(lower, "endpoint") {
		return false
	}
	switch lower {
	case "tmpdir", "tmproot", "cachedir", "logdir", "output", "target", "candidate", "guessed":
		return true
	}
	return strings.HasSuffix(lower, "path")
}

func isLocalPathFieldName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "sourcepath", "torrentpath", "clienttorrentpath", "mediainfotextpath", "mediainfojsonpath", "dbpath":
		return true
	default:
		return false
	}
}

func containsDryRunFilePathExpr(expr ast.Expr, dryRunFileVars map[string]struct{}) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if isDryRunFilePathExprNode(node, dryRunFileVars) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isDryRunFilePathExprNode(node ast.Node, dryRunFileVars map[string]struct{}) bool {
	selector, ok := node.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Path" {
		return false
	}
	if ident, ok := selector.X.(*ast.Ident); ok {
		_, found := dryRunFileVars[ident.Name]
		return found
	}
	index, ok := selector.X.(*ast.IndexExpr)
	if !ok {
		return false
	}
	filesSelector, ok := index.X.(*ast.SelectorExpr)
	return ok && filesSelector.Sel.Name == "Files"
}

// isDryRunPayloadValueExpr reports direct reads from TrackerDryRunEntry-style
// Payload maps, which may contain tracker credentials or secret URLs.
func isDryRunPayloadValueExpr(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		index, ok := node.(*ast.IndexExpr)
		if !ok {
			return true
		}
		selector, ok := index.X.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Payload" {
			found = true
			return false
		}
		return true
	})
	return found
}

// isSafeDryRunOutputExpr recognizes the redaction wrappers allowed to print
// dry-run endpoint and payload values.
func isSafeDryRunOutputExpr(expr ast.Expr) bool {
	if containsRedactionCall(expr) {
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	switch ident.Name {
	case "safeDryRunEndpoint", "formatDryRunPayloadValue", "formatDryRunFilePath":
		return true
	default:
		return false
	}
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

func hasRawErrorLogField(lowerFormat string) bool {
	return rawErrorLogFieldRe.MatchString(lowerFormat)
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

func warnLevelHygieneViolations(lowerFormat string) []string {
	if !isRoutineProgressWarnMessage(lowerFormat) {
		return nil
	}
	return []string{"Warnf appears to report routine progress; use Infof/Debugf unless user attention is required"}
}

func isRoutineProgressWarnMessage(lowerFormat string) bool {
	for _, pattern := range warnAttentionPatterns {
		if pattern.MatchString(lowerFormat) {
			return false
		}
	}
	for _, pattern := range warnRoutineProgressPatterns {
		if pattern.MatchString(lowerFormat) {
			return true
		}
	}
	return false
}

func traceLevelHygieneViolations(lowerFormat string) []string {
	for _, pattern := range traceUserOutcomePatterns {
		if pattern.MatchString(lowerFormat) {
			return []string{"Tracef appears to report user-visible outcome; use Infof/Warnf/Errorf"}
		}
	}
	return nil
}

// workflowLogSummary records structural signals used to decide whether a
// function is workflow-like enough to require progress and decision logging.
type workflowLogSummary struct {
	fn                     *ast.FuncDecl
	explicitLoggerParam    bool
	receiverBoundary       bool
	loggerAccess           bool
	workflowName           bool
	branches               int
	loops                  int
	errorReturns           int
	contextualErrorReturns int
	operationCalls         int
	decisionSignals        int
	decisionLoggerCalls    int
	loggerCalls            map[string]int
	logViolations          []workflowLogViolation
	statementCount         int
	nodeCount              int
}

// workflowLogViolation stores a workflow logging finding before allow-comment
// filtering is applied at the source position.
type workflowLogViolation struct {
	pos     token.Pos
	message string
}

// checkWorkflowLoggingCoverage catches likely workflow functions that have
// logger access but omit progress, decision, or failure logging.
func checkWorkflowLoggingCoverage(fset *token.FileSet, relPath string, file *ast.File, allows map[int]*logpolicyAllow) []Violation {
	violations := make([]Violation, 0)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		summary := summarizeWorkflowFunction(fn)
		if !summary.isWorkflowLike() {
			continue
		}
		if summary.explicitLoggerParam && summary.totalLoggerCalls() == 0 {
			appendLogpolicyViolation(fset, relPath, allows, &violations, fn.Name.Pos(), "workflow-like function has no logging; add Infof/Warnf/Debugf/Tracef progress or decision logs, or add logpolicy allow with reason")
		}
		if enableWorkflowBranchErrorCheck && summary.enforceWorkflowLogChecks() && summary.errorReturns > 0 && summary.contextualErrorReturns < summary.errorReturns && summary.totalLoggerCalls() > 0 && summary.loggerCalls["Warnf"] == 0 && summary.loggerCalls["Errorf"] == 0 {
			appendLogpolicyViolation(fset, relPath, allows, &violations, fn.Name.Pos(), "workflow-like function has branch error returns without Warnf/Errorf blocked-outcome logging")
		}
		if enableWorkflowDecisionCheck && summary.enforceWorkflowLogChecks() && summary.decisionSignals > 0 && summary.totalLoggerCalls() > 0 && summary.decisionLoggerCalls == 0 {
			appendLogpolicyViolation(fset, relPath, allows, &violations, fn.Name.Pos(), "workflow decision lacks logging; add stable decision/state context at Debugf, Infof, or Warnf")
		}
		for _, logViolation := range summary.logViolations {
			appendLogpolicyViolation(fset, relPath, allows, &violations, logViolation.pos, logViolation.message)
		}
	}
	return violations
}

func summarizeWorkflowFunction(fn *ast.FuncDecl) workflowLogSummary {
	explicitLoggerParam := functionHasLoggerParam(fn)
	summary := workflowLogSummary{
		fn:                  fn,
		explicitLoggerParam: explicitLoggerParam,
		receiverBoundary:    isReceiverWorkflowBoundary(fn),
		loggerAccess:        explicitLoggerParam,
		workflowName:        isWorkflowName(fn.Name.Name),
		loggerCalls:         map[string]int{},
		statementCount:      len(fn.Body.List),
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		summary.nodeCount++
		switch typed := node.(type) {
		case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			summary.branches++
			if containsDecisionSignal(typed) {
				summary.decisionSignals++
			}
		case *ast.ForStmt, *ast.RangeStmt:
			summary.loops++
		case *ast.ReturnStmt:
			if isNonNilErrorReturn(typed) {
				summary.errorReturns++
				if hasContextualErrorReturn(typed) {
					summary.contextualErrorReturns++
				}
			}
		case *ast.CallExpr:
			level, loggerCall := loggerCallLevel(typed)
			if loggerCall {
				summary.loggerAccess = true
				summary.loggerCalls[level]++
				if loggerCallHasDecisionSignal(typed) {
					summary.decisionLoggerCalls++
				}
				if enableWorkflowStableFieldCheck && summary.enforceWorkflowLogChecks() {
					summary.logViolations = append(summary.logViolations, workflowLogFieldViolations(typed, level)...)
				}
				return true
			}
			if isWorkflowOperationCall(typed) {
				summary.operationCalls++
			}
			if containsDecisionSignal(typed) {
				summary.decisionSignals++
			}
		}
		return true
	})
	return summary
}

func (s workflowLogSummary) totalLoggerCalls() int {
	total := 0
	for _, count := range s.loggerCalls {
		total += count
	}
	return total
}

func (s workflowLogSummary) enforceWorkflowLogChecks() bool {
	return s.explicitLoggerParam || s.receiverBoundary
}

func (s workflowLogSummary) isWorkflowLike() bool {
	if !s.loggerAccess || isExcludedWorkflowHelper(s.fn.Name.Name) {
		return false
	}
	if s.nodeCount < 25 && s.statementCount < 15 && !s.workflowName {
		return false
	}
	score := 0
	if s.workflowName {
		score++
	}
	score += minInt(s.operationCalls, 3)
	score += minInt(s.branches, 2)
	score += minInt(s.loops, 2)
	score += minInt(s.errorReturns, 2)
	if isEntrypointWorkflowName(s.fn.Name.Name) {
		score += 2
	}
	return score >= workflowLogScoreThreshold
}

func isReceiverWorkflowBoundary(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Recv == nil || !fn.Name.IsExported() {
		return false
	}
	receiver := receiverTypeName(fn)
	if receiver == "" || !ast.IsExported(receiver) || isExcludedWorkflowReceiver(receiver) {
		return false
	}
	return true
}

func receiverTypeName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return exprTypeName(fn.Recv.List[0].Type)
}

func exprTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return exprTypeName(typed.X)
	default:
		return ""
	}
}

func isExcludedWorkflowReceiver(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return true
	}
	if strings.HasSuffix(lower, "client") || strings.Contains(lower, "detector") || strings.Contains(lower, "repository") {
		return true
	}
	return false
}

func functionHasLoggerParam(fn *ast.FuncDecl) bool {
	if fn.Type == nil || fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "log" {
				return true
			}
		}
	}
	return false
}

func loggerCallLevel(call *ast.CallExpr) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if pkg, ok := selector.X.(*ast.Ident); ok {
		switch pkg.Name {
		case "fmt", "errors":
			return "", false
		}
	}
	if _, ok := loggerMethods[selector.Sel.Name]; ok {
		return selector.Sel.Name, true
	}
	return "", false
}

func workflowLogFieldViolations(call *ast.CallExpr, level string) []workflowLogViolation {
	if level != "Infof" && level != "Warnf" && level != "Debugf" {
		return nil
	}
	if len(call.Args) < 3 {
		return nil
	}
	format := stringArgValue(call, 0)
	if format == "" || hasStableKeyValueField(format) || countFormatVerbs(format) < 2 {
		return nil
	}
	return []workflowLogViolation{{
		pos:     call.Args[0].Pos(),
		message: "workflow log with multiple values should use stable key=value fields",
	}}
}

func loggerCallHasDecisionSignal(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	format := stringArgValue(call, 0)
	return format != "" && isDecisionSignal(format)
}

func countFormatVerbs(format string) int {
	count := 0
	escaped := false
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			escaped = true
			continue
		}
		count++
	}
	return count
}

func hasStableKeyValueField(format string) bool {
	return regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9_]*=%`).MatchString(format)
}

func isNonNilErrorReturn(ret *ast.ReturnStmt) bool {
	for _, result := range ret.Results {
		if ident, ok := result.(*ast.Ident); ok {
			if ident.Name == "nil" {
				continue
			}
			if isErrorLikeName(ident.Name) {
				return true
			}
		}
		if call, ok := result.(*ast.CallExpr); ok {
			if isErrorFactoryCall(call) {
				return true
			}
		}
	}
	return false
}

func hasContextualErrorReturn(ret *ast.ReturnStmt) bool {
	for _, result := range ret.Results {
		call, ok := result.(*ast.CallExpr)
		if ok && isErrorFactoryCall(call) {
			return true
		}
	}
	return false
}

func isErrorFactoryCall(call *ast.CallExpr) bool {
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		if pkg, ok := selector.X.(*ast.Ident); ok {
			return (pkg.Name == "fmt" && selector.Sel.Name == "Errorf") || (pkg.Name == "errors" && selector.Sel.Name == "New")
		}
	}
	return false
}

func isWorkflowOperationCall(call *ast.CallExpr) bool {
	name := strings.ToLower(callName(call))
	if name == "" {
		return false
	}
	signals := []string{"upload", "search", "check", "validate", "load", "save", "read", "write", "request", "post", "get", "create", "build", "resolve", "migrate", "discover", "generate", "submit", "start", "run", "decode"}
	for _, signal := range signals {
		if strings.Contains(name, signal) {
			return true
		}
	}
	return false
}

func containsDecisionSignal(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(inner ast.Node) bool {
		if found || inner == nil {
			return false
		}
		switch typed := inner.(type) {
		case *ast.Ident:
			found = isDecisionSignal(typed.Name)
		case *ast.BasicLit:
			if typed.Kind == token.STRING {
				value, err := strconv.Unquote(typed.Value)
				found = err == nil && isDecisionSignal(value)
			}
		}
		return !found
	})
	return found
}

func isDecisionSignal(value string) bool {
	lower := strings.ToLower(value)
	signals := []string{"skip", "selected", "ready", "blocked", "fallback", "retry", "prompt", "dryrun", "dry_run", "unattended", "auth", "dupe", "rule", "decision"}
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func isWorkflowName(name string) bool {
	lower := strings.ToLower(name)
	signals := []string{"run", "upload", "prepare", "process", "execute", "build", "resolve", "search", "validate", "check", "import", "export", "save", "load", "migrate", "discover", "generate", "create", "submit", "start", "tracker", "torrent", "dupe", "auth", "metadata", "screenshot", "image", "client", "queue", "config", "database", "runtime"}
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func isEntrypointWorkflowName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "run") || strings.HasPrefix(lower, "start") || strings.HasPrefix(lower, "upload") || strings.HasPrefix(lower, "process")
}

func isExcludedWorkflowHelper(name string) bool {
	lower := strings.ToLower(name)
	prefixes := []string{"string", "error", "marshal", "unmarshal", "format", "redact", "sanitize", "normalize", "clone", "copy"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return lower == "len" || lower == "less" || lower == "swap"
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
