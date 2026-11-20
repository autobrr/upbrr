// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func assertAuthoredRepeatedPlaylist(t *testing.T, root string) {
	t.Helper()
	bdmv := filepath.Join(root, "bluray", "BDMV")
	lists, err := filepath.Glob(filepath.Join(bdmv, "PLAYLIST", "*.mpls"))
	if err != nil || len(lists) != 1 {
		t.Fatalf("authored playlist: %v", err)
	}
	original, err := os.ReadFile(lists[0])
	if err != nil {
		t.Fatal(err)
	}
	playlist := int(binary.BigEndian.Uint32(original[8:12]))
	start := playlist + 10
	length := int(binary.BigEndian.Uint16(original[start:])) + 2
	item := bytes.Clone(original[start : start+length])
	in := binary.BigEndian.Uint32(item[14:18])
	binary.BigEndian.PutUint32(item[14:18], in+3*45000)
	binary.BigEndian.PutUint32(item[18:22], in+8*45000)
	// Retain the independent author's stream table and clip/STC references,
	// but author two trimmed occurrences of the exact same physical pictures.
	body := append([]byte{0, 0, 0, 2, 0, 0}, item...)
	body = append(body, item...)
	data := bytes.Clone(original[:playlist])
	data = binary.BigEndian.AppendUint32(data, uint32(len(body)))
	data = append(data, body...)
	binary.BigEndian.PutUint32(data[12:16], uint32(len(data)))
	binary.BigEndian.PutUint32(data[16:20], 0)
	data = append(data, 0, 0, 0, 2, 0, 0)
	if err := os.WriteFile(filepath.Join(bdmv, "PLAYLIST", "00001.mpls"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	discovery, err := filesystem.DiscoverComparisonProgrammes(t.Context(), bdmv)
	if err != nil {
		t.Fatal(err)
	}
	var input filesystem.ResolvedComparisonInput
	for _, candidate := range discovery.Inputs {
		if candidate.Playlist == "00001.mpls" {
			input = candidate
		}
	}
	input, err = filesystem.ResolveComparisonProgramme(t.Context(), input)
	if err != nil || len(input.Occurrences) != 2 {
		t.Fatalf("repeated timeline: %+v %v", input.Occurrences, err)
	}
	service := screenshots.NewService(config.Config{}, api.NopLogger{}, root, nil)
	scratch, err := comparisonScratchDir(filepath.Join(root, "tmp"), filepath.Join(root, "main.mkv"), "repeated")
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.InspectResolvedComparisonSource(t.Context(), input, "authored-repeat", scratch)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 10,
		StrideSeconds:   .002,
	})
	if err != nil || len(frames) < 2 {
		t.Fatalf("repeat decoding: %v", err)
	}
	frames, err = orderedComparisonSamples(frames)
	if err != nil {
		t.Fatal(err)
	}
	first := frames[0]
	found := false
	for _, repeated := range frames {
		if repeated.PTS != first.PTS || repeated.FrameID == first.FrameID {
			continue
		}
		found = true
		if repeated.Replay.TitleTime90-first.Replay.TitleTime90 != 5*90000 || repeated.Identity.Occurrence == first.Identity.Occurrence {
			t.Fatal("trim/repeat aliased native clock domains")
		}
		for _, frame := range []screenshots.ComparisonSample{first, repeated} {
			assertOriginalComparisonPicture(t, filepath.Join(root, "source.h264"), 72, source, frame)
			captured, err := service.ComparisonCapture(t.Context(), screenshots.ComparisonCaptureRequest{
				Source:           source,
				TimestampSeconds: frame.RelativeTimestampSeconds,
				Replay:           frame.Replay,
			})
			if err != nil || captured.FrameID != frame.FrameID || !captured.Replay.SamePicture(frame.Replay) {
				t.Fatalf("repeated capture changed picture: %v", err)
			}
		}
		break
	}
	if !found {
		t.Fatal("missing repeated physical picture")
	}
	joined, err := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
		Source:          source,
		StartSeconds:    4,
		DurationSeconds: 2,
		StrideSeconds:   .002,
	})
	if err != nil || len(joined) < 2 || joined[0].Identity.Occurrence == joined[len(joined)-1].Identity.Occurrence {
		t.Fatalf("independent join was not partitioned: %v", err)
	}
	bad := input
	bad.Occurrences = append([]filesystem.ComparisonOccurrence(nil), input.Occurrences...)
	bad.Occurrences[0].CPI = nil
	if _, err := service.InspectResolvedComparisonSource(t.Context(), bad, "missing-index", scratch); err == nil {
		t.Fatal("missing CPI admitted a hidden scan")
	}
	streamPath := input.Occurrences[0].Path
	encoded, err := os.ReadFile(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	broken := bytes.ReplaceAll(encoded, []byte{0, 0, 1, 0x67}, []byte{0, 0, 1, 0x6c})
	if bytes.Equal(encoded, broken) {
		t.Fatal("missing SPS removal control")
	}
	if err := os.WriteFile(streamPath, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
		Source:          source,
		DurationSeconds: 1,
		StrideSeconds:   .002,
	}); err == nil {
		t.Fatal("missing original initialization admitted a decoder")
	}
	if err := os.WriteFile(streamPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	assertAuthoredSTCEpochs(t, root, bdmv, data, input)
}

