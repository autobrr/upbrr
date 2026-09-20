// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

// ErrSourceChanged reports that a source manifest no longer describes the
// local source while an operation is verifying or consuming it.
var ErrSourceChanged = errors.New("prepared release: source changed")

// SourceIdentityProgress reports bounded local source-sampling progress. Its
// Path is private local-operation evidence and must be sanitized before any
// browser-facing transport projection.
type SourceIdentityProgress struct {
	Path           string
	CompletedBytes int64
	TotalBytes     int64
}

// SourceIdentityProgressReporter receives synchronous source-sampling progress.
type SourceIdentityProgressReporter func(SourceIdentityProgress)

// VerifyInputSource resolves, inventories, and samples one requested input
// without collecting providers or changing persisted preparation state.
func VerifyInputSource(ctx context.Context, input api.PrepareInput) (api.VerifiedInputSource, error) {
	return VerifyInputSourceWithProgress(ctx, input, nil)
}

// VerifyInputSourceWithProgress is VerifyInputSource with private local
// verification progress for the owning operation.
func VerifyInputSourceWithProgress(
	ctx context.Context,
	input api.PrepareInput,
	report SourceIdentityProgressReporter,
) (api.VerifiedInputSource, error) {
	if ctx == nil {
		return api.VerifiedInputSource{}, errors.New("prepared release: source identity context is required")
	}
	normalized, err := normalizePrepareInput(input)
	if err != nil {
		return api.VerifiedInputSource{}, err
	}
	layout, err := sourcelayout.Resolve(ctx, normalized.input.SourcePath)
	if err != nil {
		return api.VerifiedInputSource{}, fmt.Errorf("prepared release: resolve source identity layout: %w", err)
	}
	manifest, fingerprint, err := inspectSource(ctx, normalized.input, layout)
	if err != nil {
		return api.VerifiedInputSource{}, err
	}
	identity, err := VerifySourceContentIdentity(ctx, manifest, report)
	if err != nil {
		return api.VerifiedInputSource{}, err
	}
	identity.ManifestFingerprint = fingerprint
	return api.VerifiedInputSource{Manifest: manifest, Identity: identity}, nil
}

// VerifySourceContentIdentity samples at most SourceContentIdentitySampleBytes
// from each manifest file and returns a bounded source identity. It checks the
// inventory's paths, sizes, and modification times before and after sampling,
// but does not provide complete local-byte verification.
func VerifySourceContentIdentity(
	ctx context.Context,
	manifest api.SourceManifest,
	report SourceIdentityProgressReporter,
) (api.SourceContentIdentity, error) {
	if ctx == nil {
		return api.SourceContentIdentity{}, errors.New("prepared release: source identity context is required")
	}
	files, layout, total, err := sourceIdentityManifest(manifest)
	if err != nil {
		return api.SourceContentIdentity{}, err
	}
	if err := VerifySourceManifestStability(ctx, manifest); err != nil {
		return api.SourceContentIdentity{}, err
	}

	verified := make([]api.VerifiedSourceFile, 0, len(files))
	var completed int64
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return api.SourceContentIdentity{}, fmt.Errorf("prepared release: source identity canceled: %w", err)
		}
		if report != nil {
			report(SourceIdentityProgress{
				Path:           file.localPath,
				CompletedBytes: completed,
				TotalBytes:     total,
			})
		}
		sampleBytes := min(file.size, api.SourceContentIdentitySampleBytes)
		digest, bytesRead, err := hashSourceFileSample(ctx, file.localPath, sampleBytes, completed, total, report)
		if err != nil {
			return api.SourceContentIdentity{}, err
		}
		if bytesRead != sampleBytes {
			return api.SourceContentIdentity{}, fmt.Errorf("%w: file size changed while sampling %q", ErrSourceChanged, file.localPath)
		}
		completed += bytesRead
		for index := range layout {
			if layout[index].Path == file.relativePath &&
				(layout[index].Type == api.SourceEntryTypeFile || layout[index].Type == api.SourceEntryTypePlaylist) {
				layout[index].SampleSHA256 = digest
				break
			}
		}
		verified = append(verified, api.VerifiedSourceFile{
			LocalPath:    file.localPath,
			RelativePath: file.relativePath,
			Size:         file.size,
			SHA256:       digest,
		})
	}
	if err := VerifySourceManifestStability(ctx, manifest); err != nil {
		return api.SourceContentIdentity{}, err
	}

	payload, err := json.Marshal(sourceContentIdentityPayload{
		CanonicalPath:  canonicalSourceKey(manifest.SourcePath),
		Classification: manifest.Classification,
		Entries:        layout,
	})
	if err != nil {
		return api.SourceContentIdentity{}, fmt.Errorf("prepared release: marshal source identity: %w", err)
	}
	digest := sha256.Sum256(append([]byte(api.SourceContentIdentityVersion+"\x00"), payload...))
	return api.SourceContentIdentity{
		Version: api.SourceContentIdentityVersion,
		Digest:  hex.EncodeToString(digest[:]),
		Files:   verified,
	}, nil
}

