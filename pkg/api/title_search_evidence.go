// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// TrackerTitleSearchEvidence records a complete-title lookup without exposing
// private tracker rows or download links. It grants no upload authority.
type TrackerTitleSearchEvidence struct {
	Status            MetadataEvidenceStatus `json:"status"`
	TitleFingerprint  WorkflowFingerprint    `json:"titleFingerprint"`
	ResultFingerprint WorkflowFingerprint    `json:"resultFingerprint"`
	ConfigFingerprint WorkflowFingerprint    `json:"configFingerprint"`
	PolicyID          string                 `json:"policyId"`
	TorrentCount      int                    `json:"torrentCount"`
	FreshUntil        time.Time              `json:"freshUntil" ts_type:"string"`
	CheckedAt         time.Time              `json:"checkedAt" ts_type:"string"`
}

// TitleSearchIdentityFingerprint binds provider work identity to one exact
// prepared source generation. Missing identity never proves title absence.
func TitleSearchIdentityFingerprint(identity ExternalIdentity) (WorkflowFingerprint, error) {
	if strings.TrimSpace(identity.SourcePath) == "" || identity.Generation == 0 || identity.TMDBID <= 0 {
		return "", errors.New("title search requires prepared source and provider identity")
	}
	category, err := identity.RequireCategory()
	if err != nil {
		return "", fmt.Errorf("title search identity: %w", err)
	}
	return CanonicalWorkflowFingerprint(struct {
		SourcePath string
		Generation PreparedGeneration
		TMDBID     int
		Category   CanonicalCategory
	}{identity.SourcePath, identity.Generation, identity.TMDBID, category})
}

// Current reports whether complete lookup evidence belongs to the supplied
// prepared work. The owner must also check policy, configuration and freshness.
func (e TrackerTitleSearchEvidence) Current(identity ExternalIdentity) bool {
	fingerprint, err := TitleSearchIdentityFingerprint(identity)
	return err == nil && e.Status == MetadataEvidenceStatusComplete && e.TorrentCount >= 0 &&
		e.ResultFingerprint != "" && e.TitleFingerprint == fingerprint
}

// HasOtherTorrent proves a positive title match even when pagination did not
// finish. Incomplete searches can never prove absence. Owners still validate
// configuration, policy and freshness before retaining the evidence.
func (e TrackerTitleSearchEvidence) HasOtherTorrent(identity ExternalIdentity) bool {
	fingerprint, err := TitleSearchIdentityFingerprint(identity)
	return err == nil && (e.Status == MetadataEvidenceStatusComplete || e.Status == MetadataEvidenceStatusPartial) && e.TorrentCount > 0 &&
		e.ResultFingerprint != "" &&
		e.TitleFingerprint == fingerprint
}
