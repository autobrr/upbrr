// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"github.com/autobrr/upbrr/pkg/api"
)

// NativeDependencyFingerprint binds retained evidence to compatible parser revisions and native semantics.
const NativeDependencyFingerprint = "hdr:34d94903fa8c;dovi:f3ae2caa8743;playlist-v1;limits-v1"

// NativeResourcePolicy versions the authority of saved parser resource-limit outcomes.
const NativeResourcePolicy = "3gib-v2"

// NativeSection binds a fixed resource key to the encoded sidecar's size and SHA-256.
// The resource owner verifies those bytes when opening the section.
type NativeSection struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// NativeManifest joins independently hashed sections from one completed scan.
// Section names are fixed resource keys, never filesystem paths.
type NativeManifest struct {
	Schema         string                 `json:"schema"`
	Dependency     string                 `json:"dependency"`
	ResourcePolicy string                 `json:"resourcePolicy,omitempty"`
	Identity       ExtractionIdentity     `json:"identity"`
	Formats        api.HDRAnalysisFormats `json:"formats"`
	ScanComplete   bool                   `json:"scanComplete"`
	Results        []api.HDRFormatResult  `json:"results"`
	HDR10Plus      *NativeSection         `json:"hdr10plus,omitempty"`
	DolbyVision    *NativeSection         `json:"dolbyVision,omitempty"`
}

// Validate checks schema compatibility, outcome consistency and section descriptors.
// It does not open sections or verify their contents against the stored hashes.
func (m NativeManifest) Validate() error {
	if m.Schema != api.HDRExtractionSchemaV2 || m.Dependency != NativeDependencyFingerprint || !m.ScanComplete || m.Identity.SourceFingerprint == "" ||
		!api.ValidHDRTargetID(m.Identity.TargetID) {
		return errors.New("native HDR manifest identity is invalid")
	}
	if m.ResourcePolicy != "" && m.ResourcePolicy != "3gib-v1" && m.ResourcePolicy != NativeResourcePolicy {
		return errors.New("native HDR resource policy is incompatible")
	}
	if _, err := m.Formats.Normalize(); err != nil {
		return fmt.Errorf("native HDR manifest selection: %w", err)
	}
	if err := m.Formats.ValidateResults(m.Results); err != nil {
		return fmt.Errorf("native HDR manifest outcomes: %w", err)
	}
	for i, section := range []*NativeSection{m.HDR10Plus, m.DolbyVision} {
		if err := m.Results[i].Validate(); err != nil {
			return fmt.Errorf("native HDR outcome: %w", err)
		}
		state := m.Results[i].State
		if (state == api.HDRExtractionComplete) != (section != nil) && (i != 1 || state != api.HDRExtractionAbsent || section == nil) {
			return errors.New("native HDR manifest section state is invalid")
		}
		if section != nil {
			want := "hdr10plus"
			if i == 1 {
				want = "dolby-vision"
			}
			if section.ID != want || section.Size <= 0 || section.Size > MaxSidecarBytes || len(section.SHA256) != 64 {
				return errors.New("native HDR section integrity is invalid")
			}
		}
	}
	return nil
}

// WriteNativeManifest validates and writes at most 64 KiB to a caller-owned writer.
// Failed writes may leave partial output; the caller owns atomic publication.
func WriteNativeManifest(ctx context.Context, out io.Writer, manifest NativeManifest) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write native HDR manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	if err := json.MarshalWrite(&boundedWriter{output: out, remaining: 64 << 10}, manifest); err != nil {
		return fmt.Errorf("encode native HDR manifest: %w", err)
	}
	return nil
}

// ReadNativeManifest reads bounded strict JSON and checks the expected source and selection.
// A zero expected track accepts the manifest's resolved track. Legacy quota failures
// become explicit recovery outcomes while successful sibling sections remain usable.
func ReadNativeManifest(ctx context.Context, in io.Reader, expected ExtractionIdentity) (NativeManifest, error) {
	if err := ctx.Err(); err != nil {
		return NativeManifest{}, fmt.Errorf("read native HDR manifest: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(in, (64<<10)+1))
	if err != nil {
		return NativeManifest{}, fmt.Errorf("read native HDR manifest: %w", err)
	}
	if len(data) > 64<<10 {
		return NativeManifest{}, errors.New("native HDR manifest exceeds bounds")
	}
	var manifest NativeManifest
	if err := json.Unmarshal(data, &manifest, json.RejectUnknownMembers(true)); err != nil {
		return NativeManifest{}, fmt.Errorf("decode native HDR manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return NativeManifest{}, err
	}
	if manifest.Identity.SourceFingerprint != expected.SourceFingerprint || manifest.Identity.TargetID != expected.TargetID ||
		manifest.Identity.SelectionPolicy != expected.SelectionPolicy ||
		expected.ResolvedTrackID != 0 && manifest.Identity.ResolvedTrackID != expected.ResolvedTrackID {
		return NativeManifest{}, errors.New("native HDR manifest source or selection differs")
	}
	if manifest.ResourcePolicy != NativeResourcePolicy {
		// Only the old quota failure needs recovery. Its successful sibling remains
		// usable, including for source-less rendering and captured-format selection.
		for i := range manifest.Results {
			failure := manifest.Results[i].Failure
			if failure != nil && failure.Code == api.HDRAnalysisFailureResourceLimit {
				manifest.Results[i].Failure = &api.HDRAnalysisFailure{
					Code:    api.HDRAnalysisFailureResourceUnavailable,
					Message: "Saved metadata reached an older parser allowance; explicitly retry source analysis.",
				}
			}
		}
	}
	return manifest, nil
}
