// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
)

const inputExportLimit = 50 * 1024 * 1024
const inputLogTimeFormat = "2006/01/02 15:04:05"
const activeLogPrefixBytes = 256

// InputLogExport contains the retained entries and their coverage.
type InputLogExport struct {
	Text     string
	Coverage string
}

// ReadInputLog exports one attempt, preserving the CLI's per-run behavior.
func ReadInputLog(ctx context.Context, logger *Logger, dbPath string, a db.InputLogAttempt) (InputLogExport, error) {
	return ReadInputLogs(ctx, logger, dbPath, []db.InputLogAttempt{a})
}

// ReadInputLogs reads the rotating files once for all selected attempts, then
// assembles their retained records in chronological attempt order. An attempt
// is complete only when both markers and every numbered disk entry survive.
// If the export limit is reached, newer records take precedence.
func ReadInputLogs(ctx context.Context, logger *Logger, dbPath string, attempts []db.InputLogAttempt) (InputLogExport, error) {
	return readInputLogs(ctx, logger, dbPath, attempts, inputExportLimit)
}

func readInputLogs(ctx context.Context, logger *Logger, dbPath string, attempts []db.InputLogAttempt, limit int) (InputLogExport, error) {
	if len(attempts) == 0 {
		return InputLogExport{Coverage: "expired"}, nil
	}
	dir, err := db.RootDir(dbPath)
	if err != nil {
		return InputLogExport{}, fmt.Errorf("resolve input log root: %w", err)
	}
	reader := inputLogReader{records: make(map[string]*inputLogRecords, len(attempts)), limit: limit}
	for _, a := range attempts {
		reader.records[a.ID] = &inputLogRecords{}
	}
	retainedMemory := logger.recentInputLogs()
	// Track only identities present in the bounded memory snapshot, not every
	// entry read from rotating files.
	reader.memoryPending = make(map[inputLogRecordID]struct{}, len(retainedMemory))
	for _, e := range retainedMemory {
		if reader.records[e.attempt] != nil && e.sequence != 0 {
			reader.memoryPending[inputLogRecordID{e.attempt, e.sequence}] = struct{}{}
		}
	}
	if err := reader.readFiles(ctx, filepath.Join(dir, "logs")); err != nil {
		return InputLogExport{}, err
	}
	memory := inputLogReader{limit: limit}
	for _, e := range retainedMemory {
		records := reader.records[e.attempt]
		_, pending := reader.memoryPending[inputLogRecordID{e.attempt, e.sequence}]
		if records == nil || (e.sequence != 0 && !pending) {
			continue
		}
		records.memory = !records.sawDiskRecord
		memory.add(inputLogLine{
			records: records,
			text:    fmt.Sprintf("%s %s: %s\n", e.Time.Format(inputLogTimeFormat), strings.ToUpper(e.Level), e.Message),
			stamp:   e.Time.UnixNano(),
		})
	}
	for _, entry := range exportInputLogLines(reader.lines[reader.first:], memory.lines[memory.first:], limit) {
		if !entry.continuation || entry.records.text.Len() != 0 {
			entry.records.text.WriteString(entry.text)
		}
	}
	var text strings.Builder
	aggregate := ""
	for _, a := range slices.Backward(attempts) {
		records := reader.records[a.ID]
		coverage := "expired"
		if records.text.Len() > 0 {
			switch {
			case records.memory && !records.incomplete:
				coverage = "memory"
			case records.started && records.ended && !records.incomplete && !reader.incomplete && a.Outcome != "":
				coverage = "complete"
			default:
				coverage = "partial"
			}
			if text.Len() != 0 {
				text.WriteString("\n")
			}
			outcome := a.Outcome
			if outcome == "" {
				outcome = "no recorded outcome"
			}
			fmt.Fprintf(&text, "Input: %s\nAttempt: %s (%s)\nStarted: %s\nOutcome: %s\nCoverage: %s\n\n",
				SanitizeMessage(diskLogMessage(a.SourcePath)), a.ID, a.Kind, a.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), outcome, coverage)
			text.WriteString(records.text.String())
		}
		switch {
		case aggregate == "":
			aggregate = coverage
		case coverage != aggregate:
			aggregate = "partial"
		}
	}
	if text.Len() == 0 {
		aggregate = "expired"
	}
	if aggregate == "partial" && len(attempts) > 1 {
		text.WriteString("\nSome input log entries were not retained or could not be exported.\n")
		mixed := (reader.bytes > 0 || reader.trimmed) && (memory.bytes > 0 || memory.trimmed)
		if reader.legacy && mixed && (reader.trimmed || memory.trimmed || reader.bytes+memory.bytes > limit) {
			text.WriteString("Older disk entries only record whole seconds; the order within a second may be uncertain.\n")
		}
	}
	return InputLogExport{Text: text.String(), Coverage: aggregate}, nil
}

type inputLogRecords struct {
	text                                              strings.Builder
	next                                              uint64
	started, ended, incomplete, memory, sawDiskRecord bool
}

type inputLogLine struct {
	records      *inputLogRecords
	text         string
	stamp        int64
	continuation bool
}

