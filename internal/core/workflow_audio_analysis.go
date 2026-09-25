// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png" // register retained PNG validation
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/audioanalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

type audioAnalysisSubjectResolver interface {
	ResolveAudioAnalysisSubject(context.Context, api.AudioAnalysisInstructions) (api.AudioAnalysisSubject, error)
}

type audioAnalysisService interface {
	ValidateSelection(context.Context, api.AudioAnalysisSubject, api.AudioAnalysisInstructions) error
	Analyze(context.Context, api.AudioAnalysisSubject, api.AudioAnalysisInstructions, string, string) ([]audioanalysis.TrackResult, error)
}

type workflowAudioAnalysisBuilder struct {
	resolver audioAnalysisSubjectResolver
	service  audioAnalysisService
	root     string
}

func (b workflowAudioAnalysisBuilder) Build(
	ctx context.Context,
	release api.ReleaseRef,
	instructions api.AudioAnalysisInstructions,
	attemptID string,
	now time.Time,
	prior *api.AudioAnalysisResult,
	priorResource releaseworkflow.RetainedAudioAnalysisResource,
) (api.AudioAnalysisResult, releaseworkflow.RetainedAudioAnalysisResource, error) {
	if b.resolver == nil || b.service == nil {
		return api.AudioAnalysisResult{}, nil, errors.New("audio analysis dependencies are unavailable")
	}
	instructions.Release = release
	normalized, err := instructions.Normalize()
	if err != nil {
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("normalize audio analysis instructions: %w", err)
	}
	subject, err := b.resolver.ResolveAudioAnalysisSubject(ctx, normalized)
	if err != nil {
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("resolve prepared audio analysis subject: %w", err)
	}
	if err := b.service.ValidateSelection(ctx, subject, normalized); err != nil {
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("validate selected audio tracks: %w", err)
	}
	attemptRoot, err := audioAnalysisAttemptRoot(b.root, subject, attemptID)
	if err != nil {
		return api.AudioAnalysisResult{}, nil, err
	}
	if err := os.RemoveAll(attemptRoot); err != nil {
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("reset audio-analysis attempt root: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureResourceUnavailable,
			Message: "the managed audio-analysis output root could not be reset",
		}, err))
	}
	tracks, artifactPaths, status, err := b.buildTracks(ctx, subject, normalized, attemptID, attemptRoot, prior, priorResource)
	if err != nil {
		_ = os.RemoveAll(attemptRoot)
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("build audio analysis tracks: %w", err)
	}
	if ctx.Err() == nil {
		if err := b.revalidateSubject(ctx, normalized, subject); err != nil {
			_ = os.RemoveAll(attemptRoot)
			return api.AudioAnalysisResult{}, nil, fmt.Errorf("revalidate audio analysis before publication: %w", err)
		}
	}
	completedAt := now
	result := api.AudioAnalysisResult{
		Release:             release,
		ResourceID:          subject.ResourceID,
		ManifestFingerprint: subject.ManifestFingerprint,
		AttemptID:           attemptID,
		Selection:           normalized.Selection,
		TrackIDs:            append([]string(nil), normalized.TrackIDs...),
		Variants:            append([]api.AudioAnalysisVariant(nil), normalized.Variants...),
		ProfileVersion:      normalized.ProfileVersion,
		ResourceLimits:      normalized.ResourceLimits,
		Status:              status,
		Tracks:              tracks,
		CreatedAt:           now,
		CompletedAt:         &completedAt,
	}
	if len(artifactPaths) == 0 {
		_ = os.RemoveAll(attemptRoot)
		return result, nil, nil
	}
	resource, err := retainWorkflowAudioAnalysisResource(b.root, attemptRoot, artifactPaths)
	if err != nil {
		_ = os.RemoveAll(attemptRoot)
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("retain audio-analysis artifacts: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureResourceUnavailable,
			Message: "an audio-analysis artifact could not be integrity-bound",
		}, err))
	}
	return result, resource, nil
}

