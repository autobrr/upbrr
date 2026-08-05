// Package policy checks repository source for portable path-handling violations.
package policy

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

var checkedFilepathCalls = map[string]struct{}{
	"Abs":       {},
	"Base":      {},
	"Clean":     {},
	"Dir":       {},
	"Ext":       {},
	"FromSlash": {},
	"Glob":      {},
	"IsAbs":     {},
	"Join":      {},
	"Rel":       {},
	"ToSlash":   {},
}

var checkedPathCalls = map[string]struct{}{
	"Base":  {},
	"Clean": {},
	"Dir":   {},
	"Ext":   {},
	"IsAbs": {},
	"Join":  {},
}

var filesystemCalls = map[string]map[string]struct{}{
	"os": {
		"Create":    {},
		"Mkdir":     {},
		"MkdirAll":  {},
		"Open":      {},
		"OpenFile":  {},
		"ReadFile":  {},
		"Remove":    {},
		"RemoveAll": {},
		"Rename":    {},
		"Stat":      {},
		"WriteFile": {},
	},
}

// Violation identifies one path-policy finding in a repository source file.
type Violation struct {
	// File is the repository-relative source path containing the finding.
	File string
	// Line is the one-based source line containing the finding.
	Line int
	// Column is the one-based source column containing the finding.
	Column int
	// Message explains the violated path-policy rule.
	Message string
}

// allowComment tracks whether a path-policy suppression comment was consumed by
// a finding on its associated source line.
type allowComment struct {
	line int
	used bool
}

// checker holds per-file AST state and accumulated path-policy findings.
type checker struct {
	fset       *token.FileSet
	aliases    map[string]string
	relPath    string
	isTest     bool
	allows     map[int]*allowComment
	allowList  []*allowComment
	violations []Violation
}

// CheckRepository scans Go source beneath root, excluding repository metadata,
// vendored dependencies, node modules, and build output. Findings are returned
// in file, line, column, then message order.
func CheckRepository(root string) ([]Violation, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("pathpolicy: stat repository root: %w", err)
	}

	violations := make([]Violation, 0)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if shouldSkipDir(entry.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
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
		return nil, fmt.Errorf("pathpolicy: walk repository: %w", err)
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
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}
	relPath = filepath.ToSlash(relPath)

	c := &checker{
		fset:    fset,
		aliases: importAliases(file),
		relPath: relPath,
		isTest:  strings.HasSuffix(path, "_test.go"),
		allows:  make(map[int]*allowComment),
	}
	c.collectAllows(file)

	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncDecl:
			c.checkFuncDecl(typed)
		case *ast.CallExpr:
			c.checkCall(typed)
		case *ast.BinaryExpr:
			c.checkBinary(typed)
		}
		return true
	})
	c.finishAllows()

	return c.violations, nil
}

func (c *checker) checkFuncDecl(fn *ast.FuncDecl) {
	if fn.Name == nil || !isAdHocPathGuardName(fn.Name.Name) {
		return
	}
	if c.relPath == "internal/pathing/pathutil.go" {
		return
	}
	c.addViolation(fn.Name.Pos(), "use internal/pathing for local path containment/equality instead of ad hoc filepath.Rel/string checks")
}

func (c *checker) collectAllows(file *ast.File) {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			body := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(comment.Text, "//"), "/*"))
			body = strings.TrimSuffix(body, "*/")
			if !strings.HasPrefix(body, "pathpolicy:allow") {
				continue
			}
			reason := strings.TrimSpace(strings.TrimPrefix(body, "pathpolicy:allow"))
			if reason == "" {
				c.violations = append(c.violations, violationAt(c.fset, c.relPath, comment.Pos(), "pathpolicy allow comment requires a reason"))
				continue
			}
			line := c.fset.Position(comment.Pos()).Line
			allow := &allowComment{line: line}
			c.allowList = append(c.allowList, allow)
			c.allows[line] = allow
			c.allows[line+1] = allow
		}
	}
}

