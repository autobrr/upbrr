// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func measurementByteLimit(schema string, mode api.HDRDecodedMode) int64 {
	if schema == "hdr-measurement-v2" {
		if mode == api.HDRDecodedBoundarySync {
			return MaxBoundaryMeasurementBytes
		}
		if mode == api.HDRDecodedAudio {
			return MaxTrackAudioMeasurementBytes
		}
	}
	if mode == api.HDRDecodedAutomatic || mode == api.HDRDecodedAudio {
		return MaxAutomaticMeasurementBytes
	}
	return MaxMeasurementBytes
}

func measurementSchema(ctx context.Context, data []byte) (string, api.HDRDecodedMode, error) {
	d := jsontext.NewDecoder(bytes.NewReader(data))
	var schema string
	var mode api.HDRDecodedMode
	for count := 0; ; count++ {
		token, err := d.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("read measurement schema: %w", err)
		}
		if count > 100000000 || d.StackDepth() > 16 {
			return "", "", errors.New("measurement schema exceeds bounds")
		}
		if count%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return "", "", fmt.Errorf("read measurement schema: %w", err)
			}
		}
		kind, length := d.StackIndex(d.StackDepth())
		if token.Kind() == '"' && kind == '{' && length%2 == 0 {
			switch string(d.StackPointer()) {
			case "/schema":
				schema = token.String()
			case "/result/mode":
				mode = api.HDRDecodedMode(token.String())
			}
		}
		if schema != "" && mode != "" {
			break
		}
	}
	if schema != "hdr-measurement-v1" && schema != "hdr-measurement-v2" || mode == "" {
		return "", "", errors.New("unsupported measurement schema")
	}
	return schema, mode, nil
}

func preflightMeasurementV2(ctx context.Context, data []byte, mode api.HDRDecodedMode) error {
	d := jsontext.NewDecoder(bytes.NewReader(data))
	for count := 0; ; count++ {
		token, err := d.ReadToken()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect v2 measurement: %w", err)
		}
		if count >= 1000000 || d.StackDepth() > 16 {
			return errors.New("v2 measurement exceeds token or depth bounds")
		}
		if count%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("inspect v2 measurement: %w", err)
			}
		}
		parts := strings.Split(strings.TrimPrefix(string(d.StackPointer()), "/"), "/")
		bound := func(at, maximum int) bool {
			if at >= len(parts) {
				return true
			}
			index, err := strconv.Atoi(parts[at])
			return err != nil || index >= 0 && index < maximum
		}
		valid := true
		switch parts[0] {
		case "boundary":
			if len(parts) > 1 {
				switch parts[1] {
				case "formats":
					valid = bound(2, 2)
					if len(parts) > 3 {
						switch parts[3] {
						case "numericCandidates":
							valid = valid && bound(4, 60)
						case "visualCandidates":
							valid = valid && bound(4, 48)
						case "observations":
							valid = valid && bound(4, 108)
							if len(parts) > 5 && parts[5] == "scores" {
								valid = valid && bound(6, 7)
							}
						case "thumbnails":
							valid = valid && bound(4, 24)
						}
					}
				case "selectedFrameIndices":
					valid = bound(2, boundaryMaxFrames)
				case "windows":
					valid = bound(2, boundaryMaxWindows)
					if len(parts) > 3 && parts[3] == "invocations" {
						valid = valid && bound(4, 4)
						if len(parts) > 5 && parts[5] == "pts" {
							valid = valid && bound(6, 512)
						}
					}
				}
			}
		case "audio":
			if len(parts) > 1 && parts[1] == "images" {
				valid = bound(2, 2)
			}
			if len(parts) > 2 && parts[1] == "track" && parts[2] == "artifacts" {
				valid = bound(3, 3)
			}
		case "selected":
			valid = bound(1, 2)
			if len(parts) > 2 {
				valid = valid && bound(2, 600)
			}
		case "scene":
			valid = bound(1, 60)
		case "area", "areaSamples":
			valid = bound(1, 120)
		case "grade":
			valid = bound(1, 120)
		case "thumbnails":
			valid = bound(1, 24)
		case "processing":
			valid = bound(1, 3)
		case "chromaticity":
			valid = bound(1, 2) && bound(2, 4096)
		case "result":
			if len(parts) > 1 {
				switch parts[1] {
				case "artifacts":
					valid = bound(2, 3)
				case "checks":
					valid = bound(2, 32)
				case "audioTracks":
					valid = false
				}
			}
		case "automatic":
			if token.Kind() != 'n' {
				valid = false
			}
		}
		if !valid {
			return errors.New("v2 measurement exceeds nested array bounds")
		}
		container, position := d.StackIndex(d.StackDepth())
		if token.Kind() == '"' && (container != '{' || position%2 == 0) {
			value := token.String()
			last := parts[len(parts)-1]
			if (last == "luma" || last == "rgb") && strings.Contains(string(d.StackPointer()), "/thumbnails/") {
				length := sceneWidth * sceneHeight
				if last == "rgb" {
					length *= 3
				}
				if len(value) != base64.StdEncoding.EncodedLen(length) && (last != "rgb" || value != "") {
					return fmt.Errorf("v2 thumbnail %s has encoded length %d, want %d", last, len(value), base64.StdEncoding.EncodedLen(length))
				}
			}
			if parts[0] == "audio" && last == "data" && len(value) > base64.StdEncoding.EncodedLen(32<<20) {
				return errors.New("v2 audio image exceeds encoding bounds")
			}
			if parts[0] == "audio" && (last == "stats" || last == "text") && len(value) > api.AudioAnalysisStatsMaxBytes {
				return errors.New("v2 audio text exceeds bounds")
			}
		}
		if mode == api.HDRDecodedActiveArea && parts[0] == "selected" && !bound(2, 120) {
			return errors.New("v2 area selection exceeds bounds")
		}
	}
}