func (b workflowAudioAnalysisBuilder) RestoreCompatible(
	ctx context.Context,
	release api.ReleaseRef,
	prior api.AudioAnalysisResult,
	retained releaseworkflow.RetainedAudioAnalysisResource,
	attemptID string,
) (api.AudioAnalysisResult, releaseworkflow.RetainedAudioAnalysisResource, error) {
	resource, ok := retained.(workflowAudioAnalysisResource)
	if !ok || b.resolver == nil || prior.ProfileVersion != api.AudioAnalysisProfileVersion {
		return api.AudioAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	instructions := api.AudioAnalysisInstructions{
		Release:        release,
		ResourceID:     prior.ResourceID,
		Selection:      prior.Selection,
		TrackIDs:       prior.TrackIDs,
		Variants:       prior.Variants,
		ProfileVersion: api.AudioAnalysisProfileVersion,
	}
	subject, err := b.resolver.ResolveAudioAnalysisSubject(ctx, instructions)
	if err != nil || subject.Release != release || subject.ResourceID != prior.ResourceID ||
		subject.ManifestFingerprint != prior.ManifestFingerprint {
		return api.AudioAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	attemptRoot, err := audioAnalysisAttemptRoot(b.root, subject, attemptID)
	if err != nil {
		return api.AudioAnalysisResult{}, nil, err
	}
	if err := os.RemoveAll(attemptRoot); err != nil {
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("reset restored audio-analysis attempt: %w", err)
	}
	paths := make(map[api.PublicResourceID]string)
	result := prior
	result.Release = release
	result.AttemptID = attemptID
	result.Tracks = make([]api.AudioAnalysisTrackResult, len(prior.Tracks))
	for trackIndex, track := range prior.Tracks {
		result.Tracks[trackIndex] = track
		result.Tracks[trackIndex].Artifacts = append([]api.AudioAnalysisArtifact(nil), track.Artifacts...)
		for artifactIndex, artifact := range track.Artifacts {
			if artifact.Status != api.StageStatusCompleted {
				continue
			}
			pathValue, found := resource.paths[artifact.ID]
			integrity := resource.integrity[artifact.ID]
			if !found || !validAudioAnalysisArtifact(b.root, pathValue, artifact) ||
				!validAudioAnalysisIntegrity(pathValue, integrity) {
				_ = os.RemoveAll(attemptRoot)
				return api.AudioAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
			}
			cloned, cloneErr := cloneAudioAnalysisArtifact(attemptRoot, attemptID, track.TrackID, artifact, pathValue, integrity)
			if cloneErr != nil {
				_ = os.RemoveAll(attemptRoot)
				return api.AudioAnalysisResult{}, nil, fmt.Errorf("clone retained audio-analysis artifact: %w", cloneErr)
			}
			result.Tracks[trackIndex].Artifacts[artifactIndex] = cloned.Public
			paths[cloned.Public.ID] = cloned.Path
		}
	}
	if len(paths) == 0 {
		_ = os.RemoveAll(attemptRoot)
		return api.AudioAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	if err := b.revalidateSubject(ctx, instructions, subject); err != nil {
		_ = os.RemoveAll(attemptRoot)
		return api.AudioAnalysisResult{}, nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	cloned, err := retainWorkflowAudioAnalysisResource(b.root, attemptRoot, paths)
	if err != nil {
		_ = os.RemoveAll(attemptRoot)
		return api.AudioAnalysisResult{}, nil, fmt.Errorf("retain restored audio-analysis artifacts: %w", err)
	}
	return result, cloned, nil
}

func (b workflowAudioAnalysisBuilder) buildTracks(
	ctx context.Context,
	subject api.AudioAnalysisSubject,
	instructions api.AudioAnalysisInstructions,
	attemptID string,
	attemptRoot string,
	prior *api.AudioAnalysisResult,
	priorResource releaseworkflow.RetainedAudioAnalysisResource,
) ([]api.AudioAnalysisTrackResult, map[api.PublicResourceID]string, api.StageStatus, error) {
	previousTracks := make(map[string]api.AudioAnalysisTrackResult)
	previousPaths := make(map[api.PublicResourceID]string)
	previousIntegrity := make(map[api.PublicResourceID]workflowAudioAnalysisArtifactIntegrity)
	if prior != nil && prior.ManifestFingerprint == subject.ManifestFingerprint && prior.ResourceID == subject.ResourceID {
		for _, track := range prior.Tracks {
			previousTracks[track.TrackID] = track
		}
		if resource, ok := priorResource.(workflowAudioAnalysisResource); ok {
			maps.Copy(previousPaths, resource.paths)
			maps.Copy(previousIntegrity, resource.integrity)
		}
	}

	reusedByTrack := make(map[string]map[api.AudioAnalysisVariant]api.AudioAnalysisArtifact, len(instructions.TrackIDs))
	artifactPaths := make(map[api.PublicResourceID]string)
	for _, trackID := range instructions.TrackIDs {
		previous, hasPrevious := previousTracks[trackID]
		reused := make(map[api.AudioAnalysisVariant]api.AudioAnalysisArtifact)
		if hasPrevious {
			for _, artifact := range previous.Artifacts {
				pathValue := previousPaths[artifact.ID]
				if artifact.Status == api.StageStatusCompleted &&
					slices.Contains(instructions.Variants, artifact.Variant) &&
					validAudioAnalysisArtifact(b.root, pathValue, artifact) &&
					validAudioAnalysisIntegrity(pathValue, previousIntegrity[artifact.ID]) {
					cloned, cloneErr := cloneAudioAnalysisArtifact(
						attemptRoot,
						attemptID,
						trackID,
						artifact,
						pathValue,
						previousIntegrity[artifact.ID],
					)
					if cloneErr != nil {
						return nil, nil, "", fmt.Errorf("clone retained audio-analysis artifact: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
							Code:    api.AudioAnalysisFailureResourceUnavailable,
							Message: "a retained audio-analysis artifact could not be transferred to the new attempt",
						}, cloneErr))
					}
					reused[artifact.Variant] = cloned.Public
					artifactPaths[cloned.Public.ID] = cloned.Path
				}
			}
		}
		reusedByTrack[trackID] = reused
	}

	type workGroup struct {
		trackIDs []string
		variants []api.AudioAnalysisVariant
	}
	missingByTrack := make(map[string][]api.AudioAnalysisVariant, len(instructions.TrackIDs))
	groupIndex := make(map[string]int)
	groups := make([]workGroup, 0, 3)
	for _, trackID := range instructions.TrackIDs {
		reused := reusedByTrack[trackID]
		missing := make([]api.AudioAnalysisVariant, 0, len(instructions.Variants))
		for _, variant := range instructions.Variants {
			if _, ok := reused[variant]; !ok {
				missing = append(missing, variant)
			}
		}
		missingByTrack[trackID] = missing
		if len(missing) == 0 {
			continue
		}
		parts := make([]string, len(missing))
		for index, variant := range missing {
			parts[index] = string(variant)
		}
		key := strings.Join(parts, "\x00")
		index, exists := groupIndex[key]
		if !exists {
			index = len(groups)
			groupIndex[key] = index
			groups = append(groups, workGroup{variants: missing})
		}
		groups[index].trackIDs = append(groups[index].trackIDs, trackID)
	}

	generatedByTrack := make(map[string]api.AudioAnalysisTrackResult, len(instructions.TrackIDs))
	var stopFailure *api.AudioAnalysisFailure
	for _, group := range groups {
		if stopFailure != nil {
			break
		}
		if err := b.revalidateSubject(ctx, instructions, subject); err != nil {
			return nil, nil, "", fmt.Errorf("revalidate audio analysis pass boundary: %w", err)
		}
		work := instructions
		work.Selection = api.AudioAnalysisSelectionSelected
		work.TrackIDs = append([]string(nil), group.trackIDs...)
		work.Variants = append([]api.AudioAnalysisVariant(nil), group.variants...)
		for _, trackID := range group.trackIDs {
			api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
				Phase:     "audio_analysis",
				ItemID:    trackID,
				Kind:      "audio_track",
				Label:     audioTrackLabel(subject, trackID),
				Status:    api.StageStatusRunning,
				Completed: 0,
				Total:     100,
				Message:   "Streaming audio from one shared FFmpeg pass.",
				ItemOnly:  true,
			})
		}
		built, analyzeErr := b.service.Analyze(ctx, subject, work, attemptID, attemptRoot)
		if analyzeErr == nil && ctx.Err() == nil {
			if err := b.revalidateSubject(ctx, work, subject); err != nil {
				return nil, nil, "", fmt.Errorf("revalidate analyzed audio tracks: %w", err)
			}
		}
		for _, builtTrack := range built {
			generatedByTrack[builtTrack.Public.TrackID] = builtTrack.Public
			for _, artifact := range builtTrack.Artifacts {
				if !pathutil.IsWithinRoot(attemptRoot, artifact.Path) {
					return nil, nil, "", fmt.Errorf("validate audio-analysis artifact path: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
						Code:    api.AudioAnalysisFailureResourceUnavailable,
						Message: "an audio-analysis artifact escaped its managed attempt directory",
					}, errors.New("audio analysis service returned an unmanaged artifact path")))
				}
				artifactPaths[artifact.Public.ID] = artifact.Path
			}
		}
		if analyzeErr != nil {
			failure, typed := api.AsAudioAnalysisFailure(analyzeErr)
			if !typed {
				return nil, nil, "", fmt.Errorf("analyze audio tracks: %w", analyzeErr)
			}
			if failure.Code == api.AudioAnalysisFailureStaleSource || failure.Code == api.AudioAnalysisFailureAmbiguousBinding {
				return nil, nil, "", fmt.Errorf("analyze invalidated audio binding: %w", analyzeErr)
			}
			stopFailure = &failure
			for _, trackID := range group.trackIDs {
				generated, ok := generatedByTrack[trackID]
				if !ok {
					generatedByTrack[trackID] = failedAudioAnalysisTrack(subject, trackID, group.variants, failure)
					continue
				}
				generatedByTrack[trackID] = completeFailedAudioVariants(generated, group.variants, failure)
			}
		}
	}

	tracks := make([]api.AudioAnalysisTrackResult, 0, len(instructions.TrackIDs))
	for index, trackID := range instructions.TrackIDs {
		previous, hasPrevious := previousTracks[trackID]
		reused := reusedByTrack[trackID]
		missing := missingByTrack[trackID]
		if len(missing) == 0 {
			previous.Artifacts = orderedAudioArtifacts(instructions.Variants, reused)
			previous.Status = api.StageStatusCompleted
			previous.Failure = nil
			tracks = append(tracks, previous)
			emitAudioTrackProgress(ctx, previous, index+1, len(instructions.TrackIDs))
			continue
		}
		generated, ok := generatedByTrack[trackID]
		if !ok {
			failure := api.AudioAnalysisFailure{Code: api.AudioAnalysisFailureInterrupted, Message: "audio analysis stopped before this track was decoded"}
			if stopFailure != nil {
				failure = *stopFailure
			}
			generated = failedAudioAnalysisTrack(subject, trackID, missing, failure)
		}
		merged := mergeAudioTrack(previous, hasPrevious, generated, instructions.Variants, reused)
		tracks = append(tracks, merged)
		emitAudioTrackProgress(ctx, merged, index+1, len(instructions.TrackIDs))
	}
	status := audioAnalysisOutcome(tracks)
	if stopFailure != nil && stopFailure.Code == api.AudioAnalysisFailureCanceled {
		status = api.StageStatusCanceled
	} else if stopFailure != nil && stopFailure.Code == api.AudioAnalysisFailureInterrupted {
		status = api.StageStatusInterrupted
	}
	return tracks, artifactPaths, status, nil
}

