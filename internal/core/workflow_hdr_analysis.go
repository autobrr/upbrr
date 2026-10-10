// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"image/png"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/Audionut/go-hdr10-plus/extract"
	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdrAnalysisSubjectResolver interface {
	ResolveHDRAnalysisSubject(context.Context, api.HDRAnalysisInstructions) (api.HDRAnalysisSubject, error)
}

type workflowHDRAnalysisBuilder struct {
	uploads  api.MediaAssetRepository
	resolver hdrAnalysisSubjectResolver
	service  *hdranalysis.Service
	root     string
}

func (b workflowHDRAnalysisBuilder) Build(
	ctx context.Context,
	release api.ReleaseRef,
	instructions api.HDRAnalysisInstructions,
	attemptID string,
	now time.Time,
	prior *api.HDRAnalysisResult,
	priorResource releaseworkflow.RetainedHDRAnalysisResource,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	retained map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource,
	publish func(releaseworkflow.HDRExtractionRecord, releaseworkflow.RetainedHDRExtractionResource) error,
) (api.HDRAnalysisResult, releaseworkflow.RetainedHDRAnalysisResource, error) {
	instructions.Release = release
	normalized, err := instructions.Normalize()
	if err != nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("normalize HDR analysis: %w", err)
	}
	subject, err := b.resolver.ResolveHDRAnalysisSubject(ctx, normalized)
	if err != nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("resolve HDR analysis: %w", err)
	}
	root, err := hdrAttemptRoot(b.root, subject.Release, "hdr-analysis", attemptID)
	if err != nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	if err := createHDRDirectory(root); err != nil {
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	renderFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Title   string
		Peak    api.HDRPeakSource
		Profile string
	}{subject.Title, normalized.PeakSource, normalized.ProfileVersion})
	if err != nil {
		_ = os.RemoveAll(root)
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	result := api.HDRAnalysisResult{
		RenderFingerprint:   string(renderFingerprint),
		Release:             release,
		ManifestFingerprint: subject.ManifestFingerprint,
		AttemptID:           attemptID,
		TargetIDs:           normalized.TargetIDs,
		PeakSource:          normalized.PeakSource,
		ProfileVersion:      normalized.ProfileVersion,
		CreatedAt:           now,
		CompletedAt:         &now,
	}
	resource := hdrManagedResource{
		Root:      b.root,
		Directory: root,
		Kind:      "analysis",
		Files:     make(map[api.PublicResourceID]hdrManagedFile),
	}
	successes := 0
	clonedPaths := make(map[string]string)
	err = b.service.WithSession(ctx, func(session *hdranalysis.Session) error {
		discs := make(map[string]*bridge.Outcome)
		for index, target := range subject.Targets {
			parentCtx := ctx
			ctx := api.WithWorkflowProgressReporter(parentCtx, func(update api.WorkflowProgressUpdate) {
				completed := 0
				if update.Total > 0 {
					completed = min(99, update.Completed*100/update.Total)
				}
				update.Completed = index*100 + completed
				update.Total = len(subject.Targets) * 100
				update.Message = target.Target.Label + ": " + update.Message
				api.EmitWorkflowProgress(parentCtx, update)
			})
			outcome := api.HDRAnalysisTargetResult{
				TargetID: target.Target.ID,
				Label:    target.Target.Label,
				Status:   api.StageStatusFailed,
			}
			var previous *api.HDRAnalysisTargetResult
			if prior != nil && prior.Release.SourcePath == release.SourcePath && prior.ManifestFingerprint == result.ManifestFingerprint &&
				prior.RenderFingerprint == result.RenderFingerprint && prior.ProfileVersion == normalized.ProfileVersion && priorResource != nil {
				for index := range prior.Targets {
					candidate := &prior.Targets[index]
					if candidate.TargetID == target.Target.ID && candidate.Artifact != nil && candidate.Status == api.StageStatusCompleted {
						if _, err := priorResource.LocalArtifactPath(*prior, candidate.Artifact.ID); err == nil {
							previous = candidate
						}
						break
					}
				}
			}
			identity := hdranalysis.ExtractionIdentity{
				SourceFingerprint: subject.SourceFingerprint,
				TargetID:          target.Target.ID,
				SelectionPolicy:   target.Target.SelectionPolicy,
			}
			sourceRetry := prior != nil && prior.Release == release && prior.TargetNeedsSourceRetry(target.Target.ID)
			var sidecar hdranalysis.Sidecar
			var found bool
			var targetErr error
			stage := "restoring_metadata"
			if previous == nil && !sourceRetry {
				sidecar, found, targetErr = restoreHDRSidecar(ctx, release, target, identity, entries, retained)
			}
			if previous == nil && targetErr == nil && !found {
				stage = "reading_metadata"
				captureRetry := prior != nil && prior.Release == release && slices.ContainsFunc(prior.Targets, func(previous api.HDRAnalysisTargetResult) bool {
					return previous.TargetID == target.Target.ID && previous.Status == api.StageStatusFailed
				})
				switch {
				case target.CapturedFailure != nil && !captureRetry && !sourceRetry:
					targetErr = api.NewHDRAnalysisError(*target.CapturedFailure, nil)
				case target.CapturedPath != "" && !sourceRetry:
					sidecar, targetErr = b.readCaptured(ctx, target, identity)
				default:
					sidecar, targetErr = b.extractTarget(ctx, session, subject, target, root, discs)
				}
				if targetErr == nil || errors.Is(targetErr, extract.ErrNoMetadata) {
					sidecar.Identity = identity
					sidecar.Absent = errors.Is(targetErr, extract.ErrNoMetadata)
					if sidecar.Extraction != nil {
						sidecar.Identity.ResolvedTrackID = sidecar.Extraction.Frames[0].Stream.TrackID
					}
					if err := b.revalidate(ctx, normalized, subject); err != nil {
						targetErr = err
					} else {
						stage = "saving_metadata"
						record, extractionResource, saveErr := b.retainExtraction(ctx, subject.Release, sidecar, attemptID+"-"+strconv.Itoa(index+1))
						if saveErr == nil {
							saveErr = publish(record, extractionResource)
						}
						if saveErr != nil {
							targetErr = saveErr
						}
					}
				}
			}
			if previous == nil && targetErr == nil && sidecar.Absent {
				targetErr = extract.ErrNoMetadata
			}
			if targetErr == nil {
				stage = "rendering"
				id := api.PublicResourceID("hdr-plot-" + attemptID + "-" + strconv.Itoa(index+1))
				pathValue := filepath.Join(root, "hdr10plus-"+strconv.Itoa(index+1)+".png")
				reused := false
				copiedFrom := ""
				if previous != nil {
					sourcePath, pathErr := priorResource.LocalArtifactPath(*prior, previous.Artifact.ID)
					if pathErr != nil {
						targetErr = pathErr
					} else {
						content, openErr := priorResource.OpenArtifact(ctx, *prior, previous.Artifact.ID)
						if openErr != nil {
							targetErr = openErr
						} else {
							copyErr := hdranalysis.PublishFile(ctx, pathValue, func(out io.Writer) error {
								_, err := io.Copy(out, content.Body)
								if err != nil {
									return fmt.Errorf("copy retained HDR plot: %w", err)
								}
								return nil
							})
							closeErr := content.Body.Close()
							if copyErr != nil || closeErr != nil {
								targetErr = errors.Join(copyErr, closeErr)
							} else {
								copiedFrom = sourcePath
								reused = true
							}
						}
					}
				}
				if !reused && targetErr == nil {
					targetErr = session.Render(ctx, sidecar.Extraction, normalized.PeakSource, subject.Title, pathValue)
				}
				if targetErr == nil {
					targetErr = b.revalidate(ctx, normalized, subject)
				}
				if targetErr == nil {
					file, hashErr := hdrFileIntegrity(pathValue)
					if hashErr != nil {
						targetErr = hashErr
					} else {
						resource.Files[id] = file
						if copiedFrom != "" {
							clonedPaths[copiedFrom] = pathValue
						}
						outcome.Status = api.StageStatusCompleted
						if previous != nil {
							outcome.Frames, outcome.Scenes, outcome.Profile = previous.Frames, previous.Scenes, previous.Profile
						} else {
							outcome.Frames, outcome.Scenes, outcome.Profile = len(
								sidecar.Extraction.Frames,
							), len(
								sidecar.Extraction.SceneStarts,
							), sidecar.Extraction.Profile
						}
						outcome.Artifact = &api.HDRAnalysisArtifact{
							ID:     id,
							Width:  api.HDRAnalysisWidth,
							Height: api.HDRAnalysisHeight,
						}
						successes++
					}
				}
				if targetErr != nil {
					_ = os.Remove(pathValue)
				}
			}
			if targetErr != nil {
				b.service.ReportFailure(ctx, stage, targetErr)
				failure, _ := api.AsHDRAnalysisFailure(hdranalysis.Classify(targetErr))
				outcome.Failure = &failure
			}
			result.Targets = append(result.Targets, outcome)
			api.EmitWorkflowProgress(parentCtx, api.WorkflowProgressUpdate{
				Phase:     "hdr_analysis_target_complete",
				Completed: min((index+1)*100, len(subject.Targets)*100-1),
				Total:     len(subject.Targets) * 100,
				Message:   target.Target.Label + ": " + string(outcome.Status),
			})
			if target.DiscRoot != "" && (index+1 == len(subject.Targets) || subject.Targets[index+1].DiscRoot != target.DiscRoot) {
				delete(discs, target.DiscRoot)
			}
		}
		return nil
	})
	if err != nil {
		_ = resource.Release()
		return api.HDRAnalysisResult{}, nil, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.Status = api.StageStatusInterrupted
	case ctx.Err() != nil:
		result.Status = api.StageStatusCanceled
	case successes == len(result.Targets):
		result.Status = api.StageStatusCompleted
	case successes > 0:
		result.Status = api.StageStatusPartial
	default:
		result.Status = api.StageStatusFailed
	}
	completedAt := time.Now().UTC()
	result.CompletedAt = &completedAt
	if len(resource.Files) == 0 {
		_ = resource.Release()
		b.service.ReportOutcome(ctx, result.Status)
		return result, nil, nil
	}
	if prior != nil && b.uploads != nil && len(clonedPaths) > 0 {
		cloneCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := b.uploads.CloneHDRAnalysisUploads(cloneCtx, prior.Release, subject.MediaBinding, clonedPaths); err != nil {
			_ = resource.Release()
			return api.HDRAnalysisResult{}, nil, fmt.Errorf("workflow_hdr_analysis: %w", err)
		}
	}
	b.service.ReportOutcome(ctx, result.Status)
	return result, resource, nil
}

