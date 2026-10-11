// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	browseSearchResultLimit = 200
	browseSearchEntryLimit  = 20000
	browseSearchTimeout     = 2 * time.Second
)

// browsePathsWithinRoots keeps legacy listings intact while making Roots an
// explicit scope even for a single configured root. An empty root list means
// the caller has already authorized unrestricted browsing.
func browsePathsWithinRoots(ctx context.Context, req api.BrowseDirectoryRequest, fallback string, rootPaths []string) (api.BrowseDirectoryResponse, error) {
	if req.Scope != "" && req.Scope != "directory" && req.Scope != "roots" {
		return api.BrowseDirectoryResponse{}, errors.New("invalid browse scope")
	}
	if req.Scope != "roots" {
		if req.Query != "" {
			return api.BrowseDirectoryResponse{}, errors.New("recursive search requires roots scope")
		}
		return browseDirectoryWithinRoots(req, fallback, rootPaths)
	}
	if req.Path != "" || len(req.Query) > 1024 {
		return api.BrowseDirectoryResponse{}, errors.New("roots search requires an empty path and a query of at most 1024 bytes")
	}
	if err := ctx.Err(); err != nil {
		return api.BrowseDirectoryResponse{}, fmt.Errorf("browse search: %w", err)
	}
	roots, err := normalizedBrowseRoots(rootPaths)
	if err != nil {
		return api.BrowseDirectoryResponse{}, err
	}
	mode := "folder"
	if strings.EqualFold(strings.TrimSpace(req.Mode), "file") {
		mode = "file"
	}
	if len(roots) == 0 {
		if runtime.GOOS == "windows" {
			for _, entry := range browseWindowsRoots(mode).Entries {
				roots = append(roots, entry.Path)
			}
		} else {
			roots = []string{string(filepath.Separator)}
		}
	}
	if normalizeBrowseName(req.Query) == "" {
		return browseConfiguredRoots(mode, roots), nil
	}
	searchCtx, cancel := context.WithTimeout(ctx, browseSearchTimeout)
	defer cancel()
	result := searchBrowseRoots(searchCtx, roots, mode, req.Query, browseSearchResultLimit, browseSearchEntryLimit)
	if err := ctx.Err(); err != nil {
		return api.BrowseDirectoryResponse{}, fmt.Errorf("browse search: %w", err)
	}
	return result, nil
}

