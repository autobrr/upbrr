// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func assertAuthoredDVDClockWrap(t *testing.T, root string) {
	t.Helper()
	const shift = uint64(1<<33) - 45*90000
	path := filepath.Join(root, "dvd", "VIDEO_TS", "VTS_01_1.VOB")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for start := 0; start < len(data); start += 2048 {
		sector := data[start : start+2048]
		if !bytes.Equal(sector[:4], []byte{0, 0, 1, 0xba}) {
			t.Fatal("author produced a non-PS sector")
		}
		// Rebase original SCR, PES PTS/DTS and NAV presentation clocks together;
		// payloads, sector addresses, elapsed times and authored durations stay put.
		word := binary.BigEndian.Uint64(sector[4:12]) >> 16
		scr := (((word>>43)&7)<<30 | ((word>>27)&0x7fff)<<15 | ((word >> 11) & 0x7fff))
		scr = (scr + shift) & ((1 << 33) - 1)
		word = word&^(uint64(7)<<43|uint64(0x7fff)<<27|uint64(0x7fff)<<11) | (scr>>30)<<43 | ((scr>>15)&0x7fff)<<27 | (scr&0x7fff)<<11
		for i := range 6 {
			sector[4+i] = byte(word >> uint(40-i*8))
		}
		for offset := 14 + int(sector[13]&7); offset+6 <= len(sector); {
			if !bytes.Equal(sector[offset:offset+3], []byte{0, 0, 1}) {
				t.Fatal("author produced an invalid PS packet")
			}
			kind := sector[offset+3]
			if kind == 0xb9 {
				break
			}
			length := int(binary.BigEndian.Uint16(sector[offset+4:]))
			end := offset + 6 + length
			if length == 0 || end > len(sector) {
				t.Fatal("author produced a split PES header")
			}
			packet := sector[offset:end]
			if kind == 0xbf {
				payload := packet[7:]
				switch packet[6] {
				case 0:
					for _, clock := range []int{12, 16, 20} {
						value := binary.BigEndian.Uint32(payload[clock:])
						if clock != 20 || value != 0 {
							binary.BigEndian.PutUint32(payload[clock:], value+uint32(shift&0xffffffff))
						}
					}
				case 1:
					binary.BigEndian.PutUint32(payload, binary.BigEndian.Uint32(payload)+uint32(shift&0xffffffff))
				}
			} else if kind >= 0xe0 && kind <= 0xef && len(packet) >= 14 {
				flags := packet[7] >> 6
				if flags&2 != 0 {
					rebaseFixturePESClock(packet[9:14], shift)
				}
				if flags == 3 {
					rebaseFixturePESClock(packet[14:19], shift)
				}
			}
			offset = end
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	discovery, err := filesystem.DiscoverComparisonProgrammes(t.Context(), filepath.Dir(path))
	if err != nil || len(discovery.Inputs) != 1 {
		t.Fatalf("wrapped title discovery: %v", err)
	}
	var input filesystem.ResolvedComparisonInput
	for _, candidate := range discovery.Inputs {
		input = candidate
	}
	input, err = filesystem.ResolveComparisonProgramme(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	service := screenshots.NewService(config.Config{}, api.NopLogger{}, root, nil)
	scratch, err := comparisonScratchDir(filepath.Join(root, "tmp"), filepath.Join(root, "main.mkv"), "wrapped")
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.InspectResolvedComparisonSource(t.Context(), input, "authored-wrap", scratch)
	if err != nil {
		t.Fatal(err)
	}
	for _, start := range []float64{40, 44, 48} {
		frames, err := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
			Source:          source,
			StartSeconds:    start,
			DurationSeconds: 4,
			StrideSeconds:   .002,
		})
		if err != nil || len(frames) == 0 {
			t.Fatalf("native wrapped window %.0f: %v", start, err)
		}
		frame := frames[0]
		if frame.Interlace != "T" {
			t.Fatalf("fixture lost its authored top-field-first evidence: %q", frame.Interlace)
		}
		normalized, err := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
			Source:          source,
			StartSeconds:    start,
			DurationSeconds: 4,
			StrideSeconds:   .002,
			NormalizeSDR:    true,
		})
		if err != nil || len(normalized) != len(frames) {
			t.Fatalf("normalized wrapped window %.0f: %v", start, err)
		}
		for i, sample := range frames {
			if sample.FrameID != normalized[i].FrameID || sample.PictureChecksum != normalized[i].PictureChecksum || !bytes.Equal(sample.Pixels, normalized[i].Pixels) {
				t.Fatal("full-scan DVD matching bypassed interlace/PAR normalization or changed native identity")
			}
		}
		assertOriginalComparisonPicture(t, filepath.Join(root, "source.mpg"), int64(math.Round(frame.RelativeTimestampSeconds*25)), frame.PictureChecksum)
		overlap, err := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
			Source:          source,
			StartSeconds:    start - .5,
			DurationSeconds: 5,
			StrideSeconds:   .002,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := orderedComparisonSamples(append(frames, overlap...)); err != nil {
			t.Fatal(err)
		}
		captured, err := service.ComparisonCapture(t.Context(), screenshots.ComparisonCaptureRequest{
			Source:           source,
			TimestampSeconds: frame.RelativeTimestampSeconds,
			Replay:           frame.Replay,
		})
		if err != nil || captured.FrameID != frame.FrameID || captured.PictureChecksum != frame.PictureChecksum {
			t.Fatalf("wrapped native replay: %v", err)
		}
	}
}

func rebaseFixturePESClock(data []byte, shift uint64) {
	value := uint64((data[0]>>1)&7)<<30 | uint64(data[1])<<22 | uint64(data[2]>>1)<<15 | uint64(data[3])<<7 | uint64(data[4]>>1)
	value = (value + shift) & ((1 << 33) - 1)
	data[0] = data[0]&0xf0 | byte((value>>29)&14) | 1
	data[1], data[2] = byte(value>>22), byte((value>>14)&254)|1
	data[3], data[4] = byte(value>>7), byte((value<<1)&254)|1
}
