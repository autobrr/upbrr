// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package architecturepolicy

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// checkArtifactOwnership tracks application tmp roots independently of feature
// names. Only layout resolves a first-level release owner; producers may derive
// nested paths from that owned directory. Path containment remains pathpolicy's job.
func checkArtifactOwnership(file *ast.File, relative string, add func(token.Pos, string)) {
	if strings.HasSuffix(relative, "_test.go") || strings.HasPrefix(relative, "internal/pathing/layout/") {
		return
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		// This read-only cleanup helper extracts an existing first-level owner from
		// recorded artifact references; it never creates an artifact root.
		if relative == "internal/core/history_cleanup.go" && fn.Name.Name == "resolveContentTmpRoot" {
			continue
		}
		roots := map[string]bool{}
		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				if artifactRootName(name.Name) {
					roots[name.Name] = true
				}
			}
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.ValueSpec:
				for i, rhs := range n.Values {
					if i < len(n.Names) && rawArtifactRoot(rhs, roots) {
						roots[n.Names[i].Name] = true
					}
				}
			case *ast.AssignStmt:
				for i, rhs := range n.Rhs {
					if i >= len(n.Lhs) {
						break
					}
					if name, ok := n.Lhs[i].(*ast.Ident); ok && rawArtifactRoot(rhs, roots) {
						roots[name.Name] = true
					}
					if call, ok := rhs.(*ast.CallExpr); ok {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok &&
							(sel.Sel.Name == "ReleaseTempDirFor" || sel.Sel.Name == "ReleaseTempDir" || sel.Sel.Name == "ReleaseTempPathFor") &&
							len(call.Args) > 0 {
							if name, ok := call.Args[0].(*ast.Ident); ok {
								roots[name.Name] = true
							}
						}
					}
				}
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok || len(n.Args) == 0 {
					break
				}
				switch sel.Sel.Name {
				case "Join", "Mkdir", "MkdirAll", "MkdirTemp", "CreateTemp", "OpenFile", "WriteFile":
					if rawArtifactRoot(n.Args[0], roots) {
						add(
							n.Pos(),
							"release artifact roots must come from pathing/layout.ReleaseTempDirFor; derive feature subdirectories only from that owner",
						)
					}
				}
			}
			return true
		})
	}
}

func artifactRootName(name string) bool {
	return strings.EqualFold(name, "tmpRoot") || strings.EqualFold(name, "tempRoot")
}

func rawArtifactRoot(expr ast.Expr, roots map[string]bool) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		return roots[value.Name]
	case *ast.SelectorExpr:
		return artifactRootName(value.Sel.Name) || value.Sel.Name == "TempRoot"
	case *ast.CallExpr:
		if sel, ok := value.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Subdir" && len(value.Args) > 1 {
			if literal, ok := value.Args[1].(*ast.BasicLit); ok {
				text, _ := strconv.Unquote(literal.Value)
				return text == "tmp"
			}
		}
	}
	return false
}
