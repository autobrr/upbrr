// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const mediaCompatibilityVersion = "media-compatibility-v1"

// MediaCompatibilityKey identifies source facts that can safely reuse media
// across prepared generations. It is separate from PreparedMediaBinding,
// which remains the authority for an exact generation.
type MediaCompatibilityKey string

// Valid reports whether the key is a complete strong compatibility claim.
func (k MediaCompatibilityKey) Valid() bool {
	return len(k) == sha256.Size*2 && validSHA256(string(k))
}

// ReusableMediaAsset is one private image retained for a strong source and
// capture compatibility claim. Its binding is the old exact generation; a
// caller must materialize fresh workflow artifact references before use.
type ReusableMediaAsset struct {
	Binding            PreparedMediaBinding
	CompatibilityKey   MediaCompatibilityKey
	CaptureFingerprint WorkflowFingerprint
	ContentSHA256      string
	Kind               MediaArtifactKind
	Image              ScreenshotImage
	Selected           bool
	Order              int
	HostedLinks        []UploadedImageLink
}

// Valid reports whether the asset is independently reusable. Legacy records
// without a strong source key, capture claim, or content digest are rejected.
func (a ReusableMediaAsset) Valid() bool {
	return a.Binding.Valid() && a.CompatibilityKey.Valid() && strings.TrimSpace(string(a.CaptureFingerprint)) != "" &&
		validSHA256(a.ContentSHA256) && strings.TrimSpace(a.Image.Path) != "" && a.Kind != "" && validReusableMediaPurpose(a.Image.Purpose)
}

func validReusableMediaPurpose(purpose ScreenshotPurpose) bool {
	return purpose == ScreenshotPurposeFinal || purpose == ScreenshotPurposeMenu
}

// MediaReuseRepository atomically records strong reusable-media evidence and
// its exact final workflow snapshot. It never grants access to prior workflow
// resources or approvals.
type MediaReuseRepository interface {
	CommitReusableMedia(context.Context, PreparedMediaBinding, MediaCompatibilityKey, []ReusableMediaAsset, ReusableMediaCommit) error
	HasReusableMediaCommit(context.Context, ReusableMediaCommit) (bool, error)
	LoadReusableMediaAssets(context.Context, MediaCompatibilityKey) ([]ReusableMediaAsset, error)
	DeleteReusableMediaAssets(context.Context, PreparedMediaBinding, []string) error
}

// ReusableMediaCommit identifies one final workflow media snapshot whose
// reusable associations were persisted successfully.
type ReusableMediaCommit struct {
	WorkflowID WorkflowID
	MediaID    MediaArtifactSetID
	Revision   WorkflowRevision
}

// Valid reports whether the commit can identify one immutable media snapshot.
func (c ReusableMediaCommit) Valid() bool {
	return c.WorkflowID != "" && c.MediaID != "" && c.Revision > 0
}

// PreparedMediaBinding identifies repository media for one exact prepared generation.
type PreparedMediaBinding struct {
	SourcePath               string
	PreparedMediaFingerprint string
	PreparedGeneration       PreparedGeneration
	// CompatibilityKey may locate reusable media from a prior exact binding.
	// It never grants access to that binding's workflow artifacts.
	CompatibilityKey MediaCompatibilityKey
}

// Valid reports whether every binding component is present.
func (b PreparedMediaBinding) Valid() bool {
	return strings.TrimSpace(b.SourcePath) != "" &&
		strings.TrimSpace(b.PreparedMediaFingerprint) != "" &&
		b.PreparedGeneration > 0
}

// Equal reports exact equality across all binding components.
func (b PreparedMediaBinding) Equal(other PreparedMediaBinding) bool {
	return b.SourcePath == other.SourcePath &&
		b.PreparedMediaFingerprint == other.PreparedMediaFingerprint &&
		b.PreparedGeneration == other.PreparedGeneration
}

// MediaBinding identifies media derived from this exact prepared generation.
func (r PreparedRelease) MediaBinding() (PreparedMediaBinding, error) {
	fingerprint, err := CanonicalWorkflowFingerprint(struct {
		ContractVersion            string
		SourceFingerprint          string
		FactInstructionFingerprint string
		Generation                 PreparedGeneration
		Discs                      []DiscItemFacts
		SelectedPlaylists          []PlaylistInfo
	}{
		ContractVersion:            r.Compatibility.ContractVersion,
		SourceFingerprint:          r.Compatibility.SourceFingerprint,
		FactInstructionFingerprint: r.Compatibility.FactInstructionFingerprint,
		Generation:                 r.Generation,
		Discs:                      r.Disc.Items,
		SelectedPlaylists:          r.Disc.SelectedPlaylists(),
	})
	if err != nil {
		return PreparedMediaBinding{}, fmt.Errorf("prepared release: fingerprint prepared media: %w", err)
	}
	binding := PreparedMediaBinding{
		SourcePath:               r.Source.SourcePath,
		PreparedMediaFingerprint: string(fingerprint),
		PreparedGeneration:       r.Generation,
	}
	if compatibilityKey, compatibilityErr := r.MediaCompatibilityKey(); compatibilityErr == nil {
		binding.CompatibilityKey = compatibilityKey
	}
	if !binding.Valid() {
		return PreparedMediaBinding{}, errors.New("prepared release: incomplete media binding")
	}
	return binding, nil
}

// MediaCompatibilityKey derives a strong reusable-media key from verified
// source bytes and the source selections that determine capture input. It
// intentionally excludes prepared generation, provider facts, naming, and
// tracker choices.
func (r PreparedRelease) MediaCompatibilityKey() (MediaCompatibilityKey, error) {
	identity := r.SourceIdentity
	if identity.Version != SourceContentIdentityVersion || !validSHA256(identity.Digest) {
		return "", errors.New("prepared release: verified source identity is required for media reuse")
	}
	payload, err := CanonicalWorkflowFingerprint(struct {
		Version           string
		SourceVersion     string
		SourceDigest      string
		DiscType          string
		DiscItems         []DiscItemFacts
		SelectedPlaylists []PlaylistInfo
	}{
		Version:           mediaCompatibilityVersion,
		SourceVersion:     identity.Version,
		SourceDigest:      strings.ToLower(identity.Digest),
		DiscType:          r.Disc.Type,
		DiscItems:         r.Disc.Items,
		SelectedPlaylists: r.Disc.SelectedPlaylists(),
	})
	if err != nil {
		return "", fmt.Errorf("prepared release: fingerprint media compatibility: %w", err)
	}
	decoded, err := hex.DecodeString(string(payload))
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("prepared release: invalid media compatibility fingerprint")
	}
	return MediaCompatibilityKey(payload), nil
}
