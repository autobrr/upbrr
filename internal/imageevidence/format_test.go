// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imageevidence

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifiedFormatRequiresCompleteBoundedImage(t *testing.T) {
	var pngBytes, jpegBytes bytes.Buffer
	source := image.NewRGBA(image.Rect(0, 0, 19, 11))
	if err := png.Encode(&pngBytes, source); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegBytes, source, nil); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		payload []byte
		want    string
	}{
		{"PNG", pngBytes.Bytes(), "png"},
		{"JPEG", jpegBytes.Bytes(), "jpeg"},
		{"PNG header only", pngBytes.Bytes()[:33], ""},
		{"PNG missing end", pngBytes.Bytes()[:pngBytes.Len()-12], ""},
		{"unknown", []byte("not an image"), ""},
		{"over pixel budget", formatTestPNGHeader(pngBytes.Bytes(), 8192, 4321), ""},
		{"huge dimensions", formatTestPNGHeader(pngBytes.Bytes(), 2147483647, 2147483647), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "synthetic.jpg")
			if err := os.WriteFile(file, test.payload, 0o600); err != nil {
				t.Fatal(err)
			}
			if got := VerifiedFormat(file); got != test.want {
				t.Fatalf("format = %q, want %q", got, test.want)
			}
		})
	}
}

func formatTestPNGHeader(payload []byte, width, height uint32) []byte {
	header := bytes.Clone(payload[:33])
	binary.BigEndian.PutUint32(header[16:20], width)
	binary.BigEndian.PutUint32(header[20:24], height)
	binary.BigEndian.PutUint32(header[29:33], crc32.ChecksumIEEE(header[12:29]))
	return header
}

func TestVerifiedFormatByteAndPixelBounds(t *testing.T) {
	for _, test := range []struct {
		name          string
		width, height int
		want          bool
	}{
		{"landscape boundary", 8192, 4320, true},
		{"portrait boundary", 4320, 8192, true},
		{"ultrawide boundary", 16384, 2160, true},
		{"over budget", 8192, 4321, false},
		{"overflow", int(^uint(0) >> 1), int(^uint(0) >> 1), false},
		{"zero", 0, 1, false},
		{"negative", 1, -1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := withinFormatPixelBudget(test.width, test.height); got != test.want {
				t.Fatalf("budget accepted=%t, want %t", got, test.want)
			}
		})
	}
	file := filepath.Join(t.TempDir(), "large.png")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(formatEvidenceMaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := VerifiedFormat(file); got != "" {
		t.Fatalf("oversized image has format %q", got)
	}
	if got := VerifiedFormat(t.TempDir()); got != "" {
		t.Fatalf("directory has format %q", got)
	}
	if got := VerifiedFormat(file + ".missing"); got != "" {
		t.Fatalf("missing file has format %q", got)
	}
}

var (
	formatTestRegister sync.Once
	formatTestActive   atomic.Int32
	formatTestMaximum  atomic.Int32
	formatTestCalls    atomic.Int32
)

func TestVerifiedFormatSerializesDecodes(t *testing.T) {
	formatTestRegister.Do(func() {
		image.RegisterFormat("serialized-evidence-test", "evidence-format-test", func(io.Reader) (image.Image, error) {
			active := formatTestActive.Add(1)
			defer formatTestActive.Add(-1)
			for old := formatTestMaximum.Load(); active > old; old = formatTestMaximum.Load() {
				if formatTestMaximum.CompareAndSwap(old, active) {
					break
				}
			}
			formatTestCalls.Add(1)
			time.Sleep(5 * time.Millisecond)
			return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
		}, func(io.Reader) (image.Config, error) {
			return image.Config{
				ColorModel: color.RGBAModel,
				Width:      1,
				Height:     1,
			}, nil
		})
	})
	formatTestMaximum.Store(0)
	formatTestCalls.Store(0)
	file := filepath.Join(t.TempDir(), "synthetic")
	if err := os.WriteFile(file, []byte("evidence-format-test"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			<-start
			if got := VerifiedFormat(file); got != "serialized-evidence-test" {
				t.Errorf("format=%q", got)
			}
		})
	}
	close(start)
	workers.Wait()
	if formatTestCalls.Load() != 8 || formatTestMaximum.Load() != 1 {
		t.Fatalf("calls=%d peak decodes=%d", formatTestCalls.Load(), formatTestMaximum.Load())
	}
}

func TestVerifiedFormatAcceptsEncodedByteBoundary(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, formatEvidenceMaxBytes)
	copy(payload, encoded.Bytes())
	file := filepath.Join(t.TempDir(), "boundary.png")
	if err := os.WriteFile(file, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := VerifiedFormat(file); got != "png" {
		t.Fatalf("byte boundary format=%q", got)
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if got := VerifiedFormat(file); got != "" {
		t.Fatalf("over byte boundary format=%q", got)
	}
}
