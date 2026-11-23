// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/core"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
)

type cliInputLogs struct {
	core      *core.Core
	logger    *logging.Logger
	dbPath    string
	export    bool
	directory string
}

func (l *cliInputLogs) process(
	ctx context.Context, item cliPreparationItem, streams cliIO,
	process func(context.Context, cliPreparationItem) (cliQueueOutcome, error),
) (cliQueueOutcome, error) {
	if l == nil {
		outcome, err := process(ctx, item)
		return outcome, errors.Join(err, ctx.Err())
	}
	started := time.Now()
	ctx, id, finish := l.core.BeginInputLog(ctx, item.originalPath)
	itemLogger := logging.FromContext(ctx, l.logger)
	ctx = withCLIUploadProgressLogger(ctx, itemLogger)
	ctx = withCLISourceVerificationProgressLogger(ctx, itemLogger)
	outcome, err := process(ctx, item)
	err = errors.Join(err, ctx.Err())
	status := outcome.Status
	if err != nil {
		if ctx.Err() != nil {
			status = "interrupted"
		} else {
			status = "failed"
		}
		logging.FromContext(ctx, l.logger).Warnf("input: processing ended without success: %s", logging.SanitizeMessage(err.Error()))
	} else if status == "" {
		status = "completed"
	}
	finish("", status)
	if l.export {
		exportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		exportErr := errors.New("no log could be associated with this input run")
		if id != "" {
			exportErr = saveCLIInputLog(exportCtx, l.logger, l.dbPath, l.directory, db.InputLogAttempt{
				ID:         id,
				SourcePath: item.originalPath,
				Kind:       "CLI input",
				Outcome:    status,
				StartedAt:  started,
			}, streams.out)
		}
		if exportErr != nil {
			printCLIWarning(streams.errOut, "could not export input logs: "+logging.SanitizeMessage(exportErr.Error()))
		}
	}
	return outcome, err
}

func runLastInputLogExport(ctx context.Context, dbPath, source, dest string, out io.Writer) error {
	path, err := filepath.Abs(strings.TrimSpace(source))
	if err != nil {
		return exitError(1, err)
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return exitError(1, err)
	}
	attempts, err := db.ReadOnlyInputLogs(ctx, dbPath, path)
	if err != nil {
		return exitError(1, fmt.Errorf("look up retained input logs: %w", err))
	}
	if len(attempts) == 0 {
		return exitError(1, errors.New("no retained log attempt for that input"))
	}
	return wrapUpbrrError(saveCLIInputLog(ctx, nil, dbPath, dest, attempts[0], out))
}

func saveCLIInputLog(ctx context.Context, logger *logging.Logger, dbPath, directory string, attempt db.InputLogAttempt, out io.Writer) error {
	data, err := logging.ReadInputLog(ctx, logger, dbPath, attempt)
	if err != nil {
		return fmt.Errorf("read retained input log: %w", err)
	}
	if data.Text == "" {
		return errors.New("the retained entries for this attempt are no longer available")
	}
	if directory == "" {
		directory, err = db.Subdir(dbPath, "logs")
		if err != nil {
			return fmt.Errorf("find default log export directory: %w", err)
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open log export directory: %w", err)
	}
	defer root.Close()
	name := "upbrr-input-" + attempt.ID + "-" + rand.Text() + ".log"
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create log export: %w", err)
	}
	_, writeErr := io.WriteString(file, data.Text)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		_ = root.Remove(name)
		return fmt.Errorf("write log export: %w", err)
	}
	fmt.Fprintf(out, "Exported %s (%s)\n", formatPathLabel(filepath.Join(directory, name)), data.Coverage)
	return nil
}