func (b workflowHDRAnalysisBuilder) readCaptured(
	ctx context.Context,
	target api.HDRAnalysisTargetSubject,
	identity hdranalysis.ExtractionIdentity,
) (hdranalysis.Sidecar, error) {
	fail := func(err error) (hdranalysis.Sidecar, error) {
		return hdranalysis.Sidecar{}, fmt.Errorf("workflow_hdr_analysis: %w", api.NewHDRAnalysisError(
			api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable, Message: "the provisional HDR metadata is unavailable"},
			err,
		))
	}
	if target.CapturedSize <= 0 || target.CapturedSize > hdranalysis.MaxSidecarBytes || !pathutil.IsWithinRoot(b.root, target.CapturedPath) {
		return fail(releaseworkflow.ErrPrivateResourceIntegrity)
	}
	resolved, err := filepath.EvalSymlinks(target.CapturedPath)
	resolvedRoot, rootErr := filepath.EvalSymlinks(b.root)
	if err != nil || rootErr != nil || !pathutil.IsWithinRoot(resolvedRoot, resolved) {
		return fail(releaseworkflow.ErrPrivateResourceIntegrity)
	}
	file, err := os.Open(target.CapturedPath)
	if err != nil {
		return fail(err)
	}
	hash := sha256.New()
	size, hashErr := io.Copy(hash, io.LimitReader(file, target.CapturedSize+1))
	_, seekErr := file.Seek(0, io.SeekStart)
	if hashErr != nil || seekErr != nil || size != target.CapturedSize || hex.EncodeToString(hash.Sum(nil)) != target.CapturedSHA256 {
		_ = file.Close()
		return fail(releaseworkflow.ErrPrivateResourceIntegrity)
	}
	identity.ResolvedTrackID = target.CapturedTrackID
	sidecar, decodeErr := hdranalysis.ReadSidecar(ctx, file, identity)
	closeErr := file.Close()
	if err := errors.Join(decodeErr, closeErr); err != nil {
		return fail(err)
	}
	if sidecar.Absent != target.CapturedAbsent {
		return fail(releaseworkflow.ErrPrivateResourceIntegrity)
	}
	return sidecar, nil
}

