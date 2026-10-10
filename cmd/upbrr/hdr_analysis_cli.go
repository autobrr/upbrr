// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Audionut/go-hdr10-plus/extract"
	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func validateCLIHDR(opts cliOptions, visited map[string]bool) error {
	if opts.HDRAnalysis && opts.HDRAnalysisOnly {
		return errors.New("--hdr-analysis and --hdr-analysis-only are mutually exclusive")
	}
	for _, flag := range []string{"hdr-targets", "hdr-playlist", "hdr-peak-source", "hdr-output-dir"} {
		if visited[flag] && !opts.HDRAnalysis && !opts.HDRAnalysisOnly {
			return fmt.Errorf("--%s requires --hdr-analysis or --hdr-analysis-only", flag)
		}
	}
	if visited["hdr-targets"] && opts.HDRAnalysisOnly {
		return errors.New("--hdr-targets requires upload --hdr-analysis")
	}
	if (visited["hdr-playlist"] || visited["hdr-output-dir"]) && !opts.HDRAnalysisOnly {
		return errors.New("--hdr-playlist and --hdr-output-dir require --hdr-analysis-only")
	}
	if opts.HDRAnalysis || opts.HDRAnalysisOnly {
		if _, err := api.HDRPeakSource(opts.HDRPeakSource).Normalize(); err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if opts.HDRTargets != "" {
			if _, err := parseCLIHDRTargets(opts.HDRTargets); err != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", err)
			}
		}
		if opts.HDRPlaylist != "" {
			if _, err := api.NormalizeHDRPlaylist(opts.HDRPlaylist); err != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", err)
			}
		}
	}
	if opts.HDRAnalysisOnly {
		for flag := range visited {
			switch flag {
			case "hdr-analysis-only", "hdr-playlist", "hdr-peak-source", "hdr-output-dir", "unattended", "ua", "unattended_confirm", "uac":
			default:
				return fmt.Errorf("--%s cannot be used with --hdr-analysis-only", flag)
			}
		}
		if strings.TrimSpace(opts.HDROutputDir) == "" {
			return errors.New("--hdr-analysis-only requires --hdr-output-dir <parent directory>")
		}
	}
	return nil
}

func parseCLIHDRTargets(raw string) ([]string, error) {
	ids := strings.Split(raw, ",")
	seen := make(map[string]bool)
	if len(ids) > api.HDRAnalysisMaxTargets {
		return nil, errors.New("too many HDR targets")
	}
	for index, id := range ids {
		id = strings.TrimSpace(id)
		if !api.ValidHDRTargetID(id) || seen[id] {
			return nil, errors.New("--hdr-targets requires unique prepared HDR target IDs in source order")
		}
		seen[id], ids[index] = true, id
	}
	return ids, nil
}

func cliHDRUploadRequest(opts cliOptions) *api.HDRAnalysisRequest {
	if !opts.HDRAnalysis {
		return nil
	}
	var ids []string
	if opts.HDRTargets != "" {
		ids, _ = parseCLIHDRTargets(opts.HDRTargets)
	}
	peak, _ := api.HDRPeakSource(opts.HDRPeakSource).Normalize()
	return &api.HDRAnalysisRequest{TargetIDs: ids, PeakSource: peak}
}

// hdrOutputParent resolves existing ancestors before creating any output, including aliases.
func hdrOutputParent(source, output string, disc bool) (string, error) {
	sourceReal, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	parent, err := filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	ancestor := parent
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(ancestor) == ancestor {
			return "", fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		ancestor = filepath.Dir(ancestor)
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	relative, err := filepath.Rel(ancestor, parent)
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	resolved = filepath.Join(resolved, relative)
	if pathutil.SamePath(sourceReal, resolved) || disc && pathutil.IsWithinRoot(sourceReal, resolved) {
		return "", errors.New("HDR output must be outside the source")
	}
	if info, err := os.Stat(parent); err == nil && !info.IsDir() {
		return "", errors.New("HDR output parent must be a directory")
	}
	return resolved, nil
}

type hdrSourceStamp struct {
	Path     string
	Size     int64
	Modified int64
}

func hdrSourceIdentity(ctx context.Context, source string) (api.WorkflowFingerprint, error) {
	var stamps []hdrSourceStamp
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("HDR disc sources cannot contain symlinks")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("HDR source contains a non-regular file")
		}
		stamps = append(stamps, hdrSourceStamp{
			Path:     path,
			Size:     info.Size(),
			Modified: info.ModTime().UnixNano(),
		})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(stamps)
	if err != nil {
		return "", fmt.Errorf("fingerprint HDR source: %w", err)
	}
	return fingerprint, nil
}

