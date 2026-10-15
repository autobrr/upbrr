// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math/big"

	dovi "github.com/Audionut/go-dovi-extract"
)

const (
	maxDVRecordBytes = 1 << 20
	// Compression bounds disk storage independently of the streamed JSON and
	// owned model. Neither decompression nor decoding may bypass these limits.
	maxDVExpandedBytes     = 2 << 30
	dvSidecarWriteScratch  = 8 << 20
	dvSidecarReaderScratch = maxDVRecordBytes + (128 << 10)
	// Allocation classes used by the pinned dovi.Analysis.RetainedBytes model.
	dvAnalysisStorageBytes = 512
	dvRecordStorageBytes   = 256
	dvPictureStorageBytes  = 128
)

// DVSidecar binds full native metadata to the exact selected file or playlist.
type DVSidecar struct {
	Identity ExtractionIdentity
	Analysis *dovi.Analysis
	Timeline []TimelineItem
}

type dvSidecarHeader struct {
	Schema         string             `json:"schema"`
	Identity       ExtractionIdentity `json:"identity"`
	SchemaVersion  int                `json:"analysisSchema"`
	Timeline       dovi.Timeline      `json:"timeline"`
	Complete       bool               `json:"complete"`
	TrackID        uint64             `json:"trackId"`
	ObservedRPUs   uint64             `json:"observedRPUs"`
	SuppressedRPUs uint64             `json:"suppressedRPUs"`
	Records        int                `json:"records"`
	Pictures       int                `json:"pictures"`
	Occurrences    int                `json:"occurrences,omitempty"`
}

// WriteDVSidecar retains full validated native evidence as gzip-compressed,
// bounded JSON records. Legacy uncompressed records remain readable.
// The surrounding extraction manifest supplies the common scan identity and hash.
func WriteDVSidecar(ctx context.Context, output io.Writer, sidecar DVSidecar) (err error) {
	identity, analysis := sidecar.Identity, sidecar.Analysis
	if err := analysis.Validate(dovi.AnalysisOptions{MaxRecords: MaxFrames, MaxBytes: MaxRestoreBytes - dvSidecarWriteScratch}); err != nil {
		return fmt.Errorf("validate Dolby Vision sidecar: %w", err)
	}
	if err := validateDVBinding(sidecar); err != nil {
		return err
	}
	header := dvSidecarHeader{
		Schema:         "dovi-extraction-v1",
		Identity:       identity,
		SchemaVersion:  analysis.SchemaVersion,
		Timeline:       analysis.Timeline,
		Complete:       analysis.Complete,
		TrackID:        analysis.TrackID,
		ObservedRPUs:   analysis.ObservedRPUs,
		SuppressedRPUs: analysis.SuppressedRPUs,
		Records:        len(analysis.Records),
		Pictures:       len(analysis.Pictures),
		Occurrences:    len(sidecar.Timeline),
	}
	if analysis.Timeline == dovi.PlaylistFrames {
		header.Schema = "dovi-extraction-v2"
	}
	compressed, err := gzip.NewWriterLevel(&boundedWriter{output: output, remaining: MaxSidecarBytes}, gzip.BestSpeed)
	if err != nil {
		return fmt.Errorf("create Dolby Vision evidence compressor: %w", err)
	}
	defer func() {
		if closeErr := compressed.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("finish compressed Dolby Vision evidence: %w", closeErr))
		}
	}()
	writer := &boundedWriter{output: compressed, remaining: maxDVExpandedBytes}
	record := func(value any) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("write Dolby Vision evidence: %w", err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode Dolby Vision evidence: %w", err)
		}
		if len(data) >= maxDVRecordBytes {
			return dovi.ErrLimit
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return err
		}
		return nil
	}
	if err := record(header); err != nil {
		return err
	}
	for _, r := range analysis.Records {
		if err := record(r); err != nil {
			return err
		}
	}
	for _, p := range analysis.Pictures {
		if err := record(p); err != nil {
			return err
		}
	}
	for _, item := range sidecar.Timeline {
		if err := record(item); err != nil {
			return err
		}
	}
	return nil
}

