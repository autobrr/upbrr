// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/Audionut/go-hdr10-plus/extract"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func (b workflowHDRAnalysisBuilder) buildNative(
	ctx context.Context, preparation releaseworkflow.HDRPreparation, instructions api.HDRAnalysisInstructions, attempt string, now time.Time,
	prior *api.HDRAnalysisResult, priorResource releaseworkflow.RetainedHDRAnalysisResource,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	retained map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource,
	publish func(releaseworkflow.HDRExtractionRecord, releaseworkflow.RetainedHDRExtractionResource) error,
	measurements *releaseworkflow.HDRMeasurementStore,
) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
	release := preparation.Release
	subject, err := b.resolver.ResolveRetainedHDRAnalysisSubject(ctx, instructions, preparation.Targets)
	if err != nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("resolve native HDR source: %w", err)
	}
	root, err := hdrAttemptRoot(b.root, release, "hdr-analysis", attempt)
	if err != nil {
		return api.HDRAnalysisResult{}, nil, err
	}
	if err := createHDRDirectory(root); err != nil {
		return api.HDRAnalysisResult{}, nil, err
	}
	resource := hdrManagedResource{
		Root:      b.root,
		Directory: root,
		Kind:      "analysis",
		Files:     make(map[api.PublicResourceID]hdrManagedFile),
	}
	result := api.HDRAnalysisResult{
		Release:             release,
		AttemptID:           attempt,
		ManifestFingerprint: subject.ManifestFingerprint,
		TargetIDs:           instructions.TargetIDs,
		PeakSource:          instructions.PeakSource,
		ProfileVersion:      instructions.ProfileVersion,
		Formats:             instructions.Formats,
		Decoded:             instructions.Decoded,
		CreatedAt:           now,
		CompletedAt:         &now,
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Title      string
		Peak       api.HDRPeakSource
		Profile    string
		Formats    api.HDRAnalysisFormats
		Dependency string
		Renderer   string
	}{subject.Title, instructions.PeakSource, instructions.ProfileVersion, instructions.Formats, hdranalysis.NativeDependencyFingerprint, "hdr-plots-v3"})
	if err != nil {
		_ = resource.Release()
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("fingerprint native HDR rendering: %w", err)
	}
	result.RenderFingerprint = string(fingerprint)
	retainedArtifactsOnly := measurements != nil && measurements.RetainedArtifactsOnly
	if retainedArtifactsOnly {
		if prior == nil || prior.Release.SourcePath != release.SourcePath || prior.ManifestFingerprint != result.ManifestFingerprint ||
			prior.ProfileVersion != result.ProfileVersion || prior.Formats != result.Formats {
			_ = resource.Release()
			return api.HDRAnalysisResult{}, nil, fmt.Errorf("restore HDR analysis source: %w", api.NewHDRAnalysisError(api.HDRAnalysisFailure{
				Code:    api.HDRAnalysisFailureResourceUnavailable,
				Message: "Previous HDR analysis does not match this source; explicitly generate new analysis.",
			}, nil))
		}
		// Reopening preserves the saved run's rendering provenance. An explicit
		// Generate request applies current titles, renderers and measurement options.
		result.RenderFingerprint = prior.RenderFingerprint
		result.Decoded = prior.Decoded
	}
	clonedPaths := make(map[string]string)
	err = b.service.WithSession(ctx, func(session *hdranalysis.Session) error {
		discs := make(map[string]*hdranalysis.NativeDiscOutcome)
		pendingPlaylists := make(map[string]int)
		defer func() {
			for _, disc := range discs {
				disc.Release()
			}
		}()
		discBytes := func() int64 {
			var used int64
			for _, disc := range discs {
				used += disc.RetainedBytes()
			}
			return used
		}
		// Resolve artifact reuse before selecting scan targets, so a retry for
		// one playlist never assembles fresh copies of already retained targets.
		copiedTargets := make(map[string]api.HDRAnalysisTargetResult)
		if instructions.Decoded == nil && prior != nil && (prior.Decoded == nil || retainedArtifactsOnly) && (priorResource != nil || retainedArtifactsOnly) &&
			prior.ProfileVersion == result.ProfileVersion &&
			prior.Release.SourcePath == release.SourcePath &&
			prior.ManifestFingerprint == result.ManifestFingerprint &&
			prior.RenderFingerprint == result.RenderFingerprint {
			for index, target := range subject.Targets {
				for _, previous := range prior.Targets {
					if previous.TargetID != target.Target.ID || !retainedArtifactsOnly &&
						(previous.Status != api.StageStatusCompleted || len(previous.Artifacts) == 0) {
						continue
					}
					copied, paths, copyErr := copyNativeHDRTarget(ctx, *prior, previous, priorResource, resource, attempt, index)
					if retainedArtifactsOnly && copyErr != nil {
						return api.NewHDRAnalysisError(api.HDRAnalysisFailure{
							Code:    api.HDRAnalysisFailureResourceUnavailable,
							Message: "Previous HDR analysis artifacts are unavailable; explicitly generate new analysis to replace them.",
						}, copyErr)
					}
					if copyErr == nil {
						if err := b.revalidate(ctx, instructions, subject); err != nil {
							return err
						}
						copied.Label = target.Target.Label
						copiedTargets[target.Target.ID] = copied
						maps.Copy(clonedPaths, paths)
					}
					break
				}
			}
		}
		if retainedArtifactsOnly && len(copiedTargets) != len(subject.Targets) {
			return api.NewHDRAnalysisError(api.HDRAnalysisFailure{
				Code:    api.HDRAnalysisFailureResourceUnavailable,
				Message: "Previous HDR analysis artifacts are unavailable; explicitly generate new analysis to replace them.",
			}, nil)
		}
		for index, target := range subject.Targets {
			instructions := instructions
			instructions.DecodedRetry = hdrTargetDecodedRetry(instructions.DecodedRetry, prior, target.Target.ID)
			outcome := api.HDRAnalysisTargetResult{TargetID: target.Target.ID, Label: target.Target.Label}
			if copied, ok := copiedTargets[target.Target.ID]; ok {
				result.Targets = append(result.Targets, copied)
				continue
			}
			identity := hdranalysis.ExtractionIdentity{
				SourceFingerprint: subject.SourceFingerprint,
				TargetID:          target.Target.ID,
				SelectionPolicy:   target.Target.SelectionPolicy,
			}
			var evidence *hdranalysis.NativeEvidence
			var nativeRecord releaseworkflow.HDRExtractionRecord
			for _, id := range hdrExtractionIDs(entries) {
				record := entries[id]
				if record.Release == release && record.SourceFingerprint == identity.SourceFingerprint && record.TargetID == identity.TargetID &&
					record.SelectionPolicy == identity.SelectionPolicy && record.Schema == api.HDRExtractionSchemaV2 {
					nativeRecord = record
					break
				}
			}
			var found bool
			var targetErr error
			var freshMeasurements []*hdranalysis.MeasurementReport
			var boundaryPlan *hdranalysis.BoundaryPlan
			nativeRendered, nativeCheckpointed := false, false
			prepareNativeRender := func() error {
				outcome.Formats = evidence.Results
				if instructions.Decoded != nil && instructions.Decoded.BoundarySync {
					input := hdranalysis.DecodedInput{
						Path:        target.Path,
						Fingerprint: subject.SourceFingerprint,
						Native:      evidence,
					}
					if target.Target.Evidence.Decoded != nil {
						input.Source = *target.Target.Evidence.Decoded
					}
					var planErr error
					boundaryPlan, planErr = hdranalysis.PlanBoundary(input, instructions.Decoded.ActiveArea)
					if planErr != nil {
						b.service.ReportFailure(ctx, "boundary_planning", planErr)
					}
				}
				if err := evidence.RetainReportSummary(); err != nil {
					return fmt.Errorf("prepare HDR checkpoint: %w", err)
				}
				b.renderNativePlots(ctx, session, subject.Title, evidence, &outcome, resource, instructions.PeakSource, attempt, index, nil,
					instructions.ProfileVersion, nativeRecord.SHA256, result.RenderFingerprint)
				nativeRendered = true
				evidence.Release()
				return nil
			}
			preparedAudio, audioErr := b.prepareHDRAudioCapture(ctx, session, subject, target, instructions, &nativeRecord, measurements,
				&outcome, resource, attempt, index, result.RenderFingerprint)
			if audioErr != nil {
				return audioErr
			}
			if preparedAudio != nil {
				preparedAudio.CheckpointNative = func(native *hdranalysis.NativeExtraction, _ []*hdranalysis.MeasurementReport) error {
					var err error
					evidence, err = native.Evidence(instructions.Formats, target.Target.Evidence)
					identity.ResolvedTrackID = native.TrackID
					complete := native.ScanComplete
					native.Release()
					if err != nil {
						return fmt.Errorf("extract native evidence: %w", err)
					}
					if !complete || evidence == nil {
						return extract.ErrIncomplete
					}
					if err := b.resolver.ValidateHDRAnalysisSource(ctx, release); err != nil {
						return fmt.Errorf("validate HDR source: %w", err)
					}
					record, owned, err := b.retainNativeExtraction(ctx, release, identity, instructions.Formats, evidence, attempt+"-"+strconv.Itoa(index+1))
					if err != nil {
						return err
					}
					if err := publish(record, owned); err != nil {
						return err
					}
					nativeRecord, nativeCheckpointed = record, true
					return prepareNativeRender()
				}
			}
			sourceRetry := hdrNeedsSourceRetry(subject, target, prior, entries, measurements, api.HDRExtractionSchemaV2)
			if !sourceRetry {
				evidence, found, targetErr = restoreNativeHDREvidenceWithRetained(ctx, release, identity, instructions.Formats, entries, retained, discBytes())
			}
			if found && targetErr != nil && ctx.Err() == nil {
				targetErr = api.NewHDRAnalysisError(api.HDRAnalysisFailure{
					Code:    api.HDRAnalysisFailureResourceUnavailable,
					Message: "Retained HDR metadata is unavailable or incompatible; explicitly retry source analysis.",
				}, targetErr)
			}
			if found && targetErr == nil && target.DiscRoot == "" && automaticHDRNeedsCapture(instructions, nativeRecord, measurements) {
				// An explicit new automatic measurement (or its explicit retry)
				// grants one combined source pass. A missing saved report remains
				// a recovery outcome and does not silently grant another pass.
				evidence.Release()
				evidence, found = nil, false
			}
			if !found && targetErr == nil && target.CapturedFailure != nil && target.CapturedNativePath == "" && !sourceRetry {
				targetErr = api.NewHDRAnalysisError(*target.CapturedFailure, nil)
			}
			if !found && targetErr == nil && !sourceRetry {
				// A v1 record cannot authorize automatic rereading to discover DV.
				for _, record := range entries {
					if record.Release == release && record.TargetID == target.Target.ID && record.Schema != api.HDRExtractionSchemaV2 {
						targetErr = api.NewHDRAnalysisError(
							api.HDRAnalysisFailure{
								Code:    api.HDRAnalysisFailureResourceUnavailable,
								Message: "Retained HDR10+ evidence lacks Dolby Vision; explicitly retry source analysis.",
							},
							nil,
						)
						break
					}
				}
			}
			if !found && targetErr == nil {
				var native *hdranalysis.NativeExtraction
				var timeline []hdranalysis.TimelineItem
				scanComplete := false
				readsSource := target.CapturedNativePath == "" || sourceRetry
				if readsSource {
					targetErr = b.resolver.ValidateHDRAnalysisSource(ctx, release)
				}
				switch {
				case targetErr != nil:
				case target.DiscRoot != "":
					switch {
					case target.CapturedNativePath != "" && !sourceRetry:
						evidence, targetErr = b.readCapturedNative(ctx, target, identity, instructions.Formats, discBytes())
						identity.ResolvedTrackID = target.CapturedTrackID
						scanComplete = targetErr == nil
					case target.CapturedPath != "" && !sourceRetry:
						targetErr = api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable,
							Message: "The retained HDR10+ capture lacks Dolby Vision; explicitly retry source analysis."}, nil)
					default:
						disc, scanned := discs[target.DiscRoot]
						if !scanned {
							var playlists []string
							for _, selected := range subject.Targets {
								_, copied := copiedTargets[selected.Target.ID]
								if selected.DiscRoot == target.DiscRoot && !copied && nativeHDRNeedsSource(subject, selected, prior, entries, measurements) {
									playlists = append(playlists, selected.Target.Playlist)
								}
							}
							pendingPlaylists[target.DiscRoot] = len(playlists)
							disc, targetErr = session.ScanNativeDisc(
								ctx,
								nativeDiscOptions(subject, target, root),
								instructions.Formats,
								discBytes(),
								playlists...)
							if disc != nil {
								discs[target.DiscRoot] = disc
							}
						}
						if targetErr == nil {
							native, timeline, targetErr = disc.TakePlaylist(target.Target.Playlist, discBytes()-disc.RetainedBytes())
							pendingPlaylists[target.DiscRoot]--
							if pendingPlaylists[target.DiscRoot] == 0 {
								disc.Release()
								discs[target.DiscRoot] = nil
							}
						}
					}
				default:
					if instructions.Decoded != nil && (instructions.Decoded.Automatic || instructions.Decoded.Audio) {
						native, freshMeasurements, targetErr = b.captureAutomaticHDR(
							ctx,
							session,
							subject,
							target,
							instructions,
							measurements,
							root,
							discBytes(),
							preparedAudio,
						)
					} else {
						native, targetErr = session.ExtractNativeFileWithRetained(ctx, target.Path, instructions.Formats, discBytes())
					}
				}
				if nativeCheckpointed && targetErr != nil {
					// Audio publication cannot revoke already checkpointed native
					// metadata. Missing track outcomes are filled independently below.
					b.service.ReportFailure(ctx, "prepared_audio", targetErr)
					targetErr = nil
				}
				if native != nil && !nativeCheckpointed {
					var evidenceErr error
					evidence, evidenceErr = native.Evidence(instructions.Formats, target.Target.Evidence)
					identity.ResolvedTrackID = native.TrackID
					scanComplete = native.ScanComplete
					native.Release()
					if evidenceErr != nil {
						targetErr = evidenceErr
					}
					if evidence != nil {
						evidence.Timeline = timeline
					}
				}
				if evidence != nil && scanComplete && targetErr == nil && !nativeCheckpointed {
					if readsSource {
						targetErr = b.resolver.ValidateHDRAnalysisSource(ctx, release)
					}
					if targetErr == nil {
						record, owned, saveErr := b.retainNativeExtraction(
							ctx,
							release,
							identity,
							instructions.Formats,
							evidence,
							attempt+"-"+strconv.Itoa(index+1),
						)
						if saveErr == nil {
							saveErr = publish(record, owned)
							nativeRecord = record
						}
						targetErr = saveErr
						if saveErr != nil {
							b.service.ReportFailure(ctx, "retaining_native_metadata", saveErr)
						}
					}
				}
			}
			if evidence != nil {
				outcome.Formats = evidence.Results
			}
			var reports []*hdranalysis.MeasurementReport
			if targetErr != nil || evidence == nil {
				if targetErr == nil {
					targetErr = extract.ErrIncomplete
				}
				outcome.Formats = nativeFailedFormats(targetErr, instructions.Formats)
			} else {
				if !nativeRendered && (preparedAudio != nil || instructions.Decoded != nil && instructions.Decoded.BoundarySync) {
					if err := prepareNativeRender(); err != nil {
						return err
					}
				}
				if preparedAudio != nil && !nativeCheckpointed && len(preparedAudio.Selected) > 0 {
					if err := b.resolver.ValidateHDRAnalysisSource(ctx, release); err != nil {
						return fmt.Errorf("validate HDR source: %w", err)
					}
					input := hdranalysis.DecodedInput{Path: target.Path, Fingerprint: subject.SourceFingerprint}
					if err := session.MeasurePreparedAudio(ctx, input, *instructions.Decoded, root, discBytes(), *preparedAudio); err != nil {
						b.service.ReportFailure(ctx, "prepared_audio", err)
					}
				}
				if nativeRendered {
					if err := evidence.FinishSharedCapture(discBytes()); err != nil {
						return fmt.Errorf("finish native capture: %w", err)
					}
				}
				var measureErr error
				reports, measureErr = b.measureHDRTarget(
					ctx,
					session,
					subject,
					target,
					instructions,
					evidence,
					nativeRecord,
					measurements,
					entries,
					retained,
					publish,
					attempt,
					index,
					&outcome,
					freshMeasurements,
					boundaryPlan,
				)
				renderHDRMeasurements(ctx, session, subject.Title, attempt, index, reports, &outcome, resource, nativeRecord, result.RenderFingerprint)
				if measureErr != nil {
					b.service.ReportFailure(ctx, "decoded", measureErr)
				}
			}

			hdranalysis.CompletePreparedAudio(preparedAudio, &outcome, ctx.Err() != nil)
			if instructions.Decoded != nil {
				for _, mode := range hdrDecodedModes {
					if instructions.Decoded.Requested(mode) &&
						!slices.ContainsFunc(outcome.Decoded, func(result api.HDRDecodedResult) bool { return result.Mode == mode }) {
						state, reason := api.HDRDecodedFailed, "measurement_unavailable"
						if ctx.Err() != nil {
							state, reason = api.HDRDecodedCanceled, "canceled"
						}
						profile := ""
						if mode == api.HDRDecodedBoundarySync {
							profile = hdranalysis.BoundaryMeasurementProfile
						}
						outcome.Decoded = append(outcome.Decoded, api.HDRDecodedResult{
							Mode:    mode,
							State:   state,
							Reason:  reason,
							Profile: profile,
						})
					}
				}
			}
			if targetErr == nil && evidence != nil {
				if !nativeRendered {
					b.renderNativePlots(ctx, session, subject.Title, evidence, &outcome, resource, instructions.PeakSource, attempt, index, reports,
						instructions.ProfileVersion, nativeRecord.SHA256, result.RenderFingerprint)
				}
				b.renderNativeReport(ctx, evidence, &outcome, resource, instructions.PeakSource, attempt, index, reports,
					instructions.ProfileVersion, nativeRecord.SHA256, result.RenderFingerprint)
				if authorityErr := b.revalidate(ctx, instructions, subject); authorityErr != nil {
					for _, artifact := range outcome.Artifacts {
						_ = os.Remove(resource.Files[artifact.ID].Path)
						delete(resource.Files, artifact.ID)
					}
					outcome.Artifacts = nil
					outcome.Formats = nativeFailedFormats(authorityErr, instructions.Formats)
					for i, decoded := range outcome.Decoded {
						state := api.HDRDecodedFailed
						if ctx.Err() != nil {
							state = api.HDRDecodedCanceled
						}
						outcome.Decoded[i] = api.HDRDecodedResult{
							Mode:    decoded.Mode,
							State:   state,
							Reason:  "source_unavailable",
							Profile: decoded.Profile,
						}
					}
				}
			}
			if evidence != nil {
				evidence.Release()
			}
			if instructions.ProfileVersion == api.HDRAnalysisProfileV3 {
				for i := range outcome.Decoded {
					if outcome.Decoded[i].Coverage == "" {
						outcome.Decoded[i].Coverage = api.HDRCoverageUnknown
					}
				}
				slices.SortFunc(outcome.Decoded, func(a, b api.HDRDecodedResult) int {
					return slices.Index(hdrDecodedModes, a.Mode) - slices.Index(hdrDecodedModes, b.Mode)
				})
			}
			outcome.Status = outcome.AnalysisStatus()
			result.Targets = append(result.Targets, outcome)
		}
		return nil
	})
	if err != nil {
		_ = resource.Release()
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("native HDR session: %w", err)
	}
	result.Status = api.NativeHDRAggregate(result.Targets)
	if prior != nil && b.uploads != nil && priorResource != nil {
		maps.Copy(clonedPaths, regeneratedHDRImagePaths(ctx, *prior, result, priorResource, resource))
	}
	if prior != nil && b.uploads != nil && len(clonedPaths) > 0 {
		cloneCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := b.uploads.CloneHDRAnalysisUploads(cloneCtx, prior.Release, subject.MediaBinding, clonedPaths); err != nil {
			_ = resource.Release()
			return api.HDRAnalysisResult{}, nil, fmt.Errorf("clone native HDR image uploads: %w", err)
		}
	}
	completed := time.Now().UTC()
	result.CompletedAt = &completed
	b.service.ReportOutcome(ctx, result.Status)
	if len(resource.Files) == 0 {
		_ = resource.Release()
		return result, nil, nil
	}
	return result, resource, nil
}