func (c *checker) finishAllows() {
	for _, allow := range c.allowList {
		if !allow.used {
			c.violations = append(c.violations, Violation{
				File:    c.relPath,
				Line:    allow.line,
				Column:  1,
				Message: "unused pathpolicy allow comment",
			})
		}
	}
}

func (c *checker) checkCall(call *ast.CallExpr) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}

	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return
	}
	importPath := c.aliases[packageName.Name]
	switch importPath {
	case "path/filepath":
		c.checkFilepathCall(selector.Sel.Name, call)
	case "path":
		c.checkPathCall(selector.Sel.Name, call)
	case "fmt":
		if selector.Sel.Name == "Sprintf" {
			c.checkSprintf(call)
		}
	case "strings":
		switch selector.Sel.Name {
		case "Join":
			c.checkStringsJoin(call)
		case "Contains", "HasPrefix", "HasSuffix":
			c.checkStringPathAssertion(call)
		}
	default:
		if methods, ok := filesystemCalls[importPath]; ok {
			if _, checked := methods[selector.Sel.Name]; checked {
				c.checkFilesystemCall(call)
			}
		}
	}
}

func (c *checker) checkFilepathCall(name string, call *ast.CallExpr) {
	if _, checked := checkedFilepathCalls[name]; !checked {
		return
	}

	for _, arg := range call.Args {
		if value, ok := stringLiteralValue(arg); ok && isHardcodedLocalRoot(value) {
			c.addViolation(arg.Pos(), "avoid hardcoded OS-rooted local paths in filepath calls; build local paths from t.TempDir or existing variables")
		}
	}

	if name == "FromSlash" || name == "ToSlash" {
		return
	}
	for _, arg := range call.Args {
		if hasSlashDataPathSignal(arg) {
			c.addViolation(arg.Pos(), "slash-delimited torrent/API/URL paths must use path APIs or filepath.FromSlash at local filesystem boundaries")
		}
	}
}

func (c *checker) checkPathCall(name string, call *ast.CallExpr) {
	if _, checked := checkedPathCalls[name]; !checked {
		return
	}
	for _, arg := range call.Args {
		if hasLocalPathSignal(arg) && !hasSlashDataSignal(arg) {
			c.addViolation(arg.Pos(), "use filepath for local filesystem paths; path is only for slash-delimited data")
		}
	}
}

func (c *checker) checkSprintf(call *ast.CallExpr) {
	if len(call.Args) == 0 {
		return
	}
	format, ok := stringLiteralValue(call.Args[0])
	if !ok || !isPathBuildingFormat(format) {
		return
	}
	if hasSlashDataSignal(call) {
		return
	}
	if slices.ContainsFunc(call.Args[1:], hasLocalPathSignal) {
		c.addViolation(call.Pos(), "use filepath.Join instead of fmt.Sprintf to build local filesystem paths")
		return
	}
}

