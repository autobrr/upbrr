// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package sourcelayout resolves one requested preparation source into the
// local resource roots used while collecting release facts.
package sourcelayout

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// KindDiscParent identifies a directory containing a recognized disc root.
	KindDiscParent Kind = "disc_parent"
	// KindDiscRoot identifies a recognized disc root selected directly.
	KindDiscRoot Kind = "disc_root"
)

// Layout preserves requested-source identity while exposing derived local
// resource roots only to preparation internals.
type Layout struct {
	// SourcePath is the absolute, cleaned path originally selected by the caller.
	SourcePath string
	// Kind describes how SourcePath relates to ContentRoot.
	Kind Kind
	// DiscType is BDMV, DVD, or HDDVD for recognized disc layouts; otherwise empty.
	DiscType string
	// ContentRoot is the local file or directory used to collect source facts.
	ContentRoot string
	// BDMVRoot is the selected or discovered BDMV directory; otherwise empty.
	BDMVRoot string
	// DVDRoot is the selected or discovered VIDEO_TS or HVDVD_TS directory; otherwise empty.
	DVDRoot string
}

// ErrSourceNotFound identifies a requested preparation source that does not
// exist without exposing its local path through public failures.
var ErrSourceNotFound = errors.New("source layout: source not found")

// Resolve normalizes and validates sourcePath and derives any disc resource
// root without changing the source's canonical identity.
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
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Layout{}, ErrSourceNotFound
		}
		return Layout{}, fmt.Errorf("source layout: inspect source: %w", err)
	}
	if !info.IsDir() {
		return Layout{
			SourcePath:  abs,
			Kind:        KindFile,
			ContentRoot: abs,
		}, nil
	}

	markerPath, discType, err := findDiscMarker(ctx, abs)
	if err != nil {
		return Layout{}, err
	}
	if markerPath == "" {
		return Layout{
			SourcePath:  abs,
			Kind:        KindDirectory,
			ContentRoot: abs,
		}, nil
	}

	kind := KindDiscParent
	if pathutil.SamePath(abs, markerPath) {
		kind = KindDiscRoot
	}
	layout := Layout{
		SourcePath:  abs,
		Kind:        kind,
		DiscType:    discType,
		ContentRoot: markerPath,
	}
	switch discType {
	case "BDMV":
		layout.BDMVRoot = markerPath
	case "DVD", "HDDVD":
		layout.DVDRoot = markerPath
	}
	return layout, nil
}

// findDiscMarker checks root and its immediate children for a recognized disc directory.
func findDiscMarker(ctx context.Context, root string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", fmt.Errorf("source layout: scan canceled: %w", err)
	}
	if discType := markerDiscType(filepath.Base(root)); discType != "" {
		return filepath.Clean(root), discType, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", "", fmt.Errorf("source layout: read source: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", "", fmt.Errorf("source layout: scan canceled: %w", err)
		}
		if !entry.IsDir() {
			continue
		}
		if discType := markerDiscType(entry.Name()); discType != "" {
			return filepath.Join(root, entry.Name()), discType, nil
		}
	}
	return "", "", nil
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