func nativeHDRNeedsSource(subject api.HDRAnalysisSubject, target api.HDRAnalysisTargetSubject,
	prior *api.HDRAnalysisResult, entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	measurements *releaseworkflow.HDRMeasurementStore,
) bool {
	if hdrNeedsSourceRetry(subject, target, prior, entries, measurements, api.HDRExtractionSchemaV2) {
		return true
	}
	if target.CapturedNativePath != "" || target.CapturedPath != "" || target.CapturedFailure != nil {
		return false
	}
	for _, record := range entries {
		if record.Release == subject.Release && record.TargetID == target.Target.ID &&
			(record.Schema != api.HDRExtractionSchemaV2 || record.SourceFingerprint == subject.SourceFingerprint && record.SelectionPolicy == target.Target.SelectionPolicy) {
			// A missing/corrupt retained resource is a recovery outcome, not
			// permission to silently read this source again.
			return false
		}
	}
	return true
}

func hdrNeedsSourceRetry(subject api.HDRAnalysisSubject, target api.HDRAnalysisTargetSubject,
	prior *api.HDRAnalysisResult, entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	store *releaseworkflow.HDRMeasurementStore, schema string,
) bool {
	if prior == nil || prior.Release != subject.Release || !prior.TargetNeedsSourceRetry(target.Target.ID) {
		return false
	}
	if store != nil && store.NativeRetryAfterSequence != nil {
		for _, record := range entries {
			if record.Sequence > *store.NativeRetryAfterSequence && record.Release == subject.Release && record.TargetID == target.Target.ID &&
				record.SourceFingerprint == subject.SourceFingerprint && record.SelectionPolicy == target.Target.SelectionPolicy && record.Schema == schema {
				return false
			}
		}
	}
	return true
}

