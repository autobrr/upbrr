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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// Level is a maximum verbosity threshold: larger values admit progressively
// more detailed entries, while [LevelError] admits errors only.
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

// ParseLevel normalizes a configured level. Invalid input returns [LevelInfo]
// together with an error.
func ParseLevel(value string) (Level, error) {
	normalized, err := api.ParseLogLevel(value)
	if err != nil {
		return LevelInfo, fmt.Errorf("logging: %w", err)
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

// Logger writes sanitized, threshold-filtered entries to console and optional
// file outputs. The file receives every level through TRACE; the optional
// console threshold can differ from the threshold for snapshots and subscribers.
// Input captures retain TRACE independently of those thresholds. Close releases
// the optional file output.
type Logger struct {
	level           Level
	consoleLevel    Level
	consoleLevelSet bool
	consoleOut      *log.Logger
	consoleErr      *log.Logger
	file            *log.Logger
	closer          io.Closer
	mu              sync.Mutex
	nextID          int64
	buffer          []Entry
	inputLogs       *InputLogStore
	bufferCap       int
	subs            map[int]chan Entry
	subID           int
}

// InputLogStore retains a bounded in-memory buffer of scoped input log entries
// that can be shared across Logger generations for one running backend.
type InputLogStore struct {
	mu      sync.Mutex
	entries []Entry
	cap     int
	oldest  int
}

// NewInputLogStore constructs a bounded in-memory input log retention store
// using the default retention capacity.
func NewInputLogStore() *InputLogStore {
	return newInputLogStore(defaultBufferCap)
}

func newInputLogStore(capacity int) *InputLogStore {
	if capacity <= 0 {
		capacity = defaultBufferCap
	}
	return &InputLogStore{cap: capacity}
}

func (s *InputLogStore) record(entry Entry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cap == 0 {
		s.cap = defaultBufferCap
	}
	if len(s.entries) < s.cap {
		s.entries = append(s.entries, entry)
		return
	}
	s.entries[s.oldest] = entry
	s.oldest = (s.oldest + 1) % s.cap
}

func (s *InputLogStore) recent() []Entry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Concat(s.entries[s.oldest:], s.entries[:s.oldest])
}

// Entry is one sanitized retained log entry. IDs increase within a Logger, and
// Level contains the lowercase level name.
type Entry struct {
	ID       int64     `json:"id"`
	Time     time.Time `json:"time" ts_type:"string"`
	Level    string    `json:"level"`
	Message  string    `json:"message"`
	attempt  string
	sequence uint64
}

var (
	defaultConsoleMu  sync.RWMutex
	defaultConsoleOut io.Writer = os.Stdout
	defaultConsoleErr io.Writer = os.Stderr
)

const defaultBufferCap = 1000
const defaultSubscriberBuffer = 200

// New constructs a logger using cfg.Level. When file output is enabled, it
// creates or opens the app log below the database directory; callers must close
// the returned logger.
func New(cfg config.LoggingConfig, dbPath string) (*Logger, error) {
	return NewWithLevel(cfg, dbPath, "")
}

// NewWithInputLogStore constructs a logger that shares store for in-memory
// scoped input log retention across runtime generations. A nil store creates a
// new logger-local store.
func NewWithInputLogStore(cfg config.LoggingConfig, dbPath string, store *InputLogStore) (*Logger, error) {
	return newLogger(cfg, dbPath, "", store)
}

// NewWithLevel constructs a logger after replacing cfg.Level with a non-blank
// override. It has the same file ownership and side effects as [New].
func NewWithLevel(cfg config.LoggingConfig, dbPath string, override string) (*Logger, error) {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		cfg.Level = trimmed
	}
	return newLogger(cfg, dbPath, "", nil)
}

// NewWithConsoleLevel constructs a logger using cfg.Level for application
// logging and a non-blank override for console output only. Callers must close
// the returned logger when file output is enabled.
func NewWithConsoleLevel(cfg config.LoggingConfig, dbPath string, override string) (*Logger, error) {
	return newLogger(cfg, dbPath, override, nil)
}