type inputLogRecordID struct {
	attempt  string
	sequence uint64
}

type inputLogReader struct {
	memoryPending map[inputLogRecordID]struct{}
	records       map[string]*inputLogRecords
	continuation  inputLogLine
	lines         []inputLogLine
	first         int
	limit, bytes  int
	incomplete    bool
	legacy        bool
	trimmed       bool
}

func (r *inputLogReader) add(line inputLogLine) {
	if len(line.text) > r.limit {
		r.trimmed = true
		line.records.incomplete = true
		return
	}
	for r.bytes+len(line.text) > r.limit {
		r.trimmed = true
		oldest := r.lines[r.first]
		r.lines[r.first] = inputLogLine{}
		r.first++
		oldest.records.incomplete = true
		r.bytes -= len(oldest.text)
	}
	if r.first > 0 && r.first >= len(r.lines)-r.first {
		r.lines = slices.Delete(r.lines, 0, r.first)
		r.first = 0
	}
	r.lines = append(r.lines, line)
	r.bytes += len(line.text)
}

// Each source already retains only the newest candidates. Merge from the
// newest end so a memory fallback cannot evict a newer disk entry.
func exportInputLogLines(disk, memory []inputLogLine, limit int) []inputLogLine {
	if len(disk) == 0 {
		return memory
	}
	if len(memory) == 0 {
		return disk
	}
	var result []inputLogLine
	for len(disk)+len(memory) > 0 {
		var line inputLogLine
		switch {
		case len(memory) == 0 || (len(disk) != 0 && disk[len(disk)-1].stamp >= memory[len(memory)-1].stamp):
			line, disk = disk[len(disk)-1], disk[:len(disk)-1]
		default:
			line, memory = memory[len(memory)-1], memory[:len(memory)-1]
		}
		if len(line.text) > limit {
			limit = 0
			line.records.incomplete = true
			continue
		}
		limit -= len(line.text)
		result = append(result, line)
	}
	slices.Reverse(result)
	return result
}

func (r *inputLogReader) accept(line string) {
	if rest, ok := strings.CutPrefix(line, "\t"); ok {
		if r.continuation.records != nil {
			line := r.continuation
			line.text = rest
			line.continuation = true
			r.add(line)
		}
		return
	}
	r.continuation = inputLogLine{}
	// Accept only a marker immediately after the fixed timestamp and severity.
	if len(line) <= 20 || line[4] != '/' || line[7] != '/' || line[10] != ' ' {
		return
	}
	severity, payload, ok := strings.Cut(line[20:], ": ")
	if !ok || (severity != "TRACE" && severity != "DEBUG" && severity != "INFO" && severity != "WARN" && severity != "ERROR") {
		return
	}
	payload, ok = strings.CutPrefix(payload, "[input-log ")
	if !ok {
		return
	}
	id, payload, _ := strings.Cut(payload, " ")
	records := r.records[id]
	if records == nil {
		return
	}
	marker, body, ok := strings.Cut(payload, "] ")
	if !ok {
		return
	}
	number, kindAndStamp, ok := strings.Cut(marker, " ")
	kind, precise, hasPrecise := strings.Cut(kindAndStamp, " ")
	sequence, err := strconv.ParseUint(number, 10, 64)
	if !ok || err != nil || sequence == 0 {
		return
	}
	if sequence != records.next+1 || records.ended {
		records.incomplete = true
	}
	records.next = sequence
	switch kind {
	case "begin":
		records.started = sequence == 1
	case "end":
		records.ended = true
	case "record":
		records.sawDiskRecord = true
		// Older files have only the displayed wall-clock second. New
		// markers carry precise time without changing the exported text.
		var stamp int64
		if hasPrecise {
			stamp, err = strconv.ParseInt(precise, 10, 64)
		} else {
			var seconds time.Time
			seconds, err = time.ParseInLocation(inputLogTimeFormat, line[:len(inputLogTimeFormat)], time.Local)
			stamp = seconds.UnixNano()
			r.legacy = true
		}
		if err != nil {
			records.incomplete = true
			return
		}
		text := line[:20] + severity + ": " + body
		delete(r.memoryPending, inputLogRecordID{id, sequence})
		record := inputLogLine{
			records: records,
			text:    text,
			stamp:   stamp,
		}
		if len(text) <= r.limit {
			r.continuation = record
		}
		r.add(record)
	default:
		records.incomplete = true
	}
}

type retainedLogFile struct {
	name   string
	age    int
	info   os.FileInfo
	prefix []byte
}

func (r *inputLogReader) readFiles(ctx context.Context, dir string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open retained logs: %w", err)
	}
	defer root.Close()
	files, err := r.snapshotFiles(root)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read retained logs canceled: %w", err)
		}
		if err := r.readFile(ctx, root, f); err != nil {
			return err
		}
	}
	r.verifyFiles(root, files)
	return nil
}