func (b workflowHDRAnalysisBuilder) readCapturedNative(ctx context.Context, target api.HDRAnalysisTargetSubject,
	identity hdranalysis.ExtractionIdentity, formats api.HDRAnalysisFormats, otherRetainedBytes int64,
) (*hdranalysis.NativeEvidence, error) {
	fail := func(err error) (*hdranalysis.NativeEvidence, error) {
		return nil, api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable,
			Message: "The captured native HDR metadata is unavailable; explicitly retry source analysis."}, err)
	}
	file, err := b.openCapturedHDR(target.CapturedNativePath, target.CapturedNativeSize, target.CapturedNativeSHA256)
	if err != nil {
		return fail(err)
	}
	identity.ResolvedTrackID = target.CapturedTrackID
	manifest, decodeErr := hdranalysis.ReadNativeManifest(ctx, file, identity)
	if err := errors.Join(decodeErr, file.Close()); err != nil {
		return fail(err)
	}
	directory, err := os.OpenRoot(filepath.Dir(target.CapturedNativePath))
	if err != nil {
		return fail(err)
	}
	defer directory.Close()
	evidence, err := hdranalysis.RestoreNativeEvidenceWithRetained(ctx, manifest, func(id string) (io.ReadCloser, error) {
		file, err := directory.Open(id + ".json")
		if err != nil {
			return nil, fmt.Errorf("open captured native section: %w", err)
		}
		return file, nil
	}, otherRetainedBytes)
	if err != nil {
		return fail(err)
	}
	if err := evidence.SelectFormats(formats); err != nil {
		evidence.Release()
		return fail(err)
	}
	return evidence, nil
}

