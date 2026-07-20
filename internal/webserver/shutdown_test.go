// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sharedjobs "github.com/autobrr/upbrr/internal/webserver/jobs"
)

func TestStopAllDupeJobsWaitsForWorkers(t *testing.T) {
	backend := &Backend{jobEngine: sharedjobs.New(nil, sharedjobs.Config{})}
	released := make(chan struct{})
	canceled := make(chan struct{}, 1)
	started := make(chan struct{}, 1)
	owner, err := backend.ensureJobOwner("session-a")
	if err != nil {
		t.Fatalf("ensure owner: %v", err)
	}
	_, err = backend.jobEngine.StartDupe(context.Background(), owner, sharedjobs.DupeSpec{
		CorrelationID: "shutdown-dupe",
		Snapshot: webTestDupeSnapshot("Example.Release.2026.1080p-GRP"),
		Runner: blockingDupeRunner{
			started:  started,
			canceled: canceled,
			release:  released,
		},
	})
	if err != nil {
		t.Fatalf("start dupe: %v", err)
	}
	<-started

	done := make(chan struct{})
	go func() {
		backend.stopAllDupeJobs()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("expected stopAllDupeJobs to wait for active workers")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("expected shutdown to cancel dupe worker")
	}

	close(released)

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllDupeJobs to return after workers finish")
	}
}

func TestStopAllUploadJobsWaitsForWorkers(t *testing.T) {
	backend := &Backend{jobEngine: sharedjobs.New(nil, sharedjobs.Config{})}
	released := make(chan struct{})
	canceled := make(chan struct{}, 1)
	started := make(chan struct{}, 1)
	owner, err := backend.ensureJobOwner("session-a")
	if err != nil {
		t.Fatalf("ensure owner: %v", err)
	}
	_, err = backend.jobEngine.StartUpload(context.Background(), owner, sharedjobs.UploadSpec{
		CorrelationID: "shutdown-upload",
		Snapshot: webTestUploadSnapshot("Example.Release.2026.1080p-GRP"),
		Runner: blockingUploadRunner{
			started:  started,
			canceled: canceled,
			release:  released,
		},
	})
	if err != nil {
		t.Fatalf("start upload: %v", err)
	}
	<-started

	done := make(chan struct{})
	go func() {
		backend.stopAllUploadJobs()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("expected stopAllUploadJobs to wait for active workers")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("expected shutdown to cancel upload worker")
	}

	close(released)

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllUploadJobs to return after workers finish")
	}
}

func TestStopAllLogStreamsWaitsForWorkers(t *testing.T) {
	backend := &Backend{
		streams: make(map[string]*backendLogStream),
	}

	stream := &backendLogStream{
		id:   "stream-1",
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	backend.streams[stream.id] = stream
	backend.streamWG.Add(1)

	released := make(chan struct{})
	go func() {
		defer backend.streamWG.Done()
		<-stream.stop
		<-released
		close(stream.done)
	}()

	done := make(chan struct{})
	go func() {
		backend.stopAllLogStreams()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("expected stopAllLogStreams to wait for active workers")
	case <-time.After(50 * time.Millisecond):
	}

	close(released)

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllLogStreams to return after workers finish")
	}
}

