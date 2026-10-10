// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"github.com/Audionut/go-hdr10-plus/extract"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"

	"github.com/autobrr/upbrr/pkg/api"
)

// ExtractionIdentity binds private retained metadata to an exact source and selection.
// The resolved native track is retained separately from the policy that selected it.
type ExtractionIdentity struct {
	SourceFingerprint string `json:"sourceFingerprint"`
	TargetID          string `json:"targetId"`
	SelectionPolicy   string `json:"selectionPolicy"`
	ResolvedTrackID   uint64 `json:"resolvedTrackId"`
}

type sidecarHeader struct {
	Schema     string             `json:"schema"`
	Dependency string             `json:"dependency"`
	Identity   ExtractionIdentity `json:"identity"`
	Absent     bool               `json:"absent"`
	Profile    string             `json:"profile"`
	Payloads   int                `json:"payloads"`
	Frames     int                `json:"frames"`
	Scenes     int                `json:"scenes"`
	Timeline   int                `json:"timeline"`
}

// Sidecar is the complete extraction or verified supported absence, never a partial scan.
type Sidecar struct {
	Identity   ExtractionIdentity
	Absent     bool
	Extraction *extract.Extraction
	Timeline   []TimelineItem
}

func validateSidecarHeader(header sidecarHeader) error {
	if header.Timeline < 0 || header.Timeline > 8192 || header.Frames < 0 || header.Frames > MaxFrames ||
		header.Payloads < 0 || header.Payloads > MaxPayloads || header.Scenes < 0 || header.Scenes > header.Frames {
		return extract.ErrResourceLimit
	}
	if header.Absent && (header.Frames != 0 || header.Payloads != 0 || header.Scenes != 0 || header.Profile != "") ||
		!header.Absent && (header.Frames == 0 || header.Payloads == 0 || header.Identity.ResolvedTrackID == 0) {
		return errors.New("HDR sidecar terminal outcome is invalid")
	}
	allocation := int64(header.Frames)*2048 + int64(header.Payloads)*4096 + int64(header.Scenes)*16 + int64(header.Timeline)*4096
	if allocation > MaxRestoreBytes {
		return fmt.Errorf("HDR sidecar restore storage: request=%d limit=%d: %w", allocation, int64(MaxRestoreBytes), extract.ErrResourceLimit)
	}
	return nil
}

type boundedWriter struct {
	output    io.Writer
	remaining int64
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	if int64(len(value)) > w.remaining {
		return 0, extract.ErrResourceLimit
	}
	n, err := w.output.Write(value)
	w.remaining -= int64(n)
	if err != nil {
		return n, fmt.Errorf("write bounded HDR sidecar: %w", err)
	}
	return n, nil
}

// WriteSidecar streams explicit JSON arrays with one bounded record per line.
// This versioned representation makes hostile individual records bounded before decoding.
func WriteSidecar(ctx context.Context, output io.Writer, sidecar Sidecar) error {
	header := sidecarHeader{
		Schema:     api.HDRExtractionSchemaVersion,
		Dependency: DependencyFingerprint,
		Identity:   sidecar.Identity,
		Absent:     sidecar.Absent,
	}
	if !sidecar.Absent {
		if sidecar.Extraction == nil || len(sidecar.Extraction.Frames) == 0 || len(sidecar.Extraction.Payloads) == 0 {
			return extract.ErrIncomplete
		}
		header.Profile = sidecar.Extraction.Profile
		header.Payloads, header.Frames, header.Scenes = len(sidecar.Extraction.Payloads), len(sidecar.Extraction.Frames), len(sidecar.Extraction.SceneStarts)
	} else if sidecar.Extraction != nil {
		return errors.New("HDR absence sidecar cannot contain extraction")
	}
	header.Timeline = len(sidecar.Timeline)
	if err := validateSidecarHeader(header); err != nil {
		return err
	}
	if !sidecar.Absent {
		if err := ValidateExtraction(sidecar.Extraction, sidecar.Identity.SelectionPolicy == "unique_hevc"); err != nil {
			return fmt.Errorf("validate HDR sidecar: %w", err)
		}
	}
	if err := validateTimeline(sidecar.Identity.SelectionPolicy, sidecar.Timeline); err != nil {
		return err
	}
	limited := &boundedWriter{output: output, remaining: MaxSidecarBytes}
	write := func(value string) error {
		_, err := io.WriteString(limited, value)
		if err != nil {
			return fmt.Errorf("write HDR sidecar structure: %w", err)
		}
		return nil
	}
	encoded, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("encode HDR sidecar header: %w", err)
	}
	if err := write("{\"header\":" + string(encoded) + ",\n\"payloads\":[\n"); err != nil {
		return err
	}
	record := func(value any, index int) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("encode HDR sidecar record: %w", err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode HDR sidecar record: %w", err)
		}
		if len(encoded) > 8190 {
			return extract.ErrResourceLimit
		}
		if index > 0 {
			if err := write(",\n"); err != nil {
				return err
			}
		}
		_, err = limited.Write(encoded)
		return err
	}
	if sidecar.Extraction != nil {
		for index, payload := range sidecar.Extraction.Payloads {
			if err := record(payload, index); err != nil {
				return err
			}
		}
	}
	if err := write("\n],\n\"frames\":[\n"); err != nil {
		return err
	}
	if sidecar.Extraction != nil {
		for index, frame := range sidecar.Extraction.Frames {
			if err := record(frame, index); err != nil {
				return err
			}
		}
	}
	if err := write("\n],\n\"scenes\":[\n"); err != nil {
		return err
	}
	if sidecar.Extraction != nil {
		for index, scene := range sidecar.Extraction.SceneStarts {
			if err := record(scene, index); err != nil {
				return err
			}
		}
	}
	if err := write("\n],\n\"timeline\":[\n"); err != nil {
		return err
	}
	for index, item := range sidecar.Timeline {
		if err := record(item, index); err != nil {
			return err
		}
	}
	return write("\n]\n}\n")
}

