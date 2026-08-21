// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package sourcelayout resolves one requested preparation source into the
// local resource roots used while collecting release facts.
package sourcelayout

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
)

// Kind identifies how the requested source relates to its content root.
type Kind string

const (
	// KindFile identifies a source that is one regular file.
	KindFile Kind = "file"
	// KindDirectory identifies a non-disc directory source.
	KindDirectory Kind = "directory"
	// KindDiscParent identifies a directory containing one or more recognized disc roots.
	KindDiscParent Kind = "disc_parent"
	// KindDiscRoot identifies a recognized disc root selected directly.
	KindDiscRoot Kind = "disc_root"
)

// DiscResource is one preparation-private disc root within a selected source.
type DiscResource struct {
	ID   string
	Name string
	Type string
	Root string
}

// Layout preserves requested-source identity while exposing derived local
// resource roots only to preparation internals.
type Layout struct {
	// SourcePath is the absolute, cleaned path originally selected by the caller.
	SourcePath string
	// Kind describes how SourcePath relates to its discovered resources.
	Kind Kind
	// DiscType is BDMV, DVD, or HDDVD for a homogeneous recognized disc layout.
	DiscType string
	// Discs contains every recognized disc root in deterministic display order.
	Discs []DiscResource
}

var (
	// ErrSourceNotFound identifies a requested source that does not exist.
	ErrSourceNotFound = errors.New("source layout: source not found")
	// ErrDirectorySymlink identifies a selected or nested directory symlink.
	ErrDirectorySymlink = errors.New("source layout: directory symlinks are not supported")
	// ErrMixedDiscCollection identifies a collection containing different disc formats.
	ErrMixedDiscCollection = errors.New("source layout: mixed disc collections are not supported")
	// ErrUnsupportedHDDVDCollection identifies nested or multiple HD DVD roots.
	ErrUnsupportedHDDVDCollection = errors.New("source layout: HD DVD collections are not supported")
	// ErrConflictingDiscLayout identifies ambiguous or colliding disc marker identities.
	ErrConflictingDiscLayout = errors.New("source layout: conflicting disc layout")
)

type discoveredDisc struct {
	resource       DiscResource
	relativeMarker string
	relativeParent string
}

// Resolve normalizes and validates sourcePath and discovers every disc root
// without dereferencing directory symlinks or changing source identity.
func Resolve(ctx context.Context, sourcePath string) (Layout, error) {
	if ctx == nil {
		return Layout{}, errors.New("source layout: context is required")
	}
	trimmed := strings.TrimSpace(sourcePath)
	if trimmed == "" {
		return Layout{}, fmt.Errorf("source layout: source path is required: %w", internalerrors.ErrInvalidInput)
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return Layout{}, fmt.Errorf("source layout: normalize source: %w", err)
	}
	abs = filepath.Clean(abs)
	if err := ctx.Err(); err != nil {
		return Layout{}, fmt.Errorf("source layout: resolve canceled: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Layout{}, ErrSourceNotFound
		}
		return Layout{}, fmt.Errorf("source layout: inspect source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, statErr := os.Stat(abs)
		if statErr != nil {
			return Layout{}, fmt.Errorf("source layout: inspect source link: %w", statErr)
		}
		if target.IsDir() {
			return Layout{}, ErrDirectorySymlink
		}
		return Layout{SourcePath: abs, Kind: KindFile}, nil
	}
	if !info.IsDir() {
		return Layout{SourcePath: abs, Kind: KindFile}, nil
	}

	discs, err := discoverDiscs(ctx, abs)
	if err != nil {
		return Layout{}, err
	}
	if len(discs) == 0 {
		return Layout{SourcePath: abs, Kind: KindDirectory}, nil
	}
	if err := validateDiscSet(abs, discs); err != nil {
		return Layout{}, err
	}

	slices.SortFunc(discs, func(left, right discoveredDisc) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(filepath.ToSlash(left.relativeParent)), strings.ToLower(filepath.ToSlash(right.relativeParent))),
			cmp.Compare(filepath.ToSlash(left.relativeParent), filepath.ToSlash(right.relativeParent)),
			cmp.Compare(left.relativeMarker, right.relativeMarker),
		)
	})
	resources := make([]DiscResource, len(discs))
	for i := range discs {
		resources[i] = discs[i].resource
		if !usefulDiscLabel(discs[i].relativeParent) {
			resources[i].Name = fmt.Sprintf("Disc %d", i+1)
		}
	}

	kind := KindDiscParent
	if len(discs) == 1 && pathutil.SamePath(abs, discs[0].resource.Root) {
		kind = KindDiscRoot
	}
	return Layout{
		SourcePath: abs,
		Kind:       kind,
		DiscType:   discs[0].resource.Type,
		Discs:      resources,
	}, nil
}

