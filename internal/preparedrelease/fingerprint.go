// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

type normalizedPrepareInput struct {
	input     api.PrepareInput
	sourceKey string
}

// normalizePrepareInput canonicalizes fact-producing values before inspection
// and compatibility hashing while preserving one-shot controls unchanged.
func normalizePrepareInput(input api.PrepareInput) (normalizedPrepareInput, error) {
	primary, err := normalizeSourcePath(input.SourcePath)
	if err != nil {
		return normalizedPrepareInput{}, err
	}
	input.SourcePath = primary
	input.Instructions.SourceLookup = strings.TrimSpace(input.Instructions.SourceLookup)
	input.Instructions.Playlist.Selected = normalizePlaylistSelection(input.Instructions.Playlist.Selected)
	input.Instructions.TrackerIDs = normalizeTrackerIDs(input.Instructions.TrackerIDs)
	input.Search.Client = normalizeOptionalString(input.Search.Client)
	return normalizedPrepareInput{input: input, sourceKey: canonicalSourceKey(primary)}, nil
}

// normalizeTrackerIDs lowercases tracker keys and drops blank keys and values.
func normalizeTrackerIDs(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			result[key] = value
		}
	}
	return result
}

// normalizeOptionalString trims a present value without collapsing it to nil.
func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	return &normalized
}

func normalizeSourcePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", internalerrors.ErrInvalidInput
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("prepared release: normalize source path: %w", err)
	}
	return filepath.Clean(abs), nil
}

func canonicalSourceKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.Clean(value)
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func normalizePlaylistSelection(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
		if value == "." || value == "" {
			continue
		}
		key := value
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// inspectSource inventories the validated layout and returns its canonical
// manifest plus a deterministic source fingerprint.
func inspectSource(ctx context.Context, input api.PrepareInput, layout sourcelayout.Layout) (api.SourceManifest, string, error) {
	normalized, err := normalizePrepareInput(input)
	if err != nil {
		return api.SourceManifest{}, "", err
	}
	input = normalized.input
	if canonicalSourceKey(layout.SourcePath) != canonicalSourceKey(input.SourcePath) {
		return api.SourceManifest{}, "", fmt.Errorf("prepared release: source layout differs from input: %w", internalerrors.ErrInvalidInput)
	}
	entries := make([]api.SourceManifestEntry, 0, 1)
	var total int64
	for _, root := range []string{input.SourcePath} {
		if err := ctx.Err(); err != nil {
			return api.SourceManifest{}, "", fmt.Errorf("prepared release: inventory canceled: %w", err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return api.SourceManifest{}, "", fmt.Errorf("prepared release: inspect source %s: %w", root, err)
		}
		if !info.IsDir() {
			entry := manifestEntry(root, info)
			entries = append(entries, entry)
			total += entry.Size
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("prepared release: inventory walk canceled: %w", err)
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				return fmt.Errorf("prepared release: inspect inventory entry %q: %w", path, infoErr)
			}
			item := manifestEntry(path, info)
			entries = append(entries, item)
			if item.Type == api.SourceEntryTypeFile || item.Type == api.SourceEntryTypePlaylist {
				total += item.Size
			}
			return nil
		})
		if err != nil {
			return api.SourceManifest{}, "", fmt.Errorf("prepared release: inventory source %s: %w", root, err)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		left := canonicalSourceKey(entries[i].Path)
		right := canonicalSourceKey(entries[j].Path)
		return left < right
	})

	selected := make([]api.PlaylistInfo, 0, len(input.Instructions.Playlist.Selected))
	for _, value := range input.Instructions.Playlist.Selected {
		selected = append(selected, api.PlaylistInfo{File: value})
	}
	manifest := api.SourceManifest{
		SourcePath:        input.SourcePath,
		Size:              total,
		Entries:           entries,
		SelectedPlaylists: selected,
		Classification:    classifyManifest(entries),
	}
	if manifest.Classification.DiscType == "" {
		manifest.Classification.DiscType = layout.DiscType
	}
	fingerprint, err := fingerprintJSON(sourceFingerprintPayload{
		SourcePath: canonicalSourceKey(input.SourcePath),
		Entries:    canonicalFingerprintEntries(entries),
		Playlists:  input.Instructions.Playlist,
	})
	if err != nil {
		return api.SourceManifest{}, "", fmt.Errorf("prepared release: fingerprint source: %w", err)
	}
	return manifest, fingerprint, nil
}

