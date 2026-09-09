// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package architecturepolicy

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
)

// providerMetadataReviews records reviewed provider-evidence boundaries. Hashes
// cover syntax, excluding positions and comments, so changing a guarded consumer
// requires a new review of manual precedence and its callers before updating it.
//
//go:embed provider_metadata_reviews.json
var providerMetadataReviews []byte

type providerMetadataReview struct {
	SHA256 string `json:"sha256"`
	Reason string `json:"reason"`
}

type providerMetadataConsumer struct {
	key     string
	file    string
	decl    ast.Decl
	imports map[string]string
	symbols []string
	guarded bool
}

func checkProviderMetadata(root string) ([]Violation, error) {
	var reviews map[string]providerMetadataReview
	if err := json.Unmarshal(providerMetadataReviews, &reviews); err != nil {
		return nil, fmt.Errorf("decode provider metadata reviews: %w", err)
	}
	fset := token.NewFileSet()
	consumers, err := collectProviderMetadataConsumers(root, fset)
	if err != nil {
		return nil, err
	}
	return verifyProviderMetadataConsumers(consumers, reviews, fset)
}

func collectProviderMetadataConsumers(root string, fset *token.FileSet) ([]providerMetadataConsumer, error) {
	var consumers []providerMetadataConsumer
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
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
			return fmt.Errorf("resolve provider consumer path: %w", err)
		}
		relative = filepath.ToSlash(relative)
		if !strings.HasPrefix(relative, "internal/trackers/impl/") || !strings.HasSuffix(relative, ".go") || strings.HasSuffix(relative, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse provider consumer %s: %w", relative, err)
		}
		imports := make(map[string]string, len(file.Imports))
		for _, imported := range file.Imports {
			importPath := strings.Trim(imported.Path.Value, "\"")
			name := importPath[strings.LastIndex(importPath, "/")+1:]
			if imported.Name != nil {
				name = imported.Name.Name
			}
			imports[name] = strings.TrimPrefix(importPath, "github.com/autobrr/upbrr/")
		}
		for _, decl := range file.Decls {
			names, err := providerDeclarationNames(decl)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				continue
			}
			consumers = append(consumers, providerMetadataConsumer{
				key:     relative + ":" + strings.Join(names, ","),
				file:    relative,
				decl:    decl,
				imports: imports,
				symbols: names,
				guarded: containsProviderMetadata(decl),
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan provider consumers: %w", err)
	}
	return consumers, nil
}

func providerDeclarationNames(decl ast.Decl) ([]string, error) {
	switch node := decl.(type) {
	case *ast.FuncDecl:
		name := node.Name.Name
		if node.Recv != nil && len(node.Recv.List) > 0 {
			var receiver bytes.Buffer
			if err := format.Node(&receiver, token.NewFileSet(), node.Recv.List[0].Type); err != nil {
				return nil, fmt.Errorf("format provider consumer receiver: %w", err)
			}
			name = receiver.String() + "." + name
		}
		return []string{name}, nil
	case *ast.GenDecl:
		var names []string
		for _, spec := range node.Specs {
			switch value := spec.(type) {
			case *ast.ValueSpec:
				for _, name := range value.Names {
					names = append(names, name.Name)
				}
			case *ast.TypeSpec:
				names = append(names, value.Name.Name)
			}
		}
		return names, nil
	default:
		return nil, nil
	}
}

func containsProviderMetadata(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		if name, ok := node.(*ast.Ident); ok {
			switch name.Name {
			case "ProviderMetadata", "EffectiveMetadata", "SourceScopedMetadata", "TMDBMetadata", "TMDBLocalizedData", "IMDBMetadata", "IMDBAKA",
				"TVDBMetadata", "TVDBEpisodeMetadata", "TVDBNameDisambiguation", "TVmazeMetadata", "BlurayMetadata", "AniListMetadata", "PreferredTitle", "PreferredAlternateTitle",
				"PreferredOriginalTitle", "PreferredYear", "PreferredGenres", "PreferredGenreText", "PreferredOriginalLanguage", "PreferredDistributor":
				found = true
			}
		}
		return !found
	})
	return found
}

func verifyProviderMetadataConsumers(
	consumers []providerMetadataConsumer,
	reviews map[string]providerMetadataReview,
	fset *token.FileSet,
) ([]Violation, error) {
	// Follow references as well as calls: callbacks and aliases can carry raw
	// provider values across a primitive return type or a narrowed projection.
	guardedSymbols := make(map[string]bool)
	for changed := true; changed; {
		changed = false
		for i := range consumers {
			consumer := &consumers[i]
			directory := filepath.ToSlash(filepath.Dir(consumer.file))
			if !consumer.guarded {
				ast.Inspect(consumer.decl, func(node ast.Node) bool {
					switch value := node.(type) {
					case *ast.Ident:
						consumer.guarded = consumer.guarded || guardedSymbols[directory+":"+value.Name]
					case *ast.SelectorExpr:
						if name, ok := value.X.(*ast.Ident); ok {
							consumer.guarded = consumer.guarded || guardedSymbols[consumer.imports[name.Name]+":"+value.Sel.Name]
						}
					}
					return !consumer.guarded
				})
			}
			if consumer.guarded && providerValueReturn(consumer.decl) {
				for _, symbol := range consumer.symbols {
					// Receiver method references are conservatively scoped to their package.
					symbol = symbol[strings.LastIndex(symbol, ".")+1:]
					key := directory + ":" + symbol
					if !guardedSymbols[key] {
						guardedSymbols[key] = true
						changed = true
					}
				}
			}
		}
	}
	var violations []Violation
	for _, consumer := range consumers {
		review, reviewed := reviews[consumer.key]
		if !consumer.guarded && !reviewed {
			continue
		}
		fingerprint, err := providerMetadataFingerprint(consumer.decl)
		if err != nil {
			return nil, err
		}
		if !reviewed || fingerprint != review.SHA256 || strings.TrimSpace(review.Reason) == "" {
			position := fset.Position(consumer.decl.Pos())
			violations = append(violations, Violation{
				File:    consumer.file,
				Line:    position.Line,
				Column:  position.Column,
				Message: "provider metadata consumer requires manual-precedence review: " + consumer.key,
			})
		}
	}
	return violations, nil
}

// Provider subject fields remain independently detectable at their consumers.
// Follow erased scalar/container results without pulling unrelated operation and
// registry construction into the provider-value boundary.
func providerValueReturn(decl ast.Decl) bool {
	function, ok := decl.(*ast.FuncDecl)
	if !ok {
		return true // A function value may be stored in a package declaration.
	}
	if function.Type.Results == nil {
		return false
	}
	for _, result := range function.Type.Results.List {
		switch value := result.Type.(type) {
		case *ast.ArrayType, *ast.MapType, *ast.InterfaceType, *ast.StructType, *ast.FuncType:
			return true
		case *ast.Ident:
			switch value.Name {
			case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64", "any":
				return true
			}
		}
	}
	return false
}

func providerMetadataFingerprint(node ast.Node) (string, error) {
	var syntax bytes.Buffer
	err := ast.Fprint(&syntax, nil, node, func(name string, value reflect.Value) bool {
		return name != "Doc" && name != "Comment" && value.Type() != reflect.TypeFor[token.Pos]()
	})
	if err != nil {
		return "", fmt.Errorf("fingerprint provider consumer: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(syntax.Bytes())), nil
}
