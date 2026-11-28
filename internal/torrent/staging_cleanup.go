// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/autobrr/upbrr/internal/pathing"
)

func stagingSourceDirectories(ctx context.Context, source string, wanted []string) []os.FileInfo {
	var protected []os.FileInfo
	seen := make(map[string]bool)
	addAncestors := func(dir string) bool {
		for key := pathing.LexicalPathKey(dir); !seen[key]; key = pathing.LexicalPathKey(dir) {
			if ctx.Err() != nil {
				return false
			}
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() || !os.SameFile(info, info) {
				return false
			}
			protected = append(protected, info)
			seen[key] = true
			dir = filepath.Dir(dir)
		}
		return true
	}
	if !addAncestors(source) {
		return nil
	}
	for _, file := range wanted {
		if !addAncestors(filepath.Dir(file)) {
			return nil
		}
	}
	return protected
}

func staleStagingTreeDisjoint(ctx context.Context, dir, source string, protected []os.FileInfo) bool {
	if runtime.GOOS == "linux" {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return false
		}
		realDir, err := filepath.EvalSymlinks(absDir)
		if err != nil {
			return false
		}
		absSource, err := filepath.Abs(source)
		if err != nil {
			return false
		}
		realSource, err := filepath.EvalSymlinks(absSource)
		if err != nil {
			return false
		}
		table, err := os.ReadFile("/proc/self/mountinfo")
		if err != nil || !stagingMountsDisjoint(string(table), realDir, realSource) {
			return false
		}
	}
	var parent os.FileInfo
	if runtime.GOOS != "windows" {
		// The staging directory itself also must not be a mounted volume.
		info, err := os.Stat(filepath.Dir(dir))
		if err != nil {
			return false
		}
		parent = info
	}
	// WalkDir uses Lstat semantics. Symlinks and junctions are not traversed,
	// and regular hardlinks in the staging tree can safely be unlinked.
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("staging tree check canceled: %w", err)
		}
		if !entry.IsDir() {
			if path == dir {
				return fs.ErrInvalid
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("read staging directory identity: %w", err)
		}
		if !sameStagingDevice(parent, info) || !os.SameFile(info, info) {
			return fs.ErrInvalid
		}
		for _, sourceDir := range protected {
			if os.SameFile(info, sourceDir) {
				return fs.ErrInvalid
			}
		}
		return nil
	}) == nil
}

var mountPathUnescaper = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

func stagingMountsDisjoint(table, dir, source string) bool {
	type mount struct {
		device string
		root   string
		point  string
	}
	var mounts []mount
	relativeInside := func(root, target string) (string, bool) {
		// The kernel paths can name trees hidden by mounts. Use lexical
		// containment because resolving them through the VFS changes their meaning.
		rel, err := filepath.Rel(root, target)
		return rel, err == nil && filepath.IsLocal(rel)
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(table, "\n"), "\n") {
		fields := strings.SplitN(line, " ", 7)
		if len(fields) != 7 || !strings.Contains(fields[2], ":") {
			return false
		}
		root, point := mountPathUnescaper.Replace(fields[3]), mountPathUnescaper.Replace(fields[4])
		if !filepath.IsAbs(root) || !filepath.IsAbs(point) {
			return false
		}
		if _, inside := relativeInside(dir, point); inside {
			return false // Recursive deletion would descend into this mount.
		}
		mounts = append(mounts, mount{
			device: fields[2],
			root:   root,
			point:  point,
		})
	}
	coveringMounts := func(target string) []mount {
		// An overmount can hide a deeper mount that still appears in the
		// table. Keep every possible backing path for the overlap check.
		var covering []mount
		for _, m := range mounts {
			rel, inside := relativeInside(m.point, target)
			if !inside {
				continue
			}
			m.root = filepath.Join(m.root, rel)
			covering = append(covering, m)
		}
		return covering
	}
	dirMounts, sourceMounts := coveringMounts(dir), coveringMounts(source)
	if len(dirMounts) == 0 || len(sourceMounts) == 0 {
		return false
	}
	for _, backing := range dirMounts {
		for _, src := range sourceMounts {
			if backing.device != src.device {
				continue
			}
			_, sourceContainsStaging := relativeInside(src.root, backing.root)
			_, stagingContainsSource := relativeInside(backing.root, src.root)
			if sourceContainsStaging || stagingContainsSource {
				return false
			}
		}
		for _, alias := range mounts {
			if alias.device != backing.device {
				continue
			}
			if _, containsStaging := relativeInside(backing.root, alias.root); containsStaging {
				return false // The mount exposes the staging tree or one of its descendants.
			}
			if _, withinSource := relativeInside(source, alias.point); withinSource {
				if _, containsStaging := relativeInside(alias.root, backing.root); containsStaging {
					return false
				}
			}
		}
	}
	return true
}
