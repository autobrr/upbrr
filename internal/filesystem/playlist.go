// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
)

// PlaylistInfo represents a discovered playlist file with its metrics and scoring.
type PlaylistInfo struct {
	File     string
	Duration float64
	Items    []PlaylistItem
	Score    float64
	Edition  string
}

// PlaylistItem represents a single file reference within a playlist.
type PlaylistItem struct {
	File string
	Size int64
}

// DiscoverPlaylists discovers and scores all MPLS playlists in a BDMV folder.
// Returns playlists sorted by score (highest first), or an error if the BDMV directory is invalid.
func DiscoverPlaylists(ctx context.Context, bdmvRoot string) ([]PlaylistInfo, error) {
	if bdmvRoot == "" {
		return nil, fmt.Errorf("playlist: empty path: %w", internalerrors.ErrInvalidInput)
	}

	playlistDir := filepath.Join(bdmvRoot, "PLAYLIST")
	info, err := os.Stat(playlistDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("playlist: directory %q not found: %w", playlistDir, internalerrors.ErrNotFound)
		}
		return nil, fmt.Errorf("playlist: stat %q: %w", playlistDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("playlist: %q is not a directory: %w", playlistDir, internalerrors.ErrInvalidInput)
	}

	entries, err := os.ReadDir(playlistDir)
	if err != nil {
		return nil, fmt.Errorf("playlist: read directory %q: %w", playlistDir, err)
	}

	var playlists []PlaylistInfo
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), ".MPLS") {
			continue
		}

		mpslPath := filepath.Join(playlistDir, name)
		duration, items, err := ParseMPLS(mpslPath)
		if err != nil {
			return nil, fmt.Errorf("playlist: parse %q: %w", name, err)
		}

		// Calculate playlist score using weighted algorithm.
		score := calculatePlaylistScore(duration, items)

		playlists = append(playlists, PlaylistInfo{
			File:     name,
			Duration: duration,
			Items:    items,
			Score:    score,
			Edition:  "",
		})
	}

	if len(playlists) == 0 {
		return nil, fmt.Errorf("playlist: no MPLS files found in %q: %w", playlistDir, internalerrors.ErrNotFound)
	}

	// Sort by score descending (highest score first).
	sort.Slice(playlists, func(i, j int) bool {
		return playlists[i].Score > playlists[j].Score
	})

	return playlists, nil
}

// calculatePlaylistScore computes a weighted score for playlist ranking.
// Matches Python algorithm: largest file 40%, total size 30%, duration 20%, file concentration 10%.
func calculatePlaylistScore(duration float64, items []PlaylistItem) float64 {
	if len(items) == 0 {
		return 0.0
	}

	// Normalize metrics.
	maxFileSize := 100.0 * 1024 * 1024 * 1024  // 100 GB
	maxTotalSize := 150.0 * 1024 * 1024 * 1024 // 150 GB
	maxDuration := 14400.0                     // 4 hours in seconds

	// Deduplicate by filename so that looping playlists (which repeat a small set
	// of files many times) don't inflate the largest-file and total-size metrics.
	uniqueSizes := make(map[string]int64)
	for _, item := range items {
		if sz, seen := uniqueSizes[item.File]; !seen || item.Size > sz {
			uniqueSizes[item.File] = item.Size
		}
	}

	var largestFile int64
	var totalSize int64
	for _, sz := range uniqueSizes {
		totalSize += sz
		if sz > largestFile {
			largestFile = sz
		}
	}

	// File concentration: unique files / total references (with duplicates).
	// Looping playlists have many references to few files → low concentration.
	fileConcentration := float64(len(uniqueSizes)) / float64(len(items))

	// Compute weighted score.
	score := 0.0
	score += math.Min(float64(largestFile)/maxFileSize, 1.0) * 40.0
	score += math.Min(float64(totalSize)/maxTotalSize, 1.0) * 30.0
	score += math.Min(duration/maxDuration, 1.0) * 20.0
	score += fileConcentration * 10.0

	return score
}

// ParseMPLS parses an MPLS file and extracts duration and file references.
// Returns (duration in seconds, []PlaylistItem, error).
func ParseMPLS(mpslPath string) (float64, []PlaylistItem, error) {
	file, err := os.Open(mpslPath)
	if err != nil {
		return 0, nil, fmt.Errorf("open MPLS playlist: %w", err)
	}
	defer file.Close()

	// Validate MPLS signature and get header.
	header, err := loadMoviePlaylist(file)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to load movie playlist header: %w", err)
	}

	// Seek to playlist start and load the playlist structure.
	_, err = file.Seek(int64(header.PlaylistStartAddress), io.SeekStart)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to seek to playlist: %w", err)
	}

	_, playlistItems, err := loadPlaylist(file)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to load playlist: %w", err)
	}

	// Calculate total duration from play items (intime/outtime at 45kHz).
	const frequencyHz = 45000.0
	var totalDuration float64
	for _, item := range playlistItems {
		itemDuration := float64(item.OutTime-item.InTime) / frequencyHz
		totalDuration += itemDuration
	}

	// Build PlaylistItem list from clip names.
	clipNames := make([]PlaylistItem, 0, len(playlistItems))
	for _, item := range playlistItems {
		clipNames = append(clipNames, PlaylistItem{
			File: item.ClipName + ".m2ts",
			Size: 0, // Will be populated by attachStreamSizes.
		})
	}

	// Get file sizes from STREAM directory.
	itemsWithSizes, err := attachStreamSizes(filepath.Dir(mpslPath), clipNames)
	if err != nil {
		return 0, nil, err
	}

	return totalDuration, itemsWithSizes, nil
}