func copyNativeHDRTarget(ctx context.Context, prior api.HDRAnalysisResult, target api.HDRAnalysisTargetResult,
	source releaseworkflow.RetainedHDRAnalysisResource, destination hdrManagedResource, attempt string, index int,
) (api.HDRAnalysisTargetResult, map[string]string, error) {
	if source == nil && len(target.Artifacts) > 0 {
		return api.HDRAnalysisTargetResult{}, nil, fmt.Errorf("open retained HDR artifacts: %w", releaseworkflow.ErrPrivateResourceUnavailable)
	}
	result := target
	result.Formats = slices.Clone(target.Formats)
	result.Decoded = slices.Clone(target.Decoded)
	result.Artifacts = nil
	ids := make(map[api.PublicResourceID]api.PublicResourceID)
	paths := make(map[string]string)
	files := make(map[api.PublicResourceID]hdrManagedFile)
	complete := false
	defer func() {
		if !complete {
			for _, file := range files {
				_ = os.Remove(file.Path)
			}
		}
	}()
	for i, artifact := range target.Artifacts {
		from, err := source.LocalArtifactPath(prior, artifact.ID)
		if err != nil {
			return api.HDRAnalysisTargetResult{}, nil, fmt.Errorf("resolve retained native artifact: %w", err)
		}
		content, err := source.OpenArtifact(ctx, prior, artifact.ID)
		if err != nil {
			return api.HDRAnalysisTargetResult{}, nil, fmt.Errorf("open retained native artifact: %w", err)
		}
		path := filepath.Join(destination.Directory, strconv.Itoa(index+1)+"-"+artifact.Filename())
		copyErr := hdranalysis.PublishFile(ctx, path, func(out io.Writer) error {
			_, err := io.Copy(out, content.Body)
			if err != nil {
				return fmt.Errorf("copy native artifact: %w", err)
			}
			return nil
		})
		if err := errors.Join(copyErr, content.Body.Close()); err != nil {
			_ = os.Remove(path)
			return api.HDRAnalysisTargetResult{}, nil, fmt.Errorf("retain native artifact copy: %w", err)
		}
		file, err := hdrFileIntegrity(path)
		if err != nil {
			_ = os.Remove(path)
			return api.HDRAnalysisTargetResult{}, nil, err
		}
		id := api.PublicResourceID("hdr-" + attempt + "-" + strconv.Itoa(index+1) + "-" + strconv.Itoa(i+1))
		ids[artifact.ID] = id
		artifact.ID = id
		files[artifact.ID] = file
		result.Artifacts = append(result.Artifacts, artifact)
		if artifact.MIME == "image/png" {
			paths[from] = path
		}
	}
	for i := range result.Decoded {
		result.Decoded[i].Artifacts = slices.Clone(target.Decoded[i].Artifacts)
		for j, id := range result.Decoded[i].Artifacts {
			result.Decoded[i].Artifacts[j] = ids[id]
		}
		result.Decoded[i].AudioTracks = slices.Clone(target.Decoded[i].AudioTracks)
		for j := range result.Decoded[i].AudioTracks {
			track := &result.Decoded[i].AudioTracks[j]
			track.Artifacts = slices.Clone(track.Artifacts)
			for k, id := range track.Artifacts {
				track.Artifacts[k] = ids[id]
			}
		}
	}
	result.Status = result.AnalysisStatus()
	maps.Copy(destination.Files, files)
	complete = true
	return result, paths, nil
}