func (b workflowAudioAnalysisBuilder) revalidateSubject(
	ctx context.Context,
	instructions api.AudioAnalysisInstructions,
	expected api.AudioAnalysisSubject,
) error {
	current, err := b.resolver.ResolveAudioAnalysisSubject(ctx, instructions)
	if err != nil {
		return fmt.Errorf("resolve current audio analysis subject: %w", err)
	}
	if current.Release != expected.Release || current.SourcePath != expected.SourcePath || current.VideoPath != expected.VideoPath ||
		current.SourceFingerprint != expected.SourceFingerprint || current.ResourceID != expected.ResourceID ||
		current.ManifestFingerprint != expected.ManifestFingerprint || current.PrimaryTrackID != expected.PrimaryTrackID ||
		!sameAudioAnalysisTrackAuthority(current.Tracks, expected.Tracks) {
		return fmt.Errorf("compare current audio analysis subject: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureStaleSource,
			Message: "the prepared audio source changed and must be refreshed",
		}, errors.New("audio analysis subject authority changed during analysis")))
	}
	return nil
}

func sameAudioAnalysisTrackAuthority(left []api.MediaTrackFacts, right []api.MediaTrackFacts) bool {
	return slices.EqualFunc(left, right, func(a api.MediaTrackFacts, b api.MediaTrackFacts) bool {
		return a.ID == b.ID && a.ResourceID == b.ResourceID && a.ManifestFingerprint == b.ManifestFingerprint &&
			a.Ordinal == b.Ordinal && a.Codec == b.Codec && a.ChannelLayout == b.ChannelLayout &&
			a.Channels == b.Channels && a.SampleRate == b.SampleRate
	})
}

