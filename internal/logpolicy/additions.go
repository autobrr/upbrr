package logpolicy

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/scanner"
	"go/token"
	"go/types"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// AddedCounts counts Go logging source calls, not messages emitted at runtime.
type AddedCounts struct {
	Trace int `json:"TRACE"`
	Debug int `json:"DEBUG"`
	Info  int `json:"INFO"`
	Warn  int `json:"WARN"`
	Error int `json:"ERROR"`
}

var addedHunk = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// CountAdded counts calls touching added Go tokens in the merge-base-to-head
// diff. root must be checked out at head. Parsing or type errors fail analysis;
// a partial count is never returned. Platform-specific files are checked under
// the project's Linux, Windows and Darwin build contexts, including e2e, without
// running code. Calls recognized in any matching context are counted once.
func CountAdded(ctx context.Context, root, base, head string) (AddedCounts, error) {
	var counts AddedCounts
	current, err := countGit(ctx, root, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(current)) != head {
		return counts, errors.New("logging count: source is not checked out at requested head")
	}
	names, err := countGit(ctx, root, "diff", "--name-status", "--find-renames", "--diff-filter=AMR", "-z", base+"..."+head, "--", "*.go")
	if err != nil {
		return counts, err
	}
	added := make(map[string]map[int]bool)
	entries := strings.Split(strings.TrimRight(string(names), "\x00"), "\x00")
	for len(entries) > 1 {
		status, name := entries[0], entries[1]
		entries = entries[2:]
		paths := []string{name}
		if strings.HasPrefix(status, "R") {
			if len(entries) == 0 {
				return AddedCounts{}, errors.New("logging count: incomplete rename metadata")
			}
			name = entries[0]
			entries = entries[1:]
			paths = append(paths, name)
		}
		args := append([]string{"diff", "--no-ext-diff", "--no-textconv", "--text", "--find-renames", "--unified=0", base + "..." + head, "--"}, paths...)
		patch, err := countGit(ctx, root, args...)
		if err != nil {
			return AddedCounts{}, err
		}
		lines := addedLines(string(patch))
		if len(lines) > 0 {
			added[filepath.Join(root, filepath.FromSlash(name))] = lines
		}
	}
	uncompiled := maps.Clone(added)
	seen := make(map[string]bool)
	for _, tags := range [][]string{nil, {"e2e"}} {
		for _, platform := range []string{"linux", "windows", "darwin"} {
			buildContext := build.Default
			buildContext.GOOS = platform
			buildContext.CgoEnabled = false
			buildContext.BuildTags = tags
			patterns := make([]string, 0, len(added))
			for name := range added {
				matches, err := buildContext.MatchFile(filepath.Dir(name), filepath.Base(name))
				if err != nil {
					return AddedCounts{}, fmt.Errorf("logging count: cannot determine Go build context: %w", err)
				}
				if !matches {
					continue
				}
				patterns = append(patterns, "file="+name)
			}
			if len(patterns) == 0 {
				continue
			}
			pkgs, err := packages.Load(&packages.Config{
				Context:    ctx,
				Dir:        root,
				Tests:      true,
				BuildFlags: []string{"-tags=" + strings.Join(tags, ",")},
				Env:        append(os.Environ(), "GOOS="+platform, "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=", "GOPACKAGESDRIVER=off"),
				Mode:       packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
			}, patterns...)
			if err != nil {
				return AddedCounts{}, countContextError("load", platform, tags, 1)
			}
			for _, pkg := range pkgs {
				if len(pkg.Errors) > 0 {
					category := "list"
					for _, problem := range pkg.Errors {
						switch problem.Kind {
						case packages.ParseError:
							category = "parse"
						case packages.TypeError:
							category = "type"
						case packages.UnknownError, packages.ListError:
							continue
						}
					}
					return AddedCounts{}, countContextError(category, platform, tags, len(pkg.Errors))
				}
				for _, file := range pkg.Syntax {
					name := pkg.Fset.Position(file.Pos()).Filename
					lines, ok := added[name]
					if !ok {
						continue
					}
					tokens, err := addedTokens(pkg.Fset, file, name, lines)
					if err != nil {
						return AddedCounts{}, err
					}
					ast.Inspect(file, func(node ast.Node) bool {
						call, ok := node.(*ast.CallExpr)
						if !ok {
							return true
						}
						i, _ := slices.BinarySearch(tokens, call.Pos())
						if i == len(tokens) || tokens[i] >= call.End() {
							return true
						}
						selector, ok := call.Fun.(*ast.SelectorExpr)
						if !ok || !typedLoggerCall(pkg.TypesInfo, call) {
							return true
						}
						level := selector.Sel.Name
						key := name + ":" + strconv.Itoa(pkg.Fset.Position(call.Pos()).Offset)
						if seen[key] {
							return true
						}
						seen[key] = true
						switch level {
						case "Tracef":
							counts.Trace++
						case "Debugf":
							counts.Debug++
						case "Infof":
							counts.Info++
						case "Warnf":
							counts.Warn++
						case "Errorf":
							counts.Error++
						}
						return true
					})
					// Test variants can contain the same file more than once.
				}
			}
			for name := range uncompiled {
				if slices.ContainsFunc(pkgs, func(pkg *packages.Package) bool { return slices.Contains(pkg.CompiledGoFiles, name) }) {
					delete(uncompiled, name)
				}
			}
		}
	}
	if len(uncompiled) > 0 {
		return AddedCounts{}, countContextError("unsupported-context", "all", []string{"all"}, len(uncompiled))
	}
	return counts, nil
}

