// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedHDRCapturePreservesTypedFailuresAndOrdinaryReport(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome *bridge.Outcome
		code    api.HDRAnalysisFailureCode
	}{
		{name: "incomplete", code: api.HDRAnalysisFailureIncomplete},
		{
			name:    "collector quota",
			outcome: &bridge.Outcome{Report: &bd.Result{Report: "Ordinary report"}, DeclinedAfterExhaustion: 1},
			code:    api.HDRAnalysisFailureResourceLimit,
		},
		{
			name:    "unsupported mapping",
			outcome: &bridge.Outcome{Report: &bd.Result{Report: "Ordinary report", Timelines: []bd.PlaylistTimeline{{Name: "00001.MPLS"}, {Name: "00002.MPLS"}}}},
			code:    api.HDRAnalysisFailureUnsupportedInput,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resource := preparationstate.DiscResource{Root: t.TempDir(), SelectedPlaylists: []api.PlaylistInfo{{File: "1"}, {File: "2"}}}
			callback := capturePreparedHDR(t.Context(), &resource, filepath.Join(t.TempDir(), "disc"), "source")
			if err := callback(test.outcome); err == nil {
				t.Fatal("capture failure was not reported")
			}
			for _, name := range []string{"00001.MPLS", "00002.MPLS"} {
				capture := resource.HDRCaptures[name]
				if capture.Failure == nil || capture.Failure.Code != test.code || capture.SourceFingerprint != "source" || capture.Path != "" ||
					capture.Directory != "" {
					t.Fatalf("attempted capture was dropped: %#v", capture)
				}
			}
			if test.outcome != nil && test.outcome.Report.Report != "Ordinary report" {
				t.Fatal("HDR capture changed ordinary report")
			}
		})
	}
}

func TestUncachedCombinedReportRetainsActualNativeCaptureFailure(t *testing.T) {
	root := hdrDiscFixture(t, 0x1b, nil)
	artifacts := t.TempDir()
	resource := preparationstate.DiscResource{Root: root, SelectedPlaylists: []api.PlaylistInfo{{File: "00001.MPLS"}}}
	hdr := hdranalysis.New(hdranalysis.NewAdmission(), api.NopLogger{})
	bdService := bdinfo.New(api.NopLogger{})
	bdService.SetHDRService(hdr)
	service := &Service{bdinfo: bdService, logger: api.NopLogger{}}
	reads := 0
	ctx := hdranalysis.WithProgress(t.Context(), func(progress hdranalysis.Progress) {
		if progress.Phase == "reading_metadata" {
			reads++
		}
	})
	ctx = bdinfo.WithHDRCapture(ctx, capturePreparedHDR(ctx, &resource, artifacts, "source"))
	path, scanned, err := service.resolveOrCreateBDMVSummaries(ctx, api.PrepareInput{}, artifacts, root, []string{"00001.MPLS"})
	if err != nil || !scanned || reads != 1 {
		t.Fatalf("combined report scan=%v reads=%d err=%v", scanned, reads, err)
	}
	if content, err := os.ReadFile(path); err != nil || len(content) == 0 {
		t.Fatalf("ordinary report was lost: %v", err)
	}
	capture := resource.HDRCaptures["00001.MPLS"]
	if capture.Failure == nil || capture.Failure.Code != api.HDRAnalysisFailureUnsupportedInput {
		t.Fatalf("native unsupported mapping was dropped: %#v", capture)
	}
	if _, scanned, err := service.resolveOrCreateBDMVSummaries(
		ctx,
		api.PrepareInput{},
		artifacts,
		root,
		[]string{"00001.MPLS"},
	); err != nil || scanned ||
		reads != 1 {
		t.Fatalf("cached report triggered capture scan: scanned=%v reads=%d err=%v", scanned, reads, err)
	}
	if _, scanned, err := service.resolveOrCreateBDMVSummaries(
		ctx,
		api.PrepareInput{Controls: api.PreparationControls{CaptureHDRMetadata: true}},
		artifacts,
		root,
		[]string{"00001.MPLS"},
	); err != nil || !scanned ||
		reads != 2 {
		t.Fatalf("explicit HDR check skipped cached report scan: scanned=%v reads=%d err=%v", scanned, reads, err)
	}
}

