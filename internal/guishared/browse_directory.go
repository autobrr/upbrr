// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guishared

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/pkg/api"
)

func BrowseDirectory(req api.BrowseDirectoryRequest, fallbackPath string) (api.BrowseDirectoryResponse, error) {
	return browseDirectory(req, fallbackPath, "")
}

func BrowseDirectoryWithinRoot(req api.BrowseDirectoryRequest, fallbackPath string, rootPath string) (api.BrowseDirectoryResponse, error) {
	return browseDirectory(req, fallbackPath, rootPath)
}

func BrowseDirectoryWithinRoots(req api.BrowseDirectoryRequest, fallbackPath string, rootPaths []string) (api.BrowseDirectoryResponse, error) {
	return browseDirectoryWithinRoots(req, fallbackPath, rootPaths)
}

func browseDirectory(req api.BrowseDirectoryRequest, fallbackPath string, rootPath string) (api.BrowseDirectoryResponse, error) {
	roots := []string{}
	if strings.TrimSpace(rootPath) != "" {
		roots = []string{rootPath}
	}
	return browseDirectoryWithinRoots(req, fallbackPath, roots)
}

func browseDirectoryWithinRoots(req api.BrowseDirectoryRequest, fallbackPath string, rootPaths []string) (api.BrowseDirectoryResponse, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode != "file" && mode != "folder" {
		mode = "folder"
	}

	requested := strings.TrimSpace(req.Path)
	roots, err := normalizedBrowseRoots(rootPaths)
	if err != nil {
		return api.BrowseDirectoryResponse{}, err
	}
	if len(roots) > 1 && requested == "" {
		return browseConfiguredRoots(mode, roots), nil
	}
	root := ""
	if len(roots) == 1 {
		root = roots[0]
		if requested == "" {
			requested = root
		}
	}
	if len(roots) == 0 && runtime.GOOS == "windows" && requested == "" {
		return browseWindowsRoots(mode), nil
	}
	if requested == "" {
		requested = strings.TrimSpace(fallbackPath)
	}
	if requested == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return api.BrowseDirectoryResponse{}, err
		}
		requested = cwd
	}

	current, err := filepath.Abs(filepath.Clean(requested))
	if err != nil {
		return api.BrowseDirectoryResponse{}, err
	}
	info, err := os.Stat(current)
	if err != nil {
		return api.BrowseDirectoryResponse{}, err
	}
	if !info.IsDir() {
		current = filepath.Dir(current)
	}
	if len(roots) > 0 {
		current, err = filepath.EvalSymlinks(current)
		if err != nil {
			return api.BrowseDirectoryResponse{}, err
		}
		root = containingRoot(roots, current)
		if root == "" {
			return api.BrowseDirectoryResponse{}, errors.New("path is outside configured web browse roots")
		}
	}

	entries, err := os.ReadDir(current)
	if err != nil {
		return api.BrowseDirectoryResponse{}, err
	}

	items := make([]api.BrowseDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		entryInfo, err := entry.Info()
		if err != nil {
			continue
		}
		if !showBrowseEntry(mode, entry.Name(), entryInfo.IsDir()) {
			continue
		}
		itemPath := filepath.Join(current, entry.Name())
		if root != "" {
			itemRealPath, err := filepath.EvalSymlinks(itemPath)
			if err != nil || !pathWithin(root, itemRealPath) {
				continue
			}
		}
		items = append(items, api.BrowseDirectoryEntry{
			Name:       entry.Name(),
			Path:       itemPath,
			IsDir:      entryInfo.IsDir(),
			Size:       entryInfo.Size(),
			ModifiedAt: entryInfo.ModTime().UTC().Format(time.RFC3339),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	return api.BrowseDirectoryResponse{
		CurrentPath: current,
		ParentPath:  parentPathWithinRoots(current, root, roots),
		Mode:        mode,
		Entries:     items,
	}, nil
}

func showBrowseEntry(mode string, name string, isDir bool) bool {
	if isDir {
		return true
	}
	if mode == "folder" {
		return false
	}
	return filesystem.IsVideoFile(name)
}

func normalizedBrowseRoots(rootPaths []string) ([]string, error) {
	roots := make([]string, 0, len(rootPaths))
	for _, value := range rootPaths {
		root, err := normalizedBrowseRoot(value)
		if err != nil {
			return nil, err
		}
		if root == "" {
			continue
		}
		duplicate := false
		for _, existing := range roots {
			if samePath(existing, root) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			roots = append(roots, root)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return strings.ToLower(roots[i]) < strings.ToLower(roots[j])
	})
	return roots, nil
}

func normalizedBrowseRoot(rootPath string) (string, error) {
	trimmed := strings.TrimSpace(rootPath)
	if trimmed == "" {
		return "", nil
	}
	root, err := filepath.Abs(filepath.Clean(trimmed))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("configured web browse root is not a directory")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return root, nil
}

func browseConfiguredRoots(mode string, roots []string) api.BrowseDirectoryResponse {
	entries := make([]api.BrowseDirectoryEntry, 0, len(roots))
	names := make(map[string]int, len(roots))
	for _, root := range roots {
		name := filepath.Base(root)
		if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
			name = root
		}
		names[strings.ToLower(name)]++
		entries = append(entries, api.BrowseDirectoryEntry{
			Name:  name,
			Path:  root,
			IsDir: true,
		})
	}
	for i := range entries {
		if names[strings.ToLower(entries[i].Name)] > 1 {
			entries[i].Name = entries[i].Path
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return api.BrowseDirectoryResponse{
		CurrentPath: "",
		ParentPath:  "",
		Mode:        mode,
		Entries:     entries,
	}
}

func parentPathWithinRoot(current string, root string) string {
	if root != "" && samePath(current, root) {
		return ""
	}
	parent := parentPath(current)
	if root != "" && !pathWithin(root, parent) {
		return ""
	}
	return parent
}

func parentPathWithinRoots(current string, root string, roots []string) string {
	if len(roots) > 1 && root != "" && samePath(current, root) {
		return ""
	}
	return parentPathWithinRoot(current, root)
}

func containingRoot(roots []string, target string) string {
	for _, root := range roots {
		if pathWithin(root, target) {
			return root
		}
	}
	return ""
}

func parentPath(current string) string {
	parent := filepath.Dir(current)
	if parent == current || strings.TrimSpace(parent) == "." {
		if runtime.GOOS == "windows" {
			return ""
		}
		return current
	}
	return parent
}

func pathWithin(root string, target string) bool {
	if strings.TrimSpace(root) == "" {
		return true
	}
	if samePath(root, target) {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func samePath(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func browseWindowsRoots(mode string) api.BrowseDirectoryResponse {
	entries := make([]api.BrowseDirectoryEntry, 0, 26)
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err == nil {
			entries = append(entries, api.BrowseDirectoryEntry{
				Name:  root,
				Path:  root,
				IsDir: true,
			})
		}
	}
	return api.BrowseDirectoryResponse{
		CurrentPath: "",
		ParentPath:  "",
		Mode:        mode,
		Entries:     entries,
	}
}

func BrowseDirectoryFallback(dbPath string) string {
	trimmed := strings.TrimSpace(dbPath)
	if trimmed == "" {
		return ""
	}
	if info, err := os.Stat(trimmed); err == nil && info.IsDir() {
		return trimmed
	}
	dir := filepath.Dir(trimmed)
	if strings.TrimSpace(dir) == "." {
		return ""
	}
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	return ""
}

func ValidateBrowseSelection(path string, wantDir bool) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return errors.New("path is required")
	}
	info, err := os.Stat(trimmed)
	if err != nil {
		return err
	}
	if wantDir && !info.IsDir() {
		return errors.New("selected path is not a folder")
	}
	if !wantDir && info.IsDir() {
		return errors.New("selected path is not a file")
	}
	return nil
}
