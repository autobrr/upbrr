// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func (b workflowHDRAnalysisBuilder) CleanupAttempt(
	release api.ReleaseRef,
	attempt string,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
) error {
	clean := func(kind, id string) error {
		directory, err := hdrAttemptRoot(b.root, release, "hdr-"+kind, id)
		if err != nil {
			return fmt.Errorf("workflow_hdr_restore: %w", err)
		}
		if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("workflow_hdr_restore: %w", err)
		}
		return (hdrManagedResource{
			Root:      b.root,
			Directory: directory,
			Kind:      kind,
		}).Release()
	}
	if err := clean("analysis", attempt); err != nil {
		return fmt.Errorf("workflow_hdr_restore: %w", err)
	}
	for index := 1; index <= api.HDRAnalysisMaxTargets; index++ {
		id := api.HDRExtractionID(attempt + "-" + strconv.Itoa(index))
		if entry, retained := entries[id]; retained && entry.Release == release {
			continue
		}
		if err := clean("extraction", string(id)); err != nil {
			return fmt.Errorf("workflow_hdr_restore: %w", err)
		}
	}
	directory, err := hdrAttemptRoot(b.root, release, "hdr-extraction", attempt+"-rebind-1")
	if err != nil {
		return fmt.Errorf("resolve HDR rebind cleanup: %w", err)
	}
	parent := filepath.Dir(directory)
	if _, err := os.Lstat(parent); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect HDR rebind cleanup: %w", err)
	}
	if !(hdrManagedResource{
		Root:      b.root,
		Directory: directory,
		Kind:      "extraction",
	}).directoryAuthority(true) {
		return releaseworkflow.ErrPrivateResourceIntegrity
	}
	children, err := os.ReadDir(parent)
	if err != nil {
		return fmt.Errorf("read HDR rebind cleanup: %w", err)
	}
	for _, child := range children {
		if !strings.HasPrefix(child.Name(), attempt+"-rebind-") {
			continue
		}
		id := api.HDRExtractionID(child.Name())
		if entry, retained := entries[id]; retained && entry.Release == release {
			continue
		}
		if err := clean("extraction", child.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (b workflowHDRAnalysisBuilder) ValidateAuthority(ctx context.Context, result api.HDRAnalysisResult) error {
	subject, err := b.resolver.ResolveHDRAnalysisSubject(ctx, api.HDRAnalysisInstructions{
		Release:    result.Release,
		TargetIDs:  result.TargetIDs,
		PeakSource: result.PeakSource,
	})
	if err != nil {
		return fmt.Errorf("workflow_hdr_restore: %w", err)
	}
	if subject.ManifestFingerprint != result.ManifestFingerprint {
		return fmt.Errorf(
			"workflow_hdr_restore: %w",
			api.NewHDRAnalysisError(api.HDRAnalysisFailure{Code: api.HDRAnalysisFailureStaleSource, Message: "the HDR source changed"}, nil),
		)
	}
	return nil
}

func (b workflowHDRAnalysisBuilder) CloneExtractions(
	ctx context.Context,
	release api.ReleaseRef,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	retained map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource,
	attempt string,
) (map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord, map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource, error) {
	cloned := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
	resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
	err := b.service.WithSession(ctx, func(_ *hdranalysis.Session) error {
		seen := make(map[string]bool)
		for _, id := range hdrExtractionIDs(entries) {
			record := entries[id]
			if seen[record.TargetID] || retained[id] == nil {
				continue
			}
			instructions := api.HDRAnalysisInstructions{Release: release, TargetIDs: []string{record.TargetID}}
			subject, err := b.resolver.ResolveHDRAnalysisSubject(ctx, instructions)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			if subject.SourceFingerprint != record.SourceFingerprint || len(subject.Targets) != 1 {
				continue
			}
			identity := hdranalysis.ExtractionIdentity{
				SourceFingerprint: subject.SourceFingerprint,
				TargetID:          record.TargetID,
				SelectionPolicy:   subject.Targets[0].Target.SelectionPolicy,
			}
			sidecar, found, err := restoreHDRSidecar(
				ctx,
				record.Release,
				subject.Targets[0],
				identity,
				map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord{id: record},
				retained,
			)
			if err != nil || !found {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			copyID := attempt + "-" + strconv.Itoa(len(cloned)+1)
			copyRecord, resource, err := b.retainExtraction(ctx, release, sidecar, copyID)
			if err != nil {
				return fmt.Errorf("workflow_hdr_restore: %w", err)
			}
			cloned[copyRecord.ID], resources[copyRecord.ID] = copyRecord, resource
			seen[record.TargetID] = true
		}
		return nil
	})
	if err != nil {
		for _, resource := range resources {
			if releaser, ok := resource.(interface{ Release() error }); ok {
				err = errors.Join(err, releaser.Release())
			}
		}
		return nil, nil, fmt.Errorf("workflow_hdr_restore: %w", err)
	}
	return cloned, resources, nil
}
