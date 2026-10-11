// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// HDRTargetID creates an opaque stable resource/playlist identity without exposing its input.
func HDRTargetID(resourceIdentity, playlist string) string {
	digest := sha256.Sum256([]byte(resourceIdentity + "\x00" + playlist))
	return "hdr_" + hex.EncodeToString(digest[:16])
}

const (
	HDRAnalysisProfileVersion  = "hdr-analysis-v1"
	HDRExtractionSchemaVersion = "hdr-extraction-v1"
	HDRAnalysisMaxTargets      = 16
	HDRAnalysisWidth           = 3000
	HDRAnalysisHeight          = 1200
)

// HDRPeakSource selects a metadata brightness estimator, independently of extraction.
type HDRPeakSource string

const (
	HDRPeakHistogram       HDRPeakSource = "histogram"
	HDRPeakHistogram99     HDRPeakSource = "histogram99"
	HDRPeakMaxSCL          HDRPeakSource = "max-scl"
	HDRPeakMaxSCLLuminance HDRPeakSource = "max-scl-luminance"
)

// Normalize defaults an omitted estimator to histogram and rejects unsupported values.
func (p HDRPeakSource) Normalize() (HDRPeakSource, error) {
	if p == "" {
		p = HDRPeakHistogram
	}
	switch p {
	case HDRPeakHistogram, HDRPeakHistogram99, HDRPeakMaxSCL, HDRPeakMaxSCLLuminance:
		return p, nil
	default:
		return "", errors.New("HDR peak source is invalid")
	}
}

// NormalizeHDRPlaylist accepts only a numeric MPLS basename, never a path.
func NormalizeHDRPlaylist(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(strings.ToUpper(value), ".MPLS")
	if len(value) == 0 || len(value) > 5 {
		return "", errors.New("HDR playlist must be a number between 0 and 99999")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return "", errors.New("HDR playlist must be a numeric MPLS basename")
		}
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return "", fmt.Errorf("parse HDR playlist: %w", err)
	}
	return fmt.Sprintf("%05d.MPLS", number), nil
}

type HDRAnalysisResultID string
type HDRExtractionID string

// HDRAnalysisRef identifies a retained plot result at its exact workflow revision.
type HDRAnalysisRef struct {
	ID       HDRAnalysisResultID `json:"id"`
	Revision WorkflowRevision    `json:"revision"`
}

type HDRAnalysisFailureCode string

const (
	HDRAnalysisFailureInvalidSelection    HDRAnalysisFailureCode = "invalid_selection"
	HDRAnalysisFailureAmbiguousBinding    HDRAnalysisFailureCode = "ambiguous_binding"
	HDRAnalysisFailureStaleSource         HDRAnalysisFailureCode = "stale_source"
	HDRAnalysisFailureAbsent              HDRAnalysisFailureCode = "absent_hdr10plus"
	HDRAnalysisFailureUnsupportedInput    HDRAnalysisFailureCode = "unsupported_input"
	HDRAnalysisFailureUnsupportedMetadata HDRAnalysisFailureCode = "unsupported_metadata"
	HDRAnalysisFailureInvalidBitstream    HDRAnalysisFailureCode = "invalid_bitstream"
	HDRAnalysisFailureIncomplete          HDRAnalysisFailureCode = "incomplete_extraction"
	HDRAnalysisFailureResourceLimit       HDRAnalysisFailureCode = "resource_limit"
	HDRAnalysisFailureRead                HDRAnalysisFailureCode = "read_failed"
	HDRAnalysisFailureOutput              HDRAnalysisFailureCode = "output_failed"
	HDRAnalysisFailureCanceled            HDRAnalysisFailureCode = "canceled"
	HDRAnalysisFailureInterrupted         HDRAnalysisFailureCode = "interrupted"
	HDRAnalysisFailureResourceUnavailable HDRAnalysisFailureCode = "resource_unavailable"
)

type HDRAnalysisFailure struct {
	Code    HDRAnalysisFailureCode `json:"code"`
	Message string                 `json:"message"`
}

func (f HDRAnalysisFailure) Validate() error {
	switch f.Code {
	case HDRAnalysisFailureInvalidSelection, HDRAnalysisFailureAmbiguousBinding, HDRAnalysisFailureStaleSource,
		HDRAnalysisFailureAbsent, HDRAnalysisFailureUnsupportedInput, HDRAnalysisFailureUnsupportedMetadata,
		HDRAnalysisFailureInvalidBitstream, HDRAnalysisFailureIncomplete, HDRAnalysisFailureResourceLimit,
		HDRAnalysisFailureRead, HDRAnalysisFailureOutput, HDRAnalysisFailureCanceled, HDRAnalysisFailureInterrupted,
		HDRAnalysisFailureResourceUnavailable:
	default:
		return errors.New("HDR analysis failure code is invalid")
	}
	if strings.TrimSpace(f.Message) == "" {
		return errors.New("HDR analysis failure message is required")
	}
	return nil
}