// VerifySourceManifestStability rejects a source whose observed local state no
// longer matches the manifest used to begin an operation.
func VerifySourceManifestStability(ctx context.Context, manifest api.SourceManifest) error {
	if ctx == nil {
		return errors.New("prepared release: source stability context is required")
	}
	if strings.TrimSpace(manifest.SourcePath) == "" {
		return errors.New("prepared release: source identity manifest path is required")
	}
	layout, err := sourcelayout.Resolve(ctx, manifest.SourcePath)
	if err != nil {
		return fmt.Errorf("%w: resolve current source layout: %w", ErrSourceChanged, err)
	}
	current, _, err := inspectSource(ctx, api.PrepareInput{SourcePath: manifest.SourcePath}, layout)
	if err != nil {
		return fmt.Errorf("%w: inspect current source: %w", ErrSourceChanged, err)
	}
	if canonicalSourceKey(current.SourcePath) != canonicalSourceKey(manifest.SourcePath) ||
		!reflect.DeepEqual(stableSourceManifestEntries(current.Entries), stableSourceManifestEntries(manifest.Entries)) ||
		current.Classification != manifest.Classification {
		return fmt.Errorf("%w: manifest inventory differs", ErrSourceChanged)
	}
	return nil
}

func stableSourceManifestEntries(entries []api.SourceManifestEntry) []api.SourceManifestEntry {
	stable := append([]api.SourceManifestEntry(nil), entries...)
	for index := range stable {
		if stable[index].Type == api.SourceEntryTypeDirectory {
			stable[index].Size = 0
			stable[index].ModifiedAt = time.Time{}
		}
	}
	return stable
}

func validatedVerifiedSource(
	manifest api.SourceManifest,
	fingerprint string,
	verified *api.VerifiedInputSource,
) (api.SourceContentIdentity, error) {
	if verified == nil {
		return api.SourceContentIdentity{}, nil
	}
	identity := verified.Identity
	if identity.Version != api.SourceContentIdentityVersion || !validIdentityDigest(identity.Digest) {
		return api.SourceContentIdentity{}, errors.New("prepared release: verified source identity is invalid")
	}
	if identity.ManifestFingerprint != fingerprint || !sameVerifiedSourceManifest(verified.Manifest, manifest) {
		return api.SourceContentIdentity{}, fmt.Errorf("%w: verified source manifest differs", ErrSourceChanged)
	}
	if len(identity.Files) != 0 {
		expected, _, _, err := sourceIdentityManifest(manifest)
		if err != nil {
			return api.SourceContentIdentity{}, err
		}
		if len(identity.Files) != len(expected) {
			return api.SourceContentIdentity{}, fmt.Errorf("%w: verified source file inventory differs", ErrSourceChanged)
		}
		for index, file := range identity.Files {
			if !pathutil.SamePath(file.LocalPath, expected[index].localPath) || file.RelativePath != expected[index].relativePath ||
				file.Size != expected[index].size || !validIdentityDigest(file.SHA256) {
				return api.SourceContentIdentity{}, fmt.Errorf("%w: verified source file differs", ErrSourceChanged)
			}
		}
	}
	return identity, nil
}

func sameVerifiedSourceManifest(left api.SourceManifest, right api.SourceManifest) bool {
	return canonicalSourceKey(left.SourcePath) == canonicalSourceKey(right.SourcePath) &&
		left.Classification == right.Classification &&
		reflect.DeepEqual(stableSourceManifestEntries(left.Entries), stableSourceManifestEntries(right.Entries)) &&
		reflect.DeepEqual(left.SelectedPlaylists, right.SelectedPlaylists)
}

func validIdentityDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type sourceIdentityFile struct {
	localPath    string
	relativePath string
	size         int64
}

type sourceContentIdentityPayload struct {
	CanonicalPath  string                       `json:"canonicalPath"`
	Classification api.SourceClassification     `json:"classification"`
	Entries        []sourceContentIdentityEntry `json:"entries"`
}

type sourceContentIdentityEntry struct {
	Path         string              `json:"path"`
	Type         api.SourceEntryType `json:"type"`
	Size         int64               `json:"size"`
	SampleSHA256 string              `json:"sampleSha256,omitempty"`
	Disc         string              `json:"disc,omitempty"`
	DiscID       string              `json:"discID,omitempty"`
	Playlist     string              `json:"playlist,omitempty"`
}

