// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type Level int

const (
	LevelError Level = iota
	LevelWarn
	LevelInfo
	LevelDebug
	LevelTrace
)

func (l Level) String() string {
	//nolint:exhaustive // Unknown levels intentionally fall back to "info".
	switch l {
	case LevelTrace:
		return "trace"
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

func ParseLevel(value string) (Level, error) {
	normalized, err := api.ParseLogLevel(value)
	if err != nil {
		return LevelInfo, err
	}

	switch normalized {
	case "info":
		return LevelInfo, nil
	case "trace":
		return LevelTrace, nil
	case "debug":
		return LevelDebug, nil
	case "warn":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return LevelInfo, fmt.Errorf("logging: unknown normalized level %q", normalized)
	}
}

type Logger struct {
	level      Level
	consoleOut *log.Logger
	consoleErr *log.Logger
	file       *log.Logger
	closer     io.Closer
	mu         sync.Mutex
	nextID     int64
	buffer     []Entry
	bufferCap  int
	subs       map[int]chan Entry
	subID      int
}

type Entry struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

const defaultBufferCap = 1000
const defaultSubscriberBuffer = 200

func New(cfg config.LoggingConfig, dbPath string) (*Logger, error) {
	return NewWithLevel(cfg, dbPath, "")
}

func NewWithLevel(cfg config.LoggingConfig, dbPath string, override string) (*Logger, error) {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		cfg.Level = trimmed
	}

	level, err := ParseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	logger := &Logger{
		level:      level,
		consoleOut: log.New(os.Stdout, "", log.LstdFlags),
		consoleErr: log.New(os.Stderr, "", log.LstdFlags),
		bufferCap:  defaultBufferCap,
		subs:       make(map[int]chan Entry),
	}

	if cfg.FileEnabled {
		logPath, err := resolveLogPath(dbPath)
		if err != nil {
			return nil, err
		}
		maxBytes := maxBytesPerFile(cfg.MaxTotalSizeMB, cfg.MaxFiles)
		writer, err := newRotatingWriter(logPath, maxBytes, cfg.MaxFiles)
		if err != nil {
			return nil, err
		}
		logger.file = log.New(writer, "", log.LstdFlags)
		logger.closer = writer
	}

	return logger, nil
}

func ResolveEffectiveLevel(configured string, runOverride string, debug bool) string {
	if trimmed := strings.TrimSpace(runOverride); trimmed != "" {
		return trimmed
	}
	if debug {
		return LevelDebug.String()
	}
	return strings.TrimSpace(configured)
}

func (l *Logger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	if err := l.closer.Close(); err != nil {
		return fmt.Errorf("close logger: %w", err)
	}
	return nil
}

func (l *Logger) Tracef(format string, args ...any) {
	l.logf(LevelTrace, "TRACE", format, args...)
}

func (l *Logger) Debugf(format string, args ...any) {
	l.logf(LevelDebug, "DEBUG", format, args...)
}

func (l *Logger) Infof(format string, args ...any) {
	l.logf(LevelInfo, "INFO", format, args...)
}

func (l *Logger) Warnf(format string, args ...any) {
	l.logf(LevelWarn, "WARN", format, args...)
}

func (l *Logger) Errorf(format string, args ...any) {
	l.logf(LevelError, "ERROR", format, args...)
}

func (l *Logger) logf(level Level, label string, format string, args ...any) {
	if l == nil || level > l.level {
		return
	}

	formatted := fmt.Sprintf(format, args...)
	prefix := label + ": "
	if level <= LevelWarn {
		l.consoleErr.Printf(prefix+format, args...)
	} else {
		l.consoleOut.Printf(prefix+format, args...)
	}
	if l.file != nil {
		l.file.Printf(prefix+format, args...)
	}

	l.record(label, formatted)
}

func (l *Logger) record(label string, message string) {
	if l == nil {
		return
	}

	entry := Entry{
		Time:    time.Now(),
		Level:   strings.ToLower(label),
		Message: message,
	}

	l.mu.Lock()
	l.nextID++
	entry.ID = l.nextID
	l.buffer = append(l.buffer, entry)
	if l.bufferCap > 0 && len(l.buffer) > l.bufferCap {
		l.buffer = l.buffer[len(l.buffer)-l.bufferCap:]
	}
	for _, ch := range l.subs {
		select {
		case ch <- entry:
		default:
		}
	}
	l.mu.Unlock()
}

func (l *Logger) Recent(limit int) []Entry {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if limit <= 0 || limit > len(l.buffer) {
		limit = len(l.buffer)
	}
	start := len(l.buffer) - limit
	if start < 0 {
		start = 0
	}
	entries := make([]Entry, limit)
	copy(entries, l.buffer[start:])
	return entries
}

func (l *Logger) Subscribe(buffer int) (int, <-chan Entry) {
	if l == nil {
		return 0, nil
	}
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.subID++
	id := l.subID
	ch := make(chan Entry, buffer)
	l.subs[id] = ch
	return id, ch
}

func (l *Logger) Unsubscribe(id int) {
	if l == nil {
		return
	}

	l.mu.Lock()
	ch, ok := l.subs[id]
	if ok {
		delete(l.subs, id)
		close(ch)
	}
	l.mu.Unlock()
}

func maxBytesPerFile(maxTotalMB int, maxFiles int) int64 {
	if maxTotalMB <= 0 || maxFiles <= 0 {
		return 0
	}
	maxBytes := (int64(maxTotalMB) * 1024 * 1024) / int64(maxFiles)
	if maxBytes < 1 {
		return 1
	}
	return maxBytes
}

func resolveLogPath(dbPath string) (string, error) {
	logDir, err := db.Subdir(dbPath, "logs")
	if err != nil {
		return "", fmt.Errorf("logging: create log dir: %w", err)
	}
	return filepath.Join(logDir, "upbrr.log"), nil
}

func LogPath(dbPath string) (string, error) {
	return resolveLogPath(dbPath)
}

type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	file     *os.File
	size     int64
}

func newRotatingWriter(path string, maxBytes int64, maxFiles int) (*rotatingWriter, error) {
	if maxBytes <= 0 {
		return nil, errors.New("logging: max bytes per file must be greater than zero")
	}
	if maxFiles <= 0 {
		return nil, errors.New("logging: max files must be greater than zero")
	}
	writer := &rotatingWriter{path: path, maxBytes: maxBytes, maxFiles: maxFiles}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}

	if w.maxBytes > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("logging: write log file: %w", err)
	}
	return n, nil
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("logging: close log file: %w", err)
	}
	return nil
}

func (w *rotatingWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("logging: open log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("logging: stat log file: %w", err)
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *rotatingWriter) rotate() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
		w.size = 0
	}

	if w.maxFiles <= 1 {
		file, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("logging: truncate log file: %w", err)
		}
		w.file = file
		return nil
	}

	for i := w.maxFiles - 1; i >= 1; i-- {
		src := w.path + "." + strconv.Itoa(i)
		dst := w.path + "." + strconv.Itoa(i+1)
		if i == w.maxFiles-1 {
			_ = os.Remove(dst)
		}
		_ = os.Rename(src, dst)
	}

	if _, err := os.Stat(w.path); err == nil {
		_ = os.Rename(w.path, w.path+".1")
	}

	return w.open()
}

var _ api.Logger = (*Logger)(nil)
var _ io.Closer = (*Logger)(nil)
