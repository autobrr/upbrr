// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"

	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestStandaloneHDRPlaylistUsesDefaultAngle(t *testing.T) {
	item := bd.PlayItem{Angles: []bd.Angle{{
		Source: video.Source{Kind: "m2ts", Path: "00001.M2TS"},
		STC:    &video.STCSequence{},
		Video: []video.Mapping{{
			Role:      video.Primary,
			Codec:     video.HEVC,
			EntryType: 1,
		}},
	}, {Err: errors.New("unsupported alternate")}}}
	result := bd.Result{Timelines: []bd.PlaylistTimeline{{
		Name:     "00001.MPLS",
		Complete: true,
		Items:    []bd.PlayItem{item},
	}}}
	if got, err := standaloneHDRPlaylist(result, "00001.MPLS"); err != nil || got != "00001.MPLS" {
		t.Fatalf("default angle playlist = %q, %v", got, err)
	}
	result.Timelines[0].Items[0].Angles = nil
	if _, err := standaloneHDRPlaylist(result, "00001.MPLS"); err == nil {
		t.Fatal("playlist without a default angle was accepted")
	}
}

func TestHDRCLIRejectsDependentAndConflictingFlags(t *testing.T) {
	cases := []struct {
		opts    cliOptions
		visited map[string]bool
	}{
		{cliOptions{}, map[string]bool{"hdr-peak-source": true}},
		{cliOptions{HDRAnalysis: true, HDRAnalysisOnly: true}, nil},
		{cliOptions{HDRAnalysis: true}, map[string]bool{"hdr-playlist": true}},
		{cliOptions{HDRAnalysisOnly: true, HDROutputDir: "output"}, map[string]bool{"hdr-targets": true}},
		{cliOptions{HDRAnalysisOnly: true, HDROutputDir: "output"}, map[string]bool{"debug": true}},
		{cliOptions{HDRAnalysisOnly: true}, nil},
		{cliOptions{HDRAnalysis: true, HDRPeakSource: "unknown"}, nil},
	}
	for _, test := range cases {
		if err := validateCLIHDR(test.opts, test.visited); err == nil {
			t.Fatalf("invalid flags accepted: %#v", test)
		}
	}
	if err := validateCLIHDR(
		cliOptions{HDRAnalysisOnly: true, HDROutputDir: "output"},
		map[string]bool{"hdr-analysis-only": true, "unattended": true},
	); err != nil {
		t.Fatal(err)
	}
}

func TestHDROutputContainmentBeforeCreation(t *testing.T) {
	source := t.TempDir()
	for _, output := range []string{source, filepath.Join(source, "new", "nested")} {
		if _, err := hdrOutputParent(source, output, true); err == nil {
			t.Fatalf("source output %q accepted", output)
		}
	}
	if _, err := os.Stat(filepath.Join(source, "new")); !os.IsNotExist(err) {
		t.Fatal("validation created an output directory")
	}
	external := filepath.Join(t.TempDir(), "new", "nested")
	if _, err := hdrOutputParent(source, external, true); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(source, alias); err == nil {
		if _, err := hdrOutputParent(source, filepath.Join(alias, "output"), true); err == nil {
			t.Fatal("source alias output accepted")
		}
	}
}

func TestHDRCLISetupDiagnostics(t *testing.T) {
	source := filepath.Join("testdata", "synthetic-cli-hdr.mkv")
	outputFile := filepath.Join(t.TempDir(), "output-file")
	if err := os.WriteFile(outputFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	for _, test := range []struct {
		name   string
		source string
		output string
		flags  []string
		code   int
		want   string
	}{
		{
name: "missing source",
 source: missing,
 output: t.TempDir(),
 code: 1,
 want: "HDR source",
},
		{
name: "output is a file",
 source: source,
 output: outputFile,
 code: 1,
 want: "--hdr-output-dir",
},
		{
name: "output below a file",
 source: source,
 output: filepath.Join(outputFile, "reports"),
 code: 1,
 want: "--hdr-output-dir",
},
		{
name: "invalid peak option",
 source: source,
 output: t.TempDir(),
 flags: []string{"--hdr-peak-source=unknown"},
 code: 2,
 want: "invalid --hdr-peak-source; choose histogram, histogram99, max-scl, or max-scl-luminance",
},
		{
name: "invalid playlist option",
 source: source,
 output: t.TempDir(),
 flags: []string{"--hdr-playlist=unknown"},
 code: 2,
 want: "--hdr-playlist",
},
		{
name: "invalid formats option",
 source: source,
 output: t.TempDir(),
 flags: []string{"--hdr-formats=unknown"},
 code: 2,
 want: "invalid --hdr-formats; choose auto, hdr10plus, dolby-vision, or both",
},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"--hdr-analysis-only", "--hdr-metadata-only", "--hdr-output-dir", test.output}
			args = append(args, test.flags...)
			result := executeCLIForTest(t.Context(), t, append(args, test.source))
			if result.code != test.code || !strings.Contains(result.stderr, test.want) || result.stdout != "" {
				t.Fatalf("setup result: code=%d stdout=%q stderr=%q", result.code, result.stdout, result.stderr)
			}
			if test.source == missing {
				var pathErr *os.PathError
				if !errors.Is(result.err, os.ErrNotExist) || !errors.As(result.err, &pathErr) {
					t.Fatal("source diagnostic lost its filesystem cause")
				}
			}
		})
	}
}

