// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestVerifyInputSourceRehashesSameStatReplacement(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSourceIdentityFile(t, source, "first")
	modified := time.Date(2026, time.September, 19, 1, 2, 3, 0, time.UTC)
	if err := os.Chtimes(source, modified, modified); err != nil {
		t.Fatalf("set original times: %v", err)
	}
	first, err := VerifyInputSource(context.Background(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify first source: %v", err)
	}

	writeSourceIdentityFile(t, source, "other")
	if err := os.Chtimes(source, modified, modified); err != nil {
		t.Fatalf("restore replacement times: %v", err)
	}
	second, err := VerifyInputSource(context.Background(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify replaced source: %v", err)
	}
	if first.Identity.Digest == second.Identity.Digest || len(second.Identity.Files) != 1 || second.Identity.Files[0].Size != int64(len("other")) {
		t.Fatalf("source identities = %#v and %#v", first, second)
	}

	var progress []SourceIdentityProgress
	_, err = VerifySourceContentIdentity(context.Background(), second.Manifest, func(update SourceIdentityProgress) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf("verify with progress: %v", err)
	}
	if len(progress) == 0 || progress[len(progress)-1].CompletedBytes != int64(len("other")) ||
		progress[len(progress)-1].TotalBytes != int64(len("other")) {
		t.Fatalf("verification progress = %#v", progress)
	}
}

func TestVerifyInputSourceSamplesLargeFiles(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	prefix := bytes.Repeat([]byte("sample"), int(api.SourceContentIdentitySampleBytes/6))
	prefix = append(prefix, bytes.Repeat([]byte("x"), int(api.SourceContentIdentitySampleBytes)-len(prefix))...)
	firstContents := append(append([]byte(nil), prefix...), []byte("first")...)
	writeSourceIdentityBytes(t, source, firstContents)
	first, err := VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify first source: %v", err)
	}
	if len(first.Identity.Files) != 1 {
		t.Fatalf("sampled files = %#v", first.Identity.Files)
	}
	wantSample := sha256.Sum256(prefix)
	if got := first.Identity.Files[0].SHA256; got != hex.EncodeToString(wantSample[:]) {
		t.Fatalf("sample digest = %q, want prefix digest", got)
	}

	var progress []SourceIdentityProgress
	_, err = VerifySourceContentIdentity(t.Context(), first.Manifest, func(update SourceIdentityProgress) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf("verify sampled source with progress: %v", err)
	}
	if len(progress) == 0 || progress[len(progress)-1].CompletedBytes != api.SourceContentIdentitySampleBytes ||
		progress[len(progress)-1].TotalBytes != api.SourceContentIdentitySampleBytes {
		t.Fatalf("sample progress = %#v", progress)
	}
	for _, update := range progress {
		if update.CompletedBytes > api.SourceContentIdentitySampleBytes || update.TotalBytes != api.SourceContentIdentitySampleBytes {
			t.Fatalf("unbounded sample progress = %#v", update)
		}
	}

	secondContents := append(append([]byte(nil), prefix...), []byte("other")...)
	writeSourceIdentityBytes(t, source, secondContents)
	second, err := VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify changed tail: %v", err)
	}
	if second.Identity.Digest != first.Identity.Digest || second.Identity.Files[0].SHA256 != first.Identity.Files[0].SHA256 {
		t.Fatalf("tail outside sample changed identity: first=%#v second=%#v", first.Identity, second.Identity)
	}
}

func TestVerifyInputSourceBindsCanonicalPath(t *testing.T) {
	t.Parallel()

	firstPath := filepath.Join(t.TempDir(), "first.mkv")
	secondPath := filepath.Join(t.TempDir(), "second.mkv")
	writeSourceIdentityFile(t, firstPath, "same-content")
	writeSourceIdentityFile(t, secondPath, "same-content")
	first, err := VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: firstPath})
	if err != nil {
		t.Fatalf("verify first source: %v", err)
	}
	second, err := VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: secondPath})
	if err != nil {
		t.Fatalf("verify second source: %v", err)
	}
	if first.Identity.Digest == second.Identity.Digest {
		t.Fatalf("canonical paths produced the same source version: first=%#v second=%#v", first.Identity, second.Identity)
	}
}

func TestVerifySourceManifestStabilityRejectsSameSizeModifiedFile(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSourceIdentityFile(t, source, "first")
	verified, err := VerifyInputSource(context.Background(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify source: %v", err)
	}
	writeSourceIdentityFile(t, source, "other")
	modified := verified.Manifest.Entries[0].ModifiedAt.Add(time.Hour)
	if err := os.Chtimes(source, modified, modified); err != nil {
		t.Fatalf("set changed time: %v", err)
	}
	if err := VerifySourceManifestStability(context.Background(), verified.Manifest); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("stability error = %v, want source changed", err)
	}
}

func TestVerifyInputSourceHonorsCancellation(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSourceIdentityFile(t, source, "content")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := VerifyInputSource(ctx, api.PrepareInput{SourcePath: source})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("verify canceled source error = %v", err)
	}
}

