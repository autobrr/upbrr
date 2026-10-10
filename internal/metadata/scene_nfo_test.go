// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
)

func TestFetchNFOContainment(t *testing.T) {
	t.Parallel()
	const content = "  SYNTHETIC NFO\r\n  Keep spacing.\r\n"
	for _, tc := range []struct {
		name, file, link string
		cached, reject   bool
	}{
		{
			name:   "flat cached",
			file:   "scene.nfo",
			cached: true,
		},
		{
			name:   "nested cached",
			file:   "nested/scene.nfo",
			cached: true,
		},
		{name: "nested downloaded", file: "nested/scene.nfo"},
		{
			name:   "backslash nested",
			file:   `nested\scene.nfo`,
			cached: true,
		},
		{
			name:   "inward file",
			file:   "link.nfo",
			link:   "file-in",
			cached: true,
		},
		{
			name:   "inward directory",
			file:   "link/scene.nfo",
			link:   "dir-in",
			cached: true,
		},
		{
			name:   "linked root",
			file:   "scene.nfo",
			link:   "root",
			cached: true,
		},
		{
			name:   "parent cached",
			file:   "../outside/scene.nfo",
			reject: true,
		},
		{
			name:   "parent write",
			file:   "../outside/new.nfo",
			reject: true,
		},
		{
			name:   "backslash traversal",
			file:   `..\outside\scene.nfo`,
			reject: true,
		},
		{
			name:   "absolute",
			file:   "/scene.nfo",
			reject: true,
		},
		{
			name:   "drive absolute",
			file:   `C:\scene.nfo`,
			reject: true,
		},
		{
			name:   "drive relative",
			file:   "C:scene.nfo",
			reject: true,
		},
		{
			name:   "network",
			file:   `\\server\share\scene.nfo`,
			reject: true,
		},
		{
			name:   "outward file",
			file:   "link.nfo",
			link:   "file-out",
			reject: true,
		},
		{
			name:   "outward directory",
			file:   "link/scene.nfo",
			link:   "dir-out",
			reject: true,
		},
		{
			name:   "dangling file",
			file:   "link.nfo",
			link:   "file-dangling",
			reject: true,
		},
		{
			name:   "dangling directory",
			file:   "link/scene.nfo",
			link:   "dir-dangling",
			reject: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "nfo")
			outside := filepath.Join(parent, "outside")
			for _, dir := range []string{root, outside, filepath.Join(root, "nested")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			outsideFile := filepath.Join(outside, "scene.nfo")
			if err := os.WriteFile(outsideFile, []byte("outside content"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.cached {
				name := strings.ReplaceAll(tc.file, "\\", "/")
				if tc.link == "file-in" || tc.link == "dir-in" {
					name = "nested/scene.nfo"
				}
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.link != "" {
				link := filepath.Join(root, "link")
				target := filepath.Join(root, "nested")
				switch tc.link {
				case "file-in":
					link, target = filepath.Join(root, "link.nfo"), filepath.Join(target, "scene.nfo")
				case "file-out":
					link, target = filepath.Join(root, "link.nfo"), outsideFile
				case "dir-out":
					target = outside
				case "file-dangling":
					link, target = filepath.Join(root, "link.nfo"), filepath.Join(outside, "new.nfo")
				case "dir-dangling":
					target = filepath.Join(outside, "missing")
				case "root":
					link, target = filepath.Join(parent, "nfo-alias"), root
				}
				if err := os.Symlink(target, link); err != nil {
					if runtime.GOOS != "windows" || strings.HasPrefix(tc.link, "file-") {
						t.Skipf("symlink unavailable: %v", err)
					}
					output, junctionErr := exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
					if junctionErr != nil {
						t.Fatalf("create junction: %v: %s", junctionErr, output)
					}
				}
				t.Cleanup(func() { _ = os.Remove(link) })
				if tc.link == "root" {
					root = link
				}
			}
			downloads := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body := content
				if strings.Contains(req.URL.Path, "/v1/details/") {
					payload, err := json.Marshal(map[string][]map[string]string{"files": {{"name": tc.file}}})
					if err != nil {
						t.Fatal(err)
					}
					body = string(payload)
				} else {
					downloads++
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			})}
			detector := newSRRDBDetector(client, "https://scene.invalid", t.TempDir(), root)
			path, downloaded, err := detector.fetchNFO(t.Context(), "Example.Release.2026.1080p-WEB")
			if tc.reject {
				if err == nil || path != "" || downloaded || downloads != 0 {
					t.Fatalf("invalid resource selected: path=%q downloaded=%t requests=%d err=%v", path, downloaded, downloads, err)
				}
			} else {
				if err != nil || downloaded == tc.cached {
					t.Fatalf("downloaded=%t cached=%t err=%v", downloaded, tc.cached, err)
				}
				got, readErr := os.ReadFile(path)
				if readErr != nil || string(got) != content {
					t.Fatalf("content=%q err=%v", got, readErr)
				}
			}
			got, err := os.ReadFile(outsideFile)
			if err != nil || string(got) != "outside content" {
				t.Fatal("outside cached content changed")
			}
			if _, err := os.Stat(filepath.Join(outside, "new.nfo")); !os.IsNotExist(err) {
				t.Fatal("outside file created")
			}
		})
	}
}

func TestNFOContainmentPreservesSceneIdentification(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "nfo")
	outside := filepath.Join(parent, "outside.nfo")
	if err := os.WriteFile(outside, []byte("https://www.themoviedb.org/movie/999999"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"resultsCount":1,"results":[{"release":"Example.Release.2026.1080p-WEB","imdbId":"1234567","hasNFO":"yes"}]}`
		if strings.Contains(req.URL.Path, "/v1/details/") {
			body = `{"files":[{"name":"../outside.nfo"}]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	detector := newSRRDBDetector(client, "https://scene.invalid", t.TempDir(), root)
	result, err := detector.Detect(t.Context(), preparationstate.State{VideoPath: filepath.Join(parent, "Example.Release.mkv")})
	if !isSceneNFOError(err) || !result.IsScene || result.IMDBID != 1234567 || result.NFOPath != "" || result.TMDBID != 0 {
		t.Fatalf("scene identification changed: result=%+v err=%v", result, err)
	}
	if _, err := parseNFOExternalIDs(root, outside); err == nil {
		t.Fatal("outside NFO parsed")
	}
}