func discoverDiscs(ctx context.Context, root string) ([]discoveredDisc, error) {
	discs := make([]discoveredDisc, 0, 1)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("source layout: scan canceled: %w", err)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, statErr := os.Stat(path)
			if statErr == nil && target.IsDir() {
				return ErrDirectorySymlink
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		discType := markerDiscType(entry.Name())
		if discType == "" {
			return nil
		}
		disc, err := newDiscoveredDisc(root, path, discType)
		if err != nil {
			return err
		}
		discs = append(discs, disc)
		return filepath.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("source layout: scan source: %w", err)
	}
	return discs, nil
}

func newDiscoveredDisc(sourceRoot string, markerRoot string, discType string) (discoveredDisc, error) {
	relativeMarker, err := filepath.Rel(sourceRoot, markerRoot)
	if err != nil {
		return discoveredDisc{}, fmt.Errorf("source layout: resolve disc identity: %w", err)
	}
	if relativeMarker == "." {
		relativeMarker = filepath.Base(markerRoot)
	}
	relativeMarker = filepath.Clean(relativeMarker)
	if !validRelativePath(relativeMarker) {
		return discoveredDisc{}, ErrConflictingDiscLayout
	}
	relativeParent := filepath.Clean(filepath.Dir(relativeMarker))
	canonicalMarker := strings.ToLower(filepath.ToSlash(relativeMarker))
	identity := strings.ToUpper(discType) + "\x00" + canonicalMarker
	digest := sha256.Sum256([]byte(identity))
	name := filepath.ToSlash(relativeParent)
	return discoveredDisc{
		resource: DiscResource{
			ID:   "disc-" + hex.EncodeToString(digest[:]),
			Name: name,
			Type: discType,
			Root: filepath.Clean(markerRoot),
		},
		relativeMarker: filepath.ToSlash(relativeMarker),
		relativeParent: name,
	}, nil
}

func validateDiscSet(sourceRoot string, discs []discoveredDisc) error {
	types := make(map[string]struct{}, len(discs))
	identities := make(map[string]struct{}, len(discs))
	for _, disc := range discs {
		if !pathutil.IsWithinRoot(sourceRoot, disc.resource.Root) {
			return ErrConflictingDiscLayout
		}
		types[disc.resource.Type] = struct{}{}
		if _, exists := identities[disc.resource.ID]; exists {
			return ErrConflictingDiscLayout
		}
		identities[disc.resource.ID] = struct{}{}
	}
	if _, hasHDDVD := types["HDDVD"]; hasHDDVD {
		if len(discs) != 1 || len(types) != 1 || !directOrImmediateMarker(sourceRoot, discs[0].resource.Root) {
			return ErrUnsupportedHDDVDCollection
		}
	}
	if len(types) != 1 {
		return ErrMixedDiscCollection
	}
	return nil
}

func validRelativePath(value string) bool {
	if value == "" || value == "." || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return false
	}
	for _, part := range strings.FieldsFunc(filepath.ToSlash(value), func(r rune) bool { return r == '/' }) {
		if part == ".." || part == "" {
			return false
		}
	}
	return true
}

func usefulDiscLabel(value string) bool {
	value = strings.TrimSpace(filepath.ToSlash(value))
	return value != "" && value != "."
}

func directOrImmediateMarker(sourceRoot string, markerRoot string) bool {
	return pathutil.SamePath(sourceRoot, markerRoot) || pathutil.SamePath(sourceRoot, filepath.Dir(markerRoot))
}

// markerDiscType maps a case-insensitive disc directory name to its canonical type.
func markerDiscType(name string) string {
	switch {
	case strings.EqualFold(name, "BDMV"):
		return "BDMV"
	case strings.EqualFold(name, "VIDEO_TS"):
		return "DVD"
	case strings.EqualFold(name, "HVDVD_TS"):
		return "HDDVD"
	default:
		return ""
	}
}