func TestStopSessionLogStreamsStopsMatchingStreams(t *testing.T) {
	backend := &Backend{
		streams: make(map[string]*backendLogStream),
	}

	makeStream := func(id string, sessionID string) *backendLogStream {
		stream := &backendLogStream{
			id:        id,
			sessionID: sessionID,
			stop:      make(chan struct{}),
			done:      make(chan struct{}),
		}
		backend.streamWG.Go(func() {
			<-stream.stop
			close(stream.done)
		})
		return stream
	}

	backend.streams["stream-1"] = makeStream("stream-1", "session-a")
	backend.streams["stream-2"] = makeStream("stream-2", "session-a")
	backend.streams["stream-3"] = makeStream("stream-3", "session-b")

	backend.StopSessionLogStreams("session-a")

	backend.streamMu.Lock()
	_, hasFirst := backend.streams["stream-1"]
	_, hasSecond := backend.streams["stream-2"]
	_, hasThird := backend.streams["stream-3"]
	backend.streamMu.Unlock()

	if hasFirst || hasSecond {
		t.Fatal("expected session log streams to be removed")
	}
	if !hasThird {
		t.Fatal("expected other session log streams to remain")
	}

	_ = backend.StopLogStream("session-a", "stream-3")
	backend.streamMu.Lock()
	_, hasThirdAfterForeignStop := backend.streams["stream-3"]
	backend.streamMu.Unlock()
	if !hasThirdAfterForeignStop {
		t.Fatal("expected foreign session stop to leave log stream running")
	}

	_ = backend.StopLogStream("session-b", "stream-3")
}

func TestHandleEventsStopsSessionLogStreamsAfterLastSubscriberCloses(t *testing.T) {
	backend := &Backend{
		streams: make(map[string]*backendLogStream),
	}
	makeStream := func(id string) *backendLogStream {
		stream := &backendLogStream{
			id:        id,
			sessionID: "session-a",
			stop:      make(chan struct{}),
			done:      make(chan struct{}),
		}
		backend.streams[stream.id] = stream
		backend.streamWG.Go(func() {
			<-stream.stop
			close(stream.done)
		})
		return stream
	}
	firstStream := makeStream("stream-1")
	secondStream := makeStream("stream-2")
	t.Cleanup(func() {
		_ = backend.StopLogStream("session-a", firstStream.id)
		_ = backend.StopLogStream("session-a", secondStream.id)
	})

	hub := newEventHub()
	server := &Server{
		backend:        backend,
		hub:            hub,
		generalLimiter: newFixedWindowLimiter(300, time.Minute),
	}
	current := session{ID: "session-a", CSRFToken: "csrf"}
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	firstReq := httptest.NewRequestWithContext(firstCtx, http.MethodGet, "/api/events", nil)
	firstRecorder := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		server.handleEvents(firstRecorder, firstReq, current)
		close(firstDone)
	}()

	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	secondReq := httptest.NewRequestWithContext(secondCtx, http.MethodGet, "/api/events", nil)
	secondRecorder := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		server.handleEvents(secondRecorder, secondReq, current)
		close(secondDone)
	}()

	waitForEventSubscriberCount(t, hub, current.ID, 2)
	firstCancel()
	select {
	case <-firstDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected first event stream to close after request cancellation")
	}

	for _, stream := range []*backendLogStream{firstStream, secondStream} {
		select {
		case <-stream.done:
			t.Fatal("expected sibling event subscriber to keep session log streams active")
		default:
		}
	}
	backend.streamMu.Lock()
	_, firstOK := backend.streams[firstStream.id]
	_, secondOK := backend.streams[secondStream.id]
	backend.streamMu.Unlock()
	if !firstOK || !secondOK {
		t.Fatal("expected sibling event subscriber to keep session log streams registered")
	}

	secondCancel()
	select {
	case <-secondDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected final event stream to close after request cancellation")
	}

	for _, stream := range []*backendLogStream{firstStream, secondStream} {
		select {
		case <-stream.done:
		case <-time.After(250 * time.Millisecond):
			t.Fatal("expected last event subscriber close to stop session log streams")
		}
	}
	backend.streamMu.Lock()
	_, firstOK = backend.streams[firstStream.id]
	_, secondOK = backend.streams[secondStream.id]
	backend.streamMu.Unlock()
	if firstOK || secondOK {
		t.Fatal("expected last event subscriber close to remove session log streams")
	}
}