// searchBrowseRoots reads directory entries in bounded batches, never follows
// directory symlinks, and deduplicates resolved paths across overlapping roots.
// Filesystem calls themselves are not interruptible; cancellation is checked
// between calls and entries rather than leaving detached traversal goroutines.
func searchBrowseRoots(ctx context.Context, roots []string, mode, query string, resultLimit, entryLimit int) api.BrowseDirectoryResponse {
	search := browseSearch{
		ctx:         ctx,
		matcher:     newBrowseNameMatcher(query),
		directories: make(map[string]bool),
		results:     make(map[string]bool),
		result: api.BrowseDirectoryResponse{
			Mode:    mode,
			Scope:   "roots",
			Entries: []api.BrowseDirectoryEntry{},
		},
		entryLimit:  entryLimit,
		resultLimit: resultLimit,
	}
	result := &search.result
	for _, entry := range browseConfiguredRoots(mode, roots).Entries {
		root := entry.Path
		if ctx.Err() != nil {
			result.Incomplete = true
			break
		}
		handle, err := os.OpenRoot(root)
		if err != nil {
			result.Incomplete = true
			continue
		}
		key := browsePathKey(root)
		if search.matcher.matches(entry.Name) && !search.results[key] {
			search.result.Entries = append(search.result.Entries, entry)
			search.results[key] = true
		}
		search.root(handle, root)
		_ = handle.Close()
		if search.stopped() {
			result.Incomplete = true
			break
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		left, right := result.Entries[i], result.Entries[j]
		if left.IsDir != right.IsDir {
			return left.IsDir
		}
		if strings.EqualFold(left.Name, right.Name) {
			return left.Path < right.Path
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
	return *result
}

type browseSearch struct {
	ctx                              context.Context
	matcher                          browseNameMatcher
	directories, results             map[string]bool
	visited, entryLimit, resultLimit int
	result                           api.BrowseDirectoryResponse
}

func (s *browseSearch) stopped() bool {
	return s.visited >= s.entryLimit || len(s.result.Entries) >= s.resultLimit || s.ctx.Err() != nil
}

func (s *browseSearch) root(handle *os.Root, root string) {
	pending := []string{"."}
	for len(pending) > 0 && !s.stopped() {
		directory := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		key := browsePathKey(filepath.Join(root, directory))
		if s.directories[key] {
			continue
		}
		file, err := handle.Open(directory)
		if err != nil {
			s.result.Incomplete = true
			continue
		}
		s.directories[key] = true
		// Each open directory is closed before the next one, regardless of depth.
		s.directory(file, root, directory, &pending)
		_ = file.Close()
	}
}

func (s *browseSearch) directory(directory *os.File, root, relative string, pending *[]string) {
	for !s.stopped() {
		entries, err := directory.ReadDir(128)
		for _, entry := range entries {
			if s.stopped() {
				return
			}
			s.visited++
			child := filepath.Join(relative, entry.Name())
			path := filepath.Join(root, child)
			realPath, resolveErr := filepath.EvalSymlinks(path)
			if resolveErr != nil {
				s.result.Incomplete = true
				continue
			}
			if !pathutil.IsWithinRoot(root, realPath) {
				continue
			}
			key := browsePathKey(realPath)
			if entry.IsDir() {
				*pending = append(*pending, child)
			}
			if s.results[key] || !showBrowseEntry(s.result.Mode, entry.Name(), entry.IsDir()) || !s.matcher.matches(entry.Name()) {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				s.result.Incomplete = true
				continue
			}
			s.results[key] = true
			s.result.Entries = append(s.result.Entries, api.BrowseDirectoryEntry{
				Name:       entry.Name(),
				Path:       path,
				IsDir:      entry.IsDir(),
				Size:       info.Size(),
				ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
			})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.result.Incomplete = true
			}
			return
		}
	}
}

func browsePathKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

type browseNameMatcher struct {
	query  string
	words  []string
	folded []rune
}

func newBrowseNameMatcher(query string) browseNameMatcher {
	normalized := normalizeBrowseName(query)
	return browseNameMatcher{
		query:  normalized,
		words:  strings.Fields(normalized),
		folded: []rune(foldBrowseName(normalized)),
	}
}

func normalizeBrowseName(value string) string {
	value = strings.Map(func(r rune) rune {
		switch r {
		case '.', '_', '-', '[', ']', '(', ')', '{', '}':
			return ' '
		}
		return unicode.ToLower(r)
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func foldBrowseName(value string) string {
	return norm.NFC.String(strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFD.String(value)))
}

// matches mirrors qui's name matching, excluding its torrent-only glob, hash,
// tag and category searches. Fuzzy matches allow fewer than ten deleted runes,
// never substitutions or reordered characters. Accent folding is fuzzy-only.
func (m browseNameMatcher) matches(name string) bool {
	target := normalizeBrowseName(name)
	if strings.Contains(target, m.query) {
		return true
	}
	if len(m.words) > 1 {
		all := true
		for _, word := range m.words {
			if !strings.Contains(target, word) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	folded := []rune(foldBrowseName(target))
	// qui falls back to folded membership when the library's pre-fold byte
	// length check rejects a query that becomes shorter after normalization.
	if len(m.query) <= len(target) && len(folded)-len(m.folded) >= 10 {
		return false
	}
	next := 0
	for _, r := range folded {
		if next < len(m.folded) && m.folded[next] == r {
			next++
		}
	}
	return next == len(m.folded)
}