// MoviePlaylistHeader represents the MPLS file header.
type MoviePlaylistHeader struct {
	TypeIndicator             string
	VersionNumber             string
	PlaylistStartAddress      uint32
	PlaylistMarkStartAddress  uint32
	ExtensionDataStartAddress uint32
}

// PlaylistInfo represents a parsed playlist structure.
type PlaylistData struct {
	Length      uint32
	NbPlayItems uint16
	NbSubPaths  uint16
	PlayItems   []PlaylistItem
}

// PlaylistItemInfo represents a single play item in a playlist with timing info.
type PlaylistItemInfo struct {
	ClipName string
	InTime   uint32
	OutTime  uint32
}

// loadMoviePlaylist loads and parses the MPLS file header.
func loadMoviePlaylist(f *os.File) (*MoviePlaylistHeader, error) {
	header := make([]byte, 32)
	if _, err := f.Read(header); err != nil {
		return nil, fmt.Errorf("failed to read MPLS header: %w", err)
	}

	typeIndicator := string(header[0:4])
	if typeIndicator != "MPLS" {
		return nil, fmt.Errorf("invalid MPLS signature: %s", typeIndicator)
	}

	versionNumber := string(header[4:8])
	playlistStartAddr := binary.BigEndian.Uint32(header[8:12])
	playlistMarkStartAddr := binary.BigEndian.Uint32(header[12:16])
	extensionDataStartAddr := binary.BigEndian.Uint32(header[16:20])

	return &MoviePlaylistHeader{
		TypeIndicator:             typeIndicator,
		VersionNumber:             versionNumber,
		PlaylistStartAddress:      playlistStartAddr,
		PlaylistMarkStartAddress:  playlistMarkStartAddr,
		ExtensionDataStartAddress: extensionDataStartAddr,
	}, nil
}

// loadPlaylist loads and parses the playlist structure at the current file position.
// Returns both the playlist metadata and the detailed play items with timing info.
func loadPlaylist(f *os.File) (*PlaylistData, []PlaylistItemInfo, error) {
	// Read length (4 bytes, big-endian).
	lengthBytes := make([]byte, 4)
	if _, err := f.Read(lengthBytes); err != nil {
		return nil, nil, fmt.Errorf("failed to read playlist length: %w", err)
	}
	length := binary.BigEndian.Uint32(lengthBytes)

	if length == 0 {
		return &PlaylistData{Length: 0}, nil, nil
	}

	// Read reserved (2 bytes).
	reserved := make([]byte, 2)
	if _, err := f.Read(reserved); err != nil {
		return nil, nil, fmt.Errorf("failed to read reserved: %w", err)
	}

	// Read nb_play_items (2 bytes, big-endian).
	nbItemsBytes := make([]byte, 2)
	if _, err := f.Read(nbItemsBytes); err != nil {
		return nil, nil, fmt.Errorf("failed to read nb_play_items: %w", err)
	}
	nbPlayItems := binary.BigEndian.Uint16(nbItemsBytes)

	// Read nb_sub_paths (2 bytes, big-endian).
	nbSubPathsBytes := make([]byte, 2)
	if _, err := f.Read(nbSubPathsBytes); err != nil {
		return nil, nil, fmt.Errorf("failed to read nb_sub_paths: %w", err)
	}
	nbSubPaths := binary.BigEndian.Uint16(nbSubPathsBytes)

	// Load all play items.
	playItems := make([]PlaylistItemInfo, 0, nbPlayItems)
	clipItems := make([]PlaylistItem, 0, nbPlayItems)
	for i := 0; i < int(nbPlayItems); i++ {
		item, err := loadPlayItem(f)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load play item %d: %w", i, err)
		}
		// Only add if we got a valid clip filename.
		if item != nil && item.ClipName != "" {
			playItems = append(playItems, *item)
			clipItems = append(clipItems, PlaylistItem{
				File: item.ClipName + ".m2ts",
				Size: 0,
			})
		}
	}

	return &PlaylistData{
		Length:      length,
		NbPlayItems: nbPlayItems,
		NbSubPaths:  nbSubPaths,
		PlayItems:   clipItems,
	}, playItems, nil
}

