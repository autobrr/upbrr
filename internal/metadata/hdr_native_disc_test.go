// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	dovi "github.com/Audionut/go-dovi-extract"
	"github.com/Audionut/go-hdr10-plus/extract"
	bridge "github.com/Audionut/go-hdr10-plus/integration/bdinfo"
	bd "github.com/autobrr/go-bdinfo/pkg/bdinfo"

	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNativeDiscSharesReportAndPreservesHDRWhenDVFails(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "hybrid", true: "invalid-dv"}[corrupt], func(t *testing.T) {
			absent, err := os.ReadFile(filepath.Join("testdata", "synthetic-hdr-absent.hevc"))
			if err != nil {
				t.Fatal(err)
			}
			root := hdrDiscFixture(t, 0x24, absent)
			transport, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "reordered-hybrid.m2ts"))
			if err != nil {
				t.Fatal(err)
			}
			if corrupt {
				at := bytes.Index(transport, []byte{0, 0, 1, 0x7c, 1})
				if at < 0 {
					t.Fatal("fixture has no RPU")
				}
				transport[at+10] ^= 1
			}
			if err := os.WriteFile(filepath.Join(root, "BDMV", "STREAM", "00001.m2ts"), transport, 0o600); err != nil {
				t.Fatal(err)
			}
			clpiPath := filepath.Join(root, "BDMV", "CLIPINF", "00001.clpi")
			clpi, err := os.ReadFile(clpiPath)
			if err != nil {
				t.Fatal(err)
			}
			binary.BigEndian.PutUint32(clpi[56:], uint32(len(transport)/192))
			if err := os.WriteFile(clpiPath, clpi, 0o600); err != nil {
				t.Fatal(err)
			}
			playlist, err := os.ReadFile(filepath.Join(root, "BDMV", "PLAYLIST", "00001.mpls"))
			if err != nil { t.Fatal(err) }
			if err := os.WriteFile(filepath.Join(root, "BDMV", "PLAYLIST", "00002.mpls"), playlist, 0o600); err != nil { t.Fatal(err) }
			options := bd.Options{
				Path:            root,
				Settings:        bd.DefaultSettings(t.TempDir()),
				IncludeTimeline: true,
			}
			reference, err := bridge.Scan(t.Context(), bridge.Options{BDInfo: options})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				for _, p := range reference.Playlists {
					p.Extraction.Release()
				}
			}()
			service := hdranalysis.New(hdranalysis.NewAdmission(), nil)
			err = service.WithSession(t.Context(), func(session *hdranalysis.Session) error {
				// Another root's live results remain charged while this root is scanned.
				if out, err := session.ScanNativeDisc(t.Context(), options, api.HDRAnalysisFormatsBoth, hdranalysis.DiscScanRetainedBytes-128<<20, "00001.MPLS"); out != nil || !errors.Is(err, extract.ErrResourceLimit) {
					t.Fatalf("second root ignored retained quota: %+v %v", out, err)
				}
				out, scanErr := session.ScanNativeDisc(t.Context(), options, api.HDRAnalysisFormatsBoth, 64<<20, "00001.MPLS")
				if scanErr != nil {
					return fmt.Errorf("native disc verification: %w", scanErr)
				}
				defer out.Release()
				if len(out.Playlists) != 1 || len(out.DolbyVision) != 1 || out.Playlists[0].Name != "00001.MPLS" {
					t.Fatalf("retry assembled unrequested retained playlist: %+v %+v", out.Playlists, out.DolbyVision)
				}
				if out.Report.Report != reference.Report.Report || out.Report.QuickSummary != reference.Report.QuickSummary || len(out.Report.Collection) != 1 {
					t.Fatal("shared collection changed ordinary report")
				}
				native, timeline, takeErr := out.TakePlaylist("00001.MPLS", 64<<20)
				if takeErr != nil {
					return fmt.Errorf("native disc verification: %w", takeErr)
				}
				defer native.Release()
				if out.RetainedBytes() != 128<<20 { t.Fatalf("taken playlist kept owned extraction bytes: %d", out.RetainedBytes()) }
				out.Release()
				if out.RetainedBytes() != 0 { t.Fatal("released disc kept report accounting") }
				if native.HDR10PlusError != nil || native.HDR10Plus == nil || len(timeline) != 1 {
					t.Fatalf("HDR result: %v timeline=%d", native.HDR10PlusError, len(timeline))
				}
				want := reference.Playlists[0].Extraction
				if !reflect.DeepEqual(native.HDR10Plus.Frames, want.Frames) || !reflect.DeepEqual(native.HDR10Plus.Payloads, want.Payloads) {
					t.Fatal("HDR metadata changed")
				}
				if corrupt {
					if native.DolbyVisionError == nil || native.DolbyVision != nil {
						t.Fatal("invalid DV completed")
					}
					return nil
				}
				if native.DolbyVisionError != nil || native.DolbyVision == nil {
					t.Fatalf("DV result: %v", native.DolbyVisionError)
				}
				a, modelErr := native.DolbyVision.Analysis(dovi.AnalysisOptions{})
				if modelErr != nil {
					return fmt.Errorf("native disc verification: %w", modelErr)
				}
				if a.Timeline != dovi.PlaylistFrames || len(a.Pictures) != 3 || len(a.Records) != 3 || a.ObservedRPUs != 3 {
					t.Fatalf("DV model: %#v", a)
				}
				for i, au := range []uint64{0, 2, 1} {
					if a.Pictures[i].AUOrdinal != au || !a.Pictures[i].HasRPU {
						t.Fatal("DV association shifted")
					}
				}
				identity := hdranalysis.ExtractionIdentity{
					SourceFingerprint: "authored",
					TargetID:          api.HDRTargetID(root, "00001.MPLS"),
					SelectionPolicy:   "primary_hevc_angle_zero",
					ResolvedTrackID:   native.TrackID,
				}
				var encoded bytes.Buffer
				if err := hdranalysis.WriteDVSidecar(t.Context(), &encoded, hdranalysis.DVSidecar{
					Identity: identity,
					Analysis: a,
					Timeline: timeline,
				}); err != nil {
					return fmt.Errorf("native disc verification: %w", err)
				}
				restored, err := hdranalysis.ReadDVSidecar(t.Context(), bytes.NewReader(encoded.Bytes()), identity, hdranalysis.MaxRestoreBytes)
				if err != nil {
					return fmt.Errorf("native disc verification: %w", err)
				}
				if !reflect.DeepEqual(a, restored.Analysis) || !reflect.DeepEqual(timeline, restored.Timeline) {
					t.Fatal("playlist persistence changed exact evidence")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeDiscFormatSelectionAndHDRFailureIsolation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		formats api.HDRAnalysisFormats
		badHDR  bool
		late    bool
	}{
		{name: "auto", formats: api.HDRAnalysisFormatsAuto},
		{name: "hdr-only", formats: api.HDRAnalysisFormatsHDR10Plus},
		{name: "dv-only", formats: api.HDRAnalysisFormatsDolbyVision},
		{
			name:    "invalid-hdr",
			formats: api.HDRAnalysisFormatsBoth,
			badHDR:  true,
		},
		{
			name:    "late-selected-playlist",
			formats: api.HDRAnalysisFormatsBoth,
			late:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			absent, err := os.ReadFile(filepath.Join("testdata", "synthetic-hdr-absent.hevc"))
			if err != nil {
				t.Fatal(err)
			}
			root := hdrDiscFixture(t, 0x24, absent)
			transport, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "reordered-hybrid.m2ts"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.badHDR {
				at := bytes.Index(transport, []byte{0xb5, 0, 0x3c, 0, 1, 4, 1})
				if at < 0 {
					t.Fatal("fixture has no HDR10+ payload")
				}
				transport[at+7] = 1 // zero windows is invalid while the nonzero byte preserves Annex B framing.
			}
			if err := os.WriteFile(filepath.Join(root, "BDMV", "STREAM", "00001.m2ts"), transport, 0o600); err != nil {
				t.Fatal(err)
			}
			clpiPath := filepath.Join(root, "BDMV", "CLIPINF", "00001.clpi")
			clpi, err := os.ReadFile(clpiPath)
			if err != nil {
				t.Fatal(err)
			}
			binary.BigEndian.PutUint32(clpi[56:], uint32(len(transport)/192))
			if err := os.WriteFile(clpiPath, clpi, 0o600); err != nil {
				t.Fatal(err)
			}
			settings := bd.DefaultSettings(t.TempDir())
			playlist := "00001.MPLS"
			var selected []string
			if tc.late {
				data, err := os.ReadFile(filepath.Join(root, "BDMV", "PLAYLIST", "00001.mpls"))
				if err != nil {
					t.Fatal(err)
				}
				for i := 2; i <= 17; i++ {
					if err := os.WriteFile(filepath.Join(root, "BDMV", "PLAYLIST", fmt.Sprintf("%05d.mpls", i)), data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				settings.FilterLoopingPlaylists, settings.FilterShortPlaylists, settings.BigPlaylistOnly = false, false, false
				playlist, selected = "00017.MPLS", []string{"17"}
			}
			service := hdranalysis.New(hdranalysis.NewAdmission(), nil)
			err = service.WithSession(t.Context(), func(session *hdranalysis.Session) error {
				out, err := session.ScanNativeDisc(t.Context(), bd.Options{Path: root, Settings: settings}, tc.formats, 0, selected...)
				if err != nil {
					return fmt.Errorf("scan format selection fixture: %w", err)
				}
				defer out.Release()
				if len(out.Report.Collection) != 1 || out.Report.Report == "" {
					t.Fatal("format selection lost ordinary disc report")
				}
				if tc.late && (len(out.Report.Timelines) != 17 || len(out.Playlists) != 1 || len(out.DolbyVision) != 1) {
					t.Fatalf("assembly selection altered report or retained unrequested targets: timelines=%d hdr=%d dv=%d", len(out.Report.Timelines), len(out.Playlists), len(out.DolbyVision))
				}
				native, timeline, err := out.TakePlaylist(playlist, 0)
				if err != nil {
					return fmt.Errorf("take format selection playlist: %w", err)
				}
				defer native.Release()
				if !native.ScanComplete || len(timeline) != 1 {
					t.Fatalf("successful selected format lost playlist completion: HDR=%v DV=%v timeline=%d", native.HDR10PlusError, native.DolbyVisionError, len(timeline))
				}
				if tc.formats == api.HDRAnalysisFormatsHDR10Plus {
					if native.DolbyVision != nil || native.DolbyVisionError != nil {
						t.Fatal("HDR-only scan produced an unrequested DV outcome")
					}
				} else if native.DolbyVision == nil || native.DolbyVisionError != nil || len(native.DolbyVision.Records) != 3 {
					t.Fatalf("DV evidence lost: %v", native.DolbyVisionError)
				}
				switch {
				case tc.formats == api.HDRAnalysisFormatsDolbyVision:
					if native.HDR10Plus != nil || native.HDR10PlusError != nil {
						t.Fatal("DV-only scan produced an unrequested HDR outcome")
					}
				case tc.badHDR:
					if native.HDR10Plus != nil || native.HDR10PlusError == nil {
						t.Fatal("invalid HDR metadata completed")
					}
				default:
					if native.HDR10Plus == nil || native.HDR10PlusError != nil {
						t.Fatalf("HDR evidence lost: %v", native.HDR10PlusError)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