func restoreHDRSidecar(ctx context.Context, release api.ReleaseRef, target api.HDRAnalysisTargetSubject, identity hdranalysis.ExtractionIdentity,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord, retained map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource,
) (hdranalysis.Sidecar, bool, error) {
	var unavailable error
	for _, id := range hdrExtractionIDs(entries) {
		if err := ctx.Err(); err != nil {
			return hdranalysis.Sidecar{}, false, fmt.Errorf("workflow_hdr_analysis: %w", err)
		}
		record := entries[id]
		if record.Release != release || record.SourceFingerprint != identity.SourceFingerprint || record.TargetID != target.Target.ID ||
			record.SelectionPolicy != identity.SelectionPolicy ||
			record.Schema != api.HDRExtractionSchemaVersion ||
			record.Dependency != hdranalysis.DependencyFingerprint {
			continue
		}
		resource := retained[id]
		if resource == nil {
			unavailable = fmt.Errorf("workflow_hdr_analysis: %w", api.NewHDRAnalysisError(
				api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable, Message: "the retained HDR metadata is unavailable"},
				nil,
			))
			continue
		}
		reader, err := resource.OpenExtraction(ctx, record)
		if err != nil {
			if canceled := ctx.Err(); canceled != nil {
				return hdranalysis.Sidecar{}, false, fmt.Errorf("workflow_hdr_analysis: %w", canceled)
			}
			unavailable = fmt.Errorf("workflow_hdr_analysis: %w", api.NewHDRAnalysisError(
				api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable, Message: "the retained HDR metadata is missing or damaged"},
				err,
			))
			continue
		}
		identity.ResolvedTrackID = record.ResolvedTrackID
		sidecar, decodeErr := hdranalysis.ReadSidecar(ctx, reader, identity)
		closeErr := reader.Close()
		if err := ctx.Err(); err != nil {
			return hdranalysis.Sidecar{}, false, fmt.Errorf("workflow_hdr_analysis: %w", err)
		}
		if err := errors.Join(decodeErr, closeErr); err != nil {
			unavailable = fmt.Errorf("workflow_hdr_analysis: %w", api.NewHDRAnalysisError(
				api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureResourceUnavailable, Message: "the retained HDR metadata is invalid"},
				err,
			))
			continue
		}
		return sidecar, true, nil
	}
	return hdranalysis.Sidecar{}, false, unavailable
}