func (c *checker) checkStringsJoin(call *ast.CallExpr) {
	if len(call.Args) != 2 {
		return
	}
	separator, ok := stringLiteralValue(call.Args[1])
	if !ok || separator != "/" && separator != `\` {
		return
	}
	if hasLocalPathSignal(call.Args[0]) && !hasSlashDataSignal(call.Args[0]) {
		c.addViolation(call.Pos(), "use filepath.Join instead of strings.Join to build local filesystem paths")
	}
}

func (c *checker) checkStringPathAssertion(call *ast.CallExpr) {
	if !c.isTest || len(call.Args) < 2 {
		return
	}
	if isFilepathToSlashCall(call.Args[0]) {
		return
	}
	value, ok := stringLiteralValue(call.Args[1])
	if !ok || !isHardcodedLocalRoot(value) {
		return
	}
	if hasLocalPathSignal(call.Args[0]) {
		c.addViolation(call.Args[1].Pos(), "normalize local paths with filepath.ToSlash before slash-based test assertions")
	}
}

func (c *checker) checkFilesystemCall(call *ast.CallExpr) {
	if len(call.Args) == 0 {
		return
	}
	firstArg := call.Args[0]
	if hasSlashDataPathSignal(firstArg) {
		c.addViolation(firstArg.Pos(), "convert slash-delimited data with filepath.FromSlash before local filesystem calls")
	}
}

func (c *checker) checkBinary(expr *ast.BinaryExpr) {
	//nolint:exhaustive // Path policy only cares about string concatenation.
	switch expr.Op {
	case token.ADD:
		if !containsPathSeparatorLiteral(expr) {
			return
		}
		if hasLocalPathSignal(expr) && !hasSlashDataSignal(expr) {
			c.addViolation(expr.Pos(), "use filepath.Join instead of string concatenation to build local filesystem paths")
		}
	case token.EQL, token.NEQ:
		if c.relPath == "internal/pathing/pathutil.go" {
			return
		}
		if isRootTargetPathEquality(expr.X, expr.Y) {
			c.addViolation(expr.Pos(), "use pathutil.SamePath instead of lexical equality for local filesystem root/target paths")
		}
	default:
		return
	}
}

func (c *checker) addViolation(pos token.Pos, message string) {
	line := c.fset.Position(pos).Line
	if allow, ok := c.allows[line]; ok {
		allow.used = true
		return
	}

	c.violations = append(c.violations, violationAt(c.fset, c.relPath, pos, message))
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "dist", "node_modules", "vendor":
		return true
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

func stringLiteralValue(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
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

func isPathBuildingFormat(format string) bool {
	if strings.Contains(format, "://") {
		return false
	}
	return strings.Contains(format, "%s/") ||
		strings.Contains(format, "/%s") ||
		strings.Contains(format, `%s\`) ||
		strings.Contains(format, `\%s`)
}

func isHardcodedLocalRoot(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, `\\`) || strings.HasPrefix(trimmed, `\`) {
		return true
	}
	if hasWindowsDrivePrefix(trimmed) {
		return true
	}

	slashPath := strings.ReplaceAll(trimmed, `\`, "/")
	for _, root := range []string{"/tmp", "/home", "/media", "/custom"} {
		//pathpolicy:allow checking slash-normalized roots
		if slashPath == root || strings.HasPrefix(slashPath, root+"/") {
			return true
		}
	}

	return false
}

func hasWindowsDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	return (value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')
}

func containsPathSeparatorLiteral(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found {
			return false
		}
		value, ok := stringLiteralValueFromNode(node)
		if ok && (strings.Contains(value, "/") || strings.Contains(value, `\`)) {
			found = true
			return false
		}
		return true
	})
	return found
}

func isFilepathToSlashCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ToSlash" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "filepath"
}

func hasLocalPathSignal(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found {
			return false
		}
		switch typed := node.(type) {
		case *ast.Ident:
			if isLocalPathName(typed.Name) {
				found = true
				return false
			}
		case *ast.SelectorExpr:
			if isLocalPathName(typed.Sel.Name) {
				found = true
				return false
			}
		case *ast.CallExpr:
			if isLocalPathCall(typed) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func hasSlashDataPathSignal(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found {
			return false
		}
		switch typed := node.(type) {
		case *ast.Ident:
			if isSlashDataPathName(typed.Name) {
				found = true
				return false
			}
		case *ast.SelectorExpr:
			if isSlashDataSelector(typed) {
				found = true
				return false
			}
			if isSlashDataPathName(typed.Sel.Name) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func hasSlashDataSignal(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found {
			return false
		}
		if value, ok := stringLiteralValueFromNode(node); ok {
			if strings.Contains(value, "://") {
				found = true
				return false
			}
			return true
		}
		switch typed := node.(type) {
		case *ast.Ident:
			if isSlashDataName(typed.Name) {
				found = true
				return false
			}
		case *ast.SelectorExpr:
			if isSlashDataSelector(typed) {
				found = true
				return false
			}
			if isSlashDataName(typed.Sel.Name) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func stringLiteralValueFromNode(node ast.Node) (string, bool) {
	expr, ok := node.(ast.Expr)
	if !ok {
		return "", false
	}
	return stringLiteralValue(expr)
}

func isLocalPathCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok {
		if selector.Sel.Name == "TempDir" {
			return true
		}
		if pkg, ok := selector.X.(*ast.Ident); ok {
			switch pkg.Name + "." + selector.Sel.Name {
			case "os.TempDir", "os.UserHomeDir", "os.Getwd":
				return true
			}
			if pkg.Name == "filepath" {
				return true
			}
		}
		return isLocalPathName(selector.Sel.Name)
	}
	if ident, ok := call.Fun.(*ast.Ident); ok {
		return isLocalPathName(ident.Name)
	}
	return false
}

func isLocalPathName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || isSlashDataName(lower) {
		return false
	}
	if strings.Contains(lower, "filepath") || strings.Contains(lower, "dbpath") || strings.Contains(lower, "sourcepath") ||
		strings.Contains(lower, "targetpath") ||
		strings.Contains(lower, "outputpath") ||
		strings.Contains(lower, "artifactpath") ||
		strings.Contains(lower, "configpath") ||
		strings.Contains(lower, "mediainfopath") {
		return true
	}
	if strings.Contains(lower, "pathparts") || strings.Contains(lower, "fileparts") || strings.Contains(lower, "dirparts") {
		return true
	}
	for _, signal := range []string{"dir", "root", "folder", "file", "tmp", "temp", "watch", "browse", "destination"} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func isSlashDataPathName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(lower, "slashpath") ||
		strings.Contains(lower, "torrentcontentpath") ||
		strings.Contains(lower, "apipath") ||
		strings.Contains(lower, "urlpath") ||
		strings.Contains(lower, "remotepath") ||
		strings.Contains(lower, "payloadpath") ||
		strings.Contains(lower, "requestpath") ||
		strings.Contains(lower, "responsepath")
}

func isSlashDataName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if isSlashDataPathName(lower) {
		return true
	}
	for _, signal := range []string{"url", "uri", "endpoint", "route", "href", "html", "bbcode", "desc", "description", "announce", "domain", "host", "link", "web", "raw", "img", "image", "api", "query", "request", "response", "cookie", "form"} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func isSlashDataSelector(selector *ast.SelectorExpr) bool {
	if selector.Sel.Name != "Path" && selector.Sel.Name != "RawPath" && selector.Sel.Name != "EscapedPath" {
		return false
	}
	return selectorChainHasName(selector.X, "url") ||
		selectorChainHasName(selector.X, "uri") ||
		selectorChainHasName(selector.X, "parsed") ||
		selectorChainHasName(selector.X, "request") ||
		selectorChainHasName(selector.X, "response") ||
		selectorChainHasName(selector.X, "req") ||
		selectorChainHasName(selector.X, "resp")
}

func selectorChainHasName(expr ast.Expr, needle string) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		return strings.Contains(strings.ToLower(typed.Name), needle)
	case *ast.SelectorExpr:
		return strings.Contains(strings.ToLower(typed.Sel.Name), needle) || selectorChainHasName(typed.X, needle)
	default:
		return false
	}
}

func isAdHocPathGuardName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "iswithinroot", "ispathwithinroot", "pathwithinroot", "pathwithin", "samepath":
		return true
	default:
		return false
	}
}

func isRootTargetPathEquality(left ast.Expr, right ast.Expr) bool {
	leftName, leftOK := rootTargetPathName(left)
	rightName, rightOK := rootTargetPathName(right)
	if !leftOK || !rightOK {
		return false
	}
	return isRootPathName(leftName) && isTargetPathName(rightName) ||
		isTargetPathName(leftName) && isRootPathName(rightName)
}

func rootTargetPathName(expr ast.Expr) (string, bool) {
	switch typed := expr.(type) {
	case *ast.Ident:
		return pathGuardEqualityName(typed.Name)
	case *ast.SelectorExpr:
		return pathGuardEqualityName(typed.Sel.Name)
	}
	return "", false
}

func pathGuardEqualityName(name string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || isSlashDataName(lower) {
		return "", false
	}
	if isRootPathName(lower) || isTargetPathName(lower) || isLocalPathName(lower) {
		return lower, true
	}
	return "", false
}

func isRootPathName(name string) bool {
	return strings.Contains(name, "root")
}

func isTargetPathName(name string) bool {
	return strings.Contains(name, "target") || strings.Contains(name, "candidate")
}