func sourceIdentityManifest(manifest api.SourceManifest) ([]sourceIdentityFile, []sourceContentIdentityEntry, int64, error) {
	root, err := normalizeSourcePath(manifest.SourcePath)
	if err != nil {
		return nil, nil, 0, err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("prepared release: inspect source identity root: %w", err)
	}
	files := make([]sourceIdentityFile, 0, len(manifest.Entries))
	layout := make([]sourceContentIdentityEntry, 0, len(manifest.Entries))
	fileByPath := make(map[string]int, len(manifest.Entries))
	seenLayout := make(map[string]struct{}, len(manifest.Entries))
	var total int64
	for _, entry := range manifest.Entries {
		relativePath, err := sourceIdentityRelativePath(root, rootInfo.IsDir(), entry.Path)
		if err != nil {
			return nil, nil, 0, err
		}
		layoutKey := string(entry.Type) + "\x00" + relativePath
		if _, exists := seenLayout[layoutKey]; exists {
			return nil, nil, 0, fmt.Errorf("prepared release: duplicate source identity manifest entry %q", relativePath)
		}
		seenLayout[layoutKey] = struct{}{}
		identityEntry := sourceContentIdentityEntry{
			Path:     relativePath,
			Type:     entry.Type,
			Size:     entry.Size,
			Disc:     entry.Disc,
			DiscID:   entry.DiscID,
			Playlist: filepath.ToSlash(entry.Playlist),
		}
		layout = append(layout, identityEntry)
		if entry.Type != api.SourceEntryTypeFile && entry.Type != api.SourceEntryTypePlaylist {
			continue
		}
		if entry.Size < 0 {
			return nil, nil, 0, fmt.Errorf("prepared release: invalid source identity size for %q", entry.Path)
		}
		sampleBytes := min(entry.Size, api.SourceContentIdentitySampleBytes)
		if total > int64(^uint64(0)>>1)-sampleBytes {
			return nil, nil, 0, fmt.Errorf("prepared release: invalid source identity sample size for %q", entry.Path)
		}
		localPath := filepath.Clean(entry.Path)
		key := canonicalSourceKey(localPath)
		if _, exists := fileByPath[key]; exists {
			return nil, nil, 0, fmt.Errorf("prepared release: duplicate source identity file %q", localPath)
		}
		fileByPath[key] = len(files)
		files = append(files, sourceIdentityFile{
			localPath:    localPath,
			relativePath: relativePath,
			size:         entry.Size,
		})
		total += sampleBytes
	}
	sort.Slice(files, func(left, right int) bool {
		return files[left].relativePath < files[right].relativePath
	})
	sort.Slice(layout, func(left, right int) bool {
		if layout[left].Path == layout[right].Path {
			return layout[left].Type < layout[right].Type
		}
		return layout[left].Path < layout[right].Path
	})
	return files, layout, total, nil
}

func sourceIdentityRelativePath(root string, rootIsDir bool, value string) (string, error) {
	path := filepath.Clean(value)
	if !rootIsDir {
		if !pathutil.SamePath(root, path) {
			return "", fmt.Errorf("prepared release: source identity file %q differs from file source %q", path, root)
		}
		return "", nil
	}
	if !pathutil.SamePath(root, path) && !pathutil.IsWithinRoot(root, path) {
		return "", fmt.Errorf("prepared release: source identity entry %q escapes source root %q", path, root)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("prepared release: source identity relative path: %w", err)
	}
	if relative == "." {
		return "", nil
	}
	return filepath.ToSlash(relative), nil
}

func hashSourceFileSample(
	ctx context.Context,
	path string,
	sampleBytes int64,
	completed int64,
	total int64,
	report SourceIdentityProgressReporter,
) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("prepared release: open source identity file %q: %w", path, err)
	}
	defer file.Close()

	hash := sha256.New()
	buffer := make([]byte, 256*1024)
	var bytesRead int64
	for bytesRead < sampleBytes {
		if err := ctx.Err(); err != nil {
			return "", 0, fmt.Errorf("prepared release: source identity canceled: %w", err)
		}
		readSize := min(int64(len(buffer)), sampleBytes-bytesRead)
		count, readErr := file.Read(buffer[:int(readSize)])
		if count > 0 {
			if _, err := hash.Write(buffer[:count]); err != nil {
				return "", 0, fmt.Errorf("prepared release: hash source identity file: %w", err)
			}
			bytesRead += int64(count)
			if report != nil {
				report(SourceIdentityProgress{
					Path:           path,
					CompletedBytes: completed + bytesRead,
					TotalBytes:     total,
				})
			}
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return "", 0, fmt.Errorf("prepared release: read source identity file %q: %w", path, readErr)
	}
	return hex.EncodeToString(hash.Sum(nil)), bytesRead, nil
}
