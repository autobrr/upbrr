// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdranalysis

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"testing"

	dv "github.com/Audionut/go-dovi-extract/extract"
	"github.com/Audionut/go-hdr10-plus/extract"
	"github.com/autobrr/upbrr/pkg/api"
)

type physicalHDRReads struct {
	io.ReadSeeker
	ranges   hash.Hash
	position int64
	bytes    int64
}

func (r *physicalHDRReads) Read(p []byte) (int, error) {
	n, err := r.ReadSeeker.Read(p)
	if n > 0 {
		var interval [16]byte
		binary.LittleEndian.PutUint64(interval[:8], uint64(r.position))
		binary.LittleEndian.PutUint64(interval[8:], uint64(n))
		_, _ = r.ranges.Write(interval[:])
		r.position += int64(n)
		r.bytes += int64(n)
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("measure physical HDR read: %w", err)
	}
	return n, nil
}

func (r *physicalHDRReads) Seek(offset int64, whence int) (int64, error) {
	position, err := r.ReadSeeker.Seek(offset, whence)
	if err == nil {
		r.position = position
	}
	if err != nil {
		return position, fmt.Errorf("measure physical HDR seek: %w", err)
	}
	return position, nil
}

// A private full-file parity run is opt-in; the source path is never logged.
// The normal test suite uses authored fixtures in TestNativeSharedTraversal.
func TestNativeFullFileParity(t *testing.T) {
	path := os.Getenv("UPBRR_HDR_PARITY_SOURCE")
	if path == "" {
		t.Skip("set UPBRR_HDR_PARITY_SOURCE for authorized local full-file verification")
	}
	open := func() (*physicalHDRReads, *fileReader) {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal("cannot open parity input")
		}
		t.Cleanup(func() { _ = file.Close() })
		info, err := file.Stat()
		if err != nil {
			t.Fatal("cannot inspect parity input")
		}
		physical := &physicalHDRReads{ReadSeeker: file, ranges: sha256.New()}
		return physical, newFileReader(t.Context(), physical, info.Size(), api.NopLogger{}, nil)
	}
	digest := func(value any) string {
		h := sha256.New()
		if err := json.MarshalWrite(h, value); err != nil {
			t.Fatal("cannot fingerprint exported native evidence")
		}
		return string(h.Sum(nil))
	}
	hdrReads, hdrReader := open()
	hdr, err := extract.Extract(t.Context(), hdrReader, extract.Options{Limits: extract.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: MKVRetainedBytes}})
	if err != nil {
		t.Fatalf("HDR10+ reference failed: %v", Classify(err))
	}
	hdrDigest := digest(hdr)
	hdr.Release()
	t.Logf("HDR10+ reference completed; physical bytes=%d", hdrReads.bytes)
	_, dvReader := open()
	dolby, err := dv.Extract(t.Context(), dvReader, dv.Options{Limits: dv.Limits{MaxFrames: MaxFrames, MaxRetainedBytes: MKVRetainedBytes}})
	if err != nil {
		t.Fatalf("Dolby Vision reference failed: %v", Classify(err))
	}
	dvDigest := digest(dolby)
	t.Log("Dolby Vision reference completed")
	sharedReads, sharedReader := open()
	err = New(NewAdmission(), nil).WithSession(t.Context(), func(session *Session) error {
		result, extractErr := session.ExtractNative(t.Context(), sharedReader, api.HDRAnalysisFormatsBoth)
		if extractErr != nil {
			return extractErr
		}
		defer result.Release()
		if result.HDR10PlusError != nil || result.DolbyVisionError != nil || !result.ScanComplete {
			t.Fatal("shared extraction did not complete both formats")
		}
		if digest(result.HDR10Plus) != hdrDigest || digest(result.DolbyVision) != dvDigest {
			t.Fatal("shared extraction changed exported native evidence")
		}
		t.Logf("both formats agree with independent references; pictures=%d", len(result.DolbyVision.Pictures))
		evidence, evidenceErr := result.Evidence(api.HDRAnalysisFormatsBoth, api.HDRSourceEvidence{})
		if evidenceErr != nil {
			return evidenceErr
		}
		defer evidence.Release()
		if evidence.DolbyVision == nil || evidence.HDR10Plus == nil {
			t.Fatal("native conversion lost a completed format")
		}
		want := digest(evidence.DolbyVision)
		identity := testIdentity()
		identity.ResolvedTrackID = result.TrackID
		file, fileErr := os.CreateTemp(t.TempDir(), "native-evidence-")
		if fileErr != nil {
			t.Fatal("cannot create private parity evidence")
		}
		defer file.Close()
		if err := WriteDVSidecar(t.Context(), file, DVSidecar{Identity: identity, Analysis: evidence.DolbyVision}); err != nil {
			return err
		}
		size, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			return fmt.Errorf("measure native parity evidence: %w", err)
		}
		evidence.DolbyVision = nil
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("rewind native parity evidence: %w", err)
		}
		var sidecar DVSidecar
		sidecar, err = ReadDVSidecar(t.Context(), file, identity, MaxRestoreBytes-evidence.HDR10Plus.RetainedBytes())
		evidence.DolbyVision = sidecar.Analysis
		if err != nil {
			return err
		}
		if digest(evidence.DolbyVision) != want {
			t.Fatal("full-file retained Dolby Vision evidence changed")
		}
		if err := evidence.ValidateSummary(); err != nil {
			return err
		}
		t.Logf("full Dolby Vision persistence round trip confirmed; encoded bytes=%d", size)
		return nil
	})
	if err != nil {
		t.Fatalf("shared extraction failed: %v", Classify(err))
	}
	if sharedReads.bytes != hdrReads.bytes || string(sharedReads.ranges.Sum(nil)) != string(hdrReads.ranges.Sum(nil)) {
		t.Fatal("shared analysis performed different physical reads from one producer")
	}
	t.Logf("one-producer physical range parity confirmed; bytes=%d", sharedReads.bytes)
}
