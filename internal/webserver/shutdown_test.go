// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestStopAllDupeJobsWaitsForWorkers(t *testing.T) {
	backend := &Backend{
		dupes: make(map[string]*dupeCheckJob),
	}

	released := make(chan struct{})
	finished := make(chan struct{})
	backend.dupeWG.Add(1)
	backend.dupes["job-1"] = &dupeCheckJob{
		id: "job-1",
		cancel: func() {
			go func() {
				<-released
				backend.dupeWG.Done()
				close(finished)
			}()
		},
	}

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

	close(released)

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllDupeJobs to return after workers finish")
	}

	<-finished
}

func TestStopAllUploadJobsWaitsForWorkers(t *testing.T) {
	backend := &Backend{
		uploads: make(map[string]*trackerUploadJob),
	}

	released := make(chan struct{})
	finished := make(chan struct{})
	backend.uploadWG.Add(1)
	backend.uploads["job-1"] = &trackerUploadJob{
		id: "job-1",
		cancel: func() {
			go func() {
				<-released
				backend.uploadWG.Done()
				close(finished)
			}()
		},
	}

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

	close(released)

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected stopAllUploadJobs to return after workers finish")
	}

	<-finished
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

	_ = backend.StopLogStream("stream-3")
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
	done := make(chan error, 1)
	go func() {
		done <- server.serve(ctx, listener)
	}()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"http://"+listener.Addr().String()+"/api/events",
		nil,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
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