func nativeFailedFormats(err error, formats api.HDRAnalysisFormats) []api.HDRFormatResult {
	failure, _ := api.AsHDRAnalysisFailure(hdranalysis.Classify(err))
	state := api.HDRExtractionFailed
	if errors.Is(err, context.Canceled) {
		state = api.HDRExtractionCanceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		state = api.HDRExtractionInterrupted
	}
	results := []api.HDRFormatResult{{
		Format:    api.HDRNativeHDR10Plus,
		Requested: formats == api.HDRAnalysisFormatsBoth || formats == api.HDRAnalysisFormatsHDR10Plus,
		State:     state,
		Coverage:  api.HDRCoverageUnknown,
		Failure:   &failure,
		Checks:    []api.HDRCheckResult{},
	}, {
		Format:    api.HDRNativeDolbyVision,
		Requested: formats == api.HDRAnalysisFormatsBoth || formats == api.HDRAnalysisFormatsDolbyVision,
		State:     state,
		Coverage:  api.HDRCoverageUnknown,
		Failure:   &failure,
		Checks:    []api.HDRCheckResult{},
	}}
	if formats == api.HDRAnalysisFormatsHDR10Plus {
		results[1].State = api.HDRExtractionNotAnalyzed
		results[1].Failure = nil
	}
	if formats == api.HDRAnalysisFormatsDolbyVision {
		results[0].State = api.HDRExtractionNotAnalyzed
		results[0].Failure = nil
	}
	return results
}

