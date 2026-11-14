// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"

	"github.com/autobrr/upbrr/internal/dvdvideo/ifo"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

// ResolvedComparisonInput is private programme mapping and replay input. Source
// fingerprints also bind the bounded payload identity at the workflow boundary.
type ResolvedComparisonInput struct {
	Version                                              int
	Kind, Root, DiscID, CandidateID, MetadataFingerprint string
	DVD                                                  *ifo.FeatureTitle
	Playlist                                             string
	Occurrences                                          []ComparisonOccurrence
}

// ComparisonOccurrence retains exact ordered default-angle MPLS/STC evidence.
type ComparisonOccurrence struct {
	ClockIn90   int64
	Index       int
	Path        string
	In45, Out45 uint32
	Offset45    uint64
	STC         video.STCSequence
	Mapping     video.Mapping
	CPI         []CLPIEntry
}

// ComparisonDiscovery holds path-free choices and private roots; it has no
// upload preparation side effects and never invokes BDInfo Run.
type ComparisonDiscovery struct {
	Candidates          []api.ComparisonProgrammeCandidate
	Inputs              map[string]ResolvedComparisonInput
	MetadataFingerprint string
}

var comparisonDiscName = regexp.MustCompile(`^[0-9]{5}\.(?i:mpls|clpi|m2ts)$`)

// DiscoverComparisonProgrammes inventories extracted disc structural metadata.
// No candidate selection or upload playlist preference is applied here.
func DiscoverComparisonProgrammes(ctx context.Context, path string) (ComparisonDiscovery, error) {
	layout, err := sourcelayout.Resolve(ctx, path)
	if err != nil {
		return ComparisonDiscovery{}, fmt.Errorf("comparison programme metadata: %w", err)
	}
	result := ComparisonDiscovery{Inputs: map[string]ResolvedComparisonInput{}}
	digest := sha256.New()
	var bytesRead int64
	for _, disc := range layout.Discs {
		if disc.Image {
			return result, comparisonCapability("iso_clip_adapter", "Disc image comparisons require a seekable logical clip adapter.")
		}
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("comparison programme metadata: %w", err)
		}
		metadata, err := comparisonStructuralFiles(disc.Root, disc.Type)
		if err != nil {
			return result, err
		}
		for _, name := range metadata {
			file, err := pathing.OpenFileWithinRoot(disc.Root, name, os.O_RDONLY, 0)
			if err != nil {
				return result, fmt.Errorf("comparison programme metadata: %w", err)
			}
			_, _ = io.WriteString(digest, disc.ID+"\x00"+filepath.Base(name)+"\x00")
			n, readErr := io.Copy(digest, io.LimitReader(file, (64<<20)-bytesRead+1))
			_ = file.Close()
			bytesRead += n
			if readErr != nil {
				return result, fmt.Errorf("comparison programme metadata: %w", readErr)
			}
			if bytesRead > 64<<20 {
				return result, comparisonCapability("structural_limit", "The selected disc exceeds the comparison structural metadata limit.")
			}
		}
		switch disc.Type {
		case "DVD":
			feature, err := ifo.InspectFeatures(disc.Root)
			if err != nil {
				return result, fmt.Errorf("comparison programme metadata: %w", err)
			}
			for _, title := range feature.Titles {
				id := comparisonCandidateID(disc.ID, fmt.Sprintf("dvd:%d", title.Number))
				candidate := api.ComparisonProgrammeCandidate{
					ID:              id,
					Kind:            "dvd",
					DiscID:          disc.ID,
					DiscName:        disc.Name,
					Label:           fmt.Sprintf("Title %d · %d chapters", title.Number, title.Chapters),
					DurationSeconds: float64(title.Duration90) / 90000,
					Supported:       title.Unsupported == "",
					Reason:          title.Unsupported,
				}
				if !candidate.Supported {
					candidate.Limitation = "This title requires DVD navigation or random-access evidence that comparisons do not support."
				}
				result.Candidates = append(result.Candidates, candidate)
				result.Inputs[id] = ResolvedComparisonInput{
					Version:     1,
					Kind:        "dvd",
					Root:        disc.Root,
					DiscID:      disc.ID,
					CandidateID: id,
					DVD:         new(title),
				}
			}
		case "BDMV":
			playlists, err := DiscoverPlaylists(ctx, disc.Root)
			if err != nil {
				return result, err
			}
			for rank, playlist := range playlists {
				if !comparisonDiscName.MatchString(playlist.File) {
					return result, ErrComparisonIndex
				}
				id := comparisonCandidateID(disc.ID, "bluray:"+strings.ToLower(playlist.File))
				result.Candidates = append(result.Candidates, api.ComparisonProgrammeCandidate{
					ID:              id,
					Kind:            "bluray",
					DiscID:          disc.ID,
					DiscName:        disc.Name,
					Label:           playlist.File,
					DurationSeconds: playlist.Duration,
					Rank:            rank,
					Supported:       true,
				})
				result.Inputs[id] = ResolvedComparisonInput{
					Version:     1,
					Kind:        "bluray",
					Root:        disc.Root,
					DiscID:      disc.ID,
					CandidateID: id,
					Playlist:    playlist.File,
				}
			}
		default:
			return result, comparisonCapability("disc_format", "This disc format does not support title comparisons.")
		}
	}
	result.MetadataFingerprint = hex.EncodeToString(digest.Sum(nil))
	for id, input := range result.Inputs {
		input.MetadataFingerprint = result.MetadataFingerprint
		result.Inputs[id] = input
	}
	return result, nil
}

