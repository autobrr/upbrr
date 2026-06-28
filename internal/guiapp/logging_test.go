// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
)

func TestRebindLogStreamsStopsOldSubscriptionWithoutStoppingNewStream(t *testing.T) {
	oldLogger := newGUIAppLogStreamTestLogger(t)
	newLogger := newGUIAppLogStreamTestLogger(t)
	oldEmitStarted := make(chan struct{})
	releaseOldEmit := make(chan struct{})
	newEntryReceived := make(chan struct{}, 1)

	previousEmitter := emitLogStreamEvent
	t.Cleanup(func() {
		emitLogStreamEvent = previousEmitter
	})
	emitLogStreamEvent = func(_ context.Context, _ string, data ...any) {
		entry, ok := data[0].(logging.Entry)
		if !ok {
			return
		}
		switch entry.Message {
		case "old":
			close(oldEmitStarted)
			<-releaseOldEmit
		case "new":
			newEntryReceived <- struct{}{}
		}
	}

	app := &App{
		logger:  oldLogger,
		streams: make(map[string]*logStreamSession),
	}
	session := &logStreamSession{
		id:        "stream",
		eventName: logStreamEventPrefix + "stream",
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	app.streams[session.id] = session
	app.startStreamLocked(context.Background(), session)
	oldStop := session.stop
	oldDone := session.done

	oldLogger.Errorf("old")
	waitForLogStreamTestSignal(t, oldEmitStarted, "old stream emit")

	rebindDone := make(chan struct{})
	go func() {
		app.replaceRuntime(config.Config{}, nil, newLogger)
		app.rebindLogStreams(context.Background(), oldLogger, newLogger)
		close(rebindDone)
	}()

	waitForLogStreamTestSignal(t, oldStop, "old stream stop signal")
	assertLogStreamBlocked(t, rebindDone, "rebind before old stream stop")
	if got := loggerSubscriberCount(newLogger); got != 0 {
		t.Fatalf("new logger subscribers before old stop: got %d want 0", got)
	}

	close(releaseOldEmit)

	waitForLogStreamTestSignal(t, oldDone, "old stream stop")
	waitForLogStreamTestSignal(t, rebindDone, "rebind completion")
	newDone := session.done
	if got := loggerSubscriberCount(oldLogger); got != 0 {
		t.Fatalf("old logger subscribers: got %d want 0", got)
	}
	if got := loggerSubscriberCount(newLogger); got != 1 {
		t.Fatalf("new logger subscribers after old stop: got %d want 1", got)
	}
	assertLogStreamStillRunning(t, newDone, "new stream after old stop")

	newLogger.Errorf("new")
	waitForLogStreamTestSignal(t, newEntryReceived, "new stream entry")

	if err := app.StopLogStream(session.id); err != nil {
		t.Fatalf("stop log stream: %v", err)
	}
	waitForLogStreamTestSignal(t, newDone, "new stream stop")
	if got := loggerSubscriberCount(newLogger); got != 0 {
		t.Fatalf("new logger subscribers after explicit stop: got %d want 0", got)
	}
}

func TestRebindLogStreamsSameLoggerDoesNotRestartStream(t *testing.T) {
	logger := newGUIAppLogStreamTestLogger(t)
	app := &App{
		logger:  logger,
		streams: make(map[string]*logStreamSession),
	}
	session := &logStreamSession{
		id:        "stream",
		eventName: logStreamEventPrefix + "stream",
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	app.streams[session.id] = session
	app.startStreamLocked(context.Background(), session)
	stop := session.stop
	done := session.done

	app.rebindLogStreams(context.Background(), logger, logger)

	if session.stop != stop {
		t.Fatal("same-logger rebind replaced stop channel")
	}
	if session.done != done {
		t.Fatal("same-logger rebind replaced done channel")
	}
	if got := loggerSubscriberCount(logger); got != 1 {
		t.Fatalf("logger subscribers after same-logger rebind: got %d want 1", got)
	}
	assertLogStreamStillRunning(t, done, "same-logger stream")
}

func TestRebindLogStreamsNilLoggerDoesNotBlockNextRebind(t *testing.T) {
	oldLogger := newGUIAppLogStreamTestLogger(t)
	newLogger := newGUIAppLogStreamTestLogger(t)
	app := &App{
		logger:  oldLogger,
		streams: make(map[string]*logStreamSession),
	}
	session := &logStreamSession{
		id:        "stream",
		eventName: logStreamEventPrefix + "stream",
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	app.streams[session.id] = session
	app.startStreamLocked(context.Background(), session)

	app.replaceRuntime(config.Config{}, nil, nil)
	app.rebindLogStreams(context.Background(), oldLogger, nil)
	waitForLogStreamTestSignal(t, session.done, "nil logger rebind done")

	rebindDone := make(chan struct{})
	go func() {
		app.replaceRuntime(config.Config{}, nil, newLogger)
		app.rebindLogStreams(context.Background(), nil, newLogger)
		close(rebindDone)
	}()
	waitForLogStreamTestSignal(t, rebindDone, "next logger rebind")
	if got := loggerSubscriberCount(newLogger); got != 1 {
		t.Fatalf("new logger subscribers after nil rebind recovery: got %d want 1", got)
	}
}

func newGUIAppLogStreamTestLogger(t *testing.T) *logging.Logger {
	t.Helper()

	logger, err := logging.New(config.LoggingConfig{Level: "error"}, "")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	t.Cleanup(func() {
		_ = logger.Close()
	})
	return logger
}

func loggerSubscriberCount(logger *logging.Logger) int {
	return reflect.ValueOf(logger).Elem().FieldByName("subs").Len()
}

func waitForLogStreamTestSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func assertLogStreamStillRunning(t *testing.T, done <-chan struct{}, name string) {
	t.Helper()

	select {
	case <-done:
		t.Fatalf("%s stopped unexpectedly", name)
	default:
	}
}

func assertLogStreamBlocked(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()

	select {
	case <-ch:
		t.Fatalf("%s completed unexpectedly", name)
	case <-time.After(50 * time.Millisecond):
	}
}