// hdrExtractionIDs prioritizes complete metadata over absence; opaque IDs only break ties deterministically.
func hdrExtractionIDs(entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord) []api.HDRExtractionID {
	return slices.SortedFunc(maps.Keys(entries), func(a, b api.HDRExtractionID) int {
		if entries[a].Absent != entries[b].Absent {
			if entries[a].Absent {
				return 1
			}
			return -1
		}
		return cmp.Compare(a, b)
	})
}

func (b workflowHDRAnalysisBuilder) extractTarget(
	ctx context.Context,
	session *hdranalysis.Session,
	subject api.HDRAnalysisSubject,
	target api.HDRAnalysisTargetSubject,
	reportRoot string,
	discs map[string]*bridge.Outcome,
) (hdranalysis.Sidecar, error) {
	if target.DiscRoot == "" {
		extraction, err := session.ExtractFile(ctx, target.Path)
		if err != nil {
			return hdranalysis.Sidecar{}, fmt.Errorf("extract HDR file target: %w", err)
		}
		return hdranalysis.Sidecar{Extraction: extraction}, nil
	}
	outcome, scanned := discs[target.DiscRoot]
	if !scanned {
		settings := bd.DefaultSettings(reportRoot)
		settings.GenerateStreamDiagnostics = false
		settings.ExtendedStreamDiagnostics = true
		selected := 0
		for _, candidate := range subject.Targets {
			if candidate.DiscRoot == target.DiscRoot {
				selected++
			}
		}
		if selected == 1 {
			settings.PlaylistOnly = target.Target.Playlist
		}
		var err error
		outcome, err = session.ScanDisc(ctx, bd.Options{
			Path:            target.DiscRoot,
			Settings:        settings,
			IncludeTimeline: true,
		})
		if err != nil {
			return hdranalysis.Sidecar{}, fmt.Errorf("workflow_hdr_analysis: %w", err)
		}
		discs[target.DiscRoot] = outcome
	}
	sidecar, err := hdranalysis.DiscExtraction(outcome, target.Target.Playlist)
	if err != nil {
		return sidecar, fmt.Errorf("assemble HDR disc target: %w", err)
	}
	return sidecar, nil
}