func completeFailedAudioVariants(
	track api.AudioAnalysisTrackResult,
	variants []api.AudioAnalysisVariant,
	failure api.AudioAnalysisFailure,
) api.AudioAnalysisTrackResult {
	present := make(map[api.AudioAnalysisVariant]struct{}, len(track.Artifacts))
	for _, artifact := range track.Artifacts {
		present[artifact.Variant] = struct{}{}
	}
	for _, variant := range variants {
		if _, ok := present[variant]; ok {
			continue
		}
		variantFailure := failure
		track.Artifacts = append(track.Artifacts, api.AudioAnalysisArtifact{
			Variant: variant,
			Status:  api.StageStatusFailed,
			Failure: &variantFailure,
		})
	}
	trackFailure := failure
	track.Failure = &trackFailure
	return track
}

func audioAnalysisAttemptRoot(root string, subject api.AudioAnalysisSubject, attemptID string) (string, error) {
	if err := validateAudioAnalysisAttemptID(attemptID); err != nil {
		return "", err
	}
	releaseRoot, _, err := paths.ReleaseTempDirFor(root, subject.SourcePath, api.ReleaseInfo{})
	if err != nil {
		return "", fmt.Errorf("resolve audio-analysis attempt root: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureResourceUnavailable,
			Message: "the managed audio-analysis output root is unavailable",
		}, fmt.Errorf("resolve audio analysis attempt root: %w", err)))
	}
	attemptRoot := filepath.Join(
		releaseRoot,
		"audio-analysis",
		strconv.FormatUint(uint64(subject.Release.Generation), 10),
		attemptID,
	)
	if !pathutil.IsWithinRoot(releaseRoot, attemptRoot) {
		return "", fmt.Errorf("validate audio-analysis attempt root: %w", api.NewAudioAnalysisError(api.AudioAnalysisFailure{
			Code:    api.AudioAnalysisFailureResourceUnavailable,
			Message: "the managed audio-analysis output path was rejected",
		}, errors.New("audio analysis attempt escaped the managed release root")))
	}
	return attemptRoot, nil
}

func validateAudioAnalysisAttemptID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 96 {
		return errors.New("audio analysis attempt ID is invalid")
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '-' && character != '_' {
			return errors.New("audio analysis attempt ID is invalid")
		}
	}
	return nil
}

func cloneAudioAnalysisArtifact(
	attemptRoot string,
	attemptID string,
	trackID string,
	artifact api.AudioAnalysisArtifact,
	sourcePath string,
	integrity workflowAudioAnalysisArtifactIntegrity,
) (audioanalysis.Artifact, error) {
	trackDigest := sha256.Sum256([]byte(trackID))
	trackDirectory := filepath.Join(attemptRoot, "track_"+hex.EncodeToString(trackDigest[:12]))
	if !pathutil.IsWithinRoot(attemptRoot, trackDirectory) {
		return audioanalysis.Artifact{}, errors.New("audio analysis clone track path escaped the attempt root")
	}
	if err := os.MkdirAll(trackDirectory, 0o700); err != nil {
		return audioanalysis.Artifact{}, fmt.Errorf("create cloned audio-analysis track directory: %w", err)
	}
	extension := ".png"
	if artifact.Variant == api.AudioAnalysisStats {
		extension = ".txt"
	}
	destination := filepath.Join(trackDirectory, string(artifact.Variant)+extension)
	if !pathutil.IsWithinRoot(trackDirectory, destination) {
		return audioanalysis.Artifact{}, errors.New("audio analysis clone output path escaped the track root")
	}
	if err := copyAudioAnalysisFile(trackDirectory, sourcePath, destination, integrity); err != nil {
		return audioanalysis.Artifact{}, err
	}
	idDigest := sha256.Sum256([]byte(attemptID + "\x00" + trackID + "\x00" + string(artifact.Variant)))
	artifact.ID = api.PublicResourceID("audio_" + hex.EncodeToString(idDigest[:12]))
	return audioanalysis.Artifact{Public: artifact, Path: destination}, nil
}