func (b workflowHDRAnalysisBuilder) renderNativePlots(
	ctx context.Context,
	session *hdranalysis.Session,
	title string,
	evidence *hdranalysis.NativeEvidence,
	outcome *api.HDRAnalysisTargetResult,
	resource hdrManagedResource,
	peak api.HDRPeakSource,
	attempt string,
	index int,
	measurements []*hdranalysis.MeasurementReport,
	profile, measurementFingerprint, renderFingerprint string,
) {
	for i := range outcome.Formats {
		format := &outcome.Formats[i]
		if format.State != api.HDRExtractionComplete || i == 1 && format.L1Samples == 0 {
			continue
		}
		kind := api.HDRArtifactHDR10PlusPlot
		if i == 1 {
			kind = api.HDRArtifactDolbyVisionL1Plot
		}
		artifact := api.HDRTypedArtifact{
			ID:      api.PublicResourceID("hdr-" + attempt + "-" + strconv.Itoa(index+1) + "-" + strconv.Itoa(i+1)),
			Kind:    kind,
			Format:  format.Format,
			Width:   api.HDRAnalysisWidth,
			Height:  api.HDRAnalysisHeight,
			MIME:    "image/png",
			Profile: profile,
		}
		if profile == api.HDRAnalysisProfileV3 {
			artifact.MeasurementFingerprint, artifact.RenderFingerprint = measurementFingerprint, renderFingerprint
		}
		pathValue := filepath.Join(resource.Directory, strconv.Itoa(index+1)+"-"+artifact.Filename())
		var measurementBytes int64
		if len(measurements) > 0 {
			measurementBytes = 128 << 20
		}
		err := session.RenderNative(ctx, evidence, format.Format, peak, title, pathValue, measurementBytes)
		if err == nil {
			var file hdrManagedFile
			file, err = hdrFileIntegrity(pathValue)
			if err == nil {
				resource.Files[artifact.ID] = file
				outcome.Artifacts = append(outcome.Artifacts, artifact)
			}
		}
		if err != nil {
			_ = os.Remove(pathValue)
			check := api.HDRCheckResult{
				Code:    "plot_unavailable",
				State:   api.HDRCheckUnsupported,
				Message: "The plot could not be rendered; retained metadata remains available for retry.",
			}
			if errors.Is(err, context.Canceled) {
				check.Code, check.State = "render_canceled", api.HDRCheckNotRun
			}
			if errors.Is(err, context.DeadlineExceeded) {
				check.Code, check.State = "render_interrupted", api.HDRCheckNotRun
			}
			format.Checks = append(format.Checks, check)
		}
	}
}

