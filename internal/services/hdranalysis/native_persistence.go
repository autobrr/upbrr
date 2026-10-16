// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/Audionut/go-hdr10-plus/extract"

	"github.com/autobrr/upbrr/pkg/api"
)

// PublishNativeEvidence publishes completed bounded sections before their
// manifest inside a caller-owned exclusive directory. The caller owns cleanup
// and checkpoint publication; returned integrity covers the closed files.
func PublishNativeEvidence(ctx context.Context, directory string, identity ExtractionIdentity, formats api.HDRAnalysisFormats,
	evidence *NativeEvidence,
) (NativeManifest, NativeSection, error) {
	if evidence.DolbyVision != nil {
		used, err := evidence.AccountedBytes()
		if err != nil {
			return NativeManifest{}, NativeSection{}, err
		}
		if used > MaxRestoreBytes-dvSidecarWriteScratch {
			return NativeManifest{}, NativeSection{}, fmt.Errorf("reserve Dolby Vision sidecar compression: %w", extract.ErrResourceLimit)
		}
	}
	manifest := NativeManifest{
		Schema:         api.HDRExtractionSchemaV2,
		Dependency:     NativeDependencyFingerprint,
		ResourcePolicy: NativeResourcePolicy,
		Identity:       identity,
		Formats:        formats,
		ScanComplete:   true,
		Results:        evidence.Results,
	}
	save := func(key string, encode func(io.Writer) error) (*NativeSection, error) {
		path := filepath.Join(directory, key+".json")
		if err := PublishFile(ctx, path, encode); err != nil {
			return nil, fmt.Errorf("publish native %s section: %w", key, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open native section: %w", err)
		}
		hash := sha256.New()
		size, readErr := io.Copy(hash, io.LimitReader(file, MaxSidecarBytes+1))
		if err := errors.Join(readErr, file.Close()); err != nil {
			return nil, fmt.Errorf("hash native section: %w", err)
		}
		if size > MaxSidecarBytes {
			return nil, errors.New("native section exceeds its bound")
		}
		return &NativeSection{
			ID:     key,
			Size:   size,
			SHA256: hex.EncodeToString(hash.Sum(nil)),
		}, nil
	}
	var err error
	if evidence.HDR10Plus != nil {
		manifest.HDR10Plus, err = save("hdr10plus", func(out io.Writer) error {
			return writeSidecar(ctx, out, Sidecar{
				Identity:   identity,
				Extraction: evidence.HDR10Plus,
				Timeline:   evidence.Timeline,
			}, true)
		})
	}
	if err == nil && evidence.DolbyVision != nil {
		manifest.DolbyVision, err = save("dolby-vision", func(out io.Writer) error {
			return WriteDVSidecar(ctx, out, DVSidecar{
				Identity: identity,
				Analysis: evidence.DolbyVision,
				Timeline: evidence.Timeline,
			})
		})
	}
	if err != nil {
		return NativeManifest{}, NativeSection{}, err
	}
	metadata, err := save("metadata", func(out io.Writer) error { return WriteNativeManifest(ctx, out, manifest) })
	if err != nil {
		return NativeManifest{}, NativeSection{}, err
	}
	return manifest, *metadata, nil
}

// RestoreNativeEvidence opens only sections authorized by a validated manifest.
// The caller supplies its existing resource authority and owns the manifest IO.
func RestoreNativeEvidence(ctx context.Context, manifest NativeManifest, open func(string) (io.ReadCloser, error)) (*NativeEvidence, error) {
	return RestoreNativeEvidenceWithRetained(ctx, manifest, open, 0)
}

// RestoreNativeEvidenceWithRetained charges the other source before allocating either section.
func RestoreNativeEvidenceWithRetained(
	ctx context.Context,
	manifest NativeManifest,
	open func(string) (io.ReadCloser, error),
	otherRetainedBytes int64,
) (*NativeEvidence, error) {
	if otherRetainedBytes < 0 || otherRetainedBytes >= MaxRestoreBytes {
		return nil, errors.New("native restore budget is exhausted")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	evidence := &NativeEvidence{Results: manifest.Results, otherRetainedBytes: otherRetainedBytes}
	for i, section := range []*NativeSection{manifest.HDR10Plus, manifest.DolbyVision} {
		if section == nil {
			continue
		}
		input, err := open(section.ID)
		if err != nil {
			evidence.Release()
			return nil, fmt.Errorf("open native section: %w", err)
		}
		hash := sha256.New()
		limited := &io.LimitedReader{R: input, N: section.Size + 1}
		stream := io.TeeReader(limited, hash)
		if i == 0 {
			var sidecar Sidecar
			sidecar, err = readSidecarWithBudget(ctx, stream, manifest.Identity, MaxRestoreBytes-otherRetainedBytes, true)
			evidence.HDR10Plus, evidence.Timeline = sidecar.Extraction, sidecar.Timeline
		} else {
			var sidecar DVSidecar
			sidecar, err = ReadDVSidecar(ctx, stream, manifest.Identity, MaxRestoreBytes-evidence.HDR10Plus.RetainedBytes()-otherRetainedBytes)
			evidence.DolbyVision = sidecar.Analysis
			if err == nil {
				if manifest.HDR10Plus != nil && !slices.Equal(evidence.Timeline, sidecar.Timeline) {
					err = errors.New("native sections disagree on playlist authority")
				} else {
					evidence.Timeline = sidecar.Timeline
				}
			}
		}
		err = errors.Join(err, input.Close())
		if err != nil {
			evidence.Release()
			return nil, fmt.Errorf("restore native section: %w", err)
		}
		if limited.N != 1 || hex.EncodeToString(hash.Sum(nil)) != section.SHA256 {
			evidence.Release()
			return nil, errors.New("native section integrity is invalid")
		}
	}
	if err := evidence.ValidateSummary(); err != nil {
		evidence.Release()
		return nil, err
	}
	return evidence, nil
}