func copyAudioAnalysisFile(
	root string,
	sourcePath string,
	destination string,
	integrity workflowAudioAnalysisArtifactIntegrity,
) error {
	if !pathutil.IsWithinRoot(root, destination) {
		return errors.New("audio analysis clone destination is outside the managed root")
	}
	if _, err := os.Stat(destination); err == nil {
		return errors.New("audio analysis clone destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect audio analysis clone destination: %w", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open retained audio-analysis artifact: %w", err)
	}
	defer source.Close()
	temporary, err := os.CreateTemp(root, ".audio-analysis-clone-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary audio-analysis clone: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict temporary audio-analysis clone: %w", err)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, hasher), source)
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("copy retained audio-analysis artifact: %w", err)
	}
	if written != integrity.Size || !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), integrity.SHA256) {
		_ = temporary.Close()
		return errors.New("retained audio-analysis artifact failed integrity verification")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync retained audio-analysis artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close retained audio-analysis artifact: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("publish retained audio-analysis clone: %w", err)
	}
	return nil
}

func failedAudioAnalysisTrack(
	subject api.AudioAnalysisSubject,
	trackID string,
	variants []api.AudioAnalysisVariant,
	failure api.AudioAnalysisFailure,
) api.AudioAnalysisTrackResult {
	result := api.AudioAnalysisTrackResult{
		TrackID: trackID,
		Status:  api.StageStatusFailed,
		Failure: &failure,
	}
	for _, track := range subject.Tracks {
		if track.ID != trackID {
			continue
		}
		result.Ordinal = track.Ordinal
		result.Title = track.Title
		result.Codec = track.Codec
		if len(track.Languages) > 0 {
			result.Language = track.Languages[0]
		}
		result.ChannelLayout = track.ChannelLayout
		result.Channels = track.Channels
		result.SampleRate = track.SampleRate
		break
	}
	for _, variant := range variants {
		variantFailure := failure
		result.Artifacts = append(result.Artifacts, api.AudioAnalysisArtifact{
			Variant: variant,
			Status:  api.StageStatusFailed,
			Failure: &variantFailure,
		})
	}
	return result
}

func audioTrackLabel(subject api.AudioAnalysisSubject, trackID string) string {
	for _, track := range subject.Tracks {
		if track.ID == trackID {
			return fmt.Sprintf("Audio track %d", track.Ordinal)
		}
	}
	return "Audio track"
}

func emitAudioTrackProgress(ctx context.Context, track api.AudioAnalysisTrackResult, completed int, total int) {
	status := track.Status
	message := "Audio track analysis completed."
	if status == api.StageStatusPartial || status == api.StageStatusFailed {
		message = "Audio track analysis retained one or more failures."
	}
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		Phase:     "audio_analysis",
		ItemID:    track.TrackID,
		Kind:      "audio_track",
		Label:     fmt.Sprintf("Audio track %d", track.Ordinal),
		Status:    status,
		Completed: 100,
		Total:     100,
		Message:   message,
		ItemOnly:  true,
	})
	api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
		Phase:     "audio_analysis",
		Status:    status,
		Completed: completed,
		Total:     total,
		Message:   message,
	})
	for _, artifact := range track.Artifacts {
		artifactMessage := "Analysis output completed."
		if artifact.Status == api.StageStatusFailed {
			artifactMessage = "Analysis output failed."
		}
		api.EmitWorkflowProgress(ctx, api.WorkflowProgressUpdate{
			Phase:     "audio_analysis_output",
			ItemID:    track.TrackID + ":" + string(artifact.Variant),
			Kind:      "audio_output",
			Label:     fmt.Sprintf("Track %d %s", track.Ordinal, artifact.Variant),
			Status:    artifact.Status,
			Completed: 1,
			Total:     1,
			Message:   artifactMessage,
			ItemOnly:  true,
		})
	}
}

