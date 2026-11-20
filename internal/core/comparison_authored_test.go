// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This opt-in suite authors actual playable structures with independent tools.
// Setting the flag makes absent authoring prerequisites a failure, not a skip.
func TestComparisonAuthoredDiscFastAcceptance(t *testing.T) {
	if os.Getenv("UPBRR_TEST_DISC_AUTHORING") != "1" {
		t.Skip("requires FFmpeg, dvdauthor 0.7.2 and tsMuxeR 2.7.0; set UPBRR_TEST_DISC_AUTHORING=1")
	}
	tool := func(variable, name string) string {
		t.Helper()
		path := os.Getenv(variable)
		if path == "" {
			var err error
			path, err = exec.LookPath(name)
			if err != nil {
				t.Fatalf("required disc authoring tool %s: %v", name, err)
			}
		}
		return path
	}
	ffmpeg := tool("UPBRR_TEST_FFMPEG", "ffmpeg")
	dvdauthor := tool("UPBRR_TEST_DVDAUTHOR", "dvdauthor")
	tsmuxer := tool("UPBRR_TEST_TSMUXER", "tsMuxeR")
	run := func(executable string, args ...string) {
		t.Helper()
		if output, err := exec.CommandContext(t.Context(), executable, args...).CombinedOutput(); err != nil {
			t.Fatalf("author native comparison fixture: %v\n%s", err, output)
		}
	}
	root := t.TempDir()
	path := func(name string) string { return filepath.Join(root, name) }
	// Unique deterministic spatial patterns also identify individual native
	// frames, rather than proving only that two periodic test cards look alike.
	pattern := make([]byte, 2250*256)
	random := rand.New(rand.NewPCG(73, 19))
	for i := range pattern {
		pattern[i] = byte(random.Uint32())
	}
	if err := os.WriteFile(path("pattern.gray"), pattern, 0o600); err != nil {
		t.Fatal(err)
	}
	run(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "rawvideo", "-pixel_format", "gray", "-video_size", "16x16", "-framerate", "25", "-i", path("pattern.gray"),
		"-vf", "scale=720:576:flags=neighbor,setsar=64/45,setfield=tff", "-target", "pal-dvd", "-aspect", "16:9", "-q:v", "2", "-g", "12", "-bf", "2", "-flags", "+cgop+ilme+ildct", "-sc_threshold", "1000000000",
		"-color_primaries", "bt470bg", "-color_trc", "gamma28", "-colorspace", "bt470bg", "-color_range", "tv", "-an", path("source.mpg"))
	xml := fmt.Sprintf(`<dvdauthor dest="%s"><vmgm><menus><video format="pal"/></menus></vmgm><titleset><titles><video format="pal" aspect="16:9"/><pgc><vob file="%s" chapters="0,30,60"/></pgc></titles></titleset></dvdauthor>`, filepath.ToSlash(path("dvd")), filepath.ToSlash(path("source.mpg")))
	if err := os.WriteFile(path("dvd.xml"), []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	run(dvdauthor, "-x", path("dvd.xml"))
	run(ffmpeg, "-hide_banner", "-loglevel", "error", "-i", path("source.mpg"), "-vf", "setpts=N*1001/(24000*TB),scale=1280:720,setsar=1", "-r", "24000/1001", "-c:v", "ffv1", "-an", path("main.mkv"))
	run(ffmpeg, "-hide_banner", "-loglevel", "error", "-i", path("main.mkv"), "-c:v", "libx264", "-preset", "fast", "-crf", "12", "-g", "24", "-bf", "3", "-x264-params", "repeat-headers=1:open-gop=0:scenecut=0", "-an", path("source.h264"))
	meta := fmt.Sprintf("MUXOPT --blu-ray --vbr\nV_MPEG4/ISO/AVC, %q, fps=23.976, insertSEI, contSPS\n", filepath.ToSlash(path("source.h264")))
	if err := os.WriteFile(path("bluray.meta"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	run(tsmuxer, path("bluray.meta"), path("bluray"))
	for _, variable := range []string{"UPBRR_COMPARISON_PAIR", "UPBRR_COMPARISON_DVD_PROGRAMME", "UPBRR_COMPARISON_BLURAY_PROGRAMME"} {
		t.Setenv(variable, "")
	}
	assertDiscComparisonFastAcceptance(t, path("main.mkv"), path("dvd"), path("bluray"), map[string]string{
		"main":   path("main.mkv"),
		"dvd":    path("source.mpg"),
		"bluray": path("source.h264"),
	})
	assertAuthoredRepeatedPlaylist(t, root)
	assertAuthoredDVDClockWrap(t, root)
}
