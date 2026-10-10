// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func hdrTargetID(pathValue, playlist string) string {
	return api.HDRTargetID(filepath.Clean(pathValue), playlist)
}

func hdrTargetSubjects(owned envelope) []api.HDRAnalysisTargetSubject {
	var targets []api.HDRAnalysisTargetSubject
	for discIndex, disc := range owned.resources.discs {
		if disc.Type != "BDMV" {
			continue
		}
		for _, playlist := range disc.SelectedPlaylists {
			name, err := api.NormalizeHDRPlaylist(playlist.File)
			if err != nil {
				continue
			}
			targets = append(targets, api.HDRAnalysisTargetSubject{
				Target: api.HDRAnalysisTarget{
					ID:              hdrTargetID(disc.Root, name),
					Label:           fmt.Sprintf("Disc %d · %s", discIndex+1, name),
					Playlist:        name,
					SelectionPolicy: "primary_hevc_angle_zero",
					Reason:          "Check for HDR10+ when selecting the Blu-ray playlists first.",
				},
				DiscRoot: disc.Root,
			})
			target := &targets[len(targets)-1]
			for _, item := range owned.result.Release.Disc.Items {
				if item.ID != disc.ID {
					continue
				}
				for _, report := range item.Reports {
					if report.Playlist.ID == playlist.ID && report.Playlist.File == playlist.File && report.HDR10PlusConfirmed {
						target.Target.Supported = true
						target.Target.Reason = ""
					}
				}
			}
			if captured, ok := disc.HDRCaptures[name]; ok && captured.SourceFingerprint == owned.result.Release.Compatibility.SourceFingerprint {
				target.Target.Supported = captured.Path != "" && !captured.Absent && captured.Failure == nil
				if target.Target.Supported {
					target.Target.Reason = ""
				}
				if captured.Absent {
					target.Target.Reason = "No HDR10+ metadata was found in this playlist."
				}
				target.CapturedPath, target.CapturedSize, target.CapturedSHA256 = captured.Path, captured.Size, captured.SHA256
				target.CapturedTrackID, target.CapturedAbsent = captured.TrackID, captured.Absent
				if captured.Failure != nil {
					failure := *captured.Failure
					target.CapturedFailure = &failure
					target.Target.Reason = failure.Message
				}
			}
		}
	}
	if len(owned.resources.discs) > 0 {
		return targets
	}
	for _, entry := range owned.result.Release.Source.Entries {
		if entry.Type != api.SourceEntryTypeFile || !strings.EqualFold(filepath.Ext(entry.Path), ".mkv") {
			continue
		}
		targets = append(targets, api.HDRAnalysisTargetSubject{
			Target: api.HDRAnalysisTarget{
				ID:              hdrTargetID(entry.Path, ""),
				Label:           fmt.Sprintf("Video %d", len(targets)+1),
				SelectionPolicy: "unique_hevc",
				Supported:       owned.resources.hdrFileEligibility[canonicalSourceKey(entry.Path)],
			},
			Path: entry.Path,
		})
		if !targets[len(targets)-1].Target.Supported {
			targets[len(targets)-1].Target.Reason = "MediaInfo must confirm HDR10+ on a unique HEVC video track."
		}
	}
	return targets
}

// ResolveHDRAnalysisSubject binds selected targets to verified private source authority.
// Targets must be supported members of the exact prepared generation in source order.
// Matroska track numbers and disc transport mappings are resolved by the native dependencies.
func (m *Module) ResolveHDRAnalysisSubject(ctx context.Context, instructions api.HDRAnalysisInstructions) (api.HDRAnalysisSubject, error) {
	normalized, err := instructions.Normalize()
	if err != nil {
		return api.HDRAnalysisSubject{}, fmt.Errorf(
			"normalize HDR selection: %w",
			api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureInvalidSelection, Message: "the HDR target selection is invalid"}, err),
		)
	}
	owned, err := m.resolveEnvelope(ctx, normalized.Release)
	if err != nil {
		code := api.HDRAnalysisFailureResourceUnavailable
		if _, stale := errors.AsType[*StalePreparationError](err); stale {
			code = api.HDRAnalysisFailureStaleSource
		}
		return api.HDRAnalysisSubject{}, fmt.Errorf(
			"resolve HDR source: %w",
			api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: code, Message: "the prepared HDR source must be refreshed"}, err),
		)
	}
	inventory := hdrTargetSubjects(owned)
	binding, err := owned.result.Release.MediaBinding()
	if err != nil {
		return api.HDRAnalysisSubject{}, fmt.Errorf("resolve HDR media binding: %w", err)
	}
	subject := api.HDRAnalysisSubject{
		Release:           normalized.Release,
		SourceFingerprint: owned.result.Release.Compatibility.SourceFingerprint,
		MediaBinding:      binding,
		Title:             owned.result.Release.Naming.ReleaseName,
	}
	last := -1
	for _, id := range normalized.TargetIDs {
		found := -1
		for index, target := range inventory {
			if target.Target.ID == id {
				found = index
				break
			}
		}
		if found <= last {
			return api.HDRAnalysisSubject{}, fmt.Errorf("hdr_subject: %w", api.NewHDRAnalysisError(
				api.HDRAnalysisFailure{
					Code:    api.HDRAnalysisFailureInvalidSelection,
					Message: "HDR targets must belong to the prepared source and use source order",
				},
				nil,
			))
		}
		if !inventory[found].Target.Supported {
			if failure := inventory[found].CapturedFailure; failure != nil {
				return api.HDRAnalysisSubject{}, fmt.Errorf("resolve HDR target: %w", api.NewHDRAnalysisError(*failure, nil))
			}
			return api.HDRAnalysisSubject{}, fmt.Errorf("resolve HDR target: %w", api.NewHDRAnalysisError(api.HDRAnalysisFailure{
				Code:    api.HDRAnalysisFailureUnsupportedInput,
				Message: inventory[found].Target.Reason,
			}, nil))
		}
		subject.Targets = append(subject.Targets, inventory[found])
		last = found
	}
	identities := make([]string, len(subject.Targets))
	for index, target := range subject.Targets {
		identities[index] = target.Target.ID
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Source  string
		Targets []string
	}{subject.SourceFingerprint, identities})
	if err != nil {
		return api.HDRAnalysisSubject{}, fmt.Errorf("fingerprint HDR subject: %w", err)
	}
	subject.ManifestFingerprint = string(fingerprint)
	return subject, nil
}

// HDRAnalysisTargets exposes selection policy and opaque target IDs for one exact generation.
// File eligibility comes from MediaInfo; extraction establishes actual dynamic metadata presence.
func (m *Module) HDRAnalysisTargets(ctx context.Context, release api.ReleaseRef) ([]api.HDRAnalysisTarget, error) {
	owned, err := m.resolveEnvelope(ctx, release)
	if err != nil {
		return nil, fmt.Errorf("resolve HDR inventory: %w", err)
	}
	targets := hdrTargetSubjects(owned)
	public := make([]api.HDRAnalysisTarget, len(targets))
	for index, target := range targets {
		public[index] = target.Target
	}
	return public, nil
}