func newLogger(cfg config.LoggingConfig, dbPath string, consoleOverride string, inputStore *InputLogStore) (*Logger, error) {
	level, err := ParseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	consoleLevel := level
	consoleLevelSet := false
	if trimmed := strings.TrimSpace(consoleOverride); trimmed != "" {
		consoleLevel, err = ParseLevel(trimmed)
		if err != nil {
			return nil, err
		}
		consoleLevelSet = true
	}

	if inputStore == nil {
		inputStore = newInputLogStore(defaultBufferCap)
	}

	consoleOut, consoleErr := defaultConsoleWriters()
	logger := &Logger{
		level:           level,
		consoleLevel:    consoleLevel,
		consoleLevelSet: consoleLevelSet,
		consoleOut:      log.New(consoleOut, "", log.LstdFlags),
		consoleErr:      log.New(consoleErr, "", log.LstdFlags),
		inputLogs:       inputStore,
		bufferCap:       defaultBufferCap,
		subs:            make(map[int]chan Entry),
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

func defaultConsoleWriters() (io.Writer, io.Writer) {
	defaultConsoleMu.RLock()
	defer defaultConsoleMu.RUnlock()
	return defaultConsoleOut, defaultConsoleErr
}

// ResolveEffectiveLevel applies runOverride first, then the debug fallback,
// then the configured level.
func ResolveEffectiveLevel(configured string, runOverride string, debug bool) string {
	if trimmed := strings.TrimSpace(runOverride); trimmed != "" {
		return trimmed
	}
	if debug {
		return LevelDebug.String()
	}
	return strings.TrimSpace(configured)
}

// Close releases file logging resources. It is safe on a nil logger and is a
// no-op when file logging is disabled.
func (l *Logger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	if err := l.closer.Close(); err != nil {
		return fmt.Errorf("close logger: %w", err)
	}
	return nil
}

// SetConsoleOutput replaces the console writers used for stdout and stderr
// logging. Nil writers leave the corresponding output unchanged.
func (l *Logger) SetConsoleOutput(stdout io.Writer, stderr io.Writer) {
	if l == nil {
		return
	}
	if stdout != nil {
		l.consoleOut.SetOutput(stdout)
	}
	if stderr != nil {
		l.consoleErr.SetOutput(stderr)
	}
}

// ConsoleEnabled reports whether console output admits entries at level.
func (l *Logger) ConsoleEnabled(level Level) bool {
	if l == nil {
		return false
	}
	consoleLevel := l.level
	if l.consoleLevelSet {
		consoleLevel = l.consoleLevel
	}
	return level <= consoleLevel
}

// SetDefaultConsoleOutput replaces the console writers used by new loggers and
// returns a restore function. Nil writers leave the corresponding output
// unchanged. A writer implementing WriteConsoleLog(string, string) receives the
// uppercase level label and sanitized message without console formatting; its
// implementation must support concurrent calls from logging goroutines.
func SetDefaultConsoleOutput(stdout io.Writer, stderr io.Writer) func() {
	defaultConsoleMu.Lock()
	previousOut := defaultConsoleOut
	previousErr := defaultConsoleErr
	if stdout != nil {
		defaultConsoleOut = stdout
	}
	if stderr != nil {
		defaultConsoleErr = stderr
	}
	defaultConsoleMu.Unlock()

	return func() {
		defaultConsoleMu.Lock()
		defaultConsoleOut = previousOut
		defaultConsoleErr = previousErr
		defaultConsoleMu.Unlock()
	}
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
	if l == nil {
		return
	}
	applicationEnabled, consoleLevel, enabled := l.enabledLevels(l.level, level)
	if !enabled {
		return
	}
	formatted := SanitizeMessage(fmt.Sprintf(format, args...))
	l.writeSanitized(level, label, formatted, nil, applicationEnabled, consoleLevel)
}

func (l *Logger) writefAtLevel(applicationLevel Level, level Level, label string, capture *inputCapture, format string, args ...any) {
	if l == nil {
		return
	}
	applicationEnabled, consoleLevel, enabled := l.enabledLevels(applicationLevel, level)
	if !enabled && capture.isClosed() {
		return
	}
	l.writeSanitized(level, label, SanitizeMessage(fmt.Sprintf(format, args...)), capture, applicationEnabled, consoleLevel)
}

func (l *Logger) enabledLevels(applicationLevel Level, level Level) (bool, Level, bool) {
	consoleLevel := l.level
	if l.consoleLevelSet {
		consoleLevel = l.consoleLevel
	}
	applicationEnabled := level <= applicationLevel
	return applicationEnabled, consoleLevel, l.file != nil || applicationEnabled || level <= consoleLevel
}

func (l *Logger) writeSanitized(level Level, label, formatted string, capture *inputCapture, applicationEnabled bool, consoleLevel Level) {
	prefix := label + ": "
	if level <= consoleLevel {
		console := l.consoleOut
		if level <= LevelWarn {
			console = l.consoleErr
		}
		if writer, ok := console.Writer().(interface{ WriteConsoleLog(string, string) }); ok {
			writer.WriteConsoleLog(label, formatted)
		} else {
			console.Print(prefix + formatted)
		}
	}
	var at time.Time
	var captured bool
	if capture != nil {
		at, captured = capture.writeRecord(label, formatted)
	}
	if !captured && l.file != nil {
		// Indent multiline continuations so they cannot introduce a record.
		l.file.Print(prefix + diskLogMessage(formatted))
	}
	if applicationEnabled {
		if !captured {
			at = time.Now()
		}
		l.record(label, formatted, at)
	}
}

// SanitizeMessage redacts secrets while preserving ordinary filesystem paths
// for diagnostics. Callers must sanitize private client endpoints separately.
func SanitizeMessage(message string) string {
	return redaction.RedactValue(message, nil)
}

// DBRelativePathLabel returns a slash-normalized .upbrr tmp/cache/log suffix
// when value is inside a known app DB subdirectory. The boolean is false for
// paths outside those subdirectories.
func DBRelativePathLabel(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	normalized := strings.ToLower(strings.ReplaceAll(trimmed, "\\", "/"))
	original := strings.ReplaceAll(trimmed, "\\", "/")
	for _, marker := range []string{".upbrr/tmp/", ".upbrr/cache/", ".upbrr/logs/"} {
		if strings.HasPrefix(normalized, marker) {
			return original, true
		}
		if index := strings.Index(normalized, "/"+marker); index >= 0 {
			return original[index+1:], true
		}
	}
	return "", false
}

// InputLogStore returns the bounded in-memory input log store backing l, or nil
// when l is nil.
func (l *Logger) InputLogStore() *InputLogStore {
	if l == nil {
		return nil
	}
	return l.inputLogs
}

func (l *Logger) recordInputLog(entry Entry) {
	if l == nil || l.inputLogs == nil {
		return
	}
	l.inputLogs.record(entry)
}

func (l *Logger) record(label, message string, at time.Time) {
	if l == nil {
		return
	}

	entry := Entry{
		Time:    at,
		Level:   strings.ToLower(label),
		Message: message,
	}

	l.mu.Lock()
	defer l.mu.Unlock()
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
}

func (l *Logger) recentInputLogs() []Entry {
	if l == nil || l.inputLogs == nil {
		return nil
	}
	return l.inputLogs.recent()
}

// Recent returns a copied, oldest-to-newest snapshot of the most recent entries.
// A non-positive or oversized limit returns the complete retained buffer.
func (l *Logger) Recent(limit int) []Entry {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if limit <= 0 || limit > len(l.buffer) {
		limit = len(l.buffer)
	}
	start := max(len(l.buffer)-limit, 0)
	entries := make([]Entry, limit)
	copy(entries, l.buffer[start:])
	return entries
}

// Subscribe registers a future-entry stream and returns its unsubscribe ID.
// A non-positive buffer uses the package default. Delivery is non-blocking, so
// entries are dropped for a subscriber whose channel is full.
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

// Unsubscribe removes a subscription and closes its channel. Unknown IDs and a
// nil logger are ignored.
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

// LogPath returns the app log path, creating the database's logs subdirectory
// when necessary.
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
	writer := &rotatingWriter{
		path:     path,
		maxBytes: maxBytes,
		maxFiles: maxFiles,
	}
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
