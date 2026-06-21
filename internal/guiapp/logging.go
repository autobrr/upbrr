// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/autobrr/upbrr/internal/config"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/logging"
)

const logStreamEventPrefix = "log:stream:"
const logExclusionsSection = "log_exclusions"

var emitLogStreamEvent = runtime.EventsEmit

// LogExclusions stores muted log patterns for the UI.
type LogExclusions struct {
	Patterns []string `json:"patterns"`
}

type logStreamSession struct {
	id        string
	eventName string
	logger    *logging.Logger
	subID     int
	stop      chan struct{}
	done      chan struct{}
}

func (a *App) GetLogPath() (string, error) {
	if a == nil {
		return "", errors.New("app not initialized")
	}
	return wrapGUIResult(logging.LogPath(a.currentConfig().MainSettings.DBPath))
}

func (a *App) GetRecentLogs(limit int) ([]logging.Entry, error) {
	logger := a.currentLogger()
	if logger == nil {
		return nil, errors.New("logger not initialized")
	}
	return logger.Recent(limit), nil
}

func (a *App) StartLogStream() (string, error) {
	if a == nil || a.currentLogger() == nil {
		return "", errors.New("logger not initialized")
	}

	a.streamMu.Lock()
	defer a.streamMu.Unlock()

	streamID := randomLogStreamID()
	session := &logStreamSession{
		id:        streamID,
		eventName: logStreamEventPrefix + streamID,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	a.streams[streamID] = session
	a.startStreamLocked(a.runtimeContext(), session)
	return streamID, nil
}

func (a *App) StopLogStream(streamID string) error {
	if a == nil {
		return errors.New("app not initialized")
	}

	a.streamMu.Lock()
	session := a.streams[streamID]
	if session != nil {
		delete(a.streams, streamID)
		a.stopStreamLocked(session)
	}
	a.streamMu.Unlock()

	return nil
}

func (a *App) GetLogExclusions() ([]string, error) {
	if a == nil {
		return nil, errors.New("app not initialized")
	}
	if a.repo == nil {
		return nil, errors.New("config repository not initialized")
	}

	ctx := a.runtimeContext()

	var exclusions LogExclusions
	if err := config.LoadSectionFromDatabase(ctx, logExclusionsSection, &exclusions, a.repo); err != nil {
		if errors.Is(err, internalerrors.ErrNotFound) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("gui: %w", err)
	}

	return normalizePatterns(exclusions.Patterns), nil
}

func (a *App) UpdateLogExclusions(patterns []string) error {
	if a == nil {
		return errors.New("app not initialized")
	}
	if a.repo == nil {
		return errors.New("config repository not initialized")
	}

	ctx := a.runtimeContext()

	exclusions := LogExclusions{Patterns: normalizePatterns(patterns)}
	if err := config.SaveSectionToDatabase(ctx, logExclusionsSection, exclusions, a.repo); err != nil {
		return fmt.Errorf("gui: %w", err)
	}

	return nil
}

func (a *App) startStreamLocked(ctx context.Context, session *logStreamSession) {
	if session == nil {
		return
	}
	logger := a.currentLogger()
	if logger == nil {
		close(session.done)
		return
	}

	subID, ch := logger.Subscribe(0)
	session.logger = logger
	session.subID = subID

	stop := session.stop
	done := session.done
	eventName := session.eventName

	go func() {
		defer close(done)
		for {
			select {
			case entry, ok := <-ch:
				if !ok {
					return
				}
				emitLogStreamEvent(ctx, eventName, entry)
			case <-stop:
				logger.Unsubscribe(subID)
				return
			}
		}
	}()
}

func (a *App) stopStreamLocked(session *logStreamSession) {
	if session == nil || session.stop == nil {
		return
	}
	select {
	case <-session.stop:
		return
	default:
		close(session.stop)
	}
}

func (a *App) stopAllLogStreams() {
	if a == nil {
		return
	}

	a.streamMu.Lock()
	for _, session := range a.streams {
		a.stopStreamLocked(session)
	}
	a.streams = make(map[string]*logStreamSession)
	a.streamMu.Unlock()
}

// rebindLogStreams moves active UI log streams from oldLogger to newLogger.
// Existing stream goroutines are stopped and waited on before replacement
// subscriptions start, so old subscriptions cannot emit after the rebind.
func (a *App) rebindLogStreams(ctx context.Context, oldLogger *logging.Logger, newLogger *logging.Logger) {
	if a == nil {
		return
	}
	if oldLogger == newLogger {
		return
	}

	type stoppedStream struct {
		session *logStreamSession
		done    <-chan struct{}
	}

	a.streamMu.Lock()
	stopped := make([]stoppedStream, 0, len(a.streams))
	for _, session := range a.streams {
		if session == nil {
			continue
		}
		stopped = append(stopped, stoppedStream{
			session: session,
			done:    session.done,
		})
		a.stopStreamLocked(session)
	}
	a.streamMu.Unlock()

	for _, stream := range stopped {
		if stream.done != nil {
			<-stream.done
		}
	}

	a.streamMu.Lock()
	for _, stream := range stopped {
		session := stream.session
		if session == nil || a.streams[session.id] != session {
			continue
		}
		session.stop = make(chan struct{})
		session.done = make(chan struct{})
		a.startStreamLocked(ctx, session)
	}
	a.streamMu.Unlock()
}

func normalizePatterns(patterns []string) []string {
	seen := make(map[string]struct{})
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func randomLogStreamID() string {
	value, err := rand.Int(rand.Reader, new(big.Int).SetUint64(^uint64(0)))
	if err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), value.Uint64())
}
