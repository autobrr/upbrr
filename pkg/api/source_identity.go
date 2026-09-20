// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	slashpath "path" //nolint:depguard // Normalizes slash-delimited identity paths.
	"sort"
	"strings"
)

const (
	// SourceContentIdentitySampleBytes is the largest initial file sample included in a source identity.
	SourceContentIdentitySampleBytes int64 = 1 << 20
	// SourceContentIdentityVersion identifies the bounded source-sample digest format.
	SourceContentIdentityVersion = "source-content-sample-v2"
	// SubmissionContentIdentityVersion identifies the submitted-inventory digest derived from source samples.
	SubmissionContentIdentityVersion = "submission-content-sample-v2"
)

// SubmissionContentScope identifies the canonical content shape supplied to
// torrent creation and tracker submission.
type SubmissionContentScope string

const (
	// SubmissionContentScopeSingleFile identifies one submitted file.
	SubmissionContentScopeSingleFile SubmissionContentScope = "single_file"
	// SubmissionContentScopeFileSet identifies an ordered multi-file submission.
	SubmissionContentScopeFileSet SubmissionContentScope = "file_set"
	// SubmissionContentScopeFullDisc identifies an entire resolved disc inventory.
	SubmissionContentScopeFullDisc SubmissionContentScope = "full_disc"
)

// VerifiedSourceFile records one private bounded source sample. SHA256 is the
// SHA-256 of the first min(Size, SourceContentIdentitySampleBytes) bytes and
// must never be treated as a full-file digest.
type VerifiedSourceFile struct {
	LocalPath    string `json:"localPath"`
	RelativePath string `json:"relativePath"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sampleSha256"`
}

// SourceContentIdentity identifies a canonical source path, inventory layout,
// and bounded initial-file samples. It is private prepared-input evidence, not
// complete local-byte verification; PreparedRelease excludes it from transport JSON.
type SourceContentIdentity struct {
	Version             string               `json:"version"`
	Digest              string               `json:"digest"`
	ManifestFingerprint string               `json:"manifestFingerprint"`
	Files               []VerifiedSourceFile `json:"files"`
}

// VerifiedInputSource binds a canonical source manifest to its bounded source
// samples. It is an internal preparation handoff, never a browser transport shape.
type VerifiedInputSource struct {
	Manifest SourceManifest        `json:"manifest"`
	Identity SourceContentIdentity `json:"identity"`
}

// SubmissionContentFile is one path-free entry in a submitted-inventory
// identity. SHA256 is private bounded source-sample evidence; RelativePath is
// omitted for single-file identities because the filename is not part of their
// submission authority.
type SubmissionContentFile struct {
	RelativePath string `json:"relativePath,omitempty"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sampleSha256"`
}

// SubmissionContentIdentity identifies the selected local inventory and its
// private bounded source samples. It excludes source roots and tracker-specific
// metainfo and does not prove full-file byte equality.
type SubmissionContentIdentity struct {
	Version string                  `json:"version"`
	Digest  string                  `json:"digest"`
	Scope   SubmissionContentScope  `json:"scope"`
	Files   []SubmissionContentFile `json:"files"`
}

// NewSubmissionContentIdentity canonicalizes sampled submitted files and
// derives their versioned SHA-256 identity without modifying files.
// Single-file scope drops the filename; other scopes normalize and sort relative paths.
// Empty inventories, unsupported scopes, invalid sizes or sample digests, escaping paths,
// and duplicate normalized paths return a zero identity and an error.
func NewSubmissionContentIdentity(
	scope SubmissionContentScope,
	files []SubmissionContentFile,
) (SubmissionContentIdentity, error) {
	canonical, err := canonicalSubmissionContentFiles(scope, files)
	if err != nil {
		return SubmissionContentIdentity{}, err
	}
	payload, err := json.Marshal(struct {
		Scope SubmissionContentScope  `json:"scope"`
		Files []SubmissionContentFile `json:"files"`
	}{
		Scope: scope,
		Files: canonical,
	})
	if err != nil {
		return SubmissionContentIdentity{}, fmt.Errorf("submission content identity: marshal canonical payload: %w", err)
	}
	digest := sha256.Sum256(append([]byte(SubmissionContentIdentityVersion+"\x00"), payload...))
	return SubmissionContentIdentity{
		Version: SubmissionContentIdentityVersion,
		Digest:  hex.EncodeToString(digest[:]),
		Scope:   scope,
		Files:   canonical,
	}, nil
}

func canonicalSubmissionContentFiles(
	scope SubmissionContentScope,
	files []SubmissionContentFile,
) ([]SubmissionContentFile, error) {
	if len(files) == 0 {
		return nil, errors.New("submission content identity: at least one sampled file is required")
	}
	switch scope {
	case SubmissionContentScopeSingleFile, SubmissionContentScopeFileSet, SubmissionContentScopeFullDisc:
	default:
		return nil, fmt.Errorf("submission content identity: unsupported scope %q", scope)
	}
	if scope == SubmissionContentScopeSingleFile && len(files) != 1 {
		return nil, errors.New("submission content identity: single_file requires exactly one file")
	}
	canonical := make([]SubmissionContentFile, len(files))
	for index, file := range files {
		if file.Size < 0 {
			return nil, fmt.Errorf("submission content identity: file %d has a negative size", index)
		}
		if !validSHA256(file.SHA256) {
			return nil, fmt.Errorf("submission content identity: file %d has an invalid SHA-256", index)
		}
		file.SHA256 = strings.ToLower(file.SHA256)
		if scope == SubmissionContentScopeSingleFile {
			file.RelativePath = ""
		} else {
			path, err := canonicalIdentityRelativePath(file.RelativePath)
			if err != nil {
				return nil, fmt.Errorf("submission content identity: file %d: %w", index, err)
			}
			file.RelativePath = path
		}
		canonical[index] = file
	}
	if scope == SubmissionContentScopeSingleFile {
		return canonical, nil
	}
	sort.Slice(canonical, func(left, right int) bool {
		return canonical[left].RelativePath < canonical[right].RelativePath
	})
	for index := 1; index < len(canonical); index++ {
		if canonical[index-1].RelativePath == canonical[index].RelativePath {
			return nil, fmt.Errorf("submission content identity: duplicate canonical path %q", canonical[index].RelativePath)
		}
	}
	return canonical, nil
}

func canonicalIdentityRelativePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("relative path is required")
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(value, "/") || hasVolumePrefix(value) {
		return "", fmt.Errorf("relative path %q escapes its source root", value)
	}
	value = slashpath.Clean(value)
	if value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return "", fmt.Errorf("relative path %q escapes its source root", value)
	}
	return value, nil
}

func hasVolumePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':'
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