// HDRAnalysisError carries a typed target failure while preserving its diagnostic cause.
type HDRAnalysisError struct {
	Failure HDRAnalysisFailure
	cause   error
}

// NewHDRAnalysisError wraps cause and substitutes resource_unavailable for an invalid failure.
func NewHDRAnalysisError(failure HDRAnalysisFailure, cause error) error {
	if failure.Validate() != nil {
		failure = HDRAnalysisFailure{Code: HDRAnalysisFailureResourceUnavailable, Message: "HDR analysis resources are unavailable"}
	}
	return &HDRAnalysisError{Failure: failure, cause: cause}
}

func (e *HDRAnalysisError) Error() string { return e.Failure.Message }
func (e *HDRAnalysisError) Unwrap() error { return e.cause }

// AsHDRAnalysisFailure retrieves a valid typed failure through wrapped errors.
func AsHDRAnalysisFailure(err error) (HDRAnalysisFailure, bool) {
	typed, ok := errors.AsType[*HDRAnalysisError](err)
	if !ok || typed == nil || typed.Failure.Validate() != nil {
		return HDRAnalysisFailure{}, false
	}
	return typed.Failure, true
}

// HDRAnalysisInstructions selects ordered targets from one exact prepared generation.
type HDRAnalysisInstructions struct {
	Release        ReleaseRef    `json:"release"`
	TargetIDs      []string      `json:"targetIds"`
	PeakSource     HDRPeakSource `json:"peakSource"`
	ProfileVersion string        `json:"profileVersion"`
}

