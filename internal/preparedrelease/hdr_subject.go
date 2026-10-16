// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func hdrTargetID(pathValue, playlist string) string {
	return api.HDRTargetID(filepath.Clean(pathValue), playlist)
}

// ResolveRetainedHDRAnalysisSubject checks the current persisted generation without
// accessing source media. The target facts must come from the owner's exact workflow
// snapshot; paths are reconstructed only from that generation's source manifest.
func (m *Module) ResolveRetainedHDRAnalysisSubject(
	ctx context.Context,
	instructions api.HDRAnalysisInstructions,
	targets []api.HDRAnalysisTarget,
) (api.HDRAnalysisSubject, error) {
	normalized, err := instructions.Normalize()
	if err != nil {
		return api.HDRAnalysisSubject{}, fmt.Errorf("normalize retained HDR selection: %w", err)
	}
	release, err := m.hdrGeneration(ctx, normalized.Release)
	if err != nil {
		return api.HDRAnalysisSubject{}, err
	}
	m.mu.RLock()
	owned, ok := m.envelopes[canonicalSourceKey(release.Source.SourcePath)]
	m.mu.RUnlock()
	var inventory []api.HDRAnalysisTargetSubject
	if ok && owned.result.Release.Generation == release.Generation {
		inventory = hdrTargetSubjects(owned)
	} else {
		for _, target := range targets {
			for _, entry := range release.Source.Entries {
				candidate := api.HDRAnalysisTargetSubject{Target: target}
				if target.Playlist == "" && entry.Type == api.SourceEntryTypeFile && hdrTargetID(entry.Path, "") == target.ID {
					candidate.Path = entry.Path
				} else if target.Playlist != "" && entry.Disc == "BDMV" && entry.Type == api.SourceEntryTypePlaylist &&
					strings.EqualFold(entry.Playlist, target.Playlist) {
					root := filepath.Dir(filepath.Dir(entry.Path))
					if hdrTargetID(root, target.Playlist) == target.ID {
						candidate.DiscRoot = root
					}
				}
				if candidate.Path != "" || candidate.DiscRoot != "" {
					inventory = append(inventory, candidate)
					break
				}
			}
		}
	}
	return resolveHDRSelection(normalized, release, inventory)
}

func (m *Module) hdrGeneration(ctx context.Context, ref api.ReleaseRef) (api.PreparedRelease, error) {
	if m == nil || m.store == nil || ctx == nil {
		return api.PreparedRelease{}, errors.New("prepared HDR generation is unavailable")
	}
	release, found, err := m.loadCurrent(ctx, ref.SourcePath)
	if err != nil {
		return api.PreparedRelease{}, err
	}
	if !found || release.Generation != ref.Generation || ref.Generation == 0 {
		return api.PreparedRelease{}, fmt.Errorf("resolve retained HDR generation: %w", api.NewHDRAnalysisError(api.HDRAnalysisFailure{
			Code: api.HDRAnalysisFailureStaleSource, Message: "the prepared HDR generation is no longer current",
		}, &StalePreparationError{
			SourcePath: ref.SourcePath,
			Generation: ref.Generation,
			Reason:     StaleReasonGeneration,
		}))
	}
	return release, nil
}

