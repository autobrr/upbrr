// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	dv "github.com/Audionut/go-dovi-extract/extract"
	"github.com/Audionut/go-hdr10-plus/extract"
	"github.com/autobrr/upbrr/pkg/api"
)

type measuredHDRReader struct {
	*bytes.Reader
	reads []int
}

func (r *measuredHDRReader) Read(p []byte) (int, error) {
	position, _ := r.Seek(0, io.SeekCurrent)
	n, err := r.Reader.Read(p)
	for i := range n {
		r.reads[int(position)+i]++
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("measured read: %w", err)
	}
	return n, nil
}

func TestNativeSharedTraversal(t *testing.T) {
	for _, name := range []string{"single-hybrid.hevc", "reordered-hybrid.hevc", "single-hybrid.mkv", "reordered-hybrid.mkv"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			baselineReader := &measuredHDRReader{Reader: bytes.NewReader(data), reads: make([]int, len(data))}
			hdrBaseline, err := extract.Extract(t.Context(), baselineReader, extract.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer hdrBaseline.Release()
			dvBaseline, err := dv.Extract(t.Context(), bytes.NewReader(data), dv.Options{})
			if err != nil {
				t.Fatal(err)
			}
			reader := &measuredHDRReader{Reader: bytes.NewReader(data), reads: make([]int, len(data))}
			var result *NativeExtraction
			err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				result, err = session.ExtractNative(t.Context(), reader, api.HDRAnalysisFormatsAuto)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			defer result.Release()
			if !result.ScanComplete || result.HDR10PlusError != nil || result.DolbyVisionError != nil {
				t.Fatalf("outcomes: %+v", result)
			}
			if !reflect.DeepEqual(result.HDR10Plus.Frames, hdrBaseline.Frames) || !reflect.DeepEqual(result.HDR10Plus.Payloads, hdrBaseline.Payloads) || !reflect.DeepEqual(result.DolbyVision.Pictures, dvBaseline.Pictures) || !reflect.DeepEqual(result.DolbyVision.Records, dvBaseline.Records) || result.DolbyVision.Timeline != dvBaseline.Timeline || result.DolbyVision.Complete != dvBaseline.Complete || result.DolbyVision.ObservedRPUs != dvBaseline.ObservedRPUs || result.DolbyVision.SuppressedRPUs != dvBaseline.SuppressedRPUs || result.DolbyVision.TrackID != dvBaseline.TrackID {
				t.Fatal("shared traversal changed metadata")
			}
			if !reflect.DeepEqual(reader.reads, baselineReader.reads) {
				t.Fatal("dual extraction must read exactly one producer's physical ranges")
			}
			if len(result.DolbyVision.Pictures) != len(result.HDR10Plus.Frames) {
				t.Fatal("picture count mismatch")
			}
			for _, picture := range result.DolbyVision.Pictures {
				if !picture.HasRPU || result.DolbyVision.Records[picture.RPUIndex].RPU.DM.L1.MaxPQ != uint16(1000+picture.AUOrdinal) {
					t.Fatal("RPU associated with wrong picture")
				}
			}
		})
	}
}

func TestNativeConsumerFailureIsolation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "single-hybrid.hevc"))
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the final RPU's CRC without changing the Annex B framing.
	data[len(data)-2] ^= 0x10
	var result *NativeExtraction
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		result, err = session.ExtractNative(t.Context(), bytes.NewReader(data), api.HDRAnalysisFormatsBoth)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Release()
	if !result.ScanComplete || result.HDR10Plus == nil || result.HDR10PlusError != nil || result.DolbyVision != nil || result.DolbyVisionError == nil {
		t.Fatalf("outcomes: %+v", result)
	}
}