// ReadDVSidecar rejects oversized records before typed decoding and validates
// the entire native analysis. No derived summary is trusted from disk.
func ReadDVSidecar(ctx context.Context, input io.Reader, expected ExtractionIdentity, maxBytes int64) (DVSidecar, error) {
	if maxBytes < dvSidecarReaderScratch || maxBytes > MaxRestoreBytes {
		return DVSidecar{}, dovi.ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return DVSidecar{}, fmt.Errorf("read Dolby Vision evidence: %w", err)
	}
	limited := &io.LimitedReader{R: input, N: MaxSidecarBytes + 1}
	stored := bufio.NewReader(limited)
	magic, err := stored.Peek(2)
	if err != nil {
		return DVSidecar{}, fmt.Errorf("read Dolby Vision evidence envelope: %w", err)
	}
	var source io.Reader = stored
	var compressed *gzip.Reader
	if bytes.Equal(magic, []byte{0x1f, 0x8b}) {
		compressed, err = gzip.NewReader(stored)
		if err != nil {
			return DVSidecar{}, fmt.Errorf("open compressed Dolby Vision evidence: %w", err)
		}
		defer compressed.Close()
		// Exactly one complete member: do not accept concatenated evidence or
		// trailing bytes outside the checksummed record stream.
		compressed.Multistream(false)
		source = compressed
	}
	expanded := &io.LimitedReader{R: source, N: maxDVExpandedBytes + 1}
	reader := bufio.NewReaderSize(expanded, maxDVRecordBytes)
	allocated := int64(dvSidecarReaderScratch + dvAnalysisStorageBytes)
	decode := func(value any, retainEncodedBound bool) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read Dolby Vision evidence: %w", err)
		}
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) || limited.N == 0 || expanded.N == 0 {
			return dovi.ErrLimit
		}
		if err != nil {
			return fmt.Errorf("read Dolby Vision record: %w", err)
		}
		// Reserve each record's decoder expansion before allocation. Scratch
		// is reused, not cumulatively retained for every picture in a film.
		charge := int64(len(line)) * 16
		if charge > maxBytes-allocated {
			return dovi.ErrLimit
		}
		if retainEncodedBound {
			allocated += charge
		}
		if err := validateDVRecordShape(line); err != nil {
			return err
		}
		if err := json.Unmarshal(line, value, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("decode Dolby Vision record: %w", err)
		}
		return nil
	}
	var header dvSidecarHeader
	if err := decode(&header, true); err != nil {
		return DVSidecar{}, err
	}
	file := header.Schema == "dovi-extraction-v1" && header.SchemaVersion == 1 && header.Timeline == dovi.PresentationFrames &&
		header.TrackID == expected.ResolvedTrackID && header.Occurrences == 0 && expected.SelectionPolicy == "unique_hevc"
	disc := header.Schema == "dovi-extraction-v2" && header.SchemaVersion == 2 && header.Timeline == dovi.PlaylistFrames &&
		header.TrackID == 0 && header.Occurrences > 0 && header.Occurrences <= 8192 && expected.SelectionPolicy == "primary_hevc_angle_zero"
	if header.Identity != expected || !header.Complete || !file && !disc {
		return DVSidecar{}, errors.New("dolby vision sidecar identity or completion is invalid")
	}
	if header.Records < 0 || header.Records > MaxFrames || header.Pictures <= 0 || header.Pictures > MaxFrames {
		return DVSidecar{}, dovi.ErrLimit
	}
	allocated += int64(header.Records)*dvRecordStorageBytes + int64(header.Pictures)*dvPictureStorageBytes + int64(header.Occurrences)*4096
	if allocated > maxBytes {
		return DVSidecar{}, dovi.ErrLimit
	}
	analysis := &dovi.Analysis{
		SchemaVersion:  header.SchemaVersion,
		Timeline:       header.Timeline,
		Complete:       header.Complete,
		TrackID:        header.TrackID,
		ObservedRPUs:   header.ObservedRPUs,
		SuppressedRPUs: header.SuppressedRPUs,
		Records:        make([]dovi.RPU, header.Records),
		Pictures:       make([]dovi.Picture, header.Pictures),
	}
	for i := range analysis.Records {
		if err := decode(&analysis.Records[i], false); err != nil {
			return DVSidecar{}, err
		}
		// Reuse the library's validated nested-storage accounting. The model
		// base and preallocated record slot have already been charged above.
		record := dovi.Analysis{
			SchemaVersion: 1,
			Timeline:      dovi.RPUSequence,
			Complete:      true,
			Records:       analysis.Records[i : i+1],
			ObservedRPUs:  1,
		}
		retained, err := record.RetainedBytes(dovi.AnalysisOptions{MaxRecords: 1, MaxBytes: maxBytes})
		if err != nil {
			return DVSidecar{}, fmt.Errorf("account restored Dolby Vision record: %w", err)
		}
		allocated += retained - dvAnalysisStorageBytes - dvRecordStorageBytes
		if allocated > maxBytes {
			return DVSidecar{}, dovi.ErrLimit
		}
		// JSON slice growth belongs to this record's temporary allowance.
		// Transfer only the library's independently bounded owned copy.
		owned, err := record.Clone(dovi.AnalysisOptions{MaxRecords: 1, MaxBytes: maxBytes})
		if err != nil {
			return DVSidecar{}, fmt.Errorf("own restored Dolby Vision record: %w", err)
		}
		analysis.Records[i] = owned.Records[0]
	}
	for i := range analysis.Pictures {
		if err := decode(&analysis.Pictures[i], false); err != nil {
			return DVSidecar{}, err
		}
		stream := analysis.Pictures[i].Stream
		allocated += 2 * int64(len(stream.SourceID)+len(stream.ClipID))
		if allocated > maxBytes {
			return DVSidecar{}, dovi.ErrLimit
		}
	}
	sidecar := DVSidecar{
		Identity: expected,
		Analysis: analysis,
		Timeline: make([]TimelineItem, header.Occurrences),
	}
	for i := range sidecar.Timeline {
		if err := decode(&sidecar.Timeline[i], true); err != nil {
			return DVSidecar{}, err
		}
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return DVSidecar{}, errors.New("dolby vision sidecar has trailing data")
	}
	if limited.N == 0 || expanded.N == 0 {
		return DVSidecar{}, dovi.ErrLimit
	}
	if compressed != nil {
		if _, err := stored.ReadByte(); !errors.Is(err, io.EOF) {
			return DVSidecar{}, errors.New("compressed Dolby Vision sidecar has trailing data")
		}
	}
	if err := analysis.Validate(dovi.AnalysisOptions{MaxRecords: MaxFrames, MaxBytes: maxBytes}); err != nil {
		return DVSidecar{}, fmt.Errorf("validate restored Dolby Vision evidence: %w", err)
	}
	if err := validateDVBinding(sidecar); err != nil {
		return DVSidecar{}, err
	}
	return sidecar, nil
}