// ReadSidecar bounds file bytes, per-record bytes, element counts and conservative allocation.
// JSON v2 rejects duplicate names, invalid UTF-8, unknown fields and malformed fixed arrays.
func ReadSidecar(ctx context.Context, input io.Reader, expected ExtractionIdentity) (Sidecar, error) {
	limited := &io.LimitedReader{R: input, N: MaxSidecarBytes + 1}
	reader := bufio.NewReaderSize(limited, 8192)
	line := func() ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read HDR sidecar: %w", err)
		}
		value, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) || limited.N == 0 {
			return nil, extract.ErrResourceLimit
		}
		if err != nil {
			return nil, fmt.Errorf("read HDR sidecar record: %w", err)
		}
		return bytes.TrimSuffix(value, []byte{'\n'}), nil
	}
	expect := func(want string) error {
		value, err := line()
		if err != nil {
			return err
		}
		if string(value) != want {
			return errors.New("HDR sidecar structure is invalid")
		}
		return nil
	}
	value, err := line()
	if err != nil {
		return Sidecar{}, err
	}
	if !bytes.HasPrefix(value, []byte("{\"header\":")) || !bytes.HasSuffix(value, []byte{','}) {
		return Sidecar{}, errors.New("HDR sidecar header is invalid")
	}
	var header sidecarHeader
	if err := json.Unmarshal(value[len("{\"header\":"):len(value)-1], &header, json.RejectUnknownMembers(true)); err != nil {
		return Sidecar{}, fmt.Errorf("decode HDR sidecar header: %w", err)
	}
	if header.Schema != api.HDRExtractionSchemaVersion || header.Dependency != DependencyFingerprint ||
		header.Identity.SourceFingerprint != expected.SourceFingerprint ||
		header.Identity.TargetID != expected.TargetID ||
		header.Identity.SelectionPolicy != expected.SelectionPolicy ||
		expected.ResolvedTrackID != 0 && header.Identity.ResolvedTrackID != expected.ResolvedTrackID {
		return Sidecar{}, errors.New("HDR sidecar identity or schema is incompatible")
	}
	if err := validateSidecarHeader(header); err != nil {
		return Sidecar{}, err
	}
	result := &extract.Extraction{
		Profile:     header.Profile,
		Payloads:    make([]extract.Payload, 0, header.Payloads),
		Frames:      make([]extract.Picture, 0, header.Frames),
		SceneStarts: make([]uint64, 0, header.Scenes),
	}
	array := func(count int, decode func([]byte) error) error {
		for index := range count {
			value, err := line()
			if err != nil {
				return err
			}
			if index < count-1 {
				if !bytes.HasSuffix(value, []byte{','}) {
					return errors.New("HDR sidecar array separator is invalid")
				}
				value = value[:len(value)-1]
			}
			if err := decode(value); err != nil {
				return err
			}
		}
		if count == 0 {
			if err := expect(""); err != nil {
				return err
			}
		}
		return nil
	}
	if err := expect("\"payloads\":["); err != nil {
		return Sidecar{}, err
	}
	if err := array(header.Payloads, func(value []byte) error {
		var payload extract.Payload
		if err := json.Unmarshal(value, &payload, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("decode HDR payload: %w", err)
		}
		if len(payload.Distributions) > 10 || payload.Curve != nil && len(payload.Curve.Anchors) > 9 {
			return extract.ErrResourceLimit
		}
		result.Payloads = append(result.Payloads, payload)
		return nil
	}); err != nil {
		return Sidecar{}, fmt.Errorf("decode HDR payloads: %w", err)
	}
	if err := expect("],"); err != nil {
		return Sidecar{}, err
	}
	if err := expect("\"frames\":["); err != nil {
		return Sidecar{}, err
	}
	if err := array(header.Frames, func(value []byte) error {
		var frame extract.Picture
		if err := json.Unmarshal(value, &frame, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("decode HDR frame: %w", err)
		}
		if len(frame.Stream.SourceID)+len(frame.Stream.ClipID) > 1024 {
			return extract.ErrResourceLimit
		}
		if frame.PayloadIndex >= uint32(header.Payloads) { //nolint:gosec // Header payload count is validated in [0, 500000] before decoding.
			return errors.New("HDR sidecar payload reference is invalid")
		}
		result.Frames = append(result.Frames, frame)
		return nil
	}); err != nil {
		return Sidecar{}, fmt.Errorf("decode HDR frames: %w", err)
	}
	if err := expect("],"); err != nil {
		return Sidecar{}, err
	}
	if err := expect("\"scenes\":["); err != nil {
		return Sidecar{}, err
	}
	if err := array(header.Scenes, func(value []byte) error {
		var scene uint64
		if err := json.Unmarshal(value, &scene); err != nil {
			return fmt.Errorf("decode HDR scene: %w", err)
		}
		result.SceneStarts = append(result.SceneStarts, scene)
		return nil
	}); err != nil {
		return Sidecar{}, fmt.Errorf("decode HDR scenes: %w", err)
	}
	if err := expect("],"); err != nil {
		return Sidecar{}, err
	}
	if err := expect("\"timeline\":["); err != nil {
		return Sidecar{}, err
	}
	timeline := make([]TimelineItem, 0, header.Timeline)
	if err := array(header.Timeline, func(value []byte) error {
		var item TimelineItem
		if err := json.Unmarshal(value, &item, json.RejectUnknownMembers(true)); err != nil {
			return fmt.Errorf("decode HDR timeline: %w", err)
		}
		if item.Index != len(timeline) || item.Out45 <= item.In45 || item.Duration45 != uint64(item.Out45-item.In45) || item.Mapping.Role != video.Primary ||
			item.Mapping.Codec != video.HEVC ||
			item.Source.Kind != "m2ts" {
			return errors.New("HDR sidecar timeline is invalid")
		}
		timeline = append(timeline, item)
		return nil
	}); err != nil {
		return Sidecar{}, err
	}
	if err := expect("]"); err != nil {
		return Sidecar{}, err
	}
	if err := expect("}"); err != nil {
		return Sidecar{}, err
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return Sidecar{}, errors.New("HDR sidecar has trailing data")
	}
	if header.Absent {
		if err := validateTimeline(header.Identity.SelectionPolicy, timeline); err != nil {
			return Sidecar{}, err
		}
		return Sidecar{
			Identity: header.Identity,
			Absent:   true,
			Timeline: timeline,
		}, nil
	}
	if err := validateTimeline(header.Identity.SelectionPolicy, timeline); err != nil {
		return Sidecar{}, err
	}
	if err := ValidateExtraction(result, header.Identity.SelectionPolicy == "unique_hevc"); err != nil {
		return Sidecar{}, fmt.Errorf("validate restored HDR metadata: %w", err)
	}
	if header.Identity.SelectionPolicy == "unique_hevc" && result.Frames[0].Stream.TrackID != header.Identity.ResolvedTrackID {
		return Sidecar{}, errors.New("HDR sidecar resolved track is invalid")
	}
	return Sidecar{
		Identity:   header.Identity,
		Extraction: result,
		Timeline:   timeline,
	}, nil
}

func validateTimeline(policy string, items []TimelineItem) error {
	if policy != "unique_hevc" && policy != "primary_hevc_angle_zero" {
		return errors.New("HDR sidecar selection policy is unsupported")
	}
	if policy == "unique_hevc" && len(items) != 0 || policy == "primary_hevc_angle_zero" && len(items) == 0 {
		return errors.New("HDR sidecar timeline authority is invalid")
	}
	var offset uint64
	for index, item := range items {
		if item.Index != index || item.Out45 <= item.In45 || item.Duration45 != uint64(item.Out45-item.In45) || item.Offset45 != offset ||
			item.Mapping.Role != video.Primary ||
			item.Mapping.Codec != video.HEVC ||
			item.Mapping.EntryType != 1 ||
			item.Mapping.PID == 0 ||
			item.Source.Kind != "m2ts" ||
			item.Source.Path == "" ||
			len(item.Source.Path)+len(item.Source.LogicalClip) > 1024 ||
			item.STCID != item.STC.ID ||
			item.STC.HasEndPacket && item.STC.EndPacket <= uint64(item.STC.StartPacket) {
			return errors.New("HDR sidecar timeline is invalid")
		}
		offset += item.Duration45
	}
	return nil
}