func mergeAudioTrack(
	previous api.AudioAnalysisTrackResult,
	hasPrevious bool,
	generated api.AudioAnalysisTrackResult,
	variants []api.AudioAnalysisVariant,
	reused map[api.AudioAnalysisVariant]api.AudioAnalysisArtifact,
) api.AudioAnalysisTrackResult {
	merged := generated
	if hasPrevious && previous.SampleFrames > 0 && generated.SampleFrames == 0 {
		merged.SampleFrames = previous.SampleFrames
		merged.Duration = previous.Duration
	}
	generatedByVariant := make(map[api.AudioAnalysisVariant]api.AudioAnalysisArtifact, len(generated.Artifacts))
	for _, artifact := range generated.Artifacts {
		generatedByVariant[artifact.Variant] = artifact
	}
	merged.Artifacts = make([]api.AudioAnalysisArtifact, 0, len(variants))
	for _, variant := range variants {
		if artifact, ok := reused[variant]; ok {
			merged.Artifacts = append(merged.Artifacts, artifact)
			continue
		}
		if artifact, ok := generatedByVariant[variant]; ok {
			merged.Artifacts = append(merged.Artifacts, artifact)
			continue
		}
		if generated.Failure != nil {
			failure := *generated.Failure
			merged.Artifacts = append(merged.Artifacts, api.AudioAnalysisArtifact{
				Variant: variant,
				Status:  api.StageStatusFailed,
				Failure: &failure,
			})
		}
	}
	successes := 0
	for _, artifact := range merged.Artifacts {
		if artifact.Status == api.StageStatusCompleted {
			successes++
		}
	}
	switch {
	case successes == len(variants):
		merged.Status = api.StageStatusCompleted
		merged.Failure = nil
	case successes > 0:
		merged.Status = api.StageStatusPartial
	default:
		merged.Status = api.StageStatusFailed
	}
	return merged
}

func orderedAudioArtifacts(
	variants []api.AudioAnalysisVariant,
	byVariant map[api.AudioAnalysisVariant]api.AudioAnalysisArtifact,
) []api.AudioAnalysisArtifact {
	artifacts := make([]api.AudioAnalysisArtifact, 0, len(variants))
	for _, variant := range variants {
		artifacts = append(artifacts, byVariant[variant])
	}
	return artifacts
}

func audioAnalysisOutcome(tracks []api.AudioAnalysisTrackResult) api.StageStatus {
	successes := 0
	failed := 0
	for _, track := range tracks {
		for _, artifact := range track.Artifacts {
			if artifact.Status == api.StageStatusCompleted {
				successes++
			} else {
				failed++
			}
		}
		if track.Status == api.StageStatusFailed && len(track.Artifacts) == 0 {
			failed++
		}
	}
	switch {
	case failed == 0 && successes > 0:
		return api.StageStatusCompleted
	case successes > 0:
		return api.StageStatusPartial
	default:
		return api.StageStatusFailed
	}
}

func validAudioAnalysisArtifact(root string, pathValue string, artifact api.AudioAnalysisArtifact) bool {
	if artifact.Variant != api.AudioAnalysisStats {
		return validAudioAnalysisPNG(root, pathValue, artifact.Width, artifact.Height)
	}
	if strings.TrimSpace(pathValue) == "" || !pathutil.IsWithinRoot(root, pathValue) {
		return false
	}
	file, err := os.Open(pathValue)
	if err != nil {
		return false
	}
	valid := validAudioAnalysisStats(file, artifact)
	closeErr := file.Close()
	return valid && closeErr == nil
}

func validAudioAnalysisStats(reader io.Reader, artifact api.AudioAnalysisArtifact) bool {
	if strings.TrimSpace(artifact.Text) == "" || len(artifact.Text) > api.AudioAnalysisStatsMaxBytes || artifact.Width != 0 || artifact.Height != 0 {
		return false
	}
	text, err := io.ReadAll(io.LimitReader(reader, api.AudioAnalysisStatsMaxBytes+1))
	return err == nil && string(text) == artifact.Text
}

func validAudioAnalysisPNG(root string, pathValue string, width int, height int) bool {
	if width <= 0 || height <= 0 || strings.TrimSpace(pathValue) == "" || !pathutil.IsWithinRoot(root, pathValue) {
		return false
	}
	file, err := os.Open(pathValue)
	if err != nil {
		return false
	}
	configuration, format, decodeErr := image.DecodeConfig(file)
	closeErr := file.Close()
	return decodeErr == nil && closeErr == nil && format == "png" && configuration.Width == width && configuration.Height == height
}

func retainWorkflowAudioAnalysisResource(
	root string,
	attemptRoot string,
	paths map[api.PublicResourceID]string,
) (workflowAudioAnalysisResource, error) {
	integrity := make(map[api.PublicResourceID]workflowAudioAnalysisArtifactIntegrity, len(paths))
	for id, pathValue := range paths {
		if strings.TrimSpace(string(id)) == "" || !pathutil.IsWithinRoot(attemptRoot, pathValue) {
			return workflowAudioAnalysisResource{}, errors.New("retain workflow audio analysis: artifact path is invalid")
		}
		value, err := calculateAudioAnalysisArtifactIntegrity(pathValue)
		if err != nil {
			return workflowAudioAnalysisResource{}, fmt.Errorf("retain workflow audio analysis artifact: %w", err)
		}
		integrity[id] = value
	}
	return workflowAudioAnalysisResource{
		root:        root,
		attemptRoot: attemptRoot,
		paths:       maps.Clone(paths),
		integrity:   integrity,
	}, nil
}

