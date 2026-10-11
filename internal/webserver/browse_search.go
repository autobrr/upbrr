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
	"sync"
	"sync/atomic"
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

// searchBrowseRoots uses at most two filesystem-group workers with shared work
// limits, never follows directory symlinks, and deduplicates overlapping roots.
// Filesystem calls themselves are not interruptible; cancellation is checked
// between calls and entries rather than leaving detached traversal goroutines.
func searchBrowseRoots(ctx context.Context, roots []string, mode, query string, resultLimit, entryLimit int) api.BrowseDirectoryResponse {
	entries := browseConfiguredRoots(mode, roots).Entries
	groups := groupBrowseRoots(ctx, entries, browseVolumeKey)
	return searchBrowseGroups(ctx, groups, mode, query, resultLimit, entryLimit)
}

// groupBrowseRoots only permits parallel groups when every root has a known
// local filesystem identity and roots do not overlap. Otherwise the original
// serial order preserves containment scope and the selected result paths.
func groupBrowseRoots(
	ctx context.Context,
	entries []api.BrowseDirectoryEntry,
	identify func(context.Context, string) string,
) [][]api.BrowseDirectoryEntry {
	groups := make([][]api.BrowseDirectoryEntry, 0)
	indices := make(map[string]int)
	for i, entry := range entries {
		if ctx.Err() != nil {
			return nil
		}
		for _, previous := range entries[:i] {
			if ctx.Err() != nil {
				return nil
			}
			if pathutil.IsWithinRoot(previous.Path, entry.Path) || pathutil.IsWithinRoot(entry.Path, previous.Path) {
				return [][]api.BrowseDirectoryEntry{entries}
			}
		}
		key := identify(ctx, entry.Path)
		if key == "" {
			return [][]api.BrowseDirectoryEntry{entries}
		}
		index, exists := indices[key]
		if !exists {
			index = len(groups)
			indices[key] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], entry)
	}
	return groups
}

// walkBrowseGroups joins both workers before returning, including after
// cancellation while a filesystem operation is still blocked.
func walkBrowseGroups(ctx context.Context, groups [][]api.BrowseDirectoryEntry, walk func(context.Context, []api.BrowseDirectoryEntry)) {
	var next atomic.Int64
	var workers sync.WaitGroup
	for range min(2, len(groups)) {
		workers.Go(func() {
			for ctx.Err() == nil {
				index := int(next.Add(1) - 1)
				if index >= len(groups) {
					return
				}
				walk(ctx, groups[index])
			}
		})
	}
	workers.Wait()
}

func searchBrowseGroups(ctx context.Context, groups [][]api.BrowseDirectoryEntry, mode, query string, resultLimit, entryLimit int) api.BrowseDirectoryResponse {
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
	walkBrowseGroups(ctx, groups, search.group)
	if search.stopped() {
		search.incomplete()
	}
	result := &search.result
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
	mu                               sync.Mutex
	directories, results             map[string]bool
	visited, entryLimit, resultLimit int
	result                           api.BrowseDirectoryResponse
}

func (s *browseSearch) stopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stoppedLocked()
}

func (s *browseSearch) stoppedLocked() bool {
	return s.visited >= s.entryLimit || len(s.result.Entries) >= s.resultLimit || s.ctx.Err() != nil
}

func (s *browseSearch) visit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stoppedLocked() {
		return false
	}
	s.visited++
	return true
}

func (s *browseSearch) incomplete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result.Incomplete = true
}

func (s *browseSearch) claimDirectory(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.directories[key] {
		return false
	}
	s.directories[key] = true
	return true
}

func (s *browseSearch) addResult(key string, entry api.BrowseDirectoryEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.results[key] && len(s.result.Entries) < s.resultLimit && s.ctx.Err() == nil {
		s.results[key] = true
		s.result.Entries = append(s.result.Entries, entry)
	}
}

func (s *browseSearch) group(ctx context.Context, entries []api.BrowseDirectoryEntry) {
	for _, entry := range entries {
		if ctx.Err() != nil || s.stopped() {
			return
		}
		handle, err := os.OpenRoot(entry.Path)
		if err != nil {
			s.incomplete()
			continue
		}
		if s.matcher.matches(entry.Name) {
			s.addResult(browsePathKey(entry.Path), entry)
		}
		s.root(handle, entry.Path)
		_ = handle.Close()
	}
}

func (s *browseSearch) root(handle *os.Root, root string) {
	pending := []string{"."}
	for len(pending) > 0 && !s.stopped() {
		directory := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		file, err := handle.Open(directory)
		if err != nil {
			s.incomplete()
			continue
		}
		if s.claimDirectory(browsePathKey(filepath.Join(root, directory))) {
			// Each worker closes its directory before opening the next one.
			s.directory(file, root, directory, &pending)
		}
		_ = file.Close()
	}
}

func (s *browseSearch) directory(directory *os.File, root, relative string, pending *[]string) {
	for !s.stopped() {
		entries, err := directory.ReadDir(128)
		for _, entry := range entries {
			if !s.visit() {
				return
			}
			matches := showBrowseEntry(s.result.Mode, entry.Name(), entry.IsDir()) && s.matcher.matches(entry.Name())
			// Excluded files need no path resolution; directories still need traversal.
			if !entry.IsDir() && !matches {
				continue
			}
			child := filepath.Join(relative, entry.Name())
			path := filepath.Join(root, child)
			realPath, resolveErr := filepath.EvalSymlinks(path)
			if resolveErr != nil {
				s.incomplete()
				continue
			}
			if !pathutil.IsWithinRoot(root, realPath) {
				continue
			}
			if entry.IsDir() {
				*pending = append(*pending, child)
			}
			if !matches {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				s.incomplete()
				continue
			}
			s.addResult(browsePathKey(realPath), api.BrowseDirectoryEntry{
				Name:       entry.Name(),
				Path:       path,
				IsDir:      entry.IsDir(),
				Size:       info.Size(),
				ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
			})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.incomplete()
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
