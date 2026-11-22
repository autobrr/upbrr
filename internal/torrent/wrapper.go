// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // BitTorrent v1 piece hashes require SHA-1.
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/pkg/api"
)

// Serialize publication so concurrent conversions never replace identical
// artifacts while another operation is reading them.
var convertedTorrentPublicationMu sync.Mutex

type singleFileExtraction struct {
	dictionary map[string]any
	source     SubmissionContentFile
	pieces     []byte
	tailBytes  int64
}

// CanExtractSingleFile reports whether a v1 file set contains a unique matching
// source on a piece boundary. It never reads media bytes or writes artifacts.
func CanExtractSingleFile(infoBytes []byte, subject api.TorrentSubject) bool {
	plan, err := planSingleFileExtraction(infoBytes, subject)
	return err == nil && plan != nil
}

func planSingleFileExtraction(infoBytes []byte, subject api.TorrentSubject) (*singleFileExtraction, error) {
	inventory, ok, err := ResolveSubmissionContentInventory(subject)
	if err != nil || !ok || inventory.Scope != api.SubmissionContentScopeSingleFile || len(inventory.Files) != 1 {
		return nil, err
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	info, err := mi.UnmarshalInfo()
	if err != nil || len(info.Files) == 0 || ValidatePieceMetadata(info) != nil {
		return nil, nil
	}
	source := inventory.Files[0]
	selected := -1
	var offset, start int64
	for index, file := range info.Files {
		parts := file.BestPath()
		if parts[len(parts)-1] == source.TorrentPath && file.Length == source.Size {
			if selected >= 0 {
				return nil, nil
			}
			selected, start = index, offset
		}
		offset += file.Length
	}
	if selected < 0 || start%info.PieceLength != 0 {
		return nil, nil
	}
	var dictionary map[string]any
	if err := bencode.Unmarshal(mi.InfoBytes, &dictionary); err != nil {
		return nil, nil
	}
	files, ok := dictionary["files"].([]any)
	if !ok || len(files) != len(info.Files) {
		return nil, nil
	}
	fileFields, ok := files[selected].(map[string]any)
	if !ok {
		return nil, nil
	}
	for key := range fileFields {
		if key != "length" && key != "path" && key != "path.utf-8" {
			return nil, nil
		}
	}
	count := source.Size / info.PieceLength
	var tailBytes int64
	if remainder := source.Size % info.PieceLength; remainder != 0 {
		count++
		if start+source.Size != offset {
			tailBytes = remainder
		}
	}
	first := start / info.PieceLength
	pieces := slices.Clone(info.Pieces[first*metainfo.HashSize : (first+count)*metainfo.HashSize])
	delete(dictionary, "files")
	dictionary["length"] = source.Size
	dictionary["name"] = source.TorrentPath
	dictionary["pieces"] = pieces
	if _, ok := dictionary["name.utf-8"]; ok {
		dictionary["name.utf-8"] = source.TorrentPath
	}
	return &singleFileExtraction{
		dictionary: dictionary,
		source:     source,
		pieces:     pieces,
		tailBytes:  tailBytes,
	}, nil
}

type extractedTorrent struct {
	path        string
	publication *generatedTorrentPublication
}

// preparedSingleFileExtraction holds validated, captured metainfo so ranking
// alternatives never writes artifacts or hashes media.
type preparedSingleFileExtraction struct {
	metadata   metainfo.MetaInfo
	plan       *singleFileExtraction
	pieceCount int64
	encoded    []byte
}

func prepareSingleFileExtraction(path string, subject api.TorrentSubject, policy *trackerTorrentPolicy) (*preparedSingleFileExtraction, error) {
	data, err := os.ReadFile(path)
	if err != nil || validateClientCandidateBytes(path, data, subject) != nil {
		return nil, nil
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, nil
	}
	plan, err := planSingleFileExtraction(mi.InfoBytes, subject)
	if err != nil || plan == nil {
		return nil, err
	}
	mi.InfoBytes, err = bencode.Marshal(plan.dictionary)
	if err != nil {
		return nil, fmt.Errorf("torrent: encode extracted file: %w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil || policy != nil && policy.validateTorrentInfo(&info, subject) != nil {
		return nil, nil
	}
	if validateReusablePieceCount(&info, policy, subject) != nil {
		return nil, nil
	}
	var output bytes.Buffer
	if err := mi.Write(&output); err != nil {
		return nil, fmt.Errorf("torrent: encode converted metainfo: %w", err)
	}
	if policy != nil && policy.maxTorrentBytes > 0 && int64(output.Len()) > policy.maxTorrentBytes {
		return nil, nil
	}
	return &preparedSingleFileExtraction{
		metadata:   *mi,
		plan:       plan,
		pieceCount: int64(info.NumPieces()),
		encoded:    output.Bytes(),
	}, nil
}

// publish materializes the selected captured extraction. Callers must permit
// hashing before publishing a plan with a nonzero tailBytes value.
func (p *preparedSingleFileExtraction) publish(ctx context.Context, tmpRoot string, subject api.TorrentSubject) (extractedTorrent, error) {
	if strings.TrimSpace(tmpRoot) == "" {
		return extractedTorrent{}, nil
	}
	mi, plan := p.metadata, p.plan
	output := bytes.NewBuffer(p.encoded)
	if err := ctx.Err(); err != nil {
		return extractedTorrent{}, fmt.Errorf("torrent: canceled before extraction: %w", err)
	}
	if plan.tailBytes > 0 {
		if err := plan.hashFinalPiece(ctx, subject); err != nil {
			return extractedTorrent{}, err
		}
		infoBytes, err := bencode.Marshal(plan.dictionary)
		if err != nil {
			return extractedTorrent{}, fmt.Errorf("torrent: encode partial piece: %w", err)
		}
		mi.InfoBytes = infoBytes
		output = &bytes.Buffer{}
		if err := mi.Write(output); err != nil {
			return extractedTorrent{}, fmt.Errorf("torrent: encode extracted metainfo: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return extractedTorrent{}, fmt.Errorf("torrent: canceled before extracted publication: %w", err)
	}
	base, err := TempTorrentPath(tmpRoot, subject.SourcePath)
	if err != nil {
		return extractedTorrent{}, err
	}
	path := filepath.Join(filepath.Dir(base), fmt.Sprintf("converted-%x.torrent", sha256.Sum256(output.Bytes())))
	convertedTorrentPublicationMu.Lock()
	defer convertedTorrentPublicationMu.Unlock()
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, output.Bytes()) {
		return extractedTorrent{path: path}, nil
	}
	fileHandle, err := os.CreateTemp(filepath.Dir(path), ".converted-*.tmp")
	if err != nil {
		return extractedTorrent{}, fmt.Errorf("torrent: stage converted metainfo: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			_ = os.Remove(fileHandle.Name())
		}
	}()
	if _, err := fileHandle.Write(output.Bytes()); err != nil {
		_ = fileHandle.Close()
		return extractedTorrent{}, fmt.Errorf("torrent: write converted metainfo: %w", err)
	}
	if err := fileHandle.Close(); err != nil {
		return extractedTorrent{}, fmt.Errorf("torrent: close converted metainfo: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return extractedTorrent{}, fmt.Errorf("torrent: canceled after extraction: %w", err)
	}
	if plan.tailBytes > 0 {
		owned = false
		return extractedTorrent{path: fileHandle.Name(), publication: &generatedTorrentPublication{stage: fileHandle.Name()}}, nil
	}
	if err := os.Rename(fileHandle.Name(), path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, output.Bytes()) {
			return extractedTorrent{path: path}, nil
		}
		return extractedTorrent{}, fmt.Errorf("torrent: publish converted metainfo: %w", err)
	}
	return extractedTorrent{path: path}, nil
}

func (p *singleFileExtraction) hashFinalPiece(ctx context.Context, subject api.TorrentSubject) error {
	emitTorrentHashProgress(ctx, subject, "running", "Hashing final partial torrent piece", 0, 1, 0)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("torrent: canceled before partial piece: %w", err)
	}
	// Only the validated submission inventory is opened, never a torrent-supplied path.
	file, err := os.Open(p.source.LocalPath)
	if err != nil {
		return fmt.Errorf("torrent: open source for partial piece: %w", err)
	}
	hash := sha1.New() //nolint:gosec // BitTorrent v1 piece hashes require SHA-1.
	reader := io.NewSectionReader(file, p.source.Size-p.tailBytes, p.tailBytes)
	var readErr error
	buffer := make([]byte, 32<<10)
	for remaining := p.tailBytes; remaining > 0; {
		if readErr = ctx.Err(); readErr != nil {
			break
		}
		var n int
		n, readErr = io.ReadFull(reader, buffer[:min(int64(len(buffer)), remaining)])
		if readErr != nil {
			break
		}
		_, _ = hash.Write(buffer[:n])
		remaining -= int64(n)
	}
	closeErr := file.Close()
	if readErr != nil {
		return fmt.Errorf("torrent: read final partial piece: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("torrent: close partial piece source: %w", closeErr)
	}
	copy(p.pieces[len(p.pieces)-metainfo.HashSize:], hash.Sum(nil))
	emitTorrentHashProgress(ctx, subject, "running", "Hashing final partial torrent piece", 1, 1, 0)
	return nil
}