func (r *inputLogReader) snapshotFiles(root *os.Root) ([]retainedLogFile, error) {
	entries, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open log directory listing: %w", err)
	}
	list, err := entries.ReadDir(-1)
	_ = entries.Close()
	if err != nil {
		return nil, fmt.Errorf("list retained log files: %w", err)
	}
	files := make([]retainedLogFile, 0)
	for _, e := range list {
		name := e.Name()
		var age int
		if name != "upbrr.log" {
			suffix, ok := strings.CutPrefix(name, "upbrr.log.")
			if !ok {
				continue
			}
			age, err = strconv.Atoi(suffix)
			if err != nil || age < 1 || strconv.Itoa(age) != suffix {
				continue
			}
		}
		// On Windows, DirEntry.Info can defer obtaining its file ID
		// using the directory handle's relative name. Capture the ID
		// from the rooted path now so SameFile compares real identities.
		info, statErr := root.Lstat(name)
		if errors.Is(statErr, os.ErrNotExist) {
			r.incomplete = true
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("stat retained log file: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			r.incomplete = true
			continue
		}
		var prefix []byte
		if age == 0 {
			actual, head, ok, openErr := snapshotActiveLogFile(root, name, info)
			if openErr != nil {
				return nil, openErr
			}
			if !ok {
				r.incomplete = true
				continue
			}
			info = actual
			prefix = head
		}
		files = append(files, retainedLogFile{
			name:   name,
			age:    age,
			info:   info,
			prefix: prefix,
		})
	}
	slices.SortFunc(files, func(a, b retainedLogFile) int { return b.age - a.age })
	return files, nil
}

func snapshotActiveLogFile(root *os.Root, name string, expected os.FileInfo) (os.FileInfo, []byte, bool, error) {
	file, err := root.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("open retained log file: %w", err)
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return nil, nil, false, fmt.Errorf("stat retained log file: %w", err)
	}
	if !os.SameFile(expected, actual) || !actual.Mode().IsRegular() {
		return nil, nil, false, nil
	}
	if actual.Size() <= 0 {
		return actual, nil, true, nil
	}
	prefix := make([]byte, min(actual.Size(), activeLogPrefixBytes))
	if _, err := io.ReadFull(file, prefix); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("read retained log file: %w", err)
	}
	return actual, prefix, true, nil
}

func (r *inputLogReader) verifyFiles(root *os.Root, files []retainedLogFile) {
	for _, f := range files {
		now, err := root.Lstat(f.name)
		if err != nil || !sameRetainedLogFile(f, now) {
			r.incomplete = true
			continue
		}
		if f.age == 0 && len(f.prefix) > 0 && !retainedLogPrefixMatches(root, f, now) {
			r.incomplete = true
		}
	}
}

func sameRetainedLogFile(f retainedLogFile, now os.FileInfo) bool {
	if now == nil || !os.SameFile(f.info, now) || !now.Mode().IsRegular() {
		return false
	}
	if f.age > 0 {
		return f.info.Size() == now.Size() && f.info.ModTime().Equal(now.ModTime())
	}
	if now.Size() < f.info.Size() {
		return false
	}
	if now.Size() == f.info.Size() {
		return f.info.ModTime().Equal(now.ModTime())
	}
	return !now.ModTime().Before(f.info.ModTime())
}

func retainedLogPrefixMatches(root *os.Root, f retainedLogFile, expected os.FileInfo) bool {
	file, err := root.Open(f.name)
	if err != nil {
		return false
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(expected, actual) || !sameRetainedLogFile(f, actual) {
		return false
	}
	head := make([]byte, len(f.prefix))
	if _, err := io.ReadFull(file, head); err != nil {
		return false
	}
	return bytes.Equal(head, f.prefix)
}

func (r *inputLogReader) readFile(ctx context.Context, root *os.Root, f retainedLogFile) error {
	file, err := root.Open(f.name)
	if errors.Is(err, os.ErrNotExist) {
		r.incomplete = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("open retained log file: %w", err)
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat retained log file: %w", err)
	}
	if !sameRetainedLogFile(f, actual) {
		r.incomplete = true
		return nil
	}
	limited := &io.LimitedReader{R: file, N: f.info.Size()}
	var src io.Reader = limited
	if len(f.prefix) > 0 {
		head := make([]byte, len(f.prefix))
		if _, err := io.ReadFull(limited, head); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("read retained log file: %w", err)
			}
			r.incomplete = true
			return nil
		}
		if !bytes.Equal(head, f.prefix) {
			r.incomplete = true
			return nil
		}
		src = io.MultiReader(bytes.NewReader(head), limited)
	}
	if err := r.readLogRecords(ctx, src); err != nil {
		return err
	}
	if limited.N != 0 {
		r.incomplete = true
	}
	return nil
}

func (r *inputLogReader) readLogRecords(ctx context.Context, src io.Reader) error {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 64*1024), inputExportLimit)
	r.continuation = inputLogLine{}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read retained logs canceled: %w", err)
		}
		r.accept(scanner.Text() + "\n")
	}
	if err := scanner.Err(); err != nil {
		if !errors.Is(err, bufio.ErrTooLong) {
			return fmt.Errorf("read retained log file: %w", err)
		}
		r.incomplete = true
	}
	return nil
}