func (b workflowHDRAnalysisBuilder) renderNativeReport(ctx context.Context, evidence *hdranalysis.NativeEvidence,
	outcome *api.HDRAnalysisTargetResult, resource hdrManagedResource, peak api.HDRPeakSource, attempt string, index int,
	measurements []*hdranalysis.MeasurementReport, profile, measurementFingerprint, renderFingerprint string,
) {
	if ctx.Err() != nil {
		return
	}
	report := api.HDRTypedArtifact{
		ID:      api.PublicResourceID("hdr-report-" + attempt + "-" + strconv.Itoa(index+1)),
		Kind:    api.HDRArtifactStructuralReport,
		MIME:    "application/json",
		Profile: profile,
	}
	if profile == api.HDRAnalysisProfileV3 {
		report.MeasurementFingerprint, report.RenderFingerprint = measurementFingerprint, renderFingerprint
	}
	pathValue := filepath.Join(resource.Directory, strconv.Itoa(index+1)+"-"+report.Filename())
	err := hdranalysis.PublishFile(ctx, pathValue, func(out io.Writer) error {
		return evidence.WriteReport(ctx, out, peak, "", *outcome, measurements)
	})
	if err == nil {
		var file hdrManagedFile
		file, err = hdrFileIntegrity(pathValue)
		if err == nil {
			resource.Files[report.ID] = file
			found := false
			for i := range outcome.Artifacts {
				if outcome.Artifacts[i].ID == report.ID {
					outcome.Artifacts[i], found = report, true
				}
			}
			if !found {
				outcome.Artifacts = append(outcome.Artifacts, report)
			}
		}
	}
	if err != nil {
		b.service.ReportFailure(ctx, "structural_report", err)
		_ = os.Remove(pathValue)
		delete(resource.Files, report.ID)
		outcome.Artifacts = slices.DeleteFunc(outcome.Artifacts, func(artifact api.HDRTypedArtifact) bool { return artifact.ID == report.ID })
		for i := range outcome.Formats {
			if outcome.Formats[i].State == api.HDRExtractionComplete {
				outcome.Formats[i].Checks = append(outcome.Formats[i].Checks, api.HDRCheckResult{
					Code:    "report_unavailable",
					State:   api.HDRCheckUnsupported,
					Message: "The structural report could not be saved; completed images and retained metadata remain available.",
				})
			}
		}
	}
}