func validateDVBinding(sidecar DVSidecar) error {
	if err := validateTimeline(sidecar.Identity.SelectionPolicy, sidecar.Timeline); err != nil {
		return err
	}
	a := sidecar.Analysis
	if a == nil {
		return errors.New("dolby vision sidecar has no analysis")
	}
	if a.Timeline == dovi.PresentationFrames {
		if a.TrackID != sidecar.Identity.ResolvedTrackID || len(sidecar.Timeline) != 0 {
			return errors.New("dolby vision file track is invalid")
		}
		return nil
	}
	if a.Timeline != dovi.PlaylistFrames || len(sidecar.Timeline) == 0 || len(sidecar.Timeline) > 8192 || len(a.Pictures) == 0 {
		return errors.New("dolby vision playlist authority is invalid")
	}
	var actual, lower, upper, scale, ticks big.Int
	for i, picture := range a.Pictures {
		if picture.OccurrenceOrdinal >= uint64(len(sidecar.Timeline)) || !picture.PTS.Valid || !picture.POCKnown ||
			picture.Stream.SourceID != a.Pictures[0].Stream.SourceID || i > 0 && picture.OccurrenceOrdinal < a.Pictures[i-1].OccurrenceOrdinal {
			return errors.New("dolby vision picture occurrence is invalid")
		}
		item := sidecar.Timeline[picture.OccurrenceOrdinal]
		if picture.Stream.ClipID != item.Source.Path || picture.Stream.TrackID != uint64(item.Mapping.PID) ||
			!item.STC.HasEndPacket || item.In45 < item.STC.PresentationStart45 || item.Out45 > item.STC.PresentationEnd45 ||
			item.ConnectionCondition != 1 && item.ConnectionCondition != 5 && item.ConnectionCondition != 6 {
			return errors.New("dolby vision picture stream or clock differs from its playlist")
		}
		ticks.SetInt64(45000)
		actual.SetInt64(picture.PTS.Value)
		actual.Mul(&actual, &ticks)
		scale.SetUint64(picture.PTS.Timescale)
		lower.SetUint64(item.Offset45)
		lower.Mul(&lower, &scale)
		upper.SetUint64(item.Offset45 + item.Duration45)
		upper.Mul(&upper, &scale)
		if actual.Cmp(&lower) < 0 || actual.Cmp(&upper) >= 0 {
			return errors.New("dolby vision picture lies outside its selected interval")
		}
	}
	return nil
}

// Bound arrays and nesting before JSON allocates typed slices. Native validation
// then enforces each specific array's tighter grammar and presence constraints.
func validateDVRecordShape(data []byte) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	counts := make([]int, 0, 32)
	for {
		token, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect Dolby Vision record: %w", err)
		}
		kind := token.Kind()
		if kind == jsontext.KindEndObject || kind == jsontext.KindEndArray {
			if len(counts) == 0 {
				return errors.New("unbalanced Dolby Vision record")
			}
			counts = counts[:len(counts)-1]
			continue
		}
		if len(counts) > 0 {
			counts[len(counts)-1]++
			if counts[len(counts)-1] > 512 {
				return dovi.ErrLimit
			}
		}
		if kind == jsontext.KindBeginObject || kind == jsontext.KindBeginArray {
			if len(counts) == 32 {
				return dovi.ErrLimit
			}
			counts = append(counts, 0)
		}
	}
}
