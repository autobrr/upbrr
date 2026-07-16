// Package literalpolicy enforces readable layout for substantial keyed composite literals.
package literalpolicy

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const minimumKeyedElements = 3

const allowDirective = "//literalpolicy:allow"

var skippedDirectories = map[string]struct{}{
	".git":              {},
	"dist":              {},
	"node_modules":      {},
	"playwright-report": {},
	"test-results":      {},
	"tmp":               {},
}

// Violation describes a keyed composite literal that is not consistently multiline.
type Violation struct {
	Path   string
	Line   int
	Column int
}

// String returns the checker-compatible location and diagnostic.
func (v Violation) String() string {
	return fmt.Sprintf(
		"%s:%d:%d: keyed composite literals with %d or more elements must use one element per line",
		v.Path,
		v.Line,
		v.Column,
		minimumKeyedElements,
	)
}

// CheckRepository checks every non-generated Go source file below root.
func CheckRepository(root string) ([]Violation, error) {
	var violations []Violation
	err := walkGoFiles(root, func(path string, source []byte) error {
		found, _, err := inspectFile(root, path, source, false)
		violations = append(violations, found...)
		return err
	})
	return violations, err
}

// FixRepository makes substantial keyed composite literals consistently multiline.
func FixRepository(root string) (int, error) {
	fixed := 0
	err := walkGoFiles(root, func(path string, source []byte) error {
		_, updated, err := inspectFile(root, path, source, true)
		if err != nil || updated == nil {
			return err
		}
		//nolint:gosec // The path comes from the repository walk and is not supplied independently.
		if err := os.WriteFile(path, updated, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fixed++
		return nil
	})
	return fixed, err
}

func walkGoFiles(root string, visit func(string, []byte) error) error {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root {
				if _, skip := skippedDirectories[entry.Name()]; skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		//nolint:gosec // The checker intentionally reads Go files discovered inside the trusted repository root.
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if isGenerated(source) {
			return nil
		}
		return visit(path, source)
	})
	if err != nil {
		return fmt.Errorf("walk repository Go files: %w", err)
	}
	return nil
}

func isGenerated(source []byte) bool {
	return bytes.Contains(source[:min(len(source), 2048)], []byte("Code generated")) &&
		bytes.Contains(source[:min(len(source), 2048)], []byte("DO NOT EDIT."))
}

type insertion struct {
	offset int
	text   string
}

func inspectFile(root string, path string, source []byte, fix bool) ([]Violation, []byte, error) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, source, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var violations []Violation
	var insertions []insertion
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || !substantialKeyedLiteral(literal) || hasAllowDirective(files, literal, source) || consistentLayout(files, literal) {
			return true
		}
		position := files.Position(literal.Lbrace)
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relative = path
		}
		violations = append(violations, Violation{
			Path:   filepath.ToSlash(relative),
			Line:   position.Line,
			Column: position.Column,
		})
		if fix {
			insertions = append(insertions, layoutInsertions(files, literal, source)...)
		}
		return true
	})
	if !fix || len(insertions) == 0 {
		return violations, nil, nil
	}
	return violations, applyInsertions(source, insertions), nil
}

func hasAllowDirective(files *token.FileSet, literal *ast.CompositeLit, source []byte) bool {
	lines := bytes.Split(source, []byte("\n"))
	lineIndex := files.Position(literal.Lbrace).Line - 1
	for _, index := range []int{lineIndex, lineIndex - 1} {
		if index >= 0 && index < len(lines) && bytes.Contains(lines[index], []byte(allowDirective)) {
			return true
		}
	}
	return false
}

func substantialKeyedLiteral(literal *ast.CompositeLit) bool {
	if len(literal.Elts) < minimumKeyedElements {
		return false
	}
	for _, element := range literal.Elts {
		if _, ok := element.(*ast.KeyValueExpr); !ok {
			return false
		}
	}
	return true
}

func consistentLayout(files *token.FileSet, literal *ast.CompositeLit) bool {
	if files.Position(literal.Lbrace).Line == files.Position(literal.Elts[0].Pos()).Line {
		return false
	}
	for index := 1; index < len(literal.Elts); index++ {
		if files.Position(literal.Elts[index-1].End()).Line == files.Position(literal.Elts[index].Pos()).Line {
			return false
		}
	}
	return files.Position(literal.Elts[len(literal.Elts)-1].End()).Line != files.Position(literal.Rbrace).Line
}

func layoutInsertions(files *token.FileSet, literal *ast.CompositeLit, source []byte) []insertion {
	var result []insertion
	if files.Position(literal.Lbrace).Line == files.Position(literal.Elts[0].Pos()).Line {
		result = append(result, insertion{offset: files.Position(literal.Lbrace).Offset + 1, text: "\n"})
	}
	for index := 1; index < len(literal.Elts); index++ {
		previous := literal.Elts[index-1]
		current := literal.Elts[index]
		if files.Position(previous.End()).Line != files.Position(current.Pos()).Line {
			continue
		}
		start := files.Position(previous.End()).Offset
		end := files.Position(current.Pos()).Offset
		if comma := bytes.IndexByte(source[start:end], ','); comma >= 0 {
			result = append(result, insertion{offset: start + comma + 1, text: "\n"})
		}
	}
	last := literal.Elts[len(literal.Elts)-1]
	if files.Position(last.End()).Line == files.Position(literal.Rbrace).Line {
		start := files.Position(last.End()).Offset
		end := files.Position(literal.Rbrace).Offset
		separator := "\n"
		if bytes.IndexByte(source[start:end], ',') < 0 {
			separator = ",\n"
		}
		result = append(result, insertion{offset: end, text: separator})
	}
	return result
}

func applyInsertions(source []byte, insertions []insertion) []byte {
	sort.Slice(insertions, func(i, j int) bool { return insertions[i].offset > insertions[j].offset })
	seen := make(map[int]struct{}, len(insertions))
	updated := append([]byte(nil), source...)
	for _, edit := range insertions {
		if _, duplicate := seen[edit.offset]; duplicate {
			continue
		}
		seen[edit.offset] = struct{}{}
		updated = append(updated[:edit.offset], append([]byte(edit.text), updated[edit.offset:]...)...)
	}
	return updated
}
