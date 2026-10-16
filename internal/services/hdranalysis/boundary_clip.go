// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"github.com/Audionut/go-hdr10-plus/extract"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
)

type boundaryGeometry struct{ Width, Height, BitDepth, MinCB, CTB uint32 }
type boundaryNALCounter struct {
	width                                                      int
	prefix, header, sps                                        []byte
	left, size, units, nals, sampleNALs, ancillary, parameters int
	offset                                                     uint64
	raster                                                     *boundaryGeometry
	pts                                                        []extract.Timestamp
	syntax                                                     *extract.Stream
}

func (c *boundaryNALCounter) headerNAL(header []byte, size int, config bool) error {
	if len(header) < 2 || header[0]&128 != 0 || header[1]&7 == 0 {
		return errors.New("invalid NAL header")
	}
	c.nals++
	if c.nals > 4096 {
		return errors.New("total NAL cap")
	}
	if !config {
		c.sampleNALs++
		if c.sampleNALs > 64 {
			return errors.New("sample NAL cap")
		}
	}
	kind := (header[0] >> 1) & 63
	layer := ((header[0] & 1) << 5) | (header[1] >> 3)
	if layer != 0 {
		return errors.New("unsupported non-base layer")
	}
	if kind <= 31 {
		if config || kind >= 10 && kind <= 15 || kind >= 22 {
			return errors.New("unsupported VCL syntax")
		}
		c.units++
		if c.units > 512 {
			return errors.New("VCL cap")
		}
	} else {
		if size > 64<<10 {
			return errors.New("ancillary NAL byte cap")
		}
		c.ancillary += size
		if c.ancillary > 1<<20 {
			return errors.New("ancillary total cap")
		}
		if config || kind >= 32 && kind <= 34 {
			c.parameters += size
			if c.parameters > 64<<10 {
				return errors.New("configuration/parameter cap")
			}
		}
	}
	return nil
}

func (c *boundaryNALCounter) parameter(data []byte) error {
	if (data[0]>>1)&63 != 33 {
		return nil
	}
	var s h265.SPS
	if err := s.Unmarshal(data); err != nil {
		return fmt.Errorf("validate boundary clip: %w", err)
	}
	if s.ChromaFormatIdc != 1 || s.BitDepthLumaMinus8 > 2 || s.BitDepthChromaMinus8 != s.BitDepthLumaMinus8 ||
		s.PicWidthInLumaSamples == 0 || s.PicHeightInLumaSamples == 0 ||
		s.Log2MinLumaCodingBlockSizeMinus3 > 3 || s.Log2DiffMaxMinLumaCodingBlockSize > 3 ||
		s.Log2MinLumaCodingBlockSizeMinus3+s.Log2DiffMaxMinLumaCodingBlockSize+3 < 4 ||
		s.Log2MinLumaCodingBlockSizeMinus3+s.Log2DiffMaxMinLumaCodingBlockSize+3 > 6 {
		return errors.New("unsupported SPS allocation boundaryGeometry")
	}
	g := boundaryGeometry{s.PicWidthInLumaSamples, s.PicHeightInLumaSamples, s.BitDepthLumaMinus8 + 8,
		1 << (s.Log2MinLumaCodingBlockSizeMinus3 + 3), 1 << (s.Log2MinLumaCodingBlockSizeMinus3 + s.Log2DiffMaxMinLumaCodingBlockSize + 3)}
	if c.raster != nil && *c.raster != g {
		return errors.New("changing coded raster/allocation boundaryGeometry")
	}
	c.raster = &g
	return nil
}

func (c *boundaryNALCounter) configure(data []byte) error {
	if len(data) < 23 || len(data) > 64<<10 || data[0] != 1 || int(data[21]&3)+1 != c.width {
		return errors.New("invalid hvcC")
	}
	count := int(data[22])
	data = data[23:]
	for range count {
		if len(data) < 3 {
			return errors.New("short hvcC array")
		}
		n := int(binary.BigEndian.Uint16(data[1:3]))
		data = data[3:]
		for range n {
			if len(data) < 2 {
				return errors.New("short hvcC length")
			}
			size := int(binary.BigEndian.Uint16(data))
			data = data[2:]
			if size < 2 || size > len(data) {
				return errors.New("invalid hvcC NAL")
			}
			if err := c.headerNAL(data[:size], size, true); err != nil {
				return fmt.Errorf("validate boundary clip: %w", err)
			}
			if err := c.parameter(data[:size]); err != nil {
				return fmt.Errorf("validate boundary clip: %w", err)
			}
			data = data[size:]
		}
	}
	if len(data) != 0 {
		return errors.New("trailing hvcC bytes")
	}
	return nil
}