func (b workflowHDRAnalysisBuilder) revalidate(ctx context.Context, instructions api.HDRAnalysisInstructions, subject api.HDRAnalysisSubject) error {
	current, err := b.resolver.ResolveHDRAnalysisSubject(ctx, instructions)
	if err != nil {
		return fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	if current.SourceFingerprint != subject.SourceFingerprint || current.ManifestFingerprint != subject.ManifestFingerprint ||
		current.Release != subject.Release {
		return fmt.Errorf(
			"workflow_hdr_analysis: %w",
			api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureStaleSource, Message: "the HDR source changed during analysis"}, nil),
		)
	}
	return nil
}

func (b workflowHDRAnalysisBuilder) retainExtraction(
	ctx context.Context,
	release api.ReleaseRef,
	sidecar hdranalysis.Sidecar,
	id string,
) (releaseworkflow.HDRExtractionRecord, hdrManagedResource, error) {
	root, err := hdrAttemptRoot(b.root, release, "hdr-extraction", id)
	if err != nil {
		return releaseworkflow.HDRExtractionRecord{}, hdrManagedResource{}, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	if err := createHDRDirectory(root); err != nil {
		return releaseworkflow.HDRExtractionRecord{}, hdrManagedResource{}, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	resource := hdrManagedResource{
		Root:      b.root,
		Directory: root,
		Kind:      "extraction",
		Files:     make(map[api.PublicResourceID]hdrManagedFile),
	}
	pathValue := filepath.Join(root, "metadata.json")
	if err := hdranalysis.PublishFile(ctx, pathValue, func(out io.Writer) error { return hdranalysis.WriteSidecar(ctx, out, sidecar) }); err != nil {
		_ = resource.Release()
		return releaseworkflow.HDRExtractionRecord{}, hdrManagedResource{}, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	file, err := hdrFileIntegrity(pathValue)
	if err != nil {
		_ = resource.Release()
		return releaseworkflow.HDRExtractionRecord{}, hdrManagedResource{}, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	resource.Files["metadata"] = file
	record := releaseworkflow.HDRExtractionRecord{
		ID:                api.HDRExtractionID(id),
		Release:           release,
		SourceFingerprint: sidecar.Identity.SourceFingerprint,
		TargetID:          sidecar.Identity.TargetID,
		SelectionPolicy:   sidecar.Identity.SelectionPolicy,
		ResolvedTrackID:   sidecar.Identity.ResolvedTrackID,
		Schema:            api.HDRExtractionSchemaVersion,
		Dependency:        hdranalysis.DependencyFingerprint,
		Size:              file.Size,
		SHA256:            file.SHA256,
		Absent:            sidecar.Absent,
	}
	return record, resource, nil
}

func hdrAttemptRoot(root string, release api.ReleaseRef, kind, id string) (string, error) {
	if err := validateAudioAnalysisAttemptID(id); err != nil {
		return "", fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	releaseRoot, _, err := paths.ReleaseTempDirFor(root, release.SourcePath, api.ReleaseInfo{})
	if err != nil {
		return "", fmt.Errorf("resolve HDR managed root: %w", err)
	}
	value := filepath.Join(releaseRoot, kind, strconv.FormatUint(uint64(release.Generation), 10), id)
	if !pathutil.IsWithinRoot(root, value) {
		return "", errors.New("HDR attempt path escaped managed root")
	}
	return value, nil
}

func createHDRDirectory(root string) error {
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return fmt.Errorf("create HDR parent directory: %w", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return fmt.Errorf("create exclusive HDR attempt: %w", err)
	}
	return nil
}

type hdrManagedFile struct {
	Path   string
	Size   int64
	SHA256 string
}
type hdrManagedResource struct {
	Root      string
	Directory string
	Kind      string
	Files     map[api.PublicResourceID]hdrManagedFile
}

func hdrFileIntegrity(pathValue string) (hdrManagedFile, error) {
	file, err := os.Open(pathValue)
	if err != nil {
		return hdrManagedFile{}, fmt.Errorf("open HDR artifact: %w", err)
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return hdrManagedFile{}, fmt.Errorf("hash HDR artifact: %w", err)
	}
	if size <= 0 {
		return hdrManagedFile{}, errors.New("HDR artifact is empty")
	}
	return hdrManagedFile{
		Path:   pathValue,
		Size:   size,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func (r hdrManagedResource) validDirectory() bool {
	return r.directoryAuthority(false)
}

func (r hdrManagedResource) directoryAuthority(allowMissing bool) bool {
	if r.Kind != "analysis" && r.Kind != "extraction" || !filepath.IsAbs(r.Root) || !filepath.IsAbs(r.Directory) || pathutil.SamePath(r.Root, r.Directory) ||
		!pathutil.IsWithinRoot(r.Root, r.Directory) {
		return false
	}
	if filepath.Base(filepath.Dir(filepath.Dir(r.Directory))) != "hdr-"+r.Kind {
		return false
	}
	generation, err := strconv.ParseUint(filepath.Base(filepath.Dir(r.Directory)), 10, 64)
	if err != nil || generation == 0 || validateAudioAnalysisAttemptID(filepath.Base(r.Directory)) != nil {
		return false
	}
	ancestor := r.Directory
	for allowMissing {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || pathutil.SamePath(ancestor, r.Root) {
			return false
		}
		ancestor = filepath.Dir(ancestor)
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	resolvedRoot, rootErr := filepath.EvalSymlinks(r.Root)
	return err == nil && rootErr == nil && pathutil.IsWithinRoot(resolvedRoot, resolved)
}

func (r hdrManagedResource) Release() error {
	if !r.directoryAuthority(true) {
		return errors.New("HDR resource deletion authority is invalid")
	}
	if err := os.RemoveAll(r.Directory); err != nil {
		return fmt.Errorf("release HDR resource: %w", err)
	}
	return nil
}

func (r hdrManagedResource) open(id api.PublicResourceID) (*os.File, error) {
	value, ok := r.Files[id]
	limit := int64(32 << 20)
	if r.Kind == "extraction" {
		limit = hdranalysis.MaxSidecarBytes
	}
	if value.Size <= 0 || value.Size > limit || len(value.SHA256) != 64 {
		return nil, releaseworkflow.ErrPrivateResourceIntegrity
	}
	if !ok || !r.validDirectory() || !pathutil.IsWithinRoot(r.Directory, value.Path) {
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	resolved, err := filepath.EvalSymlinks(value.Path)
	resolvedRoot, rootErr := filepath.EvalSymlinks(r.Directory)
	if err != nil || rootErr != nil || !pathutil.IsWithinRoot(resolvedRoot, resolved) {
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	file, err := os.Open(value.Path)
	if err != nil {
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	hash := sha256.New()
	size, hashErr := io.Copy(hash, io.LimitReader(file, value.Size+1))
	_, seekErr := file.Seek(0, io.SeekStart)
	if hashErr != nil || seekErr != nil || size != value.Size || hex.EncodeToString(hash.Sum(nil)) != value.SHA256 {
		_ = file.Close()
		return nil, releaseworkflow.ErrPrivateResourceIntegrity
	}
	return file, nil
}

func (r hdrManagedResource) OpenExtraction(ctx context.Context, record releaseworkflow.HDRExtractionRecord) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open HDR extraction: %w", err)
	}
	file := r.Files["metadata"]
	if r.Kind != "extraction" || filepath.Base(r.Directory) != string(record.ID) ||
		filepath.Base(filepath.Dir(r.Directory)) != strconv.FormatUint(uint64(record.Release.Generation), 10) ||
		record.Size <= 0 ||
		record.Size > hdranalysis.MaxSidecarBytes ||
		file.Size != record.Size ||
		file.SHA256 != record.SHA256 {
		return nil, releaseworkflow.ErrPrivateResourceIntegrity
	}
	return r.open("metadata")
}

func (r hdrManagedResource) OpenArtifact(
	ctx context.Context,
	snapshot api.HDRAnalysisResult,
	id api.PublicResourceID,
) (releaseworkflow.MediaArtifactContent, error) {
	if err := ctx.Err(); err != nil {
		return releaseworkflow.MediaArtifactContent{}, fmt.Errorf("open HDR plot: %w", err)
	}
	if r.Kind != "analysis" || filepath.Base(r.Directory) != snapshot.AttemptID ||
		filepath.Base(filepath.Dir(r.Directory)) != strconv.FormatUint(uint64(snapshot.Release.Generation), 10) {
		return releaseworkflow.MediaArtifactContent{}, releaseworkflow.ErrPrivateResourceUnavailable
	}
	found := false
	for _, target := range snapshot.Targets {
		if target.Status == api.StageStatusCompleted && target.Artifact != nil && target.Artifact.ID == id {
			found = true
		}
	}
	if !found {
		return releaseworkflow.MediaArtifactContent{}, releaseworkflow.ErrPrivateResourceUnavailable
	}
	file, err := r.open(id)
	if err != nil {
		return releaseworkflow.MediaArtifactContent{}, fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	configuration, decodeErr := png.DecodeConfig(file)
	_, seekErr := file.Seek(0, io.SeekStart)
	if decodeErr != nil || seekErr != nil || configuration.Width != api.HDRAnalysisWidth || configuration.Height != api.HDRAnalysisHeight {
		_ = file.Close()
		return releaseworkflow.MediaArtifactContent{}, releaseworkflow.ErrPrivateResourceIntegrity
	}
	return releaseworkflow.MediaArtifactContent{Body: file, ContentType: "image/png"}, nil
}

func (r hdrManagedResource) LocalArtifactPath(snapshot api.HDRAnalysisResult, id api.PublicResourceID) (string, error) {
	content, err := r.OpenArtifact(context.Background(), snapshot, id)
	if err != nil {
		return "", fmt.Errorf("workflow_hdr_analysis: %w", err)
	}
	if err := content.Body.Close(); err != nil {
		return "", fmt.Errorf("close HDR artifact: %w", err)
	}
	return r.Files[id].Path, nil
}

func (r hdrManagedResource) MarshalPrivateResource() (string, []byte, error) {
	if !r.validDirectory() {
		return "", nil, releaseworkflow.ErrPrivateResourceIntegrity
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		return "", nil, fmt.Errorf("encode HDR private resource: %w", err)
	}
	return "hdr/" + r.Kind + "/v1", encoded, nil
}

func hdrResourceCodec(root, kind string) releaseworkflow.PrivateResourceCodec {
	decode := func(payload []byte, allowMissing bool) (any, error) {
		var resource hdrManagedResource
		if len(payload) > 64<<10 {
			return nil, releaseworkflow.ErrPrivateResourceIntegrity
		}
		if err := json.Unmarshal(payload, &resource, json.RejectUnknownMembers(true)); err != nil {
			return nil, fmt.Errorf("decode HDR resource: %w", err)
		}
		if resource.Kind != kind || !pathutil.SamePath(resource.Root, root) || !resource.directoryAuthority(allowMissing) {
			return nil, releaseworkflow.ErrPrivateResourceIntegrity
		}
		return resource, nil
	}
	return releaseworkflow.PrivateResourceCodec{
		Kind:             "hdr/" + kind + "/v1",
		Decode:           func(payload []byte) (any, error) { return decode(payload, false) },
		DecodeForRelease: func(payload []byte) (any, error) { return decode(payload, true) },
		NoExpiry:         true,
	}
}