// ValidateHDRAnalysisSource verifies media only when an operation needs new native
// evidence or decoded measurements. Retained rendering uses generation authority.
func (m *Module) ValidateHDRAnalysisSource(ctx context.Context, ref api.ReleaseRef) error {
	release, err := m.hdrGeneration(ctx, ref)
	if err != nil {
		return err
	}
	return validateSeedSource(ctx, envelopeFromPersisted(release, api.PrepareInput{SourcePath: ref.SourcePath}))
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
					Reason:          "Collect native HDR evidence when selecting the Blu-ray playlists first.",
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
				target.CapturedNativePath, target.CapturedNativeSize, target.CapturedNativeSHA256 = captured.NativePath, captured.NativeSize, captured.NativeSHA256
				if captured.Failure != nil {
					failure := *captured.Failure
					target.CapturedFailure = &failure
					target.Target.Reason = failure.Message
				}
				if captured.NativePath != "" {
					target.Target.Supported = true
					target.Target.Reason = ""
				}
			}
		}
	}
	if len(owned.resources.discs) > 0 {
		return targets
	}
	for _, entry := range owned.result.Release.Source.Entries {
		extension := strings.ToLower(filepath.Ext(entry.Path))
		if entry.Type != api.SourceEntryTypeFile || extension != ".mkv" && extension != ".mp4" && extension != ".ts" && extension != ".m2ts" {
			continue
		}
		targets = append(targets, api.HDRAnalysisTargetSubject{
			Target: api.HDRAnalysisTarget{
				ID:              hdrTargetID(entry.Path, ""),
				Label:           fmt.Sprintf("Video %d", len(targets)+1),
				SelectionPolicy: "unique_hevc",
				Supported: owned.resources.hdrFileEligibility[canonicalSourceKey(entry.Path)] ||
					owned.resources.hdrFileEvidence[canonicalSourceKey(entry.Path)].UniqueHEVC,
				Evidence: owned.resources.hdrFileEvidence[canonicalSourceKey(entry.Path)],
			},
			Path: entry.Path,
		})
		if !targets[len(targets)-1].Target.Supported {
			targets[len(targets)-1].Target.Reason = "MediaInfo must identify a unique HEVC video track."
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
	return resolveHDRSelection(normalized, owned.result.Release, hdrTargetSubjects(owned))
}

func resolveHDRSelection(
	normalized api.HDRAnalysisInstructions,
	release api.PreparedRelease,
	inventory []api.HDRAnalysisTargetSubject,
) (api.HDRAnalysisSubject, error) {
	binding, err := release.MediaBinding()
	if err != nil {
		return api.HDRAnalysisSubject{}, fmt.Errorf("resolve HDR media binding: %w", err)
	}
	subject := api.HDRAnalysisSubject{
		Release:           normalized.Release,
		SourceFingerprint: release.Compatibility.SourceFingerprint,
		MediaBinding:      binding,
		Title:             release.Naming.ReleaseName,
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
			if api.IsNativeHDRProfile(normalized.ProfileVersion) && inventory[found].Target.Evidence.UniqueHEVC {
				inventory[found].Target.Supported = true
				inventory[found].Target.Reason = ""
			}
		}
		if normalized.ProfileVersion == api.HDRAnalysisProfileVersion && inventory[found].Path != "" &&
			(!strings.EqualFold(filepath.Ext(inventory[found].Path), ".mkv") || inventory[found].Target.Evidence.UniqueHEVC && !inventory[found].Target.Evidence.HDR10Plus) {
			return api.HDRAnalysisSubject{}, fmt.Errorf(
				"legacy HDR eligibility: %w",
				api.NewHDRAnalysisError(
					api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureUnsupportedInput, Message: "HDR analysis v1 requires advertised HDR10+."},
					nil,
				),
			)
		}
		if normalized.ProfileVersion == api.HDRAnalysisProfileVersion && inventory[found].DiscRoot != "" &&
			(inventory[found].CapturedAbsent || inventory[found].CapturedFailure != nil) {
			return api.HDRAnalysisSubject{}, fmt.Errorf("legacy HDR disc eligibility: %w", api.NewHDRAnalysisError(api.HDRAnalysisFailure{
				Code: api.HDRAnalysisFailureUnsupportedInput, Message: "HDR analysis v1 requires complete HDR10+ metadata.",
			}, nil))
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
		target := inventory[found]
		target.Audio = hdrTargetAudioSubject(release, normalized.Release, binding, target)
		subject.Targets = append(subject.Targets, target)
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

// hdrTargetAudioSubject projects persisted inventory only. Pack siblings without
// their own inspected resource and disc ordinals never borrow another file's tracks.
func hdrTargetAudioSubject(
	release api.PreparedRelease,
	ref api.ReleaseRef,
	binding api.PreparedMediaBinding,
	target api.HDRAnalysisTargetSubject,
) *api.AudioAnalysisSubject {
	if target.Path == "" || target.DiscRoot != "" || !release.Media.TrackCoverageComplete {
		return nil
	}
	resource := mediafacts.TrackResourceID(target.Path)
	subject := &api.AudioAnalysisSubject{
		Release:           ref,
		MediaBinding:      binding,
		SourcePath:        release.Source.SourcePath,
		VideoPath:         target.Path,
		SourceFingerprint: release.Compatibility.SourceFingerprint,
		ResourceID:        resource,
	}
	for _, track := range release.Media.Tracks {
		if track.ResourceID != resource || track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.DiscID != "" || track.PlaylistID != "" || track.ID == "" || track.ManifestFingerprint == "" || track.Ordinal != len(subject.Tracks)+1 {
			return nil
		}
		if len(subject.Tracks) == 0 {
			subject.ManifestFingerprint = track.ManifestFingerprint
		}
		if track.ManifestFingerprint != subject.ManifestFingerprint ||
			slices.ContainsFunc(subject.Tracks, func(previous api.MediaTrackFacts) bool { return previous.ID == track.ID }) {
			return nil
		}
		track.Languages = slices.Clone(track.Languages)
		track.DetectedLanguages = slices.Clone(track.DetectedLanguages)
		subject.Tracks = append(subject.Tracks, track)
		if track.ID == release.Media.PrimaryAudioTrackID {
			subject.PrimaryTrackID = track.ID
		}
	}
	if len(subject.Tracks) == 0 {
		inventory := hdrTargetSubjects(envelopeFromPersisted(release, api.PrepareInput{}))
		if !release.Media.AudioAbsent || len(inventory) != 1 || canonicalSourceKey(inventory[0].Path) != canonicalSourceKey(target.Path) {
			return nil
		}
	}
	return subject
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