func manifestEntry(path string, info fs.FileInfo) api.SourceManifestEntry {
	entryType := api.SourceEntryTypeUnknown
	switch {
	case info.IsDir():
		entryType = api.SourceEntryTypeDirectory
	case info.Mode().IsRegular():
		entryType = api.SourceEntryTypeFile
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".mpls" || ext == ".ifo" {
		entryType = api.SourceEntryTypePlaylist
	}
	disc := ""
	upper := strings.ToUpper(filepath.ToSlash(path))
	switch {
	case strings.Contains(upper, "/BDMV/") || strings.HasSuffix(upper, "/BDMV"):
		disc = "BDMV"
	case strings.Contains(upper, "/VIDEO_TS/") || strings.HasSuffix(upper, "/VIDEO_TS"):
		disc = "DVD"
	}
	playlist := ""
	if entryType == api.SourceEntryTypePlaylist {
		playlist = filepath.Base(path)
	}
	return api.SourceManifestEntry{
		Path:       filepath.Clean(path),
		Type:       entryType,
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC(),
		Disc:       disc,
		Playlist:   playlist,
	}
}

func classifyManifest(entries []api.SourceManifestEntry) api.SourceClassification {
	classification := api.SourceClassification{}
	for _, entry := range entries {
		if classification.DiscType == "" && entry.Disc != "" {
			classification.DiscType = entry.Disc
		}
		if classification.Container == "" && entry.Type == api.SourceEntryTypeFile {
			classification.Container = strings.TrimPrefix(strings.ToLower(filepath.Ext(entry.Path)), ".")
		}
	}
	return classification
}

type sourceFingerprintPayload struct {
	SourcePath string
	Entries    []sourceFingerprintEntry
	Playlists  api.PlaylistInstruction
}

type sourceFingerprintEntry struct {
	Path         string
	Type         api.SourceEntryType
	Size         int64
	ModifiedNano int64
	Disc         string
	Playlist     string
}

func canonicalFingerprintEntries(entries []api.SourceManifestEntry) []sourceFingerprintEntry {
	result := make([]sourceFingerprintEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, sourceFingerprintEntry{
			Path:         canonicalSourceKey(entry.Path),
			Type:         entry.Type,
			Size:         entry.Size,
			ModifiedNano: entry.ModifiedAt.UTC().UnixNano(),
			Disc:         entry.Disc,
			Playlist:     filepath.ToSlash(entry.Playlist),
		})
	}
	return result
}

// preparationCompatibility fingerprints fact instructions and reusable policy;
// intent and one-shot controls are deliberately excluded.
func preparationCompatibility(input api.PrepareInput, sourceFingerprint string) (api.PreparationCompatibility, error) {
	factInstructions, err := fingerprintJSON(input.Instructions)
	if err != nil {
		return api.PreparationCompatibility{}, fmt.Errorf("prepared release: fingerprint fact instructions: %w", err)
	}
	policy, err := fingerprintJSON(struct {
		Policy api.PreparationPolicy
		Search api.ClientSearchPolicy
	}{Policy: input.Policy, Search: input.Search})
	if err != nil {
		return api.PreparationCompatibility{}, fmt.Errorf("prepared release: fingerprint policy: %w", err)
	}
	return api.PreparationCompatibility{
		SourceFingerprint:          sourceFingerprint,
		FactInstructionFingerprint: factInstructions,
		PolicyFingerprint:          policy,
		ContractVersion:            ContractVersion,
	}, nil
}

func fingerprintJSON(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("prepared release: marshal fingerprint input: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