// countContextError exposes only fixed classifications and numeric counts;
// package diagnostics can contain source text, private paths or credentials.
func countContextError(category, platform string, tags []string, count int) error {
	tag := "default"
	if len(tags) > 0 {
		tag = tags[0]
	}
	return fmt.Errorf("logging count: category=%s os=%s tags=%s count=%d", category, platform, tag, count)
}

func countGit(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("logging count: git %s: %w", args[0], err)
	}
	return out, nil
}

func addedLines(patch string) map[int]bool {
	lines := make(map[int]bool)
	line := 0
	inHunk := false
	for text := range strings.SplitSeq(patch, "\n") {
		if match := addedHunk.FindStringSubmatch(text); match != nil {
			line, _ = strconv.Atoi(match[1])
			inHunk = true
		} else if inHunk && strings.HasPrefix(text, "+") {
			lines[line] = true
			line++
		} else if inHunk && strings.HasPrefix(text, " ") {
			line++
		}
	}
	return lines
}

func addedTokens(fset *token.FileSet, file *ast.File, name string, lines map[int]bool) ([]token.Pos, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("logging count: read changed file: %w", err)
	}
	var scan scanner.Scanner
	scan.Init(fset.File(file.Pos()), data, nil, 0)
	var result []token.Pos
	for {
		pos, tok, literal := scan.Scan()
		if tok == token.EOF {
			return result, nil
		}
		if tok == token.SEMICOLON && literal == "\n" {
			continue
		}
		first := fset.Position(pos).Line
		last := first + strings.Count(literal, "\n")
		for line := first; line <= last; line++ {
			if lines[line] {
				result = append(result, pos)
				break
			}
		}
	}
}

func typedLoggerCall(info *types.Info, call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if _, ok := loggerMethods[selector.Sel.Name]; !ok {
		return false
	}
	selection := info.Selections[selector]
	if selection == nil {
		return false
	}
	receiver := selection.Recv()
	switch types.Unalias(receiver).Underlying().(type) {
	case *types.Pointer, *types.Interface:
	default:
		// The type-checked call already establishes addressability when needed.
		receiver = types.NewPointer(receiver)
	}
	methods := types.NewMethodSet(receiver)
	required := make([]string, 0, len(loggerMethods))
	for name := range loggerMethods {
		required = append(required, name)
	}
	// Narrow logging-only interfaces are also project logger contracts (for
	// example eventHub's Debugf sink). Concrete unrelated methods are excluded.
	if _, ok := types.Unalias(selection.Recv()).Underlying().(*types.Interface); ok && methods.Len() < len(loggerMethods) {
		required = required[:0]
		for method := range methods.Methods() {
			name := method.Obj().Name()
			if _, ok := loggerMethods[name]; !ok {
				return false
			}
			required = append(required, name)
		}
	}
	for _, method := range required {
		item := methods.Lookup(nil, method)
		if item == nil {
			return false
		}
		sig, ok := item.Obj().Type().(*types.Signature)
		if !ok || !sig.Variadic() || sig.Params().Len() != 2 || sig.Results().Len() != 0 ||
			!types.Identical(
				sig.Params().At(0).Type(),
				types.Typ[types.String],
			) || !types.Identical(sig.Params().At(1).Type(), types.NewSlice(types.NewInterfaceType(nil, nil).Complete())) {
			return false
		}
	}
	return true
}