func comparisonStructuralFiles(root, kind string) ([]string, error) {
	var paths []string
	dirs := []string{root}
	if kind == "BDMV" {
		dirs = []string{filepath.Join(root, "PLAYLIST"), filepath.Join(root, "CLIPINF")}
	}
	for _, directory := range dirs {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, fmt.Errorf("comparison programme metadata: %w", err)
		}
		for _, entry := range entries {
			ext := strings.ToUpper(filepath.Ext(entry.Name()))
			if ext != ".IFO" && ext != ".BUP" && ext != ".MPLS" && ext != ".CLPI" {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || !pathing.IsWithinRoot(root, path) {
				return nil, ErrComparisonIndex
			}
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	return paths, nil
}

func comparisonCandidateID(disc, programme string) string {
	digest := sha256.Sum256([]byte(disc + "\x00" + programme))
	return "programme-" + hex.EncodeToString(digest[:16])
}

// ResolveComparisonProgramme resolves only a previously discovered selection.
// Presentation Items are never used as the authored playback timeline.
func ResolveComparisonProgramme(ctx context.Context, input ResolvedComparisonInput) (ResolvedComparisonInput, error) {
	if input.Kind == "dvd" {
		return input, nil
	}
	if input.Kind != "bluray" || !comparisonDiscName.MatchString(input.Playlist) {
		return input, ErrComparisonIndex
	}
	settings := bd.DefaultSettings("")
	settings.PlaylistOnly = strings.ToUpper(input.Playlist)
	result, err := bd.DiscoverPlaylists(ctx, bd.Options{
		Path:            input.Root,
		Settings:        settings,
		IncludeTimeline: true,
	})
	if err != nil {
		return input, fmt.Errorf("comparison programme metadata: %w", err)
	}
	if len(result.Timelines) != 1 || !strings.EqualFold(result.Timelines[0].Name, input.Playlist) {
		return input, ErrComparisonIndex
	}
	timeline := result.Timelines[0]
	if !timeline.Complete || timeline.Err != nil || timeline.Duration45 == 0 {
		return input, ErrComparisonIndex
	}
	input.Occurrences = nil
	var offset uint64
	entries := 0
	for _, item := range timeline.Items {
		if item.Err != nil || len(item.Angles) == 0 || item.In45 >= item.Out45 || item.Offset45 != offset || item.Duration45 != uint64(item.Out45-item.In45) {
			return input, ErrComparisonIndex
		}
		angle := item.Angles[0]
		if angle.Err != nil || angle.Source.Kind != "m2ts" || angle.STC == nil || !angle.STC.HasEndPacket {
			return input, comparisonCapability("clock_mapping", "The selected programme has no exact primary-stream clock mapping.")
		}
		var primary []video.Mapping
		for _, mapping := range angle.Video {
			if mapping.Role == video.Primary {
				primary = append(primary, mapping)
			}
		}
		if len(primary) != 1 || primary[0].EntryType != 1 || primary[0].Codec == video.MVC {
			return input, comparisonCapability("video_mapping", "The selected programme has an unsupported video mapping.")
		}
		name := angle.Source.LogicalClip
		if !comparisonDiscName.MatchString(name) || !strings.EqualFold(filepath.Ext(name), ".m2ts") {
			return input, ErrComparisonIndex
		}
		path, pathErr := comparisonDiscResource(input.Root, "STREAM", name)
		if pathErr != nil {
			return input, pathErr
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size()%192 != 0 || !pathing.IsWithinRoot(input.Root, path) {
			return input, ErrComparisonIndex
		}
		stcDuration := angle.STC.PresentationEnd45 - angle.STC.PresentationStart45
		inOffset := item.In45 - angle.STC.PresentationStart45
		streamBytes := info.Size()
		if streamBytes <= 0 {
			return input, ErrComparisonIndex
		}
		if stcDuration == 0 || uint64(inOffset)+item.Duration45 > uint64(stcDuration) || angle.STC.EndPacket > uint64(streamBytes/192) ||
			uint64(angle.STC.StartPacket) >= angle.STC.EndPacket {
			return input, ErrComparisonIndex
		}
		clockIn90 := (int64(angle.STC.PresentationStart45) + int64(inOffset)) * 2
		clpi, pathErr := comparisonDiscResource(input.Root, "CLIPINF", strings.TrimSuffix(name, filepath.Ext(name))+".clpi")
		if pathErr != nil {
			return input, pathErr
		}
		cpi, err := ReadCLPICPI(input.Root, clpi, primary[0].PID)
		if err != nil {
			return input, comparisonCapability("missing_cpi", "The selected programme has no usable random-access map.")
		}
		entries += len(cpi)
		if entries > 1_000_000 {
			return input, ErrComparisonIndex
		}
		var selectedCPI []CLPIEntry
		clock := int64(angle.STC.PresentationStart45)
		for _, entry := range cpi {
			if entry.Packet < angle.STC.StartPacket || uint64(entry.Packet) >= angle.STC.EndPacket {
				continue
			}
			entry.PTS45 = comparisonClockNear(entry.PTS45, clock)
			if len(selectedCPI) > 0 && entry.PTS45 < clock {
				return input, ErrComparisonIndex
			}
			if entry.PTS45 < int64(angle.STC.PresentationStart45)-256 || entry.PTS45 > int64(angle.STC.PresentationStart45)+int64(stcDuration) {
				return input, ErrComparisonIndex
			}
			clock = entry.PTS45
			selectedCPI = append(selectedCPI, entry)
		}
		if len(selectedCPI) == 0 {
			return input, ErrComparisonIndex
		}
		input.Occurrences = append(input.Occurrences, ComparisonOccurrence{
			ClockIn90: clockIn90,
			Index:     item.Index,
			Path:      path,
			In45:      item.In45,
			Out45:     item.Out45,
			Offset45:  item.Offset45,
			STC:       *angle.STC,
			Mapping:   primary[0],
			CPI:       selectedCPI,
		})
		offset += item.Duration45
	}
	return input, nil
}

func comparisonDiscResource(root, directory, name string) (string, error) {
	if !comparisonDiscName.MatchString(name) {
		return "", ErrComparisonIndex
	}
	entries, err := os.ReadDir(filepath.Join(root, directory))
	if err != nil {
		return "", fmt.Errorf("comparison programme metadata: %w", err)
	}
	var result string
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), name) {
			if result != "" {
				return "", ErrComparisonIndex
			}
			result = filepath.Join(root, directory, entry.Name())
		}
	}
	if result == "" || !pathing.IsWithinRoot(root, result) {
		return "", ErrComparisonIndex
	}
	return result, nil
}

func comparisonCapability(reason, message string) error {
	return api.NewOperationError(api.OperationFailure{
		Code:             api.OperationFailureInvalidSource,
		Operation:        api.OperationKindMedia,
		Recovery:         api.OperationRecoveryEditInput,
		ComparisonReason: reason,
		Message:          message,
	}, ErrComparisonIndex)
}