func TestHDRProvisionalCaptureRecoversAfterNativePreparationExit(t *testing.T) {
	const helperEnv = "UPBRR_HDR_CAPTURE_RECOVERY_HELPER"
	if os.Getenv(helperEnv) == "1" {
		payload, err := os.ReadFile(filepath.Join("testdata", "synthetic-hdr-absent.hevc"))
		if err != nil {
			t.Fatal(err)
		}
		root := hdrDiscFixture(t, 0x24, payload)
		artifacts := os.Getenv("UPBRR_HDR_CAPTURE_STAGE")
		resource := preparationstate.DiscResource{Root: root, SelectedPlaylists: []api.PlaylistInfo{{File: "00001.MPLS"}}}
		service := bdinfo.New(api.NopLogger{})
		service.SetHDRService(hdranalysis.New(hdranalysis.NewAdmission(), api.NopLogger{}))
		ctx := bdinfo.WithHDRCapture(t.Context(), capturePreparedHDR(t.Context(), &resource, artifacts, "source"))
		if _, err := service.ExecuteForPlaylist(ctx, root, "00001.MPLS", filepath.Join(artifacts, "report.txt"), false); err != nil {
			t.Fatal(err)
		}
		capture := resource.HDRCaptures["00001.MPLS"]
		if !capture.Absent || capture.Path == "" || capture.Lease == nil || capture.Failure != nil {
			t.Fatalf("native absence was not staged: capture=%#v failure=%+v", capture, capture.Failure)
		}
		// Exit without retiring preparation: the parent exercises a new runtime's sweep.
		return
	}
	root := t.TempDir()
	artifacts := filepath.Join(root, "release", "disc")
	if err := os.MkdirAll(artifacts, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHDRProvisionalCaptureRecoversAfterNativePreparationExit$")
	command.Env = append(os.Environ(), helperEnv+"=1", "UPBRR_HDR_CAPTURE_STAGE="+artifacts)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native staging process failed: %v\n%s", err, output)
	}
	staged, err := filepath.Glob(filepath.Join(artifacts, "hdr-provisional", "capture-*", "metadata.json"))
	if err != nil || len(staged) != 1 {
		t.Fatalf("native staging evidence=%v err=%v", staged, err)
	}
	count, err := preparationstate.CleanupHDRCaptures(t.Context(), root)
	if err != nil || count != 1 {
		t.Fatalf("new runtime did not recover native staging: count=%d err=%v", count, err)
	}
	if _, err := os.Stat(staged[0]); !os.IsNotExist(err) {
		t.Fatalf("abandoned native sidecar remains: %v", err)
	}
}

