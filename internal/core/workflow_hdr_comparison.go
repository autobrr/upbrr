// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func verifyHDRComparison(ctx context.Context, source releaseworkflow.HDRComparisonSource) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	record, err := releaseworkflow.VerifyHDRComparisonSource(ctx, source.Path, verifyWorkflowInput)
	if err != nil {
		return fmt.Errorf("verify HDR comparison: %w", err)
	}
	if record.SourceVersion != source.Ref.Fingerprint || !pathing.SamePath(record.CanonicalPath, source.Path) {
		return api.ErrActiveInputChanged
	}
	return nil
}

func (b workflowHDRAnalysisBuilder) hdrComparisonInput(ctx context.Context, session *hdranalysis.Session,
	source *releaseworkflow.HDRComparisonSource, primary *hdranalysis.NativeEvidence, release api.ReleaseRef,
	entries map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord,
	retained map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource,
	publish func(releaseworkflow.HDRExtractionRecord, releaseworkflow.RetainedHDRExtractionResource) error,
	attempt string,
) (*hdranalysis.DecodedInput, error) {
	if source == nil || source.Ref.Release != release {
		return nil, releaseworkflow.ErrPrivateResourceUnavailable
	}
	if err := verifyHDRComparison(ctx, *source); err != nil {
		return nil, fmt.Errorf("HDR comparison metadata: %w", err)
	}
	otherBytes, err := primary.AccountedBytes()
	if err != nil {
		return nil, fmt.Errorf("HDR comparison metadata: %w", err)
	}
	// Reserve measurement records and decoder/renderer scratch in addition to A.
	otherBytes += 128 << 20
	identity := hdranalysis.ExtractionIdentity{
		SourceFingerprint: source.Ref.Fingerprint,
		TargetID:          api.HDRTargetID(source.Path, source.Ref.ID),
		SelectionPolicy:   "unique_hevc",
	}
	evidence, found, err := restoreNativeHDREvidenceWithRetained(ctx, release, identity, api.HDRAnalysisFormatsBoth, entries, retained, otherBytes)
	if err != nil {
		return nil, fmt.Errorf("HDR comparison metadata: %w", err)
	}
	if !found {
		native, err := session.ExtractNativeFileWithRetained(ctx, source.Path, api.HDRAnalysisFormatsBoth, otherBytes)
		if err != nil {
			return nil, fmt.Errorf("HDR comparison metadata: %w", err)
		}
		identity.ResolvedTrackID = native.TrackID
		evidence, err = native.Evidence(api.HDRAnalysisFormatsBoth, source.Evidence)
		native.Release()
		if err != nil {
			return nil, fmt.Errorf("HDR comparison metadata: %w", err)
		}
		if err := verifyHDRComparison(ctx, *source); err != nil {
			evidence.Release()
			return nil, fmt.Errorf("HDR comparison metadata: %w", err)
		}
		record, resource, err := b.retainNativeExtraction(ctx, release, identity, api.HDRAnalysisFormatsBoth, evidence, attempt+"-comparison")
		if err != nil {
			evidence.Release()
			return nil, fmt.Errorf("HDR comparison metadata: %w", err)
		}
		if err := publish(record, resource); err != nil {
			evidence.Release()
			return nil, fmt.Errorf("checkpoint HDR comparison metadata: %w", err)
		}
		entries[record.ID], retained[record.ID] = record, resource
	}
	input := &hdranalysis.DecodedInput{
		Path:        source.Path,
		Fingerprint: source.Ref.Fingerprint,
		Native:      evidence,
	}
	if source.Evidence.Decoded != nil {
		input.Source = *source.Evidence.Decoded
	}
	return input, nil
}