func TestHandleEventsReconnectBeforeIdleStopKeepsSessionLogStreamsActive(t *testing.T) {
	backend := &Backend{
		streams: make(map[string]*backendLogStream),
	}
	stream := &backendLogStream{
		id:        "stream-1",
		sessionID: "session-a",
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	backend.streams[stream.id] = stream
	backend.streamWG.Go(func() {
		<-stream.stop
		close(stream.done)
	})
	t.Cleanup(func() {
		_ = backend.StopLogStream("session-a", stream.id)
	})

	hub := newEventHub()
	server := &Server{
		backend:        backend,
		hub:            hub,
		generalLimiter: newFixedWindowLimiter(300, time.Minute),
	}
	current := session{ID: "session-a", CSRFToken: "csrf"}

	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	firstReq := httptest.NewRequestWithContext(firstCtx, http.MethodGet, "/api/events", nil)
	firstRecorder := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		server.handleEvents(firstRecorder, firstReq, current)
		close(firstDone)
	}()
	waitForEventSubscriberCount(t, hub, current.ID, 1)

	firstCancel()
	select {
	case <-firstDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected first event stream to close after request cancellation")
	}

	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	secondReq := httptest.NewRequestWithContext(secondCtx, http.MethodGet, "/api/events", nil)
	secondRecorder := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		server.handleEvents(secondRecorder, secondReq, current)
		close(secondDone)
	}()
	waitForEventSubscriberCount(t, hub, current.ID, 1)

	select {
	case <-stream.done:
		t.Fatal("expected reconnecting event subscriber to keep session log streams active")
	case <-time.After(eventSessionLogStopGracePeriod + 50*time.Millisecond):
	}

	backend.streamMu.Lock()
	_, streamStillRegistered := backend.streams[stream.id]
	backend.streamMu.Unlock()
	if !streamStillRegistered {
		t.Fatal("expected reconnecting event subscriber to keep session log streams registered")
	}

	secondCancel()
	select {
	case <-secondDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected second event stream to close after request cancellation")
	}

	select {
	case <-stream.done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected final event subscriber close to stop session log streams")
	}
}

func TestServeCancelsOpenEventStreamOnContextDone(t *testing.T) {
	hub := newEventHub()
	server := &Server{
		backend: &Backend{
			streams: make(map[string]*backendLogStream),
		},
		hub:            hub,
		generalLimiter: newFixedWindowLimiter(300, time.Minute),
		server: &http.Server{
			ReadHeaderTimeout: 10 * time.Second,
		},
		developmentNoAuth: true,
		developmentSession: session{
			ID:        "dev-no-auth",
			Username:  "dev",
			CSRFToken: "csrf",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", server.requireSession(server.handleEvents))
	server.server.Handler = mux

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- server.serve(ctx, listener)
	}()

	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()
	req, err := http.NewRequestWithContext(
		clientCtx,
		http.MethodGet,
		"http://"+listener.Addr().String()+"/api/events?csrfToken=csrf",
		nil,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	clientDone := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if resp != nil {
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
		}
		clientDone <- err
	}()

	waitForEventSubscriber(t, hub, "dev-no-auth")
	cancel()
	clientCancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected serve to return after context cancellation")
	}

	select {
	case <-clientDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected event stream client to finish after shutdown")
	}
}

func waitForEventSubscriber(t *testing.T, hub *eventHub, sessionID string) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		hub.mu.Lock()
		subscribers := len(hub.subscribers[sessionID])
		hub.mu.Unlock()
		if subscribers > 0 {
			return
		}

		select {
		case <-deadline:
			t.Fatal("expected event stream subscriber")
		case <-ticker.C:
		}
	}
}

func waitForEventSubscriberCount(t *testing.T, hub *eventHub, sessionID string, want int) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		hub.mu.Lock()
		subscribers := len(hub.subscribers[sessionID])
		hub.mu.Unlock()
		if subscribers == want {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("expected %d event stream subscribers, got %d", want, subscribers)
		case <-ticker.C:
		}
	}
}
