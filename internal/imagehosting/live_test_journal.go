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

	"github.com/autobrr/upbrr/pkg/api"
)

// The process lock also covers replacement services after runtime activation.
// The caller owns the exclusive run lock across processes and the private directory.
var imageEffectsMu sync.Mutex

var effectIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
var effectProviderPattern = regexp.MustCompile(`^[a-z0-9_]{1,50}$`)

const (
	legacyImageJournalVersion = 1
	imageJournalVersion       = 2
	imageJournalProvider      = "imagehosting"
	maxImageJournalRecordSize = 40 * 1024 * 1024
)

var errImageJournal = errors.New("image hosting: invalid or unavailable live-test image journal")

type imageEffectJournal struct {
	path    string
	runID   string
	version int
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
	Uploaded int                  `json:"uploaded,omitempty"`
	Complete bool                 `json:"complete,omitempty"`
	Images   []CleanupImageResult `json:"images,omitempty"`
}

type imageEffectAttempt struct {
	provider string
	sources  []string
	urls     []string
	uploaded int
	complete bool
	returned bool
}

type imageEffectState struct {
	version        int
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
		j.version = imageJournalVersion
		record := j.recordForProvider("run", runID, imageJournalProvider)
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
	return j.recordForProvider(kind, id, "lostimg")
}

func (j *imageEffectJournal) recordForProvider(kind, id, provider string) imageEffectRecord {
	version := j.version
	if version == 0 {
		version = imageJournalVersion
	}
	return imageEffectRecord{
		Version:  version,
		RunID:    j.runID,
		Kind:     kind,
		ID:       id,
		Provider: provider,
		Time:     time.Now().UTC(),
	}
}

func (j *imageEffectJournal) append(record imageEffectRecord) error {
	state, err := j.read()
	if err != nil {
		return err
	}
	if !validImageEffectRecord(record, j.runID) || record.Version != state.version ||
		(state.version == legacyImageJournalVersion && record.Provider != "lostimg") {
		return errImageJournal
	}
	if err := state.apply(record); err != nil {
		return err
	}
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
	reader := bufio.NewReaderSize(file, 64*1024)
	first := true
	for {
		// A torn final record is rejected rather than silently dropping ownership.
		line, readErr := readImageJournalLine(reader)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
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
		if !validImageEffectRecord(record, j.runID) {
			return nil, errImageJournal
		}
		if first {
			expectedProvider := imageJournalProvider
			if record.Version == legacyImageJournalVersion {
				expectedProvider = "lostimg"
			}
			if record.Kind != "run" || record.ID != j.runID || record.Provider != expectedProvider ||
				len(record.Sources)+len(record.URLs)+len(record.Images)+record.Uploaded != 0 || record.Complete {
				return nil, errImageJournal
			}
			state.version = record.Version
			first = false
			continue
		}
		if record.Version != state.version || (state.version == legacyImageJournalVersion && record.Provider != "lostimg") {
			return nil, errImageJournal
		}
		if err := state.apply(record); err != nil {
			return nil, err
		}
	}
	if first {
		return nil, errImageJournal
	}
	if j.version == 0 {
		j.version = state.version
	} else if j.version != state.version {
		return nil, errImageJournal
	}
	return state, nil
}

func validImageEffectRecord(record imageEffectRecord, runID string) bool {
	return (record.Version == legacyImageJournalVersion || record.Version == imageJournalVersion) && record.RunID == runID &&
		effectIDPattern.MatchString(record.ID) && effectProviderPattern.MatchString(record.Provider) && !record.Time.IsZero()
}

func (s *imageEffectState) apply(record imageEffectRecord) error {
	switch record.Kind {
	case "upload_pending":
		maxSources := api.MaxLiveTestImageUploads
		if s.version == legacyImageJournalVersion {
			maxSources = lostimgMaxBatchUploadImages
		}
		if record.Provider == imageJournalProvider || s.cleanupStarted || s.attempts[record.ID] != nil || len(record.Sources) == 0 ||
			len(record.Sources) > maxSources || len(record.URLs)+len(record.Images)+record.Uploaded != 0 || record.Complete {
			return errImageJournal
		}
		for _, source := range record.Sources {
			if len(source) != 64 || strings.Trim(source, "0123456789abcdef") != "" {
				return errImageJournal
			}
		}
		s.attempts[record.ID] = &imageEffectAttempt{provider: record.Provider, sources: record.Sources}
		s.order = append(s.order, record.ID)
	case "uploaded":
		attempt := s.attempts[record.ID]
		if attempt == nil || attempt.returned || record.Provider != attempt.provider || len(record.Sources)+len(record.Images) != 0 {
			return errImageJournal
		}
		uploaded := record.Uploaded
		if s.version == legacyImageJournalVersion {
			if uploaded != 0 || (record.Complete && len(record.URLs) != len(attempt.sources)) {
				return errImageJournal
			}
			uploaded = len(record.URLs)
		} else if uploaded < 0 || uploaded > len(attempt.sources) || len(record.URLs) > len(attempt.sources)*3 ||
			(record.Complete && uploaded != len(attempt.sources)) ||
			(uploaded == 0) != (len(record.URLs) == 0) ||
			(record.Provider == "lostimg" && len(record.URLs) != uploaded) {
			return errImageJournal
		}
		seen := make(map[string]bool)
		for _, raw := range record.URLs {
			if !validEffectURL(raw) || (s.version == legacyImageJournalVersion && seen[raw]) {
				return errImageJournal
			}
			seen[raw] = true
		}
		for index := range uploaded {
			id := fmt.Sprintf("%s_%d", record.ID, index)
			state := "retained"
			if record.Provider == "lostimg" {
				state = "uploaded"
				s.urls[id] = record.URLs[index]
			}
			s.images[id] = CleanupImageResult{ID: id, State: state}
		}
		attempt.urls = record.URLs
		attempt.uploaded = uploaded
		attempt.complete = record.Complete
		attempt.returned = true
	case "cleanup_pending", "cleanup_result":
		if record.Provider != "lostimg" || len(record.Images) == 0 || len(record.Images) > lostimgMaxBatchUploadImages ||
			len(record.Sources)+len(record.URLs)+record.Uploaded != 0 || record.Complete {
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

func readImageJournalLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxImageJournalRecordSize {
			return nil, errImageJournal
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) == 0:
			return nil, io.EOF
		default:
			return nil, errImageJournal
		}
	}
}

func validEffectURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && len(raw) <= 4096 && strings.TrimSpace(raw) == raw && parsed.Scheme == "https" &&
		parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
}

func newImageEffectID() string { return rand.Text() }