func assertAuthoredSTCEpochs(t *testing.T, root, bdmv string, playlist []byte, repeated filesystem.ResolvedComparisonInput) {
	t.Helper()
	clipName := filepath.Base(repeated.Occurrences[0].Path)
	clipPath := filepath.Join(bdmv, "CLIPINF", clipName[:len(clipName)-5]+".clpi")
	original, err := os.ReadFile(clipPath)
	if err != nil {
		t.Fatal(err)
	}
	base := int(binary.BigEndian.Uint32(original[8:12]))
	end := base + 4 + int(binary.BigEndian.Uint32(original[base:]))
	if end-base != 26 || original[base+5] != 1 || original[base+10] != 1 {
		t.Fatal("authoring tool changed the single-STC fixture")
	}
	var boundary filesystem.CLPIEntry
	origin := repeated.Occurrences[0].STC.PresentationStart45
	for _, entry := range repeated.Occurrences[0].CPI {
		if entry.PTS45 >= int64(origin)+40*45000 {
			boundary = entry
			break
		}
	}
	if boundary.Packet == 0 || boundary.PTS45 >= int64(origin)+48*45000 {
		t.Fatal("missing independent epoch boundary")
	}
	// Partition the independently authored stream into two disjoint STC packet
	// domains. Their source clocks stay native; each playlist trim starts a new
	// title-relative interval, including the later STC's nonzero physical origin.
	data := bytes.Clone(original[:end])
	data = append(data, original[base+12:end]...)
	data = append(data, original[end:]...)
	binary.BigEndian.PutUint32(data[base:], 36)
	data[base+10] = 2
	binary.BigEndian.PutUint32(data[base+22:], uint32(boundary.PTS45))
	binary.BigEndian.PutUint32(data[end+2:], boundary.Packet)
	binary.BigEndian.PutUint32(data[end+6:], uint32(boundary.PTS45))
	for _, offset := range []int{12, 16, 20, 24} {
		value := binary.BigEndian.Uint32(data[offset:])
		if value >= uint32(end) {
			binary.BigEndian.PutUint32(data[offset:], value+14)
		}
	}
	if err := os.WriteFile(clipPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(clipPath, original, 0o600); err != nil {
			t.Error(err)
		}
	}()
	list := bytes.Clone(playlist)
	start := int(binary.BigEndian.Uint32(list[8:12])) + 10
	second := start + int(binary.BigEndian.Uint16(list[start:])) + 2
	list[second+13] = 1
	binary.BigEndian.PutUint32(list[second+14:], origin+48*45000)
	binary.BigEndian.PutUint32(list[second+18:], origin+53*45000)
	if err := os.WriteFile(filepath.Join(bdmv, "PLAYLIST", "00001.mpls"), list, 0o600); err != nil {
		t.Fatal(err)
	}
	discovery, err := filesystem.DiscoverComparisonProgrammes(t.Context(), bdmv)
	if err != nil {
		t.Fatal(err)
	}
	var input filesystem.ResolvedComparisonInput
	for _, candidate := range discovery.Inputs {
		if candidate.Playlist == "00001.mpls" {
			input = candidate
		}
	}
	input, err = filesystem.ResolveComparisonProgramme(t.Context(), input)
	if err != nil || len(input.Occurrences) != 2 || input.Occurrences[0].STC.ID == input.Occurrences[1].STC.ID {
		t.Fatalf("distinct STC mapping: %+v %v", input.Occurrences, err)
	}
	service := screenshots.NewService(config.Config{}, api.NopLogger{}, root, nil)
	scratch, err := comparisonScratchDir(filepath.Join(root, "tmp"), filepath.Join(root, "main.mkv"), "epochs")
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.InspectResolvedComparisonSource(t.Context(), input, "authored-epochs", scratch)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := service.SampleComparisonSource(t.Context(), screenshots.ComparisonSampleRequest{
		Source:          source,
		StartSeconds:    5,
		DurationSeconds: 1,
		StrideSeconds:   .002,
	})
	if err != nil || len(frames) == 0 {
		t.Fatalf("later STC native decoding: %v", err)
	}
	frame := frames[0]
	assertOriginalComparisonPicture(t, filepath.Join(root, "source.h264"), 1151, source, frame)
	captured, err := service.ComparisonCapture(t.Context(), screenshots.ComparisonCaptureRequest{
		Source:           source,
		TimestampSeconds: frame.RelativeTimestampSeconds,
		Replay:           frame.Replay,
	})
	if err != nil || captured.FrameID != frame.FrameID || !captured.Replay.SamePicture(frame.Replay) {
		t.Fatalf("later STC exact replay: %v", err)
	}
}
