// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package layout

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/filelock"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

const releaseOwnerFile = ".release-owner"

var releaseDirectoryMu sync.Mutex

// CanonicalSourcePath resolves existing aliases and normalizes host path semantics.
// Missing sources retain their absolute path so explicit History deletion works.
func CanonicalSourcePath(source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", errors.New("paths: source is required")
	}
	value, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("paths: resolve source: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err == nil {
		value = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("paths: resolve source aliases: %w", err)
	}
	value = filepath.Clean(value)
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value, nil
}

func releaseOwner(source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", nil
	}
	canonical, err := CanonicalSourcePath(source)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(canonical))), nil
}

// ReleaseTempPathFor resolves ownership without creating files. The usual basename
// is retained; a different owner with the same basename gets a source-key suffix.
// The second result remains the unsuffixed filename base. This is also the
// directory lookup used by explicit History cleanup.
func ReleaseTempPathFor(tmpRoot, source string, release api.ReleaseInfo) (string, string, error) {
	if strings.TrimSpace(tmpRoot) == "" {
		return "", "", errors.New("paths: tmp root is required")
	}
	base := ReleaseTempBaseFor(source, release)
	owner, err := releaseOwner(source)
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(tmpRoot, base)
	if pathing.SamePath(tmpRoot, dir) || !pathing.IsWithinRoot(tmpRoot, dir) {
		return "", "", errors.New("paths: invalid release directory")
	}
	// A colliding owner keeps its chosen directory even after the original
	// basename owner's History entry is deleted.
	if owner != "" {
		collisionBase := base[:min(len(base), 190)] + "-" + owner
		collisionDir := filepath.Join(tmpRoot, collisionBase)
		if !pathing.IsWithinRoot(tmpRoot, collisionDir) {
			return "", "", errors.New("paths: release owner directory escapes tmp root")
		}
		if stored, err := os.ReadFile(filepath.Join(collisionDir, releaseOwnerFile)); err == nil && string(stored) == owner {
			return collisionDir, base, nil
		}
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		// Cleanup must reach its existing source-media protection rather than
		// mistake a source file in tmp for an artifact directory.
		return dir, base, nil
	}
	stored, err := os.ReadFile(filepath.Join(dir, releaseOwnerFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("paths: read release owner: %w", err)
	}
	if err == nil && string(stored) != owner {
		dir = filepath.Join(tmpRoot, base[:min(len(base), 190)]+"-"+owner)
	}
	if !pathing.IsWithinRoot(tmpRoot, dir) {
		return "", "", errors.New("paths: release owner directory escapes tmp root")
	}
	return dir, base, nil
}

func claimReleaseDirectory(tmpRoot, source string, release api.ReleaseInfo) (string, string, error) {
	releaseDirectoryMu.Lock()
	defer releaseDirectoryMu.Unlock()
	if strings.TrimSpace(tmpRoot) == "" {
		return "", "", errors.New("paths: tmp root is required")
	}
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return "", "", fmt.Errorf("paths: create tmp root: %w", err)
	}
	// Shared layout metadata is not a release artifact. The OS releases the short
	// claim lock on process exit; it must not be unlinked while another process waits.
	lock, err := filelock.Acquire(filepath.Join(tmpRoot, ".release-layout.lock"))
	if err != nil {
		return "", "", fmt.Errorf("paths: claim release directory: %w", err)
	}
	defer lock.Close()
	dir, base, err := ReleaseTempPathFor(tmpRoot, source, release)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("paths: create release directory: %w", err)
	}
	owner, err := releaseOwner(source)
	if err != nil {
		return "", "", err
	}
	marker, err := pathing.OpenFileWithinRoot(dir, filepath.Join(dir, releaseOwnerFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	switch {
	case errors.Is(err, os.ErrExist):
		stored, readErr := os.ReadFile(filepath.Join(dir, releaseOwnerFile))
		if readErr != nil || string(stored) != owner {
			return "", "", errors.New("paths: release directory belongs to another source")
		}
	case err != nil:
		return "", "", fmt.Errorf("paths: create release owner: %w", err)
	default:
		_, writeErr := marker.WriteString(owner)
		closeErr := marker.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return "", "", fmt.Errorf("paths: write release owner: %w", err)
		}
	}
	return dir, base, nil
}