func TestHDRCLIUnreadableSourceDiagnostic(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(source, 0o600) })
	if file, err := os.Open(source); err == nil {
		_ = file.Close()
		t.Skip("filesystem permits reading a file without read permission")
	}
	output := t.TempDir()
	result := executeCLIForTest(t.Context(), t, []string{
		"--hdr-analysis-only", "--hdr-metadata-only", "--hdr-output-dir", output, source,
	})
	failure, typed := api.AsHDRAnalysisFailure(result.err)
	entries, err := os.ReadDir(output)
	if result.code != 1 || !errors.Is(result.err, os.ErrPermission) || !typed || failure.Code != api.HDRAnalysisFailureRead ||
		!strings.Contains(result.stderr, "read HDR source") || result.stdout != "" || err != nil || len(entries) != 0 {
		t.Fatalf("unreadable source result: code=%d stderr=%q typed=%t failure=%s entries=%v err=%v",
			result.code, result.stderr, typed, failure.Code, entries, err)
	}
}

func TestHDRCLISetupDiagnosticSafetyAndModes(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.password=SECRET_SENTINEL\x1b[2J.mkv")
	for _, flags := range [][]string{{"--ui=plain"}, {"--ui=tui"}, {"--ua"}, {"--uac"}} {
		args := []string{"--hdr-analysis-only", "--hdr-metadata-only", "--hdr-output-dir", t.TempDir()}
		args = append(args, flags...)
		result := executeCLIForTest(t.Context(), t, append(args, source))
		_, pathFailure := errors.AsType[*os.PathError](result.err)
		if result.code != 1 || !pathFailure || !strings.Contains(result.stderr, "HDR source") || result.stdout != "" {
			t.Fatal("standalone HDR setup did not report the source failure through the command")
		}
		if strings.Contains(result.stderr, "SECRET_SENTINEL") || strings.ContainsAny(result.stderr, "\x1b\a\r") {
			t.Fatal("standalone HDR error exposed a secret or terminal control")
		}
	}
}

func TestHDRCLIAutomaticOutputFailure(t *testing.T) {
	if _, err := hdranalysis.CurrentDecoderFingerprint(t.Context()); err != nil {
		t.Skip("requires a working FFmpeg version probe")
	}
	parent := t.TempDir()
	blocked := false
	var stdout, stderr bytes.Buffer
	writer := hdrCLIProgressWriter(func(data []byte) (int, error) {
		if !blocked && strings.Contains(string(data), "HDR analysis: inspecting:") {
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 1 {
				t.Fatal("expected one empty HDR output attempt")
			}
			directory := filepath.Join(parent, entries[0].Name())
			if err := os.Remove(directory); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(directory, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			blocked = true
		}
		return stderr.Write(data)
	})
	err := executeCLI(t.Context(), []string{
		"--hdr-analysis-only", "--hdr-output-dir", parent,
		filepath.Join("..", "..", "internal", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"),
	}, cliIO{out: &stdout, errOut: writer})
	printCLIError(&stderr, err)
	failure, typed := api.AsHDRAnalysisFailure(err)
	_, filesystemCause := errors.AsType[*os.PathError](err)
	entries, readErr := os.ReadDir(parent)
	if !blocked || !typed || failure.Code != api.HDRAnalysisFailureOutput || !filesystemCause ||
		stdout.Len() != 0 || readErr != nil || len(entries) != 0 {
		t.Fatalf("output failure: blocked=%t typed=%t code=%s filesystem=%t stdout=%q entries=%v read=%v stderr=%q",
			blocked, typed, failure.Code, filesystemCause, stdout.String(), entries, readErr, stderr.String())
	}
	if !strings.Contains(stderr.String(), "prepare HDR analysis output") || strings.Contains(stderr.String(), "read HDR source") {
		t.Fatalf("output failure was reported as source reading: %q", stderr.String())
	}
	if exitErr, ok := errors.AsType[*cliExitError](err); ok && exitErr.code != 1 {
		t.Fatalf("output failure exit status = %d", exitErr.code)
	}
}

type hdrCLIProgressWriter func([]byte) (int, error)

func (w hdrCLIProgressWriter) Write(data []byte) (int, error) { return w(data) }

func TestHDRCLIRetainsCompletedImagesOnReportFailure(t *testing.T) {
	for _, test := range []struct {
		name        string
		failSummary bool
		alias       bool
		prepared    bool
	}{
		{name: "complete"},
		{name: "prepared complete", prepared: true},
		{name: "summary failure", failSummary: true},
		{
			name:        "prepared summary failure",
			failSummary: true,
			prepared:    true,
		},
		{
			name:        "summary failure through alias",
			failSummary: true,
			alias:       true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			if test.alias {
				alias := filepath.Join(t.TempDir(), "output-alias")
				if err := os.Symlink(parent, alias); err != nil {
					t.Skipf("output-directory alias unavailable: %v", err)
				}
				parent = alias
			}
			var stdout bytes.Buffer
			var diagnostic bytes.Buffer
			var stderr io.Writer = &diagnostic
			blockedSummary := false
			if test.failSummary {
				stderr = hdrCLIProgressWriter(func(data []byte) (int, error) {
					if strings.Contains(string(data), "HDR analysis: saving") {
						entries, err := os.ReadDir(parent)
						if err != nil || len(entries) != 1 {
							t.Fatalf("active attempt directory: entries=%v err=%v", entries, err)
						}
						if err := os.Mkdir(filepath.Join(parent, entries[0].Name(), "summary.json"), 0o700); err != nil {
							t.Fatal(err)
						}
						blockedSummary = true
					}
					return diagnostic.Write(data)
				})
			}
			args := []string{"--hdr-analysis-only", "--hdr-output-dir", parent}
			if test.prepared {
				args = append(args, "--hdr-audio-only", "--hdr-all-audio-tracks")
			} else {
				args = append(args, "--hdr-metadata-only")
			}
			err := executeCLI(t.Context(), append(args, filepath.Join("testdata", "synthetic-cli-hdr.mkv")), cliIO{out: &stdout, errOut: stderr})
			printCLIError(&diagnostic, err)
			entries, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if test.failSummary {
				if exitErr, ok := errors.AsType[*cliExitError](err); ok && exitErr.code != 1 {
					t.Fatalf("summary failure exit status = %d", exitErr.code)
				}
				if !blockedSummary || !errors.Is(err, os.ErrExist) || len(entries) != 1 || stdout.Len() == 0 {
					t.Fatalf("failed summary lost completed output: blocked=%t err=%v entries=%v stdout=%q", blockedSummary, err, entries, stdout.String())
				}
				if image, imageErr := os.Stat(filepath.Join(parent, entries[0].Name(), "hdr10plus.png")); imageErr != nil || image.Size() == 0 {
					t.Fatalf("completed image missing: %v", imageErr)
				}
				failure, typed := api.AsHDRAnalysisFailure(err)
				if !typed || failure.Code != api.HDRAnalysisFailureRead || !strings.Contains(diagnostic.String(), "save HDR summary") ||
					!strings.Contains(diagnostic.String(), "retained") {
					t.Fatalf("summary failure lost its operation or retained-output guidance: %q", diagnostic.String())
				}
				resolvedParent, resolveErr := filepath.EvalSymlinks(parent)
				if resolveErr != nil {
					t.Fatal(resolveErr)
				}
				if want := "HDR analysis output: " + filepath.Join(resolvedParent, entries[0].Name()) + "\n"; stdout.String() != want {
					t.Fatalf("artifact-location stdout changed: %q", stdout.String())
				}
				return
			}
			if err != nil || len(entries) != 1 || !strings.Contains(stdout.String(), "HDR analysis output:") {
				t.Fatalf("complete output: err=%v entries=%v stdout=%q", err, entries, stdout.String())
			}
			for _, artifact := range []string{"hdr10plus.png", "summary.json"} {
				info, err := os.Stat(filepath.Join(parent, entries[0].Name(), artifact))
				if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
					t.Fatalf("complete %s: info=%v err=%v", artifact, info, err)
				}
			}
		})
	}
}

func TestHDRCLIRenderCancellationRetainsMetadataReport(t *testing.T) {
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout bytes.Buffer
	var diagnostic bytes.Buffer
	stderr := hdrCLIProgressWriter(func(data []byte) (int, error) {
		if strings.Contains(string(data), "rendering") {
			cancel()
		}
		return diagnostic.Write(data)
	})
	err := executeCLI(ctx, []string{
		"--hdr-analysis-only", "--hdr-output-dir", parent, filepath.Join("testdata", "synthetic-cli-hdr.mkv"),
	}, cliIO{out: &stdout, errOut: stderr})
	printCLIError(&diagnostic, err)
	entries, readErr := os.ReadDir(parent)
	if !errors.Is(err, context.Canceled) || readErr != nil || len(entries) != 1 || stdout.Len() == 0 {
		t.Fatalf("canceled evidence: err=%v entries=%v read=%v stdout=%q", err, entries, readErr, stdout.String())
	}
	failure, typed := api.AsHDRAnalysisFailure(err)
	if !typed || failure.Code != api.HDRAnalysisFailureCanceled || !strings.Contains(diagnostic.String(), "retained") {
		t.Fatalf("cancellation lost its typed failure or retained-output guidance: %q", diagnostic.String())
	}
	report, err := os.ReadFile(filepath.Join(parent, entries[0].Name(), "summary.json"))
	if err != nil || !bytes.Contains(report, []byte("render_canceled")) || !bytes.Contains(report, []byte(`"state":"complete"`)) ||
		!bytes.Contains(report, []byte(`"status":"canceled"`)) {
		t.Fatalf("canceled render lost native observations: report=%s err=%v", report, err)
	}
}
