// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The process lock also covers replacement services after runtime activation.
// The caller owns the exclusive run lock across processes and the private directory.
var imageEffectsMu sync.Mutex

var effectIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

const imageJournalVersion = 1

var errImageJournal = errors.New("image hosting: invalid or unavailable live-test image journal")

type imageEffectJournal struct {
	path  string
	runID string
}

type imageEffectRecord struct {
	Version  int                  `json:"version"`
	RunID    string               `json:"run_id"`
	Kind     string               `json:"kind"`
	ID       string               `json:"id"`
	Provider string               `json:"provider"`
	Time     time.Time            `json:"time"`
	Sources  []string             `json:"sources,omitempty"`
	URLs     []string             `json:"urls,omitempty"`
	Complete bool                 `json:"complete,omitempty"`
	Images   []CleanupImageResult `json:"images,omitempty"`
}

type imageEffectAttempt struct {
	sources  []string
	urls     []string
	complete bool
	returned bool
}

type imageEffectState struct {
	attempts       map[string]*imageEffectAttempt
	order          []string
	images         map[string]CleanupImageResult
	urls           map[string]string
	cleanupStarted bool
	cleanupBatches map[string][]string
}

func newImageEffectJournal(runID, path string) (*imageEffectJournal, error) {
	if !effectIDPattern.MatchString(runID) || path == "" {
		return nil, errImageJournal
	}
	j := &imageEffectJournal{path: path, runID: runID}
	imageEffectsMu.Lock()
	defer imageEffectsMu.Unlock()
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		record := j.record("run", runID)
		if err := j.write(record, os.O_CREATE|os.O_EXCL|os.O_WRONLY); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, errImageJournal
	}
	if _, err := j.read(); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *imageEffectJournal) record(kind, id string) imageEffectRecord {
	return imageEffectRecord{
		Version:  imageJournalVersion,
		RunID:    j.runID,
		Kind:     kind,
		ID:       id,
		Provider: "lostimg",
		Time:     time.Now().UTC(),
	}
}

func (j *imageEffectJournal) append(record imageEffectRecord) error {
	return j.write(record, os.O_APPEND|os.O_WRONLY)
}

func (j *imageEffectJournal) write(record imageEffectRecord, flags int) error {
	data, err := json.Marshal(record)
	if err != nil {
		return errImageJournal
	}
	file, err := os.OpenFile(j.path, flags, 0o600)
	if err != nil {
		return errImageJournal
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return errImageJournal
	}
	if err := file.Sync(); err != nil {
		return errImageJournal
	}
	return nil
}

func (j *imageEffectJournal) read() (*imageEffectState, error) {
	if !effectIDPattern.MatchString(j.runID) {
		return nil, errImageJournal
	}
	info, err := os.Lstat(j.path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errImageJournal
	}
	file, err := os.Open(j.path)
	if err != nil {
		return nil, errImageJournal
	}
	defer func() { _ = file.Close() }()
	state := &imageEffectState{
		attempts:       make(map[string]*imageEffectAttempt),
		images:         make(map[string]CleanupImageResult),
		urls:           make(map[string]string),
		cleanupBatches: make(map[string][]string),
	}
	scanner := bufio.NewReaderSize(file, 1024*1024)
	first := true
	for {
		// A torn final record is rejected rather than silently dropping ownership.
		line, readErr := scanner.ReadSlice('\n')
		if errors.Is(readErr, io.EOF) && len(line) == 0 {
			break
		}
		if readErr != nil || len(line) > 1024*1024 {
			return nil, errImageJournal
		}
		var record imageEffectRecord
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, errImageJournal
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, errImageJournal
		}
		if record.Version != imageJournalVersion || record.RunID != j.runID || record.Provider != "lostimg" ||
			!effectIDPattern.MatchString(record.ID) || record.Time.IsZero() {
			return nil, errImageJournal
		}
		if first {
			if record.Kind != "run" || record.ID != j.runID || len(record.Sources)+len(record.URLs)+len(record.Images) != 0 || record.Complete {
				return nil, errImageJournal
			}
			first = false
			continue
		}
		if err := state.apply(record); err != nil {
			return nil, err
		}
	}
	if first {
		return nil, errImageJournal
	}
	return state, nil
}

func (s *imageEffectState) apply(record imageEffectRecord) error {
	switch record.Kind {
	case "upload_pending":
		if s.cleanupStarted || s.attempts[record.ID] != nil || len(record.Sources) == 0 || len(record.Sources) > lostimgMaxBatchUploadImages ||
			len(record.URLs)+len(record.Images) != 0 || record.Complete {
			return errImageJournal
		}
		for _, source := range record.Sources {
			if len(source) != 64 || strings.Trim(source, "0123456789abcdef") != "" {
				return errImageJournal
			}
		}
		s.attempts[record.ID] = &imageEffectAttempt{sources: record.Sources}
		s.order = append(s.order, record.ID)
	case "uploaded":
		attempt := s.attempts[record.ID]
		if attempt == nil || attempt.returned || len(record.Sources)+len(record.Images) != 0 ||
			(record.Complete && len(record.URLs) != len(attempt.sources)) {
			return errImageJournal
		}
		seen := make(map[string]bool)
		for index, raw := range record.URLs {
			if !validEffectURL(raw) || seen[raw] {
				return errImageJournal
			}
			seen[raw] = true
			id := fmt.Sprintf("%s_%d", record.ID, index)
			s.urls[id] = raw
			s.images[id] = CleanupImageResult{ID: id, State: "uploaded"}
		}
		attempt.urls = record.URLs
		attempt.complete = record.Complete
		attempt.returned = true
	case "cleanup_pending", "cleanup_result":
		if len(record.Images) == 0 || len(record.Images) > lostimgMaxBatchUploadImages || len(record.Sources)+len(record.URLs) != 0 || record.Complete {
			return errImageJournal
		}
		if record.Kind == "cleanup_pending" {
			if _, exists := s.cleanupBatches[record.ID]; exists || s.attempts[record.ID] != nil {
				return errImageJournal
			}
		} else if len(s.cleanupBatches[record.ID]) != len(record.Images) {
			return errImageJournal
		}
		s.cleanupStarted = true
		seen := make(map[string]bool)
		for index, image := range record.Images {
			previous, exists := s.images[image.ID]
			if !exists || seen[image.ID] {
				return errImageJournal
			}
			seen[image.ID] = true
			if record.Kind == "cleanup_pending" {
				if previous.State != "uploaded" || image.State != "cleanup_pending" || image.Reason != "" {
					return errImageJournal
				}
			} else if previous.State != "cleanup_pending" ||
				s.cleanupBatches[record.ID][index] != image.ID ||
				(image.State != "deleted" && image.State != "cleanup_unknown") ||
				(image.State == "deleted" && image.Reason != "") ||
				(image.State == "cleanup_unknown" && image.Reason != "provider_unconfirmed") {
				return errImageJournal
			}
			s.images[image.ID] = image
			if record.Kind == "cleanup_pending" {
				s.cleanupBatches[record.ID] = append(s.cleanupBatches[record.ID], image.ID)
			}
		}
	default:
		return errImageJournal
	}
	return nil
}

func validEffectURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && len(raw) <= 4096 && strings.TrimSpace(raw) == raw && parsed.Scheme == "https" &&
		parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
}

func newImageEffectID() string { return rand.Text() }