func ValidHDRTargetID(id string) bool {
	if !strings.HasPrefix(id, "hdr_") || len(id) != 36 {
		return false
	}
	for _, c := range id[4:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Normalize validates the release reference and unique targets, copies the target slice,
// and supplies default estimator/profile values. Prepared-source membership is resolved separately.
func (i HDRAnalysisInstructions) Normalize() (HDRAnalysisInstructions, error) {
	if i.Release.Generation == 0 || strings.TrimSpace(i.Release.SourcePath) == "" {
		return HDRAnalysisInstructions{}, errors.New("HDR analysis requires an exact release")
	}
	i.TargetIDs = slices.Clone(i.TargetIDs)
	if len(i.TargetIDs) == 0 || len(i.TargetIDs) > HDRAnalysisMaxTargets {
		return HDRAnalysisInstructions{}, errors.New("HDR analysis requires between 1 and 16 targets")
	}
	seen := make(map[string]bool, len(i.TargetIDs))
	for _, id := range i.TargetIDs {
		if !ValidHDRTargetID(id) || seen[id] {
			return HDRAnalysisInstructions{}, errors.New("HDR analysis target IDs must be valid and unique")
		}
		seen[id] = true
	}
	var err error
	i.PeakSource, err = i.PeakSource.Normalize()
	if err != nil {
		return HDRAnalysisInstructions{}, err
	}
	if i.ProfileVersion == "" {
		i.ProfileVersion = HDRAnalysisProfileVersion
	}
	if i.ProfileVersion != HDRAnalysisProfileVersion {
		return HDRAnalysisInstructions{}, errors.New("HDR analysis profile is unsupported")
	}
	return i, nil
}

// HDRAnalysisTarget supplies safe inventory evidence; extraction establishes actual metadata presence.
type HDRAnalysisTarget struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Playlist        string `json:"playlist,omitempty"`
	SelectionPolicy string `json:"selectionPolicy"`
	Supported       bool   `json:"supported"`
	Reason          string `json:"reason,omitempty"`
}

// HDRAnalysisSubject contains private path authority for one prepared generation.
type HDRAnalysisSubject struct {
	Release             ReleaseRef
	SourceFingerprint   string
	ManifestFingerprint string
	MediaBinding        PreparedMediaBinding
	Title               string
	Targets             []HDRAnalysisTargetSubject
}

type HDRAnalysisTargetSubject struct {
	Target          HDRAnalysisTarget
	Path            string
	DiscRoot        string
	CapturedPath    string
	CapturedSize    int64
	CapturedSHA256  string
	CapturedTrackID uint64
	CapturedAbsent  bool
	CapturedFailure *HDRAnalysisFailure
}

type HDRAnalysisArtifact struct {
	ID     PublicResourceID `json:"id"`
	Width  int              `json:"width"`
	Height int              `json:"height"`
}

type HDRAnalysisTargetResult struct {
	TargetID string               `json:"targetId"`
	Label    string               `json:"label"`
	Status   StageStatus          `json:"status"`
	Frames   int                  `json:"frames"`
	Scenes   int                  `json:"scenes"`
	Profile  string               `json:"profile,omitempty"`
	Artifact *HDRAnalysisArtifact `json:"artifact,omitempty"`
	Failure  *HDRAnalysisFailure  `json:"failure,omitempty"`
}

// HDRAnalysisResult retains terminal target outcomes and plot references for one workflow attempt.
type HDRAnalysisResult struct {
	ID                  HDRAnalysisResultID       `json:"id"`
	WorkflowID          WorkflowID                `json:"workflowId"`
	Revision            WorkflowRevision          `json:"revision"`
	Release             ReleaseRef                `json:"release"`
	ManifestFingerprint string                    `json:"manifestFingerprint"`
	RenderFingerprint   string                    `json:"renderFingerprint,omitempty"`
	AttemptID           string                    `json:"attemptId"`
	TargetIDs           []string                  `json:"targetIds"`
	PeakSource          HDRPeakSource             `json:"peakSource"`
	ProfileVersion      string                    `json:"profileVersion"`
	Status              StageStatus               `json:"status"`
	Targets             []HDRAnalysisTargetResult `json:"targets"`
	CreatedAt           time.Time                 `json:"createdAt" ts_type:"string"`
	CompletedAt         *time.Time                `json:"completedAt,omitempty" ts_type:"string"`
}

// TargetNeedsSourceRetry identifies an explicit retry after unavailable retained metadata.
// Only a failed resource_unavailable outcome authorizes bypassing the target's retained extraction.
func (r HDRAnalysisResult) TargetNeedsSourceRetry(id string) bool {
	for _, target := range r.Targets {
		if target.TargetID == id && target.Status == StageStatusFailed && target.Failure != nil &&
			target.Failure.Code == HDRAnalysisFailureResourceUnavailable {
			return true
		}
	}
	return false
}

// Validate checks result identity, ordered terminal targets and aggregate/artifact metadata.
// Prepared-source and retained-file authority must be verified separately.
func (r HDRAnalysisResult) Validate() error {
	if err := validateSnapshotIdentity(string(r.ID), r.Revision, r.CreatedAt); err != nil {
		return fmt.Errorf("HDR analysis: %w", err)
	}
	if r.WorkflowID == "" || r.ManifestFingerprint == "" || r.AttemptID == "" || r.CompletedAt == nil || r.CompletedAt.Before(r.CreatedAt) {
		return errors.New("HDR analysis requires workflow, manifest, attempt and completion authority")
	}
	if _, err := (HDRAnalysisInstructions{
		Release:        r.Release,
		TargetIDs:      r.TargetIDs,
		PeakSource:     r.PeakSource,
		ProfileVersion: r.ProfileVersion,
	}).Normalize(); err != nil {
		return err
	}
	if len(r.Targets) != len(r.TargetIDs) {
		return errors.New("HDR analysis requires one outcome per target")
	}
	successes := 0
	artifacts := make(map[PublicResourceID]bool)
	for index, target := range r.Targets {
		if target.TargetID != r.TargetIDs[index] {
			return errors.New("HDR analysis target order is invalid")
		}
		//nolint:exhaustive // Only completed and failed per-target outcomes are supported by this profile.
		switch target.Status {
		case StageStatusCompleted:
			if target.Artifact == nil || target.Artifact.ID == "" || artifacts[target.Artifact.ID] || target.Artifact.Width != HDRAnalysisWidth ||
				target.Artifact.Height != HDRAnalysisHeight ||
				target.Frames <= 0 ||
				target.Scenes <= 0 ||
				target.Scenes > target.Frames ||
				target.Failure != nil {
				return errors.New("completed HDR target requires a valid plot and summary")
			}
			artifacts[target.Artifact.ID] = true
			successes++
		case StageStatusFailed:
			if target.Artifact != nil || target.Failure == nil || target.Failure.Validate() != nil {
				return errors.New("failed HDR target requires failure detail")
			}
		default:
			return errors.New("HDR target outcome must be terminal")
		}
	}
	//nolint:exhaustive // Only these terminal aggregate outcomes may be published.
	switch r.Status {
	case StageStatusCompleted:
		if successes != len(r.Targets) {
			return errors.New("completed HDR analysis requires every target")
		}
	case StageStatusPartial:
		if successes == 0 || successes == len(r.Targets) {
			return errors.New("partial HDR analysis requires successful and failed targets")
		}
	case StageStatusFailed:
		if successes != 0 {
			return errors.New("failed HDR analysis cannot contain completed targets")
		}
	case StageStatusCanceled, StageStatusInterrupted, StageStatusUnavailable:
	default:
		return errors.New("HDR analysis outcome must be terminal")
	}
	return nil
}