func calculateAudioAnalysisArtifactIntegrity(pathValue string) (workflowAudioAnalysisArtifactIntegrity, error) {
	file, err := os.Open(pathValue)
	if err != nil {
		return workflowAudioAnalysisArtifactIntegrity{}, fmt.Errorf("open audio-analysis artifact for integrity: %w", err)
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil {
		return workflowAudioAnalysisArtifactIntegrity{}, fmt.Errorf("hash audio-analysis artifact: %w", copyErr)
	}
	if closeErr != nil {
		return workflowAudioAnalysisArtifactIntegrity{}, fmt.Errorf("close hashed audio-analysis artifact: %w", closeErr)
	}
	if size <= 0 {
		return workflowAudioAnalysisArtifactIntegrity{}, errors.New("audio-analysis artifact is empty")
	}
	return workflowAudioAnalysisArtifactIntegrity{Size: size, SHA256: hex.EncodeToString(hasher.Sum(nil))}, nil
}

func validAudioAnalysisIntegrity(pathValue string, expected workflowAudioAnalysisArtifactIntegrity) bool {
	actual, err := calculateAudioAnalysisArtifactIntegrity(pathValue)
	return err == nil && actual.Size == expected.Size && strings.EqualFold(actual.SHA256, expected.SHA256)
}

func validAudioAnalysisArtifactIntegrity(value workflowAudioAnalysisArtifactIntegrity) bool {
	digest, err := hex.DecodeString(value.SHA256)
	return value.Size > 0 && err == nil && len(digest) == sha256.Size
}

func openIntegrityVerifiedAudioAnalysisArtifact(
	pathValue string,
	expected workflowAudioAnalysisArtifactIntegrity,
) (*os.File, error) {
	if !validAudioAnalysisArtifactIntegrity(expected) {
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	file, err := os.Open(pathValue)
	if err != nil {
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(hasher, file)
	if copyErr != nil || size != expected.Size || !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expected.SHA256) {
		_ = file.Close()
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	return file, nil
}

type workflowAudioAnalysisResource struct {
	root        string
	attemptRoot string
	paths       map[api.PublicResourceID]string
	integrity   map[api.PublicResourceID]workflowAudioAnalysisArtifactIntegrity
}

type workflowAudioAnalysisArtifactIntegrity struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type persistedWorkflowAudioAnalysisResource struct {
	AttemptRoot string                                                          `json:"attemptRoot"`
	Paths       map[api.PublicResourceID]string                                 `json:"paths"`
	Integrity   map[api.PublicResourceID]workflowAudioAnalysisArtifactIntegrity `json:"integrity"`
}

func (r workflowAudioAnalysisResource) MarshalPrivateResource() (string, []byte, error) {
	if strings.TrimSpace(r.root) == "" || !validAudioAnalysisAttemptRoot(r.root, r.attemptRoot) || len(r.paths) == 0 ||
		len(r.integrity) != len(r.paths) {
		return "", nil, errors.New("marshal workflow audio analysis: resource is empty")
	}
	paths := make(map[api.PublicResourceID]string, len(r.paths))
	integrity := make(map[api.PublicResourceID]workflowAudioAnalysisArtifactIntegrity, len(r.integrity))
	for id, pathValue := range r.paths {
		value, ok := r.integrity[id]
		if strings.TrimSpace(string(id)) == "" || !pathutil.IsWithinRoot(r.attemptRoot, pathValue) || !ok ||
			!validAudioAnalysisArtifactIntegrity(value) || !validAudioAnalysisIntegrity(pathValue, value) {
			return "", nil, errors.New("marshal workflow audio analysis: artifact path is invalid")
		}
		paths[id] = filepath.Clean(pathValue)
		integrity[id] = value
	}
	payload, err := json.Marshal(persistedWorkflowAudioAnalysisResource{
		AttemptRoot: filepath.Clean(r.attemptRoot),
		Paths:       paths,
		Integrity:   integrity,
	})
	if err != nil {
		return "", nil, fmt.Errorf("marshal workflow audio analysis: %w", err)
	}
	return workflowPrivateResourceKindAudioAnalysis, payload, nil
}

func decodeWorkflowAudioAnalysisResource(root string, payload []byte) (any, error) {
	attemptRoot, persisted, err := decodeWorkflowAudioAnalysisResourceAuthority(root, payload)
	if err != nil {
		return nil, err
	}
	if len(persisted.Paths) == 0 || len(persisted.Integrity) != len(persisted.Paths) {
		return nil, errors.New("decode workflow audio analysis: artifact map is empty")
	}
	paths := make(map[api.PublicResourceID]string, len(persisted.Paths))
	integrity := make(map[api.PublicResourceID]workflowAudioAnalysisArtifactIntegrity, len(persisted.Integrity))
	for id, pathValue := range persisted.Paths {
		value, ok := persisted.Integrity[id]
		if strings.TrimSpace(string(id)) == "" || !pathutil.IsWithinRoot(attemptRoot, pathValue) || !ok ||
			!validAudioAnalysisArtifactIntegrity(value) || !validAudioAnalysisIntegrity(pathValue, value) {
			return nil, errors.New("decode workflow audio analysis: artifact path is invalid")
		}
		paths[id] = filepath.Clean(pathValue)
		integrity[id] = value
	}
	return workflowAudioAnalysisResource{
		root:        filepath.Clean(strings.TrimSpace(root)),
		attemptRoot: attemptRoot,
		paths:       paths,
		integrity:   integrity,
	}, nil
}

func decodeWorkflowAudioAnalysisResourceForRelease(root string, payload []byte) (any, error) {
	attemptRoot, _, err := decodeWorkflowAudioAnalysisResourceAuthority(root, payload)
	if err != nil {
		return nil, err
	}
	return workflowAudioAnalysisResource{
		root:        filepath.Clean(strings.TrimSpace(root)),
		attemptRoot: attemptRoot,
	}, nil
}

func decodeWorkflowAudioAnalysisResourceAuthority(
	root string,
	payload []byte,
) (string, persistedWorkflowAudioAnalysisResource, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || root == "" {
		return "", persistedWorkflowAudioAnalysisResource{}, errors.New("decode workflow audio analysis: managed root is unavailable")
	}
	var persisted persistedWorkflowAudioAnalysisResource
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return "", persistedWorkflowAudioAnalysisResource{}, fmt.Errorf("decode workflow audio analysis: %w", err)
	}
	if !validAudioAnalysisAttemptRoot(root, persisted.AttemptRoot) {
		return "", persistedWorkflowAudioAnalysisResource{}, errors.New("decode workflow audio analysis: managed attempt root is invalid")
	}
	return filepath.Clean(persisted.AttemptRoot), persisted, nil
}

func validAudioAnalysisAttemptRoot(root string, attemptRoot string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	attemptRoot = filepath.Clean(strings.TrimSpace(attemptRoot))
	return root != "." && attemptRoot != "." && root != attemptRoot && pathutil.IsWithinRoot(root, attemptRoot)
}

// Release removes only this immutable attempt directory. Reused artifacts are
// copied into each new attempt, so no retained resource shares file ownership.
func (r workflowAudioAnalysisResource) Release() error {
	if !validAudioAnalysisAttemptRoot(r.root, r.attemptRoot) {
		return errors.New("release workflow audio analysis: managed attempt root is invalid")
	}
	if err := os.RemoveAll(r.attemptRoot); err != nil {
		return fmt.Errorf("release workflow audio analysis attempt: %w", err)
	}
	return nil
}

func (r workflowAudioAnalysisResource) OpenArtifact(
	ctx context.Context,
	snapshot api.AudioAnalysisResult,
	artifactID api.PublicResourceID,
) (releaseworkflow.MediaArtifactContent, error) {
	if err := ctx.Err(); err != nil {
		return releaseworkflow.MediaArtifactContent{}, fmt.Errorf("open retained audio analysis artifact: %w", err)
	}
	var expected api.AudioAnalysisArtifact
	for _, track := range snapshot.Tracks {
		for _, artifact := range track.Artifacts {
			if artifact.ID == artifactID && artifact.Status == api.StageStatusCompleted {
				expected = artifact
				break
			}
		}
	}
	pathValue, ok := r.paths[artifactID]
	integrity, integrityOK := r.integrity[artifactID]
	if expected.ID == "" || !ok || !integrityOK || !pathutil.IsWithinRoot(r.root, pathValue) {
		return releaseworkflow.MediaArtifactContent{}, releaseworkflow.ErrPrivateResourceUnavailable
	}
	file, err := openIntegrityVerifiedAudioAnalysisArtifact(pathValue, integrity)
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, releaseworkflow.ErrPrivateResourceUnavailable
	}
	if expected.Variant == api.AudioAnalysisStats {
		valid := validAudioAnalysisStats(file, expected)
		_, seekErr := file.Seek(0, 0)
		if !valid || seekErr != nil {
			_ = file.Close()
			return releaseworkflow.MediaArtifactContent{}, releaseworkflow.ErrPrivateResourceUnavailable
		}
		return releaseworkflow.MediaArtifactContent{Body: file, ContentType: "text/plain; charset=utf-8"}, nil
	}
	configuration, format, decodeErr := image.DecodeConfig(file)
	_, seekErr := file.Seek(0, 0)
	if decodeErr != nil || seekErr != nil || format != "png" ||
		configuration.Width != expected.Width || configuration.Height != expected.Height {
		_ = file.Close()
		return releaseworkflow.MediaArtifactContent{}, releaseworkflow.ErrPrivateResourceUnavailable
	}
	return releaseworkflow.MediaArtifactContent{Body: file, ContentType: "image/png"}, nil
}

func (r workflowAudioAnalysisResource) LocalArtifactPath(
	snapshot api.AudioAnalysisResult,
	artifactID api.PublicResourceID,
) (string, error) {
	var expected api.AudioAnalysisArtifact
	for _, track := range snapshot.Tracks {
		for _, artifact := range track.Artifacts {
			if artifact.ID == artifactID && artifact.Status == api.StageStatusCompleted {
				expected = artifact
				break
			}
		}
	}
	pathValue, ok := r.paths[artifactID]
	integrity, integrityOK := r.integrity[artifactID]
	if expected.ID == "" || !ok || !integrityOK ||
		!validAudioAnalysisArtifact(r.root, pathValue, expected) ||
		!validAudioAnalysisIntegrity(pathValue, integrity) {
		return "", releaseworkflow.ErrPrivateResourceUnavailable
	}
	absolute, err := filepath.Abs(pathValue)
	if err != nil {
		return "", releaseworkflow.ErrPrivateResourceUnavailable
	}
	return absolute, nil
}

func newWorkflowAudioAnalysisBuilder(
	resolver audioAnalysisSubjectResolver,
	service *audioanalysis.Service,
	root string,
) workflowAudioAnalysisBuilder {
	return workflowAudioAnalysisBuilder{
		resolver: resolver,
		service:  service,
		root:     filepath.Clean(strings.TrimSpace(root)),
	}
}