func TestPrepareAttachesVerifiedSourceIdentity(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSourceIdentityFile(t, source, "content")
	verified, err := VerifyInputSource(context.Background(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify source: %v", err)
	}
	module := newTestModule(t, newMemoryStore(), &recordingCollector{})
	prepared, err := module.Prepare(context.Background(), api.PrepareInput{SourcePath: source, VerifiedSource: &verified})
	if err != nil {
		t.Fatalf("prepare verified source: %v", err)
	}
	if prepared.Release.SourceIdentity.Version != verified.Identity.Version ||
		prepared.Release.SourceIdentity.Digest != verified.Identity.Digest ||
		prepared.Release.SourceIdentity.ManifestFingerprint != verified.Identity.ManifestFingerprint ||
		len(prepared.Release.SourceIdentity.Files) != 1 {
		t.Fatalf("prepared source identity = %#v", prepared.Release.SourceIdentity)
	}
}

func TestResolveUploadSubjectRetainsVerifiedSourceIdentity(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSourceIdentityFile(t, source, "content")
	verified, err := VerifyInputSource(context.Background(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify source: %v", err)
	}
	module := newTestModule(t, newMemoryStore(), &recordingCollector{})
	prepared, err := module.Prepare(context.Background(), api.PrepareInput{SourcePath: source, VerifiedSource: &verified})
	if err != nil {
		t.Fatalf("prepare source: %v", err)
	}
	subject, err := module.ResolveUploadSubject(context.Background(), api.UploadSubjectInput{
		Release: api.ReleaseRef{SourcePath: source, Generation: prepared.Release.Generation},
	})
	if err != nil {
		t.Fatalf("resolve upload subject: %v", err)
	}
	if subject.SourceIdentity.Digest != verified.Identity.Digest || len(subject.SourceIdentity.Files) != len(verified.Identity.Files) {
		t.Fatalf("upload source identity = %#v", subject.SourceIdentity)
	}
}

func TestPrepareRejectsStaleVerifiedSource(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSourceIdentityFile(t, source, "content")
	verified, err := VerifyInputSource(context.Background(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify source: %v", err)
	}
	writeSourceIdentityFile(t, source, "changed")
	module := newTestModule(t, newMemoryStore(), &recordingCollector{})
	_, err = module.Prepare(context.Background(), api.PrepareInput{SourcePath: source, VerifiedSource: &verified})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("prepare stale verified source error = %v", err)
	}
}

func TestPrepareRejectsLegacyCompleteSourceIdentity(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSourceIdentityFile(t, source, "content")
	verified, err := VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify source: %v", err)
	}
	verified.Identity.Version = "source-content-v1"
	module := newTestModule(t, newMemoryStore(), &recordingCollector{})
	_, err = module.Prepare(t.Context(), api.PrepareInput{SourcePath: source, VerifiedSource: &verified})
	if err == nil || !strings.Contains(err.Error(), "verified source identity is invalid") {
		t.Fatalf("legacy source identity error = %v", err)
	}
}

func TestRestartHydrationRestoresPrivateVerifiedIdentity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSourceIdentityFile(t, source, "content")
	verified, err := VerifyInputSource(ctx, api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore()
	initial := newTestModule(t, store, &recordingCollector{})
	prepared, err := initial.Prepare(ctx, api.PrepareInput{SourcePath: source, VerifiedSource: &verified})
	if err != nil {
		t.Fatal(err)
	}
	// SQLite stores the public prepared row, which intentionally excludes the
	// private identity retained separately by the active-input repository.
	payload, err := json.Marshal(prepared.Release)
	if err != nil {
		t.Fatal(err)
	}
	var persisted api.PreparedRelease
	if err := json.Unmarshal(payload, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.SourceIdentity.Version != "" {
		t.Fatal("public prepared row leaked private verification")
	}
	store.current[canonicalSourceKey(source)] = persisted
	collector := newClientEvidenceTestCollector(clientEvidenceTestSnapshot("hydrated-hash"))
	restarted := newTestModule(t, store, collector)
	reused, err := restarted.Prepare(ctx, api.PrepareInput{
		SourcePath:      source,
		VerifiedSource:  &verified,
		RequirePrepared: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := restarted.ResolveUploadSubject(ctx, api.UploadSubjectInput{
		Release: api.ReleaseRef{SourcePath: source, Generation: reused.Release.Generation},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reused.Release.Generation != prepared.Release.Generation || upload.SourceIdentity.Digest != verified.Identity.Digest ||
		len(upload.SourceIdentity.Files) != 1 || upload.SourceManifest.SourcePath != source ||
		collector.collectCount() != 0 || collector.hydrateCount() != 1 {
		t.Fatal("restart did not restore the same verified generation without collection")
	}
}

func writeSourceIdentityFile(t *testing.T, path string, content string) {
	t.Helper()
	writeSourceIdentityBytes(t, path, []byte(content))
}

func writeSourceIdentityBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write source identity file: %v", err)
	}
}
