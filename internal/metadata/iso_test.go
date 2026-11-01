// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestISOAdmissionRequiresEverySelectedMVCReport(t *testing.T) {
	report := preparationstate.DiscReportResource{
		Playlist: api.PlaylistInfo{File: "00800.MPLS"},
		Summary:  "Playlist: 00800.MPLS\nVideo: MPEG-4 AVC Video / 20000 kbps / 1080p / 23.976 fps\nVideo: MPEG-4 MVC Video / 10000 kbps / Right Eye / 1080p / 23.976 fps\n",
	}
	if err := validateISOReports([]preparationstate.DiscReportResource{report}); err != nil {
		t.Fatal(err)
	}
	for _, summary := range []string{
		"Playlist: 00800.MPLS\nVideo: MPEG-4 AVC Video / 20000 kbps / 1080p / 23.976 fps\n",
		"Playlist: 00001.MPLS\nVideo: MPEG-4 MVC Video / 10000 kbps / Right Eye / 1080p / 23.976 fps\n",
		"Example.3D.iso\nSSIF\n3D\n",
	} {
		rejected := report
		rejected.Summary = summary
		if err := validateISOReports([]preparationstate.DiscReportResource{report, rejected}); err == nil {
			t.Fatalf("accepted unsupported or mismatched selection: %q", summary)
		}
	}
	if err := validateISOReports(nil); err == nil {
		t.Fatal("missing evidence accepted")
	}
}

type isoMediaInfoTrap struct{ calls int }

func (m *isoMediaInfoTrap) Export(context.Context, mediainfo.Request) (mediainfo.Result, error) {
	m.calls++
	return mediainfo.Result{}, errors.New("ISO must not reach MediaInfo")
}

func TestISOCollectionPreservesSourceAndRevalidatesReplacedEvidence(t *testing.T) {
	oldDiscover, oldExecute := discoverBDMVPlaylists, executePlaylistBDInfo
	t.Cleanup(func() { discoverBDMVPlaylists, executePlaylistBDInfo = oldDiscover, oldExecute })
	source := filepath.Join(t.TempDir(), "Example.3D.iso")
	if err := os.WriteFile(source, []byte("synthetic original ISO"), 0o600); err != nil {
		t.Fatal(err)
	}
	layout, err := sourcelayout.Resolve(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	discoverBDMVPlaylists = func(context.Context, string) ([]filesystem.PlaylistInfo, error) {
		return []filesystem.PlaylistInfo{{
			File:     "00001.MPLS",
			Duration: 300,
			Score:    300,
		}, {
			File:     "00800.MPLS",
			Duration: 120,
			Score:    120,
			Items:    []filesystem.PlaylistItem{{File: "00001.M2TS"}, {File: "00002.M2TS"}},
		}}, nil
	}
	scans := 0
	mvc := true
	executePlaylistBDInfo = func(_ *bdinfo.Service, _ context.Context, root, playlist, output string, summaryOnly bool) (string, error) {
		scans++
		if root != source || playlist != "00800.MPLS" || summaryOnly {
			t.Fatalf("scan lost explicit ISO selection: root=%q playlist=%q summary=%v", root, playlist, summaryOnly)
		}
		summary := "QUICK SUMMARY:\nPlaylist: 00800.MPLS\nLength: 00:02:00.000\nVideo: MPEG-4 AVC Video / 20000 kbps / 1080p / 23.976 fps\n"
		if mvc {
			summary += "Video: MPEG-4 MVC Video / 10000 kbps / Right Eye / 1080p / 23.976 fps\n"
		}
		return output, os.WriteFile(output, []byte(summary), 0o600)
	}
	repo := &stubRepo{}
	trap := &isoMediaInfoTrap{}
	cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "upbrr.db")}}
	newService := func() *Service {
		return NewService(repo, WithConfig(cfg), WithBDInfoService(bdinfo.New(api.NopLogger{})), WithMediaInfoExporter(trap))
	}
	request := preparationstate.Request{
		Layout:            layout,
		SourceFingerprint: "original",
		Input:             api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{Playlist: api.PlaylistInstruction{Set: true, Selected: []string{"00800.MPLS"}}}},
	}
	collect := func(s *Service) (preparationstate.State, error) {
		meta := preparationstate.State{
			SourcePath: source,
			Paths:      []string{source},
			DiscType:   "BDMV",
		}
		err := s.collectDiscEvidence(t.Context(), request, &meta, func(api.PreparationProgressStatus, string) {})
		return meta, err
	}
	meta, err := collect(newService())
	if err != nil {
		t.Fatal(err)
	}
	if meta.VideoPath != source || len(meta.FileList) != 1 || meta.FileList[0] != source || len(meta.Discs) != 1 || !meta.Discs[0].BluRayISO || trap.calls != 0 {
		t.Fatalf("ISO physical/source/report boundary lost: video=%q files=%v discs=%+v MIcalls=%d", meta.VideoPath, meta.FileList, meta.Discs, trap.calls)
	}
	for _, test := range []struct {
		name       string
		parsed     string
		correction *string
		want       string
	}{
		{name: "selected report", want: "1080p"},
		{
			name:   "parsed value",
			parsed: "720p",
			want:   "720p",
		},
		{
			name:       "manual value",
			correction: new("2160p"),
			want:       "2160p",
		},
		{
			name:       "manual clear",
			correction: new(""),
			want:       "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := meta
			input.Release = api.ReleaseInfo{Title: "Example Film", Resolution: test.parsed}
			input.ReleaseNameOverrides.Resolution = test.correction
			derived, err := newService().deriveMediaFacts(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if derived.Release.Resolution != test.want || derived.ResolvedNaming.Resolution != test.want {
				t.Fatalf("selected ISO resolution: fact=%q naming=%q want=%q", derived.Release.Resolution, derived.ResolvedNaming.Resolution, test.want)
			}
			if test.want != "" && !strings.Contains(derived.ReleaseName, test.want) {
				t.Fatalf("release name %q lacks resolution %q", derived.ReleaseName, test.want)
			}
		})
	}
	request.Input.Instructions.Playlist = api.PlaylistInstruction{}
	if _, err := collect(newService()); err != nil {
		t.Fatalf("remembered choice after service restart: %v", err)
	}
	request.SourceFingerprint = "replacement"
	if _, err := collect(newService()); err == nil {
		t.Fatal("stale remembered choice accepted")
	}
	request.Input.Instructions.Playlist = api.PlaylistInstruction{Set: true, Selected: []string{"00800.MPLS"}}
	mvc = false
	if _, err := collect(newService()); err == nil || !strings.Contains(err.Error(), "MVC report") {
		t.Fatalf("old cached MVC report admitted replacement: %v", err)
	}
	if scans != 2 || trap.calls != 0 {
		t.Fatalf("scans=%d MIcalls=%d", scans, trap.calls)
	}
}
