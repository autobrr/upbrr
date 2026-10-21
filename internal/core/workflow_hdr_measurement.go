// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func (r hdrManagedResource) OpenMeasurement(ctx context.Context, record releaseworkflow.HDRMeasurementRecord) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open HDR measurement: %w", err)
	}
	limit := int64(hdranalysis.MaxAutomaticMeasurementBytes)
	switch {
	case record.AudioTrackID != "":
		limit = hdranalysis.MaxTrackAudioMeasurementBytes
	case record.Result.Profile == hdranalysis.BoundaryMeasurementProfile:
		limit = hdranalysis.MaxBoundaryMeasurementBytes
	case record.Result.Profile == hdranalysis.BoundaryAreaProfile:
		limit = hdranalysis.MaxMeasurementBytes
	}
	file := r.Files["measurement"]
	if r.Kind != "measurement" || filepath.Base(r.Directory) != record.ID ||
		filepath.Base(filepath.Dir(r.Directory)) != strconv.FormatUint(uint64(record.Release.Generation), 10) ||
		record.Size <= 0 || record.Size > limit || file.Size != record.Size || file.SHA256 != record.SHA256 {
		return nil, releaseworkflow.ErrPrivateResourceIntegrity
	}
	return r.open("measurement", limit)
}

func (b workflowHDRAnalysisBuilder) retainMeasurement(ctx context.Context, native releaseworkflow.HDRExtractionRecord,
	identity, id string, report *hdranalysis.MeasurementReport,
) (releaseworkflow.HDRMeasurementRecord, hdrManagedResource, error) {
	var record releaseworkflow.HDRMeasurementRecord
	directory, err := hdrAttemptRoot(b.root, native.Release, "hdr-measurement", id)
	if err != nil {
		return record, hdrManagedResource{}, err
	}
	if err := createHDRDirectory(directory); err != nil {
		return record, hdrManagedResource{}, err
	}
	resource := hdrManagedResource{
		Root:      b.root,
		Directory: directory,
		Kind:      "measurement",
		Files:     make(map[api.PublicResourceID]hdrManagedFile),
	}
	path := filepath.Join(directory, "measurement.json")
	err = hdranalysis.PublishFile(ctx, path, func(out io.Writer) error { return hdranalysis.WriteMeasurement(ctx, out, report) })
	if err != nil {
		_ = resource.Release()
		return record, hdrManagedResource{}, fmt.Errorf("retain numeric HDR measurement: %w", err)
	}
	file, err := hdrFileIntegrity(path)
	if err != nil {
		_ = resource.Release()
		return record, hdrManagedResource{}, err
	}
	resource.Files["measurement"] = file
	record = releaseworkflow.HDRMeasurementRecord{
		ID:                id,
		Release:           native.Release,
		TargetID:          native.TargetID,
		SourceFingerprint: native.SourceFingerprint,
		ExtractionID:      native.ID,
		ExtractionSHA256:  native.SHA256,
		Identity:          identity,
		Decoder:           report.Decoder.Fingerprint,
		Result:            report.Result,
		Size:              file.Size,
		SHA256:            file.SHA256,
	}
	if report.AudioAuthority != nil {
		record.AudioTrackID = report.AudioAuthority.TrackID
		record.AudioScopeFingerprint, err = hdrAudioScopeFingerprint(*report.AudioAuthority)
		if err != nil {
			_ = resource.Release()
			return releaseworkflow.HDRMeasurementRecord{}, hdrManagedResource{}, err
		}
	}
	return record, resource, nil
}

func hdrAudioScopeFingerprint(authority hdranalysis.HDRAudioAuthority) (string, error) {
	// The immutable manifest binds original prepared facts. Decoder-filled facts
	// belong to the measurement identity, so initially unknown facts can restore
	// without probing source media. Scope never includes primary/all membership.
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Source, Resource, Manifest, Track, Profile string
		Ordinal                                    int
	}{authority.SourceFingerprint, authority.ResourceID, authority.ManifestFingerprint, authority.TrackID, api.AudioAnalysisProfileVersion, authority.Ordinal})
	return string(fingerprint), err
}