func standaloneHDRPlaylist(result bd.Result, requested string) (string, error) {
	eligible := make([]string, 0)
	for _, timeline := range result.Timelines {
		if requested != "" && !strings.EqualFold(timeline.Name, requested) {
			continue
		}
		if !timeline.Complete || timeline.Err != nil || timeline.SelectedAngle != 0 || len(timeline.Items) == 0 {
			continue
		}
		seen := make(map[string]bool)
		valid := true
		for _, item := range timeline.Items {
			if item.Err != nil || item.SelectedAlternative != 0 || len(item.Angles) == 0 {
				valid = false
				break
			}
			angle := item.Angles[0]
			if angle.Err != nil || angle.Source.Kind != "m2ts" || angle.STC == nil {
				valid = false
				break
			}
			primary := 0
			for _, mapping := range angle.Video {
				if mapping.Role == video.Primary {
					if mapping.Codec != video.HEVC || mapping.EntryType != 1 {
						valid = false
					}
					primary++
				}
			}
			if primary != 1 {
				valid = false
			}
			key := fmt.Sprintf("%s:%d:%d", angle.Source.Path, item.In45, item.Out45)
			if seen[key] && requested == "" {
				valid = false
			}
			seen[key] = true
		}
		if valid {
			eligible = append(eligible, timeline.Name)
		}
	}
	if len(eligible) != 1 {
		return "", errors.New("select one supported non-looping primary HEVC playlist with --hdr-playlist")
	}
	return eligible[0], nil
}

func runHDRAnalysisOnly(ctx context.Context, opts cliOptions, paths []string, streams cliIO) error {
	if len(paths) != 1 {
		return exitError(2, errors.New("--hdr-analysis-only requires exactly one MKV or disc content root"))
	}
	source, err := filepath.Abs(paths[0])
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	disc := info.IsDir()
	if disc {
		if marker, err := os.Stat(filepath.Join(source, "BDMV")); err != nil || !marker.IsDir() {
			return errors.New("HDR disc input must be a content root containing BDMV")
		}
	} else if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(source), ".mkv") {
		return errors.New("HDR input must be an MKV or disc content root")
	}
	if !disc && opts.HDRPlaylist != "" {
		return exitError(2, errors.New("--hdr-playlist requires a disc content root"))
	}
	parent, err := hdrOutputParent(source, opts.HDROutputDir, disc)
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	peak, err := api.HDRPeakSource(opts.HDRPeakSource).Normalize()
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	identity, err := hdrSourceIdentity(ctx, source)
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	playlist := ""
	if disc {
		if opts.HDRPlaylist != "" {
			playlist, err = api.NormalizeHDRPlaylist(opts.HDRPlaylist)
			if err != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", err)
			}
		}
		settings := bd.DefaultSettings(parent)
		settings.PlaylistOnly = playlist
		discovered, err := hdranalysis.DiscoverDisc(ctx, bd.Options{
			Path:            source,
			Settings:        settings,
			IncludeTimeline: true,
		})
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", hdranalysis.Classify(err))
		}
		playlist, err = standaloneHDRPlaylist(discovered, playlist)
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	directory, err := os.MkdirTemp(parent, "hdr-analysis-")
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	ctx = hdranalysis.WithProgress(ctx, func(progress hdranalysis.Progress) {
		fmt.Fprintf(streams.errOut, "HDR analysis: %s\n", strings.ReplaceAll(progress.Phase, "_", " "))
	})
	service := hdranalysis.New(hdranalysis.ProcessAdmission(), nil)
	err = service.WithSession(ctx, func(session *hdranalysis.Session) error {
		var metadata *extract.Extraction
		if disc {
			settings := bd.DefaultSettings(directory)
			settings.PlaylistOnly = playlist
			settings.GenerateStreamDiagnostics = false
			outcome, scanErr := session.ScanDisc(ctx, bd.Options{
				Path:            source,
				Settings:        settings,
				IncludeTimeline: true,
			})
			if scanErr != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", scanErr)
			}
			sidecar, extractErr := hdranalysis.DiscExtraction(outcome, playlist)
			if extractErr != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", extractErr)
			}
			metadata = sidecar.Extraction
		} else {
			var extractErr error
			metadata, extractErr = session.ExtractFile(ctx, source)
			if extractErr != nil {
				return fmt.Errorf("hdr_analysis_cli: %w", extractErr)
			}
		}
		current, err := hdrSourceIdentity(ctx, source)
		if err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		if current != identity {
			return fmt.Errorf("hdr_analysis_cli: %w", api.NewHDRAnalysisError(
				api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureStaleSource, Message: "HDR source changed during extraction"},
				nil,
			))
		}
		if err := session.Render(ctx, metadata, peak, "HDR10+ analysis", filepath.Join(directory, "hdr10plus.png")); err != nil {
			return fmt.Errorf("hdr_analysis_cli: %w", err)
		}
		current, err = hdrSourceIdentity(ctx, source)
		if err != nil || current != identity {
			_ = os.Remove(filepath.Join(directory, "hdr10plus.png"))
			return errors.New("HDR source changed during rendering")
		}
		summary := struct {
			Frames     int               `json:"frames"`
			Scenes     int               `json:"scenes"`
			Profile    string            `json:"profile"`
			PeakSource api.HDRPeakSource `json:"peakSource"`
			Playlist   string            `json:"playlist,omitempty"`
		}{len(metadata.Frames), len(metadata.SceneStarts), metadata.Profile, peak, playlist}
		if err := hdranalysis.PublishFile(
			ctx,
			filepath.Join(directory, "summary.json"),
			func(writer io.Writer) error { return json.MarshalWrite(writer, summary) },
		); err != nil {
			return fmt.Errorf("publish HDR summary: %w", err)
		}
		complete = true
		return nil
	})
	if complete {
		fmt.Fprintf(streams.out, "HDR analysis output: %s\n", directory)
	}
	if err != nil {
		return fmt.Errorf("hdr_analysis_cli: %w", hdranalysis.Classify(err))
	}
	return nil
}
