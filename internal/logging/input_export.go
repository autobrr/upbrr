// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/services/db"
)

const inputExportLimit = 50 * 1024 * 1024
const inputLogTimeFormat = "2006/01/02 15:04:05"

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
	if err := reader.readFiles(ctx, filepath.Join(dir, "logs")); err != nil {
		return InputLogExport{}, err
	}
	memory := inputLogReader{limit: limit}
	for _, e := range logger.recentInputLogs() {
		records := reader.records[e.attempt]
		if records != nil && !records.sawDiskRecord {
			records.memory = true
			stamp := e.Time.Format(inputLogTimeFormat)
			memory.add(inputLogLine{
				records: records,
				text:    fmt.Sprintf("%s %s: %s\n", stamp, strings.ToUpper(e.Level), e.Message),
				stamp:   stamp,
			})
		}
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
	stamp        string
	continuation bool
}

type inputLogReader struct {
	records      map[string]*inputLogRecords
	continuation inputLogLine
	lines        []inputLogLine
	first        int
	limit, bytes int
	incomplete   bool
}

func (r *inputLogReader) add(line inputLogLine) {
	if len(line.text) > r.limit {
		line.records.incomplete = true
		return
	}
	for r.bytes+len(line.text) > r.limit {
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
	number, kind, ok := strings.Cut(marker, " ")
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
		text := line[:20] + severity + ": " + body
		record := inputLogLine{
			records: records,
			text:    text,
			stamp:   text[:len(inputLogTimeFormat)],
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
	name string
	age  int
	info os.FileInfo
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
	entries, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open log directory listing: %w", err)
	}
	list, err := entries.ReadDir(-1)
	_ = entries.Close()
	if err != nil {
		return fmt.Errorf("list retained log files: %w", err)
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
		if statErr != nil || !info.Mode().IsRegular() {
			r.incomplete = true
			continue
		}
		files = append(files, retainedLogFile{
			name: name,
			age:  age,
			info: info,
		})
	}
	slices.SortFunc(files, func(a, b retainedLogFile) int { return b.age - a.age })
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read retained logs canceled: %w", err)
		}
		if err := r.readFile(ctx, root, f); err != nil {
			return err
		}
	}
	for _, f := range files {
		now, err := root.Lstat(f.name)
		if err != nil || !os.SameFile(f.info, now) || f.info.Size() != now.Size() ||
			!f.info.ModTime().Equal(now.ModTime()) {
			r.incomplete = true
		}
	}
	return nil
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
	if err != nil || !os.SameFile(f.info, actual) || !actual.Mode().IsRegular() {
		r.incomplete = true
		return nil
	}
	scanner := bufio.NewScanner(io.LimitReader(file, f.info.Size()))
	scanner.Buffer(make([]byte, 64*1024), inputExportLimit)
	r.continuation = inputLogLine{}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("read retained logs canceled: %w", err)
		}
		r.accept(scanner.Text() + "\n")
	}
	if scanner.Err() != nil {
		r.incomplete = true
	}
	return nil
}
