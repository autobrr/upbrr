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

const inputExportLimit = 8 * 1024 * 1024

// InputLogExport contains an immutable view of the retained entries and coverage.
type InputLogExport struct {
	Text     string
	Coverage string
}

// ReadInputLog uses existing application log files and, when no disk entries
// survive, the existing recent buffer. Only a gap-free begin-to-end disk
// sequence is marked complete.
func ReadInputLog(ctx context.Context, logger *Logger, dbPath string, a db.InputLogAttempt) (InputLogExport, error) {
	dir, err := db.RootDir(dbPath)
	if err != nil {
		return InputLogExport{}, fmt.Errorf("resolve input log root: %w", err)
	}
	records := &inputLogRecords{
		id: a.ID,
	}
	if err := records.readFiles(ctx, filepath.Join(dir, "logs")); err != nil {
		return InputLogExport{}, err
	}
	coverage := "expired"
	if records.text.Len() != 0 {
		coverage = "partial"
		if records.started && records.ended && !records.incomplete && a.Outcome != "" {
			coverage = "complete"
		}
	} else {
		for _, e := range logger.Recent(0) {
			if e.attempt == a.ID {
				records.add(fmt.Sprintf("%s %s: %s\n",
					e.Time.Format("2006/01/02 15:04:05"), strings.ToUpper(e.Level), e.Message))
			}
		}
		if records.text.Len() > 0 {
			coverage = "memory"
		}
	}
	if records.text.Len() == 0 {
		return InputLogExport{Coverage: coverage}, nil
	}
	var outcome = a.Outcome
	if outcome == "" {
		outcome = "no recorded outcome"
	}
	header := fmt.Sprintf("Input: %s\nAttempt: %s (%s)\nStarted: %s\nOutcome: %s\nCoverage: %s\n\n",
		SanitizeMessage(diskLogMessage(a.SourcePath)), a.ID, a.Kind, a.StartedAt.UTC().Format("2006-01-02T15:04:05Z"), outcome, coverage)
	return InputLogExport{Text: header + records.text.String(), Coverage: coverage}, nil
}

type inputLogRecords struct {
	id                                              string
	text                                            strings.Builder
	next                                            uint64
	started, ended, incomplete, includeContinuation bool
}

func (r *inputLogRecords) add(line string) {
	if r.text.Len()+len(line) > inputExportLimit {
		r.incomplete = true
		return
	}
	r.text.WriteString(line)
}

func (r *inputLogRecords) accept(line string) {
	if rest, ok := strings.CutPrefix(line, "\t"); ok {
		if r.includeContinuation {
			r.add(rest)
		}
		return
	}
	r.includeContinuation = false
	// Accept only a marker immediately after the fixed timestamp and severity.
	if len(line) <= 20 || line[4] != '/' || line[7] != '/' || line[10] != ' ' {
		return
	}
	severity, payload, ok := strings.Cut(line[20:], ": ")
	if !ok || (severity != "TRACE" && severity != "DEBUG" && severity != "INFO" && severity != "WARN" && severity != "ERROR") {
		return
	}
	payload, ok = strings.CutPrefix(payload, "[input-log "+r.id+" ")
	if !ok {
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
	if sequence != r.next+1 || r.ended {
		r.incomplete = true
	}
	r.next = sequence
	switch kind {
	case "begin":
		r.started = sequence == 1
	case "end":
		r.ended = true
	case "record":
		r.includeContinuation = true
		r.add(line[:20] + severity + ": " + body)
	default:
		r.incomplete = true
	}
}

type retainedLogFile struct {
	name string
	age  int
	info os.FileInfo
}

func (r *inputLogRecords) readFiles(ctx context.Context, dir string) error {
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
		info, statErr := e.Info()
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

func (r *inputLogRecords) readFile(ctx context.Context, root *os.Root, f retainedLogFile) error {
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
	r.includeContinuation = false
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