// Disc metadata fixture adapted from go-bdinfo and its HDR bridge fixtures.
// Copyright (c) 2026, s0up and the autobrr contributors; GPL-2.0-or-later.
func hdrDiscFixture(t *testing.T, codec byte, payload []byte) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"PLAYLIST", "CLIPINF", "STREAM"} {
		if err := os.MkdirAll(filepath.Join(root, "BDMV", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sequence := make([]byte, 22)
	sequence[1], sequence[6], sequence[7] = 1, 1, 7
	binary.BigEndian.PutUint16(sequence[8:], 0x1001)
	binary.BigEndian.PutUint32(sequence[18:], 45000*1800)
	program := make([]byte, 20)
	program[1], program[8], program[10], program[11], program[12] = 1, 1, 0x10, 0x11, 4
	program[13], program[14], program[15] = codec, 0x61, 0x30
	clpi := make([]byte, 0, 64+len(sequence)+4+len(program))
	clpi = append(clpi, make([]byte, 60)...)
	copy(clpi, "HDMV0200")
	binary.BigEndian.PutUint32(clpi[8:], 60)
	packetCount := uint32(1)
	if codec == 0x24 {
		packetCount = 3
	}
	binary.BigEndian.PutUint32(clpi[56:], packetCount)
	clpi = append(clpi, 0, 0, 0, 22)
	clpi = append(clpi, sequence...)
	binary.BigEndian.PutUint32(clpi[12:], uint32(len(clpi)))
	clpi = append(clpi, 0, 0, 0, 20)
	clpi = append(clpi, program...)
	stn := make([]byte, 0, 23)
	stn = append(stn, make([]byte, 14)...)
	stn[2] = 1
	attributes := []byte{codec, 0x61, 0x30}
	if codec == 0x24 {
		attributes = append(attributes, 0)
	}
	stn = append(stn, 3, 1, 0x10, 0x11, byte(len(attributes)))
	stn = append(stn, attributes...)
	item := make([]byte, 0, 34+len(stn))
	item = append(item, make([]byte, 32)...)
	copy(item, "00001M2TS")
	item[10], item[11] = 1, 7
	binary.BigEndian.PutUint32(item[16:], 45000*1800)
	item = append(item, 0, byte(len(stn)))
	item = append(item, stn...)
	list := make([]byte, 0, 8+len(item))
	list = append(list, make([]byte, 6)...)
	list[3] = 1
	list = append(list, 0, byte(len(item)))
	list = append(list, item...)
	playlist := make([]byte, 0, 68+len(list)+6)
	playlist = append(playlist, make([]byte, 64)...)
	copy(playlist, "MPLS0200")
	binary.BigEndian.PutUint32(playlist[8:], 64)
	playlist = append(playlist, 0, 0, byte(len(list)>>8), byte(len(list)))
	playlist = append(playlist, list...)
	binary.BigEndian.PutUint32(playlist[12:], uint32(len(playlist)))
	playlist = append(playlist, 0, 0, 0, 2, 0, 0)
	cuts := []int{0, len(payload)}
	if codec == 0x24 {
		var slices []int
		for index := 0; index < len(payload)-5; index++ {
			if payload[index] == 0 && payload[index+1] == 0 && payload[index+2] == 1 && (payload[index+3]>>1)&63 <= 31 {
				slices = append(slices, index)
			}
		}
		if len(slices) != 3 {
			t.Fatal("synthetic fixture must have three reordered pictures")
		}
		cuts = []int{0, slices[1], slices[2], len(payload)}
	}
	var transport []byte
	for index := 0; index < len(cuts)-1; index++ {
		pts := []uint64{0, 6000, 3000}[index]
		stamp := []byte{0x21 | byte((pts>>29)&0xe), byte(pts >> 22), byte((pts>>14)&0xfe) | 1, byte(pts >> 7), byte((pts<<1)&0xfe) | 1}
		body := payload[cuts[index]:cuts[index+1]]
		pes := []byte{0, 0, 1, 0xe0, 0, byte(len(body) + 8), 0x80, 0x80, 5}
		pes = append(pes, stamp...)
		pes = append(pes, body...)
		packet := make([]byte, 192)
		packet[4], packet[5], packet[6], packet[7], packet[8] = 0x47, 0x50, 0x11, 0x30|byte(index), byte(183-len(pes))
		for offset := 10; offset < len(packet)-len(pes); offset++ {
			packet[offset] = 0xff
		}
		copy(packet[len(packet)-len(pes):], pes)
		transport = append(transport, packet...)
	}
	for _, file := range []struct {
		directory, name string
		data            []byte
	}{
		{
			directory: "CLIPINF",
			name:      "00001.clpi",
			data:      clpi,
		},
		{
			directory: "PLAYLIST",
			name:      "00001.mpls",
			data:      playlist,
		},
		{
			directory: "STREAM",
			name:      "00001.m2ts",
			data:      transport,
		},
	} {
		if err := os.WriteFile(filepath.Join(root, "BDMV", file.directory, file.name), file.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