// loadPlayItem loads a single play item from the file, following the Python MplsParser pattern.
func loadPlayItem(f *os.File) (*PlaylistItemInfo, error) {
	// Remember current position for seeking later.
	startPos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("failed to get file position: %w", err)
	}

	// Read item length (2 bytes, big-endian).
	lengthBytes := make([]byte, 2)
	if _, err := f.Read(lengthBytes); err != nil {
		return nil, fmt.Errorf("failed to read play item length: %w", err)
	}
	itemLength := binary.BigEndian.Uint16(lengthBytes)

	if itemLength == 0 {
		// Seek past this empty item (length itself is 2 bytes).
		_, err := f.Seek(startPos+int64(itemLength)+2, io.SeekStart)
		if err != nil {
			return nil, fmt.Errorf("failed to seek past empty item: %w", err)
		}
		return nil, nil
	}

	// Read clip information filename (5 bytes, UTF-8).
	clipNameBytes := make([]byte, 5)
	if _, err := f.Read(clipNameBytes); err != nil {
		return nil, fmt.Errorf("failed to read clip name: %w", err)
	}
	clipName := strings.TrimSpace(string(clipNameBytes))

	// Read remaining fixed fields.
	codecID := make([]byte, 4)
	if _, err := f.Read(codecID); err != nil {
		return nil, fmt.Errorf("failed to read clip_codec_identifier: %w", err)
	}

	miscFlags := make([]byte, 2)
	if _, err := f.Read(miscFlags); err != nil {
		return nil, fmt.Errorf("failed to read misc_flags_1: %w", err)
	}

	refToStcID := make([]byte, 1)
	if _, err := f.Read(refToStcID); err != nil {
		return nil, fmt.Errorf("failed to read ref_to_stcid: %w", err)
	}

	// Read intime (4 bytes, big-endian).
	inTimeBytes := make([]byte, 4)
	if _, err := f.Read(inTimeBytes); err != nil {
		return nil, fmt.Errorf("failed to read intime: %w", err)
	}
	inTime := binary.BigEndian.Uint32(inTimeBytes)

	// Read outtime (4 bytes, big-endian).
	outTimeBytes := make([]byte, 4)
	if _, err := f.Read(outTimeBytes); err != nil {
		return nil, fmt.Errorf("failed to read outtime: %w", err)
	}
	outTime := binary.BigEndian.Uint32(outTimeBytes)

	// Seek to next item: original_position + itemLength + 2 (Python pattern).
	_, err = f.Seek(startPos+int64(itemLength)+2, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek to next item: %w", err)
	}

	return &PlaylistItemInfo{
		ClipName: clipName,
		InTime:   inTime,
		OutTime:  outTime,
	}, nil
}

// attachStreamSizes maps clip files to their actual sizes from the STREAM directory.
func attachStreamSizes(playlistDir string, items []PlaylistItem) ([]PlaylistItem, error) {
	streamDir := filepath.Join(filepath.Dir(playlistDir), "STREAM")

	// Build map of available stream files and their sizes (using lowercase keys for case-insensitive matching).
	streamFiles := make(map[string]int64)
	entries, err := os.ReadDir(streamDir)
	if err != nil {
		if os.IsNotExist(err) {
			return items, nil // No STREAM directory; return items as-is.
		}
		return nil, fmt.Errorf("read BD stream directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.EqualFold(filepath.Ext(name), ".m2ts") {
			info, err := entry.Info()
			if err == nil {
				// Store both original name and lowercase for matching
				streamFiles[name] = info.Size()
				streamFiles[strings.ToLower(name)] = info.Size()
			}
		}
	}

	// Update item sizes from stream files with best-effort matching.
	var result []PlaylistItem
	for _, item := range items {
		// Try exact match first
		if size, ok := streamFiles[item.File]; ok {
			result = append(result, PlaylistItem{
				File: item.File,
				Size: size,
			})
			continue
		}

		// If no match but items list is empty, fall back to all available streams
		if len(result) == 0 {
			// Add item with size 0 for now, we'll fill in all streams as fallback
			result = append(result, PlaylistItem{
				File: item.File,
				Size: 0,
			})
		}
	}

	// If no items matched files, return all available streams instead.
	if len(result) == 0 || (len(result) > 0 && result[0].Size == 0) {
		result = []PlaylistItem{}
		seen := make(map[string]bool)
		for name, size := range streamFiles {
			// Only add each file once (skip the lowercase duplicates we created for matching)
			if !seen[strings.ToLower(name)] && strings.EqualFold(filepath.Ext(name), ".m2ts") {
				result = append(result, PlaylistItem{
					File: name,
					Size: size,
				})
				seen[strings.ToLower(name)] = true
			}
		}
	}

	// Sort results by filename for consistent ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i].File < result[j].File
	})

	return result, nil
}

// FormatDuration converts seconds to HH:MM:SS format for display.
func FormatDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
