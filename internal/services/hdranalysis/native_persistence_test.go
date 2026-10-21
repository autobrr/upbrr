// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"

	"github.com/Audionut/go-hdr10-plus/extract"

	"github.com/autobrr/upbrr/pkg/api"
)

type failingNativeClose struct {
	io.ReadCloser
	err error
}

func (r failingNativeClose) Close() error { return errors.Join(r.ReadCloser.Close(), r.err) }

func TestNativeRestorePreservesCloseFailure(t *testing.T) {
	want := errors.New("synthetic close failure")
	err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		native, err := session.ExtractNativeFile(t.Context(), filepath.Join("testdata", "reordered-hybrid.mkv"), api.HDRAnalysisFormatsBoth)
		if err != nil {
			return err
		}
		defer native.Release()
		evidence, err := native.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if err != nil {
			return err
		}
		defer evidence.Release()
		directory := t.TempDir()
		manifest, _, err := PublishNativeEvidence(t.Context(), directory, testIdentity(), api.HDRAnalysisFormatsBoth, evidence)
		if err != nil {
			return err
		}
		restored, err := RestoreNativeEvidence(t.Context(), manifest, func(key string) (io.ReadCloser, error) {
			input, err := os.Open(filepath.Join(directory, key+".json"))
			if err != nil {
				return nil, fmt.Errorf("open test section: %w", err)
			}
			return failingNativeClose{ReadCloser: input, err: want}, nil
		})
		if restored != nil || !errors.Is(err, want) {
			t.Fatalf("restore = %v, %v; want original close failure", restored, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeEvidencePersistence(t *testing.T) {
	for _, source := range []string{"reordered-hybrid.hevc", "reordered-hybrid.mkv"} {
		for _, formats := range []api.HDRAnalysisFormats{api.HDRAnalysisFormatsBoth, api.HDRAnalysisFormatsHDR10Plus, api.HDRAnalysisFormatsDolbyVision} {
			t.Run(source+"/"+string(formats), func(t *testing.T) {
				err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
					native, err := session.ExtractNativeFile(t.Context(), filepath.Join("testdata", source), formats)
					if err != nil {
						return err
					}
					defer native.Release()
					if (native.TrackID == 0) != (filepath.Ext(source) == ".hevc") {
						t.Fatal("producer track identity disagrees with container")
					}
					evidence, err := native.Evidence(formats, api.HDRSourceEvidence{})
					if err != nil {
						return err
					}
					defer evidence.Release()
					identity := testIdentity()
					identity.ResolvedTrackID = native.TrackID
					if evidence.HDR10Plus != nil {
						var legacy bytes.Buffer
						if err := WriteSidecar(t.Context(), &legacy, Sidecar{Identity: identity, Extraction: evidence.HDR10Plus}); (err != nil) != (native.TrackID == 0) {
							t.Fatalf("legacy sidecar track validation: %v", err)
						}
					}
					if evidence.HDR10Plus != nil {
						wrong := identity
						wrong.ResolvedTrackID = 1
						if identity.ResolvedTrackID != 0 {
							wrong.ResolvedTrackID = 0
						}
						var invalid bytes.Buffer
						if err := writeSidecar(t.Context(), &invalid, Sidecar{Identity: wrong, Extraction: evidence.HDR10Plus}, true); err == nil {
							t.Fatal("native writer accepted another track identity")
						}
					}
					directory := t.TempDir()
					manifest, _, err := PublishNativeEvidence(t.Context(), directory, identity, formats, evidence)
					if err != nil {
						return err
					}
					restored, err := RestoreNativeEvidence(t.Context(), manifest, func(key string) (io.ReadCloser, error) {
						file, err := os.Open(filepath.Join(directory, key+".json"))
						if err != nil {
							return nil, fmt.Errorf("open roundtrip section: %w", err)
						}
						return file, nil
					})
					if err != nil {
						return err
					}
					defer restored.Release()
					for _, pair := range [][2]any{{evidence.HDR10Plus, restored.HDR10Plus}, {evidence.DolbyVision, restored.DolbyVision}} {
						before, err := json.Marshal(pair[0])
						if err != nil {
							return fmt.Errorf("encode original: %w", err)
						}
						after, err := json.Marshal(pair[1])
						if err != nil {
							return fmt.Errorf("encode restored: %w", err)
						}
						if !bytes.Equal(before, after) {
							t.Fatal("native roundtrip changed raw evidence")
						}
					}
					if manifest.Identity.ResolvedTrackID != native.TrackID {
						t.Fatal("raw identity changed")
					}
					if evidence.HDR10Plus != nil {
						data, err := os.ReadFile(filepath.Join(directory, "hdr10plus.json"))
						if err != nil {
							return fmt.Errorf("read native section: %w", err)
						}
						wrong := identity
						wrong.ResolvedTrackID = 1
						if identity.ResolvedTrackID != 0 {
							wrong.ResolvedTrackID = 0
						}
						if _, err := readSidecarWithBudget(t.Context(), bytes.NewReader(data), wrong, MaxRestoreBytes, true); err == nil {
							t.Fatal("native restore accepted another track identity")
						}
						if _, err := ReadSidecar(t.Context(), bytes.NewReader(data), identity); err == nil {
							t.Fatal("legacy reader accepted raw native sidecar")
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
}

func TestNativeRestoreAcceptsLegacyContainerSection(t *testing.T) {
	var encoded bytes.Buffer
	if err := WriteSidecar(t.Context(), &encoded, Sidecar{Identity: testIdentity(), Extraction: testExtraction()}); err != nil {
		t.Fatal(err)
	}
	restored, err := readSidecarWithBudget(t.Context(), bytes.NewReader(encoded.Bytes()), testIdentity(), MaxRestoreBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Extraction.Release()
	if restored.Identity != testIdentity() || len(restored.Extraction.Frames) != len(testExtraction().Frames) {
		t.Fatal("historical native container section changed")
	}
}

func TestNativeRestorePreservesDVReadFailure(t *testing.T) {
	for _, mode := range []string{"file-both", "disc-both", "disc-dv"} {
		t.Run(mode, func(t *testing.T) {
			formats := api.HDRAnalysisFormatsBoth
			if mode == "disc-dv" {
				formats = api.HDRAnalysisFormatsDolbyVision
			}
			err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				native, err := session.ExtractNativeFile(t.Context(), filepath.Join("testdata", "reordered-hybrid.mkv"), formats)
				if err != nil {
					return err
				}
				defer native.Release()
				evidence, err := native.Evidence(formats, api.HDRSourceEvidence{})
				if err != nil {
					return err
				}
				defer evidence.Release()
				identity := testIdentity()
				directory := t.TempDir()
				manifest, _, err := PublishNativeEvidence(t.Context(), directory, identity, formats, evidence)
				if err != nil {
					return err
				}
				if mode != "file-both" {
					manifest.Identity.SelectionPolicy = "primary_hevc_angle_zero"
					if manifest.HDR10Plus != nil {
						disc, err := DiscExtraction(discOutcomeFixture(), "00001.MPLS")
						if err != nil {
							return err
						}
						defer disc.Extraction.Release()
						disc.Identity = manifest.Identity
						var encoded bytes.Buffer
						if err := writeSidecar(t.Context(), &encoded, disc, true); err != nil {
							return err
						}
						if err := os.WriteFile(filepath.Join(directory, "hdr10plus.json"), encoded.Bytes(), 0o600); err != nil {
							return fmt.Errorf("write test disc HDR section: %w", err)
						}
						hash := sha256.Sum256(encoded.Bytes())
						manifest.HDR10Plus.Size = int64(encoded.Len())
						manifest.HDR10Plus.SHA256 = hex.EncodeToString(hash[:])
					}
				}
				for _, want := range []error{io.ErrUnexpectedEOF, extract.ErrResourceLimit, context.Canceled} {
					t.Run(want.Error(), func(t *testing.T) {
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						closeErr := errors.New("synthetic DV close failure")
						restored, err := RestoreNativeEvidence(ctx, manifest, func(key string) (io.ReadCloser, error) {
							if key == "dolby-vision" {
								if errors.Is(want, context.Canceled) {
									cancel()
								}
								return failingNativeClose{ReadCloser: io.NopCloser(iotest.ErrReader(want)), err: closeErr}, nil
							}
							input, err := os.Open(filepath.Join(directory, key+".json"))
							if err != nil {
								return nil, fmt.Errorf("open test HDR section: %w", err)
							}
							return input, nil
						})
						if restored != nil || !errors.Is(err, want) || !errors.Is(err, closeErr) {
							t.Fatalf("restore = %v, %v; want read %v and close %v", restored, err, want, closeErr)
						}
					})
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
