// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/Audionut/go-hdr10-plus/extract"
)

func boundaryFramedNAL(nal []byte) []byte {
	out := make([]byte, len(nal)+4)
	binary.BigEndian.PutUint32(out, uint32(len(nal)))
	copy(out[4:], nal)
	return out
}

func TestBoundaryAncillaryAndSampleLimits(t *testing.T) {
	t.Run("sample", func(t *testing.T) {
		c := &boundaryNALCounter{}
		for range 64 {
			if err := c.headerNAL([]byte{70, 1}, 2, false); err != nil {
				t.Fatal(err)
			}
		}
		if c.headerNAL([]byte{70, 1}, 2, false) == nil {
			t.Fatal("accepted65NALpacket")
		}
	})
	t.Run("total", func(t *testing.T) {
		c := &boundaryNALCounter{}
		for range 4096 {
			c.sampleNALs = 0
			if err := c.headerNAL([]byte{70, 1}, 2, false); err != nil {
				t.Fatal(err)
			}
		}
		c.sampleNALs = 0
		if c.headerNAL([]byte{70, 1}, 2, false) == nil {
			t.Fatal("accepted4097NALclip")
		}
	})
	t.Run("bytes", func(t *testing.T) {
		c := &boundaryNALCounter{}
		for range 16 {
			if err := c.headerNAL([]byte{78, 1}, 64<<10, false); err != nil {
				t.Fatal(err)
			}
		}
		if c.headerNAL([]byte{78, 1}, 2, false) == nil {
			t.Fatal("acceptedexcessancillary")
		}
	})
	t.Run("parameter", func(t *testing.T) {
		c := &boundaryNALCounter{}
		if err := c.headerNAL([]byte{64, 1}, 64<<10, true); err != nil {
			t.Fatal(err)
		}
		if c.headerNAL([]byte{64, 1}, 2, true) == nil {
			t.Fatal("acceptedexcessconfiguration")
		}
	})
	t.Run("layer", func(t *testing.T) {
		c := &boundaryNALCounter{}
		if c.headerNAL([]byte{66, 9}, 3, true) == nil {
			t.Fatal("acceptednonbasesps")
		}
	})
}

func TestBoundarySampleBoundaryAndChunkSplits(t *testing.T) {
	data := append(boundaryFramedNAL([]byte{2, 1, 0}), boundaryFramedNAL([]byte{2, 1, 0})...)
	for split := 0; split <= len(data); split++ {
		c := &boundaryNALCounter{width: 4}
		a := extract.Chunk{Data: data[:split]}
		b := extract.Chunk{Data: data[split:], ESOffset: uint64(split)}
		for _, offset := range []uint64{0, 7} {
			if offset < uint64(split) {
				a.Markers = append(a.Markers, extract.Marker{Kind: extract.SampleStart, ESOffset: offset})
			} else {
				b.Markers = append(b.Markers, extract.Marker{Kind: extract.SampleStart, ESOffset: offset})
			}
		}
		if err := c.Push(context.Background(), a); err != nil {
			t.Fatalf("split%d first:%v", split, err)
		}
		if err := c.Push(context.Background(), b); err != nil {
			t.Fatalf("split%d second:%v", split, err)
		}
		if c.units != 2 || c.left != 0 || len(c.prefix) != 0 {
			t.Fatalf("split%d incomplete", split)
		}
	}
	c := &boundaryNALCounter{width: 4}
	if c.Push(context.Background(), extract.Chunk{Data: data, Markers: []extract.Marker{{Kind: extract.SampleStart}, {Kind: extract.SampleStart, ESOffset: 6}}}) == nil {
		t.Fatal("acceptedcrosssamplenal")
	}
}

func TestBoundaryAllVCLSlicesCount(t *testing.T) {
	c := &boundaryNALCounter{}
	for range 512 {
		c.sampleNALs = 0
		if err := c.headerNAL([]byte{2, 1}, 2, false); err != nil {
			t.Fatal(err)
		}
	}
	c.sampleNALs = 0
	if c.headerNAL([]byte{2, 1}, 2, false) == nil {
		t.Fatal("accepted513VCL")
	}
}