func (b workflowHDRAnalysisBuilder) retainNativeExtraction(
	ctx context.Context,
	release api.ReleaseRef,
	identity hdranalysis.ExtractionIdentity,
	formats api.HDRAnalysisFormats,
	evidence *hdranalysis.NativeEvidence,
	id string,
) (releaseworkflow.HDRExtractionRecord, hdrManagedResource, error) {
	root, err := hdrAttemptRoot(b.root, release, "hdr-extraction", id)
	if err != nil {
		return releaseworkflow.HDRExtractionRecord{}, hdrManagedResource{}, err
	}
	if err := createHDRDirectory(root); err != nil {
		return releaseworkflow.HDRExtractionRecord{}, hdrManagedResource{}, err
	}
	resource := hdrManagedResource{
		Root:      b.root,
		Directory: root,
		Kind:      "extraction",
		Files:     make(map[api.PublicResourceID]hdrManagedFile),
	}
	manifest, metadata, err := hdranalysis.PublishNativeEvidence(ctx, root, identity, formats, evidence)
	if err != nil {
		_ = resource.Release()
		return releaseworkflow.HDRExtractionRecord{}, hdrManagedResource{}, fmt.Errorf("retain native HDR evidence: %w", err)
	}
	for _, section := range []*hdranalysis.NativeSection{manifest.HDR10Plus, manifest.DolbyVision, &metadata} {
		if section != nil {
			resource.Files[api.PublicResourceID(section.ID)] = hdrManagedFile{
				Path:   filepath.Join(root, section.ID+".json"),
				Size:   section.Size,
				SHA256: section.SHA256,
			}
		}
	}
	file := resource.Files["metadata"]
	return releaseworkflow.HDRExtractionRecord{
		ID:                api.HDRExtractionID(id),
		Release:           release,
		SourceFingerprint: identity.SourceFingerprint,
		TargetID:          identity.TargetID,
		SelectionPolicy:   identity.SelectionPolicy,
		ResolvedTrackID:   identity.ResolvedTrackID,
		Schema:            api.HDRExtractionSchemaV2,
		Dependency:        hdranalysis.NativeDependencyFingerprint,
		Size:              file.Size,
		SHA256:            file.SHA256,
	}, resource, nil
}

func (r hdrManagedResource) OpenSection(ctx context.Context, record releaseworkflow.HDRExtractionRecord, id string) (io.ReadCloser, error) {
	if id != "hdr10plus" && id != "dolby-vision" {
		return nil, releaseworkflow.ErrPrivateResourceIntegrity
	}
	manifest, err := r.OpenExtraction(ctx, record)
	if err != nil {
		return nil, err
	}
	if err := manifest.Close(); err != nil {
		return nil, fmt.Errorf("close native manifest authority: %w", err)
	}
	return r.open(api.PublicResourceID(id), hdranalysis.MaxSidecarBytes)
}

func restoreNativeHDREvidence(
	ctx context.Context,
	release api.ReleaseRef,
	identity hdranalysis.ExtractionIdentity,
	formats api.HDRAnalysisFormats,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	retained map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource,
) (*hdranalysis.NativeEvidence, bool, error) {
	return restoreNativeHDREvidenceWithRetained(ctx, release, identity, formats, entries, retained, 0)
}

func restoreNativeHDREvidenceWithRetained(ctx context.Context, release api.ReleaseRef, identity hdranalysis.ExtractionIdentity,
	formats api.HDRAnalysisFormats, entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	retained map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource, otherBytes int64,
) (*hdranalysis.NativeEvidence, bool, error) {
	for _, id := range hdrExtractionIDs(entries) {
		record := entries[id]
		if record.Release != release || record.SourceFingerprint != identity.SourceFingerprint || record.TargetID != identity.TargetID ||
			record.SelectionPolicy != identity.SelectionPolicy ||
			record.Schema != api.HDRExtractionSchemaV2 {
			continue
		}
		resource := retained[id]
		sections, ok := resource.(releaseworkflow.RetainedHDRSectionResource)
		if !ok {
			return nil, true, releaseworkflow.ErrPrivateResourceUnavailable
		}
		reader, err := resource.OpenExtraction(ctx, record)
		if err != nil {
			return nil, true, fmt.Errorf("restore native HDR section: %w", err)
		}
		manifest, decodeErr := hdranalysis.ReadNativeManifest(ctx, reader, identity)
		if err := errors.Join(decodeErr, reader.Close()); err != nil {
			return nil, true, fmt.Errorf("restore native HDR section: %w", err)
		}
		if manifest.Formats != formats {
			return nil, true, fmt.Errorf("restore native HDR selection: %w", api.NewHDRAnalysisError(
				api.HDRAnalysisFailure{
					Code:    api.HDRAnalysisFailureResourceUnavailable,
					Message: "Retained metadata uses a different format selection; explicitly retry source analysis.",
				},
				nil,
			))
		}
		evidence, err := hdranalysis.RestoreNativeEvidenceWithRetained(ctx, manifest, func(id string) (io.ReadCloser, error) {
			return sections.OpenSection(ctx, record, id)
		}, otherBytes)
		if err != nil {
			return nil, true, releaseworkflow.ErrPrivateResourceIntegrity
		}
		return evidence, true, nil
	}
	return nil, false, nil
}