func (c *boundaryNALCounter) feed(data []byte) error {
	for len(data) > 0 {
		if c.left == 0 {
			n := min(c.width-len(c.prefix), len(data))
			c.prefix = append(c.prefix, data[:n]...)
			data = data[n:]
			if len(c.prefix) < c.width {
				continue
			}
			for _, b := range c.prefix {
				c.left = (c.left << 8) | int(b)
			}
			c.size = c.left
			c.prefix = c.prefix[:0]
			c.header = c.header[:0]
			c.sps = nil
			if c.left < 2 || c.left > 64<<20 {
				return errors.New("invalid NAL length")
			}
		}
		if len(c.header) < 2 {
			n := min(2-len(c.header), len(data))
			c.header = append(c.header, data[:n]...)
			data = data[n:]
			c.left -= n
			if len(c.header) < 2 {
				continue
			}
			if err := c.headerNAL(c.header, c.size, false); err != nil {
				return fmt.Errorf("validate boundary clip: %w", err)
			}
			if (c.header[0]>>1)&63 == 33 {
				c.sps = append(c.sps, c.header...)
			}
		}
		n := min(c.left, len(data))
		if c.sps != nil {
			c.sps = append(c.sps, data[:n]...)
		}
		c.left -= n
		data = data[n:]
		if c.left == 0 && c.sps != nil {
			if err := c.parameter(c.sps); err != nil {
				return fmt.Errorf("validate boundary clip: %w", err)
			}
			c.sps = nil
		}
	}
	return nil
}

func (c *boundaryNALCounter) Push(ctx context.Context, chunk extract.Chunk) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("validate boundary clip: %w", err)
	}
	if chunk.ESOffset != c.offset {
		return errors.New("non-contiguous chunks")
	}
	data := chunk.Data
	position := chunk.ESOffset
	for _, marker := range chunk.Markers {
		if marker.Kind != extract.SampleStart {
			continue
		}
		if marker.ESOffset < position || marker.ESOffset-position > uint64(len(data)) {
			return errors.New("invalid sample marker")
		}
		count := int(marker.ESOffset - position) //nolint:gosec // Difference is bounded by len(data) above.
		if err := c.feed(data[:count]); err != nil {
			return fmt.Errorf("validate boundary clip: %w", err)
		}
		data = data[count:]
		position = marker.ESOffset
		if c.left != 0 || len(c.prefix) != 0 {
			return errors.New("NAL crosses sample boundary")
		}
		c.sampleNALs = 0
		c.pts = append(c.pts, marker.PTS)
		if len(c.pts) > 512 {
			return errors.New("sample cap")
		}
	}
	if err := c.feed(data); err != nil {
		return fmt.Errorf("validate boundary clip: %w", err)
	}
	c.offset += uint64(len(chunk.Data))
	if c.syntax != nil {
		if err := c.syntax.Push(ctx, chunk); err != nil {
			return fmt.Errorf("validate boundary syntax: %w", err)
		}
	}
	return nil
}

type boundaryClipProof struct {
	sha256     string
	bytes      int64
	geometry   boundaryGeometry
	vcl, nals  int
	timestamps []extract.Timestamp
}

func validateBoundaryClip(ctx context.Context, path string) (boundaryClipProof, error) {
	var proof boundaryClipProof
	file, err := os.Open(path)
	if err != nil {
		return proof, fmt.Errorf("validate boundary clip: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return proof, fmt.Errorf("validate boundary clip: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<20 {
		return proof, errors.New("boundary clip exceeds byte bounds")
	}
	c := &boundaryNALCounter{}
	limits := extract.Limits{
		MaxFrames:              512,
		MaxRetainedBytes:       96 << 20,
		MaxContainerIndexBytes: 8 << 20,
		MaxBlockBytes:          64 << 20,
		MaxNestingDepth:        16,
	}
	defer func() {
		if c.syntax != nil {
			c.syntax.Abort(errors.New("boundary clip validation ended"))
		}
	}()
	err = extract.Walk(ctx, file, extract.Options{Limits: limits}, func(source extract.SourceInfo) (extract.Consumer, error) {
		if source.Framing != extract.LengthPrefixed || source.LengthSize < 1 || source.LengthSize > 4 || source.Edit != nil {
			return nil, errDecodedMapping
		}
		c.width = source.LengthSize
		if err := c.configure(source.CodecConfig); err != nil {
			return nil, fmt.Errorf("validate boundary clip: %w", err)
		}
		c.syntax, err = extract.NewStream(extract.StreamOptions{
			Key:         source.Key,
			Framing:     source.Framing,
			LengthSize:  source.LengthSize,
			CodecConfig: source.CodecConfig,
			Limits:      limits,
		})
		if err != nil {
			return nil, fmt.Errorf("validate boundary clip: %w", err)
		}
		return c, nil
	})
	if err != nil {
		return proof, fmt.Errorf("validate boundary clip: %w", err)
	}
	if c.left != 0 || len(c.prefix) != 0 || c.units == 0 || c.raster == nil {
		return proof, errDecodedMapping
	}
	syntax, err := c.syntax.Finish(ctx)
	if err != nil {
		return proof, fmt.Errorf("validate boundary clip: %w", err)
	}
	syntax.Release()
	proof.geometry, proof.vcl, proof.nals, proof.timestamps = *c.raster, c.units, c.nals, c.pts
	return proof, nil
}