func TestNativeCanceledReadInvalidatesResults(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := New(NewAdmission(), nil).WithSession(ctx, func(*Session) error { t.Fatal("entered canceled session"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestNativeFileRetainsLastCollectorFailure(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "single-hybrid.hevc"))
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-2] ^= 0x10
	// A following NAL makes the corrupt RPU fail during delivery, before EOF.
	data = append(data, 0, 0, 1, 0x46, 1, 0x50)
	path := filepath.Join(t.TempDir(), "Synthetic.Corrupt.mkv")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		result, err := session.ExtractNativeFile(t.Context(), path, api.HDRAnalysisFormatsDolbyVision)
		if err != nil {
			return err
		}
		defer result.Release()
		if result.ScanComplete || result.DolbyVisionError == nil || result.HDR10PlusError != nil {
			t.Fatalf("terminal parser outcomes: %+v", result)
		}
		evidence, err := result.Evidence(api.HDRAnalysisFormatsDolbyVision, api.HDRSourceEvidence{})
		if err != nil {
			return err
		}
		defer evidence.Release()
		if evidence.Results[1].Failure == nil || evidence.Results[1].Failure.Code != api.HDRAnalysisFailureInvalidBitstream {
			t.Fatalf("parser failure was lost: %+v", evidence.Results[1])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeFileClassifiesInsufficientCollectorQuota(t *testing.T) {
	for _, formats := range []api.HDRAnalysisFormats{api.HDRAnalysisFormatsHDR10Plus, api.HDRAnalysisFormatsDolbyVision, api.HDRAnalysisFormatsBoth} {
		t.Run(string(formats), func(t *testing.T) {
			err := New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				result, err := session.ExtractNativeFileWithRetained(t.Context(), filepath.Join("testdata", "single-hybrid.hevc"), formats, MKVRetainedBytes-producerBytes-2)
				if result != nil || !errors.Is(err, extract.ErrResourceLimit) {
					t.Fatalf("insufficient collector quota: result=%+v error=%v", result, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeFilePreservesBothCollectorRejections(t *testing.T) {
	for _, container := range []string{"hevc", "mkv"} {
		t.Run(container, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "single-hybrid."+container))
			if err != nil { t.Fatal(err) }
			// A forbidden header bit is rejected by both parsers. In MKV this
			// occurs in the codec configuration while constructing consumers.
			at := bytes.Index(data, []byte{0x40, 1, 0x2c, 1, 0xff, 0xff, 2})
			if at < 0 { t.Fatal("authored VPS missing") }
			data[at] |= 0x80
			path := filepath.Join(t.TempDir(), "Synthetic.Unsupported."+container)
			if err := os.WriteFile(path, data, 0o600); err != nil { t.Fatal(err) }
			err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				result, err := session.ExtractNativeFile(t.Context(), path, api.HDRAnalysisFormatsBoth)
				if err != nil { return err }
				defer result.Release()
				if result.ScanComplete || result.HDR10PlusError == nil || result.DolbyVisionError == nil { t.Fatalf("collector outcomes lost: %+v", result) }
				evidence, err := result.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
				if err != nil { return err }
				defer evidence.Release()
				for _, outcome := range evidence.Results {
					if outcome.State != api.HDRExtractionFailed || outcome.Failure == nil { t.Fatalf("typed rejection lost: %+v", outcome) }
				}
				return nil
			})
			if err != nil { t.Fatal(err) }
		})
	}
}

func TestNativeContainerSharedTraversal(t *testing.T) {
	for _, name := range []string{"reordered-hybrid.mp4", "reordered-hybrid-fragmented.mp4", "reordered-hybrid-edited.mp4", "reordered-hybrid.ts", "reordered-hybrid.m2ts", "reordered-hybrid.wrapped.m2ts", "reordered-hybrid.split-pes.ts", "reordered-hybrid.one-pes.ts"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			baselineReader := &measuredHDRReader{Reader: bytes.NewReader(data), reads: make([]int, len(data))}
			baseline, err := extract.Extract(t.Context(), baselineReader, extract.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer baseline.Release()
			reader := &measuredHDRReader{Reader: bytes.NewReader(data), reads: make([]int, len(data))}
			err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
				result, err := session.ExtractNative(t.Context(), reader, api.HDRAnalysisFormatsBoth)
				if err != nil {
					return err
				}
				defer result.Release()
				if result.HDR10PlusError != nil || result.DolbyVisionError != nil || !result.ScanComplete || result.HDR10Plus == nil || result.DolbyVision == nil {
					t.Fatalf("MP4 outcomes: %+v", result)
				}
				if !reflect.DeepEqual(result.HDR10Plus.Frames, baseline.Frames) || !reflect.DeepEqual(result.HDR10Plus.Payloads, baseline.Payloads) {
					t.Fatal("MP4 HDR reference differs")
				}
				expectedPictures := 3
				if name == "reordered-hybrid-edited.mp4" {
					expectedPictures = 1
					picture := result.DolbyVision.Pictures[0]
					if picture.AUOrdinal != 2 || picture.PTS.Value != 1003 || picture.PTS.Timescale != 1000 || result.DolbyVision.SuppressedRPUs != 2 {
						t.Fatalf("edited presentation evidence: %+v", result.DolbyVision)
					}
				}
				if len(result.DolbyVision.Pictures) != expectedPictures || !reflect.DeepEqual(reader.reads, baselineReader.reads) {
					t.Fatal("MP4 dual extraction changed picture count or repeated payload reads")
				}
				for index, picture := range result.DolbyVision.Pictures {
					h := baseline.Frames[index]
					if picture.AUOrdinal != h.AUOrdinal || !picture.HasRPU || result.DolbyVision.Records[picture.RPUIndex].RPU.DM.L1.MaxPQ != uint16(1000+picture.AUOrdinal) {
						t.Fatal("MP4 RPU association changed")
					}
					if picture.PTS.Valid != h.PTS.Valid {
						t.Fatal("container timing presence changed")
					}
					if !h.PTS.Valid {
						continue
					}
					dvTime := new(big.Rat).SetFrac(big.NewInt(picture.PTS.Value), new(big.Int).SetUint64(picture.PTS.Timescale))
					hdrTime := new(big.Rat).SetFrac(big.NewInt(h.PTS.Value), new(big.Int).SetUint64(h.PTS.Timescale))
					if dvTime.Cmp(hdrTime) != 0 {
						t.Fatal("MP4 exact presentation timing changed")
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
